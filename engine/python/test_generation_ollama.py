"""Local Ollama profile persistence and fixed-adapter native wire contract.

The HTTP peer is synthetic. These checks do not prove local model quality.
"""
import contextlib
import asyncio
import copy
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import io
import json
import os
from pathlib import Path
import sys
import tempfile
import threading
import unittest
from unittest.mock import patch

from generation_config import GenerationStore, compile_environment, runtime_generation, validate_profile
from generation_probe import probe
from generation_ollama import receipt_for, register_verified_schema
from project_policy import PolicyError

ROOT = Path(__file__).resolve().parents[2]
PROFILE = {'mode': 'provider', 'provider': 'ollama', 'model': 'qwen3:4b',
           'endpoint': 'http://127.0.0.1:11434'}


class OllamaProfileTests(unittest.TestCase):
    def test_save_reopen_and_runtime_need_no_credential_or_codex_and_preserve_active(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'generation.json'
            store = GenerationStore(path, 'fixed', legacy_model='original-model')
            try:
                row = store.put('local', PROFILE, 0, 'save-local')
                self.assertEqual(store.put('local', PROFILE, 0, 'save-local'), row)
                self.assertEqual(store.view()['active']['id'], 'existing-codex')
                self.assertIsNone(store.view()['next'])
                self.assertFalse((path.parent / 'generation-credentials').exists())
                self.assertNotIn('credential_', json.dumps(store.view()['profiles']['local']))
                with self.assertRaisesRegex(PolicyError, 'generation_validation_required'):
                    store.select_next(row, 1, 'not-yet-validated')
                runtime = {'generation_file': str(path), 'generation_reference': row, 'service_commit': 'fixed'}
                with self.assertRaisesRegex(PolicyError, 'generation_native_schema_evidence_required'):
                    runtime_generation(runtime)
                # Synthetic receipt exercises startup binding, not model quality.
                store.state['validations']['fixture'] = {'digest': row['digest'], 'status': 'passed',
                    'plain_passed': True, 'schema_passed': True, 'quality_passed': True,
                    'native_schema_evidence': receipt_for('ollama_chat/qwen3:4b', PROFILE['endpoint'])}
                store.save()
                profile, environment, digest = runtime_generation({
                    'generation_file': str(path), 'generation_reference': row, 'service_commit': 'fixed'})
                self.assertEqual(profile, PROFILE)
                self.assertEqual(digest, row['digest'])
                self.assertEqual(environment['LLM_MODEL'], 'ollama_chat/qwen3:4b')
                self.assertEqual(environment['LLM_PROVIDER'], 'ollama')
                self.assertEqual(environment['LLM_API_KEY'], 'ollama')
                self.assertEqual(json.loads(environment['MEMLANE_OLLAMA_SCHEMA_EVIDENCE']),
                                 store.state['validations']['fixture']['native_schema_evidence'])
                self.assertNotIn('COMPLETION_URL', environment)
            finally:
                store.close()
            reopened = GenerationStore(path, 'fixed')
            try:
                self.assertEqual(reopened.resolve(row)['profile'], PROFILE)
                changed = reopened.put('local', {**PROFILE, 'model': 'other-model'}, 1, 'edit-local')
                self.assertNotEqual(changed['digest'], row['digest'])
                self.assertEqual(reopened.resolve(row)['profile'], PROFILE)
            finally:
                reopened.close()

    def test_schema_receipt_must_match_latest_passed_model_endpoint_and_contract(self):
        from generation_startup import candidate
        with tempfile.TemporaryDirectory() as directory:
            store = GenerationStore(Path(directory) / 'generation.json', 'fixed')
            try:
                row = store.put('local', PROFILE, 0, 'save-local')
                passed = {'digest': row['digest'], 'status': 'passed', 'plain_passed': True,
                          'schema_passed': True, 'quality_passed': True, 'registered_revision': 2}
                store.state['validations']['v'] = passed
                for evidence in (None, receipt_for('ollama_chat/other-model', PROFILE['endpoint']),
                                 receipt_for('ollama_chat/qwen3:4b', 'http://127.0.0.1:22222'),
                                 {**receipt_for('ollama_chat/qwen3:4b', PROFILE['endpoint']), 'contract': 'unknown'}):
                    passed['native_schema_evidence'] = evidence
                    with self.assertRaisesRegex(PolicyError, 'generation_native_schema_evidence_required'):
                        store.select_next(row, 1, 'reject')
                passed['native_schema_evidence'] = receipt_for('ollama_chat/qwen3:4b', PROFILE['endpoint'])
                store.select_next(row, 1, 'select')
                self.assertEqual(candidate(store), row)
                store.state['validations']['new'] = {**passed, 'registered_revision': 3, 'status': 'failed'}
                with self.assertRaisesRegex(PolicyError, 'generation_validation_required'):
                    candidate(store)
            finally:
                store.close()

    def test_local_endpoint_and_auth_cannot_silently_become_cloud_or_inherit_a_key(self):
        for endpoint in ['https://ollama.com', 'http://example.test', 'http://127.0.0.1:11434/v1',
                         'http://127.0.0.1:11434/api', 'http://127.0.0.1:99999',
                         'http://secret@127.0.0.1:11434', 'http://127.0.0.1:11434?key=secret']:
            with self.subTest(endpoint=endpoint), self.assertRaises(PolicyError):
                validate_profile({**PROFILE, 'endpoint': endpoint})
        for field in ('credential_ref', 'credential_version'):
            with self.subTest(field=field), self.assertRaises(PolicyError):
                validate_profile({**PROFILE, field: 'old-key'})
        with tempfile.TemporaryDirectory() as directory:
            store = GenerationStore(Path(directory) / 'generation.json', 'fixed')
            try:
                with self.assertRaisesRegex(PolicyError, 'local_ollama_credential_not_required'):
                    store.put('local', PROFILE, 0, 'wrong-key', api_key='unrelated-cloud-key')
                self.assertEqual(store.state['revision'], 0)
            finally:
                store.close()
        self.assertEqual(compile_environment({**PROFILE, 'model': 'org/model'}, {}, 'ambient-key')['LLM_MODEL'],
                         'ollama_chat/org/model')
        self.assertEqual(compile_environment(PROFILE, {}, 'ambient-key')['LLM_API_KEY'], 'ollama')


class OllamaWireTests(unittest.IsolatedAsyncioTestCase):
    @classmethod
    def setUpClass(cls):
        cls.temporary = tempfile.TemporaryDirectory()
        root = Path(cls.temporary.name)
        env = {key: value for key, value in os.environ.items() if key in ('PATH', 'LANG', 'LC_ALL', 'TMPDIR')}
        env.update(HOME=str(root), SYSTEM_ROOT_DIRECTORY=str(root / 'system'), DATA_ROOT_DIRECTORY=str(root / 'data'),
                   CACHE_ROOT_DIRECTORY=str(root / 'cache'), LOGS_ROOT_DIRECTORY=str(root / 'logs'),
                   COGNEE_LOGS_DIR=str(root / 'logs'), TELEMETRY_DISABLED='true', COGNEE_TRACING_ENABLED='false',
                   PYTHON_DOTENV_DISABLED='1', LITELLM_LOCAL_MODEL_COST_MAP='true')
        cls.environment = patch.dict(os.environ, env, clear=True)
        cls.environment.start()
        manifest = json.loads((ROOT / 'engine/coding-memory-components.json').read_text())['service']
        cls.runtime = {'official_source': str(ROOT / '.cache/coding-memory-components' / ('service-' + manifest['commit'])),
                       'official_archive': str(ROOT / '.cache/coding-memory-components' / (manifest['commit'] + '.tar.gz')),
                       'generation_mode': 'provider'}
        sys.path.insert(0, cls.runtime['official_source'])
        with contextlib.redirect_stdout(io.StringIO()):
            from cognee.infrastructure.llm.config import LLMConfig
            from cognee.context_global_variables import llm_config
        cls.LLMConfig, cls.context = LLMConfig, llm_config

    @classmethod
    def tearDownClass(cls):
        cls.environment.stop()
        cls.temporary.cleanup()

    def test_temporary_capability_restores_aliases_and_persistent_registry(self):
        import litellm
        from litellm.utils import _runtime_registered_model_cost
        model, bare = 'ollama_chat/memlane-restore-fixture', 'memlane-restore-fixture'
        endpoint = 'http://127.0.0.1:11434'
        persistent = copy.deepcopy(_runtime_registered_model_cost)
        original = {'mode': 'chat', 'supports_response_schema': False, 'max_tokens': 8192}
        litellm.model_cost[bare] = copy.deepcopy(original)
        try:
            restore = register_verified_schema(receipt_for(model, endpoint), model, endpoint)
            # With an existing bare entry, register_model updates that alias.
            self.assertTrue(litellm.model_cost[bare]['supports_response_schema'])
            # Simulate aliases materialized by the SDK while proof is active.
            litellm.model_cost[bare]['supports_response_schema'] = True
            litellm.model_cost['ollama/' + bare] = {'supports_response_schema': True}
            restore()
            self.assertEqual(litellm.model_cost[bare], original)
            self.assertNotIn(model, litellm.model_cost)
            self.assertNotIn('ollama/' + bare, litellm.model_cost)
            self.assertEqual(_runtime_registered_model_cost, persistent)
        finally:
            for alias in (model, bare, 'ollama/' + bare):
                litellm.model_cost.pop(alias, None)

    async def scenario(self, mode, options=None):
        requests = []
        expected = {'project': {'name': '\u6837\u4f8b\u9879\u76ee', 'rules': ['pnpm', 'TypeScript'], 'contact': None},
                    'ports': [43127, 43128], 'source': 'synthetic'}

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass

            def do_POST(self):
                body = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
                requests.append((self.path, self.headers.get('Authorization'), body))
                if mode == 'redirect':
                    self.send_response(307)
                    self.send_header('Location', '/redirected')
                    self.send_header('Content-Length', '0')
                    self.end_headers()
                    return
                if self.path == '/api/show':
                    result = {'model_info': {'qwen3.context_length': 8192}, 'capabilities': ['completion']}
                else:
                    structured = 'format' in body
                    schema = body.get('format')
                    capability = isinstance(schema, dict) and 'proof' in schema.get('properties', {})
                    if capability and mode != 'ignore_schema':
                        content = json.dumps({'proof': schema['properties']['proof']['enum'][0], 'absent': None})
                    else:
                        content = json.dumps(expected) if structured else 'MEMLANE-PROBE-OK'
                    result = {'model': body['model'], 'created_at': '2026-11-14T00:00:00Z',
                        'message': {'role': 'assistant', 'content': content}, 'done': True,
                        'done_reason': 'length' if mode == 'truncated' and structured and not capability else 'stop',
                        'prompt_eval_count': 1, 'eval_count': 1}
                raw = json.dumps(result).encode()
                self.send_response(200)
                self.send_header('Content-Type', 'application/json')
                self.send_header('Content-Length', str(len(raw)))
                self.end_headers()
                self.wfile.write(raw)

        server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        endpoint = 'http://127.0.0.1:' + str(server.server_port)
        environment = compile_environment({**PROFILE, **(options or {}), 'endpoint': endpoint}, {})
        parameters = {}
        if 'LLM_TEMPERATURE' in environment:
            parameters['llm_temperature'] = float(environment['LLM_TEMPERATURE'])
        if 'LLM_ARGS' in environment:
            parameters['llm_args'] = json.loads(environment['LLM_ARGS'])
            parameters['llm_max_completion_tokens'] = int(environment['LLM_MAX_COMPLETION_TOKENS'])
        cfg = self.LLMConfig(llm_provider=environment['LLM_PROVIDER'], llm_model=environment['LLM_MODEL'],
            llm_api_key=environment['LLM_API_KEY'], llm_endpoint=environment['LLM_ENDPOINT'],
            structured_output_framework='litellm_native', **parameters)
        token = self.context.set(cfg)
        import litellm
        previous_capability = copy.deepcopy(litellm.model_cost.get(environment['LLM_MODEL']))
        from litellm.utils import _runtime_registered_model_cost
        previous_persistent = copy.deepcopy(_runtime_registered_model_cost)
        try:
            with patch.dict(os.environ, {'OLLAMA_API_BASE': environment['OLLAMA_API_BASE'],
                    'LLM_ENDPOINT': environment['LLM_ENDPOINT']}), contextlib.redirect_stdout(io.StringIO()):
                result = await asyncio.wait_for(probe(self.runtime), 30)
            return result, requests
        finally:
            self.assertEqual(litellm.model_cost.get(environment['LLM_MODEL']), previous_capability,
                             'validation leaked its temporary SDK capability registration')
            self.assertEqual(_runtime_registered_model_cost, previous_persistent,
                             'validation leaked capability into the SDK persistent replay registry')
            self.context.reset(token)
            await asyncio.to_thread(server.shutdown)
            server.server_close()
            thread.join(timeout=2)
            self.assertFalse(thread.is_alive())

    async def test_native_ollama_capacity_survives_cache_rebuild(self):
        import time
        import litellm
        from generation_http import generation_http_policy
        from cognee.infrastructure.llm import overload_policy
        from cognee.infrastructure.llm.structured_output_framework.litellm_native.get_native_client import get_native_client
        from tenacity import stop_after_attempt, wait_none
        active = peak = calls = 0
        lock = threading.Lock()
        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass
            def do_POST(self):
                nonlocal active, peak, calls
                body = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
                if self.path == '/api/show':
                    result = {'model_info': {'qwen3.context_length': 8192}, 'capabilities': ['completion']}
                else:
                    with lock:
                        active += 1
                        calls += 1
                        peak = max(peak, active)
                    try:
                        time.sleep(.005)
                        result = {'model': body['model'], 'message': {'role': 'assistant', 'content': 'ok'},
                            'done': True, 'done_reason': 'stop', 'prompt_eval_count': 1, 'eval_count': 1}
                    finally:
                        with lock:
                            active -= 1
                raw = json.dumps(result).encode()
                self.send_response(200)
                self.send_header('Content-Type', 'application/json')
                self.send_header('Content-Length', str(len(raw)))
                self.end_headers()
                self.wfile.write(raw)
        server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        endpoint = 'http://127.0.0.1:' + str(server.server_port)
        cfg = self.LLMConfig(llm_provider='ollama', llm_model='ollama_chat/synthetic',
            llm_api_key='ollama', llm_endpoint=endpoint, structured_output_framework='litellm_native')
        token = self.context.set(cfg)
        try:
            with patch.dict(os.environ, {'OLLAMA_API_BASE': endpoint}), \
                    patch.object(overload_policy, 'llm_overload_policy', overload_policy.OverloadPolicy()):
                metrics = io.StringIO()
                async with generation_http_policy(endpoint=endpoint, backend_kind='ollama') as capacity:
                    for batch in range(2):
                        if batch:
                            litellm.in_memory_llm_clients_cache.flush_cache()
                        client = get_native_client()
                        complete = client.acreate_structured_output.retry_with(stop=stop_after_attempt(1), wait=wait_none())
                        with contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(metrics):
                            result = await asyncio.wait_for(asyncio.gather(*(complete(client,
                                'Synthetic', 'Return ok', str, num_retries=0) for _ in range(32))), 30)
                        self.assertTrue(all(item == 'ok' for item in result))
                    self.assertEqual(calls, 64)
                    self.assertEqual(peak, 2)
                    self.assertEqual(capacity.active, 0)
                events = [json.loads(line) for line in metrics.getvalue().splitlines() if line.startswith('{')]
                finished = [row for row in events if row.get('event') == 'request_finished' and row.get('component') == 'generation_http']
                self.assertEqual(len(finished), 64)
                self.assertTrue(all(row['backend_kind'] == 'ollama' for row in finished))
                self.assertTrue(all({'capacity_queue', 'dispatch_to_headers', 'response_body'} <= row['durations_ms'].keys() for row in finished))
        finally:
            self.context.reset(token)
            await asyncio.to_thread(server.shutdown)
            server.server_close()
            thread.join(timeout=2)
            self.assertFalse(thread.is_alive())

    async def test_explicit_sampling_and_output_limit_reach_native_chat_wire(self):
        result, requests = await self.scenario('normal', {'temperature': .6, 'max_output_tokens': 8192})
        self.assertEqual(result['status'], 'passed', result)
        chats = [body for path, _, body in requests if path == '/api/chat']
        self.assertEqual(len(chats), 3)
        for body in chats:
            nonce = 'proof' in body.get('format', {}).get('properties', {}) if isinstance(body.get('format'), dict) else False
            if nonce:
                self.assertNotIn('num_predict', body.get('options', {}))
                self.assertNotIn('temperature', body.get('options', {}))
            else:
                self.assertEqual(body['options']['num_predict'], 8192)
                self.assertEqual(body['options']['temperature'], .6)

    async def test_native_chat_checks_random_schema_before_registering_selected_model(self):
        result, requests = await self.scenario('normal')
        self.assertEqual(result['status'], 'passed', result)
        self.assertTrue(result['plain_passed'] and result['schema_passed'] and result['quality_passed'])
        chats = [(path, key, body) for path, key, body in requests if path == '/api/chat']
        self.assertEqual(len(chats), 3)
        self.assertTrue(all(key in (None, 'Bearer ollama') and body['model'] == 'qwen3:4b'
                            for _, key, body in chats))
        self.assertTrue(any(path == '/api/show' for path, _, _ in requests))
        self.assertTrue(all(path in ('/api/chat', '/api/show') for path, _, _ in requests))
        capability = chats[1][2]['format']
        nonce = capability['properties']['proof']['enum'][0]
        self.assertNotIn(nonce, json.dumps(chats[1][2]['messages']))
        self.assertIn('project', chats[2][2]['format']['properties'])
        self.assertEqual(result['structured_mode'], 'json_schema')
        self.assertEqual(result['capability_calls'], 1)
        self.assertEqual(result['native_schema_evidence']['contract'], 'ollama-native-nonce-v1')

    async def test_ignored_native_schema_never_registers_or_runs_fact_extraction(self):
        result, requests = await self.scenario('ignore_schema')
        self.assertEqual(result['status'], 'failed', result)
        self.assertEqual(result['error_code'], 'native_schema_verification_failed')
        self.assertNotIn('native_schema_evidence', result)
        self.assertEqual(len([r for r in requests if r[0] == '/api/chat']), 2)

    async def test_valid_json_with_length_finish_is_not_accepted(self):
        result, _ = await self.scenario('truncated')
        self.assertEqual(result['status'], 'failed', result)
        self.assertEqual(result['error_code'], 'completion_truncated')
        self.assertFalse(result['quality_passed'])

    async def test_native_redirect_does_not_follow_to_another_route(self):
        result, requests = await self.scenario('redirect')
        self.assertNotEqual(result['status'], 'passed')
        self.assertTrue(requests)
        self.assertTrue(all(path in ('/api/chat', '/api/show') for path, _, _ in requests))


if __name__ == '__main__':
    unittest.main()
