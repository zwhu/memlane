#!/usr/bin/env python3
"""Read actual VoiceOver navigation output for the isolated synthetic P0 App."""
import json
import subprocess
import sys
import time
import argparse
import ctypes as C
from pathlib import Path
from importlib.machinery import SourceFileLoader

ROOT=Path(__file__).resolve().parents[1]
native=SourceFileLoader('mac_native',str(ROOT/'scripts/mac-native.py')).load_module()
helper=SourceFileLoader('prep_ui',str(ROOT/'scripts/verify-preparation-ui.py')).load_module()

def vo(command):
    script='with timeout of 4 seconds\ntell application "VoiceOver"\n'+command+'\nend tell\nend timeout'
    result=subprocess.run(['osascript','-e',script],capture_output=True,text=True,timeout=6)
    if result.returncode:raise RuntimeError(result.stderr.strip())
    return result.stdout.strip()

def main():
    parser=argparse.ArgumentParser(description=__doc__);parser.add_argument('--caption',action='store_true');args=parser.parse_args()
    subprocess.run([sys.executable,str(ROOT/'scripts/p0-handoff.py'),'native-voiceover-real','实际VoiceOver导航、朗读输出、文字编辑及状态恢复'],check=True)
    mac=native.Mac();original_vo=mac.voiceover_enabled();original_front=mac.front_pid()
    sources=native.InputSources(mac);original_source=sources.current();ascii_source=None
    report={'status':'failed','original_voiceover_enabled':original_vo,'output_path':'VoiceOver actual caption output' if args.caption else 'VoiceOver last phrase and cursor, never injected speech'}
    ui=None;window=None;caption_changed=False
    def caption():
        result=subprocess.run(['pgrep','-f','^/System/Library/PrivateFrameworks/ScreenReader.framework/.*ScreenReaderUIServer.app/Contents/MacOS/ScreenReaderUIServer'],capture_output=True,text=True)
        pids=[int(p) for p in result.stdout.split()]
        if len(pids)!=1:return ''
        texts=[];app=mac.ax_app(pids[0]);windows=mac.attr(app,'AXWindows',True)
        def walk(element,depth):
            if depth>7:return
            if mac.text_attr(element,'AXRole',True) in ['AXStaticText','AXTextArea','AXTextField']:
                texts.append(mac.text_attr(element,'AXValue',True) or mac.text_attr(element,'AXTitle',True))
            kids=mac.attr(element,'AXChildren',True)
            if kids:
                try:
                    for i in range(mac.array_count(kids)):walk(mac.array_at(kids,i),depth+1)
                finally:mac.release(kids)
        try:
            if windows:
                for i in range(mac.array_count(windows)):walk(mac.array_at(windows,i),0)
        finally:
            if windows:mac.release(windows)
            mac.release(app)
        return '\n'.join(t for t in texts if t)
    def current_output():return caption() if args.caption else vo('get content of last phrase')
    def first():
        if args.caption:mac.chord(115,(1<<18)|(1<<19))
        else:vo('tell vo cursor to move to first item')
    def right():
        if args.caption:mac.chord(124,(1<<18)|(1<<19))
        else:vo('tell vo cursor to move right')
    def set_system_voiceover(enabled):
        subprocess.run(['open','x-apple.systempreferences:com.apple.preference.universalaccess?Seeing_VoiceOver'],check=True,timeout=5);time.sleep(1)
        pid=mac.bundle_pids('com.apple.systempreferences')[0];settings_window=mac.window(pid);checks=[]
        def find_switch(element,depth):
            if depth>12:return
            if mac.text_attr(element,'AXRole',True)=='AXCheckBox':checks.append(mac.retain(element))
            kids=mac.attr(element,'AXChildren',True)
            if kids:
                try:
                    for i in range(mac.array_count(kids)):find_switch(mac.array_at(kids,i),depth+1)
                finally:mac.release(kids)
        try:
            assert mac.text_attr(settings_window,'AXTitle') in ['旁白','VoiceOver'];find_switch(settings_window,0);assert len(checks)==1
            value=mac.attr(checks[0],'AXValue');number=C.c_longlong()
            try:assert mac.number_value(value,4,C.byref(number))
            finally:mac.release(value)
            if bool(number.value)!=enabled:
                r=mac.rectangle(checks[0]);mac.activate(pid);mac.click(r['x']+r['width']/2,r['y']+r['height']/2)
        finally:
            for check in checks:mac.release(check)
            mac.release(settings_window)
    try:
        mac.require_unlocked()
        fixture=Path((ROOT/'build/p0-engine/ui-fixture-path.txt').read_text().strip())
        ui=helper.UI(ROOT/'build/p0-ui-prepare/darwin-arm64/MemlaneUITest.app',fixture,launch_services=True,system_home=True)
        mac.activate(ui.pid);window=mac.window(ui.pid);report['pid']=ui.pid
        def click(label):
            node=next(n for n in ui.nodes() if n.get('label')==label and n.get('role')=='button');b=node['bounds'];w=mac.rectangle(window)
            mac.click(w['x']+b['x']+b['width']/2,w['y']+b['y']+b['height']/2)
        click('新增记忆')
        report['stage']='enable'
        def voiceover_engine_pids():
            result=subprocess.run(['pgrep','-f','^/System/Library/CoreServices/VoiceOver.app/Contents/MacOS/VoiceOver( |$)'],capture_output=True,text=True)
            return [int(pid) for pid in result.stdout.split()]
        if not original_vo:
            quickstart_pattern='^/System/Library/PrivateFrameworks/ScreenReader.framework/Versions/A/Resources/VoiceOver Quickstart.app/Contents/MacOS/VoiceOver Quickstart'
            def quickstart():
                result=subprocess.run(['pgrep','-f',quickstart_pattern],capture_output=True,text=True)
                pids=[int(p) for p in result.stdout.split()]
                return pids[0] if len(pids)==1 else None
            set_system_voiceover(True)
            welcome=helper.wait(lambda:quickstart() or mac.voiceover_enabled(),timeout=10)
            if isinstance(welcome,int) and not isinstance(welcome,bool):
                welcome_window=mac.window(welcome);buttons=[]
                def find_button(element,depth):
                    if depth>6:return
                    if mac.text_attr(element,'AXRole',True)=='AXButton' and mac.text_attr(element,'AXTitle',True) in ['使用旁白','Use VoiceOver']:
                        buttons.append(mac.retain(element))
                    kids=mac.attr(element,'AXChildren',True)
                    if kids:
                        try:
                            for i in range(mac.array_count(kids)):find_button(mac.array_at(kids,i),depth+1)
                        finally:mac.release(kids)
                try:
                    find_button(welcome_window,0);assert len(buttons)==1
                    r=mac.rectangle(buttons[0]);action=mac.string('AXPress')
                    try:assert mac.ax_action(buttons[0],action)==0
                    finally:mac.release(action)
                    report['first_welcome_used']={'path':'system welcome AXPress','bounds':r}
                finally:
                    for button in buttons:mac.release(button)
                    mac.release(welcome_window)
            helper.wait(mac.voiceover_enabled,timeout=15)
            time.sleep(.5)
            if not voiceover_engine_pids():
                subprocess.run(['launchctl','kickstart','gui/'+str(__import__('os').getuid())+'/com.apple.VoiceOver'],check=True,timeout=8)
                report['system_job_started']=True
                helper.wait(voiceover_engine_pids,timeout=10)
        mac.activate(ui.pid);time.sleep(.5)
        report['stage']='output'
        report['voiceover_pids']=voiceover_engine_pids()
        report['screenreader_ui_pids']=mac.bundle_pids('com.apple.ScreenReaderUIServer')
        result=subprocess.run(['launchctl','print','gui/'+str(__import__('os').getuid())+'/com.apple.VoiceOver'],capture_output=True,text=True)
        report['voiceover_job']=[line.strip() for line in result.stdout.splitlines() if any(key in line for key in ['state =','pid =','runs =','last exit code'])]
        if args.caption and not caption():
            mac.chord(109,(1<<18)|(1<<19)|(1<<20));caption_changed=True
            helper.wait(caption,timeout=8)
        report['initial_phrase']=current_output()
        first()
        phrases=[];labels=set()
        for i in range(55):
            # Only request output while the target test App remains in front.
            assert mac.front_pid()==ui.pid,'test App lost foreground during VoiceOver'
            text='' if args.caption else vo('get text under cursor of vo cursor');phrase=current_output()
            phrases.append({'cursor':text,'phrase':phrase})
            for label in ['总览','全部记忆','设置','记忆内容','保存']:
                if label in text or label in phrase:labels.add(label)
            if {'总览','全部记忆','设置','记忆内容','保存'}<=labels:break
            right();time.sleep(.22)
        report['navigation_output']=phrases;report['labels_read']=sorted(labels)
        assert {'总览','全部记忆','设置','记忆内容','保存'}<=labels,('VoiceOver missing labels',labels)
        assert any('保存' in p['phrase'] and any(s in p['phrase'].lower() for s in ['dimmed','disabled','不可用','变暗','暗淡']) for p in phrases),'disabled save was not spoken'
        report['stage']='edit'
        node=next(n for n in ui.nodes() if n.get('key')=='memory-draft');b=node['bounds'];w=mac.rectangle(window)
        mac.click(w['x']+b['x']+20,w['y']+b['y']+20)
        ascii_source=sources.ascii();sources.use(ascii_source);mac.type_ascii('voiceover p0');mac.key(51)
        expected='voiceover p'
        helper.wait(lambda:any(n.get('key')=='memory-draft' and n.get('value')==expected for n in ui.nodes()),timeout=8)
        report['edited_text']=expected
        report['edit_phrase']=current_output()
        assert any(s in report['edit_phrase'] for s in ['voiceover','p','删除','delete']),report['edit_phrase']
        mac.capture(ui.pid,ROOT/'build/p0-screenshots/system-voiceover.png')
        report['status']='passed'
    except Exception as exc:
        report['error']=repr(exc)
        try:
            pids=mac.bundle_pids('com.apple.VoiceOver')+mac.bundle_pids('com.apple.ScreenReaderUIServer')
            report['output_windows']=mac.visible_windows(pids)
            if ui and mac.front_pid()==ui.pid:
                for item in report['output_windows']:
                    if item['bounds']['width']>50 and item['bounds']['height']>20:
                        mac.capture_id(item['number'],ROOT/'build/p0-screenshots'/('voiceover-output-'+str(item['number'])+'.png'))
        except Exception as exc:report['diagnosis_error']=repr(exc)
    finally:
        try:
            if caption_changed and mac.voiceover_enabled():mac.chord(109,(1<<18)|(1<<19)|(1<<20))
            if not original_vo and mac.voiceover_enabled():
                mac.chord(96,1<<20)
                time.sleep(.5)
                if mac.voiceover_enabled():
                    # The system setting is authoritative even when the process
                    # exits without changing its switch. Only this pane's single
                    # switch is accessed, never its application menu/history.
                    subprocess.run(['open','x-apple.systempreferences:com.apple.preference.universalaccess?Seeing_VoiceOver'],check=True,timeout=5);time.sleep(1)
                    settings_window=mac.window(mac.bundle_pids('com.apple.systempreferences')[0]);checks=[]
                    def find_switch(element,depth):
                        if depth>12:return
                        if mac.text_attr(element,'AXRole',True)=='AXCheckBox':checks.append(mac.retain(element))
                        kids=mac.attr(element,'AXChildren',True)
                        if kids:
                            try:
                                for i in range(mac.array_count(kids)):find_switch(mac.array_at(kids,i),depth+1)
                            finally:mac.release(kids)
                    try:
                        assert mac.text_attr(settings_window,'AXTitle') in ['旁白','VoiceOver'];find_switch(settings_window,0);assert len(checks)==1
                        value=mac.attr(checks[0],'AXValue');number=C.c_longlong()
                        try:assert mac.number_value(value,4,C.byref(number))
                        finally:mac.release(value)
                        if number.value:
                            r=mac.rectangle(checks[0]);mac.click(r['x']+r['width']/2,r['y']+r['height']/2)
                    finally:
                        for check in checks:mac.release(check)
                        mac.release(settings_window)
                helper.wait(lambda:not mac.voiceover_enabled(),timeout=10)
            report['voiceover_restored']=mac.voiceover_enabled()==original_vo
        except Exception as exc:report['voiceover_cleanup_error']=repr(exc);report['status']='failed'
        try:
            if ui and ui.proc.poll() is None:ui.quit()
        except Exception as exc:report['app_cleanup_error']=repr(exc);report['status']='failed'
        try:mac.activate(original_front)
        except Exception as exc:report['foreground_restore_note']=repr(exc)
        try:
            sources.use(original_source);check=sources.current()
            try:report['input_source_restored']=sources.identifier(check)==sources.identifier(original_source)
            finally:mac.release(check)
        except Exception as exc:report['input_cleanup_error']=repr(exc);report['status']='failed'
        if window:mac.release(window)
        for source in [original_source,ascii_source]:
            if source:mac.release(source)
        (ROOT/'build/p0-engine/voiceover-verification.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n');print(json.dumps(report,ensure_ascii=False,indent=2))
    subprocess.run([sys.executable,str(ROOT/'scripts/p0-handoff.py'),'native-voiceover-result','保存实际VoiceOver结果与遗留问题','--result',json.dumps(report,ensure_ascii=False)],check=True)
    if report['status']!='passed':raise SystemExit(1)

if __name__=='__main__':main()
