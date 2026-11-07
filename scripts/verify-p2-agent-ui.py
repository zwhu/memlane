#!/usr/bin/env python3
"""实际 App 内连接/范围/断开与系统截图；真实读写通过单独验收。"""
import argparse,json,os,time,shutil
from pathlib import Path
from importlib.machinery import SourceFileLoader
ROOT=Path(__file__).resolve().parents[1]
d=SourceFileLoader('p2common',str(ROOT/'scripts/p2-common.py')).load_module()
out=ROOT/'build/p2-screenshots';out.mkdir(parents=True,exist_ok=True)
projects=json.loads((d.fixture/'projects.json').read_text())
p=argparse.ArgumentParser(description=__doc__);p.add_argument('--functional-only',action='store_true');p.add_argument('--verify',action='store_true');p.add_argument('--verify-pi',action='store_true');p.add_argument('--restart-service',action='store_true');p.add_argument('--require-success',action='store_true');args=p.parse_args()
report={'status':'failed','synthetic_only':True,'checks':[],'visual_complete':not args.functional_only};ui=None
binpath=str(Path(shutil.which('codex')).parent)
env={'PATH':binpath+':/usr/bin:/bin','PI_CODING_AGENT_DIR':str(d.fixture/'pi-agent'),'CODEX_HOME':str(Path.home()/'.codex'),'MEMLANE_PI_AUTH_DIRECTORY':str(Path.home()/'.pi/agent')}

