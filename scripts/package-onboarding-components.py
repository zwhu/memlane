#!/usr/bin/env python3
"""归档固定真实 Cognee 服务、FastEmbed 模型与官方插件；不包含业务库或凭据。"""
import argparse
import hashlib
import json
from pathlib import Path
import shutil
import subprocess
import tarfile

from coding_memory_recipe import recipe, contract, prepare_source

ROOT = Path(__file__).resolve().parents[1]
SHA = "997fd6f72253aa97b49996eb1b032793f0257935"

def digest(path):
    value=hashlib.sha256()
    with path.open("rb") as stream:
        while chunk:=stream.read(1024*1024): value.update(chunk)
    return value.hexdigest()

def archive(source, out, identity, version, executable=""):
    files=[]; links=[]
    for path in sorted(source.rglob("*")):
        relative=path.relative_to(source).as_posix()
        if path.is_symlink():
            target=path.readlink()
            assert not target.is_absolute() and path.resolve().is_relative_to(source.resolve()), relative
            links.append({"path":relative,"target":str(target)})
        elif path.is_file(): files.append({"path":relative,"bytes":path.stat().st_size,"sha256":digest(path)})
    metadata={"id":identity,"version":version,"platform":"darwin-arm64","protocol_min":1,"protocol_max":1,"executable":executable,"library":"","model":"","files":files,"links":links}
    (source/"component.json").write_text(json.dumps(metadata,separators=(",",":")))
    target=out/(identity+"-"+version+".tar.gz")
    with tarfile.open(target,"w:gz",compresslevel=6,dereference=False) as tar:
        for path in sorted(source.iterdir()): tar.add(path,arcname=path.name)
    print(json.dumps({"id":identity,"version":version,"bytes":target.stat().st_size,"sha256":digest(target)}),flush=True)
    return target

def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--model-cache",type=Path)
    parser.add_argument("--upstream",type=Path)
    parser.add_argument("--coding-memory", action="store_true", help="仅归档固定的新服务和官方插件，复用已有嵌入模型")
    parser.add_argument("--source-cache", type=Path, default=ROOT/".cache/coding-memory-components")
    parser.add_argument("--out",type=Path,default=ROOT/"build/onboarding-components")
    parser.add_argument("--service",type=Path,default=ROOT/"build/cognee-service-component/memlane-cognee-service")
    parser.add_argument("--service-version")
    args=parser.parse_args();args.out.mkdir(parents=True,exist_ok=True)
    if args.coding_memory:
        selected = recipe()
        if args.service_version not in (None, selected['service']['component_version']):
            parser.error('coding-memory component version is fixed by its recipe')
        source_contract = args.service / '_internal/memlane-coding-memory.json'
        if source_contract.is_symlink() or not source_contract.is_file() or source_contract.stat().st_size > 4096 or json.loads(source_contract.read_text()) != contract():
            parser.error('service has no matching frozen coding-memory provenance')
        upstream, _ = prepare_source('plugin', args.source_cache)
        stages = args.out / 'stages'
        if stages.exists():
            parser.error('coding-memory staging output already exists; retain it for its consumers')
        stages.mkdir()
        service, plugin = stages / 'service', stages / 'plugin'
        service.mkdir(); plugin.mkdir()
        shutil.copytree(args.service, service / 'service', symlinks=True)
        for relative in ('.agents/plugins/marketplace.json', 'LICENSE', 'integrations/codex/plugins/cognee'):
            source, target = upstream / relative, plugin / relative
            target.parent.mkdir(parents=True, exist_ok=True)
            if source.is_dir():
                shutil.copytree(source, target, ignore=shutil.ignore_patterns('__pycache__', '*.pyc'))
            else:
                shutil.copy2(source, target)
        (plugin / 'upstream.json').write_text(json.dumps({'repository': selected['plugin']['repository'], 'sha': selected['plugin']['commit'], 'version': selected['plugin']['version']}))
        outputs = [archive(service, args.out, 'cognee-service', selected['service']['component_version'], 'service/memlane-cognee-service'),
                   archive(plugin, args.out, 'cognee-codex-plugin', selected['plugin']['version'])]
        (args.out / 'archives.json').write_text(json.dumps([str(path) for path in outputs], indent=2))
        (args.out / 'coding-memory-inputs.json').write_text(json.dumps(contract(), indent=2))
        return
    if args.upstream is None or args.model_cache is None:
        parser.error('legacy mode requires --upstream and --model-cache')
    args.service_version = args.service_version or '1.6.7-memlane.3'
    assert subprocess.check_output(["git","rev-parse","HEAD"],cwd=args.upstream,text=True).strip()==SHA
    assert subprocess.check_output(["git","status","--porcelain"],cwd=args.upstream,text=True).strip()=="", "Official source must be unchanged"
    manifest=json.loads((args.upstream/"integrations/codex/plugins/cognee/.codex-plugin/plugin.json").read_text())
    assert manifest["version"]=="1.7.7"
    stages=args.out/"stages";stages.mkdir(exist_ok=True)
    service=stages/"service";model=stages/"model";plugin=stages/"plugin"
    for path in (service,model,plugin):
        if path.exists(): shutil.rmtree(path)
        path.mkdir()
    shutil.copytree(args.service,service/"service",symlinks=True)
    # Only this Goal's real downloaded BGE snapshot/cache, never user caches.
    assert args.model_cache.resolve().is_relative_to((ROOT/"build/memory-browsing-validation").resolve())
    for name in ("models--Qdrant--bge-small-zh-v1.5","models--BAAI--bge-small-zh-v1.5"):
        path=args.model_cache/name
        if path.is_dir(): shutil.copytree(path,model/"hub"/name,symlinks=True)
    assert list(model.rglob("*.onnx")), "A real downloaded model is required"
    for relative in (".agents/plugins/marketplace.json","LICENSE","integrations/codex/plugins/cognee"):
        source=args.upstream/relative;target=plugin/relative;target.parent.mkdir(parents=True,exist_ok=True)
        if source.is_dir(): shutil.copytree(source,target,symlinks=True,ignore=shutil.ignore_patterns("__pycache__","*.pyc"))
        else: shutil.copy2(source,target)
    (plugin/"upstream.json").write_text(json.dumps({"repository":"https://github.com/topoteretes/cognee-integrations","sha":SHA,"version":"1.7.7"}))
    outputs=[archive(service,args.out,"cognee-service",args.service_version,"service/memlane-cognee-service"),archive(model,args.out,"cognee-bge-zh","1.5"),archive(plugin,args.out,"cognee-codex-plugin","1.7.7")]
    (args.out/"archives.json").write_text(json.dumps([str(path) for path in outputs],indent=2))

if __name__=="__main__":main()
