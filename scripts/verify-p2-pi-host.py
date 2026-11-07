#!/usr/bin/env python3
"""真实 pi 加载薄 Extension；运行失败保留准确阻塞，不计为跨 Agent 闭环。"""
import json,subprocess
from pathlib import Path
from importlib.machinery import SourceFileLoader
ROOT=Path(__file__).resolve().parents[1]
d=SourceFileLoader('p2common',str(ROOT/'scripts/p2-common.py')).load_module()
def adapter(action,**fields):
    p=subprocess.run([str(d.OUT/'agent-check')],input=json.dumps({'action':action,**fields}),text=True,capture_output=True)
    return json.loads(p.stdout)
projects=json.loads((d.fixture/'projects.json').read_text())
found=next(a for a in adapter('discover',home=str(Path.home()))['result'] if a['agent']=='pi')
c=d.call('configure_connection',user_confirmed=True,connection={'agent':'pi','name':'P2 pi 实际宿主检查','project':'','allowed_scopes':[projects['a']['id']]})
opts={'ClientRoot':d.cfg['root'],'ServiceRoot':str(d.data),'Bridge':str(d.app/'Contents/Resources/bin/memlane'),'AgentPath':found['path'],'Workspace':str(d.fixture/'repo-a'),'PiDirectory':str(d.fixture/'pi-agent')}
assert adapter('install',connection=c,options=opts)['ok']
r=adapter('run',connection=c,options=opts,run={'Prompt':'只调用 Memlane 的 memory_scopes 工具，展示允许范围；不要读取文件或运行命令。','PiAuthDirectory':str(Path.home()/'.pi/agent')})
report={'status':'tool_roundtrip_passed' if r['ok'] else 'blocked','actual_pi_process':True,'synthetic_only':True,'version':found['version'],'result':r,'cross_agent_closed_loop':False}
(d.OUT/'pi-real-host.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
print(json.dumps(report,ensure_ascii=False),flush=True)
