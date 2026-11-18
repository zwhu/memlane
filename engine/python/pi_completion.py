"""Isolated pi RPC model requests; pi alone reads its existing authentication."""
import asyncio
import hashlib
import json
import os
from pathlib import Path

from jsonschema import validators

from codex_completion_endpoint import (CompletionError, MAX_BODY, collect_stderr,
                                       completion_workspace, default_request_args, terminate, track_process)

EXTENSION = Path(__file__).resolve().parents[1] / 'prompts/pi-schema-output.ts'


def checked_models(source):
    if not source.exists():
        return None
    if source.is_symlink() or not source.is_file() or source.stat().st_size > 1024 * 1024:
        raise CompletionError('pi_provider_configuration_invalid', 409)
    raw = source.read_bytes()
    value = json.loads(raw)

    def command(node):
        if isinstance(node, str):
            return node.startswith('!')
        if isinstance(node, dict):
            return any(command(v) for v in node.values())
        return isinstance(node, list) and any(command(v) for v in node)
    if command(value):
        raise CompletionError('pi_command_credential_unsupported', 409)
    return raw


class PiRPC:
    def __init__(self, process):
        self.process, self.count, self.received = process, 0, 0

    async def send(self, command, **fields):
        self.count += 1
        identity = 'memlane-' + str(self.count)
        self.process.stdin.write(json.dumps({'id': identity, 'type': command, **fields}).encode() + b'\n')
        await self.process.stdin.drain()
        return identity

    async def read(self):
        line = await self.process.stdout.readline()
        self.received += len(line)
        if not line or len(line) > 1024 * 1024 or self.received > 8 * 1024 * 1024:
            raise CompletionError('pi_transport_closed_or_limit')
        try:
            value = json.loads(line)
            if not isinstance(value, dict):
                raise ValueError()
            return value
        except ValueError:
            raise CompletionError('pi_transport_invalid') from None

    async def call(self, command, **fields):
        identity = await self.send(command, **fields)
        while True:
            row = await self.read()
            if row.get('type') == 'response':
                if row.get('id') != identity or row.get('command') != command or row.get('success') is not True:
                    raise CompletionError('pi_rpc_rejected')
                return row.get('data')
            if row.get('type') not in ('extension_ui_request',):
                raise CompletionError('pi_unexpected_startup_event')
            raise CompletionError('pi_interactive_request_rejected')


def check_state(state, args):
    model = state.get('model') or {}
    if (model.get('provider') != args.bridge_provider or model.get('id') != args.model or
            state.get('isStreaming') is not False or state.get('isCompacting') is not False or
            state.get('pendingMessageCount', 0) != 0):
        raise CompletionError('pi_model_or_state_changed')


