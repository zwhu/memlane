"""Native agy print transport with private context and owned process cleanup."""
import asyncio
from contextlib import contextmanager
import json
import os
from pathlib import Path
import shutil
import signal
import tempfile

from jsonschema import validators

from codex_completion_endpoint import (ActivityClock, CompletionError, MAX_BODY, await_with_idle,
                                       collect_stderr, default_request_args, read_line_with_idle,
                                       terminate, track_process)

AGENT = 'memlane-processing'


@contextmanager
def private_workspace(args):
    # Stay outside the repository: native discovery must not ascend into its
    # AGENTS.md or .agents directory, including during validation and handoff.
    root = Path(tempfile.mkdtemp(prefix='memlane-agy-'))
    try:
        yield root
    finally:
        if not args.owned_processes:
            shutil.rmtree(root)


def transport_setup(args, system, transport, root):
    work, home = (root / name for name in ('task', 'home'))
    gemini = home / '.gemini'
    for directory in (work, home, gemini):
        directory.mkdir(mode=0o700)
    agents = work / '.agents/agents'
    agents.mkdir(parents=True, mode=0o700)
    agent = agents / (AGENT + '.md')
    agent.write_text('''---
name: memlane-processing
description: Isolated memory processing request
mainAgent: true
subagent: false
model: inherit
tools: []
mcpServers: []
skills: []
plugins: []
---
''' + system + '\nReturn only the object required by the native JSON Schema.\n')
    agent.chmod(0o600)
    config = gemini / 'config'
    config.mkdir(mode=0o700)
    for name, content in (('config.json', {'plugins': {}}), ('hooks.json', {}), ('mcp_config.json', {'mcpServers': {}})):
        path = config / name
        path.write_text(json.dumps(content))
        path.chmod(0o600)
    schema_file = root / 'response-schema.json'
    schema_file.write_text(json.dumps(transport, ensure_ascii=False))
    schema_file.chmod(0o600)
    profile = Path(args.auth_home)
    if profile.name != 'config' or profile.parent.name != '.gemini':
        raise CompletionError('antigravity_auth_home_layout_unsupported', 409)
    # Isolate filesystem discovery while keeping the same OS user/keyring.
    # No token, login file, old conversation or user customization is copied.
    environment = {'HOME': str(home), 'TMPDIR': str(root),
                   'MEMLANE_PROCESSING_SESSION': '1',
                   'PATH': str(args.agent_executable.parent) + ':/usr/bin:/bin'}
    for name in ('LANG', 'LC_ALL', 'HTTPS_PROXY', 'HTTP_PROXY', 'ALL_PROXY', 'NO_PROXY'):
        if name in os.environ:
            environment[name] = os.environ[name]
    command = [str(args.agent_executable), '--input-format', 'stream-json', '--output-format', 'stream-json',
               '--model', args.model, '--agent', AGENT, '--json-schema', str(schema_file),
               '--disable-slash-commands']
    return command, environment, work


class AgyStream:
    def __init__(self, process, clock=None):
        self.process, self.received, self.clock = process, 0, clock

    async def read(self):
        if self.clock is not None:
            line = await read_line_with_idle(self.process.stdout, self.clock)
        else:
            line = await self.process.stdout.readline()
        self.received += len(line)
        if not line or len(line) > MAX_BODY or self.received > 8 * MAX_BODY:
            raise CompletionError('antigravity_transport_closed_or_limit')
        try:
            value = json.loads(line)
            if not isinstance(value, dict) or value.get('event') not in ('init', 'step_update', 'result'):
                raise ValueError()
            return value
        except (ValueError, TypeError):
            raise CompletionError('antigravity_transport_invalid') from None


def initialized(value, args, work, transport):
    if value.get('event') == 'result':
        native_failure(value.get('result'))
    init = value.get('init')
    if value.get('event') != 'init' or not isinstance(init, dict):
        raise CompletionError('antigravity_initialization_unconfirmed')
    if init.get('tools') != []:
        raise CompletionError('antigravity_tools_not_isolated', 409)
    if init.get('model') != args.model or init.get('agent') != AGENT or Path(init.get('cwd', '')).resolve() != work.resolve():
        raise CompletionError('antigravity_model_or_context_changed', 409)
    schema = init.get('json_schema')
    if isinstance(schema, str):
        try:
            schema = json.loads(schema)
        except ValueError:
            raise CompletionError('antigravity_schema_not_bound') from None
    if schema != transport:
        raise CompletionError('antigravity_schema_not_bound')
    identity = value.get('conversation_id')
    if not isinstance(identity, str) or not identity:
        raise CompletionError('antigravity_conversation_unconfirmed')
    return identity


