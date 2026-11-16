"""Agent-owned portraits: one automatic attempt, explicit manual fallback.

Bodies live in the existing official Cognee store. The private journal contains
only operation state and document references; unsaved output is a pending file.
"""
import asyncio
import contextlib
import copy
import hashlib
import json
import os
from pathlib import Path
import signal
import tempfile
import time
import uuid

import aiohttp
from aiohttp import web

from project_control import private_write, read_secret
from project_policy import PolicyError

PROMPT_PATH = Path(__file__).resolve().parents[1] / 'prompts/user-profile-export.md'


def profile_prompt():
    return PROMPT_PATH.read_text().strip()


PROMPT = profile_prompt()

MANUAL_SOURCES = {'manual:chatgpt': 'ChatGPT', 'manual:claude': 'Claude',
                  'manual:gemini': 'Gemini', 'manual:grok': 'Grok', 'manual:deepseek': 'DeepSeek'}
NAMES = {'codex': 'Codex', 'pi': 'pi', 'traex': 'TraeX', 'claude_code': 'Claude Code',
         'cursor': 'Cursor', 'opencode': 'OpenCode'}
AUTO_KINDS = {'codex', 'pi', 'traex', 'claude_code'}
LIMIT = 256 * 1024


def valid_body(body):
    if not isinstance(body, str) or not body.strip() or '\x00' in body or len(body.encode()) > LIMIT:
        raise PolicyError('invalid_profile_document')
    return body.strip()


class ProfileDocuments:
    def __init__(self, official):
        self.official = official

    async def dataset(self):
        return (await self.official.owned_dataset('ml-user-profiles'))['id']

    async def find(self, dataset, digest, operation):
        rows = await self.official.request('GET', f'/api/v1/datasets/{dataset}/data?content_hash={digest}&limit=100')
        matches = [r for r in rows if (r.get('externalMetadata') or {}).get('profile_operation') == operation
                   and r.get('datasetId') == dataset and r.get('contentHash') == digest]
        if len(matches) != 1:
            raise PolicyError('profile_save_unconfirmed', 503)
        return str(uuid.UUID(matches[0]['id']))

    async def save(self, dataset, body, row):
        form = aiohttp.FormData()
        for key, value in {'raw_data': body, 'datasetId': dataset, 'run_in_background': 'false',
                           'external_metadata': json.dumps([{'memlane_kind': 'user_profile',
                               'profile_source': row['instance_id'], 'profile_operation': row['operation_id'],
                               'profile_method': row['method'], 'generated_at': row['started_at']}])}.items():
            form.add_field(key, value)
        official = self.official
        async with official.session.post(official.url + '/api/v1/add', data=form,
                headers={'X-Api-Key': read_secret(official.token_file)}, allow_redirects=False,
                timeout=aiohttp.ClientTimeout(total=90)) as response:
            # The ingestion receipt alone does not prove the document identity.
            if response.status != 200:
                raise PolicyError('profile_save_unconfirmed', 503)
            await response.content.read(64 * 1024)
        digest = hashlib.md5(body.encode(), usedforsecurity=False).hexdigest()
        return await self.find(dataset, digest, row['operation_id'])

    async def read(self, dataset, document):
        official = self.official
        async with official.session.get(official.url + f'/api/v1/datasets/{dataset}/data/{document}/raw',
                headers={'X-Api-Key': read_secret(official.token_file)}, allow_redirects=False,
                timeout=aiohttp.ClientTimeout(total=15)) as response:
            if response.status != 200:
                raise PolicyError('profile_document_unavailable', 503)
            body = bytearray()
            async for chunk in response.content.iter_chunked(16384):
                body.extend(chunk)
                if len(body) > LIMIT + 4096:
                    raise PolicyError('profile_document_too_large', 503)
            text = bytes(body).decode('utf-8')
            if text.startswith('<!-- memlane-user-profile '):
                _, separator, rest = text.partition('-->\n\n')
                if separator:
                    text = rest
            return text


