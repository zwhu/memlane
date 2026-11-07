#!/usr/bin/env python3
"""真实 pi/Codex 进程验证 B 隔离、全局、显式授权与 worktree。"""
import argparse,json,subprocess,time
from pathlib import Path
from importlib.machinery import SourceFileLoader
ROOT=Path(__file__).resolve().parents[1]
d=SourceFileLoader('p2common',str(ROOT/'scripts/p2-common.py')).load_module()
projects=json.loads((d.fixture/'projects.json').read_text())
record=json.loads((d.fixture/'real-knowledge.json').read_text())
scopeA,scopeB=projects['a']['id'],projects['b']['id']
globalScope=next(s['id'] for s in d.call('scopes') if s['kind']=='experience')
p=argparse.ArgumentParser(description=__doc__);p.add_argument('--only-codex-worktree',action='store_true');flags=p.parse_args()
report={'status':'failed','synthetic_only':True,'actual_agent_processes':True,'checks':[]}
if flags.only_codex_worktree:
    previous=json.loads((d.OUT/'real-scope-verification.json').read_text())
    report['checks']=[c for c in previous['checks'] if c['status']=='passed' and c['name']!='codex-worktree-binding']
    assert len(report['checks'])==6,'six verified pi scope checks required'
created=[]
settings=d.fixture/'scope-pi-agent';settings.mkdir(exist_ok=True)
client=d.fixture/'scope-agent-client'

def adapter(action,**fields):
    p=subprocess.run([str(d.OUT/'agent-check')],input=json.dumps({'action':action,**fields}),text=True,capture_output=True)
    return json.loads(p.stdout)
discovery=adapter('discover',home=str(Path.home()))['result']
agents={a['agent']:a for a in discovery}

def connect(agent,allowed,workspace,project=''):
    c=d.call('configure_connection',user_confirmed=True,connection={'agent':agent,'name':'P2 实际范围检查 '+agent,'project':project,'allowed_scopes':allowed})
    options={'ClientRoot':str(client),'ServiceRoot':str(d.data),'Bridge':str(d.app/'Contents/Resources/bin/memlane'),'AgentPath':agents[agent]['path'],'Workspace':str(workspace),'PiDirectory':str(settings)}
    assert adapter('install',connection=c,options=options)['ok']
    created.append((c,options))
    return c,options

def invoke(pair,tool,args,workspace):
    c,options=pair
    prompt='这是隔离合成范围验收。只调用一次 '+tool+'，严格使用下列完整 JSON 参数，不调用其他工具，不读取文件或执行命令。显示工具结果或明确错误：'+json.dumps(args,ensure_ascii=False)
    run={'Prompt':prompt,'Workspace':str(workspace),'CodexAuthHome':str(Path.home()/'.codex'),'PiAuthDirectory':str(Path.home()/'.pi/agent')}
    r=adapter('run',connection=c,options=options,run=run)
    assert r['ok'],r
    calls=[call for call in r['result']['calls'] if call['tool']==tool]
    assert calls,r
    return calls[0]['response'],r['result']

def check(name,action):
    evidence=action();report['checks'].append({'name':name,'status':'passed','evidence':evidence})
    (d.OUT/'real-scope-verification.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
    print(json.dumps(report['checks'][-1],ensure_ascii=False),flush=True)

try:
    both=connect('pi',[scopeA,scopeB,globalScope],d.fixture/'repo-a')
    onlyB=connect('pi',[scopeB,globalScope],d.fixture/'repo-b')
    def global_save():
        receipt,execution=invoke(both,'memory_save',{'operation_id':'real-global-scope-check-1','scope':globalScope,'text':'Memlane synthetic global retry practice sharedGlobalP2Token','source':{'references':['synthetic://p2/global/retry']}},d.fixture/'repo-a')
        assert receipt['ok'] and receipt['result']['persisted']
        memory=receipt['result']['record']['memory']
        d.UI.wait(lambda:d.call('get',memory=memory)['index_state']=='indexed')
        return {'memory':memory,'actual_pi_save':True,'provider':execution.get('provider'),'model':execution.get('model')}
    if not flags.only_codex_worktree:check('explicit-global-save',global_save)
    def B_defaults():
        response,_=invoke(both,'memory_search',{'query':'crossAgentP2EditedToken'},d.fixture/'repo-b')
        assert response['ok'] and scopeA not in response['result']['scopes']
        assert all(h['version']['dataset']!=scopeA for h in response['result']['hits'])
        return {'actual_ctx_project_B':True,'A_excluded_even_when_allowed':True,'scopes':response['result']['scopes']}
    if not flags.only_codex_worktree:check('project-B-default-isolation',B_defaults)
    def extra_grant():
        response,_=invoke(both,'memory_search',{'query':'crossAgentP2EditedToken','scope':scopeA},d.fixture/'repo-b')
        assert response['ok'] and any(h['version']['memory']==record['memory'] and h['version']['revision']==2 for h in response['result']['hits'])
        return {'explicit_authorized_A_in_B':True,'current_revision':2}
    if not flags.only_codex_worktree:check('extra-project-explicit-grant',extra_grant)
    def denied():
        response,_=invoke(onlyB,'memory_get',{'memory_id':record['memory'],'scope':scopeA},d.fixture/'repo-b')
        assert not response['ok'] and response['error']['code']=='scope_denied',response
        return {'actual_pi_denied':True,'code':response['error']['code']}
    if not flags.only_codex_worktree:check('server-denies-unapproved-A',denied)
    def allowed_global():
        response,_=invoke(onlyB,'memory_search',{'query':'sharedGlobalP2Token'},d.fixture/'repo-b')
        assert response['ok'] and any(h['version']['dataset']==globalScope and 'sharedGlobalP2Token' in h['version']['text'] for h in response['result']['hits'])
        return {'B_allowed_global':True,'A_not_queried':scopeA not in response['result']['scopes']}
    if not flags.only_codex_worktree:check('global-default-in-B',allowed_global)
    worktree=d.fixture/'worktree-a'
    if not worktree.exists():subprocess.run(['/usr/bin/git','-C',str(d.fixture/'repo-a'),'worktree','add','-b','p2-synthetic-worktree',str(worktree)],check=True,capture_output=True)
    def pi_worktree():
        response,_=invoke(both,'memory_get',{'memory_id':record['memory']},worktree)
        assert response['ok'] and response['result']['dataset']==scopeA and response['result']['revision']==2
        return {'real_pi_ctx_worktree_maps_A':True,'current_revision':2}
    if not flags.only_codex_worktree:check('pi-worktree-context',pi_worktree)
    codex=connect('codex',[scopeA,globalScope],worktree,project=scopeA)
    def codex_worktree():
        response,execution=invoke(codex,'memory_get',{'memory_id':record['memory']},worktree)
        assert response['ok'] and response['result']['dataset']==scopeA and response['result']['revision']==2
        return {'real_codex_MCP_worktree':True,'explicit_binding':scopeA,'current_revision':2,'actual_tool_events':len(execution['calls'])}
    check('codex-worktree-binding',codex_worktree)
    report['status']='passed'
finally:
    for c,options in created:
        d.call('disconnect',connection_id=c['id'],user_confirmed=True)
        adapter('uninstall',connection=c,options=options)
    (d.OUT/'real-scope-verification.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
print(json.dumps({'status':report['status'],'checks':len(report['checks'])}),flush=True)
