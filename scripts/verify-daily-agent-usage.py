#!/usr/bin/env python3
"""Prepare by default; --execute explicitly runs one bounded synthetic daily pi proof.

Run through scripts/task.py. No installs, connection/config mutations, service
startup or model retries are performed by this harness. UI acceptance is separate.
"""
import argparse
import ctypes
import hashlib
import json
import os
import plistlib
import stat
import struct
from pathlib import Path
import selectors
import signal
import socket
import sqlite3
import subprocess
import sys
import tempfile
import time
import traceback
import uuid

REPO = Path(__file__).resolve().parents[1]
TOOLS = ('memory_search', 'memory_get', 'memory_adopt')
MAX_BYTES = 4 * 1024 * 1024


def require(condition, code):
    if not condition:
        raise ValueError(code)


def digest(path):
    require(path.is_file() and path.stat().st_size <= 1024 * 1024, 'configuration_file_missing_or_unbounded')
    return hashlib.sha256(path.read_bytes()).hexdigest()


def binary_digest(path):
    require(path.is_file() and path.stat().st_size <= 64 * 1024 * 1024, 'packaged_binary_missing_or_unbounded')
    result = hashlib.sha256()
    with path.open('rb') as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b''): result.update(chunk)
    return result.hexdigest()


def read_json(path):
    require(path.is_file() and path.stat().st_size <= MAX_BYTES, 'metadata_missing_or_unbounded')
    return json.loads(path.read_text())


def under(path, parent):
    return path.resolve().is_relative_to(parent.resolve())


def source_hashes(original):
    return {name: digest(original / name) for name in ('settings.json', 'models.json', 'auth.json')}


def scope_digest(connection):
    return hashlib.sha256(json.dumps([connection.get('project', ''), connection.get('allowed_scopes') or []], ensure_ascii=False, separators=(',', ':')).encode()).hexdigest()


def protocol_proof(installation, connection_id, generation, project):
    connection = installation['connection']
    require(connection['id'] == connection_id and connection['agent'] == 'pi' and connection['generation'] == generation and connection['enabled'], 'installation_identity_changed')
    scopes = connection.get('allowed_scopes') or []
    require(all(str(uuid.UUID(scope)) == scope for scope in scopes), 'exact_scope_uuids_required')
    require(project in scopes and len(scopes) == len(set(scopes)) and not connection.get('auto_project') and not connection.get('pending_project') and not connection.get('trusted_workspace'), 'installation_project_not_authorized_or_unsettled')
    require(not connection.get('project') or connection['project'] in scopes, 'bound_project_not_authorized')
    require(installation.get('protocol_generation') == generation and installation.get('protocol_verified_at') and not installation.get('protocol_error') and installation.get('protocol_scope_digest') == scope_digest(connection), 'current_protocol_scope_proof_required')
    return dict(generation=generation, verified_at=installation['protocol_verified_at'], scope_digest=installation['protocol_scope_digest'], kind='connection_authorization_metadata_only', legacy_readwrite_used=False)


