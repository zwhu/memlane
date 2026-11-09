"""新隔离包验证真实Codex读取当前知识、无新增与仓库变化后的补充。"""
import hashlib
import json
from pathlib import Path

from verify_project_task_scenario import verify as base_verify


def verify(ui, call, report, capture, resize, out, ipc, driver):
    base_verify(ui, call, report, capture, resize, out, ipc, driver)
    repo=out/'synthetic-project'
    project=next(p for p in call('scopes') if p.get('path')==str(repo))
    items=call('list',list_query=dict(kind='knowledge',datasets=[project['id']],limit=100))['items']
    assert items and len(items)<=20
    baseline={v['memory']:dict(revision=v['revision'],title=v['title'],text=v['text']) for v in items}

    def press(key):
        n=ui.node(key=key)
        assert 'disabled' not in n.get('states',[]),n
        ui.req('act',id=n['id'],action='press')
        ui.req('capture')

    def all_tasks():
        return call('project_task_list',project_task_list=dict(project=project['id'],limit=20))['items']

    def supplement(name):
        before={t['task'] for t in all_tasks()}
        resize(1200,820)
        press('project-menu-'+project['id'])
        driver.wait(lambda:'补充项目知识' in ui.labels())
        n=ui.node(label='补充项目知识')
        ui.req('act',id=n['id'],action='press')
        driver.wait(lambda:any(n.get('key')=='project-organizer-start' and 'disabled' not in n.get('states',[]) for n in ui.nodes()))
        capture(name+'-ready-system.png')
        press('project-organizer-start')
        new=driver.wait(lambda:[t for t in all_tasks() if t['task'] not in before],timeout=60)
        assert len(new)==1
        task_id=new[0]['task']
        driver.wait(lambda:call('project_task',project_task=task_id)['state'] in ('completed','failed','cancelled','interrupted'),timeout=240)
        task=call('project_task',project_task=task_id)
        (out/(name+'-receipt.json')).write_text(json.dumps(task,ensure_ascii=False,indent=2)+'\n')
        assert task['state']=='completed',task
        proof=task['execution']['knowledge_context']
        assert proof['complete'] and proof['pages']==proof['sent_pages']==1 and proof['items']==len(baseline),proof
        current=[]
        for memory,v in sorted(baseline.items()):
            current.append(dict(memory=memory,revision=v['revision'],title=v['title'],text=v['text']))
        body=json.dumps(dict(items=current,total=len(current)),ensure_ascii=False,separators=(',',':'))
        for char,escaped in [('<','\\u003c'),('>','\\u003e'),('&','\\u0026'),('\u2028','\\u2028'),('\u2029','\\u2029')]:
            body=body.replace(char,escaped)
        assert proof['digest']==hashlib.sha256(body.encode()).hexdigest(),proof
        for memory,v in baseline.items():
            after=call('get',memory=memory)
            assert after['revision']==v['revision'] and after['title']==v['title'] and after['text']==v['text']
        driver.wait(lambda:'项目知识已经就绪' in ui.labels() or '本次没有新增知识' in ui.labels())
        capture(name+'-completed-system.png')
        return task

    no_new=supplement('10-c-current-knowledge')
    assert no_new['receipt']['added']==[],no_new
    assert '本次没有新增知识' in ui.labels()
    report.update(actual_current_knowledge_context_read=True,context_digest_matches_current_versions=True,
                  unchanged_repository_adds_no_duplicate=True,current_knowledge_preserved=True)
    press('project-organizer-view')
    release='TicketBox 发布约定：仅 release 分支可以执行正式发布；其他分支只能验证，不执行正式发布。\n'
    (repo/'RELEASE.md').write_text(release)
    task=supplement('11-c-supplement-new-source')
    added=task['receipt']['added']
    assert len(added)==1,task
    new=call('get',memory=added[0]['memory'])
    assert 'release' in new['text'] and new['origin']=='knowledge' and new['index_state']=='indexed'
    links=call('organization_sources',memory=new['memory'],expected_revision=new['revision'])
    originals=[call('source_read',source_read=dict(project=project['id'],owner=new['memory'],owner_revision=new['revision'],memory=s['source_memory'],revision=s['source_revision'])) for s in links]
    assert any(v['record']['text']==release and v['quote'] in release for v in originals)
    assert call('list',list_query=dict(kind='knowledge',datasets=[project['id']]))['total']==len(baseline)+1
    resize(980,620)
    capture('12-c-supplement-completed-narrow-system.png')
    press('project-organizer-view')
    driver.wait(lambda:any(n.get('key')=='memory-'+new['memory'] for n in ui.nodes()))
    press('memory-'+new['memory'])
    capture('13-c-supplement-new-knowledge-system.png')
    report.update(changed_repository_adds_only_new_knowledge=True,new_knowledge_has_new_repository_source=True,
                  supplemented_knowledge=len(added),prior_knowledge_count=len(baseline),historical_sessions_read=False)
