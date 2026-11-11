#!/usr/bin/env python3
"""Create a disposable Native copy with isolated Agent configuration paths."""
import argparse
import hashlib
import json
from pathlib import Path
import plistlib
import re
import shutil
import subprocess

ROOT=Path(__file__).resolve().parents[1]

def main():
    parser=argparse.ArgumentParser(description=__doc__);parser.add_argument('--out',required=True,type=Path);parser.add_argument('--app-name');parser.add_argument('--reuse-pi',action='store_true');parser.add_argument('--capture-native',action='store_true');args=parser.parse_args()
    out=args.out.resolve();info=json.loads((out/'source-info.json').read_text());scratch=Path(info['root']).resolve()
    assert info['synthetic_only'] and scratch.name=='initialization' and scratch.parent.name.startswith('memlane-task-')
    assert json.loads((scratch/'synthetic-only.json').read_text())['synthetic_only']
    variant=info.get('variant','primary');assert variant in ['primary','responsive']
    pi=scratch/('pi' if variant=='primary' else 'pi-responsive');original=Path.home()/'.pi/agent'
    if args.reuse_pi:
        assert pi.is_dir() and (pi/'settings.json').is_file(), 'reuse requires the existing marked pi directory'
    else:
        pi.mkdir(exist_ok=False)
        selected=json.loads((original/'settings.json').read_text())
        (pi/'settings.json').write_text(json.dumps({k:selected[k] for k in ['defaultProvider','defaultModel'] if k in selected}))
    before={}
    for name in ['settings.json','models.json','auth.json']:
        source=original/name
        if source.is_file():before[name]=hashlib.sha256(source.read_bytes()).hexdigest()
        if not args.reuse_pi and name!='settings.json' and source.is_file():(pi/name).symlink_to(source)
    (out/'native-pi-source-before.json').write_text(json.dumps(before,indent=2))
    codex=scratch/('codex' if variant=='primary' else 'codex-responsive')
    codex_marker=codex/'memlane-native-synthetic.json'
    if codex.exists():
        assert codex.is_dir() and not codex.is_symlink() and json.loads(codex_marker.read_text()).get('synthetic_only') is True, 'refuse an unmarked Codex directory'
    else:
        codex.mkdir(mode=0o700)
        codex_marker.write_text(json.dumps({'synthetic_only':True,'purpose':'isolated Native computer connection verification'}))
    if args.app_name:assert re.fullmatch(r'[A-Za-z0-9_-]{1,80}',args.app_name), 'simple application name required'
    name=args.app_name or 'MemlaneInitialization'
    app=scratch/(name+'.app' if args.app_name or variant=='primary' else 'MemlaneInitialization-responsive.app');shutil.copytree(out/('darwin-arm64/'+name+'.app'),app,symlinks=True)
    plist=app/'Contents/Info.plist';metadata=plistlib.loads(plist.read_bytes());environment=dict(metadata.get('LSEnvironment',{}));environment['PI_CODING_AGENT_DIR']=str(pi)
    environment['CODEX_HOME']=str(codex)
    if args.capture_native:environment['MEMLANE_NATIVE_CAPTURE_DIR']=str(out/'native')
    metadata['LSEnvironment']=environment;plist.write_bytes(plistlib.dumps(metadata))
    subprocess.run(['codesign','--force','--sign','-','--timestamp=none',str(app)],check=True)
    subprocess.run(['codesign','--verify','--deep','--strict',str(app)],check=True)
    (out/'native-launch.json').write_text(json.dumps({'app':str(app),'pi_directory':str(pi),'codex_directory':str(codex),'codex_configuration_isolated':True,'codex_auth_copied':False,'original_pi_auth_link_only':True,'user_configuration_modified':False},indent=2))
    print('disposable Native copy prepared',flush=True)

if __name__=='__main__':main()
