#!/usr/bin/env python3
"""Fast actual App/helper startup probe after replacing a guarded synthetic bundle."""
import argparse
import hashlib
from importlib.machinery import SourceFileLoader
import json
import os
import plistlib
from pathlib import Path
import re
import socket
import subprocess
import time
import uuid

ROOT = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--out', type=Path, default=ROOT/'build/p4-delivery/helper-replacement.json')
parser.add_argument('--replace-version', help='Rebuild this isolated App at the same path before the startup probe')
parser.add_argument('--replace-helper', action='store_true', help='Build the real helper with a distinct test build ID and re-sign the isolated bundle')
parser.add_argument('--helper-signing', choices=['producer','explicit'], default='producer', help='Keep the Go linker signature as the production builder does, or re-sign the helper as an explicit signature fixture')
parser.add_argument('--helper-change-mode', choices=['unused-build-tag','build-id'], default='unused-build-tag', help='Change canonical Go build metadata, or use the earlier explicit build-ID fault fixture')
parser.add_argument('--refresh-launch-services', action='store_true', help='Refresh only this guarded App registration before the startup probe')
parser.add_argument('--unregister-current-app', action='store_true', help='Run the existing native unregister action in the changed App before registering again')
parser.add_argument('--fingerprint-plist', action='store_true', help='Add the changed helper SHA to this isolated LaunchAgent plist and re-seal the bundle')
parser.add_argument('--standard-helper-location', action='store_true', help='Move only the guarded helper to Contents/MacOS and update its bundled LaunchAgent')
args = parser.parse_args()
f = Path((ROOT/'build/p4-code/g2-app-fixture-path.txt').read_text().strip())
meta = json.loads((f/'source-info.json').read_text())
app = Path(meta['app'])
G = SourceFileLoader('helperreplaceguard',str(ROOT/'scripts/p3-c2-common.py')).load_module()
G.validate_fixture(f,app,'code')
U = SourceFileLoader('helperreplaceui',str(ROOT/'scripts/p1-ui.py')).load_module()
data = f/'client/data'
ipc = f'/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock'
label = meta['identifier']+'.service'
def call(op):
    with socket.socket(socket.AF_UNIX) as s:
        s.settimeout(1)
        s.connect(ipc)
        s.sendall((json.dumps({'protocol':1,'id':str(uuid.uuid4()),'op':op})+'\n').encode())
        r=json.loads(s.makefile('rb',buffering=0).readline())
    assert r['ok'],r
    return r.get('result')
def job():
    p=subprocess.run(['/bin/launchctl','print',f'gui/{os.getuid()}/'+label],capture_output=True,text=True)
    return {'present':p.returncode==0,'lines':[line.strip() for line in p.stdout.splitlines() if re.match(r'\s*(state|runs|last exit code|job state|program) =',line)]}
