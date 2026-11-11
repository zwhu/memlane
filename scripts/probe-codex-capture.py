#!/usr/bin/env python3
"""Controlled CLI hook/format probe. Local synthetic responder, no real model.

Never a daily-use acceptance: uses an isolated home and invocation-only trust
bypass for the exact probe hook below. Does not edit user hooks or trust state.
"""
import argparse
import http.server
import json
import os
from pathlib import Path
import subprocess
import tempfile
import threading


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--codex', required=True)
    parser.add_argument('--out', required=True)
    args = parser.parse_args()
    out = Path(args.out).resolve()
    out.mkdir(parents=True, exist_ok=True)
    version = subprocess.check_output([args.codex, '--version'], text=True).strip()
    with tempfile.TemporaryDirectory(prefix='memlane-codex-hook-probe-') as scratch:
        root = Path(scratch).resolve()
        home, profile, repo = root/'home', root/'codex', root/'repo'
        for path in (home, profile, repo):
            path.mkdir(mode=0o700)
        subprocess.run(['/usr/bin/git', 'init', '-q', str(repo)], check=True)
        # The only bypassed hook is this fixed, local probe. No inherited hooks.
        hook = root/'probe.py'
        hook.write_text('import json,sys,pathlib\np=json.load(sys.stdin)\n'
                        'd=pathlib.Path(sys.argv[1]);d.mkdir(exist_ok=True)\n'
                        '(d/(p["hook_event_name"]+".json")).write_text(json.dumps(p))\n'
                        't=p.get("transcript_path")\n'
                        'if t and pathlib.Path(t).is_file():\n'
                        ' (d/(p["hook_event_name"]+"-snapshot.jsonl")).write_bytes(pathlib.Path(t).read_bytes())\n'
                        'print("{}")\n')
        events = root/'events'
        events.mkdir()
        import shlex
        command = ' '.join(shlex.quote(p) for p in ('/usr/bin/python3', str(hook), str(events)))
        (profile/'hooks.json').write_text(json.dumps({'hooks': {
            event: [{'hooks': [{'type': 'command', 'command': command, 'timeout': 5}]}]
            for event in ('SessionStart', 'UserPromptSubmit', 'Stop')
        }}))
        requests = []

        class Handler(http.server.BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass

            def do_POST(self):
                # Do not retain request bodies; only this fixture can connect.
                self.rfile.read(int(self.headers.get('Content-Length', '0')))
                requests.append(self.path)
                item = {'id': 'msg_probe', 'type': 'message', 'role': 'assistant',
                        'status': 'completed', 'content': [{'type': 'output_text',
                        'text': 'MEMLANE_SYNTHETIC_CAPTURE_REPLY', 'annotations': []}]}
                response = {'id': 'resp_probe', 'object': 'response', 'status': 'completed',
                            'output': [item], 'usage': {'input_tokens': 1, 'output_tokens': 1,
                            'total_tokens': 2}}
                rows = [{'type': 'response.output_item.done', 'output_index': 0, 'item': item},
                        {'type': 'response.completed', 'response': response}]
                data = ''.join('event: '+row['type']+'\ndata: '+json.dumps(row)+'\n\n' for row in rows).encode()
                self.send_response(200)
                self.send_header('Content-Type', 'text/event-stream')
                self.send_header('Content-Length', str(len(data)))
                self.end_headers()
                self.wfile.write(data)

        server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        (profile/'config.toml').write_text(
            'model = "synthetic-capture-probe"\nmodel_provider = "fixture"\n'
            '[model_providers.fixture]\nname = "Local synthetic responder"\n'
            f'base_url = "http://127.0.0.1:{server.server_port}/v1"\n'
            'wire_api = "responses"\nrequires_openai_auth = false\n'
            '[analytics]\nenabled = false\n')
        env = dict(os.environ, HOME=str(home), CODEX_HOME=str(profile))
        try:
            proc = subprocess.run([args.codex, '--no-daemon', '--dangerously-bypass-hook-trust',
                'exec', '-C', str(repo), '--json', '--sandbox', 'read-only',
                'MEMLANE_SYNTHETIC_CAPTURE_PROMPT: return the fixture reply only.'],
                env=env, capture_output=True, text=True, timeout=45)
            (out/'probe-stdout.jsonl').write_text(proc.stdout)
            (out/'probe-stderr.log').write_text(proc.stderr)
            payloads = {p.stem: json.loads(p.read_text()) for p in events.glob('*.json')}
            (out/'probe-events.json').write_text(json.dumps(payloads, indent=2)+'\n')
            for snapshot in events.glob('*-snapshot.jsonl'):
                (out/snapshot.name).write_bytes(snapshot.read_bytes())
            # The exact hook-provided transcript only, never glob other sessions.
            paths = {p.get('transcript_path') for p in payloads.values()} - {None}
            transcripts = []
            for name in paths:
                path = Path(name).resolve()
                if root not in path.parents or not path.is_file():
                    raise RuntimeError('probe transcript escaped isolated root')
                transcripts.append(path.read_text())
            (out/'probe-transcripts.json').write_text(json.dumps(transcripts, indent=2)+'\n')
            report = {'synthetic_only': True, 'version': version, 'returncode': proc.returncode,
                      'events': sorted(payloads), 'transcripts': len(transcripts),
                      'local_requests': requests, 'real_model_requests': 0,
                      'user_configuration_modified': False, 'daily_acceptance': False}
            (out/'probe-receipt.json').write_text(json.dumps(report, indent=2)+'\n')
            print(json.dumps(report))
            if proc.returncode or 'UserPromptSubmit' not in payloads or 'Stop' not in payloads:
                raise SystemExit('probe did not establish prompt and Stop delivery; preserve evidence')
        finally:
            server.shutdown()
            server.server_close()


if __name__ == '__main__':
    main()
