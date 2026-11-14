import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest
from unittest.mock import patch

from opencode_installation import build, digest, loader, prepare, verify
from project_control import private_write
from project_policy import PolicyError


class OpenCodeArtifactTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cache = os.environ.get('MEMLANE_OPENCODE_TEST_NPM_CACHE')
        if not cache:
            raise unittest.SkipTest('set MEMLANE_OPENCODE_TEST_NPM_CACHE to verified fixed npm cache')
        cls.temporary = tempfile.TemporaryDirectory()
        cls.root = Path(cls.temporary.name).resolve()
        cls.artifact = cls.root / 'artifact'
        cls.receipt = build(cls.artifact, cache)

    @classmethod
    def tearDownClass(cls):
        cls.temporary.cleanup()

    def fixture(self, name):
        root = self.root / name
        root.mkdir()
        private_write(root / 'official-key', b'c' * 40)
        private_write(root / 'control-key', b'a' * 40)
        return root, {'key_file': str(root / 'official-key'), 'control_token_file': str(root / 'control-key')}

    def test_offline_manifest_contains_originals_licenses_and_verified_runtime_only(self):
        manifest = verify(self.artifact, self.receipt['manifest_sha256'])
        self.assertEqual(manifest['recipe']['upstream_commit'], 'a176248914fdb32a78354854b300174203b15a5c')
        self.assertEqual(len(list((self.artifact / 'upstream').rglob('*.*'))) > 10, True)
        self.assertTrue((self.artifact / 'upstream/LICENSE').is_file())
        self.assertTrue((self.artifact / 'dist/src/managed-actions.js').is_file())
        self.assertFalse((self.artifact / 'node_modules/typescript').exists())
        self.assertFalse((self.artifact / 'dist/src/cli.js').exists())
        self.assertNotIn('bin', json.loads((self.artifact / 'package.json').read_text()))
        print(json.dumps({'runtime_files': self.receipt['runtime_files'], 'generation': self.receipt['generation'],
                          'dependencies': len(json.loads((self.artifact / 'dependency-licenses.json').read_text()))}))

    def test_binding_is_immutable_private_and_endpoint_changes_get_new_generation(self):
        root, agent = self.fixture('binding')
        first = prepare(self.artifact, root, 'opencode-test', agent, 'http://127.0.0.1:19991', 'http://127.0.0.1:19992', root, artifact_sha256=self.receipt['manifest_sha256'])
        self.assertEqual(first, prepare(self.artifact, root, 'opencode-test', agent, 'http://127.0.0.1:19991', 'http://127.0.0.1:19992', root, artifact_sha256=self.receipt['manifest_sha256']))
        with self.assertRaises(ValueError):
            prepare(self.artifact, root, 'opencode-test', agent, 'http://127.0.0.1:19991', 'http://127.0.0.1:19992', root, artifact_sha256='0' * 64)
        raw = Path(first['binding_file']).read_bytes()
        second = prepare(self.artifact, root, 'opencode-test', agent, 'http://127.0.0.1:19993', 'http://127.0.0.1:19992', root, artifact_sha256=self.receipt['manifest_sha256'])
        self.assertNotEqual(first['binding_file'], second['binding_file'])
        self.assertEqual(Path(first['binding_file']).read_bytes(), raw)
        self.assertEqual(Path(first['binding_file']).stat().st_mode & 0o077, 0)
        self.assertNotIn(b'c' * 40, raw)
        with self.assertRaises(PolicyError):
            prepare(self.artifact, root, 'opencode-test', agent, 'https://remote.invalid', 'http://127.0.0.1:19992', root, artifact_sha256=self.receipt['manifest_sha256'])

    def run_loader(self, installation):
        path = Path(installation['binding_file']).parent / 'loader.mjs'
        path.write_bytes(loader(installation))
        script = "import {pathToFileURL} from 'node:url'; const m = await import(pathToFileURL(process.argv[1])); const h = await m.default({directory:process.argv[2],client:{}}); if(typeof h.dispose!=='function') throw new Error('no hooks'); await h.dispose();"
        return subprocess.run(['node', '--input-type=module', '-e', script, str(path), str(self.root)],
                              capture_output=True, text=True, timeout=30)

    def test_generated_entry_verifies_tree_binding_and_key_before_loading_hooks(self):
        root, agent = self.fixture('loader')
        installation = prepare(self.artifact, root, 'opencode-test', agent, 'http://127.0.0.1:19991', 'http://127.0.0.1:19992', root, artifact_sha256=self.receipt['manifest_sha256'])
        result = self.run_loader(installation)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        private_write(Path(agent['key_file']), b'd' * 40)
        result = self.run_loader(installation)
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn('d' * 40, result.stderr)
        private_write(Path(agent['key_file']), b'c' * 40)
        extra = self.artifact / 'unlisted.js'
        extra.write_text('throw new Error("must not run")')
        try:
            self.assertNotEqual(self.run_loader(installation).returncode, 0)
            with self.assertRaises(ValueError):
                verify(self.artifact, installation['manifest_sha256'])
        finally:
            extra.unlink()

    def test_existing_output_cannot_be_replaced(self):
        before = (self.artifact / '.memlane-opencode-artifact.json').read_bytes()
        with self.assertRaises(ValueError):
            build(self.artifact, os.environ['MEMLANE_OPENCODE_TEST_NPM_CACHE'])
        self.assertEqual((self.artifact / '.memlane-opencode-artifact.json').read_bytes(), before)

    def test_management_worker_runtime_and_input_validation_are_separate_from_agent_binding(self):
        root, agent = self.fixture('pending-worker')
        with patch.dict(os.environ, {'NODE_OPTIONS': '--require /must-not-run-during-runtime-check'}):
            installation = prepare(self.artifact, root, 'opencode-test', agent, 'http://127.0.0.1:19991',
                                   'http://127.0.0.1:19992', root, artifact_sha256=self.receipt['manifest_sha256'])
        self.assertTrue(Path(installation['node_executable']).is_absolute())
        self.assertEqual(installation['node_sha256'], digest(Path(installation['node_executable']).read_bytes()))
        binding = json.loads(Path(installation['binding_file']).read_bytes())
        self.assertNotIn('management_token_file', json.dumps(binding))
        self.assertEqual(len(binding['secrets']), 2)
        request = {'agent_id': 'opencode-test', 'installation': {key: installation[key] for key in (
            'plugin_dir', 'manifest_sha256', 'binding_file', 'binding_sha256', 'node_executable', 'node_sha256')},
            'operator': {'token_file': str(root / 'manager-key'), 'control_url': 'http://127.0.0.1:19992',
                         'service_url': 'http://127.0.0.1:19991'},
            'command': {'action': 'not-an-operation'}}
        environment = {key: value for key, value in os.environ.items() if not key.startswith('NODE_')}
        command = [installation['node_executable'], str(self.artifact / 'dist/src/managed-pending-cli.js')]
        # A valid tree reaches command validation; no HTTP is needed for this rejection.
        run = subprocess.run(command, input=json.dumps(request), env=environment, capture_output=True, text=True, timeout=10)
        self.assertEqual(json.loads(run.stdout), {'error': 'invalid_pending_action'})
        self.assertNotEqual(run.returncode, 0)
        request['installation']['node_sha256'] = '0' * 64
        run = subprocess.run(command, input=json.dumps(request), env=environment, capture_output=True, text=True, timeout=10)
        self.assertEqual(json.loads(run.stdout), {'error': 'pending_worker_unconfirmed'})
        self.assertNotIn('c' * 40, run.stdout + run.stderr)