def command_for(adapter, row, work):
    """Fixed local CLI contracts, never a request-supplied executable/prompt."""
    kind = row['kind']
    executable = str(getattr(adapter, 'executable', '') or getattr(adapter, 'config', {}).get('codex_executable', ''))
    if kind not in AUTO_KINDS or not Path(executable).is_file():
        raise PolicyError('profile_automatic_unavailable', 409)
    env = dict(os.environ, MEMLANE_PROCESSING_SESSION='1')
    # The generator must not inherit the engine's bridge/provider configuration.
    for key in list(env):
        if key.startswith(('COGNEE_', 'MEMLANE_PROJECT_')):
            env.pop(key, None)
    profile = row['profile']
    prompt = profile_prompt()
    output = work / 'answer.md'
    if kind in ('codex', 'traex'):
        env['CODEX_HOME' if kind == 'codex' else 'TRAE_HOME'] = profile
        if kind == 'traex':
            env['TRAECLI_HOME'] = profile
        args = [executable]
        if kind == 'traex':
            args += ['--no-daemon', '-a', 'never']
        args += ['exec', '--ephemeral', '--skip-git-repo-check', '-s', 'read-only',
                 '-c', 'features.hooks=false', '-o', str(output), prompt]
        # Disable MCP calls, including access to the shared Memlane store, while
        # retaining this host's model and native memory configuration.
        import tomlkit
        config = Path(profile) / ('config.toml' if kind == 'codex' else 'traecli.toml')
        parsed = tomlkit.parse(config.read_text()) if config.is_file() else {}
        for name in parsed.get('mcp_servers', {}):
            args[-1:-1] = ['-c', f'mcp_servers.{json.dumps(name)}.enabled=false']
        return args, env, output
    if kind == 'pi':
        env['PI_CODING_AGENT_DIR'] = profile
        return [executable, '--print', '--no-session', '--no-extensions', '--no-skills',
                '--tools', 'read,grep,find,ls', prompt], env, None
    env['CLAUDE_CONFIG_DIR'] = profile
    env.pop('CLAUDECODE', None)
    return [executable, '--print', '--no-session-persistence', '--output-format', 'json',
            '--permission-mode', 'dontAsk', '--tools', 'Read,Glob,Grep', '--strict-mcp-config',
            '--mcp-config', '{"mcpServers":{}}', '--settings', '{"disableAllHooks":true}', prompt], env, None


async def generate(adapter, row):
    with tempfile.TemporaryDirectory(prefix='memlane-user-profile-') as directory:
        work = Path(directory)
        args, env, output = command_for(adapter, row, work)
        process = await asyncio.create_subprocess_exec(*args, cwd=work, env=env,
            stdin=asyncio.subprocess.DEVNULL, stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.DEVNULL, start_new_session=True)
        async def collect():
            data = bytearray()
            while chunk := await process.stdout.read(16384):
                data.extend(chunk)
                if len(data) > LIMIT:
                    raise PolicyError('profile_output_too_large', 503)
            await process.wait()
            if process.returncode:
                raise PolicyError('profile_agent_failed', 503)
            if output is not None:
                if not output.is_file() or output.stat().st_size > LIMIT:
                    raise PolicyError('profile_output_unconfirmed', 503)
                return valid_body(output.read_text())
            text = data.decode('utf-8')
            if row['kind'] == 'claude_code':
                result = json.loads(text)
                if result.get('is_error') or result.get('subtype') != 'success':
                    raise PolicyError('profile_agent_failed', 503)
                text = result.get('result')
            return valid_body(text)
        try:
            return await asyncio.wait_for(collect(), 180)
        finally:
            # Own the process group, including a child still holding stdout.
            with contextlib.suppress(ProcessLookupError):
                os.killpg(process.pid, signal.SIGKILL)
            await process.wait()


