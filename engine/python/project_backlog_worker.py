"""Save a reviewed official buffer without retransmitting ambiguous entries.

Runs under one verified installation's original host identity. Only metadata is
written to stdout. Contents stay in the official buffer until a confirmed POST.
"""
import contextlib
import hashlib
import json
import os
from pathlib import Path
import signal
import stat
import sys
import threading
import time
import urllib.error


def numbered(entry):
    try:
        from _memlane_capture import numbered as supported
    except ImportError:
        return False
    return supported(entry)


def entry_key(entry):
    # The original buffered timestamp distinguishes otherwise identical turns.
    value = {k: v for k, v in entry.items() if k != '_replay_ambiguous'}
    return hashlib.sha256(json.dumps(value, sort_keys=True, ensure_ascii=False).encode()).hexdigest()


def emit(value):
    print(json.dumps(value, separators=(',', ':')), flush=True)


def capture_allowed(common, entry):
    raw = {k: v for k, v in entry.items() if k not in getattr(common, '_BUFFER_META_KEYS', set())}
    if raw.get('type') != 'trace':
        return True
    from _capture_policy import allow_tool
    return allow_tool(str(raw.get('origin_function', '')), raw.get('method_params', {}))


def _inactive_review_snapshot(common, policy, dataset, host):
    """Return an empty snapshot when a historical session was GC'd or never buffered.

    Only succeeds if the map file itself is absent (not corrupt/symlinked), the
    local bridge file is absent or has zero pending entries in every bucket, and
    any readable policy state agrees on dataset binding. Any violation or local
    buffer entry still fails closed as unconfirmed.
    """
    if not callable(getattr(common, '_session_map_path', None)) or not callable(getattr(common, '_bridge_file', None)):
        raise policy.NotReady('official_session_unconfirmed')
    map_path = Path(common._session_map_path(host))
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
    cold_watch = {
        'map_path': str(map_path),
        'bridge_path': str(bridge_path),
        'bridge_exists': bridge_st is not None,
        'bridge_dev': bridge_st.st_dev if bridge_st is not None else 0,
        'bridge_ino': bridge_st.st_ino if bridge_st is not None else 0,
        'bridge_size': bridge_st.st_size if bridge_st is not None else 0,
        'bridge_mtime_ns': bridge_st.st_mtime_ns if bridge_st is not None else 0,
    }
    return {
        'session_id': '',
        'session_inactive': True,
        'cold_watch': cold_watch,
        'tokens': [],
        'signature': empty_sig,
        'filtered': 0,
        'rejected': 0,
        'deferred': 0,
        'rejected_unsent': 0,
        'deferred_unsent': 0,
        'rejected_unknown': 0,
        'filtered_unsent': 0,
        'filtered_tokens': [],
        'unknown_signature': empty_sig,
        'unknown': 0,
        'blocked': blocked,
    }


def review(common, policy, dataset, host):
    common.set_session_key(host)
    session = common._read_map_record(host).get('session_id')
    if not session:
        return _inactive_review_snapshot(common, policy, dataset, host)
    policy._pending_identity(dataset, session)
    with common._buffer_lock():
        _, _, _, buffer_state, pending, signature = policy._pending_snapshot(dataset, session)
    with policy._state() as state:
        blocked = state.get('blocked')
    if blocked and blocked.get('scope') == 'entries':
        blocked = None
    safe = [e for e in pending if not e.get(common._AMBIGUOUS_KEY) or numbered(e)]
    from plugin_capture import capture_entry_key, entry_held
    rejected = [e for e in safe if capture_entry_key(e) in buffer_state.get('capture_rejections', {})]
    deferred = [e for e in safe if capture_entry_key(e) not in buffer_state.get('capture_rejections', {}) and entry_held(buffer_state, e)]
    safe = [e for e in safe if not entry_held(buffer_state, e)]
    classified = [(e, capture_allowed(common, e)) for e in safe]
    filtered = [e for e, allowed in classified if not allowed]
    safe = [e for e, allowed in classified if allowed]
    if blocked and blocked.get('reason') == 'capture_request_rejected':
        safe = []  # Explicitly rejected sources never join project-wide resume.
    # A legacy unknown must not prevent separately numbered messages being saved.
    if blocked and blocked.get('reason') == 'write_result_unknown' and any(numbered(e) for e in safe):
        blocked = None
    return {'session_id': session,
            'tokens': [entry_key(e) for e in safe],
            'signature': signature, 'filtered': len(filtered),
            'rejected': len(rejected), 'deferred': len(deferred),
            'rejected_unsent': sum(not e.get(common._AMBIGUOUS_KEY) for e in rejected),
            'deferred_unsent': sum(not e.get(common._AMBIGUOUS_KEY) for e in deferred),
            'rejected_unknown': sum(bool(e.get(common._AMBIGUOUS_KEY)) for e in rejected),
            'filtered_unsent': sum(not e.get(common._AMBIGUOUS_KEY) for e in filtered),
            'filtered_tokens': [entry_key(e) for e in filtered],
            'unknown_signature': hashlib.sha256(json.dumps([entry_key(e) for e in pending if e.get(common._AMBIGUOUS_KEY)]).encode()).hexdigest(),
            'unknown': sum(bool(e.get(common._AMBIGUOUS_KEY)) for e in pending), 'blocked': blocked}


