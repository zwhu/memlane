#!/usr/bin/env python3
"""Local, non-streaming Cognee completion adapter; no memory business state."""
import argparse
import asyncio
import copy
import hmac
import json
import os
from pathlib import Path
import signal
import tempfile
import time
import uuid

from aiohttp import web
from jsonschema import validators

MAX_BODY = 2 * 1024 * 1024
PROFILE = 'memlane_completion'


class CompletionError(Exception):
    def __init__(self, code, status=502):
        self.code, self.status = code, status
        super().__init__(code)



def remote_error(raw):
    # Interpret only the machine error tag, never expose model/provider prose.
    info = raw.get('codexErrorInfo') if isinstance(raw, dict) else None
    codes = {'unauthorized': ('codex_authentication_failed', 502),
             'usageLimitExceeded': ('codex_quota_exceeded', 429),
             'sessionBudgetExceeded': ('codex_quota_exceeded', 429),
             'rateLimitExceeded': ('codex_rate_limited', 429),
             'modelNotFound': ('codex_model_unavailable', 502),
             'contextWindowExceeded': ('codex_context_exceeded', 400),
             'badRequest': ('codex_request_invalid', 400),
             'sandboxError': ('codex_isolation_failed', 502)}
    code, status = codes.get(info, ('codex_model_failed', 502)) if isinstance(info, str) else ('codex_model_failed', 502)
    return CompletionError(code, status)


def strict_schema(source):
    """Strengthen finite Pydantic models; validate against both contracts later."""
    root = copy.deepcopy(source)
    if not isinstance(root, dict):
        raise CompletionError('codex_schema_invalid', 400)

    def walk(node, depth=0):
        if not isinstance(node, dict):
            return
        if depth > 64:
            raise CompletionError('codex_schema_invalid', 400)
        if any(key in node and (not isinstance(node[key], str) or not node[key].startswith('#')) for key in ('$ref', '$dynamicRef', '$recursiveRef')):
            raise CompletionError('codex_schema_unsupported', 400)
        node.pop('default', None)
        if node.get('type') == 'object' or 'properties' in node:
            props = node.get('properties')
            if props is None:
                if node.get('additionalProperties') is not False:
                    raise CompletionError('codex_schema_unsupported', 400)
                props = node['properties'] = {}
            if not isinstance(props, dict) or node.get('additionalProperties', False) is not False:
                raise CompletionError('codex_schema_unsupported', 400)
            node['additionalProperties'] = False
            required = node.get('required', [])
            if not isinstance(required, list) or any(not isinstance(x, str) or x not in props for x in required) or len(set(required)) != len(required):
                raise CompletionError('codex_schema_invalid', 400)
            node['required'] = required + sorted(set(props) - set(required))
        for key in ('properties', '$defs', 'definitions', 'patternProperties', 'dependentSchemas'):
            for child in node.get(key, {}).values():
                walk(child, depth + 1)
        for key in ('items', 'contains', 'not', 'if', 'then', 'else', 'propertyNames', 'unevaluatedProperties', 'unevaluatedItems'):
            walk(node.get(key), depth + 1)
        for key in ('anyOf', 'allOf', 'oneOf', 'prefixItems'):
            for child in node.get(key, []):
                walk(child, depth + 1)
    try:
        validators.validator_for(root).check_schema(root)
        walk(root)
    except CompletionError:
        raise
    except Exception:
        raise CompletionError('codex_schema_invalid', 400) from None
    return root


