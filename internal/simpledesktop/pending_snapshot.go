package simpledesktop

// Read only: reuse the verified official buffer contract and return hashes; message excerpts are read only on explicit request.
const pendingSnapshotScript = `
import hashlib, json, os, stat, sys
from pathlib import Path
sys.path.insert(0, sys.argv[1])
import _plugin_common as common
import _memlane_policy as policy
common.set_session_key(sys.argv[2])
dataset = sys.argv[3]
try:
    session = common._read_map_record(sys.argv[2]).get('session_id')
    if not session:
        if not callable(getattr(common, '_session_map_path', None)) or not callable(getattr(common, '_bridge_file', None)):
            raise policy.NotReady('official_session_unconfirmed')
        map_path = Path(common._session_map_path(sys.argv[2]))
        try:
            os.lstat(map_path)
        except FileNotFoundError:
            pass
        except OSError:
            raise policy.NotReady('official_session_unconfirmed') from None
        else:
            raise policy.NotReady('official_session_unconfirmed')
        bridge_st = None
        with common._buffer_lock():
            bridge_path = Path(common._bridge_file())
            try:
                bridge_st = os.lstat(bridge_path)
            except FileNotFoundError:
                bridge_st = None
            except OSError:
                raise policy.NotReady('official_session_unconfirmed') from None
            if bridge_st is not None:
                if stat.S_ISLNK(bridge_st.st_mode) or not stat.S_ISREG(bridge_st.st_mode):
                    raise policy.NotReady('official_session_unconfirmed')
                try:
                    cache = json.loads(bridge_path.read_text(encoding='utf-8'))
                except (OSError, ValueError):
                    raise policy.NotReady('official_session_unconfirmed') from None
                if not isinstance(cache, dict):
                    raise policy.NotReady('official_session_unconfirmed')
                for bucket in cache.values():
                    if not isinstance(bucket, dict):
                        raise policy.NotReady('official_session_unconfirmed')
                    entries = bucket.get('pending_entries')
                    if entries is not None and (not isinstance(entries, list) or len(entries) > 0):
                        raise policy.NotReady('official_session_unconfirmed')
        policy_state = {}
        try:
            with policy._state(writable=False) as st:
                if not isinstance(st, dict):
                    raise policy.NotReady('official_session_unconfirmed')
                policy_state = dict(st)
        except FileNotFoundError:
            policy_state = {}
        except policy.NotReady:
            raise
        except (OSError, ValueError, TypeError):
            raise policy.NotReady('official_session_unconfirmed') from None
        binding = policy_state.get('binding')
        if isinstance(binding, dict) and binding.get('dataset_id') and binding.get('dataset_id') != dataset:
            raise policy.NotReady('managed_buffer_session_mismatch')
        blocked = policy_state.get('blocked')
        if isinstance(blocked, dict) and blocked.get('scope') == 'entries':
            blocked = None
        empty_sig = hashlib.sha256(b'[]').hexdigest()
        result = {
            'pending': 0,
            'signature': empty_sig,
            'unknown_outcomes': 0,
            'retryable_unknown': 0,
            'unknown_entry_ids': [],
            'blocked': blocked,
            'session_inactive': True,
            'cold_watch': {
                'map_path': str(map_path),
                'bridge_path': str(bridge_path),
                'bridge_exists': bridge_st is not None,
                'bridge_size': bridge_st.st_size if bridge_st is not None else 0,
                'bridge_mtime_ns': bridge_st.st_mtime_ns if bridge_st is not None else 0,
            },
        }
        if len(sys.argv) > 4 and sys.argv[4] == 'details':
            result['details'] = []
        print(json.dumps(result))
    else:
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
