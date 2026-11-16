package simpledesktop

// Read only: reuse the verified official buffer contract and return hashes only.
const pendingSnapshotScript = `
import hashlib, json, sys
sys.path.insert(0, sys.argv[1])
import _plugin_common as common
import _memlane_policy as policy
common.set_session_key(sys.argv[2])
dataset = sys.argv[3]
try:
    session = common._read_map_record(sys.argv[2]).get('session_id')
    if not session:
        raise policy.NotReady('official_session_unconfirmed')
    result = policy.pending_materials(dataset, session)
    with common._buffer_lock():
        _, _, _, _, pending, signature = policy._pending_snapshot(dataset, session)
    if signature != result['signature']:
        raise policy.NotReady('pending_materials_changed_review_again')
    result['unknown_entry_ids'] = [hashlib.sha256(json.dumps(
        {k:v for k,v in e.items() if k != common._AMBIGUOUS_KEY},
        sort_keys=True, ensure_ascii=False).encode()).hexdigest()
        for e in pending if e.get(common._AMBIGUOUS_KEY)]
    print(json.dumps(result))
except policy.NotReady as error:
    print(json.dumps({'error': str(error)}))
    raise SystemExit(1)
`
