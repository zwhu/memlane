#!/usr/bin/env python3
"""Build the light App and its helper, then seal the LaunchAgent bundle layout."""
import argparse
import hashlib
import json
import os
import sys
from pathlib import Path
import plistlib
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]
GO = ROOT / ".local/toolchains/go-1.27.3/go/bin/go"


def signing_identity(config, override=None):
    identity = override if override is not None else config.get("macos", {}).get("signingIdentity", "-")
    if not isinstance(identity, str) or not identity.strip():
        raise ValueError("macOS signing identity must be nonempty")
    return identity


def seal_command(app, identity, entitlements=None):
    # MyGo has already signed nested code with this identity. Reseal only the
    # containing bundle after adding its LaunchAgent; keep its entitlements.
    command = ["/usr/bin/codesign", "--force", "--sign", identity]
    if identity == "-":
        command += ["--timestamp=none"]
    else:
        command += ["--options", "runtime", "--timestamp"]
    if entitlements:
        command += ["--entitlements", str(entitlements)]
    else:
        command += ["--preserve-metadata=entitlements"]
    return command + [str(app)]


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--out",type=Path,default=ROOT / "build")
    parser.add_argument("--runtime-config",type=Path)
    parser.add_argument("--updates-config",type=Path)
    parser.add_argument("--version")
    parser.add_argument("--name")
    parser.add_argument("--identifier")
    parser.add_argument("--signing-identity",help="Override macos.signingIdentity for both MyGo and the final bundle seal")
    parser.add_argument("--offline-candidate",action="store_true",help="Do not replace the retained runtime package pointer; never launches the App")
    parser.add_argument("--automation",action="store_true")
    parser.add_argument("--skip-dmg",action="store_true",help="Build only the App for focused native verification")
    args=parser.parse_args()
    subprocess.run(["python3", str(ROOT / "scripts/disk-space.py")], check=True)
    config=json.loads((ROOT / "mygo.json").read_text())
    config["main"]=str(ROOT / "cmd/memlane-app")
    if config.get("icon"):
        config["icon"]=str((ROOT / config["icon"]).resolve())
    config["out"]=str(args.out.resolve())
    if args.version:config["version"]=args.version
    if args.name:config["name"]=args.name
    if args.identifier:config["identifier"]=args.identifier
    identity=signing_identity(config,args.signing_identity)
    config.setdefault("macos",{})["signingIdentity"]=identity
    if config["macos"].get("entitlements"):
        config["macos"]["entitlements"]=str((ROOT / config["macos"]["entitlements"]).resolve())
    cfg_root=ROOT / ".cache/client-bundles"
    cfg_root.mkdir(parents=True,exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="source-",dir=cfg_root) as name:
        source=Path(name)
        home=source / "private-home";home.mkdir()
        env={"PATH":str(GO.parent)+":/usr/bin:/bin","HOME":str(home),
             "GOPATH":str(ROOT / ".local/gopath"),"GOCACHE":str(ROOT / ".cache/go-build"),
             "GOTOOLCHAIN":"local","CGO_ENABLED":"0","GOFLAGS":"-buildvcs=false"}
        resources=source / "resources/darwin-arm64"
        helper=resources / "bin/memlaned";helper.parent.mkdir(parents=True)
        def run(command):
            subprocess.run(command,cwd=ROOT,env=env,check=True)
        run([str(GO),"build","-trimpath","-ldflags=-s -w","-o",str(helper),"./cmd/memlaned"])
        run([str(GO),"build","-trimpath","-ldflags=-s -w","-o",str(resources / "bin/memlane"),"./cmd/memlane"])
        service_plist="io.github.zwhu.memlane.service.plist"
        variables={}
        if args.runtime_config:
            runtime=json.loads(args.runtime_config.read_text())
            if not Path(runtime["root"]).is_absolute():raise SystemExit("runtime root must be absolute")
            service_plist=runtime["service_plist"]
            variables["MEMLANE_SERVICE_CONFIG"]=str(Path(runtime["root"]) / "service.json")
            shutil.copy2(args.runtime_config,resources / "runtime-config.json")
        if args.updates_config:
            updates=json.loads(args.updates_config.read_text())
            if set(updates)-{"publicKey","url","deltas"}:raise SystemExit("only public update configuration is accepted")
            config["updates"]=updates
        (source / "mygo.json").write_text(json.dumps(config,indent=2))
        if args.automation:
            env["GOFLAGS"]+=" -tags=e2e"
            # Only the verification UI uses AppKit's own-view bitmap export.
            # Runtime helpers above and ordinary client builds remain pure Go.
            env["CGO_ENABLED"]="1"
            env["MYGO_INSPECTOR"]="1"
        # MyGo signs the helper resources. The LaunchAgent must be placed in
        # Contents/Library, so seal the complete bundle again before the DMG.
        run([str(GO),"tool","mygo","build","-skip-notarize","-skip-dmg",str(source)])
        stage=args.out.resolve() / "darwin-arm64"
        app=stage / (config["name"]+".app")
        agents=app / "Contents/Library/LaunchAgents";agents.mkdir(parents=True,exist_ok=True)
        plist={"Label":service_plist.removesuffix(".plist"),"BundleProgram":"Contents/Resources/bin/memlaned",
               "ProgramArguments":["memlaned"],"RunAtLoad":True,"KeepAlive":{"SuccessfulExit":False},
               "ThrottleInterval":10,"ProcessType":"Background"}
        if variables:plist["EnvironmentVariables"]=variables
        (agents / service_plist).write_bytes(plistlib.dumps(plist))
        run(["/usr/bin/plutil","-lint",str(agents / service_plist)])
        run(seal_command(app,identity,config["macos"].get("entitlements")))
        run(["/usr/bin/codesign","--verify","--deep","--strict",str(app)])
        dmg=stage / (config.get("slug","memlane")+"-"+config["version"]+"-arm64.dmg")
        image=source / "image";image.mkdir()
        shutil.copytree(app,image / app.name,symlinks=True)
        (image / "Applications").symlink_to("/Applications")
        if not args.skip_dmg:
            run(["/usr/bin/hdiutil","create","-ov","-volname",config["name"],"-srcfolder",str(image),"-format","UDZO",str(dmg)])
            sums=args.out.resolve() / "SHA256SUMS"
            relative=str(dmg.relative_to(args.out.resolve()))
            lines=sums.read_text().splitlines() if sums.exists() else []
            lines=[line for line in lines if line.split("  ",1)[-1]!=relative]
            lines.append(hashlib.sha256(dmg.read_bytes()).hexdigest()+"  "+relative)
            sums.write_text("\n".join(lines)+"\n")
        if not args.offline_candidate:
            retention = ROOT / "build/disk-cleanup/retained-package.txt"
            retention.parent.mkdir(exist_ok=True)
            retention.write_text(str(args.out.resolve())+"\n")
        print(json.dumps({"app":str(app),"dmg":None if args.skip_dmg else str(dmg),"service_plist":service_plist,
                          "signing_identity":identity,"offline_candidate":args.offline_candidate,
                          "runtime_model_included":False,"developer_id_notarized":False},indent=2))


if __name__=="__main__":
    if os.environ.get("MEMLANE_DISK_TASK") != "1":
        raise SystemExit(subprocess.call([sys.executable, str(ROOT / "scripts/task.py"), "--", sys.executable, __file__, *sys.argv[1:]]))
    try:
        main()
    finally:
        subprocess.run(["python3", str(ROOT / "scripts/disk-space.py"), "--finish"], check=False)
