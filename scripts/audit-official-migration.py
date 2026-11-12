#!/usr/bin/env python3
"""Read-only inventory of an explicit stopped Memlane library; never copies text."""
import argparse
from collections import Counter
import hashlib
import json
import os
from pathlib import Path
import sqlite3
import subprocess


def digest(data):
    return hashlib.sha256(data).hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--database', required=True, type=Path)
    parser.add_argument('--out', required=True, type=Path)
    args = parser.parse_args()
    database = args.database.resolve(strict=True)
    if args.database.is_symlink() or not database.is_file():
        raise SystemExit('explicit regular database required')
    wal = Path(str(database) + '-wal')
    held = subprocess.run(['/usr/sbin/lsof', '-n', '-P', str(database)], capture_output=True, text=True)
    if held.returncode not in (0, 1) or held.stdout.strip() or wal.exists():
        raise SystemExit('offline checkpointed database required; no service will be stopped automatically')
    before = digest(database.read_bytes())
    # A stopped WAL-mode database without sidecars needs immutable to avoid
    # SQLite trying to create a WAL/SHM file. Admission above forbids live use.
    db = sqlite3.connect(database.as_uri() + '?mode=ro&immutable=1', uri=True)
    db.execute('PRAGMA query_only=ON')
    schema = db.execute('PRAGMA user_version').fetchone()[0]
    counts, inventory = Counter(), []
    sql = '''SELECT m.id,m.dataset,m.current_revision,m.state,md.origin,md.source_detail
             FROM memories m JOIN memory_metadata md ON md.memory=m.id ORDER BY m.id'''
    for memory, project, revision, state, origin, detail_json in db.execute(sql):
        detail = json.loads(detail_json) if detail_json else {}
        official = detail.get('official') or {}
        native = detail.get('native') or {}
        refs = [r for r in detail.get('references', []) if r.startswith('cognee://')]
        versions = []
        edited = False
        for number, text, operation in db.execute('SELECT revision,text,operation FROM versions WHERE memory=? ORDER BY revision', (memory,)):
            payload = text.encode()
            versions.append({'revision': number, 'utf8_bytes': len(payload), 'sha256': digest(payload), 'operation': operation})
            edited = edited or operation in ('edit', 'restore')
        classification = 'official_reference_pending_comparison' if refs and (official or native) else 'no_official_identity_preserve'
        counts[classification] += 1
        counts['origin_' + origin] += 1
        if edited:
            counts['edited_or_restored_preserve'] += 1
        inventory.append({'memory': memory, 'project': project, 'current_revision': revision, 'state': state,
                          'origin': origin, 'classification': classification, 'edited_or_restored': edited,
                          'official_references': refs, 'source_metadata_sha256': digest((detail_json or '').encode()),
                          'versions': versions})
    connections = [{'id': r[0], 'agent': r[1], 'enabled': bool(r[2]), 'generation': r[3],
                    'scope_count': len(json.loads(r[4])), 'scope_sha256': digest(r[4].encode())}
                   for r in db.execute('SELECT id,agent,enabled,generation,allowed_scopes FROM connections ORDER BY id')]
    bindings = []
    for instance, dataset, project, payload in db.execute('SELECT instance,dataset,project,payload FROM cognee_bindings ORDER BY instance,dataset'):
        data = json.loads(payload)
        bindings.append({'instance': instance, 'dataset': dataset, 'project': project,
                         'connection_id': data.get('connection_id'), 'generation': data.get('generation'),
                         'material_submission': data.get('material_submission', False)})
    table_counts = {}
    for name in ('memories', 'versions', 'operations', 'jobs', 'organization_sources'):
        table_counts[name] = db.execute('SELECT count(*) FROM ' + name).fetchone()[0]
    db.close()
    artifacts = []
    roots = [database.parent / 'agent-outbox']
    adapters = database.parent / 'agent-adapters'
    if adapters.is_dir():
        roots += [p for p in adapters.iterdir() if p.is_dir() and not p.is_symlink()]
    for root in roots:
        if not root.is_dir() or root.is_symlink():
            continue
        for path in sorted(root.rglob('*.json')):
            if path.is_symlink() or not path.is_file():
                continue
            # Hash the existing queue/state without exporting its body, path
            # declarations, consent text, snippets or Agent configuration.
            raw = path.read_bytes()
            artifacts.append({'relative_path': str(path.relative_to(database.parent)), 'bytes': len(raw), 'sha256': digest(raw)})
    after = digest(database.read_bytes())
    if before != after or wal.exists():
        raise SystemExit('database changed during offline inventory; no migration receipt produced')
    report = {'schema': schema, 'mode': 'offline_read_only_hash_inventory', 'database_sha256_before': before,
              'database_sha256_after': after, 'unchanged': True, 'table_counts': table_counts, 'summary': dict(counts),
              'records': inventory, 'connections': connections, 'bindings': bindings, 'queue_and_adapter_files': artifacts,
              'official_comparison_complete': False, 'migrated': False, 'data_deleted': False,
              'boundary': 'No text, knowledge, history, queue or Agent configuration was copied, changed or removed. Missing official identity does not prove absence upstream.'}
    args.out.parent.mkdir(parents=True, exist_ok=True)
    fd = os.open(args.out, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'w') as file:
        json.dump(report, file, ensure_ascii=False, indent=2)
        file.write('\n')
    print(json.dumps({'out': str(args.out), 'summary': dict(counts), 'table_counts': table_counts,
                      'bindings': len(bindings), 'unchanged': True}, ensure_ascii=False))


if __name__ == '__main__':
    main()
