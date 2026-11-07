#!/usr/bin/env python3
"""Audit current P1 requirements against real reports, stopped data and package."""
import fcntl,hashlib,json,os,plistlib,socket,sqlite3,subprocess
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1];out=ROOT/'build/p1';report={'status':'failed','requirements':[],'artifacts':[]}
def artifact(path):
    p=ROOT/path;assert p.is_file(),path
    report['artifacts'].append({'path':path,'sha256':hashlib.sha256(p.read_bytes()).hexdigest()})
    return p
try:
    paths=['build/p1-screenshots/visual-verification.json','build/p1/service-verification.json','build/p1/library-ui-verification.json','build/p1/original-version-regression.json','build/p1/management-verification.json','build/p1/states-verification.json','build/p1/startup-verification.json','build/p1/final-window-verification.json']
    reports={path:json.loads(artifact(path).read_text()) for path in paths}
    for path,result in reports.items():assert result['status']=='passed',(path,result)
    visual=reports[paths[0]];assert visual['home_wide'] and visual['in_pane_history_restore'];assert visual['three_column_bounds']['detail-pane']['x']==640
    service=reports[paths[1]];assert len(service['checks'])==6 and all(x['status']=='passed' for x in service['checks'])
    regression=reports[paths[3]];assert len(regression['steps'])==5 and all(x['status']=='passed' for x in regression['steps'])
    managed=reports[paths[4]];assert len(managed['steps'])==6 and all(x['status']=='passed' for x in managed['steps'])
    states=reports[paths[5]];assert len(states['steps'])==6 and all(x['status']=='passed' for x in states['steps'])
    availability=json.loads(artifact('build/p1/availability-verification.json').read_text());assert availability['status']=='passed' and availability['worker_unavailability_displayed'] and availability['activity_unchanged']==5
    tests=json.loads(artifact('build/p1/final-checks.json').read_text());assert len(tests)==2 and all(x['exit_code']==0 for x in tests)
    for item in tests:artifact(item['log'])
    root=Path((out/'fixture-path.txt').read_text().strip());assert 'memlane-p1-app-' in str(root);cfg=json.loads((root/'runtime-config.json').read_text());client=Path(cfg['root']);data=client/'data'
    assert json.loads((data/'service-state.json').read_text())['stopped']
    ipc=f"/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock";assert not Path(ipc).exists()
    with (data/'.service.lock').open('r+b') as ownership:
        fcntl.flock(ownership,fcntl.LOCK_EX|fcntl.LOCK_NB)
        wal=data/'memlane.sqlite-wal';assert not wal.exists() or wal.stat().st_size==0
        with sqlite3.connect((data/'memlane.sqlite').as_uri()+'?mode=ro&immutable=1',uri=True) as db:
            assert db.execute('PRAGMA integrity_check').fetchone()[0]=='ok';assert db.execute('PRAGMA user_version').fetchone()[0]==1
            current=db.execute('SELECT current_revision,dataset,state FROM memories WHERE id=?',(managed['main_memory'],)).fetchone();assert current==(3,managed['project'],'active')
            versions=db.execute('SELECT revision,text,operation FROM versions WHERE memory=? ORDER BY revision',(managed['main_memory'],)).fetchall();assert [v[0] for v in versions]==[1,2,3];assert versions[0][1]==versions[2][1]==managed['main_original'];assert versions[1][1]==managed['main_edited'];assert versions[2][2]=='restore'
            count,distinct=db.execute("SELECT count(*),count(DISTINCT memory) FROM activities WHERE kind='created'").fetchone();assert count==distinct==states['final_activity_total']==5
            deleted=db.execute("SELECT count(*) FROM activities a JOIN memories m ON m.id=a.memory WHERE a.kind='created' AND m.state='deleted'").fetchone()[0];assert deleted==1
            report['database']={'schema_version':1,'integrity_check':'ok','main_current_revision':3,'history_revisions':[1,2,3],'created_events':count,'deleted_creation_facts':deleted,'owner_stopped':True}
    worker=Path(json.loads((client/'service.json').read_text())['worker']['Executable']);assert worker.is_file();assert not worker.with_name(worker.name+'.p1-disabled').exists()
    sizes=json.loads(artifact('build/p1/final-sizes.json').read_text());dmg=artifact('build/p0-final/darwin-arm64/memlane-0.0.1-arm64.dmg');assert dmg.stat().st_size==sizes['dmg_bytes'];assert hashlib.sha256(dmg.read_bytes()).hexdigest()==sizes['dmg_sha256']
    app=ROOT/'build/p0-final/darwin-arm64/Memlane.app';plist=plistlib.loads((app/'Contents/Library/LaunchAgents/io.github.zwhu.memlane.service.plist').read_bytes());assert (app/plist['BundleProgram']).is_file()
    assert subprocess.run(['codesign','--verify','--deep','--strict',str(app)],capture_output=True).returncode==0
    assert sum(p.stat().st_size for p in app.rglob('*') if p.is_file())==sizes['app_file_bytes']
    for p in app.rglob('*'):assert p.name not in ['model.onnx','tokenizer.json','memlane-cognee-worker','runtime-config.json']
    for path in ['docs/images/home.jpg','docs/images/memory-detail.jpg','docs/images/p1-home.png','docs/images/p1-detail.png']:
        artifact(path)
    for path in ['build/p1-screenshots/system-final-real-home-capture.json','build/p1-screenshots/system-final-real-detail-capture.json']:
        capture=json.loads(artifact(path).read_text());assert capture['window_key'] and capture['visible'] and capture['actual_system_window']
    plan=(ROOT/'docs/implementation-plan.md').read_text();p1=plan[plan.index('## P1:') if '## P1:' in plan else plan.index('## P1：'):plan.index('## P2：')];assert '- [ ]' not in p1
    mapping=[('首页宽幅、三栏与栏内操作',paths[0]),('迁移保留正文版本操作任务与绑定',paths[1]),('分页、范围、来源、历史、索引状态',paths[1]),('App项目登记与真实读取',paths[2]),('旧版本排除和迟到索引保护',paths[3]),('App新增编辑搜索历史恢复重启',paths[4]),('过期编辑不覆盖与草稿保留重试',paths[4]),('真实热力图最近新增与去重',paths[5]),('空态准备中索引失败与真实重试',paths[5]),('删除活动事实与日期查看',paths[5]),('原地重建后正常App启动',paths[6]),('最终实际窗口与主条目历史',paths[7]),('相关Go测试与静态检查','build/p1/final-checks.json'),('完整App helper LaunchAgent DMG及三类体积','build/p1/final-sizes.json')]
    report['requirements']=[{'requirement':name,'evidence':evidence,'status':'proven'} for name,evidence in mapping]
    report['sizes']=sizes;report['commit']=subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT,text=True).strip();report['status']='passed';print(json.dumps({'status':'passed','proven_requirements':len(mapping),'created_events':count,'main_revision':3,'dmg_bytes':sizes['dmg_bytes']},ensure_ascii=False))
except Exception as error:report['error']=repr(error);raise
finally:(out/'final-audit.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
