"""Private audit sink, deliberately independent of Cognee SDK storage.

Only the trusted submission owner calls this module, with its configured
SYSTEM_ROOT_DIRECTORY resolved to a physical absolute path. Never pass an HTTP
path. Publishing is a privileged in-process operation: the owner must first
prove the actual completed SDK run/edge/document/chunk mapping, not match names.
No pending or failed observation is exposed to graph browsing.
"""

import contextlib
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import sqlite3
import stat
import time
from uuid import UUID, uuid5, NAMESPACE_OID

MAX_RECORDS = 262144
MAX_RECORDS_PER_DATASET = 32768
MAX_OBSERVATION_BYTES = 64 * 1024
MAX_TOTAL_PAYLOAD_BYTES = 2 * 1024 * 1024 * 1024
MAX_DATABASE_BYTES = 3 * 1024 * 1024 * 1024
_DIRECTORY = '.memlane-grounding-audit'
_FIELDS = {'library','operation','instance','dataset','project','memory','revision','digest','document','chunk','chunk_text_digest','recipe','candidate','judge','budget'}
_COMPLETION = {'instance','dataset','document','chunk','run_id','edge_id','source_node_id','target_node_id','relationship_name','pipeline_status'}

def _fail(code):
    raise ValueError(code)

def _identifier(value):
    if not isinstance(value, str) or not re.fullmatch(r'[A-Za-z0-9_.:-]{1,256}', value):
        _fail('audit_invalid_identity')
    return value

def _uuid(value):
    try:
        if not isinstance(value, str) or str(UUID(value)) != value:
            _fail('audit_invalid_identity')
    except (AttributeError, TypeError, ValueError):
        _fail('audit_invalid_identity')
    return value

def _encoded(value):
    try:
        return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(',', ':'), allow_nan=False).encode('utf-8')
    except (ValueError, TypeError, UnicodeError):
        _fail('audit_invalid_json')

def _tree(value, depth=0):
    if depth > 12:
        _fail('audit_observation_depth')
    if isinstance(value, dict):
        if len(value) > 64 or any(not isinstance(k, str) or len(k) > 64 for k in value):
            _fail('audit_observation_shape')
        if {'start','end','text'} <= value.keys():
            start, end, text = value['start'], value['end'], value['text']
            if type(start) is not int or type(end) is not int or not isinstance(text, str) or start < 0 or end <= start or end > 256 * 1024 or end-start != len(text.encode('utf-8')):
                _fail('audit_invalid_utf8_span')
        for child in value.values():
            _tree(child, depth+1)
    elif isinstance(value, list):
        if len(value) > 128:
            _fail('audit_observation_shape')
        for child in value:
            _tree(child, depth+1)
    elif isinstance(value, str):
        if '\0' in value or len(value.encode('utf-8')) > MAX_OBSERVATION_BYTES:
            _fail('audit_observation_shape')
    elif value is not None and type(value) not in (int, bool):
        _fail('audit_observation_shape')

