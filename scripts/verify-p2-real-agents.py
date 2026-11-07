#!/usr/bin/env python3
"""真实安装的 Codex/pi 进程经生产适配器调用记忆工具；仅合成数据。"""
import argparse,json,subprocess,time
from pathlib import Path
from importlib.machinery import SourceFileLoader
ROOT=Path(__file__).resolve().parents[1]
d=SourceFileLoader('p2common',str(ROOT/'scripts/p2-common.py')).load_module()
p=argparse.ArgumentParser(description=__doc__)
p.add_argument('--agent',choices=['codex','pi'],required=True)
p.add_argument('--mode',choices=['save','read'],required=True)
p.add_argument('--provider');p.add_argument('--model')
p.add_argument('--auth-dir',type=Path)
a=p.parse_args()

def adapter(action,**fields):
    process=subprocess.run([str(d.OUT/'agent-check')],input=json.dumps({'action':action,**fields}),text=True,capture_output=True)
    response=json.loads(process.stdout)
    return response

projects=json.loads((d.fixture/'projects.json').read_text())
scope=projects['a']['id']
global_id=next(s['id'] for s in d.call('scopes') if s['kind']=='experience')
discovery=adapter('discover',home=str(Path.home()))['result']
found=next(agent for agent in discovery if agent['agent']==a.agent)
assert found['path'] and not found.get('error'),found
connections_path=d.fixture/'agent-connections.json'
connections=json.loads(connections_path.read_text()) if connections_path.exists() else {}
active=next((c for c in d.call('connections') if c['agent']==a.agent and c['enabled'] and scope in c['allowed_scopes'] and (a.agent!='codex' or c['project']==scope)),None)
if active:
    connections[a.agent]=active
elif a.agent not in connections or not next((c for c in d.call('connections') if c['id']==connections[a.agent]['id']),{}).get('enabled'):
    connections[a.agent]=d.call('configure_connection',user_confirmed=True,connection={
      **({'id':connections[a.agent]['id']} if a.agent in connections else {}),
      'agent':a.agent,'name':'P2 真实 '+a.agent,'project':scope if a.agent=='codex' else '',
      'allowed_scopes':[scope,global_id]})
connections_path.write_text(json.dumps(connections,ensure_ascii=False,indent=2))
c=connections[a.agent]
options={'ClientRoot':d.cfg['root'],'ServiceRoot':str(d.data),'Bridge':str(d.app/'Contents/Resources/bin/memlane'),
         'AgentPath':found['path'],'Workspace':str(d.fixture/'repo-a'),'PiDirectory':str(d.fixture/'pi-agent')}
installation=Path(d.cfg['root'])/'agent-adapters'/c['id']/'installation.json'
if not installation.exists():
    installed=adapter('install',connection=c,options=options)
    assert installed['ok'],installed
knowledge=d.fixture/'real-knowledge.json'
text='Memlane P2 synthetic project experience: retain requestId in API errors. crossAgentP2OriginalToken'
if a.mode=='save':
    prompt=('这是 Memlane 合成验收，只调用 Memlane memory_save 工具一次。不要读取文件、运行命令或修改现有记忆。'+
            '保存下列 JSON，operation_id 在重试时保持不变。必须取得 persisted 回执才报告保存；不调用 shell。'+
            json.dumps({'operation_id':'real-codex-p2-experience-1','text':text,'title':'P2 跨 Agent 项目经验',
                        'source':{'fragment':'Synthetic experience submitted by Codex','references':['synthetic://p2/project-a/requestId']}},ensure_ascii=False))
else:
    assert knowledge.exists(),'Codex save receipt missing'
    record=json.loads(knowledge.read_text())
    current=d.call('get',memory=record['memory'])
    prompt=('这是 Memlane 合成验收。只用 Memlane 工具：先调用 memory_search，query='+
            json.dumps(current['text'].split()[-1])+', 再 memory_get 读取 memory_id='+json.dumps(record['memory'])+
            '。输出当前正文、来源、版本。不要读取文件或调用任何命令，不要修改记忆。')
run={'Prompt':prompt,'Provider':a.provider or '', 'Model':a.model or '','Workspace':str(d.fixture/'repo-a')}
if a.auth_dir:
    run['CodexAuthHome' if a.agent=='codex' else 'PiAuthDirectory']=str(a.auth_dir.resolve())
elif a.agent=='codex':run['CodexAuthHome']=str(Path.home()/'.codex')
elif a.agent=='pi':run['PiAuthDirectory']=str(Path.home()/'.pi/agent')
began=time.monotonic()
response=adapter('run',connection=c,options=options,run=run)
report={'status':'failed','synthetic_only':True,'actual_installed_agent':True,'agent':a.agent,'mode':a.mode,
        'version':found['version'],'seconds':round(time.monotonic()-began,3),'execution':response.get('result'),
        'error':response.get('error'),'authentication_owned_by_agent':True,'isolated_config':True}
path=d.OUT/(a.agent+'-real-'+a.mode+'.json')
try:
    assert response['ok'],response
    calls=response['result']['calls']
    if a.mode=='save':
        save=next(call for call in calls if call['tool']=='memory_save' and call['response']['ok'])['response']['result']
        assert save['persisted'] and save['saved_revision']==1 and save['record']['text']==text
        knowledge.write_text(json.dumps(save['record'],ensure_ascii=False,indent=2))
    else:
        get=next(call for call in calls if call['tool']=='memory_get' and call['response']['ok'])['response']['result']
        search=next(call for call in calls if call['tool']=='memory_search' and call['response']['ok'])['response']['result']
        assert get['text']==current['text'] and get['revision']==current['revision']
        assert any(hit['version']['memory']==get['memory'] and hit['version']['revision']==get['revision'] for hit in search['hits'])
        assert get['source']=='codex' and get['source_detail']['references']==['synthetic://p2/project-a/requestId']
    report['status']='passed'
finally:
    path.write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
print(json.dumps({'status':report['status'],'agent':a.agent,'mode':a.mode,'actual_tool_calls':len(report['execution']['calls']),'report':str(path)}),flush=True)
