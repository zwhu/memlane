"""Offline, pinned OpenCode artifacts and private immutable installation bindings."""
import base64
import hashlib
import io
import json
import os
from pathlib import Path, PurePosixPath
import platform
import shutil
import subprocess
import tarfile
import tempfile

from project_control import private_write, read_secret
from project_policy import PolicyError
from opencode_plugin_patch import (managed_http_client, managed_outbox, managed_capture_callbacks,
                                   managed_config_recall, managed_action_callbacks)

ROOT = Path(__file__).resolve().parents[2]
SUPPORT = ('managed-control.ts', 'managed-delivery.ts', 'managed-capture-window.ts',
           'managed-config.ts', 'managed-recall.ts', 'managed-actions.ts', 'managed-pending.ts', 'managed-pending-cli.ts')


def digest(data):
    return hashlib.sha256(data).hexdigest()


def encoded(value):
    return (json.dumps(value, sort_keys=True, indent=2) + '\n').encode()


def member_name(name, prefix):
    if not name.startswith(prefix):
        raise ValueError('package archive prefix mismatch')
    value = name[len(prefix):]
    path = PurePosixPath(value)
    if not value or path.is_absolute() or '..' in path.parts or '\\' in value:
        raise ValueError('unsafe package archive path')
    return str(path)


def unpack(raw, prefix):
    result = {}
    with tarfile.open(fileobj=io.BytesIO(raw), mode='r:gz') as bundle:
        for member in bundle:
            if member.isdir():
                continue
            if not member.isfile() or member.size > 64 * 1024 * 1024:
                raise ValueError('unsafe package archive member')
            name = member_name(member.name, prefix)
            if name in result:
                raise ValueError('duplicate package archive member')
            result[name] = bundle.extractfile(member).read()
    return result


def tree_files(root):
    root = Path(root)
    if root.is_symlink() or root.resolve() != root.absolute():
        raise ValueError('artifact requires canonical path')
    result = {}
    for path in root.rglob('*'):
        if path.is_symlink():
            raise ValueError('artifact symlink rejected')
        if path.is_file() and path != root / '.memlane-opencode-artifact.json':
            result[str(path.relative_to(root))] = digest(path.read_bytes())
    return result


def verify(root, expected_manifest=None):
    root = Path(root)
    path = root / '.memlane-opencode-artifact.json'
    if path.is_symlink():
        raise ValueError('artifact receipt symlink rejected')
    raw = path.read_bytes()
    if expected_manifest and digest(raw) != expected_manifest:
        raise ValueError('artifact manifest changed')
    manifest = json.loads(raw)
    if manifest.get('protocol') != 1 or manifest.get('files') != tree_files(root):
        raise ValueError('artifact tree changed')
    return manifest


