#!/usr/bin/env python3
"""U 真实App/pi/Rust；隔离合成根，协议错误检查不冒充模型调用。"""
import hashlib,json,os,socket,subprocess,time,uuid
from pathlib import Path
from importlib.machinery import SourceFileLoader
ROOT=Path(__file__).resolve().parents[1];OUT=ROOT/'build/p3-usage'
f=Path((OUT/'fixture-path.txt').read_text().strip());meta=json.loads((f/'source-info.json').read_text());app=Path(meta['app'])
G=SourceFileLoader('usageguard',str(ROOT/'scripts/p3-c2-common.py')).load_module();cfg=G.validate_fixture(f,app,phase='usage')
U=SourceFileLoader('usageui',str(ROOT/'scripts/p1-ui.py')).load_module();data=f/'client/data'
ipc=f'/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock'
def response(op,**kw):
 with socket.socket(socket.AF_UNIX) as s:
  s.settimeout(8);s.connect(ipc);s.sendall((json.dumps({'protocol':1,'id':str(uuid.uuid4()),'op':op,**kw})+'\n').encode());return json.loads(s.makefile('rb',buffering=0).readline())
def call(op,**kw):
 r=response(op,**kw);assert r['ok'],r;return r.get('result')
def visible(key):return any(n.get('key')==key for n in ui.nodes())
def adapter(action,**kw):
 p=subprocess.run([str(OUT/'p2-agent-check')],input=json.dumps({'action':action,**kw}),text=True,capture_output=True,timeout=210)
 r=json.loads(p.stdout);assert r['ok'],r;return r['result']
def choose(key,label):
 before={n['id'] for n in ui.nodes()};ui.press_key(key)
 def option():
  nodes=ui.nodes();popup={n['id'] for n in nodes if n.get('role')=='popup' and n['id'] not in before};return next((n for n in nodes if n.get('label')==label and n.get('parent') in popup),None)
 n=U.wait(option);b=n['bounds'];w=ui.req('windows')['windows'][0]['id'];ui.req('input',window=w,events=[{'type':'click','x':b['x']+b['width']/2,'y':b['y']+b['height']/2}]);ui.req('idle',timeout=1500)
def first_memory():
 return next((n['key'][7:] for n in sorted(ui.nodes(),key=lambda n:n.get('bounds',{}).get('y',99999)) if n.get('key','').startswith('memory-') and n.get('key')!='memory-sort' and len(n['key'])==43),None)
def select(v):
 ui.press('U 合成源项目');U.wait(lambda:visible('memory-'+v['memory']));ui.press_key('memory-'+v['memory']);U.wait(lambda:'使用记录' in ui.labels() and v['text'] in ui.labels())
