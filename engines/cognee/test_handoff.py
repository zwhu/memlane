import asyncio
from contextvars import ContextVar
import os
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import AsyncMock, patch

from fastapi import FastAPI
import httpx

from engines.cognee import PolicyError
from engines.cognee.capacity import EndpointCapacity
from engines.cognee.handoff import ModelHandoffController
from engines.cognee.language_egress import set_default_language


CONFIG = {'provider': 'openai', 'model': 'openai/example', 'endpoint': 'http://localhost:45110/v1'}


def _start_background_run(started):
    async def run():
        started.set()
        await asyncio.Future()
    return asyncio.create_task(run())


class HandoffTests(unittest.IsolatedAsyncioTestCase):
    def setUp(self):
        self.enterContext(patch.dict(os.environ))
        self.addCleanup(set_default_language, 'zh-CN')

    def make_controller(self, **kwargs):
        return ModelHandoffController(factory=dict, background=kwargs.pop('background', lambda: ()), **kwargs)

    async def test_digest_receipt_conflict_and_no_files(self):
        with tempfile.TemporaryDirectory() as directory:
            with patch.dict(os.environ, {'HOME': directory}):
                handoff = self.make_controller()
                for digest in (None, '', 'unconfirmed'):
                    with self.assertRaisesRegex(PolicyError, 'model_digest_mismatch') as caught:
                        handoff.verify_expected_digest(digest)
                    self.assertEqual(caught.exception.status, 409)
                result = await handoff.apply('first', CONFIG, output_language='en')
                self.assertTrue(result['applied'])
                self.assertEqual(await handoff.apply('first', CONFIG, output_language='en'), result)
                with self.assertRaisesRegex(PolicyError, 'operation_id_conflict') as caught:
                    await handoff.apply('first', CONFIG, output_language='fr')
                self.assertEqual(caught.exception.status, 409)
                handoff.verify_expected_digest(result['model_digest'])
                self.assertEqual(handoff.status()['output_language'], 'en')
                await handoff.close()
                self.assertEqual(list(Path(directory).iterdir()), [])

    async def test_missing_cancellation_receipt_blocks_later_attempts(self):
        started = asyncio.Event()
        task = _start_background_run(started)
        await started.wait()
        operation = SimpleNamespace(operation_id='root-operation', user_id='owner')
        verify = AsyncMock(return_value=False)
        handoff = self.make_controller(background=lambda: {task}, stop_timeout=1,
                                      operation_for=lambda _: operation, verify_terminal=verify)
        for attempt in ('first', 'retry'):
            with self.assertRaisesRegex(PolicyError, 'generation_stop_unconfirmed') as caught:
                await handoff.apply(attempt, CONFIG)
            self.assertEqual(caught.exception.status, 409)
            verify.assert_awaited_with([operation])
            self.assertTrue(task.cancelled())
            self.assertEqual(handoff.mode, 'setup_ready')
        verify.return_value = True
        await handoff.apply('retry', CONFIG)
        self.assertEqual(handoff.mode, 'ready')

    async def test_gate_reconfiguration_rolls_back_even_after_partial_mutation(self):
        gate = EndpointCapacity(None)
        handoff = self.make_controller(gate=gate)
        active = await handoff.apply('first', {**CONFIG, 'chunks_per_batch': 2})
        previous = gate.origin, gate.limit, gate.backend_kind
        original = gate.reconfigure

        def partially_fails(endpoint, limit, kind):
            original(endpoint, limit, kind)
            if limit == 4:
                raise RuntimeError('injected reconfigure failure')

        with patch.object(gate, 'reconfigure', side_effect=partially_fails):
            with self.assertRaisesRegex(RuntimeError, 'injected'):
                await handoff.apply('second', {**CONFIG, 'endpoint': 'http://localhost:45111',
                                               'chunks_per_batch': 3, 'max_concurrent_requests': 4})
        self.assertEqual((gate.origin, gate.limit, gate.backend_kind), previous)
        self.assertEqual(os.environ['CHUNKS_PER_BATCH'], '2')
        self.assertEqual(handoff.active_digest, active['model_digest'])
        self.assertFalse(handoff.switching)

    async def test_context_middleware_and_busy_guard(self):
        handoff = self.make_controller()
        await handoff.apply('first', CONFIG)
        context = ContextVar('test_model', default=None)
        app = FastAPI()

        @app.post('/work')
        async def work():
            return {'model': context.get()['LLM_MODEL'], 'active': len(handoff.requests)}

        handoff.install_context_middleware(app, config_context=context)
        async with httpx.AsyncClient(transport=httpx.ASGITransport(app), base_url='http://runner') as client:
            self.assertEqual((await client.post('/work')).json(), {'model': CONFIG['model'], 'active': 1})
            self.assertIsNone(context.get())
            self.assertFalse(handoff.requests)
            handoff.switching = True
            response = await client.post('/work')
            self.assertEqual(response.status_code, 409)
            self.assertEqual(response.json(), {'detail': 'generation_handoff_in_progress'})
            with self.assertRaisesRegex(PolicyError, 'generation_handoff_busy'):
                await handoff.apply('second', CONFIG)


class CapacityTests(unittest.IsolatedAsyncioTestCase):
    async def test_cancelled_waiter_releases_and_stream_retains_slot(self):
        from engines.cognee.capacity import CapacityAsyncTransport

        request = httpx.Request('POST', CONFIG['endpoint'])
        gate = EndpointCapacity(CONFIG['endpoint'], limit=1)
        upstream = httpx.MockTransport(lambda received: httpx.Response(
            200, stream=httpx.ByteStream(received.headers['x-memlane-request-id'].encode())))
        transport = CapacityAsyncTransport(gate, upstream)
        response = await transport.handle_async_request(request)
        self.assertEqual(gate.active, 1)
        waiting = asyncio.create_task(gate.acquire_async(request))
        await asyncio.sleep(0)
        self.assertFalse(waiting.done())
        waiting.cancel()
        with self.assertRaises(asyncio.CancelledError):
            await waiting
        with self.assertRaisesRegex(RuntimeError, 'generation_capacity_busy'):
            gate.reconfigure('http://other', 2, 'other')
        self.assertEqual(await response.aread(), request.headers['x-memlane-request-id'].encode())
        self.assertEqual(gate.active, 0)
        gate.reconfigure('http://other', 2, 'other')
        self.assertIsNone(await gate.acquire_async(request, matching_only=True))