def build(output, npm_cache, *, node='node', archive=None):
    """Verify cached tarballs, compile in a temporary tree, publish a new artifact."""
    output, npm_cache = Path(output).absolute(), Path(npm_cache).resolve(strict=True)
    if output.exists() or output.is_symlink():
        raise ValueError('artifact output must be new')
    component = json.loads((ROOT / 'engine/coding-memory-components.json').read_text())['plugin']
    archive = Path(archive) if archive else ROOT / '.cache/coding-memory-components' / (component['commit'] + '.tar.gz')
    raw = archive.read_bytes()
    if digest(raw) != component['archive_sha256']:
        raise ValueError('fixed official archive mismatch')
    prefix = 'cognee-integrations-' + component['commit'] + '/'
    original = {}
    license_text = None
    with tarfile.open(fileobj=io.BytesIO(raw), mode='r:gz') as bundle:
        for member in bundle:
            if member.name == prefix + 'LICENSE' and member.isfile():
                license_text = bundle.extractfile(member).read()
            start = prefix + 'integrations/opencode/'
            if member.name.startswith(start) and not member.isdir():
                if not member.isfile():
                    raise ValueError('unsafe upstream member')
                name = member_name(member.name, start)
                if name in original:
                    raise ValueError('duplicate upstream member')
                original[name] = bundle.extractfile(member).read()
    if len(original) != 13 or not license_text:
        raise ValueError('fixed official source tree mismatch')
    lock = json.loads(original['package-lock.json'])
    sources = {name: (ROOT / 'engine/agents/opencode' / name).read_bytes() for name in SUPPORT}
    patch_digest = digest((ROOT / 'engine/python/opencode_plugin_patch.py').read_bytes())
    os_name = {'Darwin': 'darwin', 'Linux': 'linux', 'Windows': 'win32'}.get(platform.system())
    cpu = {'arm64': 'arm64', 'aarch64': 'arm64', 'x86_64': 'x64', 'AMD64': 'x64'}.get(platform.machine())
    if not os_name or not cpu:
        raise ValueError('unsupported artifact platform')
    dependencies, licenses, omitted = {}, [], []
    for name, spec in lock['packages'].items():
        if not name:
            continue
        if not name.startswith('node_modules/') or '..' in PurePosixPath(name).parts:
            raise ValueError('unsupported dependency path')
        incompatible = (spec.get('os') and os_name not in spec['os']) or (spec.get('cpu') and cpu not in spec['cpu'])
        if incompatible:
            if not spec.get('optional'):
                raise ValueError('incompatible required dependency')
            omitted.append(name)
            continue
        algorithm, encoded_hash = spec['integrity'].split('-', 1)
        if algorithm != 'sha512':
            raise ValueError('unsupported dependency integrity')
        expected = base64.b64decode(encoded_hash, validate=True).hex()
        cached = npm_cache / '_cacache/content-v2/sha512' / expected[:2] / expected[2:4] / expected[4:]
        package_raw = cached.read_bytes()
        if hashlib.sha512(package_raw).hexdigest() != expected:
            raise ValueError('cached dependency integrity mismatch')
        package_prefix = ('node v' + '.'.join(spec['version'].split('.')[:2]) + '/') if name == 'node_modules/@types/node' else 'package/'
        files = unpack(package_raw, package_prefix)
        package = json.loads(files['package.json'])
        if package['name'] != name.split('node_modules/')[-1] or package['version'] != spec['version']:
            raise ValueError('cached dependency identity mismatch')
        dependencies[name] = (spec, files)
        licenses.append({'name': package['name'], 'version': spec['version'], 'integrity': spec['integrity'],
                         'development_only': bool(spec.get('dev')), 'declared_license': package.get('license'),
                         'license_files': [key for key in files if 'license' in Path(key).name.lower() or 'notice' in Path(key).name.lower()]})
    node_version = subprocess.run([node, '--version'], capture_output=True, text=True, timeout=10, check=True).stdout.strip()
    recipe = {'upstream_commit': component['commit'], 'archive_sha256': component['archive_sha256'],
              'lock_sha256': digest(original['package-lock.json']), 'patch_sha256': patch_digest,
              'builder_sha256': digest(Path(__file__).read_bytes()), 'node_version': node_version,
              'platform': os_name, 'cpu': cpu, 'omitted_optional_dependencies': omitted,
              'support_sha256': {key: digest(value) for key, value in sources.items()}}
    output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='.opencode-build-', dir=output.parent) as temporary:
        work = Path(temporary).resolve()
        for name, data in original.items():
            path = work / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(data)
        (work / 'src/client.ts').write_text(managed_http_client(original['src/client.ts'].decode()))
        (work / 'src/runtime.ts').write_text(managed_outbox(original['src/runtime.ts'].decode()))
        (work / 'src/plugin.ts').write_text(managed_action_callbacks(managed_config_recall(managed_capture_callbacks(original['src/plugin.ts'].decode()))))
        for name, data in sources.items():
            (work / 'src' / name).write_bytes(data)
        for name, (_, files) in dependencies.items():
            for relative, data in files.items():
                path = work / name / relative
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(data)
        compile_result = subprocess.run([node, str(work / 'node_modules/typescript/bin/tsc'), '--strict',
                                         '--target', 'ES2022', '--module', 'NodeNext', '--moduleResolution', 'NodeNext',
                                         '--skipLibCheck', '--types', 'node', '--rootDir', str(work),
                                         '--outDir', str(work / 'dist'), str(work / 'index.ts'), str(work / 'src/managed-pending.ts'), str(work / 'src/managed-pending-cli.ts')],
                                        cwd=work, capture_output=True, text=True, timeout=60)
        if compile_result.returncode:
            raise ValueError('fixed OpenCode TypeScript compilation failed: ' + compile_result.stdout + compile_result.stderr)
        ready = work / 'artifact'
        ready.mkdir(mode=0o700)
        for name, data in original.items():
            path = ready / 'upstream' / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(data)
        (ready / 'upstream/LICENSE').write_bytes(license_text)
        (ready / 'package.json').write_bytes(encoded({'name': '@memlane/managed-opencode', 'private': True,
                                                            'type': 'module', 'exports': './dist/index.js'}))
        shutil.copytree(work / 'dist', ready / 'dist')
        for name, (spec, _) in dependencies.items():
            if not spec.get('dev'):
                target = ready / name
                target.parent.mkdir(parents=True, exist_ok=True)
                shutil.copytree(work / name, target, dirs_exist_ok=True)
        (ready / 'dependency-licenses.json').write_bytes(encoded(licenses))
        manifest = {'protocol': 1, 'recipe': recipe, 'generation': digest(encoded(recipe))[:24], 'files': tree_files(ready)}
        (ready / '.memlane-opencode-artifact.json').write_bytes(encoded(manifest))
        if output.exists() or output.is_symlink():
            raise ValueError('artifact output appeared during build')
        os.rename(ready, output)
    verify(output, digest(encoded(manifest)))
    return {'plugin_dir': str(output), 'manifest_sha256': digest(encoded(manifest)),
            'generation': manifest['generation'], 'runtime_files': len(manifest['files'])}


