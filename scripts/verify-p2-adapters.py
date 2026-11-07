#!/usr/bin/env python3
"""验证实际版本发现、增量配置及断开保留；不冒充真实 Agent 工具验收。"""
import json
import os
import subprocess
import tempfile
import uuid
from pathlib import Path

ROOT=Path(__file__).resolve().parents[1]
OUT=ROOT/'build/p2'
root=Path(tempfile.mkdtemp(prefix='memlane-p2-adapters-'))
report={'status':'failed','synthetic_only':True,'checks':[],'fixture':str(root)}

def call(action,ok=True,**fields):
    p=subprocess.run([str(OUT/'agent-check')],input=json.dumps({'action':action,**fields}),text=True,capture_output=True)
    response=json.loads(p.stdout)
    assert response['ok']==ok,response
    assert (p.returncode==0)==ok,p.stderr
    return response.get('result') if ok else response['error']

def check(name,action):
    evidence=action();report['checks'].append({'name':name,'status':'passed','evidence':evidence})
    print(json.dumps(report['checks'][-1],ensure_ascii=False),flush=True)

try:
    discovered=call('discover',home=str(Path.home()))
    assert {d['agent'] for d in discovered}=={'codex','pi'}
    assert all(d['path'] and not d.get('error') for d in discovered),discovered
    check('read-only-discovery',lambda:discovered)
    paths={d['agent']:d['path'] for d in discovered}
    repo=root/'repo';repo.mkdir()
    subprocess.run(['/usr/bin/git','init',str(repo)],check=True,capture_output=True)
    codexfile=repo/'.codex/config.toml';codexfile.parent.mkdir()
    original='# unrelated comment\nmodel = "synthetic-model"\n[mcp_servers.unrelated]\ncommand = "/synthetic/other"\nargs = ["keep"]\n'
    codexfile.write_text(original)
    options={'ClientRoot':str(root/'client'),'ServiceRoot':str(root/'client/data'),'Bridge':str(OUT/'memlane'),'AgentPath':paths['codex'],'Workspace':str(repo)}
    codex={'id':str(uuid.uuid4()),'agent':'codex','name':'synthetic Codex','project':str(uuid.uuid4()),'allowed_scopes':[],'generation':1,'enabled':True}
    def codex_incremental():
        installed=call('install',connection=codex,options=options)
        assert codexfile.read_text().startswith(original)
        assert Path(installed['backup_path']).read_text()==original
        codex['generation']=2
        revised=call('install',connection=codex,options=options)
        assert codexfile.read_text().count('[mcp_servers.memlane]')==1 and '"--generation","2"' in revised['block']
        codexfile.write_text(codexfile.read_text()+'\n# later user comment\n')
        call('uninstall',connection=codex,options=options)
        assert codexfile.read_text()==original+'\n# later user comment\n'
        return {'unrelated_exact_bytes_preserved':True,'backup_matches_original':True,'update_no_duplicates':True,'disconnect_only_owned_block':True}
    check('codex-incremental-config',codex_incremental)
    def codex_conflict():
        codexfile.write_text(original+'\n[mcp_servers.memlane]\ncommand = "user-owned"\n')
        before=codexfile.read_bytes();call('install',ok=False,connection=codex,options=options);assert codexfile.read_bytes()==before
        codexfile.write_text(original);installed=call('install',connection=codex,options=options)
        codexfile.write_text(codexfile.read_text().replace('"--generation","2"','"--generation","99"'))
        before=codexfile.read_bytes();call('uninstall',ok=False,connection=codex,options=options);assert codexfile.read_bytes()==before
        return {'unowned_entry_refused':True,'externally_edited_owned_entry_preserved':True}
    check('codex-config-conflict',codex_conflict)
    pi={'id':str(uuid.uuid4()),'agent':'pi','name':'synthetic pi','project':'','allowed_scopes':[],'generation':1,'enabled':True}
    piOptions={**options,'AgentPath':paths['pi'],'PiDirectory':str(root/'pi')}
    pifile=root/'pi/settings.json';pifile.parent.mkdir()
    settings={'extensions':['/synthetic/other.ts'],'defaultProvider':'synthetic-provider','custom':{'retained':[1,2,3]}}
    pifile.write_text(json.dumps(settings))
    def pi_incremental():
        installed=call('install',connection=pi,options=piOptions)
        extension=Path(installed['extension_path'])
        assert '__MEMLANE_CONFIG__' not in extension.read_text() and 'ctx.cwd' in extension.read_text()
        assert json.loads(Path(installed['backup_path']).read_text())==settings
        actual=json.loads(pifile.read_text());assert actual['custom']==settings['custom'] and actual['defaultProvider']==settings['defaultProvider']
        assert actual['extensions']==settings['extensions']+[str(extension)]
        pi['generation']=2;call('install',connection=pi,options=piOptions)
        assert len(json.loads(pifile.read_text())['extensions'])==2
        actual=json.loads(pifile.read_text());actual['later_setting']=True;pifile.write_text(json.dumps(actual))
        call('uninstall',connection=pi,options=piOptions)
        assert not extension.exists()
        expected={**settings,'later_setting':True};assert json.loads(pifile.read_text())==expected
        return {'unrelated_values_preserved':True,'backup_available':True,'generation_update':True,'ctx_cwd_forwarded':True,'disconnect_only_own_extension':True}
    check('pi-extension-incremental-config',pi_incremental)
    def pi_modified():
        installed=call('install',connection=pi,options=piOptions);extension=Path(installed['extension_path'])
        extension.write_text(extension.read_text()+'\n// user modification\n')
        before=pifile.read_bytes();call('install',ok=False,connection=pi,options=piOptions);assert pifile.read_bytes()==before
        call('uninstall',connection=pi,options=piOptions);assert extension.exists() and '// user modification' in extension.read_text()
        return {'modified_extension_not_overwritten':True,'modified_file_retained_after_unlink':True}
    check('pi-owned-file-conflict',pi_modified)
    def symlink_and_rollback():
        pi['id']=str(uuid.uuid4())
        # Keep a real supported settings symlink instead of replacing it.
        target=root/'pi-settings-target.json';target.write_text(json.dumps(settings))
        pifile.unlink();pifile.symlink_to(target)
        installed=call('install',connection=pi,options=piOptions)
        assert pifile.is_symlink()
        extension=Path(installed['extension_path']);before=extension.read_bytes();before_settings=target.read_bytes()
        pi['generation']+=1
        rootmode=target.parent.stat().st_mode&0o777
        target.parent.chmod(0o555)
        try:
            call('install',ok=False,connection=pi,options=piOptions)
        finally:
            target.parent.chmod(rootmode)
        assert extension.read_bytes()==before and target.read_bytes()==before_settings
        call('install',connection=pi,options=piOptions)
        assert pifile.is_symlink()
        call('uninstall',connection=pi,options=piOptions)
        assert pifile.is_symlink() and json.loads(target.read_text())==settings
        return {'settings_symlink_preserved':True,'failed_update_rolls_back_extension':True,'retry_after_failure_works':True}
    check('configuration-failure-and-symlink',symlink_and_rollback)
    report['status']='passed'
finally:
    (OUT/'adapter-verification.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
print(json.dumps({'status':report['status'],'checks':len(report['checks']),'real_agent_verified':False}),flush=True)
