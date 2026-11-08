#!/usr/bin/env python3
"""A1真实traex新工具验收，复用实际App已安装合成连接；不修改日常配置。"""
import hashlib,json,os,socket,subprocess,uuid
from pathlib import Path
from importlib.machinery import SourceFileLoader
ROOT=Path(__file__).resolve().parents[1];OUT=ROOT/'build/p3-agents';f=Path((OUT/'fixture-path.txt').read_text().strip());meta=json.loads((f/'source-info.json').read_text());app=Path(meta['app'])
G=SourceFileLoader('arealguard',str(ROOT/'scripts/p3-c2-common.py')).load_module();G.validate_fixture(f,app,phase='agents')
pre=json.loads((OUT/'preflight-app.json').read_text());assert pre['fixture']==str(f) and pre['status']=='inspected_pending_tool_assertions' and not pre['cleanup_errors'];proof=json.loads((OUT/'prompt-inspection-check.json').read_text());assert proof['status']=='passed' and proof['fixture']==str(f) and proof['identifier']==meta['identifier'] and proof['connection']==pre['connection']['id'] and proof['generation']==pre['connection']['generation'] and proof['inspection_sha256']==hashlib.sha256((OUT/'traex-prompt-input.json').read_bytes()).hexdigest()
U=SourceFileLoader('arealui',str(ROOT/'scripts/p1-ui.py')).load_module();data=f/'client/data';ipc=f'/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock'
def call(op,**kw):
 with socket.socket(socket.AF_UNIX) as s:
  s.settimeout(8);s.connect(ipc);s.sendall((json.dumps({'protocol':1,'id':str(uuid.uuid4()),'op':op,**kw})+'\n').encode());r=json.loads(s.makefile('rb',buffering=0).readline());assert r['ok'],r;return r.get('result')
