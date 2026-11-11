"""Synthetic contracts only; no installed runtime/model/graph calls."""
import asyncio
import hashlib
import os
import importlib.util
from pathlib import Path
import sys
from types import ModuleType, SimpleNamespace
import unittest
from unittest.mock import patch
from uuid import uuid4

spec = importlib.util.spec_from_file_location('material_submission', Path(__file__).resolve().parents[1] / 'engine/python/cognee_submission.py')
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


def fixture():
    m = {k: str(uuid4()) for k in ('library', 'operation', 'instance', 'dataset', 'project', 'memory')}
    m.update(revision=1, origin='knowledge', text='https://must-remain-literal.invalid/private')
    m['processing']={'revision':{'config_digest':'b'*64},'dependency_digest':'a'*64}
    m['digest'] = hashlib.sha256(m['text'].encode()).hexdigest()
    return m, {'endpoint': 'http://127.0.0.1:12345/v1', 'token': 'synthetic-task-token'}


def fake_modules(bindings):
    out = {}
    for name, attrs in bindings.items():
        parts = name.split('.')
        for i in range(1, len(parts)+1):
            key = '.'.join(parts[:i])
            if key not in out:
                out[key] = ModuleType(key)
                out[key].__path__ = []
        for key, value in attrs.items(): setattr(out[name], key, value)
    return out


class Completed(SimpleNamespace):
    pass


