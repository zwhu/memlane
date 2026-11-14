import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import tarfile
import tempfile
import unittest

from opencode_plugin_patch import managed_http_client, managed_outbox, managed_capture_callbacks

ROOT = Path(__file__).resolve().parents[2]


def official_sources():
    lock = json.loads((ROOT / 'engine/coding-memory-components.json').read_text())['plugin']
    archive = ROOT / '.cache/coding-memory-components' / (lock['commit'] + '.tar.gz')
    if hashlib.sha256(archive.read_bytes()).hexdigest() != lock['archive_sha256']:
        raise ValueError('fixed official OpenCode archive mismatch')
    result = {}
    with tarfile.open(archive) as bundle:
        for member in bundle:
            if any(member.name.endswith('/integrations/opencode/src/' + name)
                   for name in ('runtime.ts', 'client.ts', 'types.ts', 'plugin.ts')):
                result[Path(member.name).name] = bundle.extractfile(member).read().decode()
    if len(result) != 4:
        raise ValueError('fixed official OpenCode files missing')
    return result


class OpenCodePatchGuardTests(unittest.TestCase):
    def test_capture_overlay_rejects_changed_or_already_patched_source(self):
        text = official_sources()['plugin.ts']
        for candidate in (text.replace('ctx.client.session.messages(', 'changed_messages('), managed_capture_callbacks(text)):
            with self.assertRaises(ValueError):
                managed_capture_callbacks(candidate)

    def test_http_overlay_rejects_changed_or_already_patched_source(self):
        text = official_sources()['client.ts']
        for candidate in (text.replace('  async health():', '  async changed_health():'), managed_http_client(text)):
            with self.assertRaises(ValueError):
                managed_http_client(candidate)

    def test_outbox_overlay_rejects_changed_or_already_patched_source(self):
        text = official_sources()['runtime.ts']
        for candidate in (text.replace('const next = state.pending[0];', 'const next = different();'), managed_outbox(text)):
            with self.assertRaises(ValueError):
                managed_outbox(candidate)

    def test_upstream_filters_identity_and_enqueued_entry_scrubbing_stay_exact(self):
        text = official_sources()['runtime.ts']
        patched = managed_outbox(text)
        self.assertEqual(text.split('type Pending =')[0], patched.split('export type ProjectTarget =')[0])
        self.assertIn('entry: scrub(entry) as Entry', patched)


class ManagedOpenCodeRuntimeTests(unittest.TestCase):
    def test_real_pinned_outbox_restart_denial_and_unknown_contract(self):
        self.execute('managed-outbox.test.mjs')

    def test_real_pinned_http_auth_and_redirect_contract(self):
        self.execute('managed-http.test.mjs')

    def execute(self, test_file):
        modules = os.environ.get('MEMLANE_OPENCODE_TEST_NODE_MODULES')
        if not modules:
            self.skipTest('set MEMLANE_OPENCODE_TEST_NODE_MODULES to fixed OpenCode npm-ci dependencies')
        modules = Path(modules).resolve()
        source_files = official_sources()
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / 'package.json').write_text('{"type":"module"}')
            (root / 'runtime.ts').write_text(managed_outbox(source_files['runtime.ts']))
            (root / 'client.ts').write_text(managed_http_client(source_files['client.ts']))
            (root / 'types.ts').write_text(source_files['types.ts'])
            build = subprocess.run(['node', str(modules / 'typescript/bin/tsc'),
                                    '--target', 'ES2022', '--module', 'NodeNext',
                                    '--moduleResolution', 'NodeNext', '--skipLibCheck',
                                    '--types', 'node', '--typeRoots', str(modules / '@types'),
                                    '--outDir', str(root / 'dist'), str(root / 'runtime.ts'), str(root / 'client.ts')],
                                   capture_output=True, text=True, timeout=30)
            self.assertEqual(build.returncode, 0, build.stdout + build.stderr)
            env = {**os.environ, 'MEMLANE_OPENCODE_TEST_RUNTIME': str(root / 'dist/runtime.js'),
                   'MEMLANE_OPENCODE_TEST_CLIENT': str(root / 'dist/client.js')}
            run = subprocess.run(['node', '--test', str(ROOT / 'engine/agents/opencode' / test_file)],
                                 env=env, capture_output=True, text=True, timeout=30)
            self.assertEqual(run.returncode, 0, run.stdout + run.stderr)
            counts = {key: int(value) for key, value in re.findall(r'^# (tests|pass|fail) (\d+)$', run.stdout, re.M)}
            self.assertGreater(counts.get('tests', 0), 0)
            self.assertEqual(counts.get('tests'), counts.get('pass'))
            self.assertEqual(counts.get('fail'), 0)
            print(json.dumps({'node_test': test_file, **counts}))


if __name__ == '__main__':
    unittest.main()