def prepare(evidence, memory, revision, generation, connection_id, project, workspace):
    """Read marked metadata/hashes only; no pi, model, service start or writes."""
    for value in (memory, connection_id, project):
        require(str(uuid.UUID(value)) == value, 'exact_uuid_required')
    require(type(generation) is int and generation > 0 and type(revision) is int and revision > 0, 'explicit_current_generation_version_required')
    info = read_json(evidence / 'source-info.json')
    launch = read_json(evidence / 'native-launch.json')
    runtime = read_json(evidence / 'runtime-config.json')
    scratch = Path(info['root']).resolve()
    require(info.get('synthetic_only') is True and scratch.name == 'initialization' and scratch.parent.name.startswith('memlane-task-'), 'unmarked_scratch')
    require(read_json(scratch / 'synthetic-only.json').get('synthetic_only') is True, 'missing_synthetic_marker')
    client = Path(runtime['root']).resolve()
    require(client == scratch / 'client-responsive' and info.get('variant') == 'responsive', 'wrong_client_root')
    app = Path(launch['app']).resolve()
    require(under(app, scratch), 'app_outside_synthetic_root')
    with (app / 'Contents/Info.plist').open('rb') as stream:
        bundle_id = plistlib.load(stream)['CFBundleIdentifier']
    require(bundle_id.startswith('io.github.zwhu.memlane.concentrated20261111.'), 'unmarked_bundle')
    pi = Path(launch['pi_directory']).resolve()
    require(pi == scratch / 'pi-responsive' and launch.get('original_pi_auth_link_only') is True, 'wrong_isolated_pi_directory')
    installation = read_json(client / 'agent-adapters' / connection_id / 'installation.json')
    connection, options = installation['connection'], installation['options']
    require(installation.get('agent_version') == '0.84.1' and installation.get('toolset_version') == 2, 'pi_installation_version_changed')
    proof = protocol_proof(installation, connection_id, generation, project)
    socket_root = read_json(client / 'service.json')['root']
    require(Path(socket_root).resolve() == client / 'data' and Path(options['ClientRoot']).resolve() == client and Path(options['ServiceRoot']).resolve() == client / 'data' and Path(options['PiDirectory']).resolve() == pi, 'installation_root_changed')
    workspace = Path(workspace).resolve()
    require(under(workspace, scratch) and workspace != scratch and (workspace / '.git').exists(), 'wrong_synthetic_workspace')
    extension = Path(installation['extension_path']).resolve()
    require(under(extension, client / 'agent-adapters' / connection_id) and digest(extension) == installation['extension_hash'], 'extension_changed')
    helper = app / 'Contents/Resources/bin/memlaned'
    bridge = Path(options['Bridge']).resolve()
    executable = Path(options['AgentPath']).absolute()
    require(under(bridge, scratch) and helper.is_file() and bridge.is_file() and executable.is_file(), 'owned_helper_or_pi_missing')
    bridge_app = bridge.parents[3]
    with (bridge_app / 'Contents/Info.plist').open('rb') as stream:
        bridge_bundle_id = plistlib.load(stream)['CFBundleIdentifier']
    require(bridge_bundle_id.startswith('io.github.zwhu.memlane.concentrated20261111.') and bridge.parent == bridge_app / 'Contents/Resources/bin', 'installed_bridge_not_owned_marked_package')
    bridge_hash, guard_hash = binary_digest(bridge), binary_digest(helper)
    current_bridge_hash = binary_digest(app / 'Contents/Resources/bin/memlane')
    original = Path.home() / '.pi/agent'
    baseline = read_json(evidence / 'native-pi-source-before.json')
    require(source_hashes(original) == baseline, 'original_pi_configuration_changed')
    settings = read_json(pi / 'settings.json')
    require(settings.get('defaultProvider') == 'deepseek' and settings.get('defaultModel'), 'review_selected_pi_model_first')
    processing = read_json(evidence / 'processing-native-active.json')
    task = read_json(evidence / 'project-task-native-final.json')
    require(task['project'] == project and task['task']['state'] == 'completed' and task['task']['snapshot']['executor']['connection_id'] == connection_id and task['task']['snapshot']['executor']['generation'] == generation and Path(task['task']['snapshot']['workspace']).resolve() == workspace, 'selected_native_task_identity_changed')
    require(any(v['memory'] == memory and v['revision'] == revision for v in task['run']['receipt']['added']), 'target_not_from_native_task_added_knowledge')
    return dict(synthetic_only=True, evidence=str(evidence.resolve()), scratch=str(scratch), client=str(client), data=str(client / 'data'), socket_root=socket_root, bundle_identifier=bundle_id, installed_bridge_bundle_identifier=bridge_bundle_id, installed_bridge_matches_current_package=bridge_app == app, installed_bridge_hash=bridge_hash, current_package_bridge_hash=current_bridge_hash, bridge_bytes_equal_current_package_bridge=bridge_hash == current_bridge_hash, guard_hash=guard_hash, pi_directory=str(pi), workspace=str(workspace), original_pi=str(original), original_hashes=baseline, isolated_hashes=source_hashes(pi), connection=connection_id, generation=generation, project=project, memory=memory, revision=revision, provider=settings['defaultProvider'], model=settings['defaultModel'], protocol_proof=proof, processing_observation=dict(active=bool(processing['current']['active']), revision=processing['current']['revision'], validation=processing['validation']['id'], state=processing['validation']['state'], evidence_identity_matches=processing.get('evidence_identity_matches'), independent_from_daily_authorization=True, not_a_daily_production_gate=True), native_task=task['task']['id'], expected_helper_pid=processing['health']['helper_peer_pid'], extension=str(extension), extension_hash=installation['extension_hash'], bridge=str(bridge), guard=str(helper), executable=str(executable), tool_mode='', usage_channel='daily', allowed_tools=list(TOOLS), max_seconds=180, max_output_bytes=MAX_BYTES, paid_model_called=False)