def check(common, policy, dataset, host):
    """Read original official receipts; content similarity is not POST identity.

    Python's legacy entries have no idempotency marker. Matching its recent tail
    is useful evidence, but cannot safely acknowledge/delete the original entry.
    An absent item is never evidence of non-commit. No send path is called here.
    """
    before = review(common, policy, dataset, host)
    session = before['session_id']
    if not session:
        raise policy.NotReady('official_session_unconfirmed')
    policy.require('read', dataset)
    detail = common.get_session_detail_via_http(session, timeout=12)
    if (not isinstance(detail, dict) or detail.get('session_id') != session
            or detail.get('dataset_id') != dataset):
        raise policy.NotReady('official_review_unavailable')
    known = common._server_session_fingerprints(detail)
    confirmed = []
    with common._buffer_lock():
        _, _, _, _, snapshot, _ = policy._pending_snapshot(dataset, session)
    for entry in snapshot:
        if entry.get(common._AMBIGUOUS_KEY) and numbered(entry):
            from _memlane_capture import confirmed as receipt_confirmed
            if receipt_confirmed(common, dataset, session, entry):
                confirmed.append(entry)
    with common._buffer_lock():
        path, key, cache, state, pending, _ = policy._pending_snapshot(dataset, session)
        matched = sum(bool(e.get(common._AMBIGUOUS_KEY)) and not numbered(e) and common._entry_fingerprint(e) in known for e in pending)
        # Only exact original IDs with verified receipts may be consumed.
        for entry in confirmed:
            if entry in pending:
                pending.remove(entry)
        if confirmed:
            from plugin_capture import capture_entry_key, release_entry_hold
            for entry in confirmed:
                state.get('capture_retries', {}).pop(capture_entry_key(entry), None)
                state.get('capture_rejections', {}).pop(capture_entry_key(entry), None)
            state['pending_entries'] = pending
            cache[key] = state
            common._write_json_file(path, cache)
            release_entry_hold(policy, state)
    after = review(common, policy, dataset, host)
    if not confirmed and after['unknown_signature'] != before['unknown_signature']:
        raise policy.NotReady('pending_materials_changed_review_again')
    legacy = sum(bool(e.get(common._AMBIGUOUS_KEY)) and not numbered(e) for e in pending)
    return {'status': 'unconfirmed' if after['unknown'] else 'confirmed',
            'reason': 'original_receipt_unavailable' if legacy else 'capture_retry_pending' if after['unknown'] else '',
            'unknown': after['unknown'], 'matched': matched, 'unknown_signature': after['unknown_signature']}


FLUSH_BATCH_SIZE = 10


class _ConfirmedQueueChanged(Exception):
    pass


