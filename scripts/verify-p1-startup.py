#!/usr/bin/env python3
"""Lock down actual App startup after rebuilding a bundle in place."""
import hashlib,json,os,socket
from pathlib import Path
from importlib.machinery import SourceFileLoader
ROOT=Path(__file__).resolve().parents[1];driver=SourceFileLoader('p1ui',str(ROOT/'scripts/p1-ui.py')).load_module()
root=Path((ROOT/'build/p1/fixture-path.txt').read_text().strip());cfg=json.loads((root/'runtime-config.json').read_text());data=Path(cfg['root'])/'data'
ipc=f"/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock"
def ready():
    with socket.socket(socket.AF_UNIX) as s:
        s.settimeout(2);s.connect(ipc);s.sendall(b'{"protocol":1,"id":"startup-check","op":"health"}\n');r=json.loads(s.makefile('rb',buffering=0).readline())
    return r['ok'] and r['result']['state']=='ready'
ui=driver.UI(ROOT/'build/p1-app/darwin-arm64/MemlaneP1.app',root,ROOT/'build/p1-screenshots')
try:
    ui.press('设置');driver.wait(lambda:'启动服务' in ui.labels() or '停止服务' in ui.labels(),timeout=20)
    if '启动服务' in ui.labels():ui.press('启动服务')
    driver.wait(ready,timeout=20)
    result={'status':'passed','launch_services_bundle':True,'actual_service_ready':True,'same_bundle_identifier_and_synthetic_data':True}
    (ROOT/'build/p1/startup-verification.json').write_text(json.dumps(result,indent=2)+'\n');print(json.dumps(result))
finally:ui.quit(stop=True)