def loader(installation):
    """One global host initializer; verify every dependency before dynamic import."""
    parameters = {key: installation[key] for key in ('plugin_dir', 'manifest_sha256', 'binding_file', 'binding_sha256')}
    return ('''import { createHash } from "node:crypto";
import { lstatSync, readFileSync, readdirSync, realpathSync } from "node:fs";
import { join, relative, isAbsolute } from "node:path";
import { pathToFileURL } from "node:url";
const expected = ''' + json.dumps(parameters) + ''';
const fail = () => { throw new Error("memlane_opencode_installation_unconfirmed"); };
const sha = data => createHash("sha256").update(data).digest("hex");
const file = (path, limit, privateOnly = false) => {
  if (!isAbsolute(path) || realpathSync(path) !== path) fail();
  const stat = lstatSync(path);
  if (!stat.isFile() || stat.isSymbolicLink() || stat.size > limit || (privateOnly && (stat.mode & 0o077))) fail();
  return readFileSync(path);
};
export default async (ctx) => {
  if (process.env.MEMLANE_PROCESSING_SESSION === "1") return {};
  if (realpathSync(expected.plugin_dir) !== expected.plugin_dir) fail();
  const manifestPath = join(expected.plugin_dir, ".memlane-opencode-artifact.json");
  const manifestRaw = file(manifestPath, 8 * 1024 * 1024);
  if (sha(manifestRaw) !== expected.manifest_sha256) fail();
  const manifest = JSON.parse(manifestRaw);
  if (manifest.protocol !== 1 || !manifest.files || typeof manifest.files !== "object") fail();
  const found = {};
  const visit = directory => {
    for (const name of readdirSync(directory)) {
      const path = join(directory, name), stat = lstatSync(path);
      if (stat.isSymbolicLink()) fail();
      if (stat.isDirectory()) visit(path);
      else if (path !== manifestPath) {
        if (!stat.isFile()) fail();
        found[relative(expected.plugin_dir, path).split("\\\\").join("/")] = sha(file(path, 64 * 1024 * 1024));
      }
    }
  };
  visit(expected.plugin_dir);
  if (Object.keys(found).length !== Object.keys(manifest.files).length || Object.entries(found).some(([name, digest]) => manifest.files[name] !== digest)) fail();
  const bindingRaw = file(expected.binding_file, 65536, true);
  if (sha(bindingRaw) !== expected.binding_sha256) fail();
  const binding = JSON.parse(bindingRaw);
  if (binding.version !== 1 || binding.plugin_root !== expected.plugin_dir) fail();
  for (const secret of binding.secrets) if (sha(file(secret.path, 65536, true)) !== secret.sha256) fail();
  const plugin = await import(pathToFileURL(join(expected.plugin_dir, "dist/index.js")).href);
  return plugin.default(ctx, {memlane: binding.memlane, cognee: binding.cognee});
};
''').encode()


