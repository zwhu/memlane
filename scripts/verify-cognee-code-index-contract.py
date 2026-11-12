#!/usr/bin/env python3
"""Actual fixed SDK code writes/refresh in temporary synthetic storage only."""
import argparse
import asyncio
import hashlib
import json
import os
from pathlib import Path
import shutil
import sys
import tempfile
from uuid import uuid4

ROOT=Path(__file__).resolve().parents[1]


async def exercise(root,progress):
    import httpx
    from fastapi import FastAPI
    from unittest.mock import patch
    from cognee.infrastructure.databases.relational import create_db_and_tables
    from cognee.modules.users.methods import get_default_user
    from cognee.modules.data.methods.create_authorized_dataset import create_authorized_dataset
    from cognee.context_global_variables import set_database_global_context_variables
    from cognee.infrastructure.databases.graph import get_graph_engine
    from cognee.infrastructure.llm.LLMGateway import LLMGateway
    import cognee_code_guard as guard
    import cognee_code_index as index
    guard.install()
    await create_db_and_tables()
    user=await get_default_user()
    dataset=await create_authorized_dataset('synthetic_code_index_contract',user)
    scratch=root/'job';scratch.mkdir(mode=0o700)
    repository=scratch/'synthetic-repository';repository.mkdir(mode=0o700)
    sources={'package.json':'{"name":"synthetic-repository","version":"1.0.0"}',
        'helper.ts':'export function greet(name: string) { return "Hello " + name; }\n',
        'index.ts':'import {greet} from "./helper"; export function main() { return greet("world"); }\n',
        'obsolete.py':'def retired_symbol():\n    return 1\n'}
    for name,body in sources.items():(repository/name).write_text(body)
    binary=(ROOT/'.local/cognee-python/bin/enola').resolve(strict=True)
    binary_sha=hashlib.sha256(binary.read_bytes()).hexdigest()
    identity={}
    async def authorize(operation):
        assert operation==identity['operation']
        return dict(identity,repository=str(repository),scratch=str(scratch),binary=str(binary),binary_sha256=binary_sha,deny_reads=[str(root/'system'),str(root/'data')])
    app=FastAPI();instance=str(uuid4())
    epoch=index.install(app,instance,lambda:user,authorize)
    def private(payload):
        if payload.get('owner')!='synthetic-private-owner':raise PermissionError()
    async def graph():
        async with set_database_global_context_variables(dataset.id,dataset.owner_id):
            engine=await get_graph_engine()
            counts=await engine.get_graph_counts()
            data=await engine.get_graph_data()
            symbol_rows=await engine.query('MATCH (n:Node) WHERE n.type = $type RETURN n.name',params={'type':'CodeSymbol'})
        symbols=[props.get('name','') for _,props in data[0] if props.get('type')=='CodeSymbol']
        queried=[str(row[0]) for row in symbol_rows]
        assert sorted(symbols)==sorted(queried),'typed graph symbols and query differ'
        residual=[{'type':props.get('type'),'name':props.get('name'),'matching_fields':[k for k,v in props.items() if 'retired_symbol' in json.dumps(v,default=str)]}
                  for _,props in data[0] if 'retired_symbol' in json.dumps(props,default=str)]
        return counts,symbols,residual
    results=[];progress['runs']=results
    with patch.object(index,'require_owner',private),patch.object(LLMGateway,'acreate_structured_output',side_effect=AssertionError('code graph must not call LLM')) as llm:
        async with httpx.AsyncClient(transport=httpx.ASGITransport(app=app),base_url='http://synthetic') as client:
            for iteration in range(2):
                if iteration:
                    (repository/'obsolete.py').unlink()
                    (repository/'replacement.py').write_text('def replacement_symbol():\n    return 2\n')
                    for name in ('.enola',):shutil.rmtree(repository/name)
                    for name in ('home','tmp'):shutil.rmtree(scratch/name)
                digest=hashlib.sha256()
                for path in sorted(repository.iterdir()):digest.update(path.name.encode()+b'\x00'+path.read_bytes())
                identity.clear();identity.update(operation=str(uuid4()),instance=instance,dataset=str(dataset.id),user=str(user.id),source_digest=digest.hexdigest(),execution_epoch=epoch)
                body=dict(identity,owner='synthetic-private-owner')
                response=await client.post('/api/v1/memlane/code/index',json=body)
                assert response.status_code==200,(response.status_code,response.text)
                result=response.json()
                assert result['status']=='completed',result
                counts,symbols,residual=await graph()
                results.append(dict(result,graph_nodes=counts[0],graph_edges=counts[1],queried_symbols=symbols,retired_text_locations=residual))
                assert counts[0]>0 and counts[1]>0,counts
                assert any('greet' in name for name in symbols)
                if iteration:
                    assert not any('retired_symbol' in name for name in symbols),'deleted symbol remains queryable'
                    assert any('replacement_symbol' in name for name in symbols),'replacement symbol missing'
                else:assert any('retired_symbol' in name for name in symbols)
                replay=await client.post('/api/v1/memlane/code/index',json=body)
                assert replay.status_code==200 and replay.json()==result
            assert results[0]['data_id']==results[1]['data_id'],'stable source path created another official repository row'
            assert results[0]['pipeline_run_id']!=results[1]['pipeline_run_id']
        assert llm.call_count==0
    return {'passed':True,'runs':results,'deleted_symbol_removed':True,'replacement_symbol_present':True,'stable_repository_data_id':True,'llm_calls':0,'actual_sdk_graph_writes':True,'actual_daily_project':False}


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--synthetic-only',action='store_true',required=True)
    parser.add_argument('--out',type=Path,required=True)
    args=parser.parse_args()
    args.out=args.out.resolve()
    from coding_memory_recipe import prepare_source
    source,verified=prepare_source('service',ROOT/'.cache/coding-memory-components')
    result={'passed':False,'synthetic_only':True,'source_commit':source.name.removeprefix('service-'),'verified_source_files':len(verified)}
    with tempfile.TemporaryDirectory(prefix='memlane-official-code-index-') as temporary:
        root=Path(temporary).resolve()
        for name in ('home','system','data','cache','logs'):(root/name).mkdir(mode=0o700)
        # Same native JSON resource as the component builder, alongside byte-
        # verified worker source. No network installation or source-tree edits.
        overlay=root/'sdk-overlay';overlay.mkdir()
        shutil.copytree(source/'cognee_db_workers',overlay/'cognee_db_workers',ignore=shutil.ignore_patterns('__pycache__','*.pyc'))
        extension=ROOT/'.local/cognee-python/lib/python3.12/site-packages/cognee_db_workers/ladybug_extensions/v0.19.0/osx_arm64/libjson.lbug_extension'
        assert hashlib.sha256(extension.read_bytes()).hexdigest()=='e6482a025de31d662ee4b32a39c991706b8cc3c2ec49673ef5d8ce45cc23ce7a'
        dest=overlay/'cognee_db_workers/ladybug_extensions/v0.19.0/osx_arm64/libjson.lbug_extension'
        dest.parent.mkdir(parents=True,exist_ok=True);shutil.copyfile(extension,dest)
        paths=[str(overlay),str(source),str(ROOT/'engine/python')]
        os.environ.clear()
        os.environ.update(HOME=str(root/'home'),PATH='/usr/bin:/bin',PYTHONPATH=os.pathsep.join(paths),
            SYSTEM_ROOT_DIRECTORY=str(root/'system'),DATA_ROOT_DIRECTORY=str(root/'data'),CACHE_ROOT_DIRECTORY=str(root/'cache'),
            COGNEE_LOGS_DIR=str(root/'logs'),LOGS_ROOT_DIRECTORY=str(root/'logs'),COGNEE_LOG_FILE='false',
            TELEMETRY_DISABLED='true',COGNEE_TRACING_ENABLED='false',PYTHON_DOTENV_DISABLED='1',HF_HUB_OFFLINE='1',
            LITELLM_LOCAL_MODEL_COST_MAP='true',KUZU_BUFFER_POOL_SIZE='67108864')
        os.chdir(root);sys.path[:0]=paths
        sessions=[]
        try:
            result.update(asyncio.run(exercise(root,result)))
        finally:
            from cognee_db_workers import harness
            sessions=list(harness._all_sessions)
            for session in sessions:
                session.shutdown(timeout=2)
                assert not session._proc.is_alive(),'owned SDK worker still alive'
            result['owned_worker_sessions_reaped']=len(sessions)
            args.out.parent.mkdir(parents=True,exist_ok=True)
            args.out.write_text(json.dumps(result,ensure_ascii=False,indent=2)+'\n')
    print(json.dumps(result))


if __name__=='__main__':main()
