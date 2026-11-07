#!/usr/bin/env python3
"""正式 C3 服务实际 traex/Rust 验收；Go 测试自身只用新临时数据库。"""
import json,os,subprocess
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1];OUT=ROOT/'build/p3-c3';OUT.mkdir(parents=True,exist_ok=True)
f=Path((ROOT/'build/p3-c2/fixture-path.txt').read_text().strip());assert f.name.startswith('memlane-p3-c2-app-')
worker=json.loads((f/'client/service.json').read_text())['worker']
for key in ['Executable','ModelDir','Library']:
 p=Path(worker[key]);assert p.is_absolute() and p.exists() and p.resolve().is_relative_to((f/'client').resolve())
env=os.environ.copy();env['MEMLANE_C3_REAL']='1';env['MEMLANE_P3_ENGINE_CONFIG']=json.dumps(worker)
cmd=['sh','scripts/go.sh','test','-json','./internal/daemon','-run','^TestOrganizationRealServiceTraexAndRust$','-count=1']
r=subprocess.run(cmd,cwd=ROOT,env=env,capture_output=True,text=True,timeout=300)
(OUT/'real-service.jsonl').write_text(r.stdout);(OUT/'real-service.stderr.txt').write_text(r.stderr)
output=''
for line in r.stdout.splitlines():
 try:output+=json.loads(line).get('Output','')
 except json.JSONDecodeError:pass
marker='ORGANIZATION_REAL_EVIDENCE '
evidence=None
for line in output.splitlines():
 if marker in line:evidence=json.loads(line.split(marker,1)[1])
report={'status':'passed' if r.returncode==0 and evidence and evidence.get('status')=='passed' else 'failed','command':cmd,'exit_code':r.returncode,'evidence':evidence,'log':'build/p3-c3/real-service.jsonl','app_acceptance':False}
(OUT/'real-service.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
print(json.dumps(report,ensure_ascii=False));raise SystemExit(report['status']!='passed')
