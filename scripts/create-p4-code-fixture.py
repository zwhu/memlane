#!/usr/bin/env python3
"""Fresh G1 App roots and a signed, loopback-only component source; no user data."""
import base64
import argparse
import hashlib
import json
from pathlib import Path
import shutil
import socket
import subprocess
import tarfile
import tempfile
import uuid

ROOT=Path(__file__).resolve().parents[1]

def main():
    parser=argparse.ArgumentParser(description=__doc__);parser.add_argument('--app',type=Path,default=ROOT/'build/p4-code-app/darwin-arm64/MemlaneCode.app');parser.add_argument('--pointer',type=Path,default=ROOT/'build/p4-code/app-fixture-path.txt');parser.add_argument('--worker-archive',type=Path,default=ROOT/'build/p4-code-component/memlane-cognee-rust-0.0.2-darwin-arm64.tar.gz');args=parser.parse_args()
    out=ROOT/'build/p4-code';out.mkdir(parents=True,exist_ok=True)
    f=Path(tempfile.mkdtemp(prefix='memlane-p3-code-app-'));client=f/'client';client.mkdir();source=f/'source';source.mkdir()
    ident='io.github.zwhu.memlane.code.'+uuid.uuid4().hex[:12]
    with socket.socket() as s:s.bind(('127.0.0.1',0));port=s.getsockname()[1]
    url='http://127.0.0.1:'+str(port)
    archives=[args.worker_archive,ROOT/'build/p0-engine/memlane-model-e5-base-int8.tar.gz',ROOT/'build/p4-code-component/memlane-enola-code-0.4.30-v351-darwin-arm64.tar.gz']
    descriptors=[]
    for path in archives:
        with tarfile.open(path) as archive:
            raw=archive.extractfile('component.json').read();meta=json.loads(raw);total=sum(x.size for x in archive.getmembers())
        filename=meta['id']+'.tar.gz';shutil.copy2(path,source/filename)
        descriptors.append({'id':meta['id'],'version':meta['version'],'platform':meta['platform'],'protocol_min':meta['protocol_min'],'protocol_max':meta['protocol_max'],'url':url+'/'+filename,'bytes':path.stat().st_size,'unpacked_bytes':total,'sha256':hashlib.sha256(path.read_bytes()).hexdigest(),'manifest_sha256':hashlib.sha256(raw).hexdigest()})
    payload=json.dumps({'version':1,'components':descriptors},separators=(',',':')).encode()
    program='''import sys,json,base64
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey
from cryptography.hazmat.primitives import serialization
k=Ed25519PrivateKey.generate();print(json.dumps({'signature':base64.b64encode(k.sign(sys.stdin.buffer.read())).decode(),'public_key':base64.b64encode(k.public_key().public_bytes(serialization.Encoding.Raw,serialization.PublicFormat.Raw)).decode()}))'''
    p=subprocess.run([str(ROOT/'.local/cognee-python/bin/python'),'-c',program],input=payload,capture_output=True,check=True);key=json.loads(p.stdout)
    (source/'feed').write_text('{"payload":'+payload.decode()+',"signature":'+json.dumps(key['signature'])+'}')
    cfg={'root':str(client),'component_source':url+'/feed','component_public_key':key['public_key'],'worker_id':'cognee-rust','model_id':'multilingual-e5-base-int8','code_extractor_id':'enola-code','service_plist':ident+'.service.plist','allow_local_test_source':True}
    (f/'runtime-config.json').write_text(json.dumps(cfg,indent=2))
    for folder in ('pi-agent','agent-home','ui-home'):(f/folder).mkdir()
    for name in ('repo-a','repo-b'):
        repo=f/name;subprocess.run(['/usr/bin/git','init',str(repo)],check=True,capture_output=True)
        for path,text in {'src/greet.ts':'export function greet() { return "synthetic code"; }\n','src/main.ts':'import { greet } from "./greet.js";\nexport function run() { return greet(); }\n','app/greet.py':'def greet():\n    return "synthetic code"\n','app/main.py':'from app.greet import greet\ndef run():\n    return greet()\n'}.items():
            file=repo/path;file.parent.mkdir(parents=True,exist_ok=True);file.write_text(text)
    info={'identifier':ident,'synthetic_only':True,'app':str(args.app.resolve()),'slice':'G1/G2','source_port':port,'source_root':str(source),'component_descriptors':descriptors,'formal_source':False,'user_data_copied':False,'agent_configs_copied':False}
    (f/'source-info.json').write_text(json.dumps(info,indent=2));args.pointer.parent.mkdir(parents=True,exist_ok=True);args.pointer.write_text(str(f)+'\n');print(json.dumps({'fixture':str(f),'identifier':ident,'cold_component_download_bytes':sum(d['bytes'] for d in descriptors),'source_port':port}))

if __name__=='__main__':main()