def parse_request(body, model):
    if not isinstance(body, dict) or body.get('model') != model:
        raise CompletionError('codex_model_mismatch', 400)
    if body.get('stream') or body.get('tools') or body.get('functions'):
        raise CompletionError('codex_request_unsupported', 400)
    fmt = body.get('response_format')
    if fmt is None or isinstance(fmt, dict) and fmt.get('type') == 'text':
        # Cognee's own connection check (response_model=str) and other plain
        # completions deliberately have no business schema. Keep Codex's
        # native structured transport and validate its string envelope, then
        # unwrap text only for this explicitly unstructured input contract.
        schema = None
        transport = {'type': 'object', 'properties': {'text': {'type': 'string'}},
                     'required': ['text'], 'additionalProperties': False}
    elif isinstance(fmt, dict) and fmt.get('type') == 'json_schema':
        schema = fmt.get('json_schema', {}).get('schema')
        if len(json.dumps(schema)) > 65536:
            raise CompletionError('codex_schema_invalid', 400)
        transport = strict_schema(schema)
    else:
        # Do not downgrade a structured business request into arbitrary JSON
        # or text; the caller must supply its actual JSON schema.
        raise CompletionError('codex_response_format_unsupported', 400)
    messages = body.get('messages')
    if not isinstance(messages, list) or not messages:
        raise CompletionError('codex_messages_invalid', 400)
    system, prompt = [], []
    for message in messages:
        if not isinstance(message, dict) or message.get('role') not in ('system', 'developer', 'user', 'assistant'):
            raise CompletionError('codex_messages_invalid', 400)
        content = message.get('content')
        if isinstance(content, list):
            if any(not isinstance(x, dict) or x.get('type') != 'text' or not isinstance(x.get('text'), str) for x in content):
                raise CompletionError('codex_messages_invalid', 400)
            content = '\n'.join(x['text'] for x in content)
        if not isinstance(content, str):
            raise CompletionError('codex_messages_invalid', 400)
        if message['role'] in ('system', 'developer'):
            system.append(content)
        else:
            prompt.append({'role': message['role'], 'content': content})
    if not prompt:
        raise CompletionError('codex_messages_invalid', 400)
    return '\n\n'.join(system), json.dumps(prompt, ensure_ascii=False), schema, transport


def private_config(model, work):
    features = ('shell_tool unified_exec apps plugins hooks memories multi_agent multi_agent_v2 browser_use browser_use_external computer_use image_generation skill_search skill_mcp_dependency_install workspace_dependencies code_mode_host view_image remote_plugin').split()
    return '\n'.join([
        'model = ' + json.dumps(model), 'model_provider = "openai"',
        'default_permissions = "' + PROFILE + '"', 'web_search = "disabled"',
        'project_doc_max_bytes = 0', '[features]',
        *[name + ' = false' for name in features],
        '[skills]', 'include_instructions = false', '[skills.bundled]', 'enabled = false',
        '[analytics]', 'enabled = false', '[feedback]', 'enabled = false',
        '[permissions.' + PROFILE + '.filesystem]', '"/" = "deny"', '":minimal" = "read"', json.dumps(str(work)) + ' = "read"',
        '[permissions.' + PROFILE + '.network]', 'enabled = false', ''
    ])


class RPC:
    def __init__(self, process):
        self.process, self.counter, self.received = process, 0, 0

    async def send(self, method, params, notification=False):
        message = {'method': method, 'params': params}
        if not notification:
            self.counter += 1
            message['id'] = self.counter
        self.process.stdin.write(json.dumps(message).encode() + b'\n')
        await self.process.stdin.drain()
        return message.get('id')

    async def read(self):
        try:
            line = await self.process.stdout.readline()
            if not line:
                raise CompletionError('codex_transport_closed')
            self.received += len(line)
            if self.received > 64 * 1024 * 1024:
                raise CompletionError('codex_output_limit')
            message = json.loads(line)
            if not isinstance(message, dict):
                raise CompletionError('codex_transport_invalid')
            return message
        except (ValueError, asyncio.LimitOverrunError):
            raise CompletionError('codex_transport_invalid') from None

    async def call(self, method, params):
        request_id = await self.send(method, params)
        while True:
            reply = await self.read()
            if 'id' in reply:
                if reply.get('method') or reply['id'] != request_id or reply.get('error'):
                    raise CompletionError('codex_rpc_rejected')
                return reply.get('result', {})
            if reply.get('method') == 'error':
                raise remote_error(reply.get('params', {}).get('error'))


async def terminate(process):
    """Wait for the owned session leader and all processes in its group."""
    process.stdin.close()
    for sig, seconds in ((signal.SIGTERM, 3), (signal.SIGKILL, 3)):
        try:
            os.killpg(process.pid, sig)
        except ProcessLookupError:
            pass
        try:
            await asyncio.wait_for(process.wait(), seconds)
        except asyncio.TimeoutError:
            pass
        deadline = asyncio.get_running_loop().time() + seconds
        while asyncio.get_running_loop().time() < deadline:
            try:
                os.killpg(process.pid, 0)
            except ProcessLookupError:
                return
            await asyncio.sleep(.05)
    raise CompletionError('codex_process_cleanup_unknown', 503)



