#!/usr/bin/env python3
"""Prepare an explicit synthetic submission; --execute needs separate approval.

No service/model/config startup, no auto-retry. Reuse the frozen guarded daily
transport in a private imported namespace, registering save/status only.
"""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import sqlite3
import subprocess
import time
import uuid

REPO=Path(__file__).resolve().parents[1]
TRANSPORT=Path(__file__).with_name('verify-daily-agent-usage.py')
TRANSPORT_SHA='1824eea84d875f26ebb00f3aca0069390b57b0a3be1db0d10148bc1d4db9d993'
USAGE_PLAN_SHA='24c3d9c60d5124fd88f6fbba24526d54948f1745ee4aa3b9dbad0d428972c191'
ALLOWED_TOOLS=('memory_save','memory_status')

def load_transport():
    if hashlib.sha256(TRANSPORT.read_bytes()).hexdigest()!=TRANSPORT_SHA:raise ValueError('frozen_transport_changed')
    spec=importlib.util.spec_from_file_location('daily_submit_private_transport',TRANSPORT)
    module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module)
    return module

base=load_transport()
require=base.require

def write_json_exclusive(path,value):
    fd=os.open(path,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600)
    with os.fdopen(fd,'w') as stream:
        json.dump(value,stream,ensure_ascii=False,indent=2);stream.write('\n');stream.flush();os.fsync(stream.fileno())
    directory=os.open(path.parent,os.O_RDONLY)
    try:os.fsync(directory)
    finally:os.close(directory)

def prepare(usage_plan_path,output):
    require(os.environ.get('MEMLANE_DISK_TASK')=='1','prepare_via_task_py_required')
    require(base.under(output,REPO/'build'),'plan_must_be_repository_build')
    require(hashlib.sha256(usage_plan_path.read_bytes()).hexdigest()==USAGE_PLAN_SHA,'reviewed_usage_plan_changed')
    old=base.read_json(usage_plan_path)
    current=base.prepare(Path(old['evidence']),old['memory'],old['revision'],old['generation'],old['connection'],old['project'],old['workspace'])
    operation='daily-submit-'+uuid.uuid4().hex
    text=('仅适用于合成 Memlane 项目 '+current['project']+' 的日常提交验收。\n'
          '日常保存与索引完成分别核对；同一条原始提交重试沿用原操作，不另建重复知识。\n'
          '这是明确授权的新合成知识，不包含用户数据，也不修改已有知识或项目授权。')
    payload=dict(scope=current['project'],operation_id=operation,title='合成验收：日常提交与索引分别核对',text=text,origin='knowledge',source=dict(fragment='Memlane synthetic daily submission; '+operation))
    plan=dict(current,kind='daily_synthetic_submission',prepared_ms=int(time.time()*1000),operation_id=operation,payload=payload,allowed_tools=list(ALLOWED_TOOLS),expected_order=['memory_save','memory_save','memory_status'],transport_sha256=TRANSPORT_SHA,usage_plan_sha256=USAGE_PLAN_SHA,paid_model_called=False,max_seconds=180,max_output_bytes=base.MAX_BYTES)
    plan['anchor_memory'],plan['anchor_revision']=plan.pop('memory'),plan.pop('revision')
    output.mkdir(mode=0o700,parents=True,exist_ok=True)
    intent=dict(version=1,operation_id=operation,prepared_ms=plan['prepared_ms'],project=plan['project'],connection=plan['connection'],generation=plan['generation'],workspace=plan['workspace'],payload=payload,payload_sha256=hashlib.sha256(json.dumps(payload,ensure_ascii=False,separators=(',',':')).encode()).hexdigest(),allowed_tools=list(ALLOWED_TOOLS),no_memory_id_preallocated=True)
    base.persist_execution_intent(output,intent)
    write_json_exclusive(output/'plan.json',plan)
    return plan

