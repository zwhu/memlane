#!/usr/bin/env python3
"""Read-only state receipts for this marked, temporary initialization run."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import socket
import uuid

def main():
    parser=argparse.ArgumentParser(description=__doc__);parser.add_argument('--out',required=True,type=Path);parser.add_argument('--stage',required=True);parser.add_argument('--index',action='append',default=[]);args=parser.parse_args()
    out=args.out.resolve();info=json.loads((out/'source-info.json').read_text());config=json.loads((out/'runtime-config.json').read_text());scratch=Path(info['root']).resolve();root=Path(config['root'])
    assert info['synthetic_only'] and info['identifier'].startswith(('io.github.zwhu.memlane.initialization.', 'io.github.zwhu.memlane.concentrated20261111'))
    assert scratch.name=='initialization' and scratch.parent.name.startswith('memlane-task-') and root.resolve()==scratch/('client-responsive' if info.get('variant')=='responsive' else 'client')
    assert json.loads((scratch/'synthetic-only.json').read_text())['synthetic_only'] and re.fullmatch('[a-z0-9-]+',args.stage)
    data=root/'data';address=Path('/tmp')/f'memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}'/'service.sock'
    def read(op,**fields):
        with socket.socket(socket.AF_UNIX) as c:
            c.settimeout(20);c.connect(str(address));c.sendall(json.dumps(dict(protocol=1,id=str(uuid.uuid4()),op=op,**fields)).encode()+b'\n');reply=json.loads(c.makefile('rb').readline(4*1024*1024))
        assert reply['ok'],reply
        return reply['result']
    report={'synthetic_only':True,'stage':args.stage,'health':read('health'),'batch':read('agent_connection_batch'),'connections':read('connections'),'scopes':read('scopes'),'processing':read('processing_get'),'totals':{kind:read('list',list_query=dict(kind=kind,limit=1))['total'] for kind in ['knowledge','raw']}}
    report['records']={kind:read('list',list_query=dict(kind=kind,limit=50)) for kind in ['knowledge','raw']}
    report['histories']={v['memory']:read('history',memory=v['memory'],limit=50) for v in report['records']['knowledge']['items'][:5]}
    report['validation_status']={k:read('processing_validation',processing_validation_id=v['validation']['validation']) for k,v in report['processing']['saved'].items() if v.get('validation',{}).get('validation')}
    project_tasks=read('project_task_list',project_task_list=dict(limit=10))
    report['project_tasks']=[read('project_task',project_task=item['task']) for item in project_tasks.get('items',[])]
    report['plans']=read('schedules')
    report['plan_runs']={plan['plan']:read('schedule_runs',schedule=plan['plan']) for plan in report['plans']}
    organizations=read('organization_list',organization_list=dict(limit=10))
    report['organizations']=[read('organization_get',organization_task=item['task']) for item in organizations.get('items',[])]
    report['home_activity']=read('home_activity',home_activity=dict(limit=10))
    report['home_reviews']=read('home_review',home_review=dict(limit=10))
    report['index_rebuilds']={str(uuid.UUID(identifier)):read('index_rebuild',index_rebuild_id=str(uuid.UUID(identifier))) for identifier in args.index}
    first=root/'first-launch.json'
    if first.exists():report['first_launch']=json.loads(first.read_text())
    report['pi_source_unchanged']={}
    original=Path.home()/'.pi/agent'
    for name,before in json.loads((out/'native-pi-source-before.json').read_text()).items():report['pi_source_unchanged'][name]=hashlib.sha256((original/name).read_bytes()).hexdigest()==before
    with (out/('state-'+args.stage+'.json')).open('x') as f:json.dump(report,f,ensure_ascii=False,indent=2);f.write('\n')
    assert all(report['pi_source_unchanged'].values()), 'original pi configuration changed; see snapshot'
    print(json.dumps({'stage':args.stage,'totals':report['totals'],'pi_source_unchanged':report['pi_source_unchanged']}))

if __name__=='__main__':main()