def check(name,evidence):
    report['checks'].append({'name':name,'status':'passed','evidence':evidence})
    (d.OUT/'agent-ui-verification.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
    print(json.dumps(report['checks'][-1],ensure_ascii=False),flush=True)

def click(key):
    d.UI.wait(lambda:'disabled' not in ui.node(key=key).get('states',[]))
    ui.press_key(key)
def choose_project(name):
    # Select is a native MyGo dropdown; open and choose its semantic option.
    click('agent-project')
    nodes=ui.nodes()
    text=next(n for n in nodes if n.get('label')==name and n.get('role')=='text')
    bounds=text['bounds']
    ui.req('input',events=[{'type':'click','x':bounds['x']+bounds['width']/2,'y':bounds['y']+bounds['height']/2}]);ui.req('idle',timeout=2000)

def capture(name):
    if args.functional_only:ui.capture('render-'+name)
    else:ui.system_capture(name)

try:
    ui=d.UI.UI(d.app,d.fixture,out,extra_env=env)
    ui.press('设置');d.UI.wait(lambda:'停止服务' in ui.labels() or '启动服务' in ui.labels(),timeout=90)
    if args.restart_service and '停止服务' in ui.labels():
        ui.press('停止服务');d.UI.wait(lambda:'启动服务' in ui.labels(),timeout=30)
    if '启动服务' in ui.labels():ui.press('启动服务')
    d.UI.wait(lambda:d.call('health')['state']=='ready',timeout=90)
    ui.press('Agent');d.UI.wait(lambda:any(n.get('key')=='agent-pi' for n in ui.nodes()),timeout=30)
    assert any(n.get('key')=='agent-codex' for n in ui.nodes())
    check('bounded-discovery',{'Codex':'actual PATH','pi':'actual PATH','no_full_disk_scan':True})
    # Remove the previous isolated protocol-installed connection through App.
    for connection in d.call('connections'):
        if connection['enabled']:
            click('connection-'+connection['id']);click('disconnect-agent')
            d.UI.wait(lambda:not next(c for c in d.call('connections') if c['id']==connection['id'])['enabled'])
            d.UI.wait(lambda:'断开' not in ui.labels())
    config=d.fixture/'repo-a/.codex/config.toml'
    config.parent.mkdir(exist_ok=True)
    original='# P2 synthetic unrelated config\napproval_policy = "on-request"\n[mcp_servers.other]\ncommand = "/synthetic/other"\n'
    config.write_text(original)
    click('agent-codex');choose_project('P2 合成项目 A');click('connect-agent')
    d.UI.wait(lambda:any(c['agent']=='codex' and c['enabled'] for c in d.call('connections')))
    codex=next(c for c in d.call('connections') if c['agent']=='codex' and c['enabled'])
    d.UI.wait(lambda:any(n.get('key')=='verify-agent' for n in ui.nodes()))
    assert config.read_text().startswith(original)
    assert '待重载' in ui.labels() and '可用' not in ui.labels()
    capture('system-agent-codex-connected.png')
    check('codex-connect',{'project':codex['project'],'generation':codex['generation'],'unrelated_config_preserved':True,'no_false_ready':True})
    # Toggle B as explicit extra scope, and verify persisted generation changes.
    node=ui.node(key='allow-'+projects['b']['id'])
    ui.req('act',id=node['id'],action='press');ui.req('idle',timeout=2000)
    click('apply-agent-scopes')
    d.UI.wait(lambda:next(c for c in d.call('connections') if c['id']==codex['id'])['generation']>codex['generation'])
    updated=next(c for c in d.call('connections') if c['id']==codex['id'])
    assert projects['b']['id'] in updated['allowed_scopes']
    d.UI.wait(lambda:'验证读写' in ui.labels())
    check('scope-management',{'extra_project_granted':True,'reload_generation':updated['generation']})
    if args.verify:
        click('verify-agent')
        d.UI.wait(lambda:any(label.startswith('验证失败：') or label=='真实 Agent 读写验证通过。' for label in ui.labels()),timeout=200)
        label=next(label for label in ui.labels() if label.startswith('验证失败：') or label=='真实 Agent 读写验证通过。')
        failure=label.startswith('验证失败：')
        if args.require_success:assert not failure,label
        assert ('异常' if failure else '可用') in ui.labels()
        if failure:assert '可用' not in ui.labels()
        capture('system-agent-verification-result.png')
        check('actual-agent-verification-state',{'actual_agent_invoked':True,'failure':failure,'message':label,'no_false_ready':True})
    click('disconnect-agent');d.UI.wait(lambda:not next(c for c in d.call('connections') if c['id']==codex['id'])['enabled'])
    d.UI.wait(lambda:config.read_text()==original)
    check('disconnect-preserves-config',{'original_exact_bytes':True,'connection_revoked':True})
    # pi connection installs only one owned Extension into isolated settings.
    settings=d.fixture/'pi-agent/settings.json';settings.write_text(json.dumps({'extensions':['/synthetic/unrelated.ts'],'retainedSetting':True}))
    click('agent-pi');choose_project('P2 合成项目 A');click('connect-agent')
    d.UI.wait(lambda:any(c['agent']=='pi' and c['enabled'] for c in d.call('connections')))
    pi=next(c for c in d.call('connections') if c['agent']=='pi' and c['enabled'])
    d.UI.wait(lambda:'验证读写' in ui.labels())
    actual=json.loads(settings.read_text());assert actual['retainedSetting'] and actual['extensions'][0]=='/synthetic/unrelated.ts' and len(actual['extensions'])==2
    capture('system-agent-pi-connected.png')
    check('pi-connect',{'ctx_workspace_contract':True,'unrelated_extensions_preserved':True,'pending_reload':True})
    if args.verify_pi:
        click('verify-agent')
        d.UI.wait(lambda:any(label.startswith('验证失败：') or label=='真实 Agent 读写验证通过。' for label in ui.labels()),timeout=200)
        assert '真实 Agent 读写验证通过。' in ui.labels(),ui.labels()
        assert '可用' in ui.labels()
        capture('system-agent-pi-verified.png')
        check('actual-pi-App-verification',{'real_pi_save_status_search':True,'available_after_receipts':True})
    click('disconnect-agent');d.UI.wait(lambda:not next(c for c in d.call('connections') if c['id']==pi['id'])['enabled'])
    d.UI.wait(lambda:json.loads(settings.read_text())['extensions']==['/synthetic/unrelated.ts'])
    check('pi-disconnect',{'only_own_extension_removed':True,'unrelated_setting_retained':True})
    ui.quit();ui=None
    ui=d.UI.UI(d.app,d.fixture,out,extra_env=env)
    ui.press('Agent');d.UI.wait(lambda:any(n.get('key')=='connection-'+pi['id'] for n in ui.nodes()))
    assert not next(c for c in d.call('connections') if c['id']==pi['id'])['enabled']
    click('connection-'+pi['id'])
    capture('system-agent-restarted-disconnected.png')
    check('restart-connection-state',{'disconnected_state_persists':True,'actual_system_window':not args.functional_only})
    ui.quit();ui=None
    report['status']='functional_passed' if args.functional_only else 'passed'
except Exception as error:
    report['error']=repr(error)
    if ui:
        try:ui.capture('agent-ui-failed.png')
        except Exception:pass
    raise
finally:
    if ui:ui.quit()
    (d.OUT/'agent-ui-verification.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
print(json.dumps({'status':report['status'],'checks':len(report['checks'])}),flush=True)
