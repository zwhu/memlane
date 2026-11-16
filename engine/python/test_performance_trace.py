import asyncio
from contextlib import redirect_stderr
import io
import json
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

import httpx
from aiohttp.test_utils import TestClient, TestServer

import codex_completion_endpoint as endpoint
from generation_capacity import CapacityAsyncTransport, EndpointCapacity
from performance_trace import RequestTrace, drain_trace_stream
from test_codex_completion_endpoint import FAKE, SCHEMA


def records(output):
    return [json.loads(line) for line in output.getvalue().splitlines() if line.startswith('{')]


class TraceTests(unittest.TestCase):
    def test_only_public_operation_identifiers_are_carried_without_content(self):
        import sys
        operation = SimpleNamespace(operation_id='00000000-0000-0000-0000-000000000001',
                                    dataset_id='00000000-0000-0000-0000-000000000002',
                                    session_ids=['private-session-marker'], secret='private-secret-marker')
        output = io.StringIO()
        module = SimpleNamespace(get_current_operation=lambda: operation)
        with patch.dict(sys.modules, {'cognee.modules.operations': module}), redirect_stderr(output):
            RequestTrace('generation_http').finish('succeeded')
        self.assertEqual(records(output)[0]['operation_id'], operation.operation_id)
        self.assertEqual(records(output)[0]['dataset_id'], operation.dataset_id)
        self.assertNotIn('private-', output.getvalue())

    def test_monotonic_phases_finish_once_and_untrusted_id_not_logged(self):
        output = io.StringIO()
        with redirect_stderr(output), patch('performance_trace.time.monotonic', side_effect=[1, 1, 1.25, 1.5, 1.5]):
            trace = RequestTrace('synthetic', 'private-injected-value\n')
            trace.transition('queue')
            trace.transition('dispatch')
            trace.finish('succeeded')
            trace.finish('failed')
        rows = records(output)
        self.assertEqual(len(rows), 1)
        self.assertEqual(rows[0]['durations_ms'], {'queue': 250, 'dispatch': 250})
        self.assertNotIn('private-injected-value', output.getvalue())


class RequestTraceTests(unittest.IsolatedAsyncioTestCase):
    async def test_relay_drains_huge_sdk_noise_and_only_forwards_typed_metrics(self):
        output = io.StringIO()
        row = {'schema_version': 1, 'event': 'request_finished', 'component': 'generation_http',
               'request_id': 'a' * 32, 'at': '2026-11-16T00:00:00+00:00', 'elapsed_ms': 12,
               'durations_ms': {'dispatch_to_headers': 11, 'private-secret-marker': 1},
               'outcome': 'succeeded', 'backend_kind': 'anthropic', 'status': 200,
               'prompt': 'private-secret-marker'}
        stream = asyncio.StreamReader()
        stream.feed_data(b'private-secret-marker' * 20000 + b'\n')
        stream.feed_data(json.dumps(row).encode() + b'\n')
        stream.feed_eof()
        with redirect_stderr(output):
            await asyncio.wait_for(drain_trace_stream(stream, 'b' * 32), 2)
        rows = records(output)
        self.assertEqual(len(rows), 1)
        self.assertEqual(rows[0]['backend_kind'], 'anthropic')
        self.assertEqual(rows[0]['validation_request_id'], 'b' * 32)
        self.assertEqual(rows[0]['durations_ms'], {'dispatch_to_headers': 11})
        self.assertNotIn('private-secret-marker', output.getvalue())

    async def test_real_bridge_phase_timings_and_cross_boundary_identity(self):
        import sys
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            auth = root / 'auth'
            auth.mkdir()
            (auth / 'auth.json').write_text('{"private-auth-marker":true}')
            executable = root / 'codex'
            executable.write_text(FAKE.format(python=sys.executable, mode='slow-success'))
            executable.chmod(0o700)
            token = root / 'token'
            token.write_text('private-token-that-must-never-be-logged')
            args = SimpleNamespace(token_file=token, model='fixture-model', codex_executable=executable,
                auth_home=auth, timeout=3, max_concurrency=1, max_inflight=4)
            server = TestClient(TestServer(endpoint.create_app(args), handler_cancellation=True))
            await server.start_server()
            output = io.StringIO()
            try:
                url = str(server.make_url('/v1/chat/completions'))
                gate = EndpointCapacity(url, 2)
                async with httpx.AsyncClient(transport=CapacityAsyncTransport(gate)) as client:
                    with redirect_stderr(output):
                        results = await asyncio.gather(*(client.post(url,
                            headers={'Authorization': 'Bearer ' + token.read_text()},
                            json={'model': 'fixture-model', 'messages': [{'role': 'user', 'content': 'private-prompt-marker'}],
                                  'response_format': {'type': 'json_schema', 'json_schema': {'schema': SCHEMA}}}) for _ in range(2)))
                self.assertTrue(all(result.status_code == 200 for result in results))
                rows = [r for r in records(output) if r['event'] == 'request_finished']
                bridge = [r for r in rows if r['component'] == 'codex_bridge']
                http = [r for r in rows if r['component'] == 'generation_http']
                self.assertEqual(len(bridge), 2)
                self.assertEqual({r['request_id'] for r in bridge}, {r['request_id'] for r in http})
                for r in bridge:
                    self.assertTrue({'bridge_queue', 'workspace_prepare', 'process_spawn', 'rpc_initialize',
                        'thread_start', 'model_turn', 'schema_validation', 'process_cleanup'} <= r['durations_ms'].keys())
                    self.assertEqual(r['outcome'], 'succeeded')
                self.assertGreater(max(r['durations_ms']['bridge_queue'] for r in bridge), 50)
                self.assertEqual(gate.active, 0)
                for private in ['private-auth-marker', 'private-prompt-marker', token.read_text(), str(root)]:
                    self.assertNotIn(private, output.getvalue())
            finally:
                await server.close()

    async def test_cancelled_dispatch_logs_once_and_returns_capacity(self):
        entered = asyncio.Event()
        async def respond(request):
            entered.set()
            await asyncio.Event().wait()
        gate = EndpointCapacity('http://synthetic.test')
        output = io.StringIO()
        async with httpx.AsyncClient(transport=CapacityAsyncTransport(gate, httpx.MockTransport(respond))) as client:
            with redirect_stderr(output):
                task = asyncio.create_task(client.get('http://synthetic.test'))
                await entered.wait()
                task.cancel()
                with self.assertRaises(asyncio.CancelledError):
                    await task
        rows = records(output)
        self.assertEqual(len(rows), 1)
        self.assertEqual(rows[0]['outcome'], 'CancelledError')
        self.assertIn('dispatch_to_headers', rows[0]['durations_ms'])
        self.assertEqual(gate.active, 0)