def inspect(plan):
    connections = ipc(plan, 'connections')
    connection = next((c for c in connections if c['id'] == plan['connection']), {})
    require(connection.get('enabled') and connection.get('generation') == plan['generation'] and scope_digest(connection) == plan['protocol_proof']['scope_digest'], 'current_connection_authorization_changed')
    scope = next((s for s in ipc(plan, 'scopes') if s['id'] == plan['project']), {})
    require(scope.get('kind') == 'project' and Path(scope.get('path', '')).resolve() == Path(plan['workspace']), 'current_project_workspace_changed')
    target = ipc(plan, 'get', memory=plan['memory'])
    require(target.get('dataset') == plan['project'] and target.get('revision') == plan['revision'] and target.get('origin') == 'knowledge' and target.get('state') == 'active' and target.get('index_state') == 'indexed', 'target_must_be_exact_active_indexed_knowledge')
    require(0 < len(target.get('text', '').encode()) <= 8192, 'synthetic_target_body_unbounded')
    plan['readonly_inspection'] = dict(title=target['title'], memory=target['memory'], revision=target['revision'], dataset=target['dataset'], index_state=target['index_state'], target_text_hash=hashlib.sha256(target['text'].encode()).hexdigest(), model_calls=0, agent_started=False, ipc_operations=['connections', 'scopes', 'get'])
    return plan


def ipc(plan, op, **fields):
    data = plan.get('socket_root', plan['data'])
    address = Path('/tmp') / ('memlane-%d-%s' % (os.getuid(), hashlib.sha256(str(data).encode()).hexdigest()[:12])) / 'service.sock'
    with socket.socket(socket.AF_UNIX) as sock:
        sock.settimeout(15)
        sock.connect(str(address))
        require(address.stat().st_uid == os.getuid() and address.parent.stat().st_mode & 0o077 == 0, 'owned_private_socket_required')
        peer = struct.unpack('i', sock.getsockopt(0, 2, 4))[0]
        require(peer == plan['expected_helper_pid'], 'current_packaged_helper_peer_changed')
        process_path = ctypes.create_string_buffer(4096)
        require(ctypes.CDLL('/usr/lib/libproc.dylib').proc_pidpath(peer, process_path, len(process_path)) > 0 and Path(os.fsdecode(process_path.value)).resolve() == Path(plan['guard']).resolve(), 'ipc_owner_not_current_package_helper')
        sock.sendall((json.dumps(dict(protocol=1, id=str(uuid.uuid4()), op=op, **fields)) + '\n').encode())
        raw = sock.makefile('rb').readline(MAX_BYTES + 1)
    require(len(raw) <= MAX_BYTES, 'ipc_response_unbounded')
    response = json.loads(raw)
    require(response.get('protocol') == 1 and response.get('ok') is True, 'ipc_' + op + '_failed')
    return response.get('result')


def durable(plan):
    """Authoritative read-only rows, no request text/chat/credential columns."""
    database = Path(plan['data']) / 'memlane.sqlite'
    require(database.is_file() and under(database, Path(plan['client'])), 'wrong_database')
    with sqlite3.connect(database.as_uri() + '?mode=ro', uri=True, timeout=5) as db:
        db.execute('PRAGMA query_only=ON')
        columns = ('id', 'kind', 'channel', 'connection', 'generation', 'workspace', 'dataset', 'memory', 'revision', 'observed_ms', 'exposure')
        rows = [dict(zip(columns, row)) for row in db.execute('SELECT '+','.join(columns)+' FROM usage_events WHERE connection=? AND generation=? ORDER BY observed_ms,id,memory', (plan['connection'], plan['generation']))]
        versions = list(db.execute('SELECT id,current_revision,state FROM memories WHERE dataset=? ORDER BY id LIMIT 1001', (plan['project'],)))
        require(len(versions) <= 1000 and len(rows) <= 10000, 'synthetic_snapshot_unbounded')
    return dict(rows=rows, versions=versions)


def receipt_target(receipt, plan, kind, exact=False):
    require(receipt.get('channel') == 'daily' and receipt.get('kind') == kind and receipt.get('id') and receipt.get('workspace_identity'), 'not_daily_structured_receipt')
    targets = receipt.get('targets', [])
    require(targets and all(t['dataset'] == plan['project'] for t in targets), 'receipt_scope_mismatch')
    require(any(t['memory'] == plan['memory'] and t['revision'] == plan['revision'] for t in targets), 'receipt_target_version_mismatch')
    if exact:
        require(len(targets) == 1, 'unexpected_read_or_adoption_target')


