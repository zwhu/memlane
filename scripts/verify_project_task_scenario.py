"""实际包内C任务、真实Codex连接验证及Rust保存/索引；不冒称完整C舞台。"""
import hashlib
import json
from pathlib import Path
import subprocess
import uuid


def verify(ui, call, report, capture, resize, out, ipc, driver):
    root = Path(__file__).resolve().parents[1]
    assert 'memlane-p2-' in str(out), '隔离适配器要求新memlane-p2证据根'
    client = out/'client'
    app = out/'app/darwin-arm64/MemlaneProjectTask.app'
    executable = Path('/Applications/ChatGPT.app/Contents/Resources/codex-cli/bin/codex')
    auth = Path('/Users/bytedance/.codex')
    adapter = out/'agent-adapter-check'
    subprocess.run(['sh', str(root/'scripts/go.sh'), 'build', '-o', str(adapter), './cmd/p2-agent-check'], cwd=root, check=True)
    repo = out/'synthetic-project'
    subprocess.run(['/usr/bin/git', 'init', str(repo)], capture_output=True, check=True)
    materials = {'README.md': '# TicketBox 本轮合成项目\n\n本项目使用 pnpm 管理依赖；依赖变化时提交 pnpm-lock.yaml。\n知识只适用于 TicketBox，不推断其他仓库。\n',
                 'ticket.py': "def normalize_ticket(value):\n    if not isinstance(value, str):\n        raise TypeError('str required')\n    return '-'.join(value.strip().split()).lower()\n",
                 'AGENTS.md': '这是本轮合成仓库。核对README及代码，不执行项目脚本或编造测试结果。'}
    for name, text in materials.items(): (repo/name).write_text(text)
    project = call('register_project', user_confirmed=True, project=dict(name='C真实任务本轮项目', path=str(repo)))
    connection = call('configure_connection', user_confirmed=True, connection=dict(agent='codex', name='本轮真实Codex', project=project['id'], allowed_scopes=[project['id']]))
    options = dict(Project=project['id'], ClientRoot=str(client), ServiceRoot=str(client/'data'),
                   Bridge=str(app/'Contents/Resources/bin/memlane'), AgentPath=str(executable), Workspace=str(repo), CodexDirectory=str(auth))

    def adapter_call(action):
        request = dict(action=action, connection=connection, options=options,
                       run=dict(Workspace=str(repo), CodexAuthHome=str(auth)))
        result = subprocess.run([str(adapter)], input=json.dumps(request).encode(), capture_output=True, timeout=180)
        response = json.loads(result.stdout)
        (out/(action+'-receipt.json')).write_text(json.dumps(response, ensure_ascii=False, indent=2)+'\n')
        assert result.returncode == 0 and response['ok'], response
        return response['result']

    adapter_call('install')
    verified = adapter_call('verify_connection')
    assert verified['calls'] and verified['process_group_gone']
    report['actual_connection_verified'] = True
    binding = call('project_binding_change', user_confirmed=True, project_binding_change=dict(project=project['id'], connection_id=connection['id'], expected_generation=connection['generation'], expected_revision=0))
    draft = dict(operation_id=str(uuid.uuid4()), project=project['id'], binding_revision=binding['revision'], include_history=False, sessions=[])
    task = call('project_task_start', user_confirmed=True, project_task_start=draft)
    replay = call('project_task_start', user_confirmed=True, project_task_start=draft)
    assert replay['task'] == task['task'] and replay['attempt'] == task['attempt']
    driver.wait(lambda: call('project_task', project_task=task['task'])['state'] in ('completed', 'failed', 'cancelled', 'interrupted'), timeout=240)
    finished = call('project_task', project_task=task['task'])
    (out/'project-task-receipt.json').write_text(json.dumps(finished, ensure_ascii=False, indent=2)+'\n')
    assert finished['state'] == 'completed', finished
    execution, receipt = finished['execution'], finished['receipt']
    assert execution['terminal_completed'] and execution['process_reaped'] and execution['process_group_gone']
    assert receipt['persisted'] and receipt['added'] and receipt['indexed'] == len(receipt['added'])
    assert receipt['index_failed'] == 0 and receipt['source_index_failed'] == 0
    sources = {saved['memory']: call('get', memory=saved['memory']) for saved in receipt['sources']}
    assert all(v['origin'] == 'raw' and v['index_state'] == 'indexed' for v in sources.values())
    for saved in receipt['added']:
        v = call('get', memory=saved['memory'])
        assert v['origin'] == 'knowledge' and v['index_state'] == 'indexed'
        provenance = call('organization_sources', memory=v['memory'], expected_revision=v['revision'])
        assert provenance, provenance
        for source in provenance:
            q = dict(project=project['id'], owner=v['memory'], owner_revision=v['revision'], memory=source['source_memory'], revision=source['source_revision'])
            original = call('source_read', source_read=q)
            assert original['state'] == 'available' and original['record']['origin'] == 'raw'
            assert original['quote'] and original['quote'] in original['record']['text']
            assert original['record']['text'] in materials.values()
    report.update(actual_project_task_execution=True, model_inference_verified=True, actual_rust_index=True,
                  actual_repository_sources_saved=True, source_quote_version_verified=True,
                  current_bound_model=execution['model'], saved_knowledge=len(receipt['added']),
                  saved_repository_sources=len(receipt['sources']), historical_sessions_read=False,
                  full_c_motion_verified=False, task_start_control_native_verified=False,
                  fact_quality='requires_itemwise_review')
    ui.press_key('project-open-'+project['id'])
    selected = receipt['added'][0]['memory']
    driver.wait(lambda: any(n.get('key') == 'memory-'+selected for n in ui.nodes()))
    ui.press_key('memory-'+selected)
    driver.wait(lambda: '知识详情' in ui.labels())
    capture('01-c-generated-knowledge-system.png')
    resize(980, 620)
    capture('02-c-generated-knowledge-narrow-system.png')
    report['native_generated_knowledge_lineage_verified'] = True
    hashes = {name: hashlib.sha256((repo/name).read_bytes()).hexdigest() for name in materials}
    assert hashes == {name: hashlib.sha256(text.encode()).hexdigest() for name, text in materials.items()}
    report['repository_materials_unchanged'] = True
