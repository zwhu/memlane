"""Prepare a private, pinned SDK patch without changing the module cache."""
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import shlex
import shutil
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[1]
MANIFEST = ROOT / 'scripts/patches/mygo-antialiased.json'


def write_changed(path, data):
    if path.exists() and path.read_bytes() == data:
        return
    path.parent.mkdir(parents=True, exist_ok=True)
    # Tests and the UI watcher can prepare the same overlay concurrently.
    with tempfile.NamedTemporaryFile(dir=path.parent, delete=False) as output:
        output.write(data)
        temporary = Path(output.name)
    os.replace(temporary, path)


def prepare(root=ROOT, manifest_path=MANIFEST):
    manifest_bytes = manifest_path.read_bytes()
    manifest = json.loads(manifest_bytes)
    project_mod = (root / 'go.mod').read_text()
    requirement = (r'(?m)^\s*(?:require\s+)?' + re.escape(manifest['module'])
                   + r'\s+' + re.escape(manifest['version']) + r'(?:\s|$)')
    if not re.search(requirement, project_mod):
        raise RuntimeError('Project MyGo version changed; review the pinned SDK patch before building')
    sdk = root / '.local/gopath/pkg/mod' / (manifest['module'] + '@' + manifest['version'])
    target = root / '.local/mygo-sdk' / (manifest['version'] + '-' + hashlib.sha256(manifest_bytes).hexdigest()[:12])
    patched = {}
    for name, patch in manifest['files'].items():
        source = sdk / name
        if not source.is_file():
            raise RuntimeError('MyGo SDK missing; run the documented bootstrap first')
        data = source.read_bytes()
        if hashlib.sha256(data).hexdigest() != patch['sha256']:
            raise RuntimeError(f'MyGo overlay input changed: {name}; review the pinned patch before building')
        body = data.decode()
        for change in patch['replacements']:
            if body.count(change['old']) != 1:
                raise RuntimeError(f'MyGo overlay context is not unique: {name}')
            body = body.replace(change['old'], change['new'], 1)
        patched[name] = body.encode()
    target.parent.mkdir(parents=True, exist_ok=True)
    with (target.parent / '.prepare.lock').open('a+') as lock:
        fcntl.flock(lock.fileno(), fcntl.LOCK_EX)
        if not (target / '.prepared').exists():
            # Go disallows -overlay replacements inside GOMODCACHE. Use a
            # private SDK copy with -modfile, keeping both the cache and the
            # repository's go.mod/go.sum immutable.
            if target.exists():
                raise RuntimeError('Incomplete private MyGo SDK; preserve it and inspect before cleanup')
            with tempfile.TemporaryDirectory(dir=target.parent, prefix='.prepare-') as staging:
                copy = Path(staging) / 'sdk'
                shutil.copytree(sdk, copy)
                for directory, _, _ in os.walk(copy):
                    os.chmod(directory, 0o755)
                for name, body in patched.items():
                    write_changed(copy / name, body)
                write_changed(copy / '.prepared', manifest_bytes)
                os.replace(copy, target)
        if ((target / '.prepared').read_bytes() != manifest_bytes
                or any((target / name).read_bytes() != body for name, body in patched.items())):
            raise RuntimeError('Private MyGo SDK changed after preparation; do not build an unverified copy')
        modfile = target.parent / ('project-' + target.name + '.mod')
        body = project_mod
        body += '\nreplace ' + manifest['module'] + ' => ' + json.dumps(str(target.resolve())) + '\n'
        write_changed(modfile, body.encode())
        write_changed(modfile.with_suffix('.sum'), (root / 'go.sum').read_bytes())
    return modfile


def with_patched_sdk(env):
    env = dict(env)
    flags = shlex.split(env.get('GOFLAGS', ''))
    modfile = str(prepare())
    expected = '-modfile=' + modfile
    others = [flag for flag in flags if flag.startswith('-modfile=') and flag != expected]
    if others or '-modfile' in flags:
        raise RuntimeError('An unrelated Go modfile is active; preserve it and resolve the build configuration explicitly')
    if expected not in flags:
        flags.append(expected)
    env['GOFLAGS'] = shlex.join(flags)
    return env


if __name__ == '__main__':
    if len(sys.argv) < 3 or sys.argv[1] != '--exec':
        raise SystemExit('usage: mygo_sdk.py --exec GO [arguments...]')
    os.execve(sys.argv[2], sys.argv[2:], with_patched_sdk(os.environ))
