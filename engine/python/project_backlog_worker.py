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
    return {'session_id': session,
            'tokens': [entry_key(e) for e in pending if not e.get(common._AMBIGUOUS_KEY)],
            'unknown': sum(bool(e.get(common._AMBIGUOUS_KEY)) for e in pending)}


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
            policy._pending_identity(dataset, session)
            token = tokens[0]
            with common._buffer_lock():
                _, _, _, _, pending, _ = policy._pending_snapshot(dataset, session)
                matches = [e for e in pending if entry_key(e) == token]
                entry = next((dict(e) for e in matches if not e.get(common._AMBIGUOUS_KEY)),
                             dict(matches[0]) if matches else None)
            if entry is None:
                # Another lawful sender/discard may have consumed it. Do not
                # claim our POST succeeded, and never send the cached body.
                handled += 1
                tokens.pop(0)
                progress()
                continue
            if entry.get(common._AMBIGUOUS_KEY):
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
                policy.stamp_pending_entry(dataset, session, entry, False)
                progress('paused', 'policy_unconfirmed')
                return
            except urllib.error.HTTPError as error:
                if error.code in (401, 403):
                    policy.stamp_pending_entry(dataset, session, entry, False)
                    policy.block(error.code)
                    progress('paused', 'write_denied')
                else:
                    isolated += 1
                    tokens.pop(0)
                    progress('paused', 'request_result_unknown')
                return
            except Exception:
                isolated += 1
                tokens.pop(0)
                progress('paused', 'request_result_unknown')
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
        elif payload['action'] == 'save':
            save(common, policy, payload, stopped)
        else:
            raise policy.NotReady('invalid_backlog_action')
    except policy.NotReady:
        emit({'status': 'paused', 'reason': 'policy_unconfirmed'})


if __name__ == '__main__':
    main()
