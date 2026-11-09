#!/usr/bin/env python3
"""真实Codex权限profile探针；只读本轮合成文件，不调用模型。"""
import argparse
import json
import os
from pathlib import Path
import select
import signal
import subprocess
import time


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--out', type=Path, required=True)
    parser.add_argument('--executable', type=Path, default=Path('/Applications/ChatGPT.app/Contents/Resources/codex-cli/bin/codex'))
    args = parser.parse_args()
    out = args.out.resolve()
    out.mkdir(parents=True, exist_ok=False, mode=0o700)
    executable = args.executable.resolve()
    repo = out/'project'
    subprocess.run(['/usr/bin/git', 'init', str(repo)], check=True, capture_output=True)
    (repo/'inside.txt').write_text('authorized_project_marker\n')
    (out/'outside.txt').write_text('unauthorized_project_marker\n')
    (repo/'escape.txt').symlink_to(out/'outside.txt')
    (repo/'.codex').mkdir()
    (repo/'.codex/synthetic-session.txt').write_text('private_agent_marker\n')
    (repo/'nested').mkdir()
    (repo/'nested/.env.local').write_text('synthetic_secret_marker\n')
    home = out/'agent-home'
    configuration = home/'.codex'
    configuration.mkdir(parents=True, mode=0o700)
    bundle = next(p for p in executable.parents if p.suffix == '.app')
    profiles = dict(minimal_scope=[repo],
                    base_scope=[repo, Path('/usr'), Path('/bin'), Path('/System')],
                    agent_runtime=[repo, Path('/usr'), Path('/bin'), Path('/System'), bundle],
                    system_runtime=[repo, Path('/usr'), Path('/bin'), Path('/System'), bundle,
                                    Path('/dev'), Path('/private/etc'), Path('/Library'), Path('/private/var/db/dyld')])
    text = 'default_permissions="minimal_scope"\n'
    for name, paths in profiles.items():
        text += '[permissions.'+name+'.filesystem]\n"/"="deny"\n'
        if name == 'minimal_scope':
            text += '":minimal"="read"\n'
        text += ''.join(json.dumps(str(path))+'="read"\n' for path in paths)
        if name == 'minimal_scope':
            text += json.dumps(str(repo/'.codex'))+'="deny"\n'
            text += json.dumps(str(repo)+'/**/.env*')+'="deny"\n'
        text += '[permissions.'+name+'.network]\nenabled=false\n'
    config_path = configuration/'config.toml'
    config_path.write_text(text)
    config_path.chmod(0o600)
    report = dict(status='failed', synthetic_only=True, model_turn_started=False, credentials_copied=False,
                  profile_results=[], cleanup_errors=[])
    messages, pending = [], b''
    process = None
    log = (out/'stderr.log').open('w')

    def send(value):
        process.stdin.write((json.dumps(value)+'\n').encode())
        process.stdin.flush()

    def receive(wanted):
        nonlocal pending
        deadline = time.monotonic()+20
        while time.monotonic() < deadline:
            if b'\n' in pending:
                line, pending = pending.split(b'\n', 1)
                value = json.loads(line)
                messages.append(value)
                if value.get('id') == wanted:
                    return value
                continue
            if process.poll() is not None:
                raise RuntimeError('app-server exited: '+str(process.returncode))
            if select.select([process.stdout], [], [], .2)[0]:
                chunk = os.read(process.stdout.fileno(), 65536)
                if not chunk:
                    raise EOFError('app-server stdout closed')
                pending += chunk
        raise TimeoutError('RPC '+str(wanted))

    try:
        process = subprocess.Popen([str(executable), 'app-server', '--strict-config', '--stdio'], cwd=repo,
                                   env={'PATH': '/usr/bin:/bin', 'HOME': str(home)}, stdin=subprocess.PIPE,
                                   stdout=subprocess.PIPE, stderr=log, start_new_session=True, bufsize=0)
        report['pid'] = process.pid
        send(dict(id=1, method='initialize', params=dict(clientInfo=dict(name='memlane_scope_probe', version='1'),
                                                        capabilities=dict(experimentalApi=True))))
        assert 'result' in receive(1)
        send(dict(method='initialized', params={}))
        send(dict(id=2, method='config/read', params=dict(includeLayers=False, cwd=str(repo))))
        reply = receive(2)
        assert reply['result']['config']['default_permissions'] == 'minimal_scope', reply
        send(dict(id=3, method='thread/start', params=dict(cwd=str(repo), permissions='minimal_scope',
                  ephemeral=True, approvalPolicy='never', allowProviderModelFallback=False)))
        reply = receive(3)
        report['thread_start'] = reply
        assert reply['result']['activePermissionProfile']['id'] == 'minimal_scope', reply
        request_id = 3
        for name in ('read_only_control', *profiles):
            for path in (repo/'inside.txt', out/'outside.txt'):
                request_id += 1
                params = dict(command=['/bin/cat', str(path)], cwd=str(repo), timeoutMs=5000, outputBytesCap=2048)
                if name == 'read_only_control':
                    params['sandboxPolicy'] = dict(type='readOnly', networkAccess=False)
                else:
                    params['permissionProfile'] = name
                send(dict(id=request_id, method='command/exec', params=params))
                reply = receive(request_id)
                item = dict(profile=name, target='inside' if path.parent == repo else 'outside', response=reply)
                report['profile_results'].append(item)
                print(json.dumps(item, ensure_ascii=False), flush=True)
        by_profile = {}
        for item in report['profile_results']:
            by_profile.setdefault(item['profile'], {})[item['target']] = item['response'].get('result', {})
        report['control_executes'] = by_profile['read_only_control']['inside'].get('stdout') == 'authorized_project_marker\n'
        report['working_restricted_profiles'] = [name for name in profiles
             if by_profile[name]['inside'].get('exitCode') == 0 and by_profile[name]['inside'].get('stdout') == 'authorized_project_marker\n'
             and by_profile[name]['outside'].get('exitCode') not in (None, 0, 134)
             and 'unauthorized_project_marker' not in by_profile[name]['outside'].get('stdout', '')]
        probes = [dict(name='symlink_escape', command=['/bin/cat', str(repo/'escape.txt')]),
                  dict(name='private_agent_source', command=['/bin/cat', str(repo/'.codex/synthetic-session.txt')]),
                  dict(name='nested_environment_source', command=['/bin/cat', str(repo/'nested/.env.local')]),
                  dict(name='project_write', command=['/bin/sh', '-c', 'printf changed > inside.txt']),
                  dict(name='outside_write', command=['/bin/sh', '-c', 'printf changed > "$1"',
                                                      'scope-probe', str(out/'outside.txt')])]
        report['denial_results'] = []
        for probe in probes:
            request_id += 1
            params = dict(command=probe['command'], cwd=str(repo), timeoutMs=5000, outputBytesCap=2048,
                          permissionProfile='minimal_scope')
            send(dict(id=request_id, method='command/exec', params=params))
            reply = receive(request_id)
            item = dict(name=probe['name'], response=reply)
            report['denial_results'].append(item)
            print(json.dumps(item, ensure_ascii=False), flush=True)
        report['denials_passed'] = all(item['response'].get('result', {}).get('exitCode') == 1
              and 'Operation not permitted' in item['response']['result'].get('stderr', '')
              and 'unauthorized_project_marker' not in item['response']['result'].get('stdout', '')
              for item in report['denial_results'])
        report['synthetic_files_unchanged'] = (repo/'inside.txt').read_text() == 'authorized_project_marker\n' \
                    and (out/'outside.txt').read_text() == 'unauthorized_project_marker\n'
        if report['control_executes'] and 'minimal_scope' in report['working_restricted_profiles'] \
                and report['denials_passed'] and report['synthetic_files_unchanged']:
            report['status'] = 'passed'
    except Exception as error:
        report['failure'] = repr(error)
    finally:
        if process is not None:
            try:
                if process.poll() is None:
                    os.killpg(process.pid, signal.SIGTERM)
                    try:
                        process.wait(timeout=3)
                    except subprocess.TimeoutExpired:
                        os.killpg(process.pid, signal.SIGKILL)
                        process.wait(timeout=3)
                report['process_reaped'] = process.poll() is not None
            except Exception as error:
                report['cleanup_errors'].append(repr(error))
        log.close()
        (out/'protocol.json').write_text(json.dumps(messages, ensure_ascii=False, indent=2)+'\n')
        (out/'report.json').write_text(json.dumps(report, ensure_ascii=False, indent=2)+'\n')
        print(json.dumps(dict(status=report['status'], report=str(out/'report.json')), ensure_ascii=False), flush=True)
        if report['status'] != 'passed' or report['cleanup_errors']:
            raise SystemExit(1)


if __name__ == '__main__':
    main()
