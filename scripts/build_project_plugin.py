#!/usr/bin/env python3
"""Replay the PA patch against fixed upstream into a separate dev artifact."""
import argparse
import hashlib
import importlib.util
import json
from pathlib import Path
import shutil

ROOT = Path(__file__).resolve().parents[1]
PATCH = ROOT / 'engine/patches/project_access.py'
EXTENSION = ROOT / 'engine/python/project_plugin_policy.py'
INPUTS = ROOT / 'engine/patches/project-access-upstream.json'


def digest(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def build(source, output):
    source, output = Path(source).resolve(strict=True), Path(output).absolute()
    expected = json.loads(INPUTS.read_text())
    manifest = json.loads((ROOT / 'engine/coding-memory-components.json').read_text())
    if expected['commit'] != manifest['plugin']['commit']:
        raise ValueError('fixed plugin commit mismatch')
    scripts = source / 'scripts'
    for name, sha in expected['scripts'].items():
        if digest(scripts / name) != sha:
            raise ValueError('upstream plugin input mismatch: ' + name)
    source_files = {str(path.relative_to(source)): digest(path) for path in source.rglob('*')
                    if path.is_file() and '__pycache__' not in path.parts and path.suffix != '.pyc'}
    if source_files != expected['files']:
        raise ValueError('upstream plugin tree mismatch')
    if output.exists() or output.is_symlink():
        raise ValueError('output must be a new artifact directory; installed plugins are never edited')
    if source == output or source in output.parents:
        raise ValueError('artifact must be outside upstream source')
    spec = importlib.util.spec_from_file_location('project_access_patch', PATCH)
    patch = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(patch)
    if set(patch.FILES) != set(expected['scripts']):
        raise ValueError('patch input allowlist mismatch')
    # Compute every change and compile before creating the artifact.
    changed = {name: patch.apply(name, (scripts / name).read_text()) for name in patch.FILES}
    for name, value in changed.items():
        if name.endswith('.py'):
            compile(value, name, 'exec')
    compile(EXTENSION.read_text(), '_memlane_policy.py', 'exec')
    shutil.copytree(source, output, ignore=shutil.ignore_patterns('__pycache__', '*.pyc'))
    for name, value in changed.items():
        (output / 'scripts' / name).write_text(value)
    shutil.copyfile(EXTENSION, output / 'scripts/_memlane_policy.py')
    receipt = {'plugin_commit': expected['commit'], 'patch_sha256': digest(PATCH),
               'extension_sha256': digest(EXTENSION), 'upstream_lock_sha256': digest(INPUTS),
               'files': {str(path.relative_to(output)): digest(path) for path in output.rglob('*') if path.is_file()}}
    locked = manifest['plugin'].get('managed_patch_manifest')
    if locked:
        if receipt != json.loads((ROOT / 'engine' / locked).read_text()):
            shutil.rmtree(output)
            raise ValueError('managed plugin lock mismatch; review and update the fixed component input')
    (output / '.memlane-project-access.json').write_text(json.dumps(receipt, indent=2) + '\n')
    return receipt


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--source', required=True, help='fixed integrations/codex/plugins/cognee directory')
    parser.add_argument('--output', required=True, help='new separate managed artifact directory')
    args = parser.parse_args()
    print(json.dumps(build(args.source, args.output), sort_keys=True))
