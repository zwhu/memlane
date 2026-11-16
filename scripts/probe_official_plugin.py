#!/usr/bin/env python3
"""Probe the pinned, unmodified plugin in a temporary host; never use live data.

HTTP is a local fixture. Capture/processing probes replace transport and host
session lookup only. Results establish plugin contracts, not live ACL or UI.
"""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[1]
SOURCE = ROOT / '.cache/coding-memory-components/plugin-a176248914fdb32a78354854b300174203b15a5c/integrations/codex/plugins/cognee'

PROBE = r'''
import contextlib, io, json, os, threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
import subprocess, sys

source = Path(sys.argv[1])
sys.path.insert(0, str(source / 'scripts'))
dataset = '11111111-1111-4111-8111-111111111111'
other = '22222222-2222-4222-8222-222222222222'
requests = []
class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        body = self.rfile.read(int(self.headers.get('Content-Length', 0)))
        denied = other.encode() in body
        requests.append({'path': self.path, 'key_matches': self.headers.get('X-Api-Key') == 'fixture-child-key',
                         'target_matches': dataset.encode() in body, 'denied': denied})
        self.send_response(403 if denied else 200)
        self.send_header('Content-Type', 'application/json'); self.end_headers()
        self.wfile.write(json.dumps({'error': 'forbidden'} if denied else
                                   {'status': 'running', 'dataset_id': dataset, 'pipeline_run_id': 'fixture-run'}).encode())
    def log_message(self, *args): pass
server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
thread = threading.Thread(target=server.serve_forever, daemon=True); thread.start()
try:
    envfile = Path.cwd() / 'official.env'
    envfile.write_text('\n'.join(['COGNEE_BASE_URL="http://127.0.0.1:%d"' % server.server_port,
        'COGNEE_API_KEY="fixture-child-key"', 'COGNEE_PLUGIN_DATASET="%s"' % dataset,
        'COGNEE_PLUGIN_IDENTITY=false', 'COGNEE_SHARED_AGENT_MEMORY=false',
        'COGNEE_MANAGED_ENDPOINT=true', 'COGNEE_BACKEND=cloud', 'COGNEE_REMEMBER_WAIT_SECONDS=0']))
    envfile.chmod(0o600)
    os.environ['COGNEE_ENV_FILE'] = str(envfile)
    results = []
    def record(name, ok, detail):
        results.append({'name': name, 'pass': bool(ok), 'detail': detail})
    for name, args in [('save_default', []), ('save_other_dataset', ['--dataset', other])]:
        run = subprocess.run(['sh', str(source / 'scripts/cognee-remember.sh'),
                              'synthetic contract probe', '--node-set', 'user_context', *args],
                             capture_output=True, text=True, timeout=20)
        response = json.loads(run.stdout)
        last = requests[-1] if requests else {}
        if name == 'save_default':
            ok = response.get('ok') is True and response.get('dataset_id') == dataset and last.get('key_matches') and last.get('target_matches')
        else:
            ok = response.get('status') == 403 and not response.get('ok') and last.get('denied')
        record(name, ok, {'response': response, 'transport': last, 'exit_code': run.returncode})

    import config, _plugin_common as common, _coding_memory as coding
    conf = config.load_config()
    record('official_private_env', conf.get('api_key') == 'fixture-child-key' and
           conf.get('dataset') == dataset and not common.shared_memory_enabled(conf) and
           common.plugin_identity_mode(conf) == 'disabled',
           {'dataset': conf.get('dataset'), 'shared_memory': common.shared_memory_enabled(conf),
            'identity': common.plugin_identity_mode(conf)})
    os.environ['COGNEE_PLUGIN_READ_DATASET_IDS'] = dataset + ',' + other
    record('explicit_read_scope', common.recall_mode()[1] == common.RECALL_PIN_READ_SET,
           {'recall_mode': common.recall_mode()})

    # Invoke real capture with a synthetic transcript. Only session lookup and
    # outbound delivery are replaced; parser, disabled guard and cursor are real.
    transcript = Path.cwd() / 'synthetic.jsonl'
    def turn(n, question):
        return [
            {'timestamp': '2026-11-16T00:00:0%dZ' % n, 'type': 'event_msg',
             'payload': {'type': 'item_completed', 'item': {'type': 'UserMessage', 'content': question}}},
            {'timestamp': '2026-11-16T00:00:0%dZ' % (n+1), 'type': 'event_msg',
             'payload': {'type': 'task_complete', 'last_agent_message': 'synthetic answer'}}]
    rows = turn(1, 'disabled-period synthetic question')
    transcript.write_text(''.join(json.dumps(r)+'\n' for r in rows))
    os.environ['COGNEE_SESSION_KEY'] = 'contract-capture'
    common.set_session_key('contract-capture')
    coding.server_supported = lambda: True
    coding._session = lambda: ('fixture-session', dataset)
    captured = []
    coding._deliver = lambda d, s, entries: captured.extend(entries)
    payload = {'transcript_path': str(transcript), 'cwd': str(Path.cwd())}
    os.environ['COGNEE_CAPTURE'] = 'false'
    disabled = coding.capture(payload, final=True)
    record('capture_disabled', disabled.get('skipped') == 'capture_disabled' and not captured, disabled)
    rows += turn(3, 'enabled-period synthetic question')
    transcript.write_text(''.join(json.dumps(r)+'\n' for r in rows))
    os.environ['COGNEE_CAPTURE'] = 'true'
    resumed = coding.capture(payload, final=True)
    questions = [e.get('question') for e in captured if e.get('type') == 'qa']
    record('resume_backfills_disabled_period', 'disabled-period synthetic question' in questions and
           'enabled-period synthetic question' in questions,
           {'captured_questions': questions, 'summary': resumed,
            'meaning': 'gap reproduced: stock plugin does not skip disabled history on resume'})

    # Disable both known automatic knobs; final processing is a separate path.
    os.environ['COGNEE_AUTO_IMPROVE_EVERY'] = '0'
    os.environ['COGNEE_IMPROVE_COOLDOWN'] = '0'
    final_calls = []
    common._backend_reachable = lambda url: True
    common.drain_warmup_entries = lambda *a, **k: (0, 0)
    common.ensure_dataset_via_http = lambda *a: None
    common.improve_session_via_http = lambda *a: final_calls.append(a) or {'ok': True}
    common.refresh_credits = lambda *a, **k: None
    with contextlib.redirect_stderr(io.StringIO()):
        final = common.run_session_improve_detailed(dataset, 'fixture-final', trigger='final')
    record('zero_auto_knobs_do_not_disable_final', len(final_calls) == 1 and bool(final.get('ok')),
           {'final_transport_calls': len(final_calls), 'result': final,
            'meaning': 'gap reproduced: auto threshold/cooldown are not a global processing switch'})
    print(json.dumps({'scope': 'unmodified official plugin; synthetic HTTP/host transport only',
                      'results': results}, ensure_ascii=False, indent=2))
    if not all(row['pass'] for row in results): raise SystemExit(1)
finally:
    server.shutdown(); server.server_close(); thread.join(timeout=5)
'''


def main():
    lock = json.loads((ROOT / 'engine/patches/project-access-upstream.json').read_text())
    actual = {str(p.relative_to(SOURCE)): hashlib.sha256(p.read_bytes()).hexdigest()
              for p in SOURCE.rglob('*') if p.is_file() and '__pycache__' not in p.parts and p.suffix != '.pyc'}
    if actual != lock['files']:
        raise SystemExit('official input tree mismatch')
    with tempfile.TemporaryDirectory(prefix='official-plugin-probe-') as directory:
        env = {k: v for k, v in os.environ.items() if not k.startswith(
            ('COGNEE_', 'MEMLANE_', 'LLM_', 'EMBEDDING_', 'OPENAI_', 'ANTHROPIC_', 'PYTHON'))}
        env.update(HOME=directory, PYTHONDONTWRITEBYTECODE='1')
        result = subprocess.run([sys.executable, '-c', PROBE, str(SOURCE)], cwd=directory,
                                env=env, capture_output=True, text=True, timeout=60)
        sys.stdout.write(result.stdout)
        if result.returncode:
            sys.stderr.write(result.stderr)
        return result.returncode


if __name__ == '__main__':
    raise SystemExit(main())
