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
from performance_trace import RequestTrace, drain_trace_stream, relay_trace_line
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

    def test_all_bridges_and_gateway_traces_and_relay_allowlists(self):
        output = io.StringIO()
        with redirect_stderr(output):
            for comp, expected_kind in (
                ('pi_bridge', 'pi'),
                ('traex_bridge', 'traex'),
                ('antigravity_bridge', 'antigravity'),
            ):
                RequestTrace(comp).finish('succeeded')
            RequestTrace('generation_http', backend_kind='antigravity').finish('succeeded')

            for line in (
                {
                    'schema_version': 1, 'event': 'request_finished', 'component': 'pi_bridge',
                    'request_id': 'a' * 32, 'at': '2026-11-21T00:00:00+00:00', 'elapsed_ms': 210,
                    'durations_ms': {'execute': 200}, 'backend_kind': 'pi',
                    'outcome': 'pi_authentication_required', 'secret': 'private-drop',
                },
                {
                    'schema_version': 1, 'event': 'request_finished', 'component': 'antigravity_bridge',
                    'request_id': 'b' * 32, 'at': '2026-11-21T00:00:01+00:00', 'elapsed_ms': 320,
                    'durations_ms': {'execute': 310}, 'backend_kind': 'antigravity',
                    'outcome': 'antigravity_account_location_unavailable',
                },
                {
                    'schema_version': 1, 'event': 'request_finished', 'component': 'traex_bridge',
                    'request_id': 'c' * 32, 'at': '2026-11-21T00:00:02+00:00', 'elapsed_ms': 150,
                    'durations_ms': {'execute': 140}, 'backend_kind': 'traex',
                    'outcome': 'Arbitrary exception text with Bearer tok',
                },
                {
                    'schema_version': 1, 'event': 'request_finished', 'component': 'cognee_gateway',
                    'request_id': 'd' * 32, 'at': '2026-11-21T00:00:03+00:00', 'elapsed_ms': 18,
                    'durations_ms': {'upstream_request': 17}, 'route': 'POST /api/v1/recall',
                    'status': 200, 'hit_count': 2, 'outcome': 'succeeded',
                },
                {
                    'schema_version': 1, 'event': 'request_finished', 'component': 'cognee_gateway',
                    'request_id': 'e' * 32, 'at': '2026-11-21T00:00:04+00:00', 'elapsed_ms': 12,
                    'durations_ms': {}, 'route': '/api/v1/datasets/secret-uuid/data',
                    'outcome': 'timeout',
                },
            ):
                relay_trace_line(json.dumps(line).encode(), 'f' * 32)
        rows = records(output)
        self.assertEqual([r['backend_kind'] for r in rows[:4]], ['pi', 'traex', 'antigravity', 'antigravity'])
        relayed = rows[4:]
        self.assertEqual(len(relayed), 5)
        self.assertEqual((relayed[0]['component'], relayed[0]['backend_kind'], relayed[0]['outcome']),
                         ('pi_bridge', 'pi', 'pi_authentication_required'))
        self.assertEqual((relayed[1]['component'], relayed[1]['backend_kind'], relayed[1]['outcome']),
                         ('antigravity_bridge', 'antigravity', 'antigravity_account_location_unavailable'))
        self.assertEqual((relayed[2]['component'], relayed[2]['backend_kind'], relayed[2]['outcome']),
                         ('traex_bridge', 'traex', 'other_error'))
        self.assertEqual((relayed[3]['component'], relayed[3]['route'], relayed[3]['hit_count']),
                         ('cognee_gateway', 'POST /api/v1/recall', 2))
        self.assertNotIn('route', relayed[4])
        self.assertEqual(relayed[4]['outcome'], 'timeout')
        self.assertNotIn('private-drop', output.getvalue())
        self.assertNotIn('Arbitrary exception text', output.getvalue())
        self.assertNotIn('secret-uuid', output.getvalue())


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
            (auth / 'config.toml').write_text('model="fixture-model"\n')
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
