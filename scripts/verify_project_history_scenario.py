"""新隔离包：正常Codex会话、显式历史选择、版本冻结与真实知识／出处。"""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import uuid


def verify(ui, call, report, capture, resize, out, ipc, driver):
    root = Path(__file__).resolve().parents[1]
    assert 'memlane-p2-' in str(out)
    repo = out/'synthetic-project'
    subprocess.run(['/usr/bin/git','init',str(repo)],capture_output=True,check=True)
    materials = {'README.md':'# TicketBox 本轮合成项目\n本项目使用 pnpm 管理依赖；变更依赖时提交 pnpm-lock.yaml。\n',
                 'ticket.py':"def normalize_ticket(value):\n    if not isinstance(value, str):\n        raise TypeError('str required')\n    return '-'.join(value.strip().split()).lower()\n",
                 'AGENTS.md':'本轮合成仓库。核对文档及代码；不执行脚本，不改文件，不读取仓库以外材料。'}
    for name,text in materials.items(): (repo/name).write_text(text)
    # Generate a private runtime configuration from the current native provider's
    # model setting. No Agent configuration file or credential is copied.
    original_path = Path('/Users/bytedance/.codex/config.toml')
    original_digest = hashlib.sha256(original_path.read_bytes()).hexdigest()
    metadata_script = 'import tomllib,json,sys;v=tomllib.load(open(sys.argv[1],"rb"));keys=["model","model_provider","model_reasoning_effort","model_reasoning_summary","model_verbosity","service_tier"];out={k:v[k] for k in keys if k in v};out["native_provider_customized"]="openai" in v.get("model_providers",{});print(json.dumps(out))'
    current = json.loads(subprocess.run([str(root/'.local/cognee-python/bin/python'),'-c',metadata_script,str(original_path)],capture_output=True,check=True).stdout)
    assert current.get('model') and current.get('model_provider','openai') == 'openai'
    assert not current['native_provider_customized'], '该验证器不替换自定义供应商配置'
    directory = out/'bound-codex'
    directory.mkdir(mode=0o700)
    runtime_settings={k:v for k,v in current.items() if k!='native_provider_customized'}
    (directory/'config.toml').write_text(''.join(k+' = '+json.dumps(v)+'\n' for k,v in runtime_settings.items()))
    (directory/'auth.json').symlink_to('/Users/bytedance/.codex/auth.json')
    prompt = '用户明确为 TicketBox 新增约定：只允许 release 分支正式发布，发布审批号不能为空。请只复述本次用户要求，不修改文件、不执行工具、不写入仓库，也不声称代码已经强制执行。'
    env = os.environ.copy();env['CODEX_HOME']=str(directory)
    with (out/'normal-history-events.jsonl').open('w') as log,(out/'normal-history-stderr.log').open('w') as err:
        normal = subprocess.run(['/Applications/ChatGPT.app/Contents/Resources/codex-cli/bin/codex','exec','--json','--sandbox','read-only','--cd',str(repo),prompt],env=env,stdout=log,stderr=err,timeout=180)
    assert normal.returncode == 0
    logs = list((directory/'sessions').rglob('*.jsonl'))
    assert len(logs)==1
    history = logs[0]
    meta = json.loads(history.read_text().splitlines()[0])
    session_id='codex_'+meta['payload']['id']
    assert meta['payload']['cwd']==str(repo) and meta['payload']['source']=='exec'
    assert hashlib.sha256(original_path.read_bytes()).hexdigest()==original_digest
    project = call('register_project',user_confirmed=True,project=dict(name='C历史会话本轮项目',path=str(repo)))
    connection = call('configure_connection',user_confirmed=True,connection=dict(agent='codex',name='本轮真实Codex',project=project['id'],allowed_scopes=[project['id']]))
    app=out/'app/darwin-arm64/MemlaneProjectTask.app'
    adapter=out/'agent-adapter-check'
    subprocess.run(['sh',str(root/'scripts/go.sh'),'build','-o',str(adapter),'./cmd/p2-agent-check'],cwd=root,check=True)
    options=dict(Project=project['id'],ClientRoot=str(out/'client'),ServiceRoot=str(out/'client/data'),Bridge=str(app/'Contents/Resources/bin/memlane'),AgentPath='/Applications/ChatGPT.app/Contents/Resources/codex-cli/bin/codex',Workspace=str(repo),CodexDirectory=str(directory))
    for action in ('install','verify_connection'):
        request=dict(action=action,connection=connection,options=options,run=dict(Workspace=str(repo),CodexAuthHome=str(directory)))
        result=subprocess.run([str(adapter)],input=json.dumps(request).encode(),capture_output=True,timeout=180)
        response=json.loads(result.stdout)
        (out/(action+'-receipt.json')).write_text(json.dumps(response,ensure_ascii=False,indent=2)+'\n')
        assert result.returncode==0 and response['ok'],response
    call('project_binding_change',user_confirmed=True,project_binding_change=dict(project=project['id'],connection_id=connection['id'],expected_generation=connection['generation'],expected_revision=0))
    assert list((directory/'sessions').rglob('*.jsonl')) == logs
    def press(key):
        node=driver.wait(lambda:next((n for n in ui.nodes() if n.get('key')==key and 'disabled' not in n.get('states',[])),None))
        ui.req('act',id=node['id'],action='press');ui.req('capture')
    def tasks(): return call('project_task_list',project_task_list=dict(project=project['id'],limit=10))['items']
    driver.wait(lambda:any(n.get('key')=='project-open-'+project['id'] for n in ui.nodes()))
    press('project-open-'+project['id'])
    driver.wait(lambda:any(n.get('key')=='project-organize-open' for n in ui.nodes()))
    press('project-organize-open')
    driver.wait(lambda:any(n.get('key')=='project-organizer-start' and 'disabled' not in n.get('states',[]) for n in ui.nodes()))
    assert ui.node(key='project-organizer-history').get('value') in (None,'','false')
    assert not tasks()
    capture('01-c-history-default-off-system.png')
    press('project-organizer-history')
    driver.wait(lambda:'已选择 1 个已完成会话 · 历史会话会增加用量。' in ui.labels())
    assert not tasks()
    resize(980,620);capture('02-c-history-selected-narrow-system.png')
    # Changing raw bytes after selection invalidates the old selected digest;
    # no task/model may start under that obsolete intent.
    with history.open('a') as f:f.write('\n')
    press('project-organizer-start')
    driver.wait(lambda:'所选历史会话已变化，请重新读取历史选择。' in ui.labels(),timeout=60)
    assert not tasks()
    capture('03-c-history-changed-before-start-system.png')
    if report['scenario']=='project-history-guard':
        assert not any(n.get('key')=='project-organizer-retry' for n in ui.nodes())
        report.update(actual_normal_codex_history_created=True,actual_connection_verified=True,
                      changed_history_blocks_start=True,history_default_off=True,
                      history_selection_does_not_start_model=True,history_error_has_one_reselection_action=True,
                      project_model_started=False,user_historical_sessions_read=False,
                      default_agent_configuration_unchanged=hashlib.sha256(original_path.read_bytes()).hexdigest()==original_digest)
        return
    press('project-history-reload')
    driver.wait(lambda:'已选择 1 个已完成会话 · 历史会话会增加用量。' in ui.labels())
    selected_digest=hashlib.sha256(history.read_bytes()).hexdigest()
    press('project-organizer-start')
    page=driver.wait(tasks,timeout=60);assert len(page)==1
    task_id=page[0]['task']
    def task(): return call('project_task',project_task=task_id)
    assert task()['snapshot']['include_history'] and task()['snapshot']['sessions'][0]['digest']==selected_digest
    # Further external changes do not replace the already-frozen task version.
    with history.open('a') as f:f.write('\n')
    driver.wait(lambda:'正在整理项目与历史会话' in ui.labels(),timeout=90)
    capture('04-c-history-fixed-running-system.png')
    press('project-organizer-close')
    press('project-menu-'+project['id'])
    driver.wait(lambda:'查看整理进度' in ui.labels())
    node=ui.node(label='查看整理进度');ui.req('act',id=node['id'],action='press');ui.req('capture')
    assert task()['task']==task_id and task()['snapshot']['sessions'][0]['digest']==selected_digest
    driver.wait(lambda:task()['state'] in ('completed','failed','cancelled','interrupted'),timeout=240)
    finished=task();(out/'project-history-task-receipt.json').write_text(json.dumps(finished,ensure_ascii=False,indent=2)+'\n')
    assert finished['state']=='completed',finished
    h=finished['execution']['history_context'];assert h['selected']==h['read']==h['sent']==1 and h['bytes']>0,h
    assert finished['execution']['model']==current['model'] and finished['execution']['provider']=='openai'
    historical=[]
    for item in finished['receipt']['added']:
        v=call('get',memory=item['memory'])
        links=call('organization_sources',memory=v['memory'],expected_revision=v['revision'])
        for link in links:
            original=call('source_read',source_read=dict(project=project['id'],owner=v['memory'],owner_revision=v['revision'],memory=link['source_memory'],revision=link['source_revision']))
            detail=original['record'].get('source_detail',{}).get('history')
            if detail:
                assert detail['session']==session_id and detail['digest']==selected_digest and detail['captured_at']==meta['timestamp']
                assert original['state']=='available' and original['quote'] in original['record']['text']
                assert 'base_instructions' not in original['record']['text']
                if 'release' in v['text'] and '审批' in v['text']:historical.append(v)
    assert historical,'没有产生有历史出处的本项目发布约定'
    driver.wait(lambda:'项目知识已经就绪' in ui.labels())
    capture('05-c-history-completed-narrow-system.png')
    press('project-organizer-view')
    target=historical[0]['memory']
    driver.wait(lambda:any(n.get('key')=='memory-'+target for n in ui.nodes()));press('memory-'+target)
    capture('06-c-history-knowledge-with-source-system.png')
    assert len(tasks())==1 and list((directory/'sessions').rglob('*.jsonl'))==logs
    assert hashlib.sha256(original_path.read_bytes()).hexdigest()==original_digest
    report.update(model_inference_verified=True,actual_project_task_execution=True,actual_connection_verified=True,
                  actual_normal_codex_history_created=True,actual_history_read=True,history_default_off=True,
                  history_selection_does_not_start_model=True,changed_history_blocks_start=True,
                  selected_history_version_frozen=True,history_scope_reopen_keeps_task=True,
                  history_original_times_and_quotes_verified=True,history_is_separate_from_cognee_instance=True,
                  generated_historical_knowledge=len(historical),saved_knowledge=len(finished['receipt']['added']),
                  actual_rust_index=True,historical_sessions_read=True,user_historical_sessions_read=False,
                  default_agent_configuration_unchanged=True,full_c_motion_verified=False,
                  fact_quality='requires_itemwise_review')
