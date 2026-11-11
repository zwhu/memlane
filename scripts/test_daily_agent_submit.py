"""Synthetic contract-only tests. No Agent, model, daemon or user files."""
import copy
import importlib.util
from pathlib import Path
import tempfile
import sqlite3
import json
import unittest
import uuid
from unittest.mock import patch

spec=importlib.util.spec_from_file_location('daily_submit',Path(__file__).with_name('verify-daily-agent-submit.py'))
h=importlib.util.module_from_spec(spec);spec.loader.exec_module(h)

class SubmitContracts(unittest.TestCase):
    def setUp(self):
        project=str(uuid.uuid4());operation='daily-submit-'+uuid.uuid4().hex
        payload=dict(scope=project,operation_id=operation,title='Synthetic new knowledge',text='Only the synthetic project.',origin='knowledge',source=dict(fragment='Memlane synthetic daily submission'))
        self.plan=dict(project=project,payload=payload,operation_id=operation,extension='/synthetic/real-installed.ts',expected_order=['memory_save','memory_save','memory_status'])
        self.memory=str(uuid.uuid4());record=dict(memory=self.memory,dataset=project,revision=1,origin='knowledge',state='active',title=payload['title'],text=payload['text'],index_state='queued')
        def call(tool,args,result):return dict(tool=tool,arguments=args,response=dict(protocol_version=1,ok=True,result=result))
        first=dict(persisted=True,saved_revision=1,record=record)
        second=copy.deepcopy(first);second['record']['index_state']='indexed'
        self.calls=[call('memory_save',copy.deepcopy(payload),first),call('memory_save',copy.deepcopy(payload),second),call('memory_status',dict(scope=project,memory_id=self.memory),copy.deepcopy(second['record']))]

    def test_actual_save_receipt_binds_status_and_dynamic_index_is_allowed(self):
        self.assertEqual(self.memory,h.verify_calls(self.calls,self.plan))

    def test_confirmed_delivery_is_not_a_new_save_receipt(self):
        calls=copy.deepcopy(self.calls)
        delivery=dict(id='d'*64,state='confirmed',persisted_confirmed=True,local_retained=True,memory=self.memory,saved_revision=1,request_digest='a'*64,review_digest='b'*64)
        calls[0]['response']['delivery']=copy.deepcopy(delivery)
        calls[1]['response']=dict(protocol_version=1,ok=False,error=dict(code='delivery_already_confirmed'),delivery=copy.deepcopy(delivery))
        self.assertEqual(self.memory,h.verify_calls(calls,self.plan))
        self.assertNotIn('result',calls[1]['response'])
        for field,value in [('memory',str(uuid.uuid4())),('saved_revision',2),('persisted_confirmed',False),('review_digest','c'*64),('state','sending')]:
            wrong=copy.deepcopy(calls);wrong[1]['response']['delivery'][field]=value
            with self.assertRaises(ValueError):h.verify_calls(wrong,self.plan)

    def test_continuation_binds_only_original_confirmed_save(self):
        failed=dict(plan=self.plan,run_diagnostics=dict(tool_events=[dict(event='tool_execution_start',tool='memory_save',call_id='actual',scope_matches=True,fixed_payload_matches=True),dict(event='tool_execution_end',tool='memory_save',call_id='actual',ok=True,persisted=True,revision=1,actual_memory=self.memory)]))
        self.assertEqual(self.memory,h.confirmed_source_memory(failed,self.plan))
        wrong=copy.deepcopy(failed);wrong['run_diagnostics']['tool_events'][1]['persisted']=False
        with self.assertRaisesRegex(ValueError,'actual_original_save_confirmation'):h.confirmed_source_memory(wrong,self.plan)
        wrong=copy.deepcopy(failed);wrong['plan']['operation_id']='new-operation'
        with self.assertRaisesRegex(ValueError,'original_paid_intent_mismatch'):h.confirmed_source_memory(wrong,self.plan)

    def test_old_library_or_changed_spool_payload_cannot_confirm_continuation(self):
        with tempfile.TemporaryDirectory() as temporary:
            root=Path(temporary);(root/'agent-outbox').mkdir()
            plan=dict(self.plan,socket_root=str(root),data=str(root),workspace=str(root/'workspace'),connection=str(uuid.uuid4()),generation=2)
            with sqlite3.connect(root/'memlane.sqlite') as db:
                db.execute('CREATE TABLE cognee_library_identity(id INTEGER PRIMARY KEY,identity TEXT)');db.execute("INSERT INTO cognee_library_identity VALUES(1,'original-library')")
            entry=dict(id='d'*64,state='confirmed',saved_revision=1,memory=self.memory,request_digest='a'*64,digest='b'*64,attempts=1,payload=dict(connection=plan['connection'],generation=2,root=str(root),workspace=plan['workspace'],arguments=plan['payload'],grant=dict(library_id='original-library',connection_id=plan['connection'],generation=2,scope=plan['project'])))
            path=root/'agent-outbox/ledger.json';path.write_text(json.dumps(dict(entries=[entry])));path.chmod(0o600)
            self.assertEqual(1,h.confirmed_entry(plan,self.memory)['attempts'])
            with sqlite3.connect(root/'memlane.sqlite') as db:db.execute("UPDATE cognee_library_identity SET identity='restored-library'")
            with self.assertRaisesRegex(ValueError,'library_or_grant_changed'):h.confirmed_entry(plan,self.memory)
            entry['payload']['arguments']=dict(plan['payload'],text='changed');path.write_text(json.dumps(dict(entries=[entry])))
            with self.assertRaisesRegex(ValueError,'original_payload_changed'):h.confirmed_entry(plan,self.memory)

    def test_text_or_unconfirmed_save_cannot_replace_real_tool_receipt(self):
        with self.assertRaisesRegex(ValueError,'exact_submit_tool_order'):h.verify_calls([],self.plan)
        calls=copy.deepcopy(self.calls);calls[0]['response']['result']['persisted']=False
        with self.assertRaisesRegex(ValueError,'save_not_confirmed'):h.verify_calls(calls,self.plan)

    def test_changed_operation_scope_text_or_replayed_id_is_rejected(self):
        for mutate in [lambda c:c[1]['arguments'].update(operation_id='other'),lambda c:c[0]['arguments'].update(scope=str(uuid.uuid4())),lambda c:c[0]['response']['result']['record'].update(text='changed'),lambda c:c[1]['response']['result']['record'].update(memory=str(uuid.uuid4()))]:
            calls=copy.deepcopy(self.calls);mutate(calls)
            with self.assertRaises(ValueError):h.verify_calls(calls,self.plan)

    def test_status_cannot_use_model_invented_id_or_wrong_version(self):
        calls=copy.deepcopy(self.calls);calls[2]['arguments']['memory_id']=str(uuid.uuid4())
        with self.assertRaisesRegex(ValueError,'actual_save_id'):h.verify_calls(calls,self.plan)
        calls=copy.deepcopy(self.calls);calls[2]['response']['result']['revision']=2
        with self.assertRaisesRegex(ValueError,'status_receipt_mismatch'):h.verify_calls(calls,self.plan)

    def test_wrapper_delegates_only_save_status_and_never_creates_an_id(self):
        source=h.wrapper_source(self.plan,self.plan['operation_id'],'')
        self.assertIn('import installed from "/synthetic/real-installed.ts"',source)
        self.assertIn('await execute(id,args,signal,update,ctx)',source)
        self.assertIn('saved=record.memory',source)
        self.assertNotIn('randomUUID',source)
        for tool in ['memory_search','memory_get','memory_adopt','memory_initialize']:self.assertNotIn(tool,source)

    def test_local_operation_plan_is_exclusive_not_automatically_regenerated(self):
        with tempfile.TemporaryDirectory() as temporary:
            path=Path(temporary)/'plan.json';h.write_json_exclusive(path,self.plan)
            with self.assertRaises(FileExistsError):h.write_json_exclusive(path,dict(operation_id='replacement'))
        with patch.object(h.base.subprocess,'Popen',side_effect=AssertionError('no model in contracts')):
            self.assertEqual(self.memory,h.verify_calls(self.calls,self.plan))

if __name__=='__main__':unittest.main()
