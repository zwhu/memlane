import hashlib
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import mygo_sdk


class PatchedSDKTests(unittest.TestCase):
    def test_pinned_input_is_immutable_and_output_is_idempotent(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            project_mod='module example/app\nrequire example/sdk v1\n'
            (root/'go.mod').write_text(project_mod)
            (root/'go.sum').write_text('original sums\n')
            source = root / '.local/gopath/pkg/mod/example/sdk@v1/ui/theme.go'
            source.parent.mkdir(parents=True)
            original = b'old theme\n'
            source.write_bytes(original)
            manifest = root / 'patch.json'
            manifest.write_text(json.dumps({'module': 'example/sdk', 'version': 'v1', 'files': {
                'ui/theme.go': {'sha256': hashlib.sha256(original).hexdigest(),
                                'replacements': [{'old': 'old theme', 'new': 'new theme'}]}}}))
            modfile = mygo_sdk.prepare(root, manifest)
            target = next((root/'.local/mygo-sdk').glob('v1-*'))/'ui/theme.go'
            self.assertIn('replace example/sdk',modfile.read_text())
            self.assertEqual((root/'go.mod').read_text(),project_mod)
            self.assertEqual(target.read_text(), 'new theme\n')
            stamp = target.stat().st_mtime_ns
            mygo_sdk.prepare(root, manifest)
            self.assertEqual(target.stat().st_mtime_ns, stamp)
            self.assertEqual(source.read_bytes(), original)
            target.write_text('unexpected local modification\n')
            with self.assertRaisesRegex(RuntimeError,'changed after preparation'):
                mygo_sdk.prepare(root,manifest)
            target.write_text('new theme\n')
            (root/'go.mod').write_text('module example/app\nrequire example/sdk v2\n')
            with self.assertRaisesRegex(RuntimeError,'version changed'):
                mygo_sdk.prepare(root,manifest)
            (root/'go.mod').write_text(project_mod)
            source.write_text('changed SDK\n')
            with self.assertRaisesRegex(RuntimeError, 'input changed'):
                mygo_sdk.prepare(root, manifest)

    def test_existing_build_flags_survive_and_other_modfile_is_not_replaced(self):
        with patch.object(mygo_sdk, 'prepare', return_value=Path('/fixture/project.mod')):
            env = mygo_sdk.with_patched_sdk({'GOFLAGS': '-trimpath', 'GOPATH': '/fixture'})
            self.assertEqual(env['GOFLAGS'], '-trimpath -modfile=/fixture/project.mod')
            self.assertEqual(env['GOPATH'], '/fixture')
            self.assertEqual(mygo_sdk.with_patched_sdk(env), env)
            with self.assertRaisesRegex(RuntimeError, 'unrelated Go modfile'):
                mygo_sdk.with_patched_sdk({'GOFLAGS': '-modfile=/other/project.mod'})