def wrapper_source(plan,operation,_query):
    bound=json.dumps(dict(payload=plan['payload']),ensure_ascii=False)
    return 'import installed from '+json.dumps(plan['extension'])+';\nconst bound='+bound+';\n'+r'''
export default function(pi) {
 let saved="",saves=0,statuses=0,firstDelivery=null;
 installed(new Proxy(pi,{get(target,key){
  if(key!=="registerTool"){const value=Reflect.get(target,key);return typeof value==="function"?value.bind(target):value;}
  return definition=>{
   if(!["memory_save","memory_status"].includes(definition.name))return;
   const execute=definition.execute;
   target.registerTool({...definition,async execute(id,p,signal,update,ctx){
    if(p.scope!==bound.payload.scope)throw new Error("memlane_submit_scope_mismatch");
    if(definition.name==="memory_save"){
     if(saves>=2||statuses!==0||["operation_id","title","text","origin"].some(k=>p[k]!==bound.payload[k])||Object.keys(p).some(k=>!["scope","operation_id","title","text","origin","source"].includes(k))||!p.source||Object.keys(p.source).some(k=>k!=="fragment")||p.source.fragment!==bound.payload.source.fragment)throw new Error("memlane_submit_payload_or_order_mismatch");
    }else if(saves!==2||statuses!==0||p.memory_id!==saved||Object.keys(p).some(k=>!["scope","memory_id"].includes(k)))throw new Error("memlane_submit_status_target_mismatch");
    const args=definition.name==="memory_save"?bound.payload:{scope:bound.payload.scope,memory_id:saved};
    let out;
    try { out=await execute(id,args,signal,update,ctx); }
    catch(error){
     let reply;try{reply=JSON.parse(error?.message)}catch{}
     const d=reply?.delivery;
     if(definition.name==="memory_save"&&saves===1&&saved&&firstDelivery?.state==="confirmed"&&firstDelivery.persisted_confirmed===true&&firstDelivery.local_retained===true&&firstDelivery.memory===saved&&firstDelivery.saved_revision===1&&reply?.protocol_version===1&&reply.ok===false&&reply.error?.code==="delivery_already_confirmed"&&!reply.result&&d?.state==="confirmed"&&d.persisted_confirmed===true&&d.local_retained===true&&d.memory===saved&&d.saved_revision===1&&["id","request_digest","review_digest"].every(k=>typeof d[k]==="string"&&(/^[0-9a-f]{64}$/).test(d[k])&&d[k]===firstDelivery[k])){
      saves++;
      return {content:[{type:"text",text:"同一条原始提交已确认保存；本次没有再次保存，也不是新的保存回执。请继续查询实际保存ID的状态。"}],details:reply};
     }
     throw new Error("memlane_submit_bridge_unconfirmed");
    }
    if(out?.details?.protocol_version!==1||out.details.ok!==true)throw new Error("memlane_submit_bridge_unconfirmed");
    const result=out.details.result;
    if(definition.name==="memory_save"){
     const record=result?.record;
     if(result?.persisted!==true||result.saved_revision!==1||record?.revision!==1||record?.dataset!==bound.payload.scope||record?.origin!=="knowledge"||typeof record?.memory!=="string"||!(/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/).test(record.memory)||saved&&saved!==record.memory)throw new Error("memlane_submit_save_receipt_mismatch");
     saved=record.memory;if(saves===0)firstDelivery=out.details.delivery??null;saves++;
    }else{
     if(result?.memory!==saved||result.revision!==1||result.dataset!==bound.payload.scope)throw new Error("memlane_submit_status_receipt_mismatch");
     statuses++;
    }
    return out;
   }});
  };
 }}));
}
'''