ui=None;installed=None;options=None;conn=None
report={'status':'failed','actual_app':True,'real_agent':'pi','real_rust':True,'model_fixture':False,'synthetic_only':True,'fixture':str(f.resolve()),'cases':[]}
try:
 ui=U.UI.__new__(U.UI);ui.__init__(app,f,ROOT/'build/p3-usage-screenshots',extra_env={'PI_CODING_AGENT_DIR':str(f/'pi-agent')})
 U.wait(lambda:call('health')['state']=='ready',timeout=95);assert call('list',list_query={'limit':10})['total']==0,'new fixture required'
 for name,repo in [('U 合成源项目','repo-a'),('U 合成其他项目','repo-b')]:
  ui.press('项目');ui.set('project-name',name);ui.set('project-path',str(f/repo));ui.press_key('register-project');U.wait(lambda:any(s['name']==name for s in call('scopes')))
 scopes=call('scopes');project=next(s['id'] for s in scopes if s['name']=='U 合成源项目');other=next(s['id'] for s in scopes if s['name']=='U 合成其他项目')
 sources=[];ui.press('U 合成源项目')
 for title,body in [('合成 pnpm 约定','本项目使用 pnpm 管理依赖，提交 pnpm-lock.yaml。'),('合成代码风格','本项目 TypeScript 代码使用显式类型，保持函数边界清晰。')]:
  ui.press('新增记忆');ui.set('memory-title',title);ui.set('memory-draft',body);ui.press('保存')
  v=U.wait(lambda:next((v for v in call('list',list_query={'datasets':[project],'limit':10})['items'] if v['title']==title),None));U.wait(lambda:call('get',memory=v['memory'])['index_state']=='indexed',timeout=95);sources.append(v)
 v=sources[0];before=call('usage',memory=v['memory'])['app']['browses'];select(v);U.wait(lambda:call('usage',memory=v['memory'])['app']['browses']>before)
 browse=call('usage',memory=v['memory'])['app']['browses'];time.sleep(4.5);assert call('usage',memory=v['memory'])['app']['browses']==browse,'poll must not count browsing'
 ui.system_capture('usage-before-agent-system.png')
 # Actual project editor renders no knowledge body, even when its left row is selected.
 ui.press('项目');U.wait(lambda:visible('project-name'));U.wait(lambda:visible('memory-'+v['memory']));before_editor=call('usage',memory=v['memory'])['app']['browses'];ui.press_key('memory-'+v['memory']);time.sleep(3)
 assert visible('project-name') and '使用记录' not in ui.labels() and call('usage',memory=v['memory'])['app']['browses']==before_editor
 select(v);report['cases'].append({'case':'app_browse_not_poll','memory':v['memory'],'count':browse})
 found=next(x for x in adapter('discover',home=str(Path.home())) if x['agent']=='pi');assert found.get('path') and not found.get('error')
 conn=call('configure_connection',user_confirmed=True,connection={'agent':'pi','name':'U 合成日常消费者','project':project,'allowed_scopes':[project]})
 options={'ClientRoot':cfg['root'],'ServiceRoot':str(data),'Bridge':str(app/'Contents/Resources/bin/memlane'),'AgentPath':found['path'],'Workspace':str(f/'repo-a'),'PiDirectory':str(f/'pi-agent')}
 settings=f/'pi-agent/settings.json';settings.write_text(json.dumps({'synthetic_user_option':'preserve'}));installed=adapter('install',connection=conn,options=options)
 prompt=('这是Memlane使用记录隔离验收，只调用Memlane工具，不读取文件或执行命令，不保存或修改知识。先memory_get读取 memory_id='+json.dumps(v['memory'])+' scope='+json.dumps(project)+'；再memory_search query='+json.dumps(v['text'],ensure_ascii=False)+' scope='+json.dumps(project)+'。确认返回当前v1正文与来源后，memory_adopt明确报告采用该版本：operation_id="usage-adopt-stable", memory_id='+json.dumps(v['memory'])+',revision=1,scope='+json.dumps(project)+',exposure_receipt取第一次memory_get结果usage_receipt.id。再用完全相同参数调用memory_adopt一次核对幂等回执，最后说明成功读取/返回/报告采用的区别。')
 execution=adapter('run',connection=conn,options=options,run={'Prompt':prompt,'Workspace':str(f/'repo-a'),'PiAuthDirectory':str(Path.home()/'.pi/agent'),'UsageChannel':'daily'})
 (OUT/'pi-real-usage.json').write_text(json.dumps(execution,ensure_ascii=False,indent=2)+'\n')
 calls=execution['calls'];reads=[c for c in calls if c['tool']=='memory_get' and c['response']['ok'] and c['response']['result']['memory']==v['memory']]
 searches=[c for c in calls if c['tool']=='memory_search' and c['response']['ok']];adopts=[c for c in calls if c['tool']=='memory_adopt' and c['response']['ok']]
 assert reads and searches and len(adopts)>=2 and all(c['response']['result']==adopts[0]['response']['result'] for c in adopts)
 assert any(h['version']['memory']==v['memory'] and h['version']['revision']==1 for c in searches for h in c['response']['result']['hits'])
 usage=call('usage',memory=v['memory']);assert usage['daily_agent']['reads']==len(reads) and usage['daily_agent']['returns']>=1 and usage['daily_agent']['reported_adoptions']==1
 assert not any(c['tool']=='memory_save' for c in calls) and all(call('get',memory=x['memory'])['revision']==1 for x in sources)
 report['cases'].append({'case':'real_pi_read_search_report_adopt_replay','version':found['version'],'memory':v['memory'],'reads':len(reads),'adopt_tool_calls':len(adopts),'adopt_events':1,'rust_current':True})
 # Explicit service/channel and denied scope checks are no-model protocol tests.
 verify=response('agent',agent_request={'protocol_version':1,'connection_id':conn['id'],'generation':conn['generation'],'workspace':str(f/'repo-a'),'tool':'memory_get','usage_channel':'verification','arguments':{'memory_id':v['memory'],'scope':project}});assert verify['ok']
 denied=response('agent',agent_request={'protocol_version':1,'connection_id':conn['id'],'generation':conn['generation'],'workspace':str(f/'repo-a'),'tool':'memory_get','arguments':{'memory_id':v['memory'],'scope':other}});assert not denied['ok'] and denied['code']=='scope_denied'
 usage=call('usage',memory=v['memory']);assert usage['connection_verification']['reads']==1 and usage['daily_agent']['reads']==len(reads)
 global_id=next(s['id'] for s in scopes if s['kind']=='experience')
 shared=call('apply',user_confirmed=True,mutation={'kind':'save','operation_id':str(uuid.uuid4()),'memory':str(uuid.uuid4()),'dataset':global_id,'title':'U 合成全局回执材料','text':'全局建议使用前先核对当前项目明确约定。','source':'合成协议工作区夹具'})
 flexible=call('configure_connection',user_confirmed=True,connection={'agent':'pi','name':'U 工作区协议夹具','allowed_scopes':[project,other,global_id]})
 def workspace_tool(repo,tool,args):
  return response('agent',agent_request={'protocol_version':1,'connection_id':flexible['id'],'generation':flexible['generation'],'workspace':str(f/repo),'tool':tool,'arguments':args})
 read_global=workspace_tool('repo-a','memory_get',{'scope':global_id,'memory_id':shared['memory']});assert read_global['ok']
 cross_workspace=workspace_tool('repo-b','memory_adopt',{'operation_id':'cross-workspace-rejected','scope':global_id,'memory_id':shared['memory'],'revision':1,'exposure_receipt':read_global['result']['usage_receipt']['id']})
 assert not cross_workspace['ok'] and cross_workspace['code']=='exposure_required',cross_workspace
 report['cases'].append({'case':'no_model_protocol_workspace_fence','global_memory':shared['memory'],'cross_workspace_code':cross_workspace['code']})

 select(v);U.wait(lambda:any('报告采用1' in t for t in ui.labels()));ui.system_capture('usage-real-agent-system.png')
 choose('memory-sort','常用');U.wait(lambda:first_memory()==v['memory']);ui.system_capture('usage-frequent-system.png')
 select(sources[1]);ui.press_key('memory-important');U.wait(lambda:call('usage',memory=sources[1]['memory'])['important']);choose('memory-sort','重要优先');U.wait(lambda:first_memory()==sources[1]['memory']);ui.system_capture('usage-important-system.png')
 report['cases'].append({'case':'app_frequent_important','frequent_first':v['memory'],'important_first':sources[1]['memory'],'marked_by_user':True,'channels_separate':True})
 adapter('uninstall',connection=conn,options=options);installed=None;assert json.loads(settings.read_text())['synthetic_user_option']=='preserve'
 coverage=usage['started_ms'];ui.quit(stop=True);ui=None
 ui=U.UI.__new__(U.UI);ui.__init__(app,f,ROOT/'build/p3-usage-screenshots',extra_env={'PI_CODING_AGENT_DIR':str(f/'pi-agent')});ui.press('设置');U.wait(lambda:'启动服务' in ui.labels());ui.press('启动服务');U.wait(lambda:call('health')['state']=='ready',timeout=95)
 after=call('usage',memory=v['memory']);assert after['started_ms']==coverage and after['daily_agent']==usage['daily_agent'] and call('usage',memory=sources[1]['memory'])['important']
 select(v);U.wait(lambda:'使用记录' in ui.labels() and v['text'] in ui.labels());ui.system_capture('usage-restart-system.png');ui.quit(stop=True);ui=None
 report.update(status='passed',service_stopped=True,coverage_preserved=True,config_preserved=True,scope_denied=True)
except Exception as e:report['error']=repr(e);raise
finally:
 cleanup=[]
 if installed:
  try:adapter('uninstall',connection=conn,options=options)
  except Exception as e:cleanup.append({'step':'adapter','error':repr(e)})
 if ui:
  try:ui.quit(stop=True)
  except Exception as e:cleanup.append({'step':'ui','error':repr(e)})
 # U has no enabled plans. Persist stop even if the inspector quit failed.
 try:
  G.validate_fixture(f,app,phase='usage');path=data/'service-state.json';st=json.loads(path.read_text()) if path.exists() else {};st['stopped']=True;st['stop_generation']=max(1,st.get('stop_generation',0));st['last_stopped_ms']=int(time.time()*1000);temp=path.with_name('.usage-stop-'+uuid.uuid4().hex);temp.write_text(json.dumps(st));os.replace(temp,path)
  if Path(ipc).exists():
   try:response('stop')
   except OSError:pass
 except Exception as e:cleanup.append({'step':'persistent_stop','error':repr(e)})
 report['cleanup_errors']=cleanup
 if cleanup:report['status']='failed'
 (OUT/'real-app.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
print(json.dumps(report,ensure_ascii=False))
if report['status']!='passed':raise SystemExit(1)
