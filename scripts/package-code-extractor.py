#!/usr/bin/env python3
"""Package the pinned standalone Enola binary and its actual Apache-2.0 license."""
import argparse
import gzip
import hashlib
import io
import json
from pathlib import Path
import subprocess
import tarfile

ROOT = Path(__file__).resolve().parents[1]

def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--out',type=Path,default=ROOT/'build/p4-code-component')
    args=p.parse_args();binary=ROOT/'.local/cognee-python/bin/enola'
    data=subprocess.check_output([str(binary),'--version','--json'],env={'PATH':'/usr/bin:/bin','HOME':'/var/empty','ENOLA_NO_UPDATE_CHECK':'1','ENOLA_NO_PROMPTS':'1'},timeout=5)
    version=json.loads(data);assert version['version']=='0.4.30' and version['extractor_version']=='v351'
    license=ROOT/'.local/cognee-python/lib/python3.12/site-packages/enola_cli-0.4.30.dist-info/licenses/LICENSE'
    assert license.is_file() and 'Apache License' in license.read_text()
    files={'enola':binary,'LICENSE.enola':license}
    manifest={'id':'enola-code','version':'0.4.30-v351','platform':'darwin-arm64','protocol_min':1,'protocol_max':1,'executable':'enola','code_recipe':'enola-0.4.30-v351','license':'Apache-2.0','files':[{'path':name,'bytes':path.stat().st_size,'sha256':hashlib.sha256(path.read_bytes()).hexdigest()} for name,path in files.items()]}
    out=args.out.resolve();out.mkdir(parents=True,exist_ok=True);archive=out/'memlane-enola-code-0.4.30-v351-darwin-arm64.tar.gz';meta=(json.dumps(manifest,indent=2)+'\n').encode()
    with archive.open('wb') as raw,gzip.GzipFile(fileobj=raw,mode='wb',mtime=0,filename='',compresslevel=9) as compressed,tarfile.open(fileobj=compressed,mode='w') as tar:
        for name,path in files.items():
            info=tarfile.TarInfo(name);info.size=path.stat().st_size;info.mode=0o755 if name=='enola' else 0o644
            with path.open('rb') as source:tar.addfile(info,source)
        info=tarfile.TarInfo('component.json');info.size=len(meta);info.mode=0o644;tar.addfile(info,io.BytesIO(meta))
    report={'archive':str(archive),'archive_bytes':archive.stat().st_size,'unpacked_file_bytes':sum(x['bytes'] for x in manifest['files'])+len(meta),'sha256':hashlib.sha256(archive.read_bytes()).hexdigest(),'component':manifest,'python_runtime_included':False,'model_downloaded':False,'signing_notarization':'NOT PASSED'}
    (out/'extractor-sizes.json').write_text(json.dumps(report,indent=2)+'\n');print(json.dumps(report,indent=2))

if __name__=='__main__':main()
