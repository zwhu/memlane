#!/usr/bin/env python3
"""真实私有服务/worker 与 MCP/CLI 协议验收；不是 Agent 端验收。"""
import hashlib
import json
import os
import queue
import socket
import sqlite3
import subprocess
import tempfile
import threading
import time
import uuid
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / 'build/p2'
OUT.mkdir(parents=True, exist_ok=True)
REPORT = OUT / 'contract-verification.json'


def wait(action, timeout=65):
    deadline = time.monotonic() + timeout
    last = None
    while time.monotonic() < deadline:
        try:
            value = action()
            if value:
                return value
        except (OSError, AssertionError) as error:
            last = error
        time.sleep(.1)
    raise RuntimeError(f'timed out: {last}')


class MCP:
    def __init__(self, arguments, cwd, log):
        self.p = subprocess.Popen(arguments, cwd=cwd, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                  stderr=log, text=True, bufsize=1)
        self.responses = queue.Queue()
        def consume():
            for line in self.p.stdout:
                self.responses.put(json.loads(line))
        self.thread = threading.Thread(target=consume, daemon=True)
        self.thread.start()
        self.sequence = 0
        self.call('initialize', {'protocolVersion': '2025-03-26',
                  'capabilities': {}, 'clientInfo': {'name': 'memlane-protocol-check', 'version': '1'}})
        self.send({'jsonrpc': '2.0', 'method': 'notifications/initialized'})

    def send(self, request):
        self.p.stdin.write(json.dumps(request) + '\n')
        self.p.stdin.flush()

    def call(self, method, params):
        self.sequence += 1
        self.send({'jsonrpc': '2.0', 'id': self.sequence, 'method': method, 'params': params})
        while True:
            result = self.responses.get(timeout=130)
            if result.get('id') == self.sequence:
                return result

    def tool(self, name, arguments, code=None):
        response = self.call('tools/call', {'name': name, 'arguments': arguments})
        assert 'result' in response, response
        result = response['result']
        out = json.loads(result['content'][0]['text'])
        assert result.get('structuredContent') == out, result
        assert out['ok'] == (code is None), out
        assert bool(result.get('isError')) == (code is not None), result
        if code:
            assert out['error']['code'] == code, out
            return out
        return out['result']

    def close(self):
        self.p.stdin.close()
        try:
            self.p.wait(timeout=5)
        except subprocess.TimeoutExpired:
            self.p.terminate()
            self.p.wait(timeout=5)
        assert self.p.returncode == 0, self.p.returncode


