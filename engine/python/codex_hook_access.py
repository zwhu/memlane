"""Official host hook inspection and narrowly reviewed trust writes; no sessions."""
import asyncio
import hashlib
import json
import os
from pathlib import Path

from project_policy import PolicyError, mark_connection_mutation


class CodexHookAccess:
    def __init__(self, executable, profile):
        self.executable, self.profile = str(executable), str(Path(profile).expanduser().resolve())
        self.process = None
        self.sequence = 0

    async def __aenter__(self):
        env = {key: value for key, value in os.environ.items()
               if not key.startswith(('COGNEE_', 'MEMLANE_'))}
        env['CODEX_HOME'] = self.profile
        self.process = await asyncio.create_subprocess_exec(self.executable, 'app-server', '--stdio',
            stdin=asyncio.subprocess.PIPE, stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.DEVNULL, env=env, limit=2 * 1024 * 1024)
        try:
            await self.request('initialize', {'clientInfo': {'name': 'memlane-capture-access', 'version': '1'},
                                             'capabilities': {'experimentalApi': True}})
            self.process.stdin.write(b'{"method":"initialized"}\n')
            await self.process.stdin.drain()
        except BaseException:
            await self.__aexit__(None, None, None)
            raise
        return self

    async def __aexit__(self, *_):
        if self.process and self.process.returncode is None:
            try:
                self.process.terminate()
            except ProcessLookupError:
                pass
            try:
                await asyncio.wait_for(self.process.wait(), 1)
            except asyncio.TimeoutError:
                self.process.kill()
                await self.process.wait()

    async def request(self, method, params):
        self.sequence += 1
        identifier = self.sequence
        self.process.stdin.write(json.dumps({'id': identifier, 'method': method, 'params': params}).encode() + b'\n')
        await self.process.stdin.drain()
        async def response():
            total = 0
            while True:
                line = await self.process.stdout.readline()
                total += len(line)
                if not line or total > 2 * 1024 * 1024:
                    raise PolicyError('capture_host_reply_unavailable', 503)
                value = json.loads(line)
                if value.get('id') != identifier:
                    continue
                if 'error' in value:
                    raise PolicyError('capture_host_request_rejected', 409)
                return value['result']
        return await asyncio.wait_for(response(), 4)

    async def inspect(self, installation):
        from project_codex_connection import verify_cache
        plugin = 'cognee@' + installation['marketplace_name']
        result = await self.request('hooks/list', {'cwds': [self.profile]})
        entries = result.get('data', [])
        if not entries or any(row.get('errors') for row in entries):
            raise PolicyError('capture_hooks_unavailable', 503)
        hooks = [h for row in entries for h in row.get('hooks', []) if h.get('pluginId') == plugin]
        if not hooks or len({h['key'] for h in hooks}) != len(hooks):
            raise PolicyError('capture_hooks_unavailable', 503)
        for hook in hooks:
            path = Path(hook['sourcePath']).resolve()
            expected = Path(self.profile) / 'plugins/cache' / installation['marketplace_name'] / 'cognee'
            if expected not in path.parents or path.name != 'hooks.json':
                raise PolicyError('capture_hooks_source_changed', 409)
            verify_cache(path.parent, installation)
            if hook.get('isManaged') or hook.get('source') != 'plugin':
                raise PolicyError('capture_hooks_source_changed', 409)
            parts = hook.get('currentHash', '').split('/')
            if len(parts) != 2 or any(not p.startswith('sha256:') or len(p) != 71 for p in parts):
                raise PolicyError('capture_hook_hash_unavailable', 503)
        signature = hashlib.sha256(json.dumps(sorted((h['key'], h['currentHash']) for h in hooks)).encode()).hexdigest()
        trusted = all(h['trustStatus'] == 'trusted' and h['enabled'] for h in hooks)
        return {'permission': 'granted' if trusted else 'required', 'review_sha': signature, 'hooks': hooks}

    async def allow(self, installation, reviewed_sha):
        before = await self.request('config/read', {'includeLayers': True})
        inspected = await self.inspect(installation)
        if not reviewed_sha or inspected['review_sha'] != reviewed_sha:
            raise PolicyError('capture_review_changed', 409)
        layers = [row for row in before.get('layers', []) if row['name'].get('type') == 'user']
        if len(layers) != 1:
            raise PolicyError('capture_config_version_unavailable', 409)
        edits = []
        for hook in inspected['hooks']:
            command_hash, content_hash = hook['currentHash'].split('/')
            edits.append({'keyPath': 'hooks.state.' + json.dumps(hook['key']), 'mergeStrategy': 'upsert',
                'value': {'trusted_hash': command_hash, 'trusted_content_hash': content_hash, 'enabled': True}})
        mark_connection_mutation()
        await self.request('config/batchWrite', {'edits': edits, 'filePath': str(Path(self.profile) / 'config.toml'),
            'expectedVersion': layers[0]['version'], 'reloadUserConfig': True})
        after = await self.inspect(installation)
        if after['permission'] != 'granted':
            raise PolicyError('capture_allow_result_unconfirmed', 503)
        return after