def _observation(observation):
    if not isinstance(observation, dict) or set(observation) != _FIELDS:
        _fail('audit_observation_fields')
    raw = _encoded(observation)
    if len(raw) > MAX_OBSERVATION_BYTES:
        _fail('audit_observation_limit')
    _tree(observation)
    for name in ('library','operation','instance','dataset','project','memory'):
        _uuid(observation[name])
    for name in ('document','chunk'):
        _identifier(observation[name])
    if type(observation['revision']) is not int or observation['revision'] < 1:
        _fail('audit_invalid_revision')
    for name in ('digest','chunk_text_digest'):
        if not isinstance(observation[name], str) or not re.fullmatch('[0-9a-f]{64}', observation[name]):
            _fail('audit_invalid_digest')
    _identifier(observation['recipe'])
    candidate, judge = observation['candidate'], observation['judge']
    if not isinstance(candidate, dict) or not isinstance(judge, dict):
        _fail('audit_observation_shape')
    if set(candidate) != {'id','source','target','predicate','description','subject','object','predicate_span','support','nodes'} or set(judge) != {'id','state','reason','support'}:
        _fail('audit_candidate_fields')
    for name in ('id','source','target'):
        _identifier(candidate[name])
    if not isinstance(candidate['predicate'], str) or not candidate['predicate'] or len(candidate['predicate'].encode()) > 256 or not isinstance(candidate['description'], str):
        _fail('audit_candidate_fields')
    _identifier(judge['id'])
    if judge['state'] not in ('entailed','unsupported','unknown') or not isinstance(judge['reason'], str):
        _fail('audit_judge_state')
    if not isinstance(candidate['support'], list) or not isinstance(candidate['nodes'], list) or not isinstance(judge['support'], list):
        _fail('audit_candidate_fields')
    for name in ('subject','object'):
        mention = candidate[name]
        if not isinstance(mention, dict) or set(mention) != {'entity','span','form','antecedent','bridges'} or mention['form'] not in ('literal','alias','coreference') or not isinstance(mention['bridges'], list):
            _fail('audit_mention_fields')
        _identifier(mention['entity'])
        if not isinstance(mention['span'], dict) or set(mention['span']) != {'start','end','text'}:
            _fail('audit_invalid_utf8_span')
    def span(value):
        if not isinstance(value, dict) or set(value) != {'start','end','text'}:
            _fail('audit_invalid_utf8_span')
    span(candidate['predicate_span'])
    for value in candidate['support'] + judge['support']:
        span(value)
    for name in ('subject','object'):
        mention = candidate[name]
        if mention['antecedent'] is not None:
            span(mention['antecedent'])
        for value in mention['bridges']:
            span(value)
    if judge['state'] == 'entailed' and (not candidate['support'] or not judge['support']):
        _fail('audit_grounded_support_required')
    if not isinstance(observation['budget'], dict) or any(type(value) is not int for value in observation['budget'].values()) or observation['budget'] != {'extraction':1,'independent_batch_judgment':1,'automatic_retries':0}:
        _fail('audit_model_budget')
    # Snapshot caller-owned mutable JSON before opening any persistent files.
    return json.loads(raw), raw

def _audit_id(observation):
    key = [observation[k] for k in ('library','operation','instance','dataset','document','chunk')]
    key.append(observation['candidate']['id'])
    return str(uuid5(NAMESPACE_OID, _encoded(key).decode()))

def _completion(completion, observation):
    if not isinstance(completion, dict) or set(completion) != _COMPLETION or completion.get('pipeline_status') != 'PipelineRunCompleted':
        _fail('audit_completed_owner_mapping_required')
    for key in ('instance','dataset','document','chunk'):
        if completion[key] != observation[key]:
            _fail('audit_completed_mapping_mismatch')
    for key in ('run_id','edge_id','source_node_id','target_node_id'):
        _identifier(completion[key])
    if completion['relationship_name'] != observation['candidate']['predicate']:
        _fail('audit_completed_mapping_mismatch')
    return _encoded(completion)

def _regular(path, private=True, limit=MAX_DATABASE_BYTES):
    s = path.lstat()
    if not stat.S_ISREG(s.st_mode) or s.st_uid != os.getuid() or s.st_nlink != 1 or (private and s.st_mode & 0o077) or s.st_size > limit:
        _fail('audit_private_regular_file_required')
    return s

def _root(system_root):
    if not isinstance(system_root, (str, os.PathLike)):
        _fail('audit_owned_physical_root_required')
    path = Path(system_root)
    if not path.is_absolute() or '..' in path.parts:
        _fail('audit_owned_physical_root_required')
    for parent in reversed((path, *path.parents)):
        s = parent.lstat()
        if stat.S_ISLNK(s.st_mode) or not stat.S_ISDIR(s.st_mode):
            _fail('audit_symlink_parent_rejected')
    s = path.stat()
    if s.st_uid != os.getuid() or s.st_mode & 0o022:
        _fail('audit_owned_physical_root_required')
    return path

