"""Fixed public source inputs shared by the component builder and packager."""
import hashlib
import json
from pathlib import Path, PurePosixPath
import shutil
import tarfile
import tempfile
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
LOCK = ROOT / 'engine/coding-memory-components.json'


def digest(path):
    value = hashlib.sha256()
    with Path(path).open('rb') as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b''):
            value.update(chunk)
    return value.hexdigest()


def recipe():
    value = json.loads(LOCK.read_text())
    if value['protocol'] != 1 or value['service']['sdk_version'] != '1.6.7':
        raise ValueError('unsupported_coding_memory_recipe')
    return value


def contract():
    value = recipe()
    return {'protocol': 1, 'service_commit': value['service']['commit'],
            'service_archive_sha256': value['service']['archive_sha256'],
            'sdk_version': value['service']['sdk_version'],
            'component_version': value['service']['component_version'],
            'plugin_commit': value['plugin']['commit'], 'plugin_version': value['plugin']['version']}


def obtain_archive(item, cache):
    cache = Path(cache)
    cache.mkdir(parents=True, exist_ok=True)
    target = cache / (item['commit'] + '.tar.gz')
    if target.exists():
        if target.is_symlink() or not target.is_file() or digest(target) != item['archive_sha256']:
            raise ValueError('fixed_source_archive_changed')
        return target
    # Reuse the already verified source archive, without another download.
    previous = ROOT / '.cache/coding-memory-source-ec6f408/source.tar.gz'
    if previous.is_file() and not previous.is_symlink() and digest(previous) == item['archive_sha256']:
        shutil.copyfile(previous, target)
        return target
    fd, temporary = tempfile.mkstemp(prefix='.source-', dir=cache)
    import os
    try:
        with os.fdopen(fd, 'wb') as output, urllib.request.urlopen(item['archive_url'], timeout=45) as source:
            total = 0
            while True:
                chunk = source.read(1024 * 1024)
                if not chunk:
                    break
                total += len(chunk)
                if total > 100 * 1024 * 1024:
                    raise ValueError('fixed_source_archive_unbounded')
                output.write(chunk)
        if digest(temporary) != item['archive_sha256']:
            raise ValueError('fixed_source_archive_changed')
        Path(temporary).replace(target)
    finally:
        Path(temporary).unlink(missing_ok=True)
    return target


def prepare_source(kind, cache):
    value = recipe()
    item = value[kind]
    archive = obtain_archive(item, cache)
    tree = Path(cache) / (kind + '-' + item['commit'])
    # All extraction paths and content come from the checksum-verified archive;
    # no SDK import, dependency install, model or live database is involved.
    with tarfile.open(archive) as source:
        selected = []
        roots = set()
        for member in source.getmembers():
            parts = PurePosixPath(member.name).parts
            roots.add(parts[0])
            relative = PurePosixPath(*parts[1:])
            if relative.is_absolute() or '..' in relative.parts:
                raise ValueError('fixed_source_path_invalid')
            if kind == 'service':
                include = bool(relative.parts) and (relative.parts[0] in item['packages'] or str(relative) in ('pyproject.toml', 'LICENSE', 'README.md'))
            else:
                include = str(relative) in ('.agents/plugins/marketplace.json', 'LICENSE') or str(relative).startswith('integrations/codex/plugins/cognee/')
            if include and not member.isdir():
                if not member.isfile():
                    raise ValueError('fixed_source_link_unsupported')
                selected.append((member, Path(str(relative))))
        if len(roots) != 1 or not selected:
            raise ValueError('fixed_source_layout_invalid')
        expected = {}
        if tree.is_symlink():
            raise ValueError('fixed_source_tree_changed')
        tree.mkdir(parents=True, exist_ok=True)
        for member, relative in selected:
            raw = source.extractfile(member).read()
            expected[relative.as_posix()] = hashlib.sha256(raw).hexdigest()
            target = tree / relative
            parent = tree
            for part in relative.parts[:-1]:
                parent = parent / part
                if parent.is_symlink() or (parent.exists() and not parent.is_dir()):
                    raise ValueError('fixed_source_tree_changed')
            if target.exists() or target.is_symlink():
                if target.is_symlink() or not target.is_file() or digest(target) != expected[relative.as_posix()]:
                    raise ValueError('fixed_source_tree_changed')
            else:
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_bytes(raw)
                target.chmod(member.mode & 0o777)
        actual = {p.relative_to(tree).as_posix() for p in tree.rglob('*') if p.is_file() and '__pycache__' not in p.parts and p.suffix != '.pyc'}
        if actual != set(expected):
            raise ValueError('fixed_source_tree_changed')
    if kind == 'plugin':
        manifest = json.loads((tree / 'integrations/codex/plugins/cognee/.codex-plugin/plugin.json').read_text())
        if manifest['version'] != item['version']:
            raise ValueError('fixed_plugin_version_changed')
    return tree, expected