def submit_metadata(event,plan,_query=None,_completed=None):
    kind=event.get('type');tool=event.get('toolName');identity=event.get('toolCallId')
    fact=dict(event=kind,tool=tool if tool in ALLOWED_TOOLS else 'unexpected_tool',call_id=identity if isinstance(identity,str) and len(identity)<=160 and all(c.isascii() and (c.isalnum() or c in '_:-.') for c in identity) else None,observed_ms=int(time.time()*1000))
    if kind=='tool_execution_start':
        args=event.get('args',event.get('arguments',{}))
        if isinstance(args,dict):
            fact['scope_matches']=args.get('scope')==plan['project']
            if tool=='memory_save':fact['fixed_payload_matches']=args==plan['payload']
    else:
        outer=event.get('result');details=outer.get('details') if isinstance(outer,dict) else None
        fact['is_error']=event.get('isError') is True
        if fact['is_error'] and isinstance(outer,dict):
            codes=('memlane_submit_scope_mismatch','memlane_submit_payload_or_order_mismatch','memlane_submit_status_target_mismatch','memlane_submit_bridge_unconfirmed','memlane_submit_save_receipt_mismatch','memlane_submit_status_receipt_mismatch')
            for item in (outer.get('content') or [])[:8]:
                text=item.get('text') if isinstance(item,dict) else None
                if isinstance(text,str) and len(text)<=8192:
                    for code in codes:
                        if text.strip() in (code,'Error: '+code):fact['wrapper_code']=code
        if isinstance(details,dict):
            if details.get('ok') is False and (details.get('error') or {}).get('code')=='delivery_already_confirmed':
                delivery=details.get('delivery') or {}
                fact['already_confirmed_delivery']={k:delivery.get(k) for k in ['id','state','persisted_confirmed','saved_revision','memory','request_digest','review_digest']}
            fact['ok']=details.get('ok') is True;result=details.get('result')
            if isinstance(result,dict):
                record=result.get('record') if tool=='memory_save' else result
                if isinstance(record,dict):
                    value=record.get('memory')
                    try:fact['actual_memory']=str(uuid.UUID(value))
                    except (ValueError,TypeError,AttributeError):pass
                    fact['revision']=record.get('revision');fact['persisted']=result.get('persisted') is True if tool=='memory_save' else None
    return fact

def confirmed_delivery(response,memory,expected):
    require(response.get('protocol_version')==1 and response.get('ok') is False and (response.get('error') or {}).get('code')=='delivery_already_confirmed' and response.get('result') is None,'not_confirmed_delivery_fact')
    delivery=response.get('delivery') or {}
    require(expected.get('state')=='confirmed' and expected.get('persisted_confirmed') is True and expected.get('local_retained') is True and expected.get('memory')==memory and expected.get('saved_revision')==1,'original_confirmation_binding_required')
    require(delivery.get('state')=='confirmed' and delivery.get('persisted_confirmed') is True and delivery.get('local_retained') is True and delivery.get('memory')==memory and delivery.get('saved_revision')==1,'confirmed_delivery_target_mismatch')
    for key in ['id','request_digest','review_digest']:
        value=delivery.get(key)
        require(isinstance(value,str) and len(value)==64 and all(c in '0123456789abcdef' for c in value) and value==expected.get(key),'confirmed_delivery_identity_mismatch')
    return dict(kind='already_confirmed_delivery',memory=memory,saved_revision=1,delivery_id=delivery['id'],request_digest=delivery['request_digest'],review_digest=delivery['review_digest'],fresh_save_receipt=False)


def checked_save(response,plan):
    require(response.get('protocol_version')==1 and response.get('ok') is True,'structured_submit_success_required')
    receipt=response.get('result') or {};record=receipt.get('record') or {}
    require(receipt.get('persisted') is True and receipt.get('saved_revision')==1,'save_not_confirmed')
    require(record.get('dataset')==plan['project'] and record.get('revision')==1 and record.get('origin')=='knowledge' and record.get('state')=='active' and record.get('text')==plan['payload']['text'] and record.get('title')==plan['payload']['title'],'save_record_mismatch')
    require(str(uuid.UUID(record.get('memory')))==record['memory'],'save_receipt_memory_not_uuid')
    return record['memory']


