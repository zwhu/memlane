package simpledesktop

// Read only: reuse the verified official buffer contract and return hashes; message excerpts are read only on explicit request.
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
        _, _, _, buffer_state, pending, signature = policy._pending_snapshot(dataset, session)
    if signature != result['signature']:
        raise policy.NotReady('pending_materials_changed_review_again')
    try:
        from _memlane_capture import numbered
    except ImportError:
        numbered = lambda entry: False
    rejections = buffer_state.get('capture_rejections', {})
    if result.get('blocked') and result['blocked'].get('scope') == 'entries':
        result['blocked'] = None
    result['retryable_unknown'] = sum(bool(e.get(common._AMBIGUOUS_KEY)) and numbered(e) and e.get('_capture_id') not in rejections for e in pending)
    result['unknown_entry_ids'] = [hashlib.sha256(json.dumps(
        {k:v for k,v in e.items() if k != common._AMBIGUOUS_KEY},
        sort_keys=True, ensure_ascii=False).encode()).hexdigest()
        for e in pending if e.get(common._AMBIGUOUS_KEY)]
    if len(sys.argv) > 4 and sys.argv[4] == 'details':
        rows = []
        for e in pending[:20]:
            kind = str(e.get('type') or '')
            summary = e.get('question') if kind == 'qa' else e.get('origin_function') if kind == 'trace' else ''
            if kind == 'trace' and e.get('method_params'):
                summary = str(summary or '工具记录') + ' · ' + json.dumps(e['method_params'], ensure_ascii=False)
            rows.append({'id': hashlib.sha256(json.dumps({k:v for k,v in e.items() if k != common._AMBIGUOUS_KEY}, sort_keys=True, ensure_ascii=False).encode()).hexdigest(),
                         'kind': {'qa': '问答', 'trace': '工具记录'}.get(kind, '消息'),
                         'summary': str(summary or '此类消息暂无可展示的摘要')[:240],
                         'unknown': bool(e.get(common._AMBIGUOUS_KEY))})
        result['details'] = rows
    print(json.dumps(result))
except policy.NotReady as error:
    print(json.dumps({'error': str(error)}))
    raise SystemExit(1)
`