class UserProfiles:
    def __init__(self, registry, connections, documents=None, runner=generate):
        self.registry, self.connections = registry, connections
        self.documents, self.runner = documents or ProfileDocuments(registry.official), runner
        self.path = registry.path.parent / 'user-profiles.json'
        self.state = json.loads(self.path.read_text()) if self.path.exists() else {'profiles': {}}
        self.tasks = {}
        self.lock = asyncio.Lock()
        self.current = {}
        for row in self.state['profiles'].values():
            if row['status'] in ('queued', 'generating', 'saving'):
                row.update(status='unknown', error_code='profile_interrupted')
        self.persist()

    def persist(self):
        private_write(self.path, json.dumps(self.state).encode())

    def sources(self):
        rows = self.connections.view()['connections'] if self.connections else []
        rows = [r for r in rows if r.get('instance_id') not in self.registry.state.get('agent_retirements', {})]
        for identifier, name in MANUAL_SOURCES.items():
            rows.append({'instance_id': identifier, 'kind': 'manual', 'name': name, 'status': 'manual'})
        self.current = {r['instance_id']: r for r in rows}
        return rows

    @staticmethod
    def eligible(source):
        return (source.get('kind') in AUTO_KINDS and source.get('status') == 'configured_pending_new_session'
                and source.get('new_sessions', 0) > 0 and not source.get('environment_issue')
                and source.get('environment_verified') is True)

    def view(self):
        result = []
        for source in self.sources():
            identifier = source['instance_id']
            stored = copy.deepcopy(self.state['profiles'].get(identifier, {}))
            stored.pop('pending_file', None)
            stored.pop('content_hash', None)
            result.append({**stored, 'instance_id': identifier, 'kind': source['kind'],
                'name': source.get('name') or NAMES.get(source['kind'], source['kind']),
                'automatic': self.eligible(source), 'status': stored.get('status', 'manual'),
                'connection_status': source['status'],
                'save_unconfirmed': stored.get('status') == 'unknown' and bool(self.state['profiles'].get(identifier, {}).get('pending_file'))})
        return {'profiles': result, 'prompt': profile_prompt()}

    def source(self, identifier):
        self.sources()
        if identifier not in self.current:
            raise PolicyError('profile_source_unavailable', 404)
        return self.current[identifier]

    async def start(self, identifier, operation, *, body=None, automatic=False):
        if not isinstance(operation, str) or not 8 <= len(operation) <= 128:
            raise PolicyError('invalid_profile_operation')
        if body is not None:
            body = valid_body(body)
        async with self.lock:
            source = self.source(identifier)
            old = self.state['profiles'].get(identifier, {})
            if operation == old.get('operation_id'):
                return self.view()
            if old.get('status') in ('queued', 'generating', 'saving') or (old.get('status') == 'unknown' and
                    (body is None or old.get('pending_file'))):
                raise PolicyError('profile_operation_requires_review', 409)
            if body is None and not self.eligible(source):
                raise PolicyError('profile_automatic_unavailable', 409)
            if automatic and old:
                return self.view()
            row = {'instance_id': identifier, 'kind': source['kind'], 'operation_id': operation,
                'method': 'manual' if body is not None else 'automatic', 'status': 'queued',
                'started_at': time.time(), 'previous': old.get('document') or old.get('previous')}
            # Preserve the currently readable version while refreshing.
            if old.get('document'):
                row['document'] = old['document']
                row['updated_at'] = old.get('updated_at')
            self.state['profiles'][identifier] = row
            self.persist()
            adapter = self.connections.adapters.get(identifier) if self.connections else None
            task = asyncio.create_task(self.run(row, source, adapter, body))
            self.tasks[identifier] = task
            def finished(done):
                if self.tasks.get(identifier) is done:
                    self.tasks.pop(identifier, None)
            task.add_done_callback(finished)
            return self.view()

    async def run(self, row, source, adapter, body):
        try:
            # A single worker across agents prevents competing model launches.
            async with self.gate:
                if body is None:
                    row['status'] = 'generating'
                    self.persist()
                    body = await self.runner(adapter, source)
                # Verify the same instance is still usable before publishing.
                current = self.source(row['instance_id'])
                if row['method'] == 'automatic' and not self.eligible(current):
                    raise PolicyError('profile_source_changed', 409)
                pending = self.path.parent / 'profile-pending' / (hashlib.sha256(row['operation_id'].encode()).hexdigest() + '.md')
                # Official ingestion deduplicates identical bytes in a dataset.
                # A source/operation marker keeps independent portraits distinct.
                body = '<!-- memlane-user-profile ' + json.dumps({'source': row['instance_id'],
                    'operation': row['operation_id']}, ensure_ascii=True) + ' -->\n\n' + valid_body(body)
                private_write(pending, body.encode())
                row.update(status='saving', pending_file=str(pending),
                           content_hash=hashlib.md5(body.encode(), usedforsecurity=False).hexdigest())
                self.persist()
                dataset = await self.documents.dataset()
                row.update(dataset_id=dataset, status='saving')
                self.persist()
                document = await self.documents.save(dataset, body, row)
                row.update(document={'dataset_id': dataset, 'data_id': document}, status='ready', updated_at=time.time())
                row.pop('error_code', None)
                self.persist()
                pending.unlink(missing_ok=True)
                row.pop('pending_file', None)
                self.persist()
        except asyncio.CancelledError:
            row.update(status='unknown', error_code='profile_interrupted')
            self.persist()
            raise
        except Exception as error:
            row.update(status='unknown' if row.get('status') == 'saving' else 'failed',
                       error_code=error.code if isinstance(error, PolicyError) else 'profile_result_unconfirmed')
            self.persist()

    async def read(self, identifier):
        self.source(identifier)
        document = self.state['profiles'].get(identifier, {}).get('document')
        if not document:
            raise PolicyError('profile_document_unavailable', 404)
        return {'body': await self.documents.read(document['dataset_id'], document['data_id']), 'document': document}

    async def reconcile(self, identifier):
        self.source(identifier)
        row = self.state['profiles'].get(identifier, {})
        if row.get('status') != 'unknown' or not row.get('content_hash'):
            raise PolicyError('profile_operation_requires_review', 409)
        if not row.get('dataset_id'):
            raise PolicyError('profile_dataset_unconfirmed', 409)
        document = await self.documents.find(row['dataset_id'], row['content_hash'], row['operation_id'])
        row.update(document={'dataset_id': row['dataset_id'], 'data_id': document}, status='ready', updated_at=time.time())
        row.pop('error_code', None)
        self.persist()
        if row.get('pending_file'):
            Path(row['pending_file']).unlink(missing_ok=True)
            row.pop('pending_file', None)
            self.persist()
        return self.view()

    async def scan(self):
        for source in self.sources():
            if self.eligible(source) and source['instance_id'] not in self.state['profiles']:
                await self.start(source['instance_id'], uuid.uuid4().hex, automatic=True)

    async def background(self):
        while True:
            try:
                await self.scan()
            except (PolicyError, OSError, ValueError, TypeError):
                pass
            await asyncio.sleep(15)

    async def close(self):
        tasks = list(self.tasks.values())
        for task in tasks:
            task.cancel()
        await asyncio.gather(*tasks, return_exceptions=True)

    @property
    def gate(self):
        if not hasattr(self, '_gate'):
            self._gate = asyncio.Semaphore(1)
        return self._gate


