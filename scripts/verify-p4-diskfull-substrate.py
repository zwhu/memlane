#!/usr/bin/env python3
"""Actual bounded APFS image ENOSPC preflight; never an App acceptance claim."""
import argparse,errno,json,os,plistlib,subprocess,time,traceback
from importlib.machinery import SourceFileLoader
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]
parser=argparse.ArgumentParser(description=__doc__);parser.add_argument('--pointer',type=Path,default=ROOT/'build/p4-delivery/diskfull-fixture-path.txt');parser.add_argument('--out',type=Path,default=ROOT/'build/p4-delivery/diskfull-native-preflight-v3.json');args=parser.parse_args()
f=Path(args.pointer.read_text().strip()).resolve();meta=json.loads((f/'source-info.json').read_text());G=SourceFileLoader('diskfullguard',str(ROOT/'scripts/p3-c2-common.py')).load_module();G.validate_fixture(f,Path(meta['app']),'code')
data=f/'client/data';data.mkdir(mode=0o700,exist_ok=True);assert data.is_dir() and not data.is_symlink() and not list(data.iterdir()),'empty synthetic data directory required';image=f/'explicit-diskfull.dmg';assert not image.exists() and not image.is_symlink(),'never replace an existing image';device=None;filler=data/'explicit-diskfull-filler.bin';filler_owned=False;assert not args.out.exists(),'independent report required'
C=SourceFileLoader('diskfullcommon',str(ROOT/'scripts/p4-diskfull-common.py')).load_module();evidence=C.Evidence(args.out);manifest=C.Evidence(f/'diskfull-manifest.json');owned=C.OwnedImage(image,data)
r={'status':'failed','actual_app':False,'synthetic_only':True,'image':str(image),'mountpoint':str(data),'size_limit_bytes':128*1024*1024}
manifest.write(r)
try:
    p=subprocess.run(['hdiutil','create','-size','128m','-fs','APFS','-nospotlight','-volname','MemlaneDiskFull-'+f.name[-8:],'-type','UDIF',str(image)],capture_output=True,text=True,timeout=60);r['create']={'exit_code':p.returncode,'stderr':p.stderr};assert p.returncode==0,p.stderr
    device=owned.attach();r['device']=device;os.chmod(data,0o700);s=os.statvfs(data);r['free_before']=s.f_bavail*s.f_frsize;count=0;manifest.write(r)
    with filler.open('xb',buffering=0) as out:
        filler_owned=True
        while True:
            assert count<=128*1024*1024,'bounded volume did not exhaust'
            try:count+=out.write(b'\x00'*(1024*1024));os.fsync(out.fileno())
            except OSError as e:assert e.errno==errno.ENOSPC,(e.errno,str(e));r['errno']=e.errno;r['error']=str(e);break
    r['written_bytes']=count;s=os.statvfs(data);r['free_at_failure']=s.f_bavail*s.f_frsize;filler.unlink();filler_owned=False
    deadline=time.monotonic()+15
    while True:
        s=os.statvfs(data);r['free_after_release']=s.f_bavail*s.f_frsize
        if r['free_after_release']>r['free_at_failure']:break
        assert time.monotonic()<deadline,'APFS did not reclaim released filler';time.sleep(.1)
    r['status']='passed'
except Exception as e:r['error']=repr(e);r['traceback']=traceback.format_exc()
finally:
    cleanup=[]
    try:
        if filler_owned and filler.exists():filler.unlink()
        owned.detach();r['detached']=True
    except Exception as e:cleanup.append(repr(e))
    r['detach_retries']=owned.detach_retries
    r['cleanup_errors']=cleanup
    if cleanup:r['status']='failed'
    evidence.write(r);evidence.close();manifest.close()
print(json.dumps(r))
if r['status']!='passed':raise SystemExit(1)
