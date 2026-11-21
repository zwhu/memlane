"""Tests for ExternalCaptureStore + zero-patch stock Cognee capture receipts."""
import json
import os
from pathlib import Path
import stat
import subprocess
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parent))
ROOT = Path(__file__).resolve().parents[2]

WORKER = r'''
import asyncio
import hashlib
import json
import os
from pathlib import Path
import stat
import sys
from types import SimpleNamespace
from unittest.mock import MagicMock
import uuid

from fastapi import FastAPI, Header, HTTPException
from httpx import ASGITransport, AsyncClient
from sqlalchemy import insert

source = Path(sys.argv[1])
home = Path(sys.argv[2])

from cognee.infrastructure.databases.cache.sql.SqlCacheAdapter import SqlCacheAdapter
from cognee.infrastructure.databases.cache.sql.tables import cache_kv
from cognee.infrastructure.session.coding_unit_extraction import render_trace_input
from cognee.infrastructure.session.session_manager import SessionManager
from capture_receipts import Capture, install_capture_routes
from external_capture_store import ExternalCaptureStore


async def scenario():
    adapter = SqlCacheAdapter(f"sqlite+aiosqlite:///{home / 'cache.sqlite3'}")
    await adapter._ensure_initialized()
    manager = SessionManager(cache_engine=adapter)
    user = SimpleNamespace(id=uuid.uuid4())
    dataset = uuid.uuid4()
    denied = False
    post_commit_crash_once = [False]
    pre_commit_crash_once = [False]

    async def auth(x_api_key: str = Header(default="")):
        if x_api_key != "test-child":
            raise HTTPException(401, "invalid api key")
        return user

    async def resolve_dataset(session_id=None, dataset_name=None, dataset_id=None, user=None):
        if denied:
            raise HTTPException(403, "write denied")

    async def remember(entry, dataset_id=None, session_id=None, user=None):
        if pre_commit_crash_once[0]:
            pre_commit_crash_once[0] = False
            raise RuntimeError("synthetic pre-commit crash")
        if entry.type == "qa":
            eid = await manager.add_qa(
                user_id=str(user.id),
                session_id=session_id,
                question=entry.question,
                context=entry.context,
                answer=entry.answer,
                time=entry.time,
                prompt_time=entry.prompt_time,
            )
        else:
            # Verify dict marker does not leak into Cognee trace input rendering.
            rendered = render_trace_input(SimpleNamespace(origin_function=entry.origin_function, method_params=entry.method_params))
            assert "_memlane_capture" not in rendered, rendered
            eid = await manager.add_agent_trace_step(
                user_id=str(user.id),
                session_id=session_id,
                origin_function=entry.origin_function,
                status=entry.status,
                generate_feedback_with_llm=entry.generate_feedback_with_llm,
                method_params=entry.method_params,
                method_return_value=entry.method_return_value,
                time=entry.time,
            )
        if post_commit_crash_once[0]:
            post_commit_crash_once[0] = False
            raise RuntimeError("synthetic failure after native commit")
        return SimpleNamespace(status="session_stored", entry_type=entry.type, entry_id=eid)

    async def get_session_row(session_id=None, user_id=None):
        return SimpleNamespace(dataset_id=dataset)

    bindings = SimpleNamespace(
        get_authenticated_user=auth,
        get_permitted_dataset_ids=MagicMock(return_value=None),
        get_session_manager=lambda: manager,
        get_session_row=get_session_row,
        resolve_entry_dataset=resolve_dataset,
        remember=remember,
    )

    async def get_permitted(uid):
        return bindings.get_permitted_dataset_ids.return_value

    bindings.get_permitted_dataset_ids = MagicMock(side_effect=get_permitted)
    bindings.get_permitted_dataset_ids.return_value = [dataset]

    ledger_path = home / "capture-receipts.sqlite3"
    receipt_store = ExternalCaptureStore(ledger_path)
    assert stat.S_IMODE(os.stat(ledger_path).st_mode) == 0o600

    # Verify unavailable receipt_store returns 503.
    unconfigured_app = FastAPI()
    install_capture_routes(unconfigured_app, bindings=bindings, receipt_store=None)
    async with AsyncClient(transport=ASGITransport(app=unconfigured_app), base_url="http://127.0.0.1:18999",
                           headers={"X-Api-Key": "test-child"}) as uclient:
        bad_payload = dict(dataset_id=str(dataset), session_id="session", entry=dict(
            type="trace", origin_function="Read", status="success", generate_feedback_with_llm=False))
        assert (await uclient.put("/api/v1/capture/entries/" + str(uuid.uuid4()), json=bad_payload)).status_code == 503

    app = FastAPI()
    install_capture_routes(app, bindings=bindings, receipt_store=receipt_store)
    async with AsyncClient(transport=ASGITransport(app=app), base_url="http://127.0.0.1:18999",
                           headers={"X-Api-Key": "test-child"}) as client:
        # 1. Concurrent duplicate PUT -> 1 trace row, identical receipts
        trace_msg = str(uuid.uuid4())
        payload = dict(
            dataset_id=str(dataset),
            session_id="session",
            entry=dict(
                type="trace",
                origin_function="Read",
                status="success",
                generate_feedback_with_llm=False,
                method_params={"file": "synthetic.py"},
                method_return_value="synthetic",
            ),
        )
        r1, r2 = await asyncio.gather(
            client.put("/api/v1/capture/entries/" + trace_msg, json=payload),
            client.put("/api/v1/capture/entries/" + trace_msg, json=payload),
        )
        assert r1.status_code == 200 and r2.status_code == 200, (r1.text, r2.text)
        assert r1.json() == r2.json()
        assert (await client.get("/api/v1/capture/entries/" + trace_msg,
                                 params={"dataset_id": str(dataset), "session_id": "session"})).json() == r1.json()
        assert len(await manager.get_agent_trace_session(user_id=str(user.id), session_id="session")) == 1

        # 2. Pre-commit crash rolls back pending reservation; retry commits cleanly
        pre_commit_msg = str(uuid.uuid4())
        pre_commit_crash_once[0] = True
        try:
            await client.put("/api/v1/capture/entries/" + pre_commit_msg, json=payload)
            raise AssertionError("expected synthetic pre-commit crash")
        except RuntimeError:
            pass
        assert len(await manager.get_agent_trace_session(user_id=str(user.id), session_id="session")) == 1
        assert (await client.put("/api/v1/capture/entries/" + pre_commit_msg, json=payload)).status_code == 200
        assert len(await manager.get_agent_trace_session(user_id=str(user.id), session_id="session")) == 2

        # 3. Post-commit crash on Trace + QA recovers without duplicating rows
        post_trace_msg = str(uuid.uuid4())
        post_commit_crash_once[0] = True
        recover_trace = await client.put("/api/v1/capture/entries/" + post_trace_msg, json=payload)
        assert recover_trace.status_code == 200, recover_trace.text
        assert len(await manager.get_agent_trace_session(user_id=str(user.id), session_id="session")) == 3

        qa_msg = str(uuid.uuid4())
        qa_payload = dict(
            dataset_id=str(dataset),
            session_id="session",
            entry=dict(type="qa", question="Why zero patch?", answer="Reuse Cognee natively."),
        )
        post_commit_crash_once[0] = True
        recover_qa = await client.put("/api/v1/capture/entries/" + qa_msg, json=qa_payload)
        assert recover_qa.status_code == 200, recover_qa.text
        stored_qas = await manager.get_session(user_id=str(user.id), session_id="session", formatted=False)
        assert len(stored_qas) == 1
        qa_ctx = stored_qas[0].get("context") if isinstance(stored_qas[0], dict) else getattr(stored_qas[0], "context", "")
        assert len(json.loads(qa_ctx)["date"]) == 10, qa_ctx
        # Replay QA message after reopen -> still 1 QA row
        receipt_store.close()
        receipt_store_reopened = ExternalCaptureStore(ledger_path)
        app2 = FastAPI()
        install_capture_routes(app2, bindings=bindings, receipt_store=receipt_store_reopened)
        async with AsyncClient(transport=ASGITransport(app=app2), base_url="http://127.0.0.1:18999",
                               headers={"X-Api-Key": "test-child"}) as client2:
            qa_replay = await client2.put("/api/v1/capture/entries/" + qa_msg, json=qa_payload)
            assert qa_replay.json() == recover_qa.json()
            assert len(await manager.get_session(user_id=str(user.id), session_id="session", formatted=False)) == 1
        receipt_store_reopened.close()
        receipt_store = ExternalCaptureStore(ledger_path)
        app3 = FastAPI()
        install_capture_routes(app3, bindings=bindings, receipt_store=receipt_store)
        async with AsyncClient(transport=ASGITransport(app=app3), base_url="http://127.0.0.1:18999",
                               headers={"X-Api-Key": "test-child"}) as client3:
            # 4. Payload conflict, 401 wrong key, 403 denied, 404 unpermitted GET
            changed = {**payload, "entry": {**payload["entry"], "method_return_value": "different"}}
            assert (await client3.put("/api/v1/capture/entries/" + trace_msg, json=changed)).status_code == 409
            assert (await client3.put("/api/v1/capture/entries/" + trace_msg, json=payload, headers={"X-Api-Key": "wrong"})).status_code == 401
            denied = True
            assert (await client3.put("/api/v1/capture/entries/" + str(uuid.uuid4()), json=payload)).status_code == 403
            denied = False
            bindings.get_permitted_dataset_ids.return_value = []
            assert (await client3.get("/api/v1/capture/entries/" + trace_msg,
                                      params={"dataset_id": str(dataset), "session_id": "session"})).status_code == 404
            bindings.get_permitted_dataset_ids.return_value = [dataset]

            # 5. Legacy cache_kv receipt lookup + conflict guard
            legacy_msg = str(uuid.uuid4())
            legacy_encoded = json.dumps(payload["entry"], sort_keys=True, ensure_ascii=False, allow_nan=False).encode()
            legacy_cap = Capture(str(user.id), str(dataset), "session", legacy_msg, hashlib.sha256(legacy_encoded).hexdigest(), "trace")
            legacy_receipt = legacy_cap.receipt()
            async with adapter.sessionmaker() as db_sess:
                async with db_sess.begin():
                    await db_sess.execute(insert(cache_kv).values(
                        key=legacy_cap.key, value=json.dumps(legacy_receipt, sort_keys=True), expires_at=None,
                    ))
            got_legacy = await client3.get("/api/v1/capture/entries/" + legacy_msg,
                                           params={"dataset_id": str(dataset), "session_id": "session"})
            assert got_legacy.status_code == 200 and got_legacy.json() == legacy_receipt
            put_legacy = await client3.put("/api/v1/capture/entries/" + legacy_msg, json=payload)
            assert put_legacy.status_code == 200 and put_legacy.json() == legacy_receipt
            assert (await client3.put("/api/v1/capture/entries/" + legacy_msg, json=changed)).status_code == 409

            # 6. Prepared host sender drain scenario (lost response -> replay -> backlog check)
            if len(sys.argv) > 3:
                import copy, importlib
                sys.path.insert(0, sys.argv[3])
                import _plugin_common as common, _memlane_policy as policy, _project_memory as routing
                import _memlane_capture as delivery
                common.set_session_key("capture-test-host")
                policy.require = lambda *a, **kw: None
                policy._pending_identity = lambda *a, **kw: None
                routing.route = lambda *a, **kw: {"write": str(dataset)}
                legacy_buf = dict(type="trace", origin_function="Read", status="success", generate_feedback_with_llm=False,
                                  method_params={}, method_return_value="legacy", _replay_ambiguous=True, _buffered_at=1)
                bfile = common._bridge_file("session")
                bkey = common._bridge_cache_key(str(dataset), "session")
                common._write_json_file(bfile, {bkey: {"pending_entries": [copy.deepcopy(legacy_buf)]}})
                common.append_warmup_entry(str(dataset), "session", payload["entry"])
                raw = common._load_json_file(bfile)[bkey]["pending_entries"]
                message_id = raw[1]["_capture_id"]
                loop = asyncio.get_running_loop()
                calls = []
                lost = [False]

                def transport(url, body=None, *, method="POST", timeout=12, **kwargs):
                    calls.append((method, url))
                    async def invoke():
                        response = await client3.request(method, url, json=body)
                        assert response.status_code == 200, response.text
                        return response.json()
                    receipt = asyncio.run_coroutine_threadsafe(invoke(), loop).result(12)
                    if not lost[0]:
                        lost[0] = True
                        raise TimeoutError("synthetic response lost after COMMIT")
                    return receipt

                common._json_http_request = transport
                assert await asyncio.to_thread(common.drain_warmup_entries, str(dataset), "session", budget_seconds=12) == (0, 2)
                raw = common._load_json_file(bfile)
                assert raw[bkey]["pending_entries"][1]["_capture_id"] == message_id
                assert raw[bkey]["pending_entries"][1]["_replay_ambiguous"] is True
                raw[bkey]["capture_retries"][message_id]["after"] = 0
                common._write_json_file(bfile, raw)
                importlib.reload(delivery)
                assert await asyncio.to_thread(common.drain_warmup_entries, str(dataset), "session", budget_seconds=12) == (1, 1)
                assert len(calls) == 2 and calls[0] == calls[1]
                assert len(await manager.get_agent_trace_session(user_id=str(user.id), session_id="session")) == 4

                common.append_warmup_entry(str(dataset), "session", payload["entry"])
                lost[0] = False
                assert await asyncio.to_thread(common.drain_warmup_entries, str(dataset), "session", budget_seconds=12) == (0, 2)
                from project_backlog_worker import check
                common._read_map_record = lambda host: {"session_id": "session"}
                common.get_session_detail_via_http = lambda *a, **kw: {"session_id": "session", "dataset_id": str(dataset)}
                result = await asyncio.to_thread(check, common, policy, str(dataset), "capture-test-host")
                assert result["unknown"] == 1 and result["reason"] == "original_receipt_unavailable"
                assert calls[-1][0] == "GET" and len(calls) == 4, calls
                assert len(await manager.get_agent_trace_session(user_id=str(user.id), session_id="session")) == 5

    receipt_store.close()
    await adapter.close()
    print("CAPTURE_RESULT=" + json.dumps(dict(
        concurrent_retry=True, atomic_rollback=True, reopen_idempotent=True,
        response_loss_recovered=True, payload_conflict=True, acl_preserved=True,
    )))


asyncio.run(scenario())
'''


