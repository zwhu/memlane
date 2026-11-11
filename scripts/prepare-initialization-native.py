#!/usr/bin/env python3
"""Hold exactly one real synthetic initialization environment until explicit finish."""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import time
import urllib.request
import uuid

ROOT=Path(__file__).resolve().parents[1]
PYTHON=ROOT/'.local/cognee-python/bin/python'

def digest(path):
    h=hashlib.sha256()
    with path.open('rb') as f:
        while chunk:=f.read(1024*1024):h.update(chunk)
    return h.hexdigest()

def main():
    parser=argparse.ArgumentParser(description=__doc__);parser.add_argument('--out',required=True,type=Path);args=parser.parse_args()
    assert os.environ.get('MEMLANE_DISK_TASK')=='1', 'run through scripts/task.py'
    out=args.out.resolve();out.mkdir(exist_ok=False,parents=True)
    scratch=Path(os.environ['TMPDIR'])/'initialization';scratch.mkdir()
    (scratch/'synthetic-only.json').write_text('{"synthetic_only":true}')
    (out/'scratch.json').write_text(json.dumps({'synthetic_only':True,'root':str(scratch),'task_pid':os.getpid()},indent=2))
    report={'status':'preparing','synthetic_only':True,'stages':[]};server=None
    def save(): (out/'preparation-report.json').write_text(json.dumps(report,indent=2)+'\n')
    def run(command,stage):
        print(stage,flush=True)
        with (out/(stage+'.log')).open('w') as log:subprocess.run([str(x) for x in command],cwd=ROOT,stdout=log,stderr=subprocess.STDOUT,check=True)
        report['stages'].append(stage);save()
    try:
        save()
        run([sys.executable,ROOT/'scripts/build-cognee-service.py','--out',scratch/'service-dist','--report-out',out/'service-build'],'freeze-service')
        archives=scratch/'archives';archives.mkdir()
        run([sys.executable,ROOT/'scripts/package-model.py','--out',archives],'package-e5')
        old=ROOT/'build/onboarding-components/stages/model'
        expected=json.loads((old/'component.json').read_text())
        model=scratch/'bge';shutil.copytree(old,model,symlinks=True);(model/'component.json').unlink()
        pinned='https://huggingface.co/Qdrant/bge-small-zh-v1.5/resolve/46fbe35fd4374a00fee7de77dfddaeb6dd6a2c59/model_optimized.onnx'
        for entry in expected['files']:
            p=model/entry['path']
            if not p.exists():
                assert entry['bytes']==94781076 and entry['sha256']=='1294ea4b6331115a353d81f96b85e8c8d7fdcc284453d5b2fab5b016230aad38', entry
                p.parent.mkdir(parents=True,exist_ok=True)
                with urllib.request.urlopen(pinned,timeout=90) as response,p.open('wb') as dest:shutil.copyfileobj(response,dest)
            assert p.stat().st_size==entry['bytes'] and digest(p)==entry['sha256'],entry
        spec=importlib.util.spec_from_file_location('package_components',ROOT/'scripts/package-onboarding-components.py');pkg=importlib.util.module_from_spec(spec);spec.loader.exec_module(pkg)
        service=scratch/'service';service.mkdir();shutil.copytree(scratch/'service-dist/memlane-cognee-service',service/'service',symlinks=True)
        plugin=scratch/'plugin';plugin.mkdir();upstream=ROOT/'build/agent-integrations-validation/upstream'
        assert subprocess.check_output(['git','-C',str(upstream),'rev-parse','HEAD'],text=True).strip()==pkg.SHA
        assert not subprocess.check_output(['git','-C',str(upstream),'status','--porcelain'],text=True).strip()
        for relative in ['.agents/plugins/marketplace.json','LICENSE','integrations/codex/plugins/cognee']:
            p=upstream/relative;dest=plugin/relative;dest.parent.mkdir(parents=True,exist_ok=True)
            if p.is_dir():shutil.copytree(p,dest,ignore=shutil.ignore_patterns('__pycache__','*.pyc'))
            else:shutil.copy2(p,dest)
        (plugin/'upstream.json').write_text(json.dumps({'repository':'https://github.com/topoteretes/cognee-integrations','sha':pkg.SHA,'version':'1.7.7'}))
        paths=[archives/'memlane-model-e5-base-int8.tar.gz',pkg.archive(model,archives,'cognee-bge-zh','1.5'),pkg.archive(plugin,archives,'cognee-codex-plugin','1.7.7'),pkg.archive(service,archives,'cognee-service','1.6.7-memlane.7','service/memlane-cognee-service')]
        # The separate foreground compiler must finish successfully; never use a partially linked file.
        deadline=time.monotonic()+3600
        while not (out/'worker-ready.json').exists():
            if time.monotonic()>deadline:raise RuntimeError('worker compile receipt timeout')
            time.sleep(1)
        run([sys.executable,ROOT/'scripts/package-worker.py','--out',archives],'package-worker')
        paths.append(archives/'memlane-cognee-rust-0.0.4-darwin-arm64.tar.gz')
        index=scratch/'archives.json';index.write_text(json.dumps([str(p) for p in paths]))
        source=scratch/'source';log=(out/'source.log').open('w')
        server=subprocess.Popen([str(PYTHON),str(ROOT/'scripts/onboarding-component-source.py'),'--root',str(source),'--archives',str(index)],stdout=log,stderr=subprocess.STDOUT)
        deadline=time.monotonic()+30
        while not (source/'source-info.json').exists():
            if server.poll() is not None or time.monotonic()>deadline:raise RuntimeError('component source failed')
            time.sleep(.1)
        info=json.loads((source/'source-info.json').read_text());(out/'component-source.json').write_text(json.dumps(info,indent=2))
        ident='io.github.zwhu.memlane.initialization.'+uuid.uuid4().hex[:12]
        config={'root':str(scratch/'client'),'component_source':info['source'],'component_public_key':info['public_key'],'worker_id':'cognee-rust','model_id':'multilingual-e5-base-int8','service_plist':ident+'.service.plist','allow_local_test_source':True,'official':{'source':info['source'],'public_key':info['public_key'],'allow_local_test_source':True}}
        (out/'runtime-config.json').write_text(json.dumps(config,indent=2));(out/'source-info.json').write_text(json.dumps({'synthetic_only':True,'identifier':ident,'root':str(scratch),'cold_download_bytes':sum(p.stat().st_size for p in paths)},indent=2))
        run([sys.executable,ROOT/'scripts/build-client.py','--out',out,'--runtime-config',out/'runtime-config.json','--name','MemlaneInitialization','--identifier',ident],'build-app')
        run([sys.executable,ROOT/'scripts/prepare-native-launch.py','--out',out],'prepare-native-copy')
        report.update(status='ready-for-native',source_pid=server.pid);save();print('READY_FOR_NATIVE '+str(out),flush=True)
        while not (out/'finish.json').exists():
            if server.poll() is not None:raise RuntimeError('component source exited')
            time.sleep(1)
        report['status']='finished';save()
    except BaseException as e:
        report.update(status='failed',error=repr(e));save();raise
    finally:
        if server and server.poll() is None:
            server.terminate();server.wait(timeout=10)
        print('task scratch will be removed by task.py',flush=True)

if __name__=='__main__':main()