def save(common, policy, payload, stopped, report=emit):
    dataset, session = payload['dataset'], payload['session_id']
    tokens = list(payload['tokens'])
    saved = handled = isolated = filtered = rejected = deferred = 0
    lease = None
    confirmed_batch = []

    def progress(status='running', reason=''):
        report({'status': status, 'reason': reason, 'saved': saved, 'handled': handled, 'filtered': filtered,
                'rejected': rejected, 'deferred': deferred,
                'isolated': isolated, 'remaining': len(tokens)})

    try:
        policy._pending_identity(dataset, session)
        # The lock is shared with normal hooks. Waiting never resubmits content.
        while not common._try_acquire_drain_lock():
            progress('waiting', 'sender_busy')
            if stopped():
                progress('stopped', 'user_stopped')
                return
            time.sleep(.5)
        lease = common._DRAIN_LOCK.stat()
        from _capture_policy import allow_tool, sanitize
        from plugin_capture import capture_entry_key, entry_held, release_entry_hold, retain_failure

        def flush_confirmed():
            if not confirmed_batch:
                return
            with common._buffer_lock():
                path, key, cache, state, fresh, _ = policy._pending_snapshot(dataset, session)
                for done_entry in confirmed_batch:
                    index = next((i for i, e in enumerate(fresh) if e == done_entry), None)
                    if index is None:
                        # Entry may have been stamped ambiguous on disk right before HTTP send.
                        stamped = {**done_entry, common._AMBIGUOUS_KEY: True}
                        index = next((i for i, e in enumerate(fresh) if e == stamped), None)
                    if index is None:
                        raise _ConfirmedQueueChanged()
                    fresh.pop(index)
                    state.get('capture_retries', {}).pop(capture_entry_key(done_entry), None)
                state['pending_entries'] = fresh
                cache[key] = state
                common._write_json_file(path, cache)
                release_entry_hold(policy, state)
            confirmed_batch.clear()

        def flush_confirmed_best_effort():
            if not confirmed_batch:
                return
            try:
                flush_confirmed()
            except Exception:
                confirmed_batch.clear()

        while tokens:
            if stopped():
                flush_confirmed()
                progress('stopped', 'user_stopped')
                return
            current = common._DRAIN_LOCK.stat()
            if (current.st_dev, current.st_ino) != (lease.st_dev, lease.st_ino):
                flush_confirmed_best_effort()
                progress('paused', 'sender_lock_changed')
                return
            os.utime(common._DRAIN_LOCK, None, follow_symlinks=False)
            policy.require('write', dataset)
            if payload.get('automatic'):
                policy.require('capture', dataset)
            policy._pending_identity(dataset, session)
            with policy._state() as policy_state:
                blocked = policy_state.get('blocked')
            if blocked and blocked.get('scope') != 'entries' and blocked.get('reason') not in (None, 'write_result_unknown'):
                flush_confirmed()
                reason = blocked.get('reason')
                if reason in ('official_http_401', 'official_http_403', 'write_denied', 'project_write_denied'):
                    reason = 'write_denied'
                progress('stopped' if reason == 'user_stopped' else 'paused', reason)
                return
            token = tokens[0]
            with common._buffer_lock():
                _, _, _, buffer_state, pending, _ = policy._pending_snapshot(dataset, session)
                if confirmed_batch:
                    # Exclude entries already confirmed in-memory and pending batch flush.
                    remaining_pending = list(pending)
                    for done_entry in confirmed_batch:
                        idx = next((i for i, e in enumerate(remaining_pending) if e == done_entry), None)
                        if idx is None:
                            stamped = {**done_entry, common._AMBIGUOUS_KEY: True}
                            idx = next((i for i, e in enumerate(remaining_pending) if e == stamped), None)
                        if idx is not None:
                            remaining_pending.pop(idx)
                    pending = remaining_pending
                matches = [e for e in pending if entry_key(e) == token]
                entry = next((dict(e) for e in matches if not e.get(common._AMBIGUOUS_KEY) or numbered(e)),
                             dict(matches[0]) if matches else None)
            if entry is None:
                # Another lawful sender/discard may have consumed it. Do not
                # claim our POST succeeded, and never send the cached body.
                handled += 1
                tokens.pop(0)
                progress()
                continue
            if entry.get(common._AMBIGUOUS_KEY) and not numbered(entry):
                isolated += 1
                tokens.pop(0)
                progress()
                continue
            if entry_held(buffer_state, entry):
                isolated += 1
                if capture_entry_key(entry) in buffer_state.get('capture_rejections', {}):
                    rejected += 1
                else:
                    deferred += 1
                tokens.pop(0)
                progress()
                continue
            raw = {k: v for k, v in entry.items() if k not in common._BUFFER_META_KEYS}
            if raw.get('type') == 'trace' and not allow_tool(str(raw.get('origin_function', '')), raw.get('method_params', {})):
                # Keep the original entry; exclude only this token from this
                # send scope. Filtering is neither a save nor an unknown POST.
                isolated += 1
                filtered += 1
                tokens.pop(0)
                progress()
                continue
            outgoing = sanitize(raw)
            try:
                json.dumps(outgoing, allow_nan=False)
            except (ValueError, TypeError, RecursionError):
                flush_confirmed()
                retain_failure(common, policy, dataset, session, entry, rejected=True,
                               reason='capture_payload_unserializable', was_ambiguous=bool(entry.get(common._AMBIGUOUS_KEY)))
                isolated += 1
                rejected += 1
                tokens.pop(0)
                progress()
                continue
            was_ambiguous = bool(entry.get(common._AMBIGUOUS_KEY))
            # Existing official journal is the single crash/unknown boundary.
            policy.stamp_pending_entry(dataset, session, entry, True)
            try:
                common.remember_entry_via_http(dataset, session, outgoing, timeout=12)
            except policy.NotReady as error:
                flush_confirmed()
                # A mismatched/lost receipt is message-local only when its
                # original durable number exists; scope failures stay paused.
                if numbered(entry) and str(error) == 'capture_receipt_unconfirmed':
                    retain_failure(common, policy, dataset, session, entry)
                    isolated += 1
                    deferred += 1
                    tokens.pop(0)
                    progress()
                    continue
                if not numbered(entry):
                    policy.stamp_pending_entry(dataset, session, entry, False)
                progress('paused', 'capture_retry_pending' if numbered(entry) else 'policy_unconfirmed')
                return
            except urllib.error.HTTPError as error:
                flush_confirmed()
                if error.code in (401, 403):
                    policy.stamp_pending_entry(dataset, session, entry, was_ambiguous)
                    policy.block(error.code)
                    progress('paused', 'write_denied')
                    return
                if error.code in (400, 409, 413, 422):
                    retain_failure(common, policy, dataset, session, entry, rejected=True, status=error.code, was_ambiguous=was_ambiguous)
                    rejected += 1
                elif numbered(entry):
                    retain_failure(common, policy, dataset, session, entry, status=error.code)
                    deferred += 1
                isolated += 1
                tokens.pop(0)
                progress()
                continue
            except Exception:
                flush_confirmed()
                if numbered(entry):
                    retain_failure(common, policy, dataset, session, entry)
                    deferred += 1
                isolated += 1
                tokens.pop(0)
                progress()
                continue
            saved += 1
            tokens.pop(0)
            confirmed_batch.append(entry)
            if len(confirmed_batch) >= FLUSH_BATCH_SIZE:
                flush_confirmed()
            progress()
        flush_confirmed()
        progress('completed')
    except _ConfirmedQueueChanged:
        progress('paused', 'confirmed_queue_changed')
    except policy.NotReady:
        flush_confirmed_best_effort()
        progress('paused', 'policy_unconfirmed')
    except Exception:
        flush_confirmed_best_effort()
        progress('paused', 'worker_state_unconfirmed')
    finally:
        flush_confirmed_best_effort()
        if lease is not None:
            try:
                current = common._DRAIN_LOCK.stat()
                if (current.st_dev, current.st_ino) == (lease.st_dev, lease.st_ino):
                    common._release_drain_lock()
            except FileNotFoundError:
                pass