ui=None;report={'status':'failed','actual_app_connection':True,'requested_model':'GPT-6.1-Sol','model_fixture':False,'synthetic_only':True,'fixture':str(f)}
try:
 assert json.loads((data/'service-state.json').read_text())['stopped']
 ui=U.UI.__new__(U.UI);ui.__init__(app,f,ROOT/'build/p3-agents-screenshots',extra_env={'PI_CODING_AGENT_DIR':str(f/'pi-agent'),'MEMLANE_TRAE_AUTH_DIRECTORY':str(Path.home()/'.trae/cli')});ui.press('设置');U.wait(lambda:'启动服务' in ui.labels());ui.press('启动服务');U.wait(lambda:call('health')['state']=='ready',timeout=95)
 conn=next(c for c in call('connections') if c['id']==pre['connection']['id']);assert conn['generation']==pre['connection']['generation'] and conn['enabled']
 session=uuid.uuid4().hex[:12];project=conn['project'];body='A1合成经验：依赖变更与pnpm-lock.yaml一起提交，保存错误要保留请求标识。'
 prompt=('这是Memlane隔离真实工具验收，仅使用Memlane MCP工具，不读取文件或执行系统命令、不调用其他Agent。先memory_scopes核对范围；memory_save scope='+json.dumps(project)+' operation_id="a1-traex-real-save" title="A1合成工具约定" text='+json.dumps(body,ensure_ascii=False)+' origin="knowledge" source={"fragment":"合成验证实际提交的片段","references":["synthetic://a1/traex"]}。取得persisted后，memory_status查询到indexed，再memory_get该memory当前v1正文与来源，再memory_search query='+json.dumps(body,ensure_ascii=False)+' scope='+json.dumps(project)+'。使用第一次memory_get的usage_receipt.id为exposure_receipt，memory_adopt scope='+json.dumps(project)+' operation_id="a1-adopt-replay" memory_id=刚保存memory revision=1，再完全相同参数重放memory_adopt一次。返回各实际回执；不要宣称未返回的状态成功。')
 prompt=prompt.replace('a1-traex-real-save','a1-traex-real-save-'+session).replace('a1-adopt-replay','a1-adopt-replay-'+session)
 request={'action':'run','connection':conn,'options':pre['options'],'run':{'Prompt':prompt,'Workspace':str(f/'repo-a'),'TraeAuthHome':str(Path.home()/'.trae/cli'),'Model':'GPT-6.1-Sol','UsageChannel':'daily'}}
 proc=subprocess.run([str(OUT/'p2-agent-check')],input=json.dumps(request),capture_output=True,text=True,timeout=210);execution=json.loads(proc.stdout);(OUT/'traex-real-tools.json').write_text(json.dumps(execution,ensure_ascii=False,indent=2)+'\n');assert execution['ok'],execution
 actual=execution['result'];assert actual['agent']==proof['agent'] and actual['version']==proof['version'] and actual['channel']==proof['channel'] and actual['selected_model']==proof['selected_model'],actual
 report.update(real_agent=actual['agent'],real_version=actual['version'],real_channel=actual['channel'],selected_model=actual['selected_model'],actual_model_reported=actual.get('model'),actual_provider_reported=actual.get('provider'))
 calls=execution['result']['calls']
 tools={c['tool'] for c in calls if c['response']['ok']};assert {'memory_scopes','memory_save','memory_status','memory_get','memory_search','memory_adopt'}<=tools,execution
 saves=[c for c in calls if c['tool']=='memory_save' and c['response']['ok'] and c['arguments'].get('operation_id')=='a1-traex-real-save-'+session];assert saves
 memory=saves[0]['response']['result']['record']['memory'];v=call('get',memory=memory);assert v['revision']==1 and v['text']==body and v['index_state']=='indexed'
 reads=[c for c in calls if c['tool']=='memory_get' and c['response']['ok'] and c['response']['result'].get('memory')==memory and c['arguments'].get('scope')==project];assert reads
 first=reads[0]['response']['result'];assert first['revision']==1 and first['text']==body and first['source_detail']
 expected={'operation_id':'a1-adopt-replay-'+session,'memory_id':memory,'revision':1,'scope':project,'exposure_receipt':first['usage_receipt']['id']}
 adopts=[c for c in calls if c['tool']=='memory_adopt' and c['response']['ok'] and c['arguments']==expected];assert len(adopts)>=2 and adopts[0]['response']['result']==adopts[1]['response']['result']
 assert any(c['tool']=='memory_status' and c['response']['ok'] and c['response']['result'].get('memory')==memory and c['response']['result']['index_state']=='indexed' for c in calls)
 assert any(c['tool']=='memory_search' and c['response']['ok'] and any(h['version']['memory']==memory and h['version']['revision']==1 for h in c['response']['result']['hits']) for c in calls)
 usage=call('usage',memory=memory);assert usage['daily_agent']['reads']>=1 and usage['daily_agent']['returns']>=1 and usage['daily_agent']['reported_adoptions']==1
 report.update(status='passed',memory=memory,tools=sorted(tools),adoption_events=1,scope=project,current_version=1,service_stopped=True)
 ui.quit(stop=True);ui=None
except Exception as e:report.update(status='failed',error=repr(e));raise
finally:
 cleanup=[]
 if ui:
  try:G.close_partial_ui(ui,f,app,'agents')
  except Exception as e:cleanup.append({'step':'ui','error':repr(e)})
 try:
  G.validate_fixture(f,app,phase='agents');p=data/'service-state.json';st=json.loads(p.read_text());st['stopped']=True;tmp=p.with_name('.a-real-stop-'+uuid.uuid4().hex);tmp.write_text(json.dumps(st));os.replace(tmp,p)
  if Path(ipc).exists():
   try:call('stop')
   except OSError:pass
 except Exception as e:cleanup.append({'step':'persistent_stop','error':repr(e)})
 report['cleanup_errors']=cleanup
 if cleanup:report['status']='failed'
 (OUT/'real-tools-report.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
print(json.dumps(report,ensure_ascii=False))
if report['status']!='passed':raise SystemExit(1)
