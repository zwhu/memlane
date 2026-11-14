"""Local Ollama profile persistence and fixed-adapter native wire contract.

The HTTP peer is synthetic. These checks do not prove local model quality.
"""
import contextlib
import asyncio
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
                profile, environment, digest = runtime_generation({
                    'generation_file': str(path), 'generation_reference': row, 'service_commit': 'fixed'})
                self.assertEqual(profile, PROFILE)
                self.assertEqual(digest, row['digest'])
                self.assertEqual(environment['LLM_MODEL'], 'ollama_chat/qwen3:4b')
                self.assertEqual(environment['LLM_PROVIDER'], 'ollama')
                self.assertEqual(environment['LLM_API_KEY'], 'ollama')
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

    async def scenario(self, mode):
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
                    content = json.dumps(expected) if structured else 'MEMLANE-PROBE-OK'
                    result = {'model': body['model'], 'created_at': '2026-11-14T00:00:00Z',
                        'message': {'role': 'assistant', 'content': content}, 'done': True,
                        'done_reason': 'length' if mode == 'truncated' and structured else 'stop',
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
        environment = compile_environment({**PROFILE, 'endpoint': endpoint}, {})
        cfg = self.LLMConfig(llm_provider=environment['LLM_PROVIDER'], llm_model=environment['LLM_MODEL'],
            llm_api_key=environment['LLM_API_KEY'], llm_endpoint=environment['LLM_ENDPOINT'],
            structured_output_framework='litellm_native')
        token = self.context.set(cfg)
        try:
            with patch.dict(os.environ, {'OLLAMA_API_BASE': environment['OLLAMA_API_BASE']}), contextlib.redirect_stdout(io.StringIO()):
                result = await asyncio.wait_for(probe(self.runtime), 30)
            return result, requests
        finally:
            self.context.reset(token)
            await asyncio.to_thread(server.shutdown)
            server.server_close()
            thread.join(timeout=2)
            self.assertFalse(thread.is_alive())

    async def test_native_chat_and_nested_format_use_actual_fixed_sdk_without_capability_registration(self):
        result, requests = await self.scenario('normal')
        self.assertEqual(result['status'], 'passed', result)
        self.assertTrue(result['plain_passed'] and result['schema_passed'] and result['quality_passed'])
        chats = [(path, key, body) for path, key, body in requests if path == '/api/chat']
        self.assertEqual(len(chats), 2)
        self.assertTrue(all(key in (None, 'Bearer ollama') and body['model'] == 'qwen3:4b'
                            for _, key, body in chats))
        self.assertTrue(any(path == '/api/show' for path, _, _ in requests))
        self.assertTrue(all(path in ('/api/chat', '/api/show') for path, _, _ in requests))
        self.assertIn('format', chats[1][2])
        self.assertIn(result['structured_mode'], ('json_schema', 'json_schema_non_strict', 'json_object'))

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