_UNSET = object()


@contextlib.contextmanager
def _scoped_improve_cooldown(value):
    if value is None:
        yield
        return
    previous = os.environ.get('COGNEE_IMPROVE_COOLDOWN', _UNSET)
    os.environ['COGNEE_IMPROVE_COOLDOWN'] = str(value)
    try:
        yield
    finally:
        if previous is _UNSET:
            os.environ.pop('COGNEE_IMPROVE_COOLDOWN', None)
        else:
            os.environ['COGNEE_IMPROVE_COOLDOWN'] = previous


def process(common, policy, payload):
    """Delegate to the installed official plugin; never implement its policy."""
    dataset, session = payload['dataset'], payload['session_id']
    policy._pending_identity(dataset, session)
    if (callable(getattr(policy, '_pending_snapshot', None))
            and callable(getattr(common, '_buffer_lock', None))
            and callable(getattr(policy, '_state', None))):
        try:
            from plugin_capture import release_entry_hold
            with common._buffer_lock():
                _, _, _, buffer_state, _, _ = policy._pending_snapshot(dataset, session)
                release_entry_hold(policy, buffer_state)
        except (AttributeError, KeyError, OSError, ValueError):
            pass
    not_ready = getattr(policy, 'NotReady', ())
    try:
        policy.require('process', dataset, automatic=True)
    except not_ready:
        if callable(getattr(policy, 'current', None)):
            snapshot = policy.current(dataset)
            pol = snapshot.get('policy') if isinstance(snapshot, dict) else None
            if (isinstance(snapshot, dict) and snapshot.get('ready')
                    and isinstance(pol, dict)
                    and 'write' in (pol.get('permissions') or ())
                    and pol.get('can_auto_process')):
                blocked = False
                if callable(getattr(policy, '_state', None)):
                    try:
                        with policy._state(writable=False) as state:
                            blocked = bool(state.get('blocked'))
                    except Exception:
                        return {'status': 'deferred', 'reason': 'policy_unconfirmed'}
                if not blocked:
                    return {'status': 'deferred', 'reason': 'policy_unconfirmed'}
        raise
    # Idle admission uses the official configurable cooldown; its retry,
    # no-new-entry and submission behavior stays owned by the installed plugin.
    cooldown = '300' if payload.get('incremental_cooldown_seconds') == 300 else None
    with _scoped_improve_cooldown(cooldown):
        # A confirmed-cancelled original submit is not a completed improve. Reuse
        # the official switch trigger and native applied markers/watermarks; do
        # not delete its throttle history or reset any content processing watermark.
        handoff = bool(payload.get('handoff_operation'))
        reason = '' if handoff else common.improve_throttle_reason(session)
        if reason:
            return {'status': 'skipped' if reason == 'no_new_entries' else 'deferred', 'reason': reason}
        outcome = common.run_session_improve_detailed(dataset, session, trigger='switch' if handoff else 'auto')
    if outcome.get('ok'):
        # A confirmed submit is not a completed improve or generated knowledge.
        return {'status': 'submitted'}
    reason = str(outcome.get('reason') or '')
    # Official contract: incomplete_drain means the improve submit landed,
    # but some source entries have not reached the server. Preserve that fact
    # and let the existing cooldown/watermarks govern tail continuation.
    if reason == 'incomplete_drain':
        return {'status': 'partial', 'reason': reason}
    # A busy response is a definite non-admission; unreachable means nothing
    # was submitted. Other failures can include a timed-out accepted request.
    if reason in ('busy', 'unreachable'):
        return {'status': 'deferred', 'reason': reason}
    if reason == 'managed_policy_paused':
        return {'status': 'disabled', 'reason': reason}
    return {'status': 'unknown', 'reason': reason or 'official_submission_unconfirmed'}


