#!/usr/bin/env python3
"""Exercise actual macOS events against the isolated packaged P0 App."""
import argparse
import json
import socket
import subprocess
import sys
import time
from importlib.machinery import SourceFileLoader
from pathlib import Path

ROOT=Path(__file__).resolve().parents[1]
driver=SourceFileLoader('mac_native',str(ROOT/'scripts/mac-native.py')).load_module()
helper=SourceFileLoader('prep_ui',str(ROOT/'scripts/verify-preparation-ui.py')).load_module()

def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--step',choices=['controls','minimize','fullscreen','close','drag','nondrag'],required=True)
    parser.add_argument('--app',type=Path,default=ROOT/'build/p0-ui-prepare/darwin-arm64/MemlaneUITest.app')
    parser.add_argument('--fixture',type=Path)
    args=parser.parse_args()
    fixture=args.fixture or Path((ROOT/'build/p0-engine/ui-fixture-path.txt').read_text().strip())
    mac=driver.Mac();original_front=None
    report={'step':args.step,'status':'failed','input_path':'CGEventPost actual macOS HID events'}
    ui=None;window=None
    out=ROOT/'build/p0-engine/native-system-verification.json'
    combined=json.loads(out.read_text()) if out.exists() else {'steps':[]}
    subprocess.run([sys.executable,str(ROOT/'scripts/p0-handoff.py'),'native-system-'+args.step,'实际CoreGraphics系统输入验收'],check=True)
    try:
        report['graphical_session']=mac.session_state()
        mac.require_unlocked()
        original_front=mac.front_pid()
        ui=helper.UI(args.app,fixture,launch_services=True)
        mac.activate(ui.pid);helper.wait(lambda:mac.front_pid()==ui.pid,timeout=10)
        window=mac.window(ui.pid)
        report['pid']=ui.pid
        report['initial_window']=mac.rectangle(window)
        mac.capture(ui.pid,ROOT/'build/p0-screenshots/system-window.png')
        if args.step in ['controls','minimize']:
            report['yellow_position']=mac.click_button(window,'AXMinimizeButton')
            helper.wait(lambda:mac.bool_attr(window,'AXMinimized'),timeout=10)
            report['actual_yellow_minimized']=True
            report['dock_restore_position']=mac.dock_click(ui.name,mac.text_attr(window,'AXTitle'))
            helper.wait(lambda:not mac.bool_attr(window,'AXMinimized'),timeout=10)
            report['actual_dock_restores']=True
        if args.step in ['controls','fullscreen']:
            report['green_position']=mac.click_button(window,'AXZoomButton')
            helper.wait(lambda:mac.bool_attr(window,'AXFullScreen'),timeout=10)
            mac.capture(ui.pid,ROOT/'build/p0-screenshots/system-fullscreen.png')
            report['actual_green_fullscreen']=True
            report['fullscreen_exit_menu']=mac.menu_press(ui.pid,'视图',['Full Screen','全屏'])
            helper.wait(lambda:not mac.bool_attr(window,'AXFullScreen'),timeout=10)
            helper.wait(lambda:mac.rectangle(window)['width']==report['initial_window']['width'],timeout=10)
            report['standard_fullscreen_menu_exits']=True
        if args.step in ['controls','close']:
            report['red_position']=mac.click_button(window,'AXCloseButton')
            helper.wait(lambda:not ui.req('windows')['windows'][0]['visible'],timeout=10)
            assert ui.proc.poll() is None
            report['actual_red_hides_without_exit']=True
            report['dock_reopen_position']=mac.dock_click(ui.name)
            helper.wait(lambda:ui.req('windows')['windows'][0]['visible'],timeout=10)
            mac.capture(ui.pid,ROOT/'build/p0-screenshots/system-reopened.png')
            report['os_activation_reopens']=True
        if args.step=='drag':
            changes=[]
            for label,local in [('sidebar_titlebar',(100,25)),('content_toolbar',(700,25))]:
                before=mac.rectangle(window)
                mac.drag(before['x']+local[0],before['y']+local[1],35,30)
                after=mac.rectangle(window)
                assert abs(after['x']-before['x']-35)<=2 and abs(after['y']-before['y']-30)<=2,(label,before,after)
                changes.append({'area':label,'before':before,'after':after})
            button=next(n for n in ui.nodes() if n.get('label')=='全部记忆' and n.get('role')=='button')
            before=mac.rectangle(window);b=button['bounds'];mac.click(before['x']+b['x']+b['width']/2,before['y']+b['y']+b['height']/2)
            assert mac.rectangle(window)==before
            helper.wait(lambda:any(n.get('label')=='全部记忆' and n.get('role')=='text' for n in ui.nodes()),timeout=10)
            report.update(actual_titlebar_drag=changes,navigation_click_does_not_drag=True)
            mac.capture(ui.pid,ROOT/'build/p0-screenshots/system-dragged.png')
        if args.step=='nondrag':
            node=next(n for n in ui.nodes() if n.get('label')=='新增记忆' and n.get('role')=='button');b=node['bounds'];before=mac.rectangle(window)
            mac.drag(before['x']+b['x']+b['width']/2,before['y']+b['y']+b['height']/2,12,4)
            assert mac.rectangle(window)==before
            if not any(n.get('key')=='memory-draft' for n in ui.nodes()):
                mac.click(before['x']+b['x']+b['width']/2,before['y']+b['y']+b['height']/2)
            field=next(n for n in ui.nodes() if n.get('key')=='memory-draft');b=field['bounds']
            mac.drag(before['x']+b['x']+20,before['y']+b['y']+20,55,25)
            assert mac.rectangle(window)==before
            report.update(toolbar_button_drag_does_not_move_window=True,textarea_drag_does_not_move_window=True)
        report['status']='passed'
    except Exception as exc:
        report['error']=repr(exc)
    finally:
        if window:mac.release(window)
        if ui and ui.proc.poll() is None:
            try:ui.quit()
            except Exception:
                ui.proc.terminate();ui.proc.wait(timeout=10)
        try:
            if original_front:mac.activate(original_front)
        except Exception as exc:
            report['foreground_restore_note']=repr(exc)
        combined['steps']=[s for s in combined['steps'] if s['step']!=args.step]+[report]
        required={'minimize','fullscreen','close','drag','nondrag'}
        passed={s['step'] for s in combined['steps'] if s['status']=='passed'}
        combined['status']='passed' if required<=passed else 'in_progress'
        out.write_text(json.dumps(combined,ensure_ascii=False,indent=2)+'\n')
        print(json.dumps(report,ensure_ascii=False,indent=2))
    if report['status']!='passed':raise SystemExit(1)
    subprocess.run([sys.executable,str(ROOT/'scripts/p0-handoff.py'),'native-system-'+args.step+'-done','本次实际系统操作验收通过','--result',json.dumps(report,ensure_ascii=False)],check=True)

if __name__=='__main__':main()