def pending_runtime(manifest, node='node'):
    """Resolve management runtime before provisioning or writing a binding."""
    if 'dist/src/managed-pending-cli.js' not in manifest['files']:
        return {}
    import re
    try:
        if not isinstance(node, str) or (node != 'node' and not Path(node).is_absolute()):
            raise ValueError()
        executable = Path(shutil.which(node) or node).resolve(strict=True)
        if not executable.is_file() or not os.access(executable, os.X_OK) or executable.stat().st_size > 512 * 1024 * 1024:
            raise ValueError()
        environment = {key: value for key, value in os.environ.items() if not key.startswith('NODE_')}
        version = subprocess.run([str(executable), '--version'], env=environment, capture_output=True,
                                 text=True, check=True, timeout=5).stdout.strip()
        if not re.fullmatch(r'v\d+\.\d+\.\d+', version) or int(version[1:].split('.')[0]) < 20:
            raise ValueError()
        return {'node_executable': str(executable), 'node_sha256': digest(executable.read_bytes()),
                'node_version': version, 'helper_sha256': manifest['files']['dist/src/managed-pending-cli.js']}
    except (OSError, ValueError, subprocess.SubprocessError):
        raise PolicyError('opencode_pending_runtime_unconfirmed', 503) from None


def prepare(artifact, root, instance_id, agent, service_url, control_url, profile, *, artifact_sha256, node='node'):
    import re
    from urllib.parse import urlsplit
    if not re.fullmatch(r'[a-zA-Z0-9_-]{1,64}', instance_id):
        raise PolicyError('invalid_installation_id')
    root, artifact, profile = Path(root).absolute(), Path(artifact).resolve(strict=True), Path(profile).absolute()
    if root.resolve() != root or profile.resolve() != profile:
        raise PolicyError('canonical_host_paths_required')
    for value in (service_url, control_url):
        url = urlsplit(value)
        if (url.scheme != 'http' or url.hostname != '127.0.0.1' or not url.port or url.username or url.password
                or url.path not in ('', '/') or url.query or url.fragment):
            raise PolicyError('installation_local_endpoint_required')
    if not isinstance(artifact_sha256, str) or not re.fullmatch(r'[0-9a-f]{64}', artifact_sha256):
        raise PolicyError('fixed_opencode_artifact_required')
    manifest = verify(artifact, artifact_sha256)
    component = json.loads((ROOT / 'engine/coding-memory-components.json').read_text())['plugin']
    if (manifest['recipe']['upstream_commit'] != component['commit']
            or manifest['recipe']['archive_sha256'] != component['archive_sha256']):
        raise PolicyError('fixed_opencode_artifact_required')
    runtime = pending_runtime(manifest, node)
    secrets = []
    for field in ('key_file', 'control_token_file'):
        path = Path(agent[field]).absolute()
        if path.resolve(strict=True) != path or path.is_symlink() or path.stat().st_mode & 0o077:
            raise PolicyError('private_installation_credential_required')
        if len(read_secret(path)) < 32:
            raise PolicyError('private_installation_credential_required')
        secrets.append({'path': str(path), 'sha256': digest(path.read_bytes())})
    state = root / 'opencode-state' / instance_id
    binding = {'version': 1, 'plugin_root': str(artifact), 'profile': str(profile), 'instance_id': instance_id,
               'secrets': secrets,
               'memlane': {'instanceId': instance_id, 'origin': control_url.rstrip('/'),
                           'tokenFile': str(Path(agent['control_token_file']).absolute()), 'stateDir': str(state / 'bindings')},
               'cognee': {'baseUrl': service_url.rstrip('/'), 'apiKeyFile': str(Path(agent['key_file']).absolute()),
                          'stateDir': str(state / 'outbox')}}
    raw = encoded(binding)
    generation = digest(raw + encoded(manifest))[:24]
    binding_file = root / 'opencode-installations' / instance_id / generation / 'binding.json'
    if binding_file.exists():
        if binding_file.is_symlink() or binding_file.read_bytes() != raw or binding_file.stat().st_mode & 0o077:
            raise PolicyError('existing_installation_changed', 409)
    else:
        private_write(binding_file, raw)
    installation = {'plugin_dir': str(artifact), 'manifest_sha256': digest((artifact / '.memlane-opencode-artifact.json').read_bytes()),
                    'binding_file': str(binding_file), 'binding_sha256': digest(raw), 'generation': generation,
                    'host_kind': 'opencode', 'instance_id': instance_id, 'profile': str(profile), 'status': 'prepared'}
    installation['loader_sha256'] = digest(loader(installation))
    installation.update(runtime)
    return installation
