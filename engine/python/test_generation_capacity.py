"""Synthetic capacity regression: real HTTPX/LiteLLM, no model or data store."""
import asyncio
import unittest
from unittest.mock import patch

import httpx

from generation_capacity import EndpointCapacity, CapacityAsyncTransport, CapacityTransport


class CapacityTests(unittest.IsolatedAsyncioTestCase):
    async def test_real_bridge_admission_with_64_sdk_requests(self):
        import tempfile
        from pathlib import Path
        from types import SimpleNamespace
        from aiohttp.test_utils import TestClient, TestServer
        from openai import AsyncOpenAI
        import litellm
        import codex_completion_endpoint as endpoint
        from generation_http import generation_http_policy
        peak = active = 0
        async def execute(*args):
            nonlocal active, peak
            active += 1
            peak = max(peak, active)
            try:
                await asyncio.sleep(.005)
                return 'ok'
            finally:
                active -= 1
        with tempfile.TemporaryDirectory() as temporary:
            token = Path(temporary) / 'token'
            token.write_text('synthetic-private-bridge-token')
            args = SimpleNamespace(token_file=token, model='synthetic')
            with patch.object(endpoint, 'execute', execute):
                server = TestClient(TestServer(endpoint.create_app(args), handler_cancellation=True))
                await server.start_server()
                try:
                    url = str(server.make_url('/v1'))
                    async with generation_http_policy(endpoint=url) as capacity:
                        sdk = AsyncOpenAI(api_key=token.read_text(), base_url=url,
                            http_client=litellm.aclient_session, max_retries=0)
                        results = await asyncio.wait_for(asyncio.gather(*(sdk.chat.completions.create(
                            model='synthetic', messages=[{'role': 'user', 'content': 'synthetic'}]) for _ in range(64))), 15)
                        self.assertTrue(all(result.choices[0].message.content == 'ok' for result in results))
                        self.assertEqual(peak, 2)
                        self.assertEqual(capacity.active, 0)
                        health = await (await server.get('/health')).json()
                        self.assertEqual(health['active_or_waiting'], 0)
                finally:
                    await server.close()

    async def test_proxy_mounts_and_sdk_retry_keep_same_capacity(self):
        import litellm
        from aiohttp import web
        from aiohttp.test_utils import TestServer
        from openai import AsyncOpenAI
        from generation_http import generation_http_policy
        active = peak = calls = 0
        async def proxy(request):
            nonlocal active, peak, calls
            calls += 1
            attempt = calls
            active += 1
            peak = max(peak, active)
            try:
                await asyncio.sleep(.005)
                if attempt == 1:
                    return web.json_response({'error': {'message': 'synthetic overload'}}, status=429)
                return web.json_response({'id': 'synthetic', 'object': 'chat.completion', 'created': 1,
                    'model': 'synthetic', 'choices': [{'index': 0, 'message': {'role': 'assistant', 'content': 'ok'}, 'finish_reason': 'stop'}]})
            finally:
                active -= 1
        app = web.Application()
        app.router.add_post('/{path:.*}', proxy)
        server = TestServer(app)
        await server.start_server()
        try:
            proxy_url = str(server.make_url('/')).rstrip('/')
            with patch.dict('os.environ', {'HTTP_PROXY': proxy_url, 'HTTPS_PROXY': proxy_url,
                    'ALL_PROXY': proxy_url, 'NO_PROXY': '', 'no_proxy': ''}):
                async with generation_http_policy(endpoint='http://backend.test/v1') as capacity:
                    sdk = AsyncOpenAI(api_key='synthetic', base_url='http://backend.test/v1',
                                      http_client=litellm.aclient_session, max_retries=1)
                    results = await asyncio.wait_for(asyncio.gather(*(sdk.chat.completions.create(
                        model='synthetic', messages=[{'role': 'user', 'content': 'synthetic'}]) for _ in range(16))), 10)
                    self.assertEqual(len(results), 16)
                    self.assertEqual(calls, 17)
                    self.assertEqual(peak, 2)
                    self.assertEqual(capacity.active, 0)
        finally:
            await server.close()

    async def test_policy_protects_shared_endpoint(self):
        from generation_http import generation_http_policy
        import litellm
        from litellm.llms.custom_httpx.http_handler import AsyncHTTPHandler
        async with generation_http_policy(endpoint='http://127.0.0.1:1', max_concurrent_requests=2) as capacity:
            self.assertEqual(capacity.limit, 2)
            handler = AsyncHTTPHandler()
            litellm.in_memory_llm_clients_cache.set_cache('first', handler)
            litellm.in_memory_llm_clients_cache.set_cache('alias', handler)
            await asyncio.sleep(0)
            self.assertIs(handler.client, litellm.aclient_session)
            self.assertFalse(handler.client.is_closed)

    async def test_many_clients_share_slots_and_other_endpoint_bypasses(self):
        capacity = EndpointCapacity('http://backend.test/v1', 2)
        active = peak = 0
        async def handle(request):
            nonlocal active, peak
            active += 1
            peak = max(peak, active)
            try:
                await asyncio.sleep(.005)
                return httpx.Response(200, json={'ok': True})
            finally:
                active -= 1
        clients = [httpx.AsyncClient(transport=CapacityAsyncTransport(capacity, httpx.MockTransport(handle))) for _ in range(4)]
        try:
            results = await asyncio.gather(*(clients[n % 4].post('http://backend.test/v1/chat/completions') for n in range(64)))
            self.assertTrue(all(result.status_code == 200 for result in results))
            self.assertEqual(peak, 2)
            self.assertEqual(capacity.active, 0)
            self.assertFalse(capacity.matches(httpx.Request('POST', 'http://other.test/v1/chat/completions')))
        finally:
            for client in clients:
                await client.aclose()

    async def test_headers_do_not_release_slot_and_cancel_timeout_release(self):
        capacity = EndpointCapacity('http://backend.test', 1, .03)
        release_body = asyncio.Event()
        class DelayedBody(httpx.AsyncByteStream):
            async def __aiter__(self):
                await release_body.wait()
                yield b'ok'
        async def handle(request):
            return httpx.Response(200, stream=DelayedBody())
        async with httpx.AsyncClient(transport=CapacityAsyncTransport(capacity, httpx.MockTransport(handle))) as client:
            response = await client.send(client.build_request('GET', 'http://backend.test'), stream=True)
            self.assertEqual(capacity.active, 1)
            with self.assertRaisesRegex(httpx.PoolTimeout, 'generation_capacity_wait_timeout'):
                await client.get('http://backend.test')
            self.assertEqual(len(capacity._waiting), 0)
            pending = asyncio.create_task(client.get('http://backend.test'))
            await asyncio.sleep(.005)
            pending.cancel()
            await asyncio.gather(pending, return_exceptions=True)
            self.assertEqual(capacity.active, 1)
            self.assertEqual(len(capacity._waiting), 0)
            consuming = asyncio.create_task(response.aread())
            await asyncio.sleep(.005)
            consuming.cancel()
            await asyncio.gather(consuming, return_exceptions=True)
            await response.aclose()
            self.assertEqual(capacity.active, 0)
            release_body.set()
            self.assertEqual((await client.get('http://backend.test')).content, b'ok')

    async def test_sync_thread_and_async_callers_share_capacity(self):
        capacity = EndpointCapacity('http://backend.test', 1)
        ticket = await capacity.acquire_async(httpx.Request('GET', 'http://backend.test'))
        with httpx.Client(transport=CapacityTransport(capacity, httpx.MockTransport(lambda request: httpx.Response(200, text='ok')))) as client:
            pending = asyncio.create_task(asyncio.to_thread(client.get, 'http://backend.test'))
            await asyncio.sleep(.02)
            self.assertFalse(pending.done())
            capacity.release(ticket)
            self.assertEqual((await pending).text, 'ok')
        self.assertEqual(capacity.active, 0)

    async def test_independent_event_loops_share_one_endpoint_capacity(self):
        capacity = EndpointCapacity('http://backend.test', 1)
        async def worker():
            for _ in range(8):
                ticket = await capacity.acquire_async(httpx.Request('GET', 'http://backend.test'))
                try:
                    await asyncio.sleep(.001)
                finally:
                    capacity.release(ticket)
        await asyncio.wait_for(asyncio.gather(*(asyncio.to_thread(lambda: asyncio.run(worker())) for _ in range(4))), 5)
        self.assertEqual(capacity.peak, 1)
        self.assertEqual(capacity.active, 0)

    async def test_transport_failure_and_cancel_race_do_not_leak(self):
        capacity = EndpointCapacity('http://backend.test', 1)
        for _ in range(30):
            first = await capacity.acquire_async(httpx.Request('GET', 'http://backend.test'))
            pending = asyncio.create_task(capacity.acquire_async(httpx.Request('GET', 'http://backend.test')))
            await asyncio.sleep(0)
            capacity.release(first)
            pending.cancel()
            await asyncio.gather(pending, return_exceptions=True)
            self.assertEqual(capacity.active, 0)
        async def fail(request):
            raise httpx.ConnectError('synthetic', request=request)
        async with httpx.AsyncClient(transport=CapacityAsyncTransport(capacity, httpx.MockTransport(fail))) as client:
            with self.assertRaises(httpx.ConnectError):
                await client.get('http://backend.test')
        self.assertEqual(capacity.active, 0)
