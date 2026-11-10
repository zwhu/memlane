#!/usr/bin/env python3
"""在全新隔离根中以真实CLI核对worktree会话及官方归档布局。"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import select
import signal
import subprocess
import time


def archive_thread(executable, directory, repo, thread_id, out):
    env = os.environ.copy()
    env['CODEX_HOME'] = str(directory)
    messages, pending = [], b''
    with (out/'archive-stderr.log').open('w') as log:
        process = subprocess.Popen([str(executable), 'app-server', '--stdio'], cwd=repo,
                                   env=env, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                   stderr=log, start_new_session=True, bufsize=0)
        def send(value):
            process.stdin.write((json.dumps(value)+'\n').encode())
            process.stdin.flush()
        def receive(wanted):
            nonlocal pending
            deadline = time.monotonic()+30
            while time.monotonic() < deadline:
                if b'\n' in pending:
                    line, pending = pending.split(b'\n', 1)
                    value = json.loads(line)
                    messages.append(value)
                    if value.get('id') == wanted:
                        assert 'result' in value, value
                        return value['result']
                    continue
                assert process.poll() is None, 'app-server exited'
                if select.select([process.stdout], [], [], .2)[0]:
                    chunk = os.read(process.stdout.fileno(), 65536)
                    assert chunk, 'app-server stdout closed'
                    pending += chunk
            raise TimeoutError('archive RPC '+str(wanted))
        try:
            send(dict(id=1, method='initialize', params=dict(clientInfo=dict(name='memlane_history_layout', version='1'))))
            receive(1)
            send(dict(method='initialized', params={}))
            send(dict(id=2, method='thread/read', params=dict(threadId=thread_id, includeTurns=False)))
            before = receive(2)
            send(dict(id=3, method='thread/archive', params=dict(threadId=thread_id)))
            receive(3)
            send(dict(id=4, method='thread/read', params=dict(threadId=thread_id, includeTurns=False)))
            after = receive(4)
            return before, after
        finally:
            (out/'archive-protocol.json').write_text(json.dumps(messages, ensure_ascii=False, indent=2)+'\n')
            process.stdin.close()
            try:
                process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                os.killpg(process.pid, signal.SIGTERM)
                try:
                    process.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    os.killpg(process.pid, signal.SIGKILL)
                    process.wait(timeout=5)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--out', required=True, type=Path)
    args = parser.parse_args()
    out = args.out.resolve()
    assert not out.exists(), '证据目录必须全新'
    out.mkdir(parents=True)
    report = dict(status='failed', user_historical_sessions_read=False)
    try:
        root = Path(__file__).resolve().parents[1]
        repo, worktree, directory = out/'repo', out/'worktree', out/'codex'
        subprocess.run(['/usr/bin/git', 'init', str(repo)], capture_output=True, check=True)
        (repo/'README.md').write_text('本轮合成项目；发布仅使用release分支。\n')
        subprocess.run(['/usr/bin/git', '-C', str(repo), 'add', 'README.md'], capture_output=True, check=True)
        subprocess.run(['/usr/bin/git', '-C', str(repo), '-c', 'user.name=Memlane Test', '-c', 'user.email=test@example.invalid', 'commit', '-m', 'synthetic'], capture_output=True, check=True)
        subprocess.run(['/usr/bin/git', '-C', str(repo), 'worktree', 'add', '-b', 'history-test', str(worktree)], capture_output=True, check=True)
        original = Path('/Users/bytedance/.codex/config.toml')
        original_digest = hashlib.sha256(original.read_bytes()).hexdigest()
        scalar = 'import tomllib,json,sys;v=tomllib.load(open(sys.argv[1],"rb"));k=["model","model_provider","model_reasoning_effort","model_reasoning_summary","model_verbosity","service_tier"];o={s:v[s] for s in k if s in v};assert "openai" not in v.get("model_providers",{});print(json.dumps(o))'
        settings = json.loads(subprocess.run([str(root/'.local/cognee-python/bin/python'), '-c', scalar, str(original)], capture_output=True, check=True).stdout)
        assert settings.get('model') and settings.get('model_provider', 'openai') == 'openai'
        directory.mkdir(mode=0o700)
        (directory/'config.toml').write_text(''.join(k+' = '+json.dumps(v)+'\n' for k,v in settings.items()))
        (directory/'auth.json').symlink_to('/Users/bytedance/.codex/auth.json')
        executable = Path('/Applications/ChatGPT.app/Contents/Resources/codex-cli/bin/codex')
        env = os.environ.copy(); env['CODEX_HOME'] = str(directory)
        with (out/'normal-events.jsonl').open('w') as log, (out/'normal-stderr.log').open('w') as err:
            result = subprocess.run([str(executable), 'exec', '--json', '--sandbox', 'read-only', '--cd', str(worktree), '请只复述用户要求：本项目正式发布仅用release分支。不要执行工具。'], env=env, stdout=log, stderr=err, timeout=180)
        assert result.returncode == 0
        logs = list((directory/'sessions').rglob('*.jsonl'))
        assert len(logs) == 1
        source = logs[0]; raw = source.read_bytes(); meta = json.loads(raw.splitlines()[0])
        thread_id = meta['payload']['id']
        assert meta['payload']['cwd'] == str(worktree)
        before, after = archive_thread(executable, directory, worktree, thread_id, out)
        archived = list((directory/'archived_sessions').rglob('*.jsonl'))
        assert len(archived) == 1 and archived[0].parent == directory/'archived_sessions'
        assert not source.exists() and archived[0].name == source.name and archived[0].read_bytes() == raw
        assert before['thread']['id'] == after['thread']['id'] == thread_id
        assert Path(after['thread']['path']) == archived[0]
        assert hashlib.sha256(original.read_bytes()).hexdigest() == original_digest
        report.update(status='passed', cli_version=meta['payload']['cli_version'], model=settings['model'],
                      session=thread_id, source=str(source), archived=str(archived[0]),
                      raw_digest=hashlib.sha256(raw).hexdigest(), archive_keeps_bytes=True,
                      worktree=str(worktree), repository=str(repo), default_config_unchanged=True)
    except Exception as e:
        report['error'] = repr(e)
        raise
    finally:
        (out/'report.json').write_text(json.dumps(report, ensure_ascii=False, indent=2)+'\n')


if __name__ == '__main__':
    main()
