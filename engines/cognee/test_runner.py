import hashlib
import io
import json
import os
from pathlib import Path
import resource
import subprocess
import sys
import tarfile
import tempfile
import unittest
from unittest.mock import AsyncMock, patch

import httpx
from starlette.responses import JSONResponse

from engines.cognee import PolicyError, private_write, read_secret
from engines.cognee import runner


class SourceTests(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name).resolve()
        self.source = self.root / 'source'
        self.module = self.source / 'cognee' / '__init__.py'
        self.module.parent.mkdir(parents=True)
        data = b'value = 1\n'
        self.module.write_bytes(data)
        self.archive = self.root / 'source.tar.gz'
        with tarfile.open(self.archive, 'w:gz') as archive:
            member = tarfile.TarInfo('pinned/cognee/__init__.py')
            member.size = len(data)
            archive.addfile(member, io.BytesIO(data))
        manifest = self.root / 'manifest.json'
        manifest.write_text(json.dumps({'service': {
            'packages': ['cognee'], 'commit': 'pinned',
            'archive_sha256': hashlib.sha256(self.archive.read_bytes()).hexdigest()}}))
        self.enterContext(patch.object(runner, 'COMPONENT_MANIFEST', manifest))
        self.config = {'official_source': self.source, 'official_archive': self.archive}

    def test_full_verification_and_explicit_cache(self):
        self.assertEqual(runner.verify_source(self.config), self.source)
        self.assertEqual({path.name for path in self.root.iterdir()}, {'source', 'source.tar.gz', 'manifest.json'})
        config = {**self.config, 'verify_cache_path': self.root / 'cache.json'}
        runner.verify_source(config)
        with patch.object(runner.tarfile, 'open', side_effect=AssertionError('archive reopened')):
            self.assertEqual(runner.verify_source(config), self.source)
        self.module.write_text('different source')
        with self.assertRaisesRegex(ValueError, 'differs from pinned archive'):
            runner.verify_source(config)

    def test_bytecode_rejected_on_full_and_cache_paths(self):
        config = {**self.config, 'verify_cache_path': self.root / 'cache.json'}
        runner.verify_source(config)
        for suffix in ('.pyc', '.pyo'):
            bytecode = self.module.with_suffix(suffix)
            bytecode.write_bytes(b'bytecode')
            for configuration in (self.config, config):
                with self.subTest(suffix=suffix, cache='verify_cache_path' in configuration):
                    with self.assertRaisesRegex(ValueError, 'bytecode'):
                        runner.verify_source(configuration)
            with self.assertRaises(ImportError):
                runner._SourceOnlyFileLoader('cognee', str(bytecode)).get_code('cognee')
            bytecode.unlink()
        code = runner._SourceOnlyFileLoader('cognee', str(self.module)).get_code('cognee')
        self.assertIn('value', code.co_names)

    def test_symlinks_rejected(self):
        link = self.root / 'linked.tar.gz'
        link.symlink_to(self.archive)
        with self.assertRaisesRegex(ValueError, 'linked'):
            runner.verify_source({**self.config, 'official_archive': link})
        other = self.module.rename(self.root / 'elsewhere.py')
        self.module.symlink_to(other)
        with self.assertRaisesRegex(ValueError, 'differs'):
            runner.verify_source(self.config)

    def test_private_token_round_trip_and_modes(self):
        path = self.root / 'private' / 'token'
        private_write(path, b'secret\n')
        self.assertEqual(path.stat().st_mode & 0o777, 0o600)
        self.assertEqual(runner.load_internal_token({'internal_token_file': path}), 'secret')
        link = path.with_name('linked')
        link.symlink_to(path)
        with self.assertRaises(OSError):
            private_write(link, b'unexpected')
        path.chmod(0o644)
        with self.assertRaisesRegex(PolicyError, 'private_credential_required'):
            read_secret(path)

    def test_configure_store_disables_session_ttl(self):
        store_root = self.root / 'store'
        store_root.mkdir(mode=0o700)
        with patch.dict(os.environ, {'SESSION_TTL_SECONDS': '604800'}, clear=False):
            runner._configure_store({}, store_root)
            self.assertEqual(os.environ['CACHE_BACKEND'], 'sqlite')
            self.assertEqual(os.environ['CACHING'], 'true')
            self.assertEqual(os.environ['SESSION_TTL_SECONDS'], '0')
            from cognee.infrastructure.databases.cache.config import CacheConfig

            self.assertEqual(CacheConfig().session_ttl_seconds, 0)


