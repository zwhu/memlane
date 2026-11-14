package simpledesktop

// Runs only after verifyInstallation. Reuses the installed official reader and
// fingerprint contract under that host's identity; never changes its artifact,
// queue or unknown markers. A matching tail cannot prove a particular POST.
const pendingReviewScript = `
import json, sys
sys.path.insert(0, sys.argv[1])
import _plugin_common as common
import _memlane_policy as policy
common.set_session_key(sys.argv[2])
dataset = sys.argv[3]
try:
    session = common._read_map_record(sys.argv[2]).get('session_id')
    if not session:
        raise policy.NotReady('official_session_unconfirmed')
    policy.require('read', dataset)
    before = policy.pending_materials(dataset, session)
    with common._buffer_lock():
        _, _, _, _, pending, signature = policy._pending_snapshot(dataset, session)
    if signature != before['signature']:
        raise policy.NotReady('pending_materials_changed_review_again')
    detail = common.get_session_detail_via_http(session, timeout=12)
    if (not isinstance(detail, dict) or detail.get('session_id') != session
            or detail.get('dataset_id') != dataset):
        raise policy.NotReady('official_review_unavailable')
    known = common._server_session_fingerprints(detail)
    matched = sum(bool(e.get(common._AMBIGUOUS_KEY)) and common._entry_fingerprint(e) in known for e in pending)
    after = policy.pending_materials(dataset, session)
    if after['signature'] != signature:
        raise policy.NotReady('pending_materials_changed_review_again')
    print(json.dumps(dict(after, checked=True, matched_unknown=matched, session_id=session)))
except policy.NotReady as error:
    print(json.dumps({'error': str(error)}))
    raise SystemExit(1)
`