def verify_calls(calls,plan):
    require([call['tool'] for call in calls]==plan['expected_order'],'exact_submit_tool_order_required')
    require(all(call['arguments']==plan['payload'] for call in calls[:2]),'submitted_payload_changed')
    memory=checked_save(calls[0]['response'],plan)
    replay=calls[1]['response']
    if replay.get('ok') is True:
        require(checked_save(replay,plan)==memory,'same_operation_created_duplicate_knowledge')
    else:
        # A retained confirmation is its own fact, never a new SaveReceipt.
        confirmed_delivery(replay,memory,calls[0]['response'].get('delivery') or {})
    require(calls[2]['arguments']==dict(scope=plan['project'],memory_id=memory),'status_did_not_bind_actual_save_id')
    require(calls[2]['response'].get('protocol_version')==1 and calls[2]['response'].get('ok') is True,'structured_status_success_required')
    status=calls[2]['response']['result']
    require(status.get('memory')==memory and status.get('dataset')==plan['project'] and status.get('revision')==1,'status_receipt_mismatch')
    return memory


def authorization(plan):
    connection=next((c for c in base.ipc(plan,'connections') if c['id']==plan['connection']),{})
    require(connection.get('enabled') and connection.get('generation')==plan['generation'] and base.scope_digest(connection)==plan['protocol_proof']['scope_digest'],'current_authorization_changed')
    scope=next((s for s in base.ipc(plan,'scopes') if s['id']==plan['project']),{})
    require(scope.get('kind')=='project' and Path(scope.get('path','')).resolve()==Path(plan['workspace']),'project_workspace_changed')
    return {key:connection.get(key) for key in ['id','agent','generation','enabled','project','allowed_scopes','auto_project','pending_project','trusted_workspace','trusted_repository']}

def project_snapshot(plan):
    database=Path(plan['data'])/'memlane.sqlite'
    with sqlite3.connect(database.as_uri()+'?mode=ro',uri=True,timeout=5) as db:
        db.execute('PRAGMA query_only=ON')
        return [list(row) for row in db.execute('SELECT id,current_revision,state,(SELECT count(*) FROM versions v WHERE v.memory=m.id) FROM memories m WHERE dataset=? ORDER BY id LIMIT 1001',(plan['project'],))]

def index_fact(plan,memory):
    with sqlite3.connect((Path(plan['data'])/'memlane.sqlite').as_uri()+'?mode=ro',uri=True,timeout=5) as db:
        db.execute('PRAGMA query_only=ON')
        row=db.execute('SELECT m.id,m.current_revision,m.indexed_revision,m.state,j.revision,j.state,(length(j.error)>0),(SELECT count(*) FROM index_bindings b WHERE b.memory=m.id AND b.revision=j.revision) FROM memories m JOIN jobs j ON j.memory=m.id WHERE m.id=? AND j.revision=1',(memory,)).fetchone()
    return dict(zip(['memory','current_revision','indexed_revision','state','job_revision','job_state','job_error_present','binding_count'],row)) if row else {}

