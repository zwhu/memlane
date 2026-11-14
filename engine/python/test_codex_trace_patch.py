"""Codex file-change evidence through the prepared plugin and fixed SDK renderer.

No model call or original session replay; the small diff is derived from the
recorded insertion that produced MA-CODEX-FACT01, with a synthetic path/id.
"""
import importlib.util
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

from project_control import private_write
from project_installation import prepare

ROOT = Path(__file__).resolve().parents[2]
DIFF = '@@ -9,2 +9,7 @@\n \n+def retry_indices(value):\n+    limit = retry_limit(value)\n+    return list(range(limit))\n+\n+\n def retry_options(value):\n'
WORKER = '''import contextlib,copy,io,json,socket,sys
from types import SimpleNamespace
def no_network(*args,**kwargs):
 raise AssertionError('local trace rendering must not perform network requests')
socket.socket.connect=no_network
sys.path[:0]=[sys.argv[1],sys.argv[2]]
with contextlib.redirect_stdout(io.StringIO()):
 import _coding_memory as coding
 from _capture_policy import sanitize
 from cognee.infrastructure.session.coding_unit_extraction import render_trace_line
items=json.load(sys.stdin)
result=[]
for item in items:
 before=copy.deepcopy(item)
 calls=coding._codex_call(item,'2026-11-14T00:00:00+00:00')
 entries=[coding.trace_entry(coding._sanitize_call(call,sanitize)) for call in calls]
 rendered=[render_trace_line(SimpleNamespace(**entry,session_feedback=''),'/synthetic/repo') for entry in entries]
 result.append({'calls':calls,'entries':entries,'rendered':rendered,'input_event_unchanged':item==before})
print('MEMLANE_TRACE_TEST='+json.dumps(result,ensure_ascii=False))
'''


class CodexTraceTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.temporary = tempfile.TemporaryDirectory()
        cls.root = Path(cls.temporary.name)
        cls.addClassCleanup(cls.temporary.cleanup)
        spec = importlib.util.spec_from_file_location('codex_trace_builder', ROOT / 'scripts/build_project_plugin.py')
        builder = importlib.util.module_from_spec(spec); spec.loader.exec_module(builder)
        cls.source = cls.root / 'fixed-input'
        builder.build(ROOT / '.cache/coding-memory-components/plugin-a176248914fdb32a78354854b300174203b15a5c/integrations/codex/plugins/cognee', cls.source)
        cls.fixed_manifest = (cls.source / '.memlane-project-access.json').read_bytes()
        home = cls.root / 'home'; home.mkdir()
        private_write(cls.root / 'agent', b'synthetic-trace-child-key')
        private_write(cls.root / 'control', b'c' * 40)
        cls.installation = prepare(cls.source, cls.root, 'codex-trace-test', {
            'key_file': str(cls.root / 'agent'), 'control_token_file': str(cls.root / 'control')},
            'http://127.0.0.1:18999', 'http://127.0.0.1:18998', str(home), host_kind='codex', python=sys.executable)
        cls.multi_diff = '@@ -1,2 +1,2 @@\n-old_start\n+new_start\n context\n@@ -20,2 +20,3 @@\n before_tail\n+added_tail\n after_tail\n'
        cls.items = [
            {'id': 'synthetic-file-event', 'status': 'completed',
             'changes': {'/synthetic/repo/retry_config.py': {'type': 'update', 'unified_diff': DIFF}}},
            {'id': 'multi-file', 'status': 'completed', 'changes': {
                '/synthetic/repo/first.py': {'type': 'update', 'unified_diff': cls.multi_diff},
                '/synthetic/repo/second.py': {'type': 'update', 'unified_diff': '@@ -1 +1 @@\n-old_second\n+new_second\n'}}},
            {'id': 'failed-change', 'status': 'failed', 'changes': {
                '/synthetic/repo/failure.py': {'type': 'update', 'unified_diff': DIFF}}},
            {'id': 'other-changes', 'status': 'completed', 'changes': {
                '/synthetic/repo/binary.dat': {'type': 'update', 'unified_diff': None},
                '/synthetic/repo/added.py': {'type': 'add', 'content': 'actual_added_content'},
                '/synthetic/repo/deleted.py': {'type': 'delete', 'content': None}}},
            {'id': 'private-large', 'status': 'completed', 'changes': {
                '/synthetic/repo/private.py': {'type': 'update', 'unified_diff':
                    '@@ -1 +1 @@\n+' + 'x' * 3900 + '<private>SYNTHETIC_PRIVATE_CARRY</private>' + 'y' * 6000}}},
        ]
        for item in cls.items:
            item['type'] = 'FileChange'
        manifest = json.loads((ROOT / 'engine/coding-memory-components.json').read_text())['service']
        sdk = ROOT / '.cache/coding-memory-components' / ('service-' + manifest['commit'])
        env = {k: v for k, v in os.environ.items() if k in ('PATH', 'LANG', 'LC_ALL', 'TMPDIR')}
        env.update(HOME=str(home), SYSTEM_ROOT_DIRECTORY=str(cls.root / 'system'), DATA_ROOT_DIRECTORY=str(cls.root / 'data'),
            CACHE_ROOT_DIRECTORY=str(cls.root / 'cache'), LOGS_ROOT_DIRECTORY=str(cls.root / 'logs'), COGNEE_LOGS_DIR=str(cls.root / 'logs'),
            TELEMETRY_DISABLED='true', COGNEE_TRACING_ENABLED='false', PYTHON_DOTENV_DISABLED='1', LITELLM_LOCAL_MODEL_COST_MAP='true',
            LLM_PROVIDER='openai', LLM_MODEL='openai/synthetic', LLM_API_KEY='synthetic-unused-render-key')
        result = subprocess.run([sys.executable, '-c', WORKER, str(Path(cls.installation['plugin_dir']) / 'scripts'), str(sdk)],
            input=json.dumps(cls.items), env=env, text=True, capture_output=True, timeout=30)
        if result.returncode:
            raise AssertionError('fixed SDK renderer worker failed: ' + result.stderr[-1000:])
        cls.results = json.loads(next(line.split('=', 1)[1] for line in result.stdout.splitlines() if line.startswith('MEMLANE_TRACE_TEST=')))

    def test_recorded_insertion_evidence_reaches_actual_official_extraction_line(self):
        line = self.results[0]['rendered'][0]
        self.assertIn('retry_indices(value)', line)
        self.assertIn('retry_options(value)', line)
        self.assertIn('@@ -9,2 +9,7 @@', line)
        self.assertNotIn('old_string', self.results[0]['calls'][0]['input'])
        self.assertEqual(self.results[0]['calls'][0]['input']['diff'], DIFF)

    def test_multi_hunk_results_remain_exact_without_fabricated_replacement_strings(self):
        result = self.results[1]
        self.assertEqual(len(result['calls']), 2)
        self.assertEqual(result['calls'][0]['output'], {'status': 'completed', 'unified_diff': self.multi_diff})
        for name in ('new_start', 'added_tail', 'after_tail', '@@ -20,2 +20,3 @@'):
            self.assertIn(name, result['rendered'][0])
        self.assertNotIn('new_second', result['rendered'][0])
        self.assertIn('new_second', result['rendered'][1])
        for call in result['calls']:
            self.assertNotIn('old_string', call['input'])
            self.assertNotIn('new_string', call['input'])

    def test_event_ids_and_failure_status_preserved(self):
        self.assertEqual(self.results[0]['calls'][0]['id'], 'synthetic-file-event:/synthetic/repo/retry_config.py')
        self.assertEqual(self.results[2]['entries'][0]['status'], 'error')
        self.assertTrue(self.results[2]['calls'][0]['is_error'])
        self.assertEqual(self.results[2]['calls'][0]['output']['status'], 'failed')
        self.assertIn('[error]', self.results[2]['rendered'][0])
        self.assertTrue(all(row['input_event_unchanged'] for row in self.results))

    def test_non_codex_adapter_keeps_its_original_trace_conversion(self):
        home = self.root / 'pi-home'; home.mkdir()
        installation = prepare(self.source, self.root / 'pi-only', 'pi-trace-test', {
            'key_file': str(self.root / 'agent'), 'control_token_file': str(self.root / 'control')},
            'http://127.0.0.1:18999', 'http://127.0.0.1:18998', str(home), host_kind='pi', python=sys.executable)
        def function(path):
            return path.read_text().split('def _codex_file_changes(', 1)[1].split('\ndef _codex_call(', 1)[0]
        self.assertEqual(function(Path(installation['plugin_dir']) / 'scripts/_coding_memory.py'), function(self.source / 'scripts/_coding_memory.py'))
        receipt = json.loads((Path(installation['plugin_dir']) / '.memlane-project-access.json').read_text())
        self.assertNotIn('trace_patch_sha256', receipt)

    def test_missing_diff_add_and_delete_keep_existing_output_and_tool_mapping(self):
        self.assertEqual([x['name'] for x in self.results[3]['calls']], ['Edit', 'Write', 'Edit'])
        self.assertEqual([x['output'] for x in self.results[3]['calls']], ['completed'] * 3)

    def test_privacy_filter_runs_before_both_input_and_output_byte_caps(self):
        entry = self.results[4]['entries'][0]
        self.assertNotIn('SYNTHETIC_PRIVATE_CARRY', json.dumps(entry))
        self.assertNotIn('<private>', json.dumps(entry))
        self.assertLessEqual(len(entry['method_params']['diff'].encode()), 4000)
        self.assertLessEqual(len(entry['method_return_value'].encode()), 8000)
        self.assertLess(len(self.results[4]['rendered'][0]), 1000)

    def test_fixed_input_unchanged_and_derived_receipt_pins_trace_overlay(self):
        self.assertEqual((self.source / '.memlane-project-access.json').read_bytes(), self.fixed_manifest)
        receipt = json.loads((Path(self.installation['plugin_dir']) / '.memlane-project-access.json').read_text())
        self.assertEqual(receipt['trace_patch_sha256'], hashlib.sha256((ROOT / 'engine/python/project_trace_patch.py').read_bytes()).hexdigest())
        original = (self.source / 'scripts/_coding_memory.py').read_text()
        from project_trace_patch import codex_file_change_output
        with self.assertRaisesRegex(ValueError, 'anchor mismatch'):
            codex_file_change_output(codex_file_change_output(original))
        with self.assertRaisesRegex(ValueError, 'anchor mismatch'):
            codex_file_change_output(original.replace('calls.append({**call, **common})', 'changed_upstream()'))


if __name__ == '__main__':
    unittest.main()
