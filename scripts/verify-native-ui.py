#!/usr/bin/env python3
"""Real AppKit buttons and NSAccessibility plus rendered native layout checks."""
import argparse,json,socket,subprocess,sys,uuid,time
from pathlib import Path
from importlib.machinery import SourceFileLoader
ROOT=Path(__file__).resolve().parents[1]
helper=SourceFileLoader('prepui',str(ROOT/'scripts/verify-preparation-ui.py')).load_module()
def main():
 p=argparse.ArgumentParser(description=__doc__);p.add_argument('--app',type=Path,required=True);p.add_argument('--fixture',type=Path,required=True);p.add_argument('--from-step',choices=['buttons','layout','text'],default='buttons');p.add_argument('--only-step',choices=['buttons','layout','text']);a=p.parse_args();path='/tmp/memlane-native-'+uuid.uuid4().hex[:10]+'.sock';ui=helper.UI(a.app,a.fixture,native=path,rpc_timeout=60);s=socket.socket(socket.AF_UNIX);s.settimeout(60);s.connect(path);f=s.makefile('rwb',buffering=0);out=ROOT/'build/p0-engine/native-ui-verification.json';report=json.loads(out.read_text()) if out.exists() else {'steps':[]}
 trace=[];original_req=ui.req
 def traced(method,**params):
  started=time.monotonic()
  try:return original_req(method,**params)
  finally:
   trace.append({'method':method,'action':params.get('action'),'elapsed_ms':round((time.monotonic()-started)*1000,2)})
   (ROOT/'build/p0-engine/native-content-timing.json').write_text(json.dumps(trace,indent=2)+'\n')
 ui.req=traced
 def native(op='snapshot',**fields):s.sendall((json.dumps({'op':op,**fields})+'\n').encode());r=json.loads(f.readline());assert 'error' not in r,r;return r
 def step(name,fn):
  if a.only_step and name!=a.only_step:return
  if ['buttons','layout','text'].index(name)<['buttons','layout','text'].index(a.from_step):return
  subprocess.run([sys.executable,str(ROOT/'scripts/p0-handoff.py'),'e-native-'+name,'真实AppKit/NSAccessibility/窗口渲染验收'],check=True);r={'step':name,'status':'failed'}
  try:r.update(fn() or {});r['status']='passed'
  except Exception as e:r['error']=repr(e)
  report['steps']=[s for s in report['steps'] if s['step']!=name]+[r];report['status']='in_progress';out.write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n');print(json.dumps(r,ensure_ascii=False),flush=True)
  if r['status']!='passed':raise AssertionError(r)
  subprocess.run([sys.executable,str(ROOT/'scripts/p0-handoff.py'),'e-native-'+name+'-done','本子项通过，保留系统输入法和VoiceOver未通过边界','--result',json.dumps(r,ensure_ascii=False)],check=True)
 try:
  def buttons():
   snap=native();assert all(b['exists'] and b['enabled'] and not b['hidden'] for b in snap['buttons']);assert any(m['class']=='NSVisualEffectView' and m['material']==7 for m in snap['materials']);native('minimize');helper.wait(lambda:native()['minimized']);native('restore');helper.wait(lambda:not native()['minimized']);native('zoom');helper.wait(lambda:native()['zoomed']);native('zoom');helper.wait(lambda:not native()['zoomed']);native('close');assert not native()['visible'];assert ui.proc.poll() is None;native('restore');assert native()['visible'];return {'actual_standard_window_buttons':True,'red_hides':True,'yellow_minimize_restore':True,'green_zoom_restore':True,'native_sidebar_material':snap['materials']}
  step('buttons',buttons)
  def layout():
   ui.press('总览');before=next(n['bounds']['width'] for n in ui.nodes() if n.get('label')=='总览' and n.get('role')=='button');ui.press('切换侧边栏');after=next(n['bounds']['width'] for n in ui.nodes() if n.get('label')=='总览' and n.get('role')=='button');assert after<before;ui.capture('native-collapsed.png');ui.press('切换侧边栏');ui.press('设置');samples=[]
   for i in range(4):ui.press('放大')
   assert '140%' in ui.labels();ui.capture('native-zoom-140.png');samples.append({'zoom':1.4,'snapshot':native()})
   for i in range(6):ui.press('缩小')
   assert '80%' in ui.labels();ui.capture('native-zoom-80.png');samples.append({'zoom':.8,'snapshot':native()})
   for i in range(2):ui.press('放大')
   native('resize',width=980,height=620);ui.capture('native-min-size.png');nodes=ui.nodes();b=native()['bounds'];assert b['Width']==980 and b['Height']==620,b
   for n in nodes:
    if n.get('role')=='button' and 'disabled' not in n.get('states',[]):assert n['bounds']['x']>=-1 and n['bounds']['y']>=-1,n
   native('resize',width=1200,height=780);return {'collapse_expand':True,'zoom_80_140':True,'minimum_size_rendered':True,'zoom_materials':[s['snapshot']['materials'] for s in samples]}
  step('layout',layout)
  def text():
   ui.press('新增记忆');text=('P0 synthetic long paragraph 中文原文 request_id 恢复版本。\n'*100);field=next(n for n in ui.nodes() if n.get('key')=='memory-draft');ui.req('act',id=field['id'],action='setValue',text=text);ui.req('act',id=field['id'],action='focus');ui.req('input',events=[{'type':'key','key':'CmdOrCtrl+A'}]);field=next(n for n in ui.nodes() if n.get('key')=='memory-draft');assert field['value']==text;assert sorted(field['selection'])==[0,len(text)],field.get('selection');ui.capture('native-long-selection.png');before=ui.req('tree').get('focus');ui.req('input',events=[{'type':'key','key':'Tab'}]);after=ui.req('tree').get('focus');assert after!=before,(before,after);ax=native()['nsaccessibility'];assert any(n['role']=='AXTextArea' for n in ax),ax;assert any(n['label']=='切换侧边栏' and n['role']=='AXButton' for n in ax);ui.press('设置');ui.press('全部记忆');assert next(n for n in ui.nodes() if n.get('key')=='memory-draft')['value']==text;return {'synthetic_characters':len(text),'long_selection_all':True,'keyboard_tab_moves_focus':True,'nsaccessibility_text_and_button_exposed':True,'draft_preserved':True,'input_path':'MyGo原生内容键盘事件；未验收系统输入法候选窗','voiceover_verified':False}
  step('text',text);report['status']='partial_passed';report['pending']=['系统红黄绿按钮','实际拖动','系统中文输入法候选窗','VoiceOver实际听读'];out.write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
 finally:
  s.close()
  if ui.proc.poll() is None:ui.quit()
if __name__=='__main__':main()
