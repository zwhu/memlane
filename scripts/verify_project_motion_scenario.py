"""C真实任务与停帧测量；独立标明的Native视觉夹具覆盖阶段矩阵。"""
import json
from pathlib import Path
import socket
import threading
import time
import uuid


def verify(ui, call, report, capture, resize, out, ipc, driver):
    from verify_project_task_scenario import verify as task_verify
    measurements, observations, errors = [], [], []
    stopped = threading.Event()
    threads, seen = [], set()

    def native(op, **fields):
        with socket.socket(socket.AF_UNIX) as s:
            s.settimeout(10);s.connect(ui.native_path)
            s.sendall((json.dumps(dict(op=op,**fields))+'\n').encode())
            value=json.loads(s.makefile('rb',buffering=0).readline())
        assert 'error' not in value,value
        return value

    def observe(task_id, attempt):
        version='0'*64
        try:
            while not stopped.is_set():
                with socket.socket(socket.AF_UNIX) as s:
                    s.settimeout(8);s.connect(str(ipc))
                    query=dict(protocol=1,id=str(uuid.uuid4()),op='project_task_watch',project_task_watch=dict(task=task_id,attempt=attempt,version=version))
                    s.sendall((json.dumps(query)+'\n').encode())
                    response=json.loads(s.makefile('rb',buffering=0).readline())
                assert response['ok'],response
                result=response['result'];task=result['task']
                observations.append(dict(observed_ms=int(time.time()*1000),task=task['task'],attempt=task['attempt'],state=task['state'],version=result['version']))
                version=result['version']
                if task['state'] not in ('queued','starting','organizing','saving','indexing','cancelling'):return
        except Exception as error:
            if not stopped.is_set():errors.append(repr(error))

    def observed_call(op, **fields):
        result=call(op,**fields)
        if op=='project_task_list':
            for task in result['items']:
                if task['task'] not in seen:
                    seen.add(task['task'])
                    thread=threading.Thread(target=observe,args=(task['task'],task['attempt']),daemon=True)
                    threads.append(thread);thread.start()
        return result

    def stopped_frames(label, duration=.45):
        # The native probe reads counters without requesting a render. These
        # short waits measure visual settling, never an Agent workflow.
        if label != 'real-running-window-hidden': ui.req('idle',timeout=3000)
        before=native('motion');time.sleep(duration);after=native('motion')
        sample=dict(label=label,before=before,after=after,interval_ms=duration*1000)
        measurements.append(sample)
        counters=(label,dict(frames=before['frames'],requests=before['requests']),dict(frames=after['frames'],requests=after['requests']))
        # Ordinary data updates may cause a short repaint burst. The scene's
        # animation requests must stop; visible-window draw count is measured,
        # not mistaken for a promise that all other UI work has stopped.
        assert after['requests']==before['requests'],counters
        if label=='real-running-window-hidden':
            assert before['window_hidden'] and not before['task_watch_active'],counters
            assert after['frames']==before['frames'],counters
        return sample

    def checked_capture(name):
        if name=='02-c-running-system.png':
            before=native('motion')
            assert before['requests']>0,'运动舞台未请求帧'
            bounds=before['samples'][-1]['bounds']
            px=bounds[0]+bounds[2]*.85
            py=bounds[1]+bounds[3]*.75
            ui.req('input',events=[dict(type='pointerMove',x=px,y=py)])
            time.sleep(.4)
            tilted=native('motion')
            assert any(abs(s.get('TiltX',0))+abs(s.get('TiltY',0))>.05 for s in tilted['samples'][-30:]),'指针未进入舞台'
            ui.req('input',events=[dict(type='pointerMove',x=10,y=10)])
            time.sleep(.4)
            returned=native('motion')
            assert abs(returned['samples'][-1]['TiltX'])+abs(returned['samples'][-1]['TiltY'])<.05,'视差未回位'
            measurements.append(dict(label='real-pointer-tilt-return',tilted=tilted,returned=returned))
            assert native('close')['visible'] is False
            stopped_frames('real-running-window-hidden')
            assert native('restore')['visible'] is True
            report['actual_running_window_hidden_stops_frames']=True
            report['actual_pointer_tilt_and_return_verified']=True
        capture(name)
        if name in ('03b-c-reduced-reopened-running-system.png','04-c-cancelled-system.png','05-c-cancelled-narrow-system.png','06-c-completed-system.png','07-c-completed-narrow-system.png'):
            stopped_frames('real-'+name)
        measurements.append(dict(label='real-'+name,snapshot=native('motion')))

    visual_only=report['scenario']=='project-motion-visual'
    if not visual_only:
        try:
            task_verify(ui,observed_call,report,checked_capture,resize,out,ipc,driver)
        finally:
            stopped.set()
            for thread in threads:thread.join(timeout=6)
            (out/'actual-task-observations.json').write_text(json.dumps(dict(items=observations,errors=errors),ensure_ascii=False,indent=2)+'\n')
            (out/'actual-motion-measurements.json').write_text(json.dumps(measurements,ensure_ascii=False,indent=2)+'\n')
        assert not errors,errors
    stages=sorted({v['state'] for v in observations})
    if not visual_only:
        report['actual_task_watch_states']=stages
        report['actual_owner_state_notifications_verified']=True
        report['actual_saving_observed']='saving' in stages
        report['actual_indexing_observed']='indexing' in stages

    samples=list({(s['process'],s['frame']):s for m in measurements for q in ('snapshot','after','tilted') for s in m.get(q,{}).get('samples',[]) if s.get('cpu_ms',0)>=0}.values())
    cpu=sorted(s['cpu_ms'] for s in samples)
    summary=dict(scope='CPU绘制回调耗时与请求帧计数；不代表GPU或整页帧预算',count=len(cpu),p95_ms=cpu[int((len(cpu)-1)*.95)] if cpu else None,max_ms=max(cpu) if cpu else None)
    (out/'motion-performance-scope.json').write_text(json.dumps(summary,ensure_ascii=False,indent=2)+'\n')
    if not visual_only:report['actual_static_motion_request_stop_verified']=True
    report.update(motion_performance_scope=summary['scope'],full_c_motion_verified=False)
    if report['scenario']=='project-motion-task':
        return

    # Explicit fixture state is confined to the automation bundle and has no
    # service client or model permission. It exercises exactly the same scene
    # and controls, but these frames do not prove business-state transitions.
    matrix=[]
    record_count=call('list',list_query=dict(limit=1))['total']
    task_ids={v['task'] for v in call('project_task_list',project_task_list=dict(limit=10))['items']}
    states=('ready','starting','organizing','saving','indexing','completed','failed','cancelling','cancelled')
    for reduced in (False,True):
        for width,height in ((1200,820),(980,620)):
            resize(width,height)
            for stage in states:
                marker=native('motion-fixture',stage=stage,reduced=reduced)
                assert marker['visual_fixture_only']
                ui.req('capture')
                time.sleep(.65 if not reduced and stage in ('starting','saving','completed') else .1)
                name='visual-c-'+stage+'-'+str(width)+('-reduced' if reduced else '-regular')+'-system.png'
                capture(name)
                if reduced or stage in ('failed','cancelling','cancelled'):
                    stopped_frames('visual-'+name,duration=.2)
                sample=native('motion')
                matrix.append(dict(stage=stage,reduced=reduced,width=width,height=height,screenshot=name,probe=sample))
                print(json.dumps(dict(visual_motion_stage=stage,width=width,reduced=reduced)),flush=True)
    (out/'visual-motion-matrix.json').write_text(json.dumps(dict(visual_fixture_only=True,items=matrix),ensure_ascii=False,indent=2)+'\n')
    assert call('list',list_query=dict(limit=1))['total']==record_count
    assert {v['task'] for v in call('project_task_list',project_task_list=dict(limit=10))['items']}==task_ids
    if visual_only:
        cpu=sorted({(s['process'],s['frame']):s['cpu_ms'] for m in matrix for s in m['probe']['samples']}.values())
        summary.update(count=len(cpu),p95_ms=cpu[int((len(cpu)-1)*.95)] if cpu else None,max_ms=max(cpu) if cpu else None,visual_fixture_only=True)
        (out/'motion-performance-scope.json').write_text(json.dumps(summary,ensure_ascii=False,indent=2)+'\n')
        (out/'visual-motion-measurements.json').write_text(json.dumps(measurements,ensure_ascii=False,indent=2)+'\n')
        report.update(project_model_started=False,visual_fixture_only=True)
    report.update(synthetic_static_motion_request_stop_verified=True,synthetic_native_motion_matrix_verified=True,
                  synthetic_motion_windows=len(matrix),motion_performance_scope=summary['scope'],full_c_motion_verified=False,visual_fixture_keeps_business_counts=True)