class CaptureReceiptTests(unittest.TestCase):
    def test_symlink_ledger_rejected(self):
        from external_capture_store import ExternalCaptureStore, ensure_qa_context_date

        # External QA date helper populates missing/JSON contexts and preserves explicit dates or plain text.
        from datetime import datetime
        ts = '2026-11-19T10:15:00+00:00'
        expected_day = datetime.fromisoformat(ts).astimezone().strftime('%Y-%m-%d')
        empty_ctx = ensure_qa_context_date({'type': 'qa', 'question': 'q', 'answer': 'a', 'prompt_time': ts})
        self.assertEqual(json.loads(empty_ctx['context'])['date'], expected_day)
        json_ctx = ensure_qa_context_date({'type': 'qa', 'question': 'q', 'answer': 'a', 'context': '{"cwd":"/repo"}', 'time': ts})
        self.assertEqual(json.loads(json_ctx['context']), {'cwd': '/repo', 'date': expected_day})
        explicit_ctx = {'type': 'qa', 'question': 'q', 'answer': 'a', 'context': '{"date":"2026-11-10"}', 'time': ts}
        self.assertEqual(ensure_qa_context_date(explicit_ctx), explicit_ctx)
        plain_ctx = {'type': 'qa', 'question': 'q', 'answer': 'a', 'context': 'plain notes', 'time': ts}
        self.assertEqual(ensure_qa_context_date(plain_ctx), plain_ctx)

        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            target = root / 'real.sqlite3'
            target.write_text('x')
            link = root / 'link.sqlite3'
            link.symlink_to(target)
            with self.assertRaises(ValueError):
                ExternalCaptureStore(link)

    def test_sqlite_wal_pragmas_and_checkpoint(self):
        from external_capture_store import ExternalCaptureStore
        with tempfile.TemporaryDirectory() as temporary:
            store_path = Path(temporary) / 'capture-receipts.sqlite3'
            store = ExternalCaptureStore(store_path)
            self.assertEqual(store.db.execute('PRAGMA journal_mode').fetchone()[0].lower(), 'wal')
            self.assertEqual(store.db.execute('PRAGMA journal_size_limit').fetchone()[0], 4194304)
            self.assertEqual(store.db.execute('PRAGMA wal_autocheckpoint').fetchone()[0], 1000)
            self.assertTrue(store.checkpoint('TRUNCATE'))
            store.close()

    def test_pinned_native_sqlite_and_http_recovery(self):
        self.run_worker()

    def test_three_prepared_host_senders_keep_original_id_after_response_loss(self):
        import importlib.util
        from project_installation import prepare
        from project_control import private_write

        spec = importlib.util.spec_from_file_location('capture_builder', ROOT / 'scripts/build_project_plugin.py')
        builder = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(builder)
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            source = root / 'plugin'
            builder.build(
                ROOT / '.cache/coding-memory-components/plugin-a176248914fdb32a78354854b300174203b15a5c/integrations/codex/plugins/cognee',
                source,
            )
            private_write(root / 'key', b'test-child')
            private_write(root / 'control', b'c' * 40)
            for kind in ('codex', 'pi', 'traex'):
                home = root / kind
                home.mkdir()
                installed = prepare(
                    source, root / 'managed', kind + '-capture',
                    {'key_file': str(root / 'key'), 'control_token_file': str(root / 'control')},
                    'http://127.0.0.1:18999', 'http://127.0.0.1:18998', str(home), host_kind=kind, python=sys.executable,
                )
                with self.subTest(host=kind):
                    self.run_worker(str(Path(installed['plugin_dir']) / 'scripts'))

    def run_worker(self, scripts=None):
        manifest = json.loads((ROOT / 'engine/coding-memory-components.json').read_text())['service']
        source = ROOT / '.cache/coding-memory-components' / ('service-' + manifest['commit'])
        with tempfile.TemporaryDirectory() as temporary:
            home = Path(temporary)
            env = {k: v for k, v in os.environ.items() if k in ('PATH', 'LANG', 'LC_ALL', 'TMPDIR')}
            env.update(
                HOME=str(home), SYSTEM_ROOT_DIRECTORY=str(home / 'system'), DATA_ROOT_DIRECTORY=str(home / 'data'),
                CACHE_ROOT_DIRECTORY=str(home / 'cache'), LOGS_ROOT_DIRECTORY=str(home / 'logs'),
                COGNEE_LOGS_DIR=str(home / 'logs'), TELEMETRY_DISABLED='true', COGNEE_TRACING_ENABLED='false',
                PYTHON_DOTENV_DISABLED='1', LITELLM_LOCAL_MODEL_COST_MAP='true',
                PYTHONPATH=os.pathsep.join([str(source), str(ROOT / 'engine/python')]),
            )
            if scripts:
                env.update(
                    COGNEE_BASE_URL='http://127.0.0.1:18999', COGNEE_API_KEY='test-child',
                    MEMLANE_PROJECT_CONTROL_URL='http://127.0.0.1:18998',
                )
            result = subprocess.run(
                [sys.executable, '-c', WORKER, str(source), str(home)] + ([scripts] if scripts else []),
                cwd=home, env=env, capture_output=True, text=True, timeout=50,
            )
            self.assertEqual(result.returncode, 0, result.stdout[-1000:] + result.stderr[-4500:])
            report = json.loads(next(line.split('=', 1)[1] for line in result.stdout.splitlines() if line.startswith('CAPTURE_RESULT=')))
            self.assertTrue(all(report.values()))


if __name__ == '__main__':
    unittest.main()