class SetupTests(unittest.IsolatedAsyncioTestCase):
    async def test_allowlist_websocket_and_transition(self):
        setup = True
        async def downstream(scope, receive, send):
            await JSONResponse({'downstream': True})(scope, receive, send)
        app = runner.SetupOnlyMiddleware(downstream, is_setup_only=lambda: setup)
        async with httpx.AsyncClient(transport=httpx.ASGITransport(app), base_url='http://runner') as client:
            for path in ('/health', '/health/'):
                response = await client.get(path)
                self.assertEqual(response.json(), {'status': 'setup_ready', 'generation': 'unconfigured',
                                                   'checks': 'liveness_only'})
                self.assertEqual(response.headers['cache-control'], 'no-store')
            for method, path in (('GET', '/internal/v1/health'), ('GET', '/internal/v1/model/status'),
                                 ('POST', '/internal/v1/model/apply'), ('POST', '/internal/v1/datasets'),
                                 ('POST', '/api/v1/agents/create'),
                                 ('GET', '/api/v1/datasets/id/data/count')):
                self.assertEqual((await client.request(method, path)).json(), {'downstream': True})
            for path in ('/internal/v1/items', '/api/v1/permissions//x', '/api/v1/permissions/%2e/x',
                         '/api/v1/permissions/%2e%2e/x'):
                response = await client.get(path)
                self.assertEqual(response.status_code, 503)
                self.assertEqual(response.headers['cache-control'], 'no-store')
            send = AsyncMock()
            await app({'type': 'websocket', 'path': '/'}, AsyncMock(), send)
            send.assert_awaited_once_with({'type': 'websocket.close', 'code': 1008})
            setup = False
            self.assertEqual((await client.get('/internal/v1/items')).json(), {'downstream': True})


class NoFileLimitTests(unittest.TestCase):
    def test_raise_nofile_limit_darwin_infinity_and_step_down(self):
        inf = resource.RLIM_INFINITY
        state = [256, inf]
        calls = []

        def fake_get(_res):
            return tuple(state)

        def fake_set(_res, pair):
            calls.append(pair)
            if pair[0] > 10240:
                raise ValueError('not allowed')
            state[0], state[1] = pair

        with (patch.object(runner, '_darwin_maxfilesperproc', return_value=138240),
              patch.object(runner.resource, 'getrlimit', side_effect=fake_get),
              patch.object(runner.resource, 'setrlimit', side_effect=fake_set)):
            got = runner.raise_nofile_limit()
        self.assertEqual(got, (10240, inf))
        self.assertEqual(calls, [(65536, inf), (16384, inf), (10240, inf)])

    def test_raise_nofile_limit_respects_hard_cap_and_never_downgrades(self):
        inf = resource.RLIM_INFINITY
        with (patch.object(runner, '_darwin_maxfilesperproc', return_value=138240),
              patch.object(runner.resource, 'getrlimit', return_value=(131072, inf)),
              patch.object(runner.resource, 'setrlimit') as mock_set):
            self.assertEqual(runner.raise_nofile_limit(), (131072, inf))
            mock_set.assert_not_called()

        state = [256, 8192]
        calls = []

        def fake_set(_res, pair):
            calls.append(pair)
            state[0], state[1] = pair

        with (patch.object(runner, '_darwin_maxfilesperproc', return_value=138240),
              patch.object(runner.resource, 'getrlimit', side_effect=lambda _: tuple(state)),
              patch.object(runner.resource, 'setrlimit', side_effect=fake_set)):
            self.assertEqual(runner.raise_nofile_limit(), (8192, 8192))
        self.assertEqual(calls, [(8192, 8192)])

    def test_raise_nofile_limit_real_os_and_spawn_child_inheritance(self):
        script = (
            'import json, multiprocessing, resource\n'
            'from engines.cognee.runner import raise_nofile_limit\n'
            'def _child(q):\n'
            '    q.put(resource.getrlimit(resource.RLIMIT_NOFILE)[0])\n'
            'if __name__ == "__main__":\n'
            '    soft, hard = resource.getrlimit(resource.RLIMIT_NOFILE)\n'
            '    resource.setrlimit(resource.RLIMIT_NOFILE, (256, hard))\n'
            '    before = resource.getrlimit(resource.RLIMIT_NOFILE)[0]\n'
            '    after = raise_nofile_limit()[0]\n'
            '    ctx = multiprocessing.get_context("spawn")\n'
            '    q = ctx.Queue()\n'
            '    p = ctx.Process(target=_child, args=(q,))\n'
            '    p.start()\n'
            '    child_soft = q.get(timeout=10)\n'
            '    p.join(timeout=10)\n'
            '    print(json.dumps({"before": before, "after": after, "child": child_soft}))\n'
        )
        with tempfile.TemporaryDirectory() as tmpdir:
            script_path = Path(tmpdir) / 'rlimit_probe.py'
            script_path.write_text(script)
            proc = subprocess.run(
                [sys.executable, str(script_path)],
                cwd=str(Path(__file__).resolve().parents[2]),
                env={**os.environ, 'PYTHONPATH': str(Path(__file__).resolve().parents[2])},
                capture_output=True,
                text=True,
                check=False,
            )
        self.assertEqual(proc.returncode, 0, msg=proc.stderr)
        data = json.loads(proc.stdout.strip().splitlines()[-1])
        self.assertEqual(data['before'], 256)
        self.assertGreaterEqual(data['after'], 4096)
        self.assertGreaterEqual(data['child'], 4096)