def execute(plan,output):
    require(os.environ.get('MEMLANE_DISK_TASK')=='1','execute_via_task_py_required')
    require(base.under(output,REPO/'build'),'report_must_be_repository_build')
    require(plan['transport_sha256']==TRANSPORT_SHA and plan['kind']=='daily_synthetic_submission' and plan['allowed_tools']==list(ALLOWED_TOOLS),'reviewed_submit_plan_required')
    require(plan['payload']['operation_id']==plan['operation_id'] and plan['payload']['scope']==plan['project'],'intent_payload_mismatch')
    output.mkdir(mode=0o700,parents=True,exist_ok=False)
    started=time.monotonic();deadline=started+180
    report=dict(status='failed',model_attempted=False,model_called=False,native_ui_tested=False,plan=plan,run_diagnostics={})
    try:
        require(base.source_hashes(Path(plan['original_pi']))==plan['original_hashes'] and base.source_hashes(Path(plan['pi_directory']))==plan['isolated_hashes'],'configuration_changed')
        require(base.digest(Path(plan['extension']))==plan['extension_hash'] and base.binary_digest(Path(plan['bridge']))==plan['installed_bridge_hash'] and base.binary_digest(Path(plan['guard']))==plan['guard_hash'],'installed_tool_or_guard_changed')
        grant_before=authorization(plan);before=project_snapshot(plan);require(len(before)<=1000,'project_snapshot_unbounded')
        base.persist_execution_intent(output,dict(operation_id=plan['operation_id'],started_ms=int(time.time()*1000),project=plan['project'],connection=plan['connection'],generation=plan['generation'],payload=plan['payload'],no_memory_id_preallocated=True))
        prompt=('Memlane explicit synthetic daily submission. Only memory_save and memory_status tools; no files/commands/other tools. '
                '1 Call memory_save exactly with '+json.dumps(plan['payload'],ensure_ascii=False)+' . '
                '2 Replay memory_save with identical arguments and same operation_id to verify idempotency. '
                '3 Call memory_status once with scope='+plan['project']+' and memory_id taken only from actual memory_save result.record.memory. Do not invent a memory ID. Index pending is not failure; host checks index later. Do not substitute text for any tool call.')
        base.TOOLS=ALLOWED_TOOLS;base.wrapper_source=wrapper_source;base.tool_metadata=submit_metadata
        runtime_plan=dict(plan,max_seconds=max(1,int(deadline-time.monotonic())))
        report.update(model_attempted=True,model_called=None)
        calls=base.run_pi(runtime_plan,prompt,plan['operation_id'],'',report['run_diagnostics'])
        memory=verify_calls(calls,plan);report.update(model_called=True,actual_memory=memory,revision=1,calls=calls)
        fact={}
        while time.monotonic()<deadline:
            fact=index_fact(plan,memory)
            if fact.get('indexed_revision')==1 and fact.get('job_state')=='completed' and not fact.get('job_error_present') and fact.get('binding_count',0)>0:break
            if fact.get('job_state')=='failed':break
            time.sleep(min(1,max(0,deadline-time.monotonic())))
        report['index_fact']=fact
        require(fact.get('indexed_revision')==1 and fact.get('job_state')=='completed' and not fact.get('job_error_present') and fact.get('binding_count',0)>0,'index_not_confirmed')
        current=base.ipc(plan,'get',memory=memory)
        require(current.get('revision')==1 and current.get('dataset')==plan['project'] and current.get('origin')=='knowledge' and current.get('index_state')=='indexed' and current.get('text')==plan['payload']['text'],'host_current_mismatch')
        after=project_snapshot(plan);new=[row for row in after if row[0] not in {r[0] for r in before}]
        require([row for row in after if row[0]!=memory]==before and new==[[memory,1,'active',1]],'existing_knowledge_changed_or_duplicate_created')
        require(authorization(plan)==grant_before,'authorization_changed_during_submission')
        feed=base.ipc(plan,'home_activity',home_activity=dict(project=plan['project'],kind='save',limit=50))
        event=next((e for e in feed['items'] if e['memory']==memory and e['revision']==1 and e['dataset']==plan['project'] and e['kind']=='save'),None)
        require(event is not None,'home_project_save_fact_missing')
        page=base.ipc(plan,'list',list_query=dict(datasets=[plan['project']],kind='knowledge',text=plan['payload']['title'],limit=50))
        require(any(v['memory']==memory and v['revision']==1 for v in page['items']),'project_knowledge_list_missing_new_save')
        report.update(status='passed',home_event=event,existing_project_knowledge_unchanged=True,grants_unchanged=True,idempotent_single_new_knowledge=True,model_text_used_as_evidence=False,processing_session_used=False)
    except Exception as error:
        failure=report['run_diagnostics'].get('primary_failure') or base.safe_failure(error,report['run_diagnostics'])
        report.update(failure_code=failure['code'],failure_diagnostic=failure)
    finally:
        report['original_pi_unchanged']=base.source_hashes(Path(plan['original_pi']))==plan['original_hashes']
        report['isolated_pi_unchanged']=base.source_hashes(Path(plan['pi_directory']))==plan['isolated_hashes']
        report['extension_unchanged']=base.digest(Path(plan['extension']))==plan['extension_hash']
        if not all(report[k] for k in ['original_pi_unchanged','isolated_pi_unchanged','extension_unchanged']):report.update(status='failed',failure_code='protected_configuration_changed')
        write_json_exclusive(output/'report.json',report)
    print(json.dumps(dict(status=report['status'],model_called=report['model_called'],report=str(output/'report.json'),native_ui_tested=False)))
    return 0 if report['status']=='passed' else 1

