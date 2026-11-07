#!/usr/bin/env python3
"""Exercise real P1 private IPC, schema migration and worker on synthetic data."""
import argparse,hashlib,json,os,signal,socket,sqlite3,subprocess,tempfile,time,uuid
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]
from importlib.machinery import SourceFileLoader
driver=SourceFileLoader('p1ui',str(ROOT/'scripts/p1-ui.py')).load_module()

def main():
    p=argparse.ArgumentParser(description=__doc__);p.add_argument('--daemon',type=Path,default=ROOT/'build/p1/memlaned');a=p.parse_args()
    out=ROOT/'build/p1';out.mkdir(parents=True,exist_ok=True)
    report={'status':'failed','synthetic_only':True,'checks':[]};proc=None
    with tempfile.TemporaryDirectory(prefix='memlane-p1-service-') as temporary:
        root=Path(temporary);data=root/'data';data.mkdir();db=data/'memlane.sqlite'
        dataset,memory=str(uuid.uuid4()),str(uuid.uuid4())
        legacy={'operation_id':'legacy-save','kind':'save','dataset':dataset,'memory':memory,'expected_revision':0,'text':'legacyOriginalToken original'}
        edit={**legacy,'operation_id':'legacy-edit','kind':'edit','expected_revision':1,'text':'legacyCurrentToken updated'}
        with sqlite3.connect(db) as c:
            c.executescript("""
CREATE TABLE memories(id TEXT PRIMARY KEY,dataset TEXT NOT NULL,current_revision INTEGER NOT NULL,indexed_revision INTEGER NOT NULL DEFAULT 0,generation TEXT NOT NULL DEFAULT '',state TEXT NOT NULL);
CREATE TABLE versions(memory TEXT NOT NULL REFERENCES memories(id),revision INTEGER NOT NULL,text TEXT NOT NULL,operation TEXT NOT NULL,created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,PRIMARY KEY(memory,revision));
CREATE TABLE operations(id TEXT PRIMARY KEY,digest TEXT NOT NULL,memory TEXT NOT NULL,revision INTEGER NOT NULL);
CREATE TABLE jobs(memory TEXT NOT NULL,revision INTEGER NOT NULL,state TEXT NOT NULL,error TEXT NOT NULL DEFAULT '',PRIMARY KEY(memory,revision));
CREATE TABLE index_bindings(chunk_id TEXT NOT NULL,memory TEXT NOT NULL,revision INTEGER NOT NULL,generation TEXT NOT NULL,PRIMARY KEY(chunk_id,generation));
""")
            c.execute("INSERT INTO memories VALUES(?,?,2,2,'legacy-generation','active')",(memory,dataset))
            for revision,m in enumerate([legacy,edit],1):
                c.execute('INSERT INTO versions VALUES(?,?,?,?,?)',(memory,revision,m['text'],m['kind'],'2025-03-04 05:06:07'))
                digest=hashlib.sha256(json.dumps(m,ensure_ascii=False,separators=(',',':')).encode()).hexdigest()
                c.execute('INSERT INTO operations VALUES(?,?,?,?)',(m['operation_id'],digest,memory,revision))
            c.execute("INSERT INTO jobs VALUES(?,2,'completed','')",(memory,))
            c.execute("INSERT INTO index_bindings VALUES(?, ?,2,'legacy-generation')",(str(uuid.uuid4()),memory))
            original={table:c.execute('SELECT * FROM '+table).fetchall() for table in ['memories','versions','operations','jobs','index_bindings']}
        # Component binaries/models are immutable P0 synthetic artifacts. Only
        # the service/index data root is new; no real user directory is opened.
        oldroot=Path((ROOT/'build/p0-engine/client-fixture-path.txt').read_text().strip())
        oldconfig=json.loads((oldroot/'client/service.json').read_text())
        config={**oldconfig,'root':str(data),'worker':{**oldconfig['worker'],'DataDir':str(data/'index'),'DenyNetwork':True},'retry_delay_ms':500}
        configpath=root/'service.json';configpath.write_text(json.dumps(config))
        ipc=f"/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock"
        def start():
            return subprocess.Popen([str(a.daemon.resolve()),'--config',str(configpath)],cwd=root,env={'PATH':'/usr/bin:/bin','HOME':str(root/'home')},stdout=subprocess.DEVNULL,stderr=(out/'service.log').open('a'))
        def call(op,ok=True,**fields):
            req={'protocol':1,'id':str(uuid.uuid4()),'op':op,**fields}
            with socket.socket(socket.AF_UNIX) as s:
                s.settimeout(30);s.connect(ipc);s.sendall((json.dumps(req)+'\n').encode())
                r=json.loads(s.makefile('rb',buffering=0).readline())
            assert r['ok']==ok,r
            return r.get('result') if ok else r
        def check(name,action):
            began=time.monotonic();result=action();entry={'name':name,'status':'passed','seconds':round(time.monotonic()-began,3),'evidence':result};report['checks'].append(entry);print(json.dumps(entry,ensure_ascii=False),flush=True)
            (out/'service-verification.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
        try:
            proc=start();driver.wait(lambda:call('health')['state']=='ready',timeout=65)
            def migration():
                v=call('get',memory=memory);assert v['revision']==2 and v['text']==edit['text'] and v['original']==legacy['text'];assert v['origin']=='imported'
                h=call('history',memory=memory)['items'];assert [x['text'] for x in h]==[edit['text'],legacy['text']];assert all(x['created_at']=='2025-03-04 05:06:07' for x in h)
                for mutation in [legacy,edit]:assert call('apply',mutation=mutation,user_confirmed=True)['revision']==mutation['expected_revision']+1
                activity=call('activities',timezone='Asia/Shanghai');assert activity['total']==0 and activity['imported']==1
                return {'preserved_original_versions':2,'p0_idempotency_replay':True,'imports_excluded_from_heatmap':True}
            check('legacy-migration',migration)
            repo=root/'repo';(repo/'.git').mkdir(parents=True);link=root/'repo-link';link.symlink_to(repo)
            project=call('register_project',project={'name':'合成项目 A','path':str(repo)},user_confirmed=True)
            same=call('register_project',project={'name':'同路径名称','path':str(link)},user_confirmed=True);assert project['id']==same['id']
            repoB=root/'repo-b';(repoB/'.git').mkdir(parents=True)
            projectB=call('register_project',project={'name':'合成项目 B','path':str(repoB)},user_confirmed=True)
            scopes=call('scopes');globals_={s['kind']:s['id'] for s in scopes if s['kind'] in ['preference','experience']}
            check('scope-mapping',lambda:{'project_uuid_stable_for_normalized_path':True,'global_scope_kinds':list(globals_)})
            ids=[];mutations=[]
            for i in range(13):
                id_=str(uuid.uuid4());ids.append(id_)
                mutation={'operation_id':str(uuid.uuid4()),'kind':'save','dataset':project['id'],'memory':id_,'expected_revision':0,'title':f'合成知识 {i}','text':f'P1 synthetic paginationToken {i} original','source':'手动新增'}
                mutations.append(mutation);call('apply',mutation=mutation,user_confirmed=True)
            def pagination():
                seen=[];cursor='';pages=0
                while True:
                    result=call('list',list_query={'datasets':[project['id']],'limit':4,'cursor':cursor});assert result['total']==13
                    seen += [v['memory'] for v in result['items']];pages+=1;cursor=result.get('next_cursor','')
                    if not cursor:break
                assert len(seen)==len(set(seen))==13 and set(seen)==set(ids)
                assert call('list',list_query={'datasets':[projectB['id']]})['total']==0
                assert call('list',list_query={'datasets':[project['id']],'source':'other'})['total']==0
                call('list',ok=False,list_query={'cursor':'malformed!!'})
                call('list',ok=False,list_query={'limit':101})
                return {'pages':pages,'unique_items':len(seen),'scope_source_filter':True,'bad_cursor_and_limit_rejected':True}
            check('stable-pagination',pagination)
            def version_and_state():
                id_=ids[0];mutation=mutations[0]
                driver.wait(lambda:call('get',memory=id_)['index_state']=='indexed',timeout=45)
                assert call('get',memory=id_)['title']==mutation['title']
                health=call('health');os.kill(health['worker_pid'],signal.SIGKILL)
                driver.wait(lambda:call('health')['worker_pid']==0)
                changed={**mutation,'operation_id':str(uuid.uuid4()),'kind':'edit','expected_revision':1,'text':'P1 currentVersionToken new original','title':'新标题'}
                saved=call('apply',mutation=changed,user_confirmed=True);assert saved['revision']==2
                v=call('get',memory=id_);assert v['text']==changed['text'] and v['index_state']!='indexed';assert v['original']==mutation['text']
                stale=call('apply',ok=False,mutation={**changed,'operation_id':str(uuid.uuid4()),'text':'stale must not win'},user_confirmed=True);assert stale['code']=='conflict'
                assert call('get',memory=id_)['text']==changed['text']
                call('search',ok=False,datasets=[project['id']],query='currentVersionToken',limit=3)
                driver.wait(lambda:call('get',memory=id_)['index_state']=='indexed',timeout=65)
                hits=call('library_search',scope=project['id'],query='currentVersionToken',limit=3);assert any(h['version']['memory']==id_ and h['version']['revision']==2 and h['version']['title']=='新标题' for h in hits)
                assert all(h['version']['dataset']!=project['id'] for h in call('library_search',scope=projectB['id'],query='currentVersionToken',limit=3))
                restored=call('apply',mutation={**mutation,'operation_id':str(uuid.uuid4()),'kind':'restore','expected_revision':2,'restore_revision':1},user_confirmed=True);assert restored['revision']==3
                current=call('get',memory=id_);assert current['text']==mutation['text'] and current['title']==mutation['title']
                history=call('history',memory=id_,limit=2);assert [x['revision'] for x in history['items']]==[3,2] and history['next_revision']==2
                older=call('history',memory=id_,before_revision=2,limit=2);assert [x['revision'] for x in older['items']]==[1]
                return {'stale_revision_rejected':True,'save_distinct_from_index':True,'real_worker_failure_and_recovery':True,'source_and_current_search_provenance':True,'restore_revision':3,'history_pages_preserved':True}
            check('versions-and-index-state',version_and_state)
            def activities():
                for id_ in ids:driver.wait(lambda:call('get',memory=id_)['index_state']=='indexed',timeout=45)
                summary=call('activities',timezone='Asia/Shanghai');assert summary['total']==13,summary
                for mutation in mutations:call('apply',mutation=mutation,user_confirmed=True)
                call('retry_index',memory=ids[0],expected_revision=3)
                driver.wait(lambda:call('get',memory=ids[0])['index_state']=='indexed')
                assert call('activities',timezone='Asia/Shanghai')['total']==13
                date=next(iter(summary['days']));assert call('list',list_query={'date':date,'timezone':'Asia/Shanghai'})['total']==13
                deleted=call('apply',mutation={**mutations[1],'operation_id':str(uuid.uuid4()),'kind':'delete','expected_revision':1},user_confirmed=True);assert deleted['revision']==2
                summary=call('activities',timezone='Asia/Shanghai');assert summary['total']==13
                day=call('list',list_query={'date':date,'timezone':'Asia/Shanghai'});assert any(v['memory']==ids[1] and v['state']=='deleted' for v in day['items'])
                # Raw inputs and explicit imports are indexed but never count as new knowledge.
                extra=[]
                for origin in ['raw','imported']:
                    id_=str(uuid.uuid4());extra.append(id_);call('apply',mutation={**mutations[0],'memory':id_,'operation_id':str(uuid.uuid4()),'origin':origin},user_confirmed=True)
                    driver.wait(lambda:call('get',memory=id_)['index_state']=='indexed')
                final=call('activities',timezone='Asia/Shanghai');assert final['total']==13 and final['imported']==2
                assert call('list',list_query={'datasets':[project['id']],'kind':'raw'})['total']==1
                return {'new_knowledge':13,'edits_restore_replay_retry_no_increment':True,'deleted_activity_retained':True,'raw_excluded':True,'imports_separate':2,'date_list_matches_histogram':True}
            check('activity-invariants',activities)
            call('stop');assert proc.wait(timeout=15)==0;proc=None
            with sqlite3.connect(db) as c:
                assert c.execute('PRAGMA user_version').fetchone()[0]==1
                assert c.execute('PRAGMA integrity_check').fetchone()[0]=='ok'
                assert c.execute('SELECT memory,revision,text,operation,created_at FROM versions WHERE memory=? ORDER BY revision',(memory,)).fetchall()==original['versions']
                assert c.execute('SELECT * FROM operations WHERE memory=? ORDER BY revision',(memory,)).fetchall()==original['operations']
                assert c.execute('SELECT * FROM index_bindings WHERE memory=?',(memory,)).fetchall()==original['index_bindings']
            statepath=data/'service-state.json';state=json.loads(statepath.read_text());state['stopped']=False;statepath.write_text(json.dumps(state))
            proc=start();driver.wait(lambda:call('health')['state']=='ready',timeout=65)
            assert call('get',memory=ids[0])['revision']==3
            assert len(call('history',memory=ids[0])['items'])==3
            assert {s['kind']:s['id'] for s in call('scopes') if s['kind'] in globals_}==globals_
            assert call('activities',timezone='Asia/Shanghai')['total']==13
            check('restart-and-migration-integrity',lambda:{'schema_version':1,'integrity_check':'ok','legacy_original_operations_bindings_preserved':True,'current_history_scopes_activity_survive_restart':True})
            call('stop');assert proc.wait(timeout=15)==0;proc=None
            report['status']='passed'
        except Exception as error:report['error']=repr(error);raise
        finally:
            if proc and proc.poll() is None:proc.terminate();proc.wait(timeout=15)
            (out/'service-verification.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
    print(json.dumps(report,ensure_ascii=False,indent=2))
if __name__=='__main__':main()