def verify_calls(calls, plan, operation, query):
    require([c['tool'] for c in calls] == ['memory_search', 'memory_get', 'memory_adopt', 'memory_adopt'], 'exact_tool_order_required')
    for call in calls:
        require(call['response'].get('protocol_version') == 1 and call['response'].get('ok') is True and call['arguments'].get('scope') == plan['project'], 'structured_success_and_scope_required')
    search, read, adoption, replay = [c['response']['result'] for c in calls]
    require(calls[0]['arguments']['query'] == query and calls[0]['arguments'].get('scopes', [plan['project']]) == [plan['project']], 'search_query_or_scope_changed')
    require(calls[1]['arguments'].get('memory_id')==plan['memory'],'read_argument_target_mismatch')
    require(any(h['version']['memory'] == plan['memory'] and h['version']['revision'] == plan['revision'] and h['version']['dataset'] == plan['project'] for h in search.get('hits', [])), 'search_did_not_return_current_target')
    receipt_target(search['usage_receipt'], plan, 'return')
    require(read.get('memory') == plan['memory'] and read.get('dataset') == plan['project'] and read.get('revision') == plan['revision'], 'read_did_not_match_current_target')
    require(hashlib.sha256(read.get('text', '').encode()).hexdigest() == plan['target_text_hash'], 'read_body_not_current')
    receipt_target(read['usage_receipt'], plan, 'read', exact=True)
    for call in calls[2:]:
        args = call['arguments']
        require(args.get('operation_id') == operation and args.get('memory_id') == plan['memory'] and args.get('revision') == plan['revision'] and args.get('exposure_receipt') == read['usage_receipt']['id'], 'adoption_did_not_use_actual_read_exposure')
    receipt_target(adoption, plan, 'adopt', exact=True)
    require(adoption == replay and search['usage_receipt']['workspace_identity'] == read['usage_receipt']['workspace_identity'] == adoption['workspace_identity'], 'adoption_replay_or_workspace_mismatch')
    return {'return': search['usage_receipt'], 'read': read['usage_receipt'], 'adopt': adoption}


def verify_durable(before, after, receipts, plan):
    require(before['versions'] == after['versions'], 'daily_probe_changed_saved_knowledge')
    old = {(r['id'], r['memory'], r['revision']) for r in before['rows']}
    new = [r for r in after['rows'] if (r['id'], r['memory'], r['revision']) not in old]
    require(new and all(r['channel'] == 'daily' and r['connection'] == plan['connection'] and r['generation'] == plan['generation'] and r['dataset'] == plan['project'] for r in new), 'non_daily_or_wrong_connection_event')
    target = [r for r in new if r['memory'] == plan['memory']]
    require(len(target) == 3 and {r['kind'] for r in target} == {'return', 'read', 'adopt'}, 'durable_return_read_adopt_not_separate')
    for row in target:
        receipt = receipts[row['kind']]
        require(row['id'] == receipt['id'] and row['revision'] == plan['revision'] and row['workspace'] == receipt['workspace_identity'] and row['observed_ms'] == receipt['observed_ms'], 'durable_receipt_identity_mismatch')
        if row['kind'] == 'adopt':
            require(row['exposure'] == receipts['read']['id'], 'durable_adoption_exposure_mismatch')
    return target


def wrapper_source(plan, operation, query):
    """Only register scoped read/search/adopt; delegate to the real installed extension."""
    config = json.dumps(dict(project=plan['project'], memory=plan['memory'], revision=plan['revision'], operation=operation, query=query), ensure_ascii=False)
    return ('import installed from '+json.dumps(plan['extension'])+';\nconst bound='+config+';\n'+r'''
export default function(pi) {
 let stage=0,exposure="",adopts=0;
 const tools=["memory_search","memory_get","memory_adopt"];
 installed(new Proxy(pi,{get(target,key) {
  if(key!=="registerTool") {const value=Reflect.get(target,key);return typeof value==="function"?value.bind(target):value;}
  return definition=>{
   if(!tools.includes(definition.name))return;
   const execute=definition.execute;
   target.registerTool({...definition,async execute(id,p,signal,update,ctx) {
    if(p.scope!==bound.project)throw new Error("memlane_daily_scope_mismatch");
    if(definition.name==="memory_search"&&(stage!==0||p.query!==bound.query||p.scopes?.some(s=>s!==bound.project)))throw new Error("memlane_daily_search_contract_mismatch");
    if(definition.name==="memory_get"&&(stage!==1||p.memory_id!==bound.memory))throw new Error("memlane_daily_read_contract_mismatch");
    if(definition.name==="memory_adopt"&&(stage!==2||adopts>=2||p.memory_id!==bound.memory||p.revision!==bound.revision||p.operation_id!==bound.operation||p.exposure_receipt!==exposure))throw new Error("memlane_daily_adopt_contract_mismatch");
    const args=definition.name==="memory_search"?{scope:bound.project,query:p.query,limit:10}:p;
    const out=await execute(id,args,signal,update,ctx);
    if(out?.details?.protocol_version!==1||out.details.ok!==true)throw new Error("memlane_daily_bridge_receipt_unconfirmed");
    if(definition.name==="memory_search")stage=1;
    if(definition.name==="memory_get") {exposure=out.details.result?.usage_receipt?.id;if(!exposure)throw new Error("daily proof missing exposure");stage=2;}
    if(definition.name==="memory_adopt")adopts++;
    return out;
   }});
  };
 }}));
}
''')


