"""Prepared official tools: denial vs unknown and no credential/CLI fallback."""
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

from project_installation import prepare
from project_tool_patch import common_recall_response, forget_response, remember_response
from project_control import private_write

ROOT = Path(__file__).resolve().parents[2]


class ToolPatchTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.temporary = tempfile.TemporaryDirectory()
        cls.root = Path(cls.temporary.name)
        spec = importlib.util.spec_from_file_location('tool_patch_builder', ROOT/'scripts/build_project_plugin.py')
        builder = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(builder)
        cls.source = cls.root/'fixed-input'
        builder.build(ROOT/'.cache/coding-memory-components/plugin-a176248914fdb32a78354854b300174203b15a5c/integrations/codex/plugins/cognee', cls.source)
        cls.manifest = (cls.source/'.memlane-project-access.json').read_bytes()

    @classmethod
    def tearDownClass(cls):
        cls.temporary.cleanup()

    def scenario(self, kind, operation, permissions, *, unavailable=False, common=False):
        with tempfile.TemporaryDirectory(dir=self.root) as temporary:
            root = Path(temporary)
            home = root/'home'; home.mkdir()
            private_write(root/'agent-key', b'synthetic-child-never-output')
            private_write(root/'control-key', b'c'*40)
            installation = prepare(self.source, root, kind+'-tools', {
                'key_file': str(root/'agent-key'), 'control_token_file': str(root/'control-key')},
                'http://127.0.0.1:18999', 'http://127.0.0.1:18998', str(home), host_kind=kind, python=sys.executable)
            scripts = Path(installation['plugin_dir'])/'scripts'
            project, shared = '11111111-1111-4111-8111-111111111111', '22222222-2222-4222-8222-222222222222'
            spy = root/'spy'; spy.mkdir()
            decision = {'revision': 1, 'ready': True, 'dataset_id': project, 'policy': {'permissions': permissions},
                        'common': {'ready': True, 'readable': True, 'dataset_id': shared}}
            (spy/'sitecustomize.py').write_text(
                'import _memlane_policy as p\n'
                + ('def missing(*a, **k): raise p.NotReady("synthetic unavailable")\np.current=missing\n' if unavailable
                   else 'p.current=lambda *a, **k: '+repr(decision)+'\n'))
            binary = root/'bin'; binary.mkdir()
            marker = root/'curl-called'
            (binary/'curl').write_text('#!/bin/sh\ntouch "'+str(marker)+'"\nprintf "synthetic response\\nHTTP 200\\n"\n')
            (binary/'curl').chmod(0o700)
            environment = {k:v for k,v in os.environ.items() if k in ('PATH','LANG','LC_ALL','TMPDIR')}
            environment.update(HOME=str(home), PATH=str(Path(sys.executable).parent)+os.pathsep+str(binary)+os.pathsep+os.environ['PATH'],
                               PYTHONPATH=str(spy)+os.pathsep+str(scripts), COGNEE_SESSION_KEY='synthetic-explicit-tools')
            target = shared if common else project
            command = ['bash', str(scripts/'cognee-forget.sh'), operation, target, '33333333-3333-4333-8333-333333333333']
            if operation == 'remember':
                command = [sys.executable, '-c', '''import json
import _remember_http as tool
print(json.dumps(tool.do_remember("http://127.0.0.1:18999", "unused", "synthetic", "11111111-1111-4111-8111-111111111111", "memory", file_path="must-not-read-missing.txt")))
''']
            if operation == 'remember_allowed':
                environment['COGNEE_REMEMBER_WAIT_SECONDS'] = '0'
                command = [sys.executable, '-c', '''import json,io
import _remember_http as tool
calls=[]
def opener(request,**kwargs):
    assert request.get_header("X-api-key")=="synthetic-child-never-output"
    assert b"synthetic content" in request.data
    calls.append(request.full_url)
    return io.BytesIO(b'{"dataset_id":"11111111-1111-4111-8111-111111111111","status":"running"}')
result=tool.do_remember("http://127.0.0.1:18999", "unused", "synthetic content", "11111111-1111-4111-8111-111111111111", "memory",opener=opener)
assert result["ok"] and calls==["http://127.0.0.1:18999/api/v1/remember"]
print("permitted official multipart request verified")
''']
            if operation == 'common_recall':
                command = [sys.executable, '-c', '''
import _plugin_common as common, _memlane_policy as policy
common_id = "22222222-2222-4222-8222-222222222222"
for rows in ([], [{"id": "raw", "datasetId": common_id, "externalMetadata": {}}]):
    calls = []
    def request(path, payload=None, **kw):
        calls.append(path)
        if path.startswith("/api/v1/datasets/"):
            return rows
        raise AssertionError("empty common catalog reached search")
    common._json_http_request = request
    assert policy.merge_block("message retry", ".", lambda *a: "project rules") == "project rules"
    assert calls == ["/api/v1/datasets/" + common_id + "/data?limit=30&offset=0"], calls
print("empty common catalog never searched; project recall retained")
''']
            result = subprocess.run(command, env=environment, cwd=home, text=True, capture_output=True, timeout=10)
            self.assertNotIn('synthetic-child-never-output', result.stdout+result.stderr)
            self.assertEqual((self.source/'.memlane-project-access.json').read_bytes(), self.manifest)
            return result, marker.exists()

    def test_empty_common_candidates_do_not_submit_search_in_prepared_hosts(self):
        for kind in ('codex', 'pi', 'traex'):
            with self.subTest(kind=kind):
                result, contacted = self.scenario(kind, 'common_recall', ['read'])
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertFalse(contacted)

    def test_known_delete_and_read_denials_are_structured_before_transport_for_both_hosts(self):
        for kind in ('codex','claude_code'):
            for operation, permissions, reason in (('forget',['read'],'project_delete_denied'),('raw',[],'project_read_denied')):
                with self.subTest(kind=kind, operation=operation):
                    result, contacted = self.scenario(kind, operation, permissions)
                    self.assertEqual(result.returncode, 2, result.stderr)
                    body = json.loads(result.stdout)
                    self.assertEqual((body['error'],body['status']), (reason,403))
                    self.assertTrue(body['local_denial'])
                    self.assertFalse(contacted)

    def test_unavailable_policy_stays_unknown_instead_of_false_permission_denial(self):
        for kind in ('codex','claude_code'):
            with self.subTest(kind=kind):
                result, contacted = self.scenario(kind,'forget',['delete','read','write'],unavailable=True)
                body = json.loads(result.stdout)
                self.assertEqual((body['error'],body['status']), ('project_policy_unconfirmed',0))
                self.assertNotIn('local_denial',body)
                self.assertEqual(result.returncode,2)
                self.assertFalse(contacted)

    def test_permitted_project_and_common_reads_keep_official_transport(self):
        for kind in ('codex','claude_code'):
            for common in (False,True):
                with self.subTest(kind=kind,common=common):
                    result, contacted = self.scenario(kind,'raw',['read'],common=common)
                    self.assertEqual(result.returncode,0,result.stderr)
                    self.assertIn('HTTP 200',result.stdout)
                    self.assertTrue(contacted)

    def test_common_read_does_not_grant_common_delete(self):
        result, contacted = self.scenario('codex','forget',['delete','read','write'],common=True)
        self.assertEqual(json.loads(result.stdout)['status'],403)
        self.assertFalse(contacted)

    def test_remember_distinguishes_known_denial_and_unknown_before_file_read(self):
        for kind in ('codex','claude_code'):
            for unavailable in (False,True):
                with self.subTest(kind=kind,unavailable=unavailable):
                    result, contacted = self.scenario(kind,'remember',['read'],unavailable=unavailable)
                    self.assertEqual(result.returncode,0,result.stderr)
                    body=json.loads(result.stdout)
                    self.assertEqual(body['status'],0 if unavailable else 403)
                    if unavailable:self.assertEqual(body['error'],'project_policy_unconfirmed')
                    self.assertFalse(contacted)

    def test_allowed_remember_preserves_pinned_credentials_and_official_multipart(self):
        for kind in ('codex','claude_code'):
            with self.subTest(kind=kind):
                result,_=self.scenario(kind,'remember_allowed',['delete','read','write'])
                self.assertEqual(result.returncode,0,result.stderr)
                self.assertIn('permitted official multipart request verified',result.stdout)

    def test_overlay_refuses_changed_or_already_modified_input(self):
        source = (self.source/'scripts/cognee-forget.sh').read_text()
        with self.assertRaises(ValueError):forget_response('not fixed input')
        with self.assertRaises(ValueError):forget_response(forget_response(source))
        with self.assertRaises(ValueError):remember_response('not fixed input')
        policy = (self.source/'scripts/_memlane_policy.py').read_text()
        with self.assertRaises(ValueError):common_recall_response('not fixed input')
        with self.assertRaises(ValueError):common_recall_response(common_recall_response(policy))
