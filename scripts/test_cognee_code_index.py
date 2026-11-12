"""Actual ASGI lifecycle with a synthetic official SDK; no graph/model/scan."""
import asyncio
import json
import os
from pathlib import Path
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch
from uuid import uuid4

sys.path.insert(0,str(Path(__file__).resolve().parents[1]/'engine/python'))
import cognee_code_index as module
import cognee_code_guard as guard
from cognee_code_process import CodeProcessError
from test_cognee_submission import fake_modules


class Tests(unittest.IsolatedAsyncioTestCase):
    async def scenario(self,case):
        from fastapi import FastAPI
        import httpx
        instance,dataset,user_id=map(str,(uuid4(),uuid4(),uuid4()))
        user=SimpleNamespace(id=uuid4());user.id=__import__('uuid').UUID(user_id)
        calls=[];started=asyncio.Event();stopped=asyncio.Event();release=asyncio.Event()
        permitted=True
        preflights=0;both=asyncio.Event()
        identity={}
        async def authorize(op):
            nonlocal preflights
            if case=='preflight_duplicate':
                preflights+=1
                if preflights==2:both.set()
                if preflights<=2:await both.wait()
            if not permitted or case=='owner_denied':raise PermissionError('PRIVATE_OWNER_DIAGNOSTIC')
            return dict(identity,repository='/synthetic/job/repo',scratch='/synthetic/job',
                binary='/synthetic/enola',binary_sha256='a'*64,deny_reads=['/synthetic/source'])
        async def authorized(ids,permission,principal,strict):
            self.assertEqual(ids,[__import__('uuid').UUID(dataset)]);self.assertEqual(permission,'write')
            self.assertIs(principal,user);self.assertTrue(strict)
            if case=='dataset_denied':raise PermissionError('PRIVATE_PERMISSION_DIAGNOSTIC')
            return [SimpleNamespace(id=ids[0])]
        async def data(dataset_id,limit):
            self.assertEqual(limit,2)
            return [SimpleNamespace(path=Path('/other/repo'))] if case=='foreign_repo' else []
        async def remember(repository,**kwargs):
            calls.append(True);started.set()
            value=guard._execution.get()
            self.assertIsNotNone(value);self.assertEqual(value.repository,repository)
            self.assertEqual(kwargs,dict(dataset_id=__import__('uuid').UUID(dataset),user=user,content_type='code',run_in_background=False,index_vectors=False,raise_on_error=False))
            await guard.require(repository=repository,dataset=dataset,user=user)
            try:
                if case in ('cancel','revoke','duplicate','preflight_duplicate'):await release.wait()
                if case=='reap_unknown':raise CodeProcessError('code_process_reap_unconfirmed')
                result=dict(status='completed',dataset_id=dataset,items_processed=1,items=[dict(kind='code_repository',id=str(uuid4()),pipeline_run_id=str(uuid4()),source='PRIVATE_SOURCE')])
                if case=='failed':result.update(status='errored',error='PRIVATE_SDK_DIAGNOSTIC');result['items'][0].update(status='errored',error='PRIVATE_ITEM_DIAGNOSTIC')
                if case=='wrong_result_scope':result['dataset_id']=str(uuid4())
                return result
            finally:stopped.set()
        def require_owner(payload):
            if not isinstance(payload,dict) or payload.get('owner')!='private-owner':raise PermissionError()
        sdk=fake_modules({'cognee':{'remember':remember},
            'cognee.modules.data.methods':{'get_authorized_existing_datasets':authorized,'get_dataset_data':data},
            'cognee.tasks.code_graph.cluster':{'code_repo_path':lambda row:row.path}})
        with patch.dict(sys.modules,sdk),patch.object(module,'require_owner',require_owner):
            app=FastAPI();epoch=module.install(app,instance,lambda:user,authorize)
            identity.update(operation=str(uuid4()),instance=instance,dataset=dataset,user=user_id,source_digest='b'*64,execution_epoch=epoch)
            body=dict(identity,owner='private-owner')
            async with httpx.AsyncClient(transport=httpx.ASGITransport(app=app),base_url='http://fixture') as client:
                if case in ('wrong_owner','old_epoch','extra_path'):
                    if case=='wrong_owner':body['owner']='other'
                    elif case=='old_epoch':body['execution_epoch']=str(uuid4())
                    else:body['repository']='/request-must-not-choose'
                    response=await client.post('/api/v1/memlane/code/index',json=body)
                    self.assertIn(response.status_code,(403,409));self.assertEqual(calls,[]);return
                if case=='preflight_duplicate':
                    first=asyncio.create_task(client.post('/api/v1/memlane/code/index',json=body))
                    second=asyncio.create_task(client.post('/api/v1/memlane/code/index',json=body))
                    await asyncio.wait_for(started.wait(),5);release.set()
                    replies=await asyncio.wait_for(asyncio.gather(first,second),5)
                    self.assertTrue(all(r.status_code in (200,409) for r in replies))
                    self.assertEqual(len(calls),1,'concurrent preflights triggered duplicate SDK executions')
                    return
                if case in ('cancel','revoke','duplicate'):
                    submitting=asyncio.create_task(client.post('/api/v1/memlane/code/index',json=body))
                    await asyncio.wait_for(started.wait(),5)
                    duplicate=await client.post('/api/v1/memlane/code/index',json=body)
                    self.assertEqual(duplicate.status_code,409)
                    if case=='revoke':permitted=False
                    elif case=='cancel':
                        cancellation=await client.post('/api/v1/memlane/code/index/cancel',json=body)
                        self.assertEqual(cancellation.status_code,200);self.assertTrue(stopped.is_set())
                    else:release.set()
                    response=await asyncio.wait_for(submitting,5)
                    self.assertEqual(response.status_code,200)
                    self.assertEqual(response.json()['status'],'completed' if case=='duplicate' else 'cancelled')
                else:response=await client.post('/api/v1/memlane/code/index',json=body)
                self.assertNotIn('PRIVATE',response.text)
                if case in ('owner_denied','dataset_denied','foreign_repo'):
                    self.assertEqual(response.status_code,403);self.assertEqual(calls,[]);return
                if case=='reap_unknown':
                    self.assertEqual(response.status_code,503)
                    cancellation=await client.post('/api/v1/memlane/code/index/cancel',json=body)
                    self.assertEqual(cancellation.status_code,409)
                    replay=await client.post('/api/v1/memlane/code/index',json=body)
                    self.assertEqual(replay.status_code,409)
                else:
                    self.assertEqual(response.status_code,200)
                    expected='failed' if case=='failed' else 'unconfirmed' if case=='wrong_result_scope' else 'completed'
                    if case not in ('cancel','revoke'):self.assertEqual(response.json()['status'],expected)
                    replay=await client.post('/api/v1/memlane/code/index',json=body)
                    self.assertEqual(replay.status_code,409 if case=='wrong_result_scope' else 200)
                self.assertEqual(len(calls),1,'a replay retriggered the SDK')

    async def test_identity_scope_and_safe_result(self):
        for case in ('normal','wrong_owner','old_epoch','extra_path','owner_denied','dataset_denied','foreign_repo','failed','wrong_result_scope','reap_unknown'):
            with self.subTest(case=case):await self.scenario(case)

    async def test_concurrent_cancel_and_revoke(self):
        for case in ('duplicate','cancel','revoke','preflight_duplicate'):
            with self.subTest(case=case):await self.scenario(case)

    async def test_private_owner_file_and_actual_unix_protocol(self):
        with tempfile.TemporaryDirectory(prefix='mli-',dir='/tmp') as temporary:
            root=Path(temporary).resolve();os.chmod(root,0o700)
            socket=root/'owner.sock';config=root/'owner.json';token='synthetic-private-token-'*3
            requests=[];bad_reply=False
            async def handle(reader,writer):
                q=json.loads(await reader.readline());requests.append(q)
                writer.write(json.dumps({'protocol':1,'id':'wrong' if bad_reply else q['id'],'ok':True,'result':{'synthetic':'grant'}}).encode()+b'\n')
                await writer.drain();writer.close();await writer.wait_closed()
            server=await asyncio.start_unix_server(handle,path=str(socket))
            config.write_text(json.dumps({'protocol':1,'socket':str(socket),'token':token}));config.chmod(0o600)
            try:
                with patch.object(module,'owner_path',lambda:str(config)):
                    module.require_owner({'owner':token})
                    with self.assertRaises(PermissionError):module.require_owner({'owner':'wrong'})
                    operation=str(uuid4())
                    self.assertEqual(await module.authorize_owner(operation),{'synthetic':'grant'})
                    self.assertEqual(requests[0]['op'],'official_code_job_authorize')
                    self.assertEqual(requests[0]['official_code_job'],operation)
                    self.assertEqual(requests[0]['coding_memory_capability'],token)
                    bad_reply=True
                    with self.assertRaisesRegex(PermissionError,'official_code_authorization_unconfirmed'):await module.authorize_owner(operation)
                    config.chmod(0o644)
                    count=len(requests)
                    with self.assertRaises(PermissionError):await module.authorize_owner(operation)
                    self.assertEqual(len(requests),count)
            finally:
                server.close();await server.wait_closed()

    def test_official_result_class_and_unknown_shape(self):
        identity={'operation':str(uuid4()),'instance':str(uuid4()),'dataset':str(uuid4()),'user':str(uuid4()),'source_digest':'a'*64,'execution_epoch':str(uuid4())}
        raw={'status':'completed','dataset_id':identity['dataset'],'items_processed':1,'items':[{'kind':'code_repository','id':str(uuid4()),'pipeline_run_id':str(uuid4())}]}
        result=SimpleNamespace(to_dict=lambda:raw)
        self.assertEqual(module.safe_result(identity,result)['status'],'completed')
        raw['items_processed']=True
        self.assertEqual(module.safe_result(identity,result)['status'],'unconfirmed')


if __name__=='__main__':unittest.main()