def reject_commands(value):
    if isinstance(value, str):
        require(not value.startswith('!'), 'command_valued_pi_configuration_rejected')
    elif isinstance(value, list):
        for item in value: reject_commands(item)
    elif isinstance(value, dict):
        for item in value.values(): reject_commands(item)


def persist_execution_intent(output, intent):
    path = output / 'intent.json'
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'w') as stream:
        json.dump(intent, stream, ensure_ascii=False, indent=2)
        stream.write('\n'); stream.flush(); os.fsync(stream.fileno())
    directory_fd = os.open(output, os.O_RDONLY)
    try: os.fsync(directory_fd)
    finally: os.close(directory_fd)


def tool_metadata(event, plan, bound_query=None, completed_count=None):
    kind = event.get('type')
    tool = event.get('toolName')
    identity = event.get('toolCallId')
    # IDs are metadata only; omit arbitrary/large/non-identifier strings.
    safe_id = identity if isinstance(identity, str) and len(identity) <= 160 and all(c.isascii() and (c.isalnum() or c in '_:-.') for c in identity) else None
    fact = dict(event=kind, tool=tool if tool in TOOLS else 'unexpected_tool', call_id=safe_id, observed_ms=int(time.time()*1000))
    if kind == 'tool_execution_start':
        args = event.get('args', event.get('arguments', {}))
        if isinstance(args, dict):
            fact.update(scope_matches=args.get('scope') == plan['project'], target_matches=args.get('memory_id') == plan['memory'] if tool != 'memory_search' else None)
            if tool == 'memory_search':
                value=args.get('query');encoded=value.encode() if isinstance(value,str) else b''
                fact.update(query_present=isinstance(value,str),query_hash=hashlib.sha256(encoded).hexdigest() if isinstance(value,str) else None,query_exact=bound_query is not None and value == bound_query,query_bytes=len(encoded),scopes_match=args.get('scopes',[plan['project']]) == [plan['project']],search_first=completed_count == 0)

    else:
        outer = event.get('result')
        details = outer.get('details') if isinstance(outer, dict) else None
        fact['is_error'] = event.get('isError', False) is True
        known_codes=('memlane_daily_scope_mismatch','memlane_daily_search_contract_mismatch','memlane_daily_read_contract_mismatch','memlane_daily_adopt_contract_mismatch','memlane_daily_bridge_receipt_unconfirmed')
        if isinstance(outer,dict):
            texts=outer.get('content',[])
            if isinstance(texts,list):
                for item in texts[:8]:
                    text=item.get('text') if isinstance(item,dict) else None
                    if isinstance(text,str) and len(text)<=8192:
                        for code in known_codes:
                            if fact['is_error'] and text.strip() in (code, 'Error: '+code): fact['wrapper_code']=code
        if isinstance(details, dict):
            fact.update(protocol=1 if details.get('protocol_version') == 1 else None, ok=details.get('ok') is True)
            result = details.get('result')
            if isinstance(result, dict):
                receipt = result.get('usage_receipt', result)
                value = receipt.get('id') if isinstance(receipt, dict) else None
                try: fact['receipt_id'] = str(uuid.UUID(value)) if isinstance(value, str) else None
                except ValueError: fact['receipt_id'] = None
    return fact


def safe_failure(error, diagnostic):
    return dict(stage=diagnostic.get('stage', 'unknown'), code=str(error) if isinstance(error, ValueError) and str(error).replace('_','').isalnum() else type(error).__name__, exception=type(error).__name__, errno=error.errno if isinstance(error, OSError) else None, frames=[dict(file=Path(frame.filename).name, function=frame.name, line=frame.lineno) for frame in traceback.extract_tb(error.__traceback__)])


