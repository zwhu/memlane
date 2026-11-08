#!/usr/bin/env python3
"""G0 real static extraction/Rust acceptance, isolated synthetic repositories only."""
import argparse
import hashlib
import json
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parents[1]

def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--archive', type=Path, default=ROOT/'build/p4-code-component/memlane-cognee-rust-0.0.2-darwin-arm64.tar.gz')
    p.add_argument('--model', type=Path, default=ROOT/'.local/models/multilingual-e5-base-int8')
    p.add_argument('--enola', type=Path, default=ROOT/'.local/cognee-python/bin/enola')
    p.add_argument('--checker', type=Path, default=ROOT/'build/p4-code/p4-code-check')
    p.add_argument('--out', type=Path, default=ROOT/'build/p4-code/relocated-substrate.json')
    args = p.parse_args()
    args.out.parent.mkdir(parents=True, exist_ok=True)
    result = {'status':'failed','synthetic_only':True,'relocated_archive':str(args.archive),'actual_app':False,'actual_daily_agent':False}
    try:
        with tempfile.TemporaryDirectory(prefix='memlane-code-component-') as name:
            component = Path(name)
            with tarfile.open(args.archive) as archive:
                meta = json.load(archive.extractfile('component.json'))
                assert meta['id']=='cognee-rust' and meta['version']=='0.0.2' and meta['code_recipe']=='enola-0.4.30-v351'
                seen=set()
                for asset in meta['files']:
                    rel=Path(asset['path']);assert not rel.is_absolute() and '..' not in rel.parts and asset['path']==rel.as_posix(), 'component asset path must be canonical relative path'
                    assert asset['path'] not in seen, 'duplicate component asset'
                    seen.add(asset['path'])
                    member=archive.getmember(asset['path']);assert member.isfile()
                    dest=component/rel;dest.parent.mkdir(parents=True,exist_ok=True)
                    with archive.extractfile(member) as src,dest.open('wb') as out:shutil.copyfileobj(src,out)
                    assert dest.stat().st_size==asset['bytes'] and hashlib.sha256(dest.read_bytes()).hexdigest()==asset['sha256']
                    dest.chmod(0o755 if asset['path']==meta['executable'] else 0o644)
            for field in ('executable','library'):
                value=meta[field];rel=Path(value)
                assert value and not rel.is_absolute() and '..' not in rel.parts and value==rel.as_posix() and value in seen, 'component executable/library must be verified archive members'
                target=component/rel
                assert target.is_file() and not target.is_symlink() and target.resolve().is_relative_to(component.resolve()), 'component executable/library escapes archive directory'
                asset=next(x for x in meta['files'] if x['path']==value)
                assert target.stat().st_size==asset['bytes'] and hashlib.sha256(target.read_bytes()).hexdigest()==asset['sha256'], 'selected component member changed after verification'
            run=subprocess.run([str(args.checker),'--worker',str(component/meta['executable']),'--library',str(component/meta['library']),'--model',str(args.model.resolve()),'--enola',str(args.enola.resolve())],capture_output=True,text=True,timeout=210)
            result['execution']=json.loads(run.stdout)
            result['exit_code']=run.returncode
            assert run.returncode==0 and result['execution']['status']=='passed',result['execution'].get('error',run.stderr[-1000:])
            result['status']='passed'
    except Exception as exc:
        result['error']=str(exc)
    args.out.write_text(json.dumps(result,ensure_ascii=False,indent=2)+'\n')
    print(json.dumps({k:v for k,v in result.items() if k!='execution'},ensure_ascii=False))
    if result['status']!='passed':raise SystemExit(1)

if __name__=='__main__':main()