class SubmissionContracts(unittest.TestCase):
    def test_identity_digest_and_private_gateway(self):
        m, g = fixture()
        module.validate({'material': m, 'gateway': g}, m['instance'])
        for key, value in [('endpoint', 'https://provider.invalid/v1'), ('endpoint', 'http://127.0.0.1:123/v1?key=x'), ('token', 'bad\nsecret')]:
            bad = dict(g); bad[key] = value
            with self.assertRaises(ValueError): module.validate({'material':m,'gateway':bad},m['instance'])
        for key, value in [('revision', 2.0), ('digest', 'bad'), ('instance', str(uuid4())), ('origin', 'imported')]:
            bad = dict(m);bad[key] = value
            with self.assertRaises(ValueError):module.validate({'material':bad,'gateway':g},m['instance'])

    def test_document_lineage_is_per_library_and_version_not_operation(self):
        m,_ = fixture();first=module.document_id(m)
        replay=dict(m);replay['operation']=str(uuid4());self.assertEqual(first,module.document_id(replay))
        for key,value in [('revision',2),('library',str(uuid4())),('project',str(uuid4())),('processing',{'revision':{'config_digest':'c'*64},'dependency_digest':'a'*64})]:
            changed=dict(m);changed[key]=value;self.assertNotEqual(first,module.document_id(changed))

    def test_regular_completion_rejects_wrong_item_dataset_skip_and_hook_error(self):
        import copy
        dataset_id, identity = uuid4(), uuid4()
        item = Completed(status='PipelineRunCompleted', dataset_id=dataset_id, after_run_error=None)
        run = Completed(status='PipelineRunCompleted', dataset_id=dataset_id, after_run_error=None,
                        data_ids=[identity], data_ingestion_info=[{'run_info': item, 'data_id': identity}])
        modules = fake_modules({'cognee.modules.pipelines.models.PipelineRunInfo': {'PipelineRunCompleted': Completed}})
        with patch.dict(sys.modules, modules):
            module.require_completed_material_run({dataset_id: run}, dataset_id, identity)
            for change in ('dataset', 'ids', 'extra_item', 'skip', 'hook', 'item_id', 'item_dataset'):
                bad = copy.deepcopy(run)
                if change == 'dataset': bad.dataset_id = uuid4()
                if change == 'ids': bad.data_ids = [uuid4()]
                if change == 'extra_item': bad.data_ingestion_info *= 2
                if change == 'skip': bad.data_ingestion_info[0]['run_info'].status = 'PipelineRunAlreadyCompleted'
                if change == 'hook': bad.after_run_error = 'synthetic hook failure'
                if change == 'item_id': bad.data_ingestion_info[0]['data_id'] = uuid4()
                if change == 'item_dataset': bad.data_ingestion_info[0]['run_info'].dataset_id = uuid4()
                with self.assertRaises(ValueError, msg=change):
                    module.require_completed_material_run({dataset_id: bad}, dataset_id, identity)
            with self.assertRaises(ValueError):
                module.require_completed_material_run({uuid4(): run}, dataset_id, identity)

    def test_official_pipeline_only_exact_data_and_owner_model(self):
        m,g=fixture();record=SimpleNamespace(id=module.document_id(m),mime_type='text/plain',pipeline_status={},external_metadata=None,raw_data_location='owned-storage')
        records=[];calls=[];configs=[]
        async def dataset(**kw):return 'authenticated-user',SimpleNamespace(name='authorized')
        async def listing(*args,**kwargs):self.assertEqual(kwargs['limit'],4097);return list(records)
        async def migrated(*args):pass
        async def tasks(**kwargs):
            self.assertEqual(kwargs['summary_method'],'from_extraction')
            self.assertIsInstance(kwargs['calculate_chunk_graphs'],Grounded)
            return ['official-cognify-tasks']
        class Grounded:
            def __init__(self,*args):pass
            async def publish_completed(self,runs):calls.append({'pipeline_name':'audit-publish'})
            def mark_failed(self,code):raise AssertionError('unexpected failed pipeline')
        async def pipeline(**kw):
            calls.append(kw)
            self.assertEqual(len(kw['data']),1)
            self.assertEqual(kw['datasets'],[module.UUID(m['dataset'])])
            self.assertTrue(kw['skip_connection_test'])
            self.assertFalse(kw['data_cache'])
            if kw['pipeline_name']=='add_pipeline':
                item=kw['data'][0]
                self.assertEqual(item.data,m['text'])
                self.assertTrue(item.literal_text)
                self.assertEqual(item.data_id,record.id)
                record.external_metadata=item.external_metadata;records.append(record)
            else:
                self.assertIs(kw['data'][0],record)
                self.assertFalse(kw['incremental_loading'])
                self.assertEqual(kw['rollback_handler'],'official-rollback')
                self.assertEqual(kw['after_items_ran'],'official-reconcile')
                record.pipeline_status={'add_pipeline':{m['dataset']:'completed'}}
            one=Completed(status='PipelineRunCompleted',dataset_id=module.UUID(m['dataset']),after_run_error=None)
            return {module.UUID(m['dataset']):Completed(status='PipelineRunCompleted',dataset_id=module.UUID(m['dataset']),after_run_error=None,data_ids=[record.id],data_ingestion_info=[{'run_info':one,'data_id':record.id}])}
        class File:
            async def __aenter__(self):return self
            async def __aexit__(self,*args):pass
            def read(self,*args):return m['text']
        def config(**kwargs):configs.append(kwargs);return kwargs
        modules=fake_modules({
          'cognee.modules.data.methods':{'get_dataset_data':listing},
          'cognee.modules.pipelines':{'Task':lambda *a,**kw:(a,kw),'run_pipeline':'official-driver'},
          'cognee.modules.pipelines.layers.pipeline_execution_mode':{'run_pipeline_blocking':pipeline},
          'cognee.modules.migrations.startup':{'run_migrations_and_block':migrated},
          'cognee.modules.pipelines.layers.resolve_authorized_user_dataset':{'resolve_authorized_user_dataset':dataset},
          'cognee.tasks.ingestion':{'resolve_data_directories':'official-resolve','ingest_data':'official-ingest'},
          'cognee.tasks.ingestion.data_item':{'DataItem':lambda **kw:SimpleNamespace(**kw)},
          'cognee.infrastructure.llm.config':{'LLMConfig':config},
          'cognee.api.v1.cognify.cognify':{'get_default_tasks':tasks,'raise_if_cognify_errored':lambda result:None},
          'cognee.modules.pipelines.models.DataItemStatus':{'is_data_item_completed':lambda value:value=='completed'},
          'cognee.modules.pipelines.models.PipelineRunInfo':{'PipelineRunCompleted':Completed},
          'cognee.infrastructure.files.utils.open_data_file':{'open_data_file':lambda *a,**kw:File()},
          'cognee_processing_adapter':{'install':lambda:None},
          'cognee_grounded_graph':{'MaterialGroundedExtractor':Grounded},
          'cognee.modules.cognify.rollback':{'cognify_rollback_handler':'official-rollback'},
          'cognee.tasks.ingestion.document_structure':{'reconcile_document_structure':'official-reconcile'},
          'cognee.infrastructure.databases.vector.compact_vector_store':{'compact_vector_store':'official-compact'}})
        with patch.dict(sys.modules,modules),patch.dict(os.environ,{'SYSTEM_ROOT_DIRECTORY':'/tmp/synthetic-owner'}):result=asyncio.run(module.submit_official(m,g,'authenticated-user'))
        self.assertEqual([c['pipeline_name'] for c in calls],['add_pipeline','cognify_pipeline','audit-publish'])
        self.assertEqual(result['document'],str(record.id));self.assertEqual(result['state'],'processed')
        self.assertEqual(configs[0]['llm_endpoint'],g['endpoint']);self.assertEqual(configs[0]['llm_args'],{})
        for stage in ('extraction','summarization','query'):self.assertEqual(configs[0]['llm_'+stage+'_endpoint'],g['endpoint'])


if __name__=='__main__':unittest.main()
