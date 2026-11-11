#!/usr/bin/env python3
"""Read-only IPC evidence for an explicitly marked isolated Native batch run."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import socket
import uuid


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--out', type=Path, required=True)
    parser.add_argument('--stage', required=True)
    args = parser.parse_args()
    out = args.out.resolve()
    source = json.loads((out / 'source-info.json').read_text())
    config = json.loads((out / 'runtime-config.json').read_text())
    root = Path(config['root']).resolve()
    assert source['synthetic_only'] is True
    assert source['identifier'].startswith('io.github.zwhu.memlane.batchnative.')
    assert root == out / 'client' and re.fullmatch(r'[a-z0-9-]+', args.stage)
    data_root = root / 'data'
    address = Path('/tmp') / ('memlane-' + str(os.getuid()) + '-' + hashlib.sha256(str(data_root).encode()).hexdigest()[:12]) / 'service.sock'

    def read(op, **fields):
        request = dict(protocol=1, id=str(uuid.uuid4()), op=op, **fields)
        with socket.socket(socket.AF_UNIX) as connection:
            connection.settimeout(15)
            connection.connect(str(address))
            connection.sendall(json.dumps(request).encode() + b'\n')
            with connection.makefile('rb') as stream:
                reply = json.loads(stream.readline(4 * 1024 * 1024))
        assert reply['ok'], reply
        return reply['result']

    batch = read('agent_connection_batch')
    connections = read('connections')
    scopes = read('scopes')
    totals = {kind: read('list', list_query=dict(kind=kind, limit=1))['total'] for kind in ('knowledge', 'raw')}
    assert totals == dict(knowledge=0, raw=0), totals
    evidence = dict(synthetic_only=True, stage=args.stage, batch=batch,
                    connections=connections, memory_totals=totals, project_count=sum(s['kind'] == 'project' for s in scopes))
    destination = out / ('state-' + args.stage + '.json')
    with destination.open('x') as stream:
        json.dump(evidence, stream, ensure_ascii=False, indent=2)
    current = batch.get('batch')
    print(json.dumps(dict(path=str(destination), state=current['state'] if current else None,
                          items=[dict(state=i['state'], attempt=i['attempt'], connection=i['connection_id']) for i in current['items']] if current else [],
                          project_count=evidence['project_count'])))


if __name__ == '__main__':
    main()
