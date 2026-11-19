"""Save a reviewed official buffer without retransmitting ambiguous entries.

Runs under one verified installation's original host identity. Only metadata is
written to stdout. Contents stay in the official buffer until a confirmed POST.
"""
import hashlib
import json
import os
from pathlib import Path
import signal
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


def review(common, policy, dataset, host):
    common.set_session_key(host)
    session = common._read_map_record(host).get('session_id')
    if not session:
        raise policy.NotReady('official_session_unconfirmed')
    policy._pending_identity(dataset, session)
    with common._buffer_lock():
        _, _, _, _, pending, _ = policy._pending_snapshot(dataset, session)
    with policy._state() as state:
        blocked = state.get('blocked')
    safe = [e for e in pending if not e.get(common._AMBIGUOUS_KEY) or numbered(e)]
    if blocked and blocked.get('reason') == 'capture_request_rejected':
        safe = []  # Explicitly rejected sources never join project-wide resume.
    # A legacy unknown must not prevent separately numbered messages being saved.
    if blocked and blocked.get('reason') == 'write_result_unknown' and any(numbered(e) for e in safe):
        blocked = None
    return {'session_id': session,
            'tokens': [entry_key(e) for e in safe],
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
            state['pending_entries'] = pending
            cache[key] = state
            common._write_json_file(path, cache)
    after = review(common, policy, dataset, host)
    if not confirmed and after['unknown_signature'] != before['unknown_signature']:
        raise policy.NotReady('pending_materials_changed_review_again')
    legacy = sum(bool(e.get(common._AMBIGUOUS_KEY)) and not numbered(e) for e in pending)
    return {'status': 'unconfirmed' if after['unknown'] else 'confirmed',
            'reason': 'original_receipt_unavailable' if legacy else 'capture_retry_pending' if after['unknown'] else '',
            'unknown': after['unknown'], 'matched': matched, 'unknown_signature': after['unknown_signature']}


def save(common, policy, payload, stopped, report=emit):
    dataset, session = payload['dataset'], payload['session_id']
    tokens = list(payload['tokens'])
    saved = handled = isolated = 0
    lease = None

    def progress(status='running', reason=''):
        report({'status': status, 'reason': reason, 'saved': saved, 'handled': handled,
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
        while tokens:
            if stopped():
                progress('stopped', 'user_stopped')
                return
            current = common._DRAIN_LOCK.stat()
            if (current.st_dev, current.st_ino) != (lease.st_dev, lease.st_ino):
                progress('paused', 'sender_lock_changed')
                return
            os.utime(common._DRAIN_LOCK, None, follow_symlinks=False)
            policy.require('write', dataset)
            if payload.get('automatic'):
                policy.require('capture', dataset)
            policy._pending_identity(dataset, session)
            with policy._state() as policy_state:
                blocked = policy_state.get('blocked')
            if blocked and blocked.get('reason') == 'capture_request_rejected':
                progress('paused', 'capture_request_rejected')
                return
            token = tokens[0]
            with common._buffer_lock():
                _, _, _, _, pending, _ = policy._pending_snapshot(dataset, session)
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
            raw = {k: v for k, v in entry.items() if k not in common._BUFFER_META_KEYS}
            if raw.get('type') == 'trace' and not allow_tool(str(raw.get('origin_function', '')), raw.get('method_params', {})):
                progress('paused', 'capture_filter_changed')
                return
            outgoing = sanitize(raw)
            # Existing official journal is the single crash/unknown boundary.
            policy.stamp_pending_entry(dataset, session, entry, True)
            try:
                common.remember_entry_via_http(dataset, session, outgoing, timeout=12)
            except policy.NotReady:
                if not numbered(entry):
                    policy.stamp_pending_entry(dataset, session, entry, False)
                progress('paused', 'capture_retry_pending' if numbered(entry) else 'policy_unconfirmed')
                return
            except urllib.error.HTTPError as error:
                if error.code in (401, 403):
                    policy.stamp_pending_entry(dataset, session, entry, False)
                    policy.block(error.code)
                    progress('paused', 'write_denied')
                elif numbered(entry) and error.code in (400, 409, 413, 422):
                    progress('paused', 'capture_request_rejected')
                else:
                    if not numbered(entry):
                        isolated += 1
                        tokens.pop(0)
                    progress('paused', 'capture_retry_pending' if numbered(entry) else 'request_result_unknown')
                return
            except Exception:
                if not numbered(entry):
                    isolated += 1
                    tokens.pop(0)
                progress('paused', 'capture_retry_pending' if numbered(entry) else 'request_result_unknown')
                return
            saved += 1
            tokens.pop(0)
            # Trim only this confirmed entry against a fresh locked read. New
            # arrivals and every other unknown entry are left byte-for-byte.
            with common._buffer_lock():
                path, key, cache, state, fresh, _ = policy._pending_snapshot(dataset, session)
                index = next((i for i, e in enumerate(fresh) if e == entry), None)
                if index is None:
                    progress('paused', 'confirmed_queue_changed')
                    return
                fresh.pop(index)
                state['pending_entries'] = fresh
                cache[key] = state
                common._write_json_file(path, cache)
            progress()
        progress('completed')
    except policy.NotReady:
        progress('paused', 'policy_unconfirmed')
    except Exception:
        progress('paused', 'worker_state_unconfirmed')
    finally:
        if lease is not None:
            try:
                current = common._DRAIN_LOCK.stat()
                if (current.st_dev, current.st_ino) == (lease.st_dev, lease.st_ino):
                    common._release_drain_lock()
            except FileNotFoundError:
                pass


def process(common, policy, payload):
    """Delegate to the installed official plugin; never implement its policy."""
    dataset, session = payload['dataset'], payload['session_id']
    policy._pending_identity(dataset, session)
    policy.require('process', dataset, automatic=True)
    # Idle admission uses the official configurable cooldown; its retry,
    # no-new-entry and submission behavior stays owned by the installed plugin.
    if payload.get('incremental_cooldown_seconds') == 300:
        os.environ['COGNEE_IMPROVE_COOLDOWN'] = '300'
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
    # A busy response is a definite non-admission; unreachable means nothing
    # was submitted. Other failures can include a timed-out accepted request.
    if reason in ('busy', 'unreachable'):
        return {'status': 'deferred', 'reason': reason}
    if reason == 'managed_policy_paused':
        return {'status': 'disabled', 'reason': reason}
    return {'status': 'unknown', 'reason': reason or 'official_submission_unconfirmed'}


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
    except policy.NotReady:
        emit({'status': 'paused', 'reason': 'policy_unconfirmed'})


if __name__ == '__main__':
    main()