def native_failure(result):
    if not isinstance(result, dict):
        raise CompletionError('antigravity_terminal_invalid')
    error = result.get('error')
    if isinstance(error, str):
        if 'not currently available in your location' in error:
            raise CompletionError('antigravity_account_location_unavailable', 403)
        if any(term in error.lower() for term in ('authentication required', 'authentication failed')):
            raise CompletionError('antigravity_authentication_required', 409)
    if result.get('status') in ('CANCELED', 'INTERRUPTED'):
        raise CompletionError('antigravity_cancelled', 499)
    raise CompletionError('antigravity_model_failed')


def terminal(value, schema, transport):
    result = value.get('result')
    if value.get('event') != 'result' or not isinstance(result, dict):
        raise CompletionError('antigravity_terminal_invalid')
    if result.get('status') != 'SUCCESS' or result.get('error'):
        native_failure(result)
    if 'structured_output' not in result:
        raise CompletionError('antigravity_structured_output_missing')
    output = result['structured_output']
    if isinstance(output, str):
        try:
            output = json.loads(output)
        except ValueError:
            raise CompletionError('antigravity_schema_failed') from None
    try:
        for contract in ((schema, transport) if schema is not None else (transport,)):
            validators.validator_for(contract)(contract).validate(output)
        encoded = json.dumps(output, ensure_ascii=False, allow_nan=False)
        if len(encoded.encode()) > MAX_BODY:
            raise ValueError()
    except Exception:
        raise CompletionError('antigravity_schema_failed') from None
    return output['text'] if schema is None else encoded


async def execute(args, system, prompt, schema, transport):
    args = default_request_args(args)
    if getattr(args, 'owned_processes', None) is None:
        args.owned_processes = {}
    with private_workspace(args) as root:
        command, environment, work = transport_setup(args, system, transport, root)
        spawn = asyncio.create_task(asyncio.create_subprocess_exec(*command, cwd=work, env=environment,
            stdin=asyncio.subprocess.PIPE, stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE,
            start_new_session=True, limit=MAX_BODY + 1))
        try:
            process = await asyncio.shield(spawn)
        except asyncio.CancelledError:
            process = await spawn
            track_process(args, process, True)
            await terminate(process)
            track_process(args, process, False)
            raise
        clock = ActivityClock(args.timeout)
        stderr = asyncio.create_task(collect_stderr(process.stderr, on_activity=clock.touch))
        try:
            track_process(args, process, True)
            stream = AgyStream(process, clock)
            # No material is sent until the native client reports the
            # selected model, private workspace, empty tools and schema.
            conversation = initialized(await stream.read(), args, work, transport)
            process.stdin.write(json.dumps({'event': 'user', 'message': {'content': prompt}},
                                           ensure_ascii=False).encode() + b'\n')
            await await_with_idle(process.stdin.drain(), clock)
            while True:
                row = await stream.read()
                if row['event'] == 'init':
                    raise CompletionError('antigravity_duplicate_initialization')
                event = row.get(row['event'])
                if not isinstance(event, dict):
                    raise CompletionError('antigravity_transport_invalid')
                identity = event.get('conversation_id')
                if not isinstance(identity, str) or not identity:
                    raise CompletionError('antigravity_conversation_unconfirmed')
                if conversation != identity:
                    raise CompletionError('antigravity_conversation_changed')
                if row['event'] == 'step_update':
                    step = str(event.get('step_type', '')).upper()
                    if event.get('tool_name') or event.get('tool_info') or event.get('subagent_info') or 'TOOL' in step or 'SUBAGENT' in step:
                        raise CompletionError('antigravity_tool_rejected', 409)
                    continue
                value = terminal(row, schema, transport)
                process.stdin.close()
                await await_with_idle(process.wait(), clock)
                if process.returncode != 0:
                    raise CompletionError('antigravity_process_failed')
                return value
        except (TimeoutError, asyncio.CancelledError) as error:
            # Native print mode uses the same interrupt as Ctrl+C. Await the
            # local process, then verify/reap the full group below.
            try:
                os.killpg(process.pid, signal.SIGINT)
                await asyncio.wait_for(process.wait(), 2)
            except (ProcessLookupError, TimeoutError):
                pass
            if isinstance(error, TimeoutError):
                raise CompletionError('antigravity_timeout', 504) from None
            raise
        finally:
            async def cleanup():
                await terminate(process)
                track_process(args, process, False)
                await stderr
            finish = asyncio.create_task(cleanup())
            try:
                await asyncio.shield(finish)
            except asyncio.CancelledError:
                await finish
                raise
