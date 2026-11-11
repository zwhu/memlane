"""No-model, temporary-root contract tests for the private grounding ledger."""
import importlib.util
import json
import os
from pathlib import Path
import sqlite3
import tempfile
import unittest
from uuid import uuid4

spec=importlib.util.spec_from_file_location('grounding_audit',Path(__file__).resolve().parents[1]/'engine/python/cognee_grounding_audit.py')
a=importlib.util.module_from_spec(spec);spec.loader.exec_module(a)

class AuditTests(unittest.TestCase):
 def setUp(self):
  self.temp=tempfile.TemporaryDirectory();self.addCleanup(self.temp.cleanup);self.root=str(Path(self.temp.name).resolve())
  uid=lambda:str(uuid4())
  self.observation={k:uid() for k in ['library','operation','instance','dataset','project','memory','document','chunk']}
  self.observation.update(revision=1,digest='a'*64,chunk_text_digest='b'*64,recipe='grounding-v1',candidate={'id':'c1','source':'source-candidate','target':'target-candidate','predicate':'sends','description':'synthetic','subject':{'entity':'subject','span':{'start':0,'end':1,'text':'A'},'form':'literal','antecedent':None,'bridges':[]},'object':{'entity':'object','span':{'start':8,'end':9,'text':'B'},'form':'literal','antecedent':None,'bridges':[]},'predicate_span':{'start':2,'end':7,'text':'sends'},'support':[{'start':0,'end':9,'text':'A sends B'}],'nodes':[]},judge={'id':'independent-judge-1','state':'entailed','reason':'supported','support':[{'start':0,'end':9,'text':'A sends B'}]},budget={'extraction':1,'independent_batch_judgment':1,'automatic_retries':0})
  self.completion={k:self.observation[k] for k in ['instance','dataset','document','chunk']};self.completion.update(run_id=uid(),edge_id=uid(),source_node_id=uid(),target_node_id=uid(),relationship_name='sends',pipeline_status='PipelineRunCompleted')
 def lookup(self,**change):
  q={k:self.completion[k] for k in ['instance','dataset','edge_id','document','chunk']};q.update(change);return a.lookup_published(self.root,**q)
 def test_pending_publish_exact_lookup_and_replay(self):
  self.assertIsNone(self.lookup())
  audit=a.record_pending(self.root,self.observation);self.assertEqual(audit,a.record_pending(self.root,self.observation));self.assertIsNone(self.lookup())
  published=a.publish_completed(self.root,audit,self.completion);self.assertEqual(published,a.publish_completed(self.root,audit,self.completion));self.assertEqual(self.lookup()['audit_id'],audit)
  for field in ['instance','dataset','edge_id','document','chunk']:self.assertIsNone(self.lookup(**{field:str(uuid4())}))
  self.assertEqual((Path(self.root)/'.memlane-grounding-audit'/'audit.sqlite').stat().st_mode&0o777,0o600)
 def test_unknown_and_failed_never_browse(self):
  for state in ['unknown','unsupported']:
   self.observation['candidate']['id']=state;self.observation['judge']['state']=state
   audit=a.record_pending(self.root,self.observation)
   with self.assertRaises(ValueError):a.publish_completed(self.root,audit,self.completion)
   self.assertIsNone(self.lookup())
  self.observation['candidate']['id']='cancelled';self.observation['judge']['state']='entailed'
  audit=a.record_pending(self.root,self.observation);a.mark_failed(self.root,audit,'cancelled')
  with self.assertRaises(ValueError):a.publish_completed(self.root,audit,self.completion)
  self.assertIsNone(self.lookup())
 def test_mapping_and_observation_conflicts_are_atomic(self):
  audit=a.record_pending(self.root,self.observation)
  changed=json.loads(json.dumps(self.observation));changed['judge']['reason']='changed'
  with self.assertRaises(ValueError):a.record_pending(self.root,changed)
  for field,value in [('document',str(uuid4())),('chunk',str(uuid4())),('pipeline_status','PipelineRunFailed'),('relationship_name','guessed')]:
   bad={**self.completion,field:value}
   with self.assertRaises(ValueError):a.publish_completed(self.root,audit,bad)
  self.assertIsNone(self.lookup());a.publish_completed(self.root,audit,self.completion)
  with self.assertRaises(ValueError):a.publish_completed(self.root,audit,{**self.completion,'run_id':str(uuid4())})
  with self.assertRaises(ValueError):a.mark_failed(self.root,audit,'cancelled')
 def test_symlink_root_parent_and_database_rejected(self):
  parent=Path(self.root)/'alias';parent.symlink_to(self.root,target_is_directory=True)
  with self.assertRaises(ValueError):a.record_pending(str(parent),self.observation)
  audit_dir=Path(self.root)/'.memlane-grounding-audit';audit_dir.mkdir(mode=0o700)
  target=Path(self.root)/'other';target.write_bytes(b'unchanged');(audit_dir/'audit.sqlite').symlink_to(target)
  with self.assertRaises(ValueError):a.record_pending(self.root,self.observation)
  self.assertEqual(target.read_bytes(),b'unchanged')
 def test_bounded_and_utf8_span_checks(self):
  bad=json.loads(json.dumps(self.observation));bad['candidate']['support']=[{'start':0,'end':1,'text':'中'}]
  with self.assertRaises(ValueError):a.record_pending(self.root,bad)
  bad=json.loads(json.dumps(self.observation));bad['candidate']['description']='x'*70000
  with self.assertRaises(ValueError):a.record_pending(self.root,bad)

 def test_real_dataset_record_capacity_and_database_size(self):
  a.record_pending(self.root,self.observation)
  dbpath=Path(self.root)/'.memlane-grounding-audit'/'audit.sqlite'
  rows=[]
  # Valid frozen observations, built in one SQLite fixture transaction rather
  # than 32768 fsync cycles; the next real API call enforces the production cap.
  for n in range(1,a.MAX_RECORDS_PER_DATASET):
   observation=json.loads(json.dumps(self.observation));observation['candidate']['id']='candidate-%d'%n
   observation,raw=a._observation(observation)
   rows.append((a._audit_id(observation),'pending',observation['instance'],observation['dataset'],observation['document'],observation['chunk'],raw.decode(),0))
  with sqlite3.connect(dbpath) as db:
   db.executemany('INSERT INTO audit(id,state,instance,dataset,document,chunk,observation,created_ms) VALUES(?,?,?,?,?,?,?,?)',rows)
   self.assertEqual(db.execute('SELECT count(*) FROM audit').fetchone()[0],32768)
  self.observation['candidate']['id']='one-over-real-dataset-cap'
  with self.assertRaisesRegex(ValueError,'audit_capacity_reached'):a.record_pending(self.root,self.observation)
  with dbpath.open('r+b') as f:f.truncate(a.MAX_DATABASE_BYTES+1)
  with self.assertRaisesRegex(ValueError,'audit_private_regular_file_required'):self.lookup()

 def test_private_files_hardlinks_sidecars_and_restart(self):
  audit=a.record_pending(self.root,self.observation)
  directory=Path(self.root)/'.memlane-grounding-audit';dbpath=directory/'audit.sqlite'
  os.chmod(dbpath,0o644)
  with self.assertRaises(ValueError):self.lookup()
  os.chmod(dbpath,0o600)
  hard=Path(self.root)/'hardlink';os.link(dbpath,hard)
  with self.assertRaises(ValueError):self.lookup()
  hard.unlink()
  sidecar=Path(str(dbpath)+'-journal');sidecar.symlink_to(dbpath)
  with self.assertRaises(ValueError):self.lookup()
  sidecar.unlink()
  self.assertIsNone(self.lookup())
  a.publish_completed(self.root,audit,self.completion)
  # Every operation opens/closes its own actual SQLite connection; the final
  # read verifies durability without a process-global in-memory receipt cache.
  self.assertEqual(self.lookup()['observation'],self.observation)

 def test_new_explicit_run_appends_and_lookup_selects_latest_published(self):
  first=a.record_pending(self.root,self.observation)
  a.publish_completed(self.root,first,self.completion)
  old=self.lookup()
  second_observation=json.loads(json.dumps(self.observation))
  second_observation['operation']=str(uuid4())
  second_observation['judge']['id']='independent-judge-second'
  second=a.record_pending(self.root,second_observation)
  second_completion={**self.completion,'run_id':str(uuid4())}
  self.assertEqual(self.lookup()['audit_id'],first)
  a.publish_completed(self.root,second,second_completion)
  latest=self.lookup()
  self.assertEqual(latest['audit_id'],second)
  self.assertGreater(latest['published_sequence'],old['published_sequence'])
  self.assertEqual(latest['judgment_scope'],'historical_model_observation')
  # A new unsupported judgment is durable but never replaces published proof.
  pending=json.loads(json.dumps(second_observation));pending['operation']=str(uuid4());pending['judge']['state']='unsupported'
  a.record_pending(self.root,pending)
  self.assertEqual(self.lookup()['audit_id'],second)
  a.publish_completed(self.root,first,self.completion)
  self.assertEqual(self.lookup()['audit_id'],second)
  with self.assertRaises(ValueError):a.publish_completed(self.root,second,self.completion)
  dbpath=Path(self.root)/'.memlane-grounding-audit'/'audit.sqlite'
  with sqlite3.connect(dbpath) as db:
   rows=db.execute("SELECT id,completion FROM audit WHERE state='published' ORDER BY published_seq").fetchall()
  self.assertEqual([r[0] for r in rows],[first,second])
  self.assertEqual(json.loads(rows[0][1]),self.completion)

 def test_v1_ledger_is_not_read_as_v2_or_migrated_implicitly(self):
  import hashlib
  directory=Path(self.root)/'.memlane-grounding-audit';directory.mkdir(mode=0o700)
  dbpath=directory/'audit.sqlite'
  with sqlite3.connect(dbpath) as db:db.executescript('CREATE TABLE old_audit(id TEXT);PRAGMA user_version=1;')
  os.chmod(dbpath,0o600);before=hashlib.sha256(dbpath.read_bytes()).hexdigest()
  with self.assertRaisesRegex(ValueError,'audit_schema_unsupported'):self.lookup()
  self.assertEqual(hashlib.sha256(dbpath.read_bytes()).hexdigest(),before)

if __name__=='__main__':unittest.main()
