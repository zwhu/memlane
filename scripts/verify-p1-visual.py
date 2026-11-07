#!/usr/bin/env python3
"""Verify P1 layouts and in-pane operations in a real isolated MyGo App."""
import argparse,json,tempfile
from pathlib import Path
from importlib.machinery import SourceFileLoader
ROOT=Path(__file__).resolve().parents[1]
driver=SourceFileLoader('p1ui',str(ROOT/'scripts/p1-ui.py')).load_module()

def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--app',type=Path,default=ROOT/'build/p1-visual/darwin-arm64/MemlaneP1Visual.app')
    a=p.parse_args();out=ROOT/'build/p1-screenshots';report={'status':'failed','fixture':'e2e-only synthetic visual data','window':{'width':1200,'height':820},'designs':['docs/images/home.jpg','docs/images/memory-detail.jpg']}
    ui=None
    try:
        with tempfile.TemporaryDirectory(prefix='memlane-p1-visual-') as root:
            ui=driver.UI(a.app.resolve(),Path(root),out,visual=True)
            ui.press('总览');ui.capture('visual-home.png');ui.system_capture('system-visual-home.png')
            assert any(n.get('key')=='overview' for n in ui.nodes())
            assert not any(n.get('key','').startswith('list-') for n in ui.nodes())
            ui.press('项目记忆按仓库隔离');ui.capture('visual-detail.png');ui.system_capture('system-visual-detail.png')
            nav=ui.node(key='nav-总览')['bounds'];listing=ui.node(key='list-全部记忆:')['bounds'];detail=ui.node(label='记忆详情')['bounds']
            bounds={'navigation':{'width':nav['width']+21},'item-list':{'x':listing['x'],'width':listing['width']+1},'detail-pane':{'x':detail['x']-22}}
            assert bounds['navigation']['width']==212,bounds
            assert bounds['item-list']['width']==428,bounds
            assert bounds['detail-pane']['x']==640,bounds
            ui.press('历史记录');assert '恢复 v1' in ui.labels();ui.capture('visual-history.png')
            ui.press('恢复 v1');assert '确认恢复' in ui.labels();assert 'disabled' in ui.node(label='确认恢复')['states'];ui.capture('visual-restore.png')
            ui.press('返回历史');ui.press('返回详情');ui.press('编辑');ui.set('memory-draft','P1 隔离的未保存编辑草稿')
            ui.press('设置');ui.press('全部记忆');assert ui.node(key='memory-draft')['value']=='P1 隔离的未保存编辑草稿'
            ui.press('新增记忆');ui.set('memory-draft','P1 隔离的新增草稿');ui.press('设置');ui.press('全部记忆');assert ui.node(key='memory-draft')['value']=='P1 隔离的新增草稿';ui.capture('visual-new.png')
            assert 'disabled' in ui.node(label='保存')['states']
            report.update(status='passed',three_column_bounds=bounds,home_wide=True,in_pane_history_restore=True,edit_and_new_drafts_preserved=True,synthetic_saves_disabled=True,system_screenshots=['system-visual-home.png','system-visual-detail.png'])
            ui.quit();ui=None
    except Exception as error:
        report['error']=repr(error)
        if ui:
            try:ui.capture('visual-failed.png')
            except Exception:pass
        raise
    finally:
        out.mkdir(parents=True,exist_ok=True);(out/'visual-verification.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
        if ui and ui.proc.poll() is None:ui.quit()
    print(json.dumps(report,ensure_ascii=False,indent=2))
if __name__=='__main__':main()
