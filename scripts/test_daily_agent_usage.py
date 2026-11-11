"""No model/native/service invocation: contract and receipt checks only."""
import copy
import hashlib
import importlib.util
import json
import plistlib
from pathlib import Path
import tempfile
import unittest
from types import SimpleNamespace
from unittest.mock import patch
import uuid

spec=importlib.util.spec_from_file_location('daily_usage',Path(__file__).with_name('verify-daily-agent-usage.py'))
harness=importlib.util.module_from_spec(spec);spec.loader.exec_module(harness)


class DailyUsageContracts(unittest.TestCase):
    def setUp(self):
        self.plan=dict(project='11111111-1111-4111-8111-111111111111',memory=str(uuid.uuid4()),revision=3,connection='22222222-2222-4222-8222-222222222222',generation=3,target_text_hash=hashlib.sha256(b'synthetic knowledge').hexdigest(),extension='/synthetic/installed/memlane.ts')
        self.query='synthetic knowledge';self.operation='daily-proof-synthetic'
        target=dict(memory=self.plan['memory'],dataset=self.plan['project'],revision=3)
        self.receipts={kind:dict(id=str(uuid.uuid4()),kind=kind,channel='daily',workspace_identity='fixed-workspace',observed_ms=100,targets=[target]) for kind in ['return','read','adopt']}
        read=dict(memory=target['memory'],dataset=target['dataset'],revision=3,text=self.query,usage_receipt=self.receipts['read'])
        search=dict(hits=[dict(version=read)],usage_receipt=self.receipts['return'])
        read_args=dict(scope=target['dataset'],memory_id=target['memory'])
        adoption_args=dict(read_args,revision=3,operation_id=self.operation,exposure_receipt=self.receipts['read']['id'])
        def call(tool,args,result):return dict(tool=tool,arguments=args,response=dict(protocol_version=1,ok=True,result=result))
        self.calls=[call('memory_search',dict(scope=target['dataset'],query=self.query),search),call('memory_get',read_args,read),call('memory_adopt',adoption_args,self.receipts['adopt']),call('memory_adopt',adoption_args,copy.deepcopy(self.receipts['adopt']))]
        self.rows=[dict(id=receipt['id'],kind=kind,channel='daily',connection=self.plan['connection'],generation=3,workspace='fixed-workspace',dataset=target['dataset'],memory=target['memory'],revision=3,observed_ms=100,exposure=self.receipts['read']['id'] if kind=='adopt' else '') for kind,receipt in self.receipts.items()]
        self.before=dict(versions=[(target['memory'],3,'active')],rows=[])
        self.after=dict(versions=list(self.before['versions']),rows=self.rows)

    def test_exact_receipts_and_three_durable_event_kinds(self):
        receipts=harness.verify_calls(self.calls,self.plan,self.operation,self.query)
        self.assertEqual(3,len(harness.verify_durable(self.before,self.after,receipts,self.plan)))

    def test_protocol_proof_cannot_use_legacy_or_old_scope(self):
        connection=dict(id=self.plan['connection'],agent='pi',generation=2,enabled=True,allowed_scopes=[self.plan['project']])
        installation=dict(connection=connection,verified_generation=2)
        with self.assertRaisesRegex(ValueError,'current_protocol_scope_proof_required'):harness.protocol_proof(installation,self.plan['connection'],2,self.plan['project'])
        installation.update(protocol_generation=2,protocol_verified_at='synthetic',protocol_scope_digest=harness.scope_digest(connection))
        self.assertFalse(harness.protocol_proof(installation,self.plan['connection'],2,self.plan['project'])['legacy_readwrite_used'])
        connection['allowed_scopes'].append(str(uuid.uuid4()))
        with self.assertRaisesRegex(ValueError,'current_protocol_scope_proof_required'):harness.protocol_proof(installation,self.plan['connection'],2,self.plan['project'])

    def test_safe_failure_omits_exception_text_and_filenames(self):
        try: raise PermissionError(1, 'do-not-record-secret', '/private/do-not-record-secret')
        except PermissionError as error: result=harness.safe_failure(error,dict(stage='subprocess_popen'))
        self.assertEqual(1,result['errno']);self.assertEqual('subprocess_popen',result['stage'])
        self.assertNotIn('do-not-record-secret',json.dumps(result))
        self.assertTrue(all(set(frame)=={'file','function','line'} for frame in result['frames']))

    def test_intent_is_persisted_and_never_overwritten(self):
        with tempfile.TemporaryDirectory() as temporary:
            output=Path(temporary);intent=dict(operation_id='fixed-op',memory=self.plan['memory'],revision=3,started_ms=123)
            harness.persist_execution_intent(output,intent)
            self.assertEqual(intent,json.loads((output/'intent.json').read_text()))
            with self.assertRaises(FileExistsError):harness.persist_execution_intent(output,dict(operation_id='different'))
            self.assertEqual(intent,json.loads((output/'intent.json').read_text()))

    def test_failure_keeps_bounded_real_tool_metadata(self):
        # Exercise the actual stdout event parser and failure/finally path;
        # Popen and reads are synthetic, with no Agent/model/daemon launched.
        with tempfile.TemporaryDirectory() as temporary:
            root=Path(temporary);(root/'models.json').write_text('{}')
            plan=dict(self.plan,pi_directory=str(root),guard='/synthetic/guard',executable='/synthetic/pi',workspace=str(root),provider='synthetic',model='synthetic',max_seconds=2)
            start=dict(type='tool_execution_start',toolName='memory_get',toolCallId='call-fixed',args=dict(scope=plan['project'],memory_id=plan['memory']))
            end=dict(type='tool_execution_end',toolName='memory_get',toolCallId='call-fixed',isError=False,result=dict(details=dict(protocol_version=1,ok=True,result=dict(text='do-not-record-body',usage_receipt=self.receipts['read']))))
            stdout=SimpleNamespace(fileno=lambda:10001);stderr=SimpleNamespace(fileno=lambda:10002)
            process=SimpleNamespace(stdout=stdout,stderr=stderr,pid=99999999,wait=lambda timeout:1)
            payload=(json.dumps(start)+'\n'+json.dumps(end)+'\n').encode();sent=[False]
            class Selector:
                def __init__(self):self.mapping={}
                def register(self,stream,flags,data):self.mapping[stream.fileno()]=SimpleNamespace(fileobj=stream,data=data)
                def get_map(self):return self.mapping
                def select(self,timeout):return [(key,None) for key in list(self.mapping.values())]
                def unregister(self,stream):del self.mapping[stream.fileno()]
            def read(fd,size):
                if fd==10001 and not sent[0]:sent[0]=True;return payload
                return b''
            diagnostic={}
            with patch.dict(harness.os.environ,{'MEMLANE_DISK_TASK':'1'}),patch.object(harness.subprocess,'Popen',return_value=process),patch.object(harness.os,'write',return_value=1),patch.object(harness.os,'read',side_effect=read),patch.object(harness.os,'killpg',side_effect=ProcessLookupError()),patch.object(harness.selectors,'DefaultSelector',Selector):
                with self.assertRaisesRegex(ValueError,'pi_daily_process_failed'):harness.run_pi(plan,'no model','fixed-op','synthetic',diagnostic)
            self.assertEqual(2,len(diagnostic['tool_events']))
            self.assertEqual(self.receipts['read']['id'],diagnostic['tool_events'][1]['receipt_id'])
            self.assertTrue(diagnostic['process_started']);self.assertEqual('process_terminal_wait',diagnostic['primary_failure']['stage'])
            self.assertNotIn('do-not-record-body',json.dumps(diagnostic))

    def test_tool_failure_survives_permission_error_during_group_check(self):
        # Exercise the actual stdout event parser and failure/finally path;
        # Popen and reads are synthetic, with no Agent/model/daemon launched.
        with tempfile.TemporaryDirectory() as temporary:
            root=Path(temporary);(root/'models.json').write_text('{}')
            plan=dict(self.plan,pi_directory=str(root),guard='/synthetic/guard',executable='/synthetic/pi',workspace=str(root),provider='synthetic',model='synthetic',max_seconds=2)
            start=dict(type='tool_execution_start',toolName='memory_get',toolCallId='call-fixed',args=dict(scope=plan['project'],memory_id=plan['memory']))
            end=dict(type='tool_execution_end',toolName='memory_get',toolCallId='call-fixed',isError=True,result=dict(details=dict(protocol_version=1,ok=True,result=dict(text='do-not-record-body',usage_receipt=self.receipts['read']))))
            stdout=SimpleNamespace(fileno=lambda:10001);stderr=SimpleNamespace(fileno=lambda:10002)
            process=SimpleNamespace(stdout=stdout,stderr=stderr,pid=99999999,wait=lambda timeout:1)
            payload=(json.dumps(start)+'\n'+json.dumps(end)+'\n').encode();sent=[False]
            class Selector:
                def __init__(self):self.mapping={}
                def register(self,stream,flags,data):self.mapping[stream.fileno()]=SimpleNamespace(fileobj=stream,data=data)
                def get_map(self):return self.mapping
                def select(self,timeout):return [(key,None) for key in list(self.mapping.values())]
                def unregister(self,stream):del self.mapping[stream.fileno()]
            def read(fd,size):
                if fd==10001 and not sent[0]:sent[0]=True;return payload
                return b''
            diagnostic={}
            with patch.dict(harness.os.environ,{'MEMLANE_DISK_TASK':'1'}),patch.object(harness.subprocess,'Popen',return_value=process),patch.object(harness.os,'write',return_value=1),patch.object(harness.os,'read',side_effect=read),patch.object(harness.os,'killpg',side_effect=PermissionError(1,'do-not-record-cleanup-text')),patch.object(harness.selectors,'DefaultSelector',Selector):
                with self.assertRaisesRegex(ValueError,'tool_execution_failed'):harness.run_pi(plan,'no model','fixed-op','synthetic',diagnostic)
            self.assertEqual(2,len(diagnostic['tool_events']))
            self.assertEqual(self.receipts['read']['id'],diagnostic['tool_events'][1]['receipt_id'])
            self.assertTrue(diagnostic['process_started']);self.assertEqual('process_output_read',diagnostic['primary_failure']['stage'])
            self.assertNotIn('do-not-record-body',json.dumps(diagnostic))

            self.assertEqual('tool_execution_failed',diagnostic['primary_failure']['code'])
            self.assertEqual('unknown_permission',diagnostic['group_observation'])
            self.assertNotIn('group_gone',diagnostic)
            self.assertTrue(any(error['errno']==1 for error in diagnostic['cleanup_errors']))
            self.assertNotIn('do-not-record-cleanup-text',json.dumps(diagnostic))

    def test_model_words_cannot_replace_calls(self):
        with self.assertRaisesRegex(ValueError,'exact_tool_order'):
            harness.verify_calls([],self.plan,self.operation,self.query)

    def test_processing_or_verification_receipt_is_rejected(self):
        for channel in ['verification','background','processing']:
            calls=copy.deepcopy(self.calls);calls[1]['response']['result']['usage_receipt']['channel']=channel
            with self.assertRaises(ValueError):harness.verify_calls(calls,self.plan,self.operation,self.query)

    def test_wrong_version_scope_or_fabricated_exposure_is_rejected(self):
        mutations=[lambda c:c[1]['response']['result'].update(revision=2),lambda c:c[0]['response']['result']['usage_receipt']['targets'][0].update(dataset=str(uuid.uuid4())),lambda c:c[2]['arguments'].update(exposure_receipt='model-invented-id')]
        for mutate in mutations:
            calls=copy.deepcopy(self.calls);mutate(calls)
            with self.assertRaises(ValueError):harness.verify_calls(calls,self.plan,self.operation,self.query)

    def test_tool_success_without_durable_matching_rows_is_rejected(self):
        for field,value in [('connection',str(uuid.uuid4())),('generation',4),('revision',2),('channel','verification'),('exposure','invented')]:
            after=copy.deepcopy(self.after);after['rows'][2][field]=value
            with self.assertRaises(ValueError):harness.verify_durable(self.before,after,self.receipts,self.plan)
        after=copy.deepcopy(self.after);after['versions'].append((str(uuid.uuid4()),1,'active'))
        with self.assertRaisesRegex(ValueError,'changed_saved_knowledge'):harness.verify_durable(self.before,after,self.receipts,self.plan)

    def test_wrapper_has_no_write_tool_and_calls_actual_extension(self):
        source=harness.wrapper_source(self.plan,self.operation,self.query)
        self.assertIn('import installed from "/synthetic/installed/memlane.ts"',source)
        self.assertIn('await execute(id,args,signal,update,ctx)',source)
        self.assertIn('{scope:bound.project,query:p.query,limit:10}',source)
        self.assertNotIn('{...p,scopes:[bound.project]',source)
        self.assertIn('p.scope!==bound.project',source)
        self.assertNotIn('memory_save',source)
        self.assertNotIn('memory_initialize',source)
        self.assertIn('MEMLANE_USAGE_CHANNEL',Path(harness.__file__).read_text())
        with self.assertRaises(ValueError):harness.reject_commands({'providers':{'key':'!unauthorized-command'}})

    def test_preparation_is_read_only_and_requires_synthetic_identity(self):
        with tempfile.TemporaryDirectory() as temporary:
            base=Path(temporary);scratch=base/'memlane-task-fixture/initialization';scratch.mkdir(parents=True)
            client=scratch/'client-responsive';adapter=client/'agent-adapters'/'22222222-2222-4222-8222-222222222222';adapter.mkdir(parents=True)
            workspace=scratch/'synthetic-flow-project';(workspace/'.git').mkdir(parents=True)
            pi=scratch/'pi-responsive';pi.mkdir();home=base/'home';original=home/'.pi/agent';original.mkdir(parents=True)
            settings={'defaultProvider':'deepseek','defaultModel':'synthetic-model'}
            for root in [pi,original]:
                (root/'settings.json').write_text(json.dumps(settings));(root/'models.json').write_text('{}');(root/'auth.json').write_text('{}')
            extension=adapter/'memlane.ts';extension.write_text('export default function(){}')
            app=scratch/'Fixture.app';binary=app/'Contents/Resources/bin/memlaned';binary.parent.mkdir(parents=True);binary.write_text('synthetic helper, never executed');(binary.parent/'memlane').write_text('synthetic bridge, never executed');(app/'Contents/Info.plist').write_bytes(plistlib.dumps({'CFBundleIdentifier':'io.github.zwhu.memlane.concentrated20261111.fixture'}))
            node=base/'node/bin/pi';node.parent.mkdir(parents=True);node.write_text('synthetic executable, never executed')
            options=dict(ClientRoot=str(client),ServiceRoot=str(client/'data'),PiDirectory=str(pi),Workspace=str(workspace),Project='11111111-1111-4111-8111-111111111111',Bridge=str(binary),AgentPath=str(node))
            installation=dict(agent_version='0.84.1',toolset_version=2,protocol_generation=3,protocol_verified_at='synthetic-time',connection=dict(id='22222222-2222-4222-8222-222222222222',agent='pi',generation=3,enabled=True,allowed_scopes=['11111111-1111-4111-8111-111111111111']),options=options,extension_path=str(extension),extension_hash=harness.digest(extension));installation['protocol_scope_digest']=harness.scope_digest(installation['connection'])
            (client/'service.json').write_text(json.dumps({'root':str(client/'data')}));(adapter/'installation.json').write_text(json.dumps(installation));(scratch/'synthetic-only.json').write_text('{"synthetic_only":true}')
            evidence=base/'evidence';evidence.mkdir()
            for name,data in [('source-info.json',dict(synthetic_only=True,identifier='io.github.zwhu.memlane.concentrated20261111.fixture',root=str(scratch),variant='responsive')),('runtime-config.json',dict(root=str(client))),('native-launch.json',dict(app=str(app),pi_directory=str(pi),original_pi_auth_link_only=True)),('native-pi-source-before.json',harness.source_hashes(original))]:(evidence/name).write_text(json.dumps(data))
            (evidence/'processing-native-active.json').write_text(json.dumps(dict(current=dict(active=True,revision='independent-processing'),validation=dict(id='independent-validation',state='succeeded'),health=dict(helper_peer_pid=1),evidence_identity_matches=True)))
            (evidence/'project-task-native-final.json').write_text(json.dumps(dict(project=self.plan['project'],task=dict(id='native-task',state='completed',snapshot=dict(executor=dict(connection_id=self.plan['connection'],generation=3),workspace=str(workspace))),run=dict(receipt=dict(added=[dict(memory=self.plan['memory'],revision=3)])))))
            with patch.object(harness.Path,'home',return_value=home):
                with patch.object(harness.subprocess,'Popen',side_effect=AssertionError('preparation launched a process')):
                    plan=harness.prepare(evidence,self.plan['memory'],3,3,self.plan['connection'],self.plan['project'],workspace)
                    self.assertFalse(plan['paid_model_called'])
                    self.assertEqual('daily',plan['usage_channel'])
                (scratch/'synthetic-only.json').write_text('{"synthetic_only":false}')
                with self.assertRaisesRegex(ValueError,'missing_synthetic_marker'):harness.prepare(evidence,self.plan['memory'],3,3,self.plan['connection'],self.plan['project'],workspace)


if __name__=='__main__':unittest.main()