def main():
    report = {'status': 'failed', 'synthetic_only': True, 'evidence_kind': 'protocol_with_real_service_worker',
              'real_agent_verified': False, 'checks': []}
    daemon = None
    clients = []
    root = Path(tempfile.mkdtemp(prefix='memlane-p2-contract-'))
    data = root / 'data'
    data.mkdir()
    old = Path((ROOT / 'build/p0-engine/client-fixture-path.txt').read_text().strip())
    assert 'memlane-client-p0-' in str(old)
    previous = json.loads((old / 'client/service.json').read_text())
    config = {**previous, 'root': str(data), 'worker': {**previous['worker'], 'DataDir': str(data/'index'), 'DenyNetwork': True}, 'retry_delay_ms': 500}
    configpath = root / 'service.json'
    configpath.write_text(json.dumps(config))
    ipc = f"/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock"
    logs = (OUT / 'contract-daemon.log').open('w')
    bridge_logs = (OUT / 'contract-mcp.log').open('w')
    report['fixture'] = str(root)

    def call(op, **fields):
        with socket.socket(socket.AF_UNIX) as s:
            s.settimeout(125)
            s.connect(ipc)
            s.sendall((json.dumps({'protocol': 1, 'id': str(uuid.uuid4()), 'op': op, **fields})+'\n').encode())
            response = json.loads(s.makefile('rb', buffering=0).readline())
        assert response['ok'], response
        return response.get('result')

    def start():
        return subprocess.Popen([str(OUT/'memlaned'), '--config', str(configpath)], cwd=root,
                                env={'PATH': '/usr/bin:/bin', 'HOME': str(root/'home')}, stderr=logs)

    def check(name, action):
        began = time.monotonic()
        evidence = action()
        entry = {'name': name, 'status': 'passed', 'seconds': round(time.monotonic()-began, 3), 'evidence': evidence}
        report['checks'].append(entry)
        REPORT.write_text(json.dumps(report, ensure_ascii=False, indent=2)+'\n')
        print(json.dumps(entry, ensure_ascii=False), flush=True)

    def git(*args):
        return subprocess.check_output(['/usr/bin/git', *args], stderr=subprocess.DEVNULL, text=True).strip()

    def repo(name):
        path = root/name
        path.mkdir()
        git('init', str(path))
        git('-C', str(path), '-c', 'user.name=Synthetic', '-c', 'user.email=synthetic@example.invalid', 'commit', '--allow-empty', '-m', 'synthetic')
        return path

    def connection(agent, project, allowed):
        return call('configure_connection', user_confirmed=True, connection={
                    'agent': agent, 'name': agent+' synthetic', 'project': project, 'allowed_scopes': allowed})

    def command(c, mode='mcp', workspace=None):
        cmd = [str(OUT/'memlane'), mode, '--root', str(data), '--connection', c['id'], '--generation', str(c['generation'])]
        if workspace:
            cmd += ['--workspace', str(workspace)]
        return cmd

    def client(c, workspace=None, cwd=None):
        mcp = MCP(command(c, workspace=workspace), cwd or root, bridge_logs)
        clients.append(mcp)
        return mcp

    try:
        # Seed a v1 business database by running the committed P1 daemon only
        # on this new synthetic root. Reuse no user database/configuration.
        baseline = ROOT / 'build/p1/memlaned'
        assert baseline.is_file(), 'P1 baseline daemon missing'
        daemon = subprocess.Popen([str(baseline), '--config', str(configpath)], cwd=root, stderr=logs)
        wait(lambda: call('health')['state']=='ready')
        legacy_dataset, legacy_memory = str(uuid.uuid4()), str(uuid.uuid4())
        legacy_mutation = {'operation_id': 'synthetic-v1-save', 'kind': 'save', 'dataset': legacy_dataset, 'memory': legacy_memory,
                           'expected_revision': 0, 'text': 'synthetic V1 preservedBodyToken'}
        call('apply', mutation=legacy_mutation, user_confirmed=True)
        wait(lambda: call('get', memory=legacy_memory)['index_state']=='indexed')
        call('quiesce')
        daemon.wait(timeout=15)
        with sqlite3.connect(data/'memlane.sqlite') as db:
            assert db.execute('PRAGMA user_version').fetchone()[0]==1
            snapshots = {table: db.execute('SELECT * FROM '+table).fetchall() for table in ['memories','versions','operations','jobs','index_bindings','activities']}
        daemon = start()
        wait(lambda: call('health')['state']=='ready')

        def migration():
            assert call('get',memory=legacy_memory)['text']==legacy_mutation['text']
            assert call('apply',mutation=legacy_mutation,user_confirmed=True)['revision']==1
            call('quiesce');daemon.wait(timeout=15)
            with sqlite3.connect(data/'memlane.sqlite') as db:
                assert db.execute('PRAGMA user_version').fetchone()[0]==2
                assert db.execute('PRAGMA integrity_check').fetchone()[0]=='ok'
                for table, rows in snapshots.items():
                    assert db.execute('SELECT * FROM '+table).fetchall()==rows, table
            return {'schema': 2, 'preserved_tables': list(snapshots), 'old_operation_replay': True}
        check('v1-migration', migration)
        daemon = start();wait(lambda: call('health')['state']=='ready')
        A, B = repo('project-a'), repo('project-b')
        worktree = root/'worktree-a'
        git('-C', str(A), 'worktree', 'add', '-b', 'synthetic-worktree', str(worktree))
        nested = worktree/'nested';nested.mkdir()
        link = root/'workspace-link';link.symlink_to(nested)
        clone = root/'clone-a';git('clone', str(A), str(clone))
        scopeA = call('register_project',project={'name':'合成 A','path':str(A)},user_confirmed=True)
        scopeB = call('register_project',project={'name':'合成 B','path':str(B)},user_confirmed=True)
        global_id = next(s['id'] for s in call('scopes') if s['kind']=='experience')
        def mappings():
            assert call('register_project',project={'name':'worktree alias','path':str(link)},user_confirmed=True)['id']==scopeA['id']
            independent=call('register_project',project={'name':'独立 clone','path':str(clone)},user_confirmed=True)
            assert independent['id']!=scopeA['id']
            return {'worktree_nested_symlink_same_project':True,'independent_clone_distinct':True}
        check('repository-mapping', mappings)
        codex = connection('codex',scopeA['id'],[scopeA['id'],global_id])
        piA = connection('pi','',[scopeA['id'],scopeB['id'],global_id])
        piB = connection('pi','',[scopeB['id'],global_id])
        c = client(codex,workspace=link)
        pA = client(piA,workspace=A)
        pB = client(piB,workspace=B)
        unbound = client(piA,cwd=A)
        tools=c.call('tools/list',{})['result']['tools']
        assert {t['name'] for t in tools}=={'memory_scopes','memory_save','memory_search','memory_get','memory_status'}
        c.tool('memory_search',{'query':'x','scope':scopeB['id']},'scope_denied')
        unbound.tool('memory_search',{'query':'x'},'scope_required')
        assert unbound.tool('memory_scopes',{})['requires_scope']
        mismatch=client(codex,workspace=B)
        mismatch.tool('memory_search',{'query':'x'},'workspace_mismatch')
        nonrepo=client(piA,workspace=root)
        nonrepo.tool('memory_search',{'query':'x'},'workspace_unavailable')
        assert nonrepo.tool('memory_scopes',{})['requires_scope']
        assert nonrepo.tool('memory_search',{'query':'x','scope':global_id})['scopes']==[global_id]
        c.tool('memory_save',{'operation_id':'invalid','text':'x','memory_id':str(uuid.uuid4())},'invalid_request')
        payload={'operation_id':'synthetic-stable-save','title':'合成共享经验','text':'P2 original sharedCurrentToken alpha',
                 'source':{'fragment':'Agent submitted synthetic fragment','references':['synthetic://project-a/experience']}}
        receipt=c.tool('memory_save',payload)
        memory_id=receipt['record']['memory']
        assert receipt['persisted'] and receipt['saved_revision']==1
        wait(lambda: c.tool('memory_status',{'memory_id':memory_id})['index_state']=='indexed')
        def writes():
            again=c.tool('memory_save',payload)
            assert again['record']['memory']==memory_id and again['saved_revision']==1
            c.tool('memory_save',{**payload,'text':'different'},'conflict')
            c.tool('memory_save',{**payload,'scope':global_id},'conflict')
            assert call('list',list_query={'datasets':[scopeA['id']]})['total']==1
            assert c.tool('memory_get',{'memory_id':memory_id})['source_detail']==payload['source']
            return {'persisted_receipt':True,'stable_id':True,'changed_content_and_scope_conflict':True,'only_one_memory':True,'source_preserved':True}
        check('durable-save-and-idempotency',writes)
        def isolation():
            assert any(h['version']['memory']==memory_id for h in pA.tool('memory_search',{'query':'sharedCurrentToken'})['hits'])
            assert all(h['version']['dataset']!=scopeA['id'] for h in pB.tool('memory_search',{'query':'sharedCurrentToken'})['hits'])
            pB.tool('memory_get',{'memory_id':memory_id},'scope_denied')
            pB.tool('memory_search',{'query':'sharedCurrentToken','scope':scopeA['id']},'scope_denied')
            global_receipt=c.tool('memory_save',{'operation_id':'synthetic-global','scope':global_id,'text':'P2 global sharedGlobalToken'})
            gid=global_receipt['record']['memory']
            wait(lambda: pB.tool('memory_get',{'memory_id':gid})['index_state']=='indexed')
            assert any(h['version']['memory']==gid for h in pB.tool('memory_search',{'query':'sharedGlobalToken'})['hits'])
            # piA is authorized for A+B, but B workspace still defaults B+global.
            granted=client(piA,workspace=B)
            assert all(h['version']['dataset']!=scopeA['id'] for h in granted.tool('memory_search',{'query':'sharedCurrentToken'})['hits'])
            assert any(h['version']['memory']==memory_id for h in granted.tool('memory_search',{'query':'sharedCurrentToken','scope':scopeA['id']})['hits'])
            return {'A_B_isolation':True,'default_allowed_globals':True,'extra_project_explicit_only':True,'no_process_cwd_inference':True,'workspace_mismatch_rejected':True}
        check('server-scope-authorization',isolation)
        def versions():
            changed=call('apply',user_confirmed=True,mutation={'operation_id':'synthetic-app-edit','kind':'edit','dataset':scopeA['id'],'memory':memory_id,'expected_revision':1,'text':'P2 edited sharedEditedToken beta'})
            assert changed['revision']==2
            replay=c.tool('memory_save',payload)
            assert replay['saved_revision']==1 and replay['record']['revision']==2 and replay['record']['text']==changed['text']
            wait(lambda:pA.tool('memory_get',{'memory_id':memory_id})['index_state']=='indexed')
            hits=pA.tool('memory_search',{'query':'sharedEditedToken'})['hits']
            assert any(h['version']['memory']==memory_id and h['version']['revision']==2 for h in hits)
            assert all(h['version']['revision']==2 for h in pA.tool('memory_search',{'query':'sharedCurrentToken'})['hits'] if h['version']['memory']==memory_id)
            restored=call('apply',user_confirmed=True,mutation={'operation_id':'synthetic-app-restore','kind':'restore','dataset':scopeA['id'],'memory':memory_id,'expected_revision':2,'restore_revision':1})
            assert restored['revision']==3
            wait(lambda:pA.tool('memory_get',{'memory_id':memory_id})['index_state']=='indexed')
            assert all(h['version']['revision']==3 for h in pA.tool('memory_search',{'query':'sharedEditedToken'})['hits'] if h['version']['memory']==memory_id)
            return {'edit_revision':2,'restore_revision':3,'retry_returns_current_body':True,'search_excludes_old_versions':True,'history':len(call('history',memory=memory_id)['items'])}
        check('current-versions-and-restore',versions)
        def stopped():
            call('stop');daemon.wait(timeout=15)
            out=c.tool('memory_save',{'operation_id':'synthetic-stopped','text':'must not claim saved'},'service_unavailable')
            assert out['error']['retryable']
            pA.tool('memory_search',{'query':'x'},'service_unavailable')
            cli=subprocess.run(command(codex,'call')+['--tool','memory_save'],input=json.dumps(payload),text=True,capture_output=True)
            assert cli.returncode!=0 and json.loads(cli.stdout)['error']['code']=='service_unavailable'
            assert not any('persisted' in x for x in [json.loads(cli.stdout).get('result',{})])
            return {'MCP_isError':True,'CLI_nonzero':True,'no_false_save_or_empty_search':True}
        check('service-stopped-errors',stopped)
        # Explicit resume action on synthetic service; no automatic bridge resume.
        state=json.loads((data/'service-state.json').read_text());assert state['stopped']
        state['stopped']=False;state['restarts']=[];(data/'service-state.json').write_text(json.dumps(state))
        daemon=start();wait(lambda:call('health')['state']=='ready')
        def restart():
            assert pA.tool('memory_get',{'memory_id':memory_id})['revision']==3
            assert len(call('connections'))==3
            assert c.tool('memory_save',payload)['record']['revision']==3
            updated=call('configure_connection',user_confirmed=True,connection={'id':codex['id'],'agent':'codex','name':'changed scope','project':scopeA['id'],'allowed_scopes':[scopeA['id']]})
            assert updated['generation']==2
            c.tool('memory_get',{'memory_id':memory_id},'reload_required')
            fresh=client(updated,workspace=A);assert fresh.tool('memory_get',{'memory_id':memory_id})['revision']==3
            call('disconnect',connection_id=codex['id'],user_confirmed=True)
            fresh.tool('memory_get',{'memory_id':memory_id},'disconnected')
            return {'connection_and_memory_persist':True,'generation_invalidates_old_session':True,'disconnect_revokes':True}
        check('restart-reload-disconnect',restart)
        def context_boundaries():
            unknown=repo('unregistered')
            unknown_client=client(piA,workspace=unknown)
            unknown_client.tool('memory_search',{'query':'sharedCurrentToken'},'scope_required')
            assert unknown_client.tool('memory_scopes',{})['requires_scope']
            assert unknown_client.tool('memory_search',{'query':'sharedCurrentToken','scope':scopeA['id']})['scopes']==[scopeA['id']]
            no_global=connection('pi','',[scopeB['id']])
            onlyB=client(no_global,workspace=B)
            assert onlyB.tool('memory_search',{'query':'sharedGlobalToken'})['scopes']==[scopeB['id']]
            onlyB.tool('memory_search',{'query':'sharedGlobalToken','scope':global_id},'scope_denied')
            # A raw save is distinct from knowledge and never increments activity.
            before=call('activities',timezone='UTC')['total']
            raw=onlyB.tool('memory_save',{'operation_id':'synthetic-raw','text':'synthetic raw received fragment','origin':'raw'})
            wait(lambda:onlyB.tool('memory_get',{'memory_id':raw['record']['memory']})['index_state']=='indexed')
            assert call('activities',timezone='UTC')['total']==before
            # Explicitly bound A without workspace is valid; cwd B is ignored.
            bound=connection('codex',scopeA['id'],[scopeA['id']])
            fixed=client(bound,cwd=B)
            assert fixed.tool('memory_scopes',{})['current_project']==scopeA['id']
            assert fixed.tool('memory_get',{'memory_id':memory_id})['dataset']==scopeA['id']
            return {'unregistered_requires_choice':True,'explicit_authorized_scope_works':True,'unapproved_global_denied':True,'raw_no_heatmap_count':True,'explicit_binding_precedes_process_cwd':True}
        check('workspace-and-global-boundaries',context_boundaries)
        # Reproduce legacy P1 duplicate paths only while the single owner is
        # stopped. An explicit binding resolves ambiguity without merging rows.
        call('quiesce');daemon.wait(timeout=15)
        duplicate=str(uuid.uuid4())
        with sqlite3.connect(data/'memlane.sqlite') as db:
            db.execute('INSERT INTO scopes(id,kind,name,path) VALUES(?,?,?,?)',(duplicate,'project','legacy duplicate',str(worktree)))
        daemon=start();wait(lambda:call('health')['state']=='ready')
        def ambiguity():
            pA.tool('memory_search',{'query':'x'},'scope_ambiguous')
            assert pA.tool('memory_scopes',{})['requires_scope']
            chosen=connection('codex',scopeA['id'],[scopeA['id']])
            bound=client(chosen,workspace=link)
            assert bound.tool('memory_get',{'memory_id':memory_id})['dataset']==scopeA['id']
            return {'legacy_ambiguity_requires_choice':True,'explicit_binding_checked_against_worktree':True}
        check('legacy-project-ambiguity',ambiguity)
        # A service can persist new originals even if the engine cannot start.
        # Search must return an error and index status must not claim readiness.
        fault_connection=connection('pi',scopeB['id'],[scopeB['id']])
        fault_client=client(fault_connection,workspace=B)
        call('quiesce');daemon.wait(timeout=15)
        missing={**config,'worker':{**config['worker'],'Executable':str(root/'missing-worker')}}
        configpath.write_text(json.dumps(missing))
        daemon=start();wait(lambda:call('health')['state']=='failed')
        fault_payload={'operation_id':'synthetic-engine-failure','text':'synthetic engineFailureDurableToken'}
        accepted=fault_client.tool('memory_save',fault_payload)
        fault_id=accepted['record']['memory']
        assert accepted['persisted'] and accepted['record']['index_state']=='pending'
        fault_client.tool('memory_search',{'query':'engineFailureDurableToken'},'index_unavailable')
        call('quiesce');daemon.wait(timeout=15)
        configpath.write_text(json.dumps(config))
        state=json.loads((data/'service-state.json').read_text());state['restarts']=[];(data/'service-state.json').write_text(json.dumps(state))
        daemon=start();wait(lambda:call('health')['state']=='ready')
        def engine_recovery():
            wait(lambda:fault_client.tool('memory_get',{'memory_id':fault_id})['index_state']=='indexed')
            assert fault_client.tool('memory_save',fault_payload)['record']['revision']==1
            assert any(h['version']['memory']==fault_id for h in fault_client.tool('memory_search',{'query':'engineFailureDurableToken'})['hits'])
            return {'persisted_while_engine_missing':True,'index_not_ready':True,'search_explicit_error':True,'durable_queue_replayed_once':True}
        check('engine-failure-durable-recovery',engine_recovery)
        report['status']='passed'
    except Exception as error:
        report['error']=repr(error)
        raise
    finally:
        for c in clients:
            c.close()
        if daemon and daemon.poll() is None:
            try:
                call('stop');daemon.wait(timeout=15)
            except Exception:
                daemon.terminate();daemon.wait(timeout=15)
        logs.close();bridge_logs.close()
        REPORT.write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
    print(json.dumps({'status':report['status'],'checks':len(report['checks']),'report':str(REPORT),'real_agent_verified':False}),flush=True)


if __name__=='__main__':main()
