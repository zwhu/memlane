#!/usr/bin/env python3
"""One journaled synthetic write through the unmodified official skill script.

Uses the existing daily project and Codex child credential. Never retries an
uncertain write, creates a dataset, changes ACL, or replays conversations.
"""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile
import urllib.request

ROOT = Path(__file__).resolve().parents[1]


def main():
    runtime = json.loads((ROOT / '.local/daily/runtime.json').read_text())
    registry = json.loads(Path(runtime['project_control']['registry_file']).read_text())
    project = next(p for p in registry['projects'].values() if p.get('status') == 'verified' and
                   str(ROOT) in p.get('aliases', []))
    agent = next(a for a in registry['agents'].values() if a.get('kind') == 'codex')
    source = ROOT / '.cache/coding-memory-components/plugin-a176248914fdb32a78354854b300174203b15a5c/integrations/codex/plugins/cognee'
    lock = json.loads((ROOT / 'engine/patches/project-access-upstream.json').read_text())
    for name, digest in lock['files'].items():
        if hashlib.sha256((source / name).read_bytes()).hexdigest() != digest:
            raise SystemExit('unmodified official input required')
    folder = ROOT / '.local/official-agent-convergence-20261117'
    folder.mkdir(mode=0o700, exist_ok=True)
    journal = folder / 'direct-save.json'
    if journal.exists():
        raise SystemExit('existing operation retained; inspect before any further action')
    marker = 'memlane-direct-contract-20261117'
    content = ('# 官方直接保存链路验收样例\n\n' + marker +
               '\n\n这是一条合成验收材料，仅用于验证官方 skill 保存、Memlane 展示和跨 Agent 读取。'
               '不是项目约定、用户偏好或实际业务事实，不应影响后续编码决定。\n')
    state = {'operation': marker, 'status': 'prepared', 'dataset_id': project['dataset_id'],
             'content_sha256': hashlib.sha256(content.encode()).hexdigest()}
    def save():
        temporary = journal.with_suffix('.tmp')
        temporary.write_text(json.dumps(state, ensure_ascii=False, indent=2) + '\n')
        temporary.chmod(0o600)
        temporary.replace(journal)
    save()
    with tempfile.TemporaryDirectory(prefix='official-direct-host-') as host:
        path = Path(host) / (marker + '.txt')
        path.write_text(content)
        env = {k: v for k, v in os.environ.items() if not k.startswith(
            ('COGNEE_', 'MEMLANE_', 'LLM_', 'EMBEDDING_', 'PYTHON'))}
        env.update(HOME=host, COGNEE_ENV_FILE=agent['installation']['env_file'],
                   COGNEE_PLUGIN_DATASET=project['dataset_id'], COGNEE_PLUGIN_IDENTITY='false',
                   COGNEE_SHARED_AGENT_MEMORY='false', COGNEE_REMEMBER_BACKGROUND='false',
                   COGNEE_REMEMBER_WAIT_SECONDS='0', PYTHONDONTWRITEBYTECODE='1')
        state['status'] = 'submitting'
        save()
        try:
            result = subprocess.run(['sh', str(source / 'scripts/cognee-remember.sh'), '--file', str(path),
                                     '--node-set', 'user_context', '--dataset', project['dataset_id']],
                                    cwd=host, env=env, capture_output=True, text=True, timeout=300)
            receipt = json.loads(result.stdout)
        except (subprocess.TimeoutExpired, ValueError):
            state['status'] = 'unknown'
            save()
            raise SystemExit('official write result unknown; original operation retained, no retry')
        state['receipt'] = {k: receipt[k] for k in ('ok', 'status', 'dataset_id', 'pipeline_run_id', 'queryable') if k in receipt}
        state['shell_exit'] = result.returncode
        state['status'] = 'accepted' if receipt.get('ok') else 'unconfirmed'
        save()
        if not receipt.get('ok'):
            raise SystemExit('official write not confirmed; inspect original operation')
    headers = {'X-Api-Key': Path(agent['key_file']).read_text().strip()}
    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, *args, **kwargs): return None
    opener = urllib.request.build_opener(NoRedirect)
    def get(path):
        req = urllib.request.Request(runtime['service_url'].rstrip('/') + path, headers=headers)
        with opener.open(req, timeout=15) as response: return response.read()
    matches = []
    for offset in range(0, 100001, 100):
        rows = json.loads(get('/api/v1/datasets/' + project['dataset_id'] + '/data?limit=100&offset=' + str(offset)))
        # Official Data stores the stem separately from its extension.
        matches.extend(row for row in rows if row.get('name') in (marker, marker + '.txt')
                       and row.get('contentHash') == hashlib.md5(content.encode()).hexdigest())
        if len(rows) < 100: break
    if len(matches) != 1:
        raise SystemExit('accepted write lacks unique Data; inspect journal, do not resubmit')
    document = matches[0]
    raw = get('/api/v1/datasets/' + project['dataset_id'] + '/data/' + document['id'] + '/raw')
    state.update(data_id=document['id'], raw_sha256=hashlib.sha256(raw).hexdigest(),
                 node_sets=document.get('externalMetadata', {}).get('node_set'),
                 status='raw_verified' if raw == content.encode() else 'raw_mismatch')
    save()
    print(json.dumps({k: state[k] for k in ('operation', 'status', 'data_id', 'node_sets')}, ensure_ascii=False))
    return 0 if state['status'] == 'raw_verified' else 1


if __name__ == '__main__':
    raise SystemExit(main())
