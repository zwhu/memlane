"""Tests for official_service_dev.verify_source cold-start cache and security fallbacks."""
import hashlib
import io
import json
import os
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest.mock import patch

import official_service_dev as service_dev


def _build_pinned_fixture(root, *, extra_entries=None, commit='1' * 40):
    source = root / 'source'
    source.mkdir(parents=True, exist_ok=True)
    archive = root / 'cognee-pinned.tar.gz'
    entries = {
        'pyproject.toml': b'[project]\nname = "cognee"\nversion = "1.6.7"\n',
        'cognee/__init__.py': b'__version__ = "1.6.7"\n',
        'cognee/api/client.py': b'app = object()\n',
        'cognee/prompts/sample.txt': b'pinned prompt text\n',
        'kuzu/__init__.py': b'shim = True\n',
    }
    if extra_entries:
        entries.update(extra_entries)
    with tarfile.open(archive, 'w:gz') as bundle:
        for rel, content in entries.items():
            info = tarfile.TarInfo(name='cognee-' + commit + '/' + rel)
            info.size = len(content)
            bundle.addfile(info, io.BytesIO(content))
            target = source / rel
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(content)
    archive_sha = hashlib.sha256(archive.read_bytes()).hexdigest()
    manifest_path = root / 'coding-memory-components.json'
    manifest = {
        'protocol': 1,
        'service': {
            'commit': commit,
            'archive_sha256': archive_sha,
            'packages': ['cognee', 'kuzu'],
        },
    }
    manifest_path.write_text(json.dumps(manifest), encoding='utf-8')
    return source, archive, manifest_path, manifest['service']


class VerifySourceCacheTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name).resolve()
        self.source, self.archive, self.manifest_path, self.service_manifest = _build_pinned_fixture(self.root)
        self.state_dir = self.root / 'state'
        self.state_dir.mkdir(mode=0o700)
        self.config = {
            'official_source': str(self.source),
            'official_archive': str(self.archive),
            'project_control': {'registry_file': str(self.state_dir / 'projects.json')},
        }
        self.cache_path = self.state_dir / 'engine-components' / 'source-verify-cache.json'
        self.manifest_patch = patch.object(service_dev, 'COMPONENT_MANIFEST', self.manifest_path)
        self.manifest_patch.start()
        self.addCleanup(self.manifest_patch.stop)

    def test_cache_write_0600_and_fast_hit_skips_tar_and_hashing(self):
        with patch.object(service_dev.tarfile, 'open', wraps=tarfile.open) as open_tar:
            resolved = service_dev.verify_source(self.config)
            self.assertEqual(resolved, self.source)
            self.assertEqual(open_tar.call_count, 1)

        self.assertTrue(self.cache_path.is_file())
        self.assertFalse(self.cache_path.is_symlink())
        self.assertEqual(self.cache_path.stat().st_mode & 0o777, 0o600)
        self.assertEqual(self.cache_path.parent.stat().st_mode & 0o777, 0o700)

        cached = json.loads(self.cache_path.read_text(encoding='utf-8'))
        self.assertEqual(cached['schema_version'], 2)
        self.assertEqual(cached['commit'], self.service_manifest['commit'])
        self.assertEqual(cached['archive_sha256'], self.service_manifest['archive_sha256'])
        self.assertEqual(cached['archive_path'], str(self.archive))
        self.assertEqual(cached['source_path'], str(self.source))
        archive_stat = self.archive.stat()
        self.assertEqual(cached['archive_stat'], {'mtime_ns': archive_stat.st_mtime_ns, 'size': archive_stat.st_size})
        self.assertEqual(
            set(cached['files']),
            {'pyproject.toml', 'cognee/__init__.py', 'cognee/api/client.py', 'cognee/prompts/sample.txt', 'kuzu/__init__.py'},
        )
        for rel, meta in cached['files'].items():
            self.assertEqual(len(meta), 3, rel)
            self.assertEqual(meta[2], hashlib.sha256((self.source / rel).read_bytes()).hexdigest())
        self.assertEqual(
            cached['py_files'],
            ['cognee/__init__.py', 'cognee/api/client.py', 'kuzu/__init__.py'],
        )

        # Clean directory hits fast-path cache without opening the tar archive.
        with patch.object(service_dev.tarfile, 'open', side_effect=AssertionError('tar opened on cache hit')):
            self.assertEqual(service_dev.verify_source(self.config), self.source)

        # Unverified bytecode (.pyc) inside source must invalidate cache and force full archive verification.
        pycache = self.source / 'cognee' / '__pycache__'
        pycache.mkdir()
        pyc_file = pycache / '__init__.cpython-312.pyc'
        pyc_file.write_bytes(b'synthetic-pyc')
        with patch.object(service_dev.tarfile, 'open', wraps=tarfile.open) as open_tar:
            self.assertEqual(service_dev.verify_source(self.config), self.source)
            self.assertEqual(open_tar.call_count, 1)

        # Removing bytecode allows fast-path cache hit again.
        pyc_file.unlink()
        pycache.rmdir()
        with patch.object(service_dev.tarfile, 'open', side_effect=AssertionError('tar opened on clean hit')):
            self.assertEqual(service_dev.verify_source(self.config), self.source)

    def test_same_mtime_same_size_tampering_invalidates_cache_and_source_only_hook_blocks_pyc(self):
        import importlib
        import importlib.util
        import py_compile
        import sys

        service_dev.verify_source(self.config)

        # Same-size AND identical-mtime_ns tampering must fail content SHA256 cache check and full archive verify.
        client_py = self.source / 'cognee/api/client.py'
        original = client_py.read_bytes()
        original_stat = client_py.stat()
        tampered = b'app = object()#'  # exact same length (15 bytes) as b'app = object()\n'
        self.assertEqual(len(tampered), len(original))
        client_py.write_bytes(tampered)
        os.utime(client_py, ns=(original_stat.st_atime_ns, original_stat.st_mtime_ns))
        with patch.object(service_dev.tarfile, 'open', wraps=tarfile.open) as open_tar:
            with self.assertRaisesRegex(ValueError, 'official package differs from pinned archive'):
                service_dev.verify_source(self.config)
            self.assertEqual(open_tar.call_count, 1)
        client_py.write_bytes(original)
        os.utime(client_py, ns=(original_stat.st_atime_ns, original_stat.st_mtime_ns))

        # Source-only import hook ignores a pre-planted poisoned .pyc matching source mtime+size.
        mod_dir = self.root / 'hook_source'
        pkg = mod_dir / 'verified_pkg_test_only'
        pkg.mkdir(parents=True)
        mod_file = pkg / '__init__.py'
        mod_file.write_text('VALUE = "poisoned"\n', encoding='utf-8')
        pyc_path = Path(importlib.util.cache_from_source(str(mod_file)))
        py_compile.compile(str(mod_file), cfile=str(pyc_path), doraise=True)
        pyc_stat = mod_file.stat()
        # Overwrite .py with legitimate code of identical byte length and identical mtime_ns.
        legit = 'VALUE = "verified"\n'
        self.assertEqual(len(legit), len('VALUE = "poisoned"\n'))
        mod_file.write_text(legit, encoding='utf-8')
        os.utime(mod_file, ns=(pyc_stat.st_atime_ns, pyc_stat.st_mtime_ns))

        hook = service_dev._install_source_only_import_hook(mod_dir)
        sys.path.insert(0, str(mod_dir))
        try:
            sys.modules.pop('verified_pkg_test_only', None)
            loaded = importlib.import_module('verified_pkg_test_only')
            self.assertEqual(loaded.VALUE, 'verified')
        finally:
            sys.modules.pop('verified_pkg_test_only', None)
            if str(mod_dir) in sys.path:
                sys.path.remove(str(mod_dir))
            if hook in sys.path_hooks:
                sys.path_hooks.remove(hook)
            sys.path_importer_cache.pop(str(mod_dir), None)
            sys.path_importer_cache.pop(str(pkg), None)

    def test_official_data_cache_path_and_no_cache_when_stateless(self):
        data_dir = self.root / 'daily-data'
        data_dir.mkdir(mode=0o700)
        config = {
            'official_source': str(self.source),
            'official_archive': str(self.archive),
            'official_data': str(data_dir),
        }
        cache_path = data_dir / 'memlane-state' / 'source-verify-cache.json'
        self.assertEqual(service_dev.verify_source(config), self.source)
        self.assertTrue(cache_path.is_file())
        self.assertEqual(cache_path.stat().st_mode & 0o777, 0o600)
        self.assertEqual(cache_path.parent.stat().st_mode & 0o777, 0o700)
        with patch.object(service_dev.tarfile, 'open', side_effect=AssertionError('tar opened on cache hit')):
            self.assertEqual(service_dev.verify_source(config), self.source)

        # Callers without project_control or official_data (e.g. first_install_store) verify without cache.
        stateless = {'official_source': str(self.source), 'official_archive': str(self.archive)}
        with patch.object(service_dev.tarfile, 'open', wraps=tarfile.open) as open_tar:
            self.assertEqual(service_dev.verify_source(stateless), self.source)
            self.assertEqual(open_tar.call_count, 1)

    def test_fallback_when_source_files_are_modified(self):
        service_dev.verify_source(self.config)

        # Modify a Python module (same size, bumped mtime_ns).
        client_py = self.source / 'cognee/api/client.py'
        original = client_py.read_bytes()
        original_stat = client_py.stat()
        client_py.write_bytes(b'app = 123456()\n')
        os.utime(client_py, ns=(original_stat.st_atime_ns, original_stat.st_mtime_ns + 10_000))
        with patch.object(service_dev.tarfile, 'open', wraps=tarfile.open) as open_tar:
            with self.assertRaisesRegex(ValueError, 'official package differs from pinned archive'):
                service_dev.verify_source(self.config)
            self.assertEqual(open_tar.call_count, 1)
        client_py.write_bytes(original)
        service_dev.verify_source(self.config)

        # Modify a non-Python package file tracked from the tar.
        prompt_txt = self.source / 'cognee/prompts/sample.txt'
        prompt_original = prompt_txt.read_bytes()
        prompt_txt.write_bytes(b'tampered prompt text\n')
        with self.assertRaisesRegex(ValueError, 'official package differs from pinned archive'):
            service_dev.verify_source(self.config)
        prompt_txt.write_bytes(prompt_original)
        service_dev.verify_source(self.config)

        # Modify pyproject.toml metadata.
        pyproject = self.source / 'pyproject.toml'
        pyproject.write_bytes(b'[project]\nname = "tampered"\n')
        with self.assertRaisesRegex(ValueError, 'official package metadata differs from pinned archive'):
            service_dev.verify_source(self.config)

    def test_fallback_when_unexpected_python_module_added(self):
        service_dev.verify_source(self.config)

        # Plain extra .py file.
        extra = self.source / 'cognee/injected.py'
        extra.write_text('injected = True\n', encoding='utf-8')
        with patch.object(service_dev.tarfile, 'open', wraps=tarfile.open) as open_tar:
            with self.assertRaisesRegex(ValueError, 'unexpected official Python module'):
                service_dev.verify_source(self.config)
            self.assertEqual(open_tar.call_count, 1)
        extra.unlink()

        # Extra .py hidden inside __pycache__.
        pycache_extra = self.source / 'cognee/__pycache__/sneaky.py'
        pycache_extra.parent.mkdir(exist_ok=True)
        pycache_extra.write_text('sneaky = True\n', encoding='utf-8')
        with self.assertRaisesRegex(ValueError, 'unexpected official Python module'):
            service_dev.verify_source(self.config)
        pycache_extra.unlink()

        # Symlinked extra .py file.
        outside = self.root / 'outside.py'
        outside.write_text('outside = True\n', encoding='utf-8')
        linked = self.source / 'cognee/linked.py'
        linked.symlink_to(outside)
        with self.assertRaisesRegex(ValueError, 'unexpected official Python module'):
            service_dev.verify_source(self.config)

    def test_fallback_when_source_file_or_directory_replaced_with_symlink(self):
        service_dev.verify_source(self.config)

        # Replace a verified file with a symlink pointing to identical content & timestamps.
        client_py = self.source / 'cognee/api/client.py'
        original = client_py.read_bytes()
        original_stat = client_py.stat()
        shadow = self.root / 'shadow-client.py'
        shadow.write_bytes(original)
        os.utime(shadow, ns=(original_stat.st_atime_ns, original_stat.st_mtime_ns))
        client_py.unlink()
        client_py.symlink_to(shadow)
        with patch.object(service_dev.tarfile, 'open', wraps=tarfile.open) as open_tar:
            with self.assertRaisesRegex(ValueError, 'official package differs from pinned archive'):
                service_dev.verify_source(self.config)
            self.assertEqual(open_tar.call_count, 1)

        # Restore file, warm cache, then replace intermediate directory with a symlink.
        client_py.unlink()
        client_py.write_bytes(original)
        service_dev.verify_source(self.config)
        api_dir = self.source / 'cognee/api'
        shadow_dir = self.root / 'shadow-api'
        shadow_dir.mkdir()
        shadow_client = shadow_dir / 'client.py'
        shadow_client.write_bytes(original)
        client_stat = client_py.stat()
        os.utime(shadow_client, ns=(client_stat.st_atime_ns, client_stat.st_mtime_ns))
        client_py.unlink()
        api_dir.rmdir()
        api_dir.symlink_to(shadow_dir)
        with patch.object(service_dev.tarfile, 'open', wraps=tarfile.open) as open_tar:
            with self.assertRaisesRegex(ValueError, 'official package differs from pinned archive'):
                service_dev.verify_source(self.config)
            self.assertEqual(open_tar.call_count, 1)

    def test_fallback_when_archive_changes_or_is_symlink(self):
        service_dev.verify_source(self.config)

        # Replacing archive bytes changes stat -> falls back and detects digest mismatch.
        original_archive = self.archive.read_bytes()
        self.archive.write_bytes(b'corrupted archive payload')
        with self.assertRaisesRegex(ValueError, 'fixed official archive digest mismatch'):
            service_dev.verify_source(self.config)

        # Replace archive with a symlink pointing to valid archive bytes: must never fast-hit.
        real_copy = self.root / 'real-copy.tar.gz'
        real_copy.write_bytes(original_archive)
        self.archive.unlink()
        self.archive.symlink_to(real_copy)
        with patch.object(service_dev.tarfile, 'open', wraps=tarfile.open) as open_tar:
            self.assertEqual(service_dev.verify_source(self.config), self.source)
            self.assertEqual(open_tar.call_count, 1)
            self.assertEqual(service_dev.verify_source(self.config), self.source)
            self.assertEqual(open_tar.call_count, 2)

    def test_corrupt_insecure_or_symlink_cache_falls_back_and_atomically_rewrites(self):
        service_dev.verify_source(self.config)

        # 1. Corrupt JSON cache.
        self.cache_path.write_text('{not-json', encoding='utf-8')
        with patch.object(service_dev.tarfile, 'open', wraps=tarfile.open) as open_tar:
            self.assertEqual(service_dev.verify_source(self.config), self.source)
            self.assertEqual(open_tar.call_count, 1)
        self.assertEqual(self.cache_path.stat().st_mode & 0o777, 0o600)
        with patch.object(service_dev.tarfile, 'open', side_effect=AssertionError('tar opened after rewrite')):
            self.assertEqual(service_dev.verify_source(self.config), self.source)

        # 2. Group/world-readable cache file (0644) must be rejected on read and rewritten 0600.
        self.cache_path.chmod(0o644)
        with patch.object(service_dev.tarfile, 'open', wraps=tarfile.open) as open_tar:
            self.assertEqual(service_dev.verify_source(self.config), self.source)
            self.assertEqual(open_tar.call_count, 1)
        self.assertEqual(self.cache_path.stat().st_mode & 0o777, 0o600)

        # 3. Symlinked cache file must be rejected without reading or overwriting its target.
        victim = self.root / 'victim.json'
        victim.write_text('{"keep":true}', encoding='utf-8')
        self.cache_path.unlink()
        self.cache_path.symlink_to(victim)
        with patch.object(service_dev.tarfile, 'open', wraps=tarfile.open) as open_tar:
            self.assertEqual(service_dev.verify_source(self.config), self.source)
            self.assertEqual(open_tar.call_count, 1)
        self.assertFalse(self.cache_path.is_symlink())
        self.assertEqual(self.cache_path.stat().st_mode & 0o777, 0o600)
        self.assertEqual(victim.read_text(encoding='utf-8'), '{"keep":true}')

        # 4. Symlinked parent directory must never be read from or written into.
        outside_dir = self.root / 'outside-cache-dir'
        outside_dir.mkdir(mode=0o700)
        self.cache_path.unlink()
        self.cache_path.parent.rmdir()
        self.cache_path.parent.symlink_to(outside_dir)
        with patch.object(service_dev.tarfile, 'open', wraps=tarfile.open) as open_tar:
            self.assertEqual(service_dev.verify_source(self.config), self.source)
            self.assertEqual(open_tar.call_count, 1)
        self.assertEqual(list(outside_dir.iterdir()), [])


if __name__ == '__main__':
    unittest.main()
