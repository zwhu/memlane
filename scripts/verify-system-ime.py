#!/usr/bin/env python3
"""Verify a real system Pinyin candidate window and committed synthetic text."""
import json
import subprocess
import sys
import socket
import uuid
import argparse
from importlib.machinery import SourceFileLoader
from pathlib import Path

ROOT=Path(__file__).resolve().parents[1]
native=SourceFileLoader('mac_native',str(ROOT/'scripts/mac-native.py')).load_module()
helper=SourceFileLoader('prep_ui',str(ROOT/'scripts/verify-preparation-ui.py')).load_module()

def main():
    parser=argparse.ArgumentParser(description=__doc__);parser.add_argument('--system-home',action='store_true');args=parser.parse_args()
    subprocess.run([sys.executable,str(ROOT/'scripts/p0-handoff.py'),'native-ime-real-input','真实系统拼音候选窗口、提交、删除和标识符输入'],check=True)
    mac=native.Mac();sources=native.InputSources(mac);original=sources.current();original_front=mac.front_pid()
    report={'status':'failed','original_input_source':sources.identifier(original),'system_gui_home':args.system_home,'input_path':'CGEventCreateKeyboardEvent hardware key codes'}
    ui=None;window=None;pinyin=None;ascii_source=None
    try:
        mac.require_unlocked()
        pinyin=sources.find('com.apple.inputmethod.SCIM.ITABC')
        report['stage']='launch'
        fixture=Path((ROOT/'build/p0-engine/ui-fixture-path.txt').read_text().strip())
        native_path='/tmp/memlane-ime-'+uuid.uuid4().hex[:8]+'.sock'
        ui=helper.UI(ROOT/'build/p0-ui-prepare/darwin-arm64/MemlaneUITest.app',fixture,launch_services=True,native=native_path,system_home=args.system_home)
        report['pid']=ui.pid;report['stage']='foreground'
        mac.activate(ui.pid);helper.wait(lambda:mac.front_pid()==ui.pid,timeout=10);window=mac.window(ui.pid)
        def click(label):
            assert mac.front_pid()==ui.pid
            node=next(n for n in ui.nodes() if n.get('label')==label and n.get('role')=='button');b=node['bounds'];w=mac.rectangle(window);mac.click(w['x']+b['x']+b['width']/2,w['y']+b['y']+b['height']/2)
        def field():return next(n for n in ui.nodes() if n.get('key')=='memory-draft')
        click('新增记忆');node=field();b=node['bounds'];w=mac.rectangle(window);mac.click(w['x']+b['x']+18,w['y']+b['y']+18)
        sources.use(pinyin);assert mac.front_pid()==ui.pid
        report['stage']='candidate'
        def ime_pids():
            extension=subprocess.run(['pgrep','-f','^/System/Library/Input Methods/SCIM.app/Contents/PlugIns/SCIM_Extension.appex/Contents/MacOS/SCIM_Extension'],capture_output=True,text=True)
            hosts=subprocess.run(['pgrep','-x','imklaunchagent|TextInputMenuAgent|TextInputSwitcher'],capture_output=True,text=True)
            return sorted(set([ui.pid]+mac.bundle_pids('com.apple.inputmethod.SCIM')+mac.bundle_pids('com.apple.SCIM-Container')+mac.bundle_pids('com.apple.TextInputMenuAgent')+[int(p) for p in (extension.stdout+' '+hosts.stdout).split()]))
        report['ime_pids']=ime_pids()
        previous_windows={item['number'] for item in mac.visible_windows(report['ime_pids'])}
        mac.type_ascii('xiangmu')
        def candidate():
            pids=ime_pids()
            if not pids:return None
            windows=mac.visible_windows(pids)
            match=[item for item in windows if item['number'] not in previous_windows and item['bounds']['width']>=50 and item['bounds']['height']>=20 and w['x']<=item['bounds']['x']<=w['x']+w['width'] and w['y']<=item['bounds']['y']<=w['y']+w['height']]
            return match[0] if len(match)==1 else None
        panel=helper.wait(candidate,timeout=10);report['actual_candidate_window']=panel
        assert w['y']+b['y']-30<=panel['bounds']['y']<=w['y']+b['y']+b['height']
        mac.capture_id(panel['number'],ROOT/'build/p0-screenshots/system-pinyin-candidate.png')
        report['stage']='commit'
        mac.key(49);helper.wait(lambda:field()['value']=='项目',timeout=10)
        mac.type_ascii('jingyan');mac.key(49);helper.wait(lambda:field()['value']=='项目经验',timeout=10)
        mac.key(51);helper.wait(lambda:field()['value']=='项目经',timeout=10)
        ascii_source=sources.ascii();sources.use(ascii_source);mac.type_ascii(' request_id');expected='项目经 request_id'
        helper.wait(lambda:field()['value']==expected,timeout=10)
        click('设置');click('全部记忆');assert field()['value']==expected
        mac.capture(ui.pid,ROOT/'build/p0-screenshots/system-ime-committed.png')
        report.update(status='passed',candidate_at_text_caret=True,project_experience_committed=True,backspace_deletes_committed_character=True,ascii_identifier_committed=True,navigation_preserves_text=True,actual_text=expected)
    except Exception as exc:
        report['error']=repr(exc)
        if ui:
            try:
                report['last_synthetic_field']=field()
                report['tree_focus']=ui.req('tree').get('focus')
                with socket.socket(socket.AF_UNIX) as connection:
                    connection.settimeout(5);connection.connect(native_path);connection.sendall(b'{"op":"snapshot"}\n');report['native_keyboard_diagnostic']=json.loads(connection.makefile('rb',buffering=0).readline())
                pids=ime_pids()
                report['ime_windows']=mac.visible_windows(pids)
                mac.capture(ui.pid,ROOT/'build/p0-screenshots/system-ime-failed.png')
                if report['stage']=='candidate':
                    mac.key(49)
                    report['diagnostic_space_commit']=field().get('value','')
            except Exception as diagnosis_error:report['diagnosis_error']=repr(diagnosis_error)
    finally:
        try:
            sources.use(original)
            if ui and ui.proc.poll() is None:ui.quit()
        except Exception as exc:report['cleanup_error']=repr(exc);report['status']='failed'
        try:
            mac.activate(original_front)
        except Exception as exc:report['foreground_restore_note']=repr(exc)
        try:
            sources.use(original)
            check=sources.current()
            try:report['input_source_restored']=sources.identifier(check)==sources.identifier(original)
            finally:mac.release(check)
        except Exception as exc:report['cleanup_error']=repr(exc);report['status']='failed'
        if window:mac.release(window)
        for source in [original,pinyin,ascii_source]:
            if source:mac.release(source)
        (ROOT/'build/p0-engine/system-ime-verification.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n');print(json.dumps(report,ensure_ascii=False,indent=2))
    if report['status']!='passed':raise SystemExit(1)
    subprocess.run([sys.executable,str(ROOT/'scripts/p0-handoff.py'),'native-ime-real-input-done','真实系统输入法验收通过，原输入源恢复','--result',json.dumps(report,ensure_ascii=False)],check=True)

if __name__=='__main__':main()