@contextlib.contextmanager
def _database(system_root, create=False):
    root = _root(system_root)
    directory = root / _DIRECTORY
    try:
        directory.mkdir(mode=0o700) if create else None
    except FileExistsError:
        pass
    if not directory.exists() and not directory.is_symlink():
        yield None
        return
    ds = directory.lstat()
    if not stat.S_ISDIR(ds.st_mode) or ds.st_uid != os.getuid() or ds.st_mode & 0o077:
        _fail('audit_private_directory_required')
    path = directory / 'audit.sqlite'
    if not create and not path.exists() and not path.is_symlink():
        yield None
        return
    lock_path = directory / 'owner.lock'
    flags = os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW | os.O_CLOEXEC
    lock = os.open(lock_path, flags, 0o600)
    db = None
    try:
        before = _regular(lock_path, limit=4096)
        if (os.fstat(lock).st_dev, os.fstat(lock).st_ino) != (before.st_dev, before.st_ino):
            _fail('audit_file_identity_changed')
        until = time.monotonic()+2
        while True:
            try:
                fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
                break
            except BlockingIOError:
                if time.monotonic() >= until:
                    _fail('audit_owner_busy')
                time.sleep(0.01)
        fresh = False
        if create:
            try:
                fd = os.open(path, os.O_CREAT | os.O_EXCL | os.O_WRONLY | os.O_NOFOLLOW | os.O_CLOEXEC, 0o600)
            except FileExistsError:
                pass
            else:
                os.fsync(fd)
                os.close(fd)
                fresh = True
        before = _regular(path)
        for suffix in ('-journal','-wal','-shm'):
            sidecar = Path(str(path)+suffix)
            if sidecar.exists() or sidecar.is_symlink():
                _regular(sidecar)
                if suffix != '-journal':
                    _fail('audit_unexpected_journal_mode')
        db = sqlite3.connect(path.as_uri()+'?mode=rw', uri=True, timeout=2)
        after = _regular(path)
        if (before.st_dev,before.st_ino) != (after.st_dev,after.st_ino):
            _fail('audit_file_identity_changed')
        db.execute('PRAGMA trusted_schema=OFF')
        db.execute('PRAGMA busy_timeout=2000')
        db.execute('PRAGMA synchronous=FULL')
        if db.execute('PRAGMA journal_mode').fetchone()[0] != 'delete':
            _fail('audit_unexpected_journal_mode')
        version = db.execute('PRAGMA user_version').fetchone()[0]
        if not fresh and version != 2:
            _fail('audit_schema_unsupported')
        page_size = db.execute('PRAGMA page_size').fetchone()[0]
        db.execute('PRAGMA max_page_count=%d' % (MAX_DATABASE_BYTES // page_size))
        if fresh:
            db.executescript("""CREATE TABLE audit(
             id TEXT PRIMARY KEY, state TEXT NOT NULL CHECK(state IN ('pending','failed','published')),
             instance TEXT NOT NULL,dataset TEXT NOT NULL,document TEXT NOT NULL,chunk TEXT NOT NULL,
             observation TEXT NOT NULL,completion TEXT,error_code TEXT NOT NULL DEFAULT '',
             edge_id TEXT,run_id TEXT,created_ms INTEGER NOT NULL,published_ms INTEGER,
             published_seq INTEGER NOT NULL DEFAULT 0);
             CREATE UNIQUE INDEX published_edge_run ON audit(instance,dataset,edge_id,document,chunk,run_id) WHERE state='published';
             CREATE UNIQUE INDEX published_sequence ON audit(published_seq) WHERE state='published';
             PRAGMA user_version=2;""")
        elif version != 2:
            _fail('audit_schema_unsupported')
        db.execute('BEGIN IMMEDIATE')
        yield db
        db.commit()
        _regular(path)
    except sqlite3.Error:
        _fail('audit_storage_failure')
    finally:
        if db is not None:
            db.close()
        os.close(lock)

def record_pending(system_root, observation):
    observation, raw = _observation(observation)
    audit_id = _audit_id(observation)
    with _database(system_root, create=True) as db:
        prior = db.execute('SELECT observation FROM audit WHERE id=?', (audit_id,)).fetchone()
        if prior:
            if prior[0] != raw.decode():
                _fail('audit_observation_conflict')
            return audit_id
        count, size = db.execute('SELECT count(*),COALESCE(sum(length(CAST(observation AS BLOB))+COALESCE(length(CAST(completion AS BLOB)),0)),0) FROM audit').fetchone()
        dataset_count = db.execute('SELECT count(*) FROM audit WHERE instance=? AND dataset=?', (observation['instance'],observation['dataset'])).fetchone()[0]
        if count >= MAX_RECORDS or dataset_count >= MAX_RECORDS_PER_DATASET or size+len(raw) > MAX_TOTAL_PAYLOAD_BYTES:
            _fail('audit_capacity_reached')
        db.execute('INSERT INTO audit(id,state,instance,dataset,document,chunk,observation,created_ms) VALUES(?,?,?,?,?,?,?,?)', (audit_id,'pending',observation['instance'],observation['dataset'],observation['document'],observation['chunk'],raw.decode(),time.time_ns()//1000000))
    return audit_id

def publish_completed(system_root, audit_id, completion):
    _uuid(audit_id)
    with _database(system_root) as db:
        row = db.execute('SELECT state,observation,completion FROM audit WHERE id=?', (audit_id,)).fetchone() if db else None
        if not row:
            _fail('audit_observation_missing')
        state, raw, prior = row
        observation, _ = _observation(json.loads(raw))
        encoded = _completion(completion, observation)
        if state == 'published':
            if prior != encoded.decode():
                _fail('audit_completed_mapping_conflict')
        else:
            if state != 'pending' or observation['judge']['state'] != 'entailed':
                _fail('audit_unverified_candidate')
            size = db.execute('SELECT COALESCE(sum(length(CAST(observation AS BLOB))+COALESCE(length(CAST(completion AS BLOB)),0)),0) FROM audit').fetchone()[0]
            if size+len(encoded) > MAX_TOTAL_PAYLOAD_BYTES:
                _fail('audit_capacity_reached')
            sequence = db.execute('SELECT COALESCE(max(published_seq),0)+1 FROM audit').fetchone()[0]
            db.execute("UPDATE audit SET state='published',completion=?,edge_id=?,run_id=?,published_ms=?,published_seq=? WHERE id=? AND state='pending'", (encoded.decode(),completion['edge_id'],completion['run_id'],time.time_ns()//1000000,sequence,audit_id))
    return {'audit_id':audit_id,'state':'published','observation':observation,'completion':json.loads(encoded)}

def lookup_published(system_root, *, instance, dataset, edge_id, document, chunk):
    _uuid(instance); _uuid(dataset)
    for value in (edge_id, document, chunk):
        _identifier(value)
    with _database(system_root) as db:
        if db is None:
            return None
        row = db.execute("SELECT id,observation,completion,published_seq,published_ms FROM audit WHERE state='published' AND instance=? AND dataset=? AND edge_id=? AND document=? AND chunk=? ORDER BY published_seq DESC,id DESC LIMIT 1", (instance,dataset,edge_id,document,chunk)).fetchone()
        if not row:
            return None
        audit_id, raw, completed, sequence, published_ms = row
        observation, _ = _observation(json.loads(raw))
        completion = json.loads(completed)
        _completion(completion, observation)
        if observation['judge']['state'] != 'entailed' or _audit_id(observation) != audit_id or any(completion[k] != v for k,v in {'instance':instance,'dataset':dataset,'edge_id':edge_id,'document':document,'chunk':chunk}.items()):
            _fail('audit_stored_identity_mismatch')
    return {'audit_id':audit_id,'state':'published','observation':observation,'completion':completion,
            'published_sequence':sequence,'published_ms':published_ms,
            'judgment_scope':'historical_model_observation','selection_policy':'latest_published_for_exact_edge_document_chunk'}

def mark_failed(system_root, audit_id, code):
    _uuid(audit_id)
    if not isinstance(code, str) or not re.fullmatch('[a-z0-9_]{1,128}', code):
        _fail('audit_invalid_failure_code')
    with _database(system_root) as db:
        row = db.execute('SELECT state,error_code FROM audit WHERE id=?', (audit_id,)).fetchone() if db else None
        if not row:
            _fail('audit_observation_missing')
        if row[0] == 'published' or row[0] == 'failed' and row[1] != code:
            _fail('audit_terminal_conflict')
        db.execute("UPDATE audit SET state='failed',error_code=? WHERE id=? AND state='pending'", (code,audit_id))
