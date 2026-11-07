"""P2 drivers share an explicit synthetic App root and real private IPC."""
import hashlib,json,os,socket,uuid
from pathlib import Path
from importlib.machinery import SourceFileLoader
ROOT=Path(__file__).resolve().parents[1]
OUT=ROOT/'build/p2'
UI=SourceFileLoader('p2ui',str(ROOT/'scripts/p1-ui.py')).load_module()
fixture=Path((OUT/'fixture-path.txt').read_text().strip())
assert 'memlane-p2-app-' in str(fixture)
cfg=json.loads((fixture/'runtime-config.json').read_text())
data=Path(cfg['root'])/'data'
ipc=f"/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock"
metadata=json.loads((fixture/'source-info.json').read_text())
app=Path(metadata.get('app',str(ROOT/'build/p2-app/darwin-arm64/MemlaneP2.app')))
def call(op,**fields):
    with socket.socket(socket.AF_UNIX) as s:
        s.settimeout(125);s.connect(ipc);s.sendall((json.dumps({'protocol':1,'id':str(uuid.uuid4()),'op':op,**fields})+'\n').encode())
        response=json.loads(s.makefile('rb',buffering=0).readline())
    assert response['ok'],response
    return response.get('result')
