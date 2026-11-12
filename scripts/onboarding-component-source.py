#!/usr/bin/env python3
"""本轮真实独立组件受控源；签名用于本机验收，不宣称公开发布物。"""
import argparse
import base64
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
from pathlib import Path
import tarfile
import threading
from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric import ed25519

def digest(path):
    value=hashlib.sha256()
    with path.open("rb") as stream:
        while chunk:=stream.read(1024*1024):value.update(chunk)
    return value.hexdigest()

def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root",required=True,type=Path)
    parser.add_argument("--archives",required=True,type=Path)
    parser.add_argument("--development", action="store_true", help="为当前本机开发环境提供真实组件；不创建运行库或启动引擎")
    args=parser.parse_args();args.root.mkdir(parents=True,exist_ok=True,mode=0o700)
    if (args.root/'source-info.json').exists():
        raise SystemExit('source root already has an owner record; inspect its process before choosing a new invocation')
    if not args.development:
        assert json.loads((args.root.parent/"synthetic-only.json").read_text())["synthetic_only"]
    archives={};components=[]
    for raw in json.loads(args.archives.read_text()):
        path=Path(raw).resolve()
        with tarfile.open(path) as tar:
            data=tar.extractfile("component.json").read();metadata=json.loads(data)
            total=sum(item.size for item in tar.getmembers())
        archives[metadata["id"]]=path
        descriptor={key:metadata[key] for key in ("id","version","platform","protocol_min","protocol_max")}
        descriptor.update(bytes=path.stat().st_size,unpacked_bytes=total,sha256=digest(path),manifest_sha256=hashlib.sha256(data).hexdigest());components.append(descriptor)
    key=ed25519.Ed25519PrivateKey.generate();state={"requests":{},"bytes_sent":0};lock=threading.Lock()
    class Handler(BaseHTTPRequestHandler):
        def log_message(self,*args):pass
        def send_json(self,value):
            data=json.dumps(value,separators=(",",":"),ensure_ascii=False).encode();self.send_response(200);self.send_header("Content-Length",str(len(data)));self.end_headers();self.wfile.write(data)
        def do_GET(self):
            with lock:state["requests"][self.path]=state["requests"].get(self.path,0)+1
            if self.path=="/state":
                with lock:copy=json.loads(json.dumps(state))
                self.send_json(copy);return
            if self.path=="/components.json":
                payload=json.dumps({"version":1,"components":components},separators=(",",":"),ensure_ascii=False).encode()
                self.send_json({"payload":json.loads(payload),"signature":base64.b64encode(key.sign(payload)).decode()});return
            name=self.path.removeprefix("/components/").removesuffix(".tar.gz")
            path=archives.get(name)
            if not path:self.send_error(404);return
            offset=0
            if self.headers.get("Range","").startswith("bytes="):offset=int(self.headers["Range"][6:].split("-")[0])
            size=path.stat().st_size
            if not 0<=offset<size:self.send_error(416);return
            self.send_response(206 if offset else 200)
            if offset:self.send_header("Content-Range",f"bytes {offset}-{size-1}/{size}")
            self.send_header("Content-Length",str(size-offset));self.end_headers();sent=0
            try:
                with path.open("rb") as stream:
                    stream.seek(offset)
                    while chunk:=stream.read(65536):self.wfile.write(chunk);sent+=len(chunk)
            except (BrokenPipeError,ConnectionResetError):pass
            with lock:state["bytes_sent"]+=sent
    server=ThreadingHTTPServer(("127.0.0.1",0),Handler);base=f"http://127.0.0.1:{server.server_port}"
    for descriptor in components:descriptor["url"]=base+"/components/"+descriptor["id"]+".tar.gz"
    public=base64.b64encode(key.public_key().public_bytes(serialization.Encoding.Raw,serialization.PublicFormat.Raw)).decode()
    (args.root/"source-info.json").write_text(json.dumps({"source":base+"/components.json","public_key":public,"allow_local_test_source":True,"actual_components":components,"public_distribution":False,"development_source":args.development},indent=2))
    server.serve_forever()

if __name__=="__main__":main()
