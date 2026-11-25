import hashlib
import io
import json
from pathlib import Path
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
                                 ('POST', '/internal/v1/model/apply'), ('POST', '/api/v1/agents/create'),
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