def confirmed_source_memory(failed,plan):
    require(failed.get('plan',{}).get('operation_id')==plan['operation_id'] and failed['plan'].get('payload')==plan['payload'],'original_paid_intent_mismatch')
    events=failed.get('run_diagnostics',{}).get('tool_events',[])
    starts={event.get('call_id'):event for event in events if event.get('event')=='tool_execution_start'}
    for event in events:
        start=starts.get(event.get('call_id'),{})
        if event.get('event')=='tool_execution_end' and event.get('tool')=='memory_save' and event.get('ok') is True and event.get('persisted') is True and event.get('revision')==1 and start.get('fixed_payload_matches') is True and start.get('scope_matches') is True:
            value=event.get('actual_memory');require(str(uuid.UUID(value))==value,'original_saved_id_missing');return value
    raise ValueError('actual_original_save_confirmation_required')


def confirmed_entry(plan,memory):
    root=Path(plan['socket_root']);path=root/'agent-outbox/ledger.json'
    require(path.is_file() and not path.is_symlink() and path.stat().st_uid==os.getuid() and path.stat().st_size<=4*1024*1024,'owned_bounded_outbox_required')
    ledger=base.read_json(path)
    entries=[entry for entry in ledger.get('entries',[]) if entry.get('payload',{}).get('connection')==plan['connection'] and entry.get('payload',{}).get('arguments',{}).get('operation_id')==plan['operation_id']]
    require(len(entries)==1,'exact_original_outbox_entry_required');entry=entries[0];payload=entry['payload'];grant=payload.get('grant') or {}
    require(payload['arguments']==plan['payload'] and payload.get('generation')==plan['generation'] and payload.get('root')==plan['socket_root'] and Path(payload.get('workspace','')).resolve()==Path(plan['workspace']).resolve(),'outbox_original_payload_changed')
    require(entry.get('state')=='confirmed' and entry.get('memory')==memory and entry.get('saved_revision')==1 and not entry.get('assignment_operation'),'original_agent_confirmation_required')
    with sqlite3.connect((Path(plan['data'])/'memlane.sqlite').as_uri()+'?mode=ro',uri=True) as db:
        db.execute('PRAGMA query_only=ON');library=db.execute('SELECT identity FROM cognee_library_identity WHERE id=1').fetchone()[0]
    require(grant.get('library_id')==library and grant.get('connection_id')==plan['connection'] and grant.get('generation')==plan['generation'] and grant.get('scope')==plan['project'],'confirmed_entry_library_or_grant_changed')
    return dict(id=entry['id'],state=entry['state'],persisted_confirmed=True,local_retained=True,memory=memory,saved_revision=1,request_digest=entry['request_digest'],review_digest=entry['digest'],attempts=entry.get('attempts'),library_id=library)