async def collect_stderr(stream):
    # Keep draining after the retained prefix fills: pipe backpressure must not
    # strand a child during failure or cleanup. This is never a public log.
    retained = bytearray()
    while True:
        chunk = await stream.read(4096)
        if not chunk:
            return bytes(retained)
        retained.extend(chunk[:max(0, 16384 - len(retained))])


def record_start_failure(args, process, code, stderr):
    directory = getattr(args, 'diagnostics_dir', None)
    if directory is None:
        token_file = getattr(args, 'token_file', None)
        directory = token_file.parent / 'diagnostics' if token_file else None
    if directory is None:
        return
    directory = Path(directory)
    directory.mkdir(parents=True, exist_ok=True, mode=0o700)
    if directory.is_symlink():
        raise CompletionError('codex_diagnostic_directory_invalid', 503)
    record = {'at': int(time.time()), 'failure_code': code,
              'pid': process.pid, 'exit_code': process.returncode,
              'stderr': stderr.decode(errors='replace')}
    payload = json.dumps(record, ensure_ascii=False).encode()
    while len(payload) > 16384:
        record['stderr'] = record['stderr'][:len(record['stderr']) // 2]
        payload = json.dumps(record, ensure_ascii=False).encode()
    temporary = directory / ('.codex-start-failure-' + uuid.uuid4().hex)
    descriptor = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, 'wb') as handle:
        handle.write(payload)
    os.replace(temporary, directory / 'codex-start-failure.json')


