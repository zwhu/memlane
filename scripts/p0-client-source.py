#!/usr/bin/env python3
"""Controlled local component/update source; all keys are temporary test keys."""
import argparse
import base64
from datetime import datetime, timedelta, timezone
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import ipaddress
import json
import os
from pathlib import Path
import ssl
import tarfile
import threading
import time

from cryptography import x509
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import ed25519, rsa
from cryptography.x509.oid import NameOID


def digest(path):
    h=hashlib.sha256()
    with path.open("rb") as source:
        while chunk:=source.read(1024*1024):h.update(chunk)
    return h.digest()


def descriptor(path):
    with tarfile.open(path) as source:
        metadata=source.extractfile("component.json").read()
        component=json.loads(metadata)
        total=sum(m.size for m in source.getmembers())
    return {"id":component["id"],"version":component["version"],"platform":component["platform"],
            "protocol_min":component["protocol_min"],"protocol_max":component["protocol_max"],
            "bytes":path.stat().st_size,"unpacked_bytes":total,"sha256":digest(path).hex(),
            "manifest_sha256":hashlib.sha256(metadata).hexdigest()}


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root",type=Path,required=True)
    parser.add_argument("--repo",type=Path,default=Path(__file__).resolve().parents[1])
    args=parser.parse_args()
    root=args.root.resolve();repo=args.repo.resolve()
    root.mkdir(parents=True,exist_ok=True,mode=0o700)
    (root / "releases").mkdir(exist_ok=True)
    component_key=ed25519.Ed25519PrivateKey.generate()
    update_key=ed25519.Ed25519PrivateKey.generate()
    component_public=base64.b64encode(component_key.public_key().public_bytes(serialization.Encoding.Raw,serialization.PublicFormat.Raw)).decode()
    update_public=base64.b64encode(update_key.public_key().public_bytes(serialization.Encoding.Raw,serialization.PublicFormat.Raw)).decode()
    tls_key=rsa.generate_private_key(public_exponent=65537,key_size=2048)
    subject=x509.Name([x509.NameAttribute(NameOID.COMMON_NAME,"Memlane P0 local source")])
    now=datetime.now(timezone.utc)
    cert=(x509.CertificateBuilder().subject_name(subject).issuer_name(subject).public_key(tls_key.public_key())
          .serial_number(x509.random_serial_number()).not_valid_before(now-timedelta(minutes=5)).not_valid_after(now+timedelta(days=2))
          .add_extension(x509.BasicConstraints(ca=True,path_length=None),critical=True)
          .add_extension(x509.SubjectAlternativeName([x509.IPAddress(ipaddress.ip_address("127.0.0.1"))]),critical=False)
          .sign(tls_key,hashes.SHA256()))
    certificate=cert.public_bytes(serialization.Encoding.PEM)
    (root / "public-update-ca.pem").write_bytes(certificate)
    private_path=root / ".tls-test-key.pem"
    private_path.write_bytes(tls_key.private_bytes(serialization.Encoding.PEM,serialization.PrivateFormat.PKCS8,serialization.NoEncryption()))
    private_path.chmod(0o600)
    archives={"cognee-rust":repo / "build/p0-engine/memlane-cognee-rust-0.0.1-darwin-arm64.tar.gz",
              "multilingual-e5-base-int8":repo / "build/p0-engine/memlane-model-e5-base-int8.tar.gz"}
    components=[descriptor(path) for path in archives.values()]
    state={"component_mode":"normal","update_mode":"normal","requests":{},"archive_bytes_sent":0,"component_chunk_delay":0}
    lock=threading.Lock()
    class Handler(BaseHTTPRequestHandler):
        def log_message(self,*args):pass
        def json(self,value,status=200):
            body=json.dumps(value,separators=(",",":"),ensure_ascii=False).encode()
            self.send_response(status);self.send_header("Content-Type","application/json");self.send_header("Content-Length",str(len(body)));self.end_headers();self.wfile.write(body)
        def do_POST(self):
            if self.path!="/control":self.send_error(404);return
            body=json.loads(self.rfile.read(min(int(self.headers.get("Content-Length","0")),4096)))
            with lock:
                if "component_chunk_delay" in body:
                    delay=float(body["component_chunk_delay"])
                    if not 0<=delay<=1:self.json({"error":"invalid delay"},400);return
                    state["component_chunk_delay"]=delay
                for key in ("component_mode","update_mode"):
                    if key in body:
                        if body[key] not in ("normal","signature_bad","truncate","corrupt","incompatible","http_error"):
                            self.json({"error":"unknown mode"},400);return
                        state[key]=body[key]
            self.json({"updated":True})
        def do_GET(self):
            with lock:
                state["requests"][self.path]=state["requests"].get(self.path,0)+1
                component_mode,update_mode=state["component_mode"],state["update_mode"]
            if self.path=="/state":
                with lock:copy=json.loads(json.dumps(state))
                self.json(copy);return
            if self.path=="/components.json":
                feed={"version":1,"components":[dict(d) for d in components]}
                if component_mode=="incompatible":feed["components"][0].update(protocol_min=2,protocol_max=2)
                payload=json.dumps(feed,separators=(",",":"),ensure_ascii=False).encode()
                signature=bytearray(component_key.sign(payload))
                if component_mode=="signature_bad":signature[0]^=1
                body=b'{"payload":'+payload+b',"signature":"'+base64.b64encode(signature)+b'"}'
                self.send_response(200);self.send_header("Content-Length",str(len(body)));self.end_headers();self.wfile.write(body);return
            if self.path=="/update-darwin-arm64.json":
                manifest=root / "latest-update.json"
                if not manifest.exists():self.send_error(404);return
                release=json.loads(manifest.read_text())
                archive=root / "releases" / release["archive"]
                signature=bytearray(update_key.sign(digest(archive)))
                if update_mode=="signature_bad":signature[0]^=1
                self.json({"version":release["version"],"url":tls_url+"/releases/"+archive.name,
                           "size":archive.stat().st_size,"signature":base64.b64encode(signature).decode(),
                           "notes":"P0受控源更新验证"});return
            if self.path.startswith("/components/"):
                id=self.path.removeprefix("/components/").removesuffix(".tar.gz")
                archive=archives.get(id);mode=component_mode
            elif self.path.startswith("/releases/"):
                name=self.path.removeprefix("/releases/")
                if Path(name).name!=name:self.send_error(404);return
                archive=root / "releases" / name;mode=update_mode
            else:self.send_error(404);return
            if not archive or not archive.is_file():self.send_error(404);return
            if mode=="http_error":self.send_error(503);return
            offset=0
            if self.headers.get("Range","").startswith("bytes="):
                offset=int(self.headers["Range"][6:].split("-")[0])
            size=archive.stat().st_size
            self.send_response(206 if offset else 200)
            if offset:self.send_header("Content-Range",f"bytes {offset}-{size-1}/{size}")
            self.send_header("Content-Length",str(size-offset));self.end_headers()
            sent=0
            try:
                with archive.open("rb") as source:
                    source.seek(offset)
                    while chunk:=source.read(64*1024):
                        if mode=="corrupt" and sent==0:chunk=bytes([chunk[0]^1])+chunk[1:]
                        self.wfile.write(chunk);sent+=len(chunk)
                        if self.path.startswith("/components/"):
                            with lock:delay=state["component_chunk_delay"]
                            if delay:time.sleep(delay)
                        if mode=="truncate" and sent>=128*1024:self.close_connection=True;break
            except (BrokenPipeError,ConnectionResetError):pass
            with lock:state["archive_bytes_sent"]+=sent
    http=ThreadingHTTPServer(("127.0.0.1",0),Handler)
    tls=ThreadingHTTPServer(("127.0.0.1",0),Handler)
    context=ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    context.load_cert_chain(str(root / "public-update-ca.pem"),str(private_path))
    tls.socket=context.wrap_socket(tls.socket,server_side=True)
    http_url=f"http://127.0.0.1:{http.server_port}"
    tls_url=f"https://127.0.0.1:{tls.server_port}"
    for component in components:component["url"]=http_url+"/components/"+component["id"]+".tar.gz"
    suffix=hashlib.sha256(str(root).encode()).hexdigest()[:12]
    config={"root":str(root / "client"),"component_source":http_url+"/components.json",
            "component_public_key":component_public,"worker_id":"cognee-rust","model_id":"multilingual-e5-base-int8",
            "service_plist":f"io.github.zwhu.memlane.p0.{suffix}.plist","allow_local_test_source":True,
            "update_manifest_url":tls_url+"/update-darwin-arm64.json","update_ca_pem":certificate.decode()}
    (root / "runtime-config.json").write_text(json.dumps(config,indent=2))
    (root / "public-updates.json").write_text(json.dumps({"publicKey":update_public,"url":tls_url,"deltas":0},indent=2))
    info={"root":str(root),"http_url":http_url,"tls_url":tls_url,"config":str(root / "runtime-config.json"),
          "updates_config":str(root / "public-updates.json"),"identifier":f"io.github.zwhu.memlane.p0.{suffix}",
          "service_plist":config["service_plist"],"temporary_test_keys_only":True}
    (root / "source-info.json").write_text(json.dumps(info,indent=2))
    threading.Thread(target=tls.serve_forever,daemon=True).start()
    print(json.dumps(info,indent=2),flush=True)
    try:http.serve_forever()
    finally:http.server_close();tls.shutdown();tls.server_close();private_path.unlink(missing_ok=True)


if __name__=="__main__":main()