async def execute(args, system, prompt, schema, transport):
    args = default_request_args(args)
    with completion_workspace(args) as root:
        home, private, work = (root / name for name in ('home', 'pi', 'task'))
        for directory in (home, private, work):
            directory.mkdir(mode=0o700)
        # No credential parsing, exporting, or transfer to the provider adapter.
        # The isolated pi process opens this reference itself.
        auth = args.auth_home / 'auth.json'
        if auth.is_symlink() or not auth.is_file() or auth.stat().st_mode & 0o077:
            raise CompletionError('pi_authentication_required', 409)
        (private / 'auth.json').symlink_to(auth)
        models = checked_models(args.auth_home / 'models.json')
        if models is not None:
            target = private / 'models.json'
            target.write_bytes(models)
            target.chmod(0o400)
        if args.bridge_provider not in ('deepseek', 'ollama'):
            raise CompletionError('pi_provider_schema_unsupported', 409)
        settings = {'retry': {'enabled': False}, 'compaction': {'enabled': False}}
        if 'thinking_level' in args.agent_defaults_snapshot:
            settings['defaultThinkingLevel'] = args.agent_defaults_snapshot['thinking_level']
        (private / 'settings.json').write_text(json.dumps(settings))
        (private / 'settings.json').chmod(0o600)
        digest = hashlib.sha256(json.dumps(transport, separators=(',', ':'), ensure_ascii=False).encode()).hexdigest()
        extension = EXTENSION.read_text().replace('__MEMLANE_OUTPUT_SCHEMA__', json.dumps(transport, ensure_ascii=False))
        extension = extension.replace('__MEMLANE_SCHEMA_DIGEST__', digest).replace('__MEMLANE_SCHEMA_PROVIDER__', args.bridge_provider)
        path = root / 'schema-extension.ts'
        path.write_text(extension)
        path.chmod(0o400)
        environment = {'HOME': str(home), 'PI_CODING_AGENT_DIR': str(private), 'PI_TELEMETRY': '0',
                       'PI_OFFLINE': '1', 'MEMLANE_PROCESSING_SESSION': '1', 'TMPDIR': str(root),
                       'PATH': str(args.agent_executable.parent) + ':/usr/bin:/bin'}
        for key in ('LANG', 'LC_ALL', 'HTTPS_PROXY', 'HTTP_PROXY', 'ALL_PROXY', 'NO_PROXY'):
            if key in os.environ:
                environment[key] = os.environ[key]
        command = [str(args.agent_executable), '--mode', 'rpc', '--no-session', '--offline',
                   '--no-extensions', '--no-context-files', '--no-skills', '--no-prompt-templates',
                   '--no-themes', '--no-builtin-tools', '--no-tools', '--provider', args.bridge_provider,
                   '--model', args.model, '--system-prompt', system, '--extension', str(path)]
        spawn = asyncio.create_task(asyncio.create_subprocess_exec(*command, cwd=work, env=environment,
            stdin=asyncio.subprocess.PIPE, stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE,
            start_new_session=True, limit=1024 * 1024 + 1))
        try:
            process = await asyncio.shield(spawn)
        except asyncio.CancelledError:
            process = await spawn
            track_process(args, process, True)
            await terminate(process)
            track_process(args, process, False)
            raise
        stderr = asyncio.create_task(collect_stderr(process.stderr))
        rpc, accepted, ended, final = PiRPC(process), False, False, None
        try:
            track_process(args, process, True)
            async with asyncio.timeout(args.timeout):
                await rpc.call('set_auto_retry', enabled=False)
                await rpc.call('set_auto_compaction', enabled=False)
                check_state(await rpc.call('get_state'), args)
                identity = await rpc.send('prompt', message=prompt + '\nReturn only JSON matching:\n' + json.dumps(transport))
                while True:
                    row = await rpc.read()
                    kind = row.get('type', '')
                    if kind == 'response':
                        if accepted or row.get('id') != identity or row.get('command') != 'prompt' or row.get('success') is not True:
                            raise CompletionError('pi_prompt_rejected')
                        accepted = True
                    elif kind == 'message_end':
                        message = row.get('message', {})
                        if message.get('role') == 'assistant':
                            if final is not None or message.get('stopReason') != 'stop':
                                raise CompletionError('pi_model_failed')
                            content = message.get('content', [])
                            if any(item.get('type') not in ('text', 'thinking') for item in content):
                                raise CompletionError('pi_tool_rejected')
                            final = ''.join(item.get('text', '') for item in content if item.get('type') == 'text')
                    elif kind == 'agent_end':
                        if ended or row.get('willRetry') is not False:
                            raise CompletionError('pi_retry_or_terminal_invalid')
                        ended = True
                    elif kind == 'agent_settled':
                        if not accepted or not ended or not final:
                            raise CompletionError('pi_terminal_incomplete')
                        break
                    elif (kind.startswith(('tool_', 'auto_', 'summarization_')) or kind == 'extension_ui_request' or
                          kind == 'error'):
                        raise CompletionError('pi_tool_or_retry_rejected')
                    elif kind not in ('agent_start', 'turn_start', 'turn_end', 'message_start', 'message_update'):
                        raise CompletionError('pi_event_unrecognized')
                check_state(await rpc.call('get_state'), args)
                # Extension metadata is stderr-only in pi 0.84.1. Parse it after
                # confirmed shutdown below, never expose provider error bodies.
                if len(final.encode()) > MAX_BODY:
                    raise CompletionError('pi_output_limit')
                try:
                    value = json.loads(final)
                    for contract in ((schema, transport) if schema is not None else (transport,)):
                        validators.validator_for(contract)(contract).validate(value)
                except Exception:
                    raise CompletionError('pi_schema_failed') from None
        except (TimeoutError, asyncio.CancelledError) as error:
            try:
                async with asyncio.timeout(2):
                    await rpc.call('abort')
            except Exception:
                pass
            if isinstance(error, TimeoutError):
                raise CompletionError('pi_timeout', 504) from None
            raise
        finally:
            async def cleanup():
                await terminate(process)
                track_process(args, process, False)
                return await stderr
            finish = asyncio.create_task(cleanup())
            try:
                diagnostic = await asyncio.shield(finish)
            except asyncio.CancelledError:
                await finish
                raise
        evidence = []
        for line in diagnostic.decode(errors='replace').split('\n'):
            try:
                entry = json.loads(line)
                if isinstance(entry, dict) and entry.get('schema_sha256') == digest:
                    evidence.append(entry)
            except ValueError:
                pass
        bound = [e for e in evidence if e.get('type') == 'memlane_schema_bound']
        responses = [e for e in evidence if e.get('type') == 'memlane_schema_response']
        if len(bound) != 1 or len(responses) != 1 or not 200 <= responses[0].get('status', 0) < 300:
            raise CompletionError('pi_native_schema_unconfirmed')
        return value['text'] if schema is None else final