async def execute(args, system, prompt, schema, transport):
    with tempfile.TemporaryDirectory(prefix='memlane-completion-') as temporary:
        root = Path(temporary)
        home, private, work = (root / name for name in ('home', 'codex', 'task'))
        for directory in (home, private, work):
            directory.mkdir(mode=0o700)
        config = private / 'config.toml'
        config.write_text(private_config(args.model, work))
        config.chmod(0o600)
        # Copy only subscription authentication, never daily config/plugins/history.
        auth = args.auth_home / 'auth.json'
        if not auth.is_file():
            raise CompletionError('codex_authentication_required', 503)
        auth_target = private / 'auth.json'
        auth_target.write_bytes(auth.read_bytes())
        auth_target.chmod(0o600)
        environment = {'HOME': str(home), 'CODEX_HOME': str(private), 'TMPDIR': str(root),
                       'PATH': str(args.codex_executable.parent) + ':/usr/bin:/bin'}
        for key in ('LANG', 'LC_ALL', 'HTTPS_PROXY', 'HTTP_PROXY', 'ALL_PROXY', 'NO_PROXY'):
            if key in os.environ:
                environment[key] = os.environ[key]
        # The group is owned by this request, never the user's ordinary Codex.
        spawn = asyncio.create_task(asyncio.create_subprocess_exec(str(args.codex_executable), 'app-server', '--strict-config', '--stdio',
            cwd=work, env=environment, stdin=asyncio.subprocess.PIPE, stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE, start_new_session=True, limit=8 * 1024 * 1024))
        try:
            process = await asyncio.shield(spawn)
        except asyncio.CancelledError:
            process = await spawn
            await terminate(process)
            raise
        stderr_task = asyncio.create_task(collect_stderr(process.stderr))
        rpc, thread_id, turn_id, failure = RPC(process), None, None, None
        try:
            async with asyncio.timeout(args.timeout):
                await rpc.call('initialize', {'clientInfo': {'name': 'memlane_completion', 'version': '1'}, 'capabilities': {'experimentalApi': True}})
                await rpc.send('initialized', {}, notification=True)
                started = await rpc.call('thread/start', {'cwd': str(work), 'runtimeWorkspaceRoots': [],
                    'permissions': PROFILE, 'model': args.model, 'modelProvider': 'openai', 'approvalPolicy': 'never',
                    'ephemeral': True, 'allowProviderModelFallback': False, 'dynamicTools': [],
                    'baseInstructions': system, 'developerInstructions': 'Complete only this isolated model request.'})
                thread_id = started.get('thread', {}).get('id')
                if not thread_id or started.get('model') != args.model or started.get('modelProvider') != 'openai' or started.get('runtimeWorkspaceRoots') != [] or started.get('activePermissionProfile', {}).get('id') != PROFILE:
                    raise CompletionError('codex_model_or_permissions_changed')
                request_id = await rpc.send('turn/start', {'threadId': thread_id, 'input': [{'type': 'text', 'text': prompt, 'text_elements': []}], 'outputSchema': transport, 'permissions': PROFILE})
                final, announced = None, None
                while True:
                    event = await rpc.read()
                    method, params = event.get('method', ''), event.get('params', {})
                    if 'id' in event:
                        if method or event['id'] != request_id or event.get('error') or turn_id:
                            raise CompletionError('codex_tool_or_rpc_rejected')
                        turn_id = event.get('result', {}).get('turn', {}).get('id')
                        if not turn_id or announced and announced != turn_id:
                            raise CompletionError('codex_event_identity_invalid')
                        continue
                    if method == 'error':
                        raise remote_error(params.get('error'))
                    if method == 'turn/started':
                        announced = params.get('turn', {}).get('id')
                        if params.get('threadId') != thread_id or not announced or turn_id and announced != turn_id:
                            raise CompletionError('codex_event_identity_invalid')
                    if method.startswith('item/'):
                        if params.get('threadId') != thread_id or not turn_id or params.get('turnId') != turn_id:
                            raise CompletionError('codex_event_identity_invalid')
                        if method in ('item/started', 'item/completed'):
                            item = params.get('item', {})
                            if item.get('type') not in ('agentMessage', 'reasoning', 'userMessage'):
                                raise CompletionError('codex_unexpected_tool_execution')
                            if method == 'item/completed' and item['type'] == 'agentMessage' and item.get('phase') != 'commentary':
                                if final is not None:
                                    raise CompletionError('codex_multiple_final_answers')
                                final = item.get('text')
                        elif not method.startswith(('item/agentMessage/', 'item/reasoning/')):
                            raise CompletionError('codex_unexpected_tool_execution')
                    if method == 'turn/completed':
                        turn = params.get('turn', {})
                        if params.get('threadId') != thread_id or not turn_id or turn.get('id') != turn_id:
                            raise CompletionError('codex_event_identity_invalid')
                        if turn.get('status') != 'completed' or turn.get('error'):
                            raise remote_error(turn.get('error'))
                        break
                try:
                    if not isinstance(final, str) or len(final) > MAX_BODY:
                        raise ValueError()
                    value = json.loads(final)
                    for contract in ((schema, transport) if schema is not None else (transport,)):
                        validators.validator_for(contract)(contract).validate(value)
                except Exception:
                    raise CompletionError('codex_schema_failed') from None
                return value['text'] if schema is None else final
        except CompletionError as error:
            failure = error.code
            raise
        except (TimeoutError, asyncio.CancelledError) as error:
            failure = 'codex_timeout' if isinstance(error, TimeoutError) else 'codex_cancelled'
            if thread_id and turn_id:
                try:
                    async with asyncio.timeout(2):
                        await rpc.send('turn/interrupt', {'threadId': thread_id, 'turnId': turn_id})
                        while True:
                            event = await rpc.read()
                            params = event.get('params', {})
                            if event.get('method') == 'turn/completed' and params.get('threadId') == thread_id and params.get('turn', {}).get('id') == turn_id:
                                break
                except Exception:
                    pass
            if isinstance(error, TimeoutError):
                raise CompletionError('codex_timeout', 504) from None
            raise
        finally:
            # Shield cleanup even when upstream disconnects or server shuts down.
            async def finish():
                await terminate(process)
                stderr = await stderr_task
                if failure and turn_id is None:
                    record_start_failure(args, process, failure, stderr)
            cleanup = asyncio.create_task(finish())
            try:
                await asyncio.shield(cleanup)
            except asyncio.CancelledError:
                await cleanup
                raise