_PERMANENT_PROCESS_NOT_READY = frozenset({
    '当前 Agent 没有此项目的写入权限',
    'project_read_denied',
    'project_write_denied',
    'project_binding_required',
    'managed_dataset_mismatch',
    'managed_buffer_session_mismatch',
    'host_session_required',
    'existing_session_requires_explicit_binding',
    'session_binding_conflict',
    'control_configuration_invalid',
    'control_credential_invalid',
    'invalid_backlog_action',
})


def main():
    payload = json.load(sys.stdin)
    sys.path.insert(0, payload['scripts'])
    import _plugin_common as common
    import _memlane_policy as policy
    common.set_session_key(payload['host'])
    stop = threading.Event()
    for signum in (signal.SIGTERM, signal.SIGINT):
        signal.signal(signum, lambda *_: stop.set())
    parent = os.getppid()
    def watch():
        while not stop.wait(.5):
            if os.getppid() != parent:
                stop.set()
                return
    threading.Thread(target=watch, daemon=True).start()
    stopped = lambda: stop.is_set() or Path(payload['cancel_file']).exists()
    try:
        if payload['action'] == 'review':
            emit(review(common, policy, payload['dataset'], payload['host']))
        elif payload['action'] == 'check':
            emit(check(common, policy, payload['dataset'], payload['host']))
        elif payload['action'] == 'save':
            save(common, policy, payload, stopped)
        elif payload['action'] == 'process':
            # The official final drain can legitimately exceed the manager's
            # silence timeout. Heartbeats report waiting, never submission.
            done = threading.Event()
            def heartbeat():
                while not done.wait(10):
                    emit({'status': 'waiting', 'reason': 'official_plugin_running'})
            pulse = threading.Thread(target=heartbeat, daemon=True)
            pulse.start()
            try:
                result = ({'status': 'disabled', 'reason': 'user_stopped'} if stopped()
                          else process(common, policy, payload))
            finally:
                done.set()
                pulse.join()
            emit(result)
        else:
            raise policy.NotReady('invalid_backlog_action')
    except policy.NotReady as error:
        reason = str(error) or 'policy_unconfirmed'
        if payload.get('action') == 'process':
            if reason in _PERMANENT_PROCESS_NOT_READY:
                code = reason if reason.isascii() and ' ' not in reason and len(reason) <= 80 else 'managed_policy_paused'
                emit({'status': 'disabled', 'reason': code})
            else:
                emit({'status': 'deferred', 'reason': 'policy_unconfirmed'})
        else:
            emit({'status': 'paused', 'reason': 'policy_unconfirmed'})


if __name__ == '__main__':
    main()