ui=U.UI.__new__(U.UI)
report={'status':'failed','synthetic_only':True,'actual_app':False,'actual_helper':False,'formal_distribution':False,'fixture':str(f),'bundle':str(app)}
started=time.monotonic()
try:
    if args.replace_version:
        assert json.loads((data/'service-state.json').read_text())['stopped'], 'stop guarded service before replacing the fixture App'
        info=plistlib.loads((app/'Contents/Info.plist').read_bytes())
        report['old_version']=info['CFBundleShortVersionString']
        args.out.parent.mkdir(parents=True,exist_ok=True)
        with args.out.with_suffix('.build.log').open('w') as log:
            p=subprocess.run(['python3',str(ROOT/'scripts/build-client.py'),'--out',str(app.parent.parent),'--runtime-config',str(f/'runtime-config.json'),'--identifier',meta['identifier'],'--name',info['CFBundleName'],'--version',args.replace_version,'--automation','--skip-dmg'],stdout=log,stderr=subprocess.STDOUT)
        assert p.returncode==0,'fixture App build failed'
        report['new_version']=plistlib.loads((app/'Contents/Info.plist').read_bytes())['CFBundleShortVersionString']
        G.validate_fixture(f,app,'code')
    if args.replace_helper:
        assert json.loads((data/'service-state.json').read_text())['stopped'], 'stop guarded service before replacing helper'
        service=plistlib.loads((app/'Contents/Library/LaunchAgents'/(meta['identifier']+'.service.plist')).read_bytes())
        helper=app/service['BundleProgram']
        report['old_helper_sha']=hashlib.sha256(helper.read_bytes()).hexdigest()
        marker='memlane_f1_fixture_'+uuid.uuid4().hex
        command=['sh',str(ROOT/'scripts/go.sh'),'build','-trimpath','-ldflags=-s -w']
        if args.helper_change_mode=='build-id':command[-1]+=' -buildid='+marker
        else:command+=['-tags',marker]
        subprocess.run(command+['-o',str(helper),'./cmd/memlaned'],check=True,capture_output=True)
        for target in ((app,) if args.helper_signing=='producer' else (helper,app)):
            subprocess.run(['/usr/bin/codesign','--force','--sign','-','--timestamp=none',str(target)],check=True,capture_output=True)
        subprocess.run(['/usr/bin/codesign','--verify','--deep','--strict',str(app)],check=True,capture_output=True)
        report['new_helper_sha']=hashlib.sha256(helper.read_bytes()).hexdigest()
        report['helper_build_id_fixture']=True
        report['helper_change_mode']=args.helper_change_mode
        report['helper_signing_fixture']=args.helper_signing
        assert report['new_helper_sha']!=report['old_helper_sha']
    if args.standard_helper_location:
        plist=app/'Contents/Library/LaunchAgents'/(meta['identifier']+'.service.plist')
        service=plistlib.loads(plist.read_bytes())
        original=app/service['BundleProgram']
        destination=app/'Contents/MacOS/memlaned'
        if original!=destination:original.rename(destination)
        service['BundleProgram']='Contents/MacOS/memlaned'
        plist.write_bytes(plistlib.dumps(service))
        subprocess.run(['/usr/bin/codesign','--force','--sign','-','--timestamp=none',str(app)],check=True,capture_output=True)
        subprocess.run(['/usr/bin/codesign','--verify','--deep','--strict',str(app)],check=True,capture_output=True)
        report['standard_helper_location']=True
        G.validate_fixture(f,app,'code')
    if args.fingerprint_plist:
        plist=app/'Contents/Library/LaunchAgents'/(meta['identifier']+'.service.plist')
        service=plistlib.loads(plist.read_bytes())
        report['old_plist_sha']=hashlib.sha256(plist.read_bytes()).hexdigest()
        service.setdefault('EnvironmentVariables',{})['MEMLANE_HELPER_BUILD_ID']=hashlib.sha256((app/'Contents/Resources/bin/memlaned').read_bytes()).hexdigest()
        plist.write_bytes(plistlib.dumps(service))
        subprocess.run(['/usr/bin/codesign','--force','--sign','-','--timestamp=none',str(app)],check=True,capture_output=True)
        subprocess.run(['/usr/bin/codesign','--verify','--deep','--strict',str(app)],check=True,capture_output=True)
        report['new_plist_sha']=hashlib.sha256(plist.read_bytes()).hexdigest()
    if args.refresh_launch_services:
        register='/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister'
        p=subprocess.run([register,'-f',str(app.resolve())],capture_output=True,text=True)
        report['launch_services_refresh_exit']=p.returncode
        assert p.returncode==0,'guarded Launch Services refresh failed'
    if args.unregister_current_app:
        args.out.parent.mkdir(parents=True,exist_ok=True)
        native_log=args.out.with_suffix('.native-unregister.log')
        p=subprocess.run(['/usr/bin/open','-n','-W','--stdout',str(native_log.resolve()),'--stderr',str(native_log.resolve()),'--env','HOME='+str(f/'ui-home'),'--env','PATH=/usr/bin:/bin',str(app.resolve()),'--args','--p0-action','unregister'],capture_output=True,text=True,timeout=20)
        assert p.returncode==0,'changed App native unregister process failed'
        receipts=[]
        for line in native_log.read_text().splitlines():
            try:receipts.append(json.loads(line))
            except json.JSONDecodeError:pass
        receipt=next(r for r in receipts if r.get('action')=='unregister')
        assert 'error' not in receipt and receipt['system_state']=='not_registered',receipt
        report['changed_app_unregister_receipt']=receipt
    U.UI.__init__(ui,app,f,ROOT/'build/p4-delivery/helper-replacement-screenshots')
    report['actual_app']=True
    ui.press('设置')
    U.wait(lambda:'启动服务' in ui.labels() or '停止服务' in ui.labels(),timeout=20)
    if '启动服务' in ui.labels():ui.press('启动服务')
    deadline=time.monotonic()+12
    while time.monotonic()<deadline:
        try:
            health=call('health')
            if health['state']=='ready':
                report['health']=health
                report['actual_helper']=True
                report['status']='passed'
                break
        except (OSError,AssertionError):pass
        report['launch_job']=job()
        if any('last exit code = 78' in line for line in report['launch_job']['lines']):
            raise AssertionError('actual LaunchAgent BundleProgram EX_CONFIG (78); helper never served ready IPC')
        time.sleep(.2)
    assert report['status']=='passed','helper did not serve ready IPC'
except Exception as e:report['error']=repr(e)
finally:
    cleanup=[]
    try:call('stop')
    except (FileNotFoundError,ConnectionRefusedError):pass
    except Exception as e:cleanup.append('stop: '+repr(e))
    try:G.close_partial_ui(ui,f,app,'code')
    except Exception as e:cleanup.append('close: '+repr(e))
    report['cleanup_errors']=cleanup
    report['wall_seconds']=time.monotonic()-started
    if cleanup:report['status']='failed'
    args.out.parent.mkdir(parents=True,exist_ok=True)
    args.out.write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
print(json.dumps(report,ensure_ascii=False))
if report['status']!='passed':raise SystemExit(1)