def create_app(args):
    token = args.token_file.read_text().strip()
    if len(token) < 24:
        raise ValueError('completion token must contain at least 24 characters')
    app = web.Application(client_max_size=MAX_BODY)
    concurrency = getattr(args, 'max_concurrency', 2)
    inflight = getattr(args, 'max_inflight', 32)
    if not 1 <= concurrency <= inflight <= 32:
        raise ValueError('completion limits require 1 <= concurrency <= inflight <= 32')
    slot = asyncio.Semaphore(concurrency)
    admitted = 0
    handlers = set()
    parent_pid = os.getppid()

    async def watch_parent():
        while True:
            await asyncio.sleep(.5)
            if os.getppid() != parent_pid:
                os.kill(os.getpid(), signal.SIGTERM)
                return

    async def lifecycle(app):
        monitor = asyncio.create_task(watch_parent())
        yield
        monitor.cancel()
        try:
            await monitor
        except asyncio.CancelledError:
            pass

    async def shutdown(app):
        active = list(handlers)
        for task in active:
            task.cancel()
        await asyncio.gather(*active, return_exceptions=True)

    app.cleanup_ctx.append(lifecycle)
    app.on_shutdown.append(shutdown)

    async def health(request):
        return web.json_response({'status': 'ready', 'model': args.model, 'provider': 'codex', 'active_or_waiting': admitted, 'max_concurrency': concurrency, 'max_inflight': inflight})

    async def complete(request):
        nonlocal admitted
        if not hmac.compare_digest(request.headers.get('Authorization', ''), 'Bearer ' + token):
            return web.json_response({'error': {'code': 'unauthorized', 'message': 'unauthorized'}}, status=401)
        handler = asyncio.current_task()
        handlers.add(handler)
        handler.add_done_callback(handlers.discard)
        try:
            body = await request.json()
            parsed = parse_request(body, args.model)
            if admitted >= inflight:
                raise CompletionError('codex_busy', 429)
            admitted += 1
            try:
                async with slot:
                    task = asyncio.create_task(execute(args, *parsed))
                    try:
                        while not task.done():
                            if request.transport is None or request.transport.is_closing():
                                task.cancel()
                                try:
                                    await task
                                except asyncio.CancelledError:
                                    pass
                                raise CompletionError('codex_upstream_cancelled', 499)
                            await asyncio.wait({task}, timeout=.1)
                        result = await task
                    finally:
                        if not task.done():
                            task.cancel()
                            try:
                                await task
                            except asyncio.CancelledError:
                                pass
            finally:
                admitted -= 1
            return web.json_response({'id': 'chatcmpl-' + uuid.uuid4().hex, 'object': 'chat.completion',
                'created': int(time.time()), 'model': args.model,
                'choices': [{'index': 0, 'message': {'role': 'assistant', 'content': result}, 'finish_reason': 'stop'}]})
        except CompletionError as error:
            return web.json_response({'error': {'code': error.code, 'message': error.code, 'type': 'completion_error'}}, status=error.status)
        except (ValueError, TypeError, AttributeError):
            return web.json_response({'error': {'code': 'invalid_request', 'message': 'invalid_request'}}, status=400)
        except OSError:
            return web.json_response({'error': {'code': 'codex_process_unavailable', 'message': 'codex_process_unavailable'}}, status=503)

    app.router.add_get('/health', health)
    app.router.add_post('/v1/chat/completions', complete)
    return app


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--host', choices=('127.0.0.1', '::1'), default='127.0.0.1')
    parser.add_argument('--port', type=int, required=True)
    parser.add_argument('--token-file', type=Path, required=True)
    parser.add_argument('--codex-executable', type=Path, required=True)
    parser.add_argument('--auth-home', type=Path, required=True)
    parser.add_argument('--model', required=True)
    parser.add_argument('--timeout', type=float, default=120)
    parser.add_argument('--max-concurrency', type=int, default=2)
    parser.add_argument('--max-inflight', type=int, default=32)
    parser.add_argument('--diagnostics-dir', type=Path, help='Private bounded startup failures; defaults beside the token file')
    args = parser.parse_args()
    if not args.codex_executable.is_absolute() or not args.codex_executable.is_file() or not args.auth_home.is_absolute() or args.timeout <= 0:
        parser.error('absolute existing executable, absolute auth home and positive timeout required')
    web.run_app(create_app(args), host=args.host, port=args.port, access_log=None, print=None,
                handler_cancellation=True, shutdown_timeout=15)


if __name__ == '__main__':
    main()
