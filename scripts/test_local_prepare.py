import fcntl
import io
import json
import os
from pathlib import Path
import sys
import tarfile
import tempfile
import unittest
from unittest.mock import patch

import local_prepare as setup


class LocalPrepareTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name).resolve() / 'components'

    def fake_components(self, **overrides):
        from contextlib import ExitStack
        stack = ExitStack()
        self.addCleanup(stack.close)
        for name in ('prepare_python', 'run', 'download_archive', 'prepare_source', 'probe'):
            stack.enter_context(patch.object(setup, name, **overrides.get(name, {})))

    def test_refuses_existing_daily_store_without_mutation(self):
        self.root.mkdir(mode=0o700)
        (self.root / 'system').mkdir()
        (self.root / 'data').mkdir()
        with patch.object(setup, 'run') as run:
            with self.assertRaisesRegex(ValueError, 'unowned'):
                setup.prepare(self.root, '/fake/uv')
        run.assert_not_called()
        self.assertEqual(sorted(p.name for p in self.root.iterdir()), ['data', 'system'])

    def test_symlink_destination_does_not_modify_target(self):
        target = self.root.with_name('daily')
        target.mkdir(mode=0o700)
        self.root.symlink_to(target, target_is_directory=True)
        with self.assertRaisesRegex(ValueError, 'symlink'):
            setup.prepare(self.root, '/fake/uv')
        self.assertEqual(list(target.iterdir()), [])

    def test_missing_component_subdirectory_inside_daily_store_is_rejected(self):
        daily = self.root.with_name('daily')
        daily.mkdir(mode=0o700)
        config = self.root.with_name('runtime.json')
        config.write_text(json.dumps({'official_data': str(daily)}))
        with patch.object(setup, 'DAILY_CONFIG', config):
            with self.assertRaisesRegex(ValueError, 'separate'):
                setup.prepare(daily / 'new-components', '/fake/uv')
        self.assertEqual(list(daily.iterdir()), [])

    def test_failure_retains_completed_steps_and_resume_does_not_create_storage(self):
        self.fake_components(probe={'side_effect': [RuntimeError('probe failed'), None]})
        with self.assertRaisesRegex(RuntimeError, 'probe failed'):
            setup.prepare(self.root, '/fake/uv')
        state = json.loads((self.root / 'status.json').read_text())
        self.assertEqual(state['status'], 'failed')
        self.assertEqual(state['step'], 'probe')
        self.assertEqual(state['completed'], ['python', 'dependencies', 'archive', 'source'])
        result = setup.prepare(self.root, '/fake/uv')
        self.assertEqual(result['status'], 'components_ready')
        self.assertFalse(result['storage_initialized'])
        self.assertFalse((self.root / 'system').exists())
        self.assertFalse((self.root / 'data').exists())
        self.assertEqual((self.root / 'status.json').stat().st_mode & 0o777, 0o600)
        self.assertEqual(self.root.stat().st_mode & 0o777, 0o700)

    def test_concurrent_prepare_and_changed_inputs_are_rejected(self):
        self.fake_components()
        setup.prepare(self.root, '/fake/uv')
        with (self.root / '.prepare.lock').open('a+') as lock:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            with self.assertRaises(BlockingIOError):
                setup.prepare(self.root, '/fake/uv')
        before = (self.root / 'status.json').read_bytes()
        with patch.object(setup, 'PYTHON_VERSION', '3.12.99'):
            with self.assertRaisesRegex(ValueError, 'inputs changed'):
                setup.prepare(self.root, '/fake/uv')
        self.assertEqual((self.root / 'status.json').read_bytes(), before)

    def test_download_hash_failure_does_not_publish_archive(self):
        self.root.mkdir(mode=0o700)
        with patch.object(setup.urllib.request, 'urlopen', return_value=io.BytesIO(b'corrupt')):
            with self.assertRaisesRegex(ValueError, 'digest mismatch'):
                setup.download_archive(self.root / 'service.tar.gz',
                    {'archive_url': 'https://example.invalid/fixed', 'archive_sha256': '0' * 64})
        self.assertEqual(list(self.root.iterdir()), [])

    def test_archive_traversal_and_links_never_publish_source(self):
        self.root.mkdir(mode=0o700)
        for name, kind in [('repo/../../escape', tarfile.REGTYPE), ('repo/cognee/link', tarfile.SYMTYPE)]:
            with self.subTest(name=name):
                archive = self.root / 'bad.tar'
                with tarfile.open(archive, 'w') as bundle:
                    member = tarfile.TarInfo(name)
                    member.type = kind
                    member.linkname = '/tmp'
                    bundle.addfile(member)
                with self.assertRaisesRegex(ValueError, 'unsafe'):
                    setup.extract_source(archive, self.root / 'source')
                self.assertFalse((self.root / 'source').exists())

    def test_upstream_agent_symlink_is_not_part_of_the_service_component(self):
        self.root.mkdir(mode=0o700)
        archive = self.root / 'upstream.tar'
        with tarfile.open(archive, 'w') as bundle:
            link = tarfile.TarInfo('repo/.claude/skills')
            link.type, link.linkname = tarfile.SYMTYPE, '../skills'
            bundle.addfile(link)
            content = tarfile.TarInfo('repo/cognee/__init__.py')
            content.size = 4
            bundle.addfile(content, io.BytesIO(b'pass'))
        setup.extract_source(archive, self.root / 'source')
        self.assertEqual((self.root / 'source/cognee/__init__.py').read_bytes(), b'pass')
        self.assertFalse((self.root / 'source/.claude').exists())

    def test_dependency_install_is_hash_checked_and_host_overrides_are_ignored(self):
        self.fake_components()
        with patch.dict(os.environ, {'UV_INDEX_URL': 'https://bad.invalid', 'PYTHONPATH': '/bad',
                                    'PIP_TARGET': '/bad', 'VIRTUAL_ENV': '/bad'}):
            setup.prepare(self.root, '/fake/uv')
        command = setup.run.call_args.args[0]
        self.assertIn('--require-hashes', command)
        self.assertIn('--build-constraints', command)
        self.assertIn('--no-config', command)
        env = setup.run.call_args.kwargs['env']
        for key in ('UV_INDEX_URL', 'PIP_TARGET', 'PYTHONPATH', 'VIRTUAL_ENV'):
            self.assertNotIn(key, env)
        self.assertEqual(env['UV_PYTHON_INSTALL_DIR'], str(self.root / 'python'))

    def test_prepare_cli_does_not_require_or_create_runtime_config(self):
        import dev
        missing = self.root.with_name('missing-runtime.json')
        with patch.object(sys, 'argv', ['dev.py', 'prepare', '--component-root', str(self.root),
                                       '--config', str(missing)]), \
                patch.object(setup, 'prepare', return_value={'status': 'components_ready'}) as prepare, \
                patch.object(dev, 'load_config', side_effect=AssertionError('runtime must not be loaded')):
            dev.main()
        prepare.assert_called_once_with(self.root, None)
        self.assertFalse(missing.exists())

    def test_capability_probe_isolates_home_cwd_and_cleans_import_side_effects(self):
        self.root.mkdir(mode=0o700)
        def importing(command, *, env, cwd):
            self.assertEqual(env['HOME'], cwd)
            self.assertEqual(env['COGNEE_LOGS_DIR'], cwd + '/logs')
            Path(cwd, 'import-created-file').touch()
        with patch.object(setup, 'run', side_effect=importing):
            setup.probe(self.root / 'venv/bin/python', self.root / 'source', {})
        self.assertEqual(list(self.root.iterdir()), [])


if __name__ == '__main__':
    unittest.main()