def attach_user_profiles(app, registry, connections):
    profiles = UserProfiles(registry, connections)
    async def handler(request):
        if not request['manager']:
            raise PolicyError('management_required', 403)
        identifier = request.match_info.get('id')
        if request.method == 'GET':
            return web.json_response(await profiles.read(identifier) if identifier else profiles.view())
        payload = await request.json()
        action = request.match_info['action']
        if action == 'reconcile':
            if payload != {}:
                raise PolicyError('invalid_request_fields')
            return web.json_response(await profiles.reconcile(identifier))
        expected = {'operation_id', 'body'} if action == 'import' else {'operation_id'}
        if not isinstance(payload, dict) or set(payload) != expected:
            raise PolicyError('invalid_request_fields')
        if action == 'import':
            valid_body(payload['body'])
        return web.json_response(await profiles.start(identifier, payload['operation_id'], body=payload.get('body')))
    app.add_routes([web.get('/v1/user-profiles', handler), web.get('/v1/user-profiles/{id}/document', handler),
                    web.post('/v1/user-profiles/{id}/{action:generate|import|reconcile}', handler)])
    async def startup(_):
        app['user_profiles_worker'] = asyncio.create_task(profiles.background())
    async def cleanup(_):
        app['user_profiles_worker'].cancel()
        await asyncio.gather(app['user_profiles_worker'], return_exceptions=True)
        await profiles.close()
    app.on_startup.append(startup)
    app.on_cleanup.append(cleanup)
    return profiles
