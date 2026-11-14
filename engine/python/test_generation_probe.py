"""Real pinned Cognee/LiteLLM adapter against a synthetic loopback endpoint.

No vendor account/model is exercised; these are protocol/error boundary tests.
"""
import asyncio
import contextlib
import io
import json
import os
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

from aiohttp import web
from aiohttp.test_utils import TestServer

from generation_probe import probe, CompletionObservation, ProbeFailure
from generation_config import compile_environment

ROOT = Path(__file__).resolve().parents[2]
EXPECTED = {'project': {'name': '样例项目', 'rules': ['pnpm', 'TypeScript'], 'contact': None},
            'ports': [43127, 43128], 'source': 'synthetic'}


def configured_parameters(provider, model, endpoint, parameters):
    if parameters is None:
        return {}
    environment = compile_environment({'mode': 'provider', 'provider': provider, 'model': model,
        'endpoint': endpoint, 'credential_ref': 'fixture', 'credential_version': 'v1', **parameters}, {}, 'synthetic-only-key')
    return {'llm_temperature': float(environment['LLM_TEMPERATURE']),
            'llm_max_completion_tokens': int(environment['LLM_MAX_COMPLETION_TOKENS']),
            'llm_args': json.loads(environment['LLM_ARGS'])}


class ProbeTests(unittest.IsolatedAsyncioTestCase):
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

    async def scenario(self, mode, *, native=True, parameters=None):
        requests = []
        from cognee.infrastructure.llm.structured_output_framework.litellm_native.native_adapter import clear_nonstrict_demotions
        clear_nonstrict_demotions()

        async def completion(request):
            self.assertEqual(request.headers.get('Authorization'), 'Bearer synthetic-only-key')
            body = await request.json()
            requests.append(body)
            if mode == 'redirect' and request.path != '/redirected':
                return web.Response(status=307, headers={'Location': '/redirected'})
            structured = 'response_format' in body
            if mode in ('401', '429'):
                return web.json_response({'error': {'message': 'synthetic-only-secret-body', 'type': 'invalid_request_error'}}, status=int(mode))
            if mode == 'timeout':
                await asyncio.sleep(1)
            shape = body.get('response_format', {}).get('type')
            if structured and mode in ('json_fallback', 'unsupported') and shape == 'json_schema':
                return web.json_response({'error': {'message': 'response_format schema unsupported', 'type': 'invalid_request_error'}}, status=400)
            if structured and mode == 'unsupported':
                return web.json_response({'error': {'message': 'response_format unsupported', 'type': 'invalid_request_error'}}, status=400)
            content = json.dumps(EXPECTED, ensure_ascii=False) if structured else 'MEMLANE-PROBE-OK'
            reason = 'stop'
            message = {'role': 'assistant', 'content': content}
            if mode == 'truncated' and structured:
                # Even a valid JSON object must not pass when marked partial.
                reason = 'length'
            if mode == 'refusal' and structured:
                message['refusal'] = 'synthetic-only-secret-refusal'
            if mode == 'invalid' and structured:
                message['content'] = '{"project":null}'
            if mode == 'quality' and structured:
                message['content'] = content.replace('43127', '99999')
            return web.json_response({'id': 'synthetic-completion', 'object': 'chat.completion', 'created': 1,
                'model': body['model'], 'choices': [{'index': 0, 'message': message, 'finish_reason': reason}],
                'usage': {'prompt_tokens': 1, 'completion_tokens': 1, 'total_tokens': 2}})

        app = web.Application()
        app.router.add_post('/v1/chat/completions', completion)
        app.router.add_post('/redirected', completion)
        server = TestServer(app)
        await server.start_server()
        model = 'openai/gpt-4o-2024-08-06' if native else 'openai/memlane-unknown-fixture'
        endpoint = str(server.make_url('/v1'))
        options = configured_parameters('custom', model, endpoint, parameters)
        options.setdefault('llm_args', {})
        if mode == 'timeout':
            options['llm_args']['timeout'] = .1
        cfg = self.LLMConfig(llm_provider='custom', llm_model=model, llm_api_key='synthetic-only-key',
                             llm_endpoint=endpoint, structured_output_framework='litellm_native', **options)
        token = self.context.set(cfg)
        import litellm
        original = litellm.acompletion
        try:
            with contextlib.redirect_stdout(io.StringIO()):
                result = await asyncio.wait_for(probe(self.runtime), 30)
            self.assertIs(litellm.acompletion, original, 'probe observer leaked after completion')
            self.assertNotIn('synthetic-only-secret', json.dumps(result))
            self.assertTrue(all(r['model'] == model.removeprefix('openai/') for r in requests))
            return result, requests
        finally:
            self.context.reset(token)
            await server.close()

    async def test_actual_schema_and_json_object_modes_are_recorded_without_capability_forgery(self):
        for native, mode in ((True, 'json_schema'), (False, 'json_object')):
            with self.subTest(mode=mode):
                result, requests = await self.scenario('success', native=native)
                self.assertEqual(result['status'], 'passed', result)
                self.assertEqual(result['structured_mode'], mode)
                self.assertEqual(result['structured_mode_evidence'], 'official_adapter_request')
                self.assertEqual(len(requests), 2)

    async def native_protocol(self, provider, mode, parameters=None):
        """Exercise the installed vendor wire adapter, not an OpenAI-shaped proxy."""
        requests = []

        async def completion(request):
            body = await request.json()
            requests.append({'path': request.path, 'body': body})
            if mode == 'redirect' and request.path != '/redirected':
                return web.Response(status=307, headers={'Location': '/redirected'})
            if provider == 'anthropic':
                self.assertEqual(request.headers.get('x-api-key'), 'synthetic-only-key')
            else:
                self.assertEqual(request.headers.get('x-goog-api-key') or request.query.get('key'),
                                 'synthetic-only-key')
            if mode in ('401', '429'):
                if provider == 'anthropic':
                    error = {'type': 'error', 'error': {'type': 'authentication_error' if mode == '401' else 'rate_limit_error',
                                                       'message': 'synthetic-only-secret-body'}}
                else:
                    error = {'error': {'code': int(mode), 'status': 'UNAUTHENTICATED' if mode == '401' else 'RESOURCE_EXHAUSTED',
                                       'message': 'synthetic-only-secret-body'}}
                return web.json_response(error, status=int(mode))
            content = 'MEMLANE-PROBE-OK' if len(requests) == 1 else json.dumps(EXPECTED, ensure_ascii=False)
            truncated = mode == 'truncated' and len(requests) > 1
            if provider == 'anthropic':
                result = {'id': 'msg_synthetic', 'type': 'message', 'role': 'assistant', 'model': body['model'],
                          'content': [{'type': 'text', 'text': content}],
                          'stop_reason': 'max_tokens' if truncated else 'end_turn', 'stop_sequence': None,
                          'usage': {'input_tokens': 1, 'output_tokens': 1}}
            else:
                result = {'candidates': [{'content': {'role': 'model', 'parts': [{'text': content}]},
                                          'finishReason': 'MAX_TOKENS' if truncated else 'STOP', 'index': 0}],
                          'usageMetadata': {'promptTokenCount': 1, 'candidatesTokenCount': 1, 'totalTokenCount': 2},
                          'modelVersion': 'gemini-2.5-flash'}
            return web.json_response(result)

        app = web.Application()
        app.router.add_post('/{path:.*}', completion)
        server = TestServer(app)
        await server.start_server()
        model = 'anthropic/claude-sonnet-4-6' if provider == 'anthropic' else 'gemini/gemini-2.5-flash'
        endpoint = str(server.make_url('/anthropic' if provider == 'anthropic' else '/v1beta'))
        cfg = self.LLMConfig(llm_provider=provider, llm_model=model, llm_api_key='synthetic-only-key',
                             llm_endpoint=endpoint, structured_output_framework='litellm_native',
                             **configured_parameters(provider, model, endpoint, parameters))
        token = self.context.set(cfg)
        try:
            with contextlib.redirect_stdout(io.StringIO()):
                result = await asyncio.wait_for(probe(self.runtime), 30)
            self.assertNotIn('synthetic-only-secret', json.dumps(result))
            expected_path = '/anthropic/v1/messages' if provider == 'anthropic' else '/v1beta/models/gemini-2.5-flash:generateContent'
            if mode != 'redirect':
                self.assertTrue(all(row['path'] == expected_path for row in requests), requests)
            return result, requests
        finally:
            self.context.reset(token)
            await server.close()

    async def test_explicit_parameters_reach_each_native_vendor_wire(self):
        parameters = {'temperature': .6, 'max_output_tokens': 4096}
        result, requests = await self.scenario('success', parameters=parameters)
        self.assertEqual(result['status'], 'passed', result)
        for body in requests:
            self.assertEqual(body['temperature'], .6)
            self.assertEqual(body['max_tokens'], 4096)
        for provider in ('anthropic', 'gemini'):
            with self.subTest(provider=provider):
                result, requests = await self.native_protocol(provider, 'success', parameters)
                self.assertEqual(result['status'], 'passed', result)
                for row in requests:
                    body = row['body']
                    if provider == 'gemini':
                        self.assertEqual(body['generationConfig']['temperature'], .6)
                        # The pinned SDK serializes this protobuf field in
                        # snake_case, as used by its native Gemini transport.
                        self.assertEqual(body['generationConfig']['max_output_tokens'], 4096)
                    else:
                        self.assertEqual(body['temperature'], .6)
                        self.assertEqual(body['max_tokens'], 4096)

    async def test_native_anthropic_and_gemini_wire_plain_schema_and_quality(self):
        for provider in ('anthropic', 'gemini'):
            with self.subTest(provider=provider):
                result, requests = await self.native_protocol(provider, 'success')
                self.assertEqual(result['status'], 'passed', result)
                self.assertTrue(result['plain_passed'] and result['schema_passed'] and result['quality_passed'])
                self.assertEqual(len(requests), 2)

    async def test_redirect_is_not_followed_by_any_vendor_transport(self):
        for provider in ('openai', 'anthropic', 'gemini'):
            with self.subTest(provider=provider):
                result, requests = (await self.scenario('redirect') if provider == 'openai'
                                    else await self.native_protocol(provider, 'redirect'))
                self.assertNotEqual(result['status'], 'passed')
                self.assertEqual(len(requests), 1, 'credentialed redirect was followed')
                self.assertEqual(result['error_code'], 'provider_redirect_rejected', result)

    async def test_production_transport_policy_survives_sdk_client_eviction(self):
        """Direct official adapter calls: no probe observer protects this path."""
        import litellm
        from generation_http import generation_http_policy
        from generation_probe import failure
        from tenacity import stop_after_attempt, wait_none
        from cognee.infrastructure.llm.structured_output_framework.litellm_native.get_native_client import get_native_client
        requests, target_requests = [], []
        async def redirect(request):
            requests.append(request.path)
            return web.Response(status=308, headers={'Location': str(target.make_url('/unconfigured'))})
        async def forbidden(request):
            target_requests.append(request.path)
            return web.Response(status=500)
        target_app, source_app = web.Application(), web.Application()
        target_app.router.add_route('*', '/{path:.*}', forbidden)
        source_app.router.add_post('/{path:.*}', redirect)
        target, source = TestServer(target_app), TestServer(source_app)
        await target.start_server()
        await source.start_server()
        original = (litellm.aclient_session, litellm.client_session, litellm.in_memory_llm_clients_cache)
        try:
            async with generation_http_policy():
                owned_async, owned_sync = litellm.aclient_session, litellm.client_session
                for provider, model in (('custom', 'openai/gpt-4o-2024-08-06'),
                                        ('anthropic', 'anthropic/claude-sonnet-4-6'),
                                        ('gemini', 'gemini/gemini-2.5-flash')):
                    cfg = self.LLMConfig(llm_provider=provider, llm_model=model,
                        llm_api_key='synthetic-only-key', llm_endpoint=str(source.make_url('/v1')),
                        structured_output_framework='litellm_native')
                    token = self.context.set(cfg)
                    try:
                        for iteration in range(2):
                            if iteration:
                                litellm.in_memory_llm_clients_cache.flush_cache()
                            client = get_native_client()
                            complete = client.acreate_structured_output.retry_with(stop=stop_after_attempt(1), wait=wait_none())
                            with self.assertRaises(Exception) as caught, contextlib.redirect_stdout(io.StringIO()):
                                await asyncio.wait_for(complete(client, 'Synthetic input', 'Return text', str, num_retries=0), 15)
                            self.assertEqual(failure(caught.exception), ('failed', 'provider_redirect_rejected'))
                    finally:
                        self.context.reset(token)
                self.assertEqual(len(requests), 6)
                self.assertEqual(target_requests, [], 'SDK contacted an unconfigured second origin')
            self.assertTrue(owned_async.is_closed and owned_sync.is_closed)
            self.assertEqual((litellm.aclient_session, litellm.client_session, litellm.in_memory_llm_clients_cache), original)
        finally:
            await source.close()
            await target.close()

    async def test_native_anthropic_and_gemini_denial_rate_and_truncation(self):
        for provider in ('anthropic', 'gemini'):
            for mode, error in (('401', 'authentication_failed'), ('429', 'rate_or_quota_limit'),
                                ('truncated', 'completion_truncated')):
                with self.subTest(provider=provider, mode=mode):
                    result, requests = await self.native_protocol(provider, mode)
                    self.assertEqual(result['status'], 'failed', result)
                    self.assertEqual(result['error_code'], error)
                    self.assertEqual(len(requests), 2 if mode == 'truncated' else 1)

    async def test_same_model_schema_demotion_is_observed_without_cross_provider_fallback(self):
        result, requests = await self.scenario('json_fallback')
        self.assertEqual(result['status'], 'passed', result)
        self.assertEqual(result['structured_mode'], 'json_object')
        self.assertEqual(len(requests), 4)

    async def test_auth_rate_refusal_and_valid_but_truncated_output_fail_explicitly(self):
        for mode, expected, count in (('401', 'authentication_failed', 1), ('429', 'rate_or_quota_limit', 1),
                                      ('refusal', 'provider_refusal', 2), ('truncated', 'completion_truncated', 2)):
            with self.subTest(mode=mode):
                result, requests = await self.scenario(mode)
                self.assertEqual(result['status'], 'failed', result)
                self.assertEqual(result['error_code'], expected)
                self.assertEqual(len(requests), count, 'unexpected model/SDK retry')
                self.assertFalse(result['quality_passed'])

    async def test_schema_rejection_and_quality_mismatch_are_separate(self):
        for mode, error in (('invalid', 'schema_validation_failed'), ('quality', 'extraction_quality_failed'),
                             ('unsupported', 'structured_output_unsupported')):
            with self.subTest(mode=mode):
                result, _ = await self.scenario(mode)
                self.assertEqual(result['status'], 'failed', result)
                self.assertEqual(result['error_code'], error)
        result, requests = await self.scenario('invalid', native=False)
        self.assertEqual(result['error_code'], 'schema_validation_failed')
        self.assertEqual(len(requests), 4, 'fixed adapter JSON self-correction policy changed')

    async def test_changed_model_or_endpoint_is_rejected_before_network(self):
        client = type('Client', (), {'model': 'selected', 'endpoint': 'https://selected.test', 'api_key': 'private'})()
        async def forbidden(*args, **kwargs):
            raise AssertionError('sent a request to fallback')
        observer = CompletionObservation(client, forbidden)
        for changed in ({'model': 'other'}, {'api_base': 'https://other.test'}, {'api_key': 'other'}):
            with self.assertRaisesRegex(ProbeFailure, ''):
                await observer(**{'model': client.model, 'api_base': client.endpoint, 'api_key': client.api_key, **changed})
        self.assertEqual(observer.calls, 0)

    async def test_provider_timeout_remains_unknown_without_automatic_sdk_retry(self):
        result, requests = await self.scenario('timeout')
        self.assertEqual(result['status'], 'result_unknown', result)
        self.assertEqual(result['error_code'], 'provider_timeout')
        self.assertEqual(len(requests), 1)


if __name__ == '__main__':
    unittest.main()