def continue_confirmed(plan,failed_path,replay_path,output,owner_helper=None,owner_pid=None):
    # No save/model call occurs in this independent continuation. The original
    # failed paid run is immutable and is never relabelled as a completed chain.
    require(os.environ.get('MEMLANE_DISK_TASK')=='1','continuation_via_task_py_required')
    failed=base.read_json(failed_path);replay_proof=base.read_json(replay_path)
    memory=confirmed_source_memory(failed,plan)
    runtime=dict(plan)
    if owner_helper is not None or owner_pid is not None:
        require(owner_helper is not None and owner_pid is not None and owner_pid>1 and base.under(owner_helper,Path(plan['scratch'])) and owner_helper.name=='memlaned' and owner_helper.is_file(),'explicit_current_owned_helper_required')
        with (owner_helper.resolve().parents[3]/'Contents/Info.plist').open('rb') as stream:
            bundle=base.plistlib.load(stream).get('CFBundleIdentifier','')
        require(bundle.startswith('io.github.zwhu.memlane.concentrated20261111.'),'current_helper_marked_bundle_required')
        # The SameUser socket peer and kernel executable are still checked by
        # base.ipc. Only the transport owner changes, never op/payload/library.
        runtime.update(guard=str(owner_helper.resolve()),guard_hash=base.binary_digest(owner_helper),expected_helper_pid=owner_pid)
    require(base.under(output,REPO/'build'),'continuation_output_must_be_build')
    output.mkdir(mode=0o700,parents=True,exist_ok=False)
    report=dict(transport_owner=dict(helper=runtime['guard'],helper_sha256=runtime['guard_hash'],pid=runtime['expected_helper_pid'],explicit_readonly_rebind=owner_helper is not None),bridge=dict(path=plan['bridge'],mixed_version_retained=True),status='failed',kind='confirmed_submission_status_continuation',operation_id=plan['operation_id'],actual_memory=memory,revision=1,model_rerun=False,save_replayed_by_continuation=False,status_collector_only=True,original_paid_run_status=failed.get('status'),original_agent_status_tool_executed=False,not_a_fresh_save_receipt=True)
    before_hash=hashlib.sha256(failed_path.read_bytes()).hexdigest()
    try:
        grant_before=authorization(runtime);entry=confirmed_entry(runtime,memory)
        observed=replay_proof['replay']
        require(observed.get('operation_id')==plan['operation_id'] and observed.get('same_payload') is True,'same_original_replay_intent_required')
        retained=dict(protocol_version=observed.get('protocol'),ok=observed.get('ok'),error=dict(code=observed.get('code')),delivery=observed.get('delivery'))
        report['confirmed_replay_fact']=confirmed_delivery(retained,memory,entry)
        before=project_snapshot(runtime)
        base.persist_execution_intent(output,dict(operation_id=plan['operation_id'],memory=memory,revision=1,project=plan['project'],connection=plan['connection'],generation=plan['generation'],failed_report_sha256=before_hash,replay_proof_sha256=hashlib.sha256(replay_path.read_bytes()).hexdigest(),mode='memory_status_only_no_model',no_new_operation=True))
        installation=base.read_json(Path(plan['client'])/'agent-adapters'/plan['connection']/'installation.json')
        call='collector-confirmed-status-'+uuid.uuid4().hex
        argv=[plan['bridge'],'call','--durable-delivery','--root',installation['options']['ServiceRoot'],'--connection',plan['connection'],'--generation',str(plan['generation']),'--workspace',plan['workspace'],'--call-id',call,'--usage-channel','daily','--tool-mode','','--tool','memory_status']
        result=subprocess.run(argv,input=json.dumps(dict(scope=plan['project'],memory_id=memory)).encode(),stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=20)
        require(len(result.stdout)<=base.MAX_BYTES,'status_response_unbounded');response=json.loads(result.stdout)
        require(result.returncode==0 and response.get('protocol_version')==1 and response.get('ok') is True,'actual_status_cli_failed')
        status=response['result'];require(status.get('memory')==memory and status.get('revision')==1 and status.get('dataset')==plan['project'] and status.get('origin')=='knowledge' and status.get('text')==plan['payload']['text'] and status.get('title')==plan['payload']['title'],'actual_status_target_or_content_changed')
        report['status_observation']=dict(call_id=call,protocol=1,ok=True,memory=memory,revision=1,dataset=status['dataset'],index_state=status.get('index_state'),collector_only=True)
        fact=index_fact(runtime,memory);require(fact.get('indexed_revision')==1 and fact.get('job_state')=='completed' and not fact.get('job_error_present') and fact.get('binding_count',0)>0,'actual_index_not_confirmed')
        feed=base.ipc(runtime,'home_activity',home_activity=dict(project=plan['project'],kind='save',limit=50));event=next((e for e in feed['items'] if e['memory']==memory and e['revision']==1 and e['dataset']==plan['project']),None);require(event is not None,'actual_home_save_fact_missing')
        page=base.ipc(runtime,'list',list_query=dict(datasets=[plan['project']],kind='knowledge',text=plan['payload']['title'],limit=50));require(any(v['memory']==memory and v['revision']==1 for v in page['items']),'actual_project_list_fact_missing')
        require(project_snapshot(runtime)==before and authorization(runtime)==grant_before and confirmed_entry(runtime,memory)==entry,'continuation_changed_original_facts')
        report.update(status='confirmed_save_status_checks_completed',index_fact=fact,home_event=event,no_new_knowledge=True,original_operation_preserved=True,grants_unchanged=True,existing_versions_unchanged=True,outbox_attempts=entry['attempts'])
    except Exception as error:
        report.update(failure_code=base.safe_failure(error,dict(stage='status_continuation'))['code'])
    finally:
        report['original_paid_report_unchanged']=hashlib.sha256(failed_path.read_bytes()).hexdigest()==before_hash
        report['original_pi_unchanged']=base.source_hashes(Path(plan['original_pi']))==plan['original_hashes'];report['isolated_pi_unchanged']=base.source_hashes(Path(plan['pi_directory']))==plan['isolated_hashes'];report['extension_unchanged']=base.digest(Path(plan['extension']))==plan['extension_hash']
        if not all(report[k] for k in ['original_paid_report_unchanged','original_pi_unchanged','isolated_pi_unchanged','extension_unchanged']):report.update(status='failed',failure_code='protected_source_changed')
        write_json_exclusive(output/'report.json',report)
    print(json.dumps(dict(status=report['status'],model_rerun=False,report=str(output/'report.json'))))
    return 0 if report['status']=='confirmed_save_status_checks_completed' else 1


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--prepare-from-usage',type=Path);parser.add_argument('--plan',type=Path);parser.add_argument('--continue-confirmed',action='store_true');parser.add_argument('--failed-report',type=Path);parser.add_argument('--replay-proof',type=Path);parser.add_argument('--owner-helper',type=Path);parser.add_argument('--owner-pid',type=int);parser.add_argument('--out',required=True,type=Path);parser.add_argument('--execute',action='store_true')
    args=parser.parse_args()
    if args.continue_confirmed:
        require(not args.execute and args.plan is not None and args.failed_report is not None and args.replay_proof is not None and args.prepare_from_usage is None,'explicit_original_continuation_inputs_required')
        return continue_confirmed(base.read_json(args.plan),args.failed_report,args.replay_proof,args.out.resolve(),args.owner_helper,args.owner_pid)
    if args.execute:
        require(args.plan is not None and args.prepare_from_usage is None,'execute_only_reviewed_plan')
        plan=base.read_json(args.plan);intent=base.read_json(args.plan.parent/'intent.json')
        require(intent['operation_id']==plan['operation_id'] and intent['payload']==plan['payload'],'persisted_plan_intent_changed')
        return execute(plan,args.out.resolve())
    require(args.prepare_from_usage is not None and args.plan is None,'explicit_prepare_input_required')
    plan=prepare(args.prepare_from_usage.resolve(),args.out.resolve())
    print(json.dumps(dict(prepared=True,plan=str(args.out.resolve()/'plan.json'),operation_id=plan['operation_id'],payload=plan['payload'],allowed_tools=plan['allowed_tools'],model_called=False,no_memory_id_preallocated=True),ensure_ascii=False,indent=2));return 0

if __name__=='__main__':raise SystemExit(main())
