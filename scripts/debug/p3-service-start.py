#!/usr/bin/env python3
"""Bounded real App start feedback; no model or private user data."""
import json,subprocess,sys,time
from pathlib import Path
from importlib.machinery import SourceFileLoader
ROOT=Path(__file__).resolve().parents[2];OUT=ROOT/'build/p3-c4'
f=Path((OUT/'fixture-path.txt').read_text().strip());meta=json.loads((f/'source-info.json').read_text());app=Path(meta['app'])
G=SourceFileLoader('launchguard',str(ROOT/'scripts/p3-c2-common.py')).load_module();G.validate_fixture(f,app,phase='c4')
U=SourceFileLoader('launchui',str(ROOT/'scripts/p1-ui.py')).load_module();ui=None;report={'status':'failed'}
try:
 ui=U.UI(app,f,ROOT/'build/p3-c4-launch-probe',extra_env={'PI_CODING_AGENT_DIR':str(f/'pi-agent')})
 ui.press('设置');U.wait(lambda:'启动服务' in ui.labels());ui.press('启动服务');time.sleep(6)
 result=subprocess.run(['launchctl','print','gui/501/'+meta['identifier']+'.service'],capture_output=True,text=True)
 report['launchctl_exit']=result.returncode
 report['launchctl']=[l.strip() for l in (result.stdout+result.stderr).splitlines() if any(x in l for x in ['state =','program =','path =','last exit code','pid =','Could not find','originator','bundle','associated'])]
 report['labels']=ui.labels()
 report['status']='passed' if '服务运行中' in report['labels'] else 'failed'
finally:
 if ui:ui.quit(stop=True)
 (OUT/'service-start-probe.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
print(json.dumps(report,ensure_ascii=False));sys.exit(report['status']!='passed')