def run_pi(plan, prompt, operation, query, diagnostic=None):
    diagnostic = diagnostic if diagnostic is not None else {}
    diagnostic.update(stage='temporary_directory', process_started=False, launch_attempted=False, tool_events=[])
    require(os.environ.get('MEMLANE_DISK_TASK') == '1', 'execute_via_scripts_task_py_required')
    with tempfile.TemporaryDirectory(prefix='memlane-daily-pi-') as temporary:
        diagnostic['stage']='private_directories'
        root = Path(temporary); agent = root/'agent'; home = root/'home'
        agent.mkdir(mode=0o700); home.mkdir(mode=0o700)
        diagnostic['stage']='auth_link'
        (agent/'auth.json').symlink_to(Path(plan['pi_directory'])/'auth.json')
        diagnostic['stage']='model_config_read'
        raw = (Path(plan['pi_directory'])/'models.json').read_bytes()
        require(len(raw) <= 1024*1024, 'pi_models_unbounded'); reject_commands(json.loads(raw))
        diagnostic['stage']='private_model_config_copy'
        (agent/'models.json').write_bytes(raw); (agent/'models.json').chmod(0o400)
        diagnostic['stage']='private_settings_copy'
        (agent/'settings.json').write_text(json.dumps({'defaultProvider':plan['provider'],'defaultModel':plan['model']})); (agent/'settings.json').chmod(0o400)
        diagnostic['stage']='bounded_extension_write'
        extension = root/'daily-proof.ts'; extension.write_text(wrapper_source(plan,operation,query))
        args = [plan['guard'],'--proposal-guard',plan['executable'],'--mode','json','--print','--no-session','--offline','--no-extensions','--no-context-files','--no-skills','--no-prompt-templates','--no-themes','--no-builtin-tools','--extension',str(extension),'--tools',','.join(TOOLS),'--provider',plan['provider'],'--model',plan['model'],prompt]
        # Give the existing packaged process guard its owner pipe as FD3. The
        # small exec shim maps a pass_fds descriptor without shell/preexec code.
        diagnostic['stage']='owner_pipe_create'
        read_fd,write_fd=os.pipe()
        shim='import os,sys;f=int(sys.argv[1]);os.dup2(f,3);os.set_inheritable(3,True);f!=3 and os.close(f);os.execv(sys.argv[2],sys.argv[2:])'
        env={k:os.environ[k] for k in ('LANG','LC_ALL','HTTPS_PROXY','HTTP_PROXY','ALL_PROXY','NO_PROXY') if k in os.environ}
        env.update(PATH=str(Path(plan['executable']).parent)+':/usr/bin:/bin',HOME=str(home),TMPDIR=str(root),PI_CODING_AGENT_DIR=str(agent),PI_TELEMETRY='0',MEMLANE_USAGE_CHANNEL='daily',MEMLANE_TOOL_MODE='')
        process=None; calls=[]; starts={};used=set();buffer=b'';total=0;deadline=time.monotonic()+plan['max_seconds']
        try:
            diagnostic.update(stage='subprocess_popen', launch_attempted=True)
            process=subprocess.Popen([sys.executable,'-c',shim,str(read_fd)]+args,cwd=plan['workspace'],env=env,pass_fds=(read_fd,),start_new_session=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
            diagnostic.update(stage='owner_pipe_permit', process_started=True, pid=process.pid)
            os.close(read_fd);read_fd=-1;os.write(write_fd,b'S')
            diagnostic['stage']='output_selector_registration'
            selector=selectors.DefaultSelector();selector.register(process.stdout,selectors.EVENT_READ,'stdout');selector.register(process.stderr,selectors.EVENT_READ,'stderr')
            diagnostic['stage']='process_output_read'
            while selector.get_map():
                require(time.monotonic()<deadline,'pi_daily_timeout')
                for key,_ in selector.select(min(1,max(0,deadline-time.monotonic()))):
                    chunk=os.read(key.fileobj.fileno(),65536)
                    if not chunk:selector.unregister(key.fileobj);continue
                    total+=len(chunk);require(total<=MAX_BYTES,'pi_daily_output_unbounded')
                    if key.data=='stderr':continue  # never retain upstream diagnostic text
                    buffer+=chunk
                    while b'\n' in buffer:
                        line,buffer=buffer.split(b'\n',1)
                        if not line.strip():continue
                        event=json.loads(line)
                        kind=event.get('type')
                        if kind in ('tool_execution_start','tool_execution_end'):
                            if len(diagnostic['tool_events']) < 8: diagnostic['tool_events'].append(tool_metadata(event,plan,query,len(calls)))
                            else: diagnostic['tool_events_truncated']=True
                        if kind=='tool_execution_start':
                            tool,call=event.get('toolName'),event.get('toolCallId')
                            require(tool in TOOLS and call and call not in used,'unexpected_or_duplicate_tool_call');used.add(call);starts[call]=(tool,event.get('args',event.get('arguments',{})))
                        elif kind=='tool_execution_end':
                            call=event.get('toolCallId');require(call in starts,'tool_end_without_actual_start')
                            tool,arguments=starts.pop(call);require(tool==event.get('toolName') and not event.get('isError',False),'tool_execution_failed')
                            result=event.get('result',{}).get('details')
                            require(isinstance(result,dict) and result.get('protocol_version')==1,'missing_structured_bridge_receipt')
                            calls.append(dict(call_id=call,tool=tool,arguments=arguments,response=result))
                            require(len(calls)<=4,'too_many_tool_calls')
            diagnostic['stage']='process_terminal_wait'
            terminal_code=process.wait(timeout=5)
            diagnostic['return_code']=terminal_code
            require(not buffer.strip() and not starts and terminal_code==0,'pi_daily_process_failed')
        finally:
            active_error=sys.exc_info()[1]
            if active_error is not None: diagnostic['primary_failure']=safe_failure(active_error,diagnostic)
            cleanup_errors=[]
            def cleanup_error(error): cleanup_errors.append(safe_failure(error,diagnostic))
            diagnostic['stage']='owner_pipe_close'
            for descriptor in (read_fd,write_fd):
                if descriptor >= 0:
                    try: os.close(descriptor)
                    except OSError as error: cleanup_error(error)
            if process is not None:
                diagnostic['stage']='process_group_kill'
                try: os.killpg(process.pid,signal.SIGKILL)
                except ProcessLookupError: pass
                except OSError as error: cleanup_error(error)
                diagnostic['stage']='process_reap'
                try: process.wait(timeout=5);diagnostic['reaped']=True
                except (OSError,subprocess.TimeoutExpired) as error: cleanup_error(error)
                diagnostic['stage']='process_group_absence_check'
                try: os.killpg(process.pid,0)
                except ProcessLookupError: diagnostic.update(group_gone=True,group_observation='absent')
                except PermissionError as error:
                    diagnostic['group_observation']='unknown_permission';cleanup_error(error)
                except OSError as error:
                    diagnostic['group_observation']='unknown_error';cleanup_error(error)
                else: diagnostic.update(group_gone=False,group_observation='present')
            diagnostic['cleanup_errors']=cleanup_errors
            if active_error is None and (cleanup_errors or process is not None and diagnostic.get('group_gone') is not True):
                raise ValueError('process_group_cleanup_unverified')
        diagnostic['stage']='temporary_cleanup'
        return calls


def execute(plan, output):
    require(os.environ.get('MEMLANE_DISK_TASK')=='1','execute_via_scripts_task_py_required')
    require(under(output,REPO/'build'),'report_must_be_in_repository_build')
    output.mkdir(mode=0o700,parents=True,exist_ok=False)
    report={'synthetic_only':True,'status':'failed','mode':'daily','plan':plan,'model_called':False,'model_attempted':False,'native_ui_tested':False,'started_ms':int(time.time()*1000)}
    try:
        require(source_hashes(Path(plan['original_pi']))==plan['original_hashes'] and source_hashes(Path(plan['pi_directory']))==plan['isolated_hashes'],'pi_configuration_changed_before_execution')
        require(binary_digest(Path(plan['bridge'])) == plan['installed_bridge_hash'] and binary_digest(Path(plan['guard'])) == plan['guard_hash'], 'packaged_helper_changed_before_execution')
        require(ipc(plan,'health')['state']=='ready','service_must_already_be_ready')
        connections=ipc(plan,'connections');connection=next((c for c in connections if c['id']==plan['connection']),{})
        require(connection.get('enabled') and connection.get('generation')==plan['generation'] and plan['project'] in connection.get('allowed_scopes',[]),'current_connection_authorization_changed')
        scope=next((s for s in ipc(plan,'scopes') if s['id']==plan['project']),{})
        require(scope.get('kind')=='project' and Path(scope.get('path','')).resolve()==Path(plan['workspace']),'current_project_workspace_changed')
        target=ipc(plan,'get',memory=plan['memory'])
        require(target.get('dataset')==plan['project'] and target.get('revision')==plan['revision'] and target.get('origin')!='raw' and target.get('state')=='active' and target.get('index_state')=='indexed','target_must_be_exact_active_indexed_knowledge')
        require(0<len(target.get('text','').encode())<=8192,'synthetic_target_body_unbounded')
        plan['target_text_hash']=hashlib.sha256(target['text'].encode()).hexdigest()
        query=target['text'][:256];operation='daily-proof-'+uuid.uuid4().hex
        expected_workspace=hashlib.sha256(json.dumps([plan['project'],str(Path(plan['workspace']).resolve())],ensure_ascii=False,separators=(',',':')).encode()).hexdigest()
        before=durable(plan);usage_before=ipc(plan,'usage',memory=plan['memory'])
        prompt='Memlane synthetic daily usage proof '+operation+'。仅调用Memlane工具，不读文件、不运行命令、不保存或修改知识。严格依次：1 memory_search '+json.dumps({'scope':plan['project'],'query':query,'limit':10},ensure_ascii=False)+'；2 memory_get '+json.dumps({'scope':plan['project'],'memory_id':plan['memory']})+'；3 memory_adopt，scope='+plan['project']+'，memory_id='+plan['memory']+'，revision='+str(plan['revision'])+'，operation_id='+operation+'，exposure_receipt必须使用实际memory_get返回usage_receipt.id；4 用完全相同参数再次memory_adopt验证幂等。不要以文字代替任何工具调用。'
        intent=dict(version=1,operation_id=operation,started_ms=report['started_ms'],intent_persisted_ms=int(time.time()*1000),connection=plan['connection'],generation=plan['generation'],project=plan['project'],memory=plan['memory'],revision=plan['revision'],workspace_identity=expected_workspace,target_text_hash=plan['target_text_hash'],query_hash=hashlib.sha256(query.encode()).hexdigest(),allowed_tools=list(TOOLS),guard_hash=plan['guard_hash'],installed_bridge_hash=plan['installed_bridge_hash'])
        report['run_diagnostics']=dict(stage='intent_persist',process_started=False,launch_attempted=False)
        persist_execution_intent(output,intent)
        report['intent']=intent
        report['model_attempted']=True;report['model_called']=None
        calls=run_pi(plan,prompt,operation,query,report['run_diagnostics'])
        receipts=verify_calls(calls,plan,operation,query)
        report['model_called']=True
        require(all(r['workspace_identity']==expected_workspace for r in receipts.values()),'trusted_workspace_identity_mismatch')
        after=durable(plan);rows=verify_durable(before,after,receipts,plan)
        current=ipc(plan,'get',memory=plan['memory']);require(current['revision']==plan['revision'] and current['dataset']==plan['project'] and hashlib.sha256(current['text'].encode()).hexdigest()==plan['target_text_hash'],'target_changed_during_proof')
        usage_after=ipc(plan,'usage',memory=plan['memory'])
        for key in ('reads','returns','reported_adoptions'):require(usage_after['daily_agent'][key]==usage_before['daily_agent'][key]+1,'usage_aggregate_delta_mismatch')
        require(usage_after['connection_verification']==usage_before['connection_verification'],'verification_channel_contaminated')
        activity=ipc(plan,'home_activity',home_activity={'project':plan['project'],'limit':50})
        event_ids={'usage:'+r['id']+':'+plan['memory']+':'+str(plan['revision']) for r in rows}
        require(event_ids.issubset({e['id'] for e in activity['items']}),'home_feed_missing_exact_durable_events')
        report.update(status='passed',calls=calls,receipts=receipts,durable_events=rows,usage_before=usage_before,usage_after=usage_after,home_event_ids=sorted(event_ids),knowledge_unchanged=True,model_text_used_as_evidence=False,processing_session_used=False,process_group_gone=True)
    except Exception as error:
        # Bounded codes only: no raw Agent/provider stderr or auth configuration.
        report['failure_code']=str(error) if isinstance(error,ValueError) and str(error).replace('_','').isalnum() else type(error).__name__
        report['failure_diagnostic']=report.get('run_diagnostics',{}).get('primary_failure') or safe_failure(error,report.get('run_diagnostics',{}))
        report['failure_code']=report['failure_diagnostic']['code']
    finally:
        for key,check in [('original_pi_unchanged',lambda:source_hashes(Path(plan['original_pi']))==plan['original_hashes']),('isolated_pi_unchanged',lambda:source_hashes(Path(plan['pi_directory']))==plan['isolated_hashes']),('extension_unchanged',lambda:digest(Path(plan['extension']))==plan['extension_hash'])]:
            try:report[key]=check()
            except Exception:report[key]=False
        if not all(report[k] for k in ('original_pi_unchanged','isolated_pi_unchanged','extension_unchanged')):report['status']='failed';report['failure_code']='source_configuration_changed'
        path=output/'report.json';fd=os.open(path,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600)
        with os.fdopen(fd,'w') as handle:json.dump(report,handle,ensure_ascii=False,indent=2);handle.write('\n')
    print(json.dumps({'status':report['status'],'model_called':report['model_called'],'report':str(output/'report.json'),'native_ui_tested':False}))
    return 0 if report['status']=='passed' else 1


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--evidence',required=True,type=Path);parser.add_argument('--memory',required=True);parser.add_argument('--revision',required=True,type=int);parser.add_argument('--connection',required=True);parser.add_argument('--project',required=True);parser.add_argument('--workspace',required=True,type=Path);parser.add_argument('--generation',type=int,required=True);parser.add_argument('--inspect',action='store_true');parser.add_argument('--execute',action='store_true');parser.add_argument('--out',type=Path)
    args=parser.parse_args();plan=prepare(args.evidence.resolve(),args.memory,args.revision,args.generation,args.connection,args.project,args.workspace)
    if args.inspect: plan=inspect(plan)
    if not args.execute:print(json.dumps(plan,ensure_ascii=False,indent=2));return 0
    require(args.out is not None,'explicit_new_report_directory_required')
    return execute(plan,args.out.resolve())

if __name__=='__main__':raise SystemExit(main())
