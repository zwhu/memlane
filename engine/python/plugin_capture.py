"""Installed as _memlane_capture: numbered sends through the atomic protocol."""
import hashlib
import json
import uuid
import os
import time
import urllib.error

KEY = '_capture_id'


def numbered(entry):
    value = entry.get(KEY) if isinstance(entry, dict) else None
    try:
        return bool(value and str(uuid.UUID(value)) == value)
    except (ValueError, TypeError, AttributeError):
        return False


def wire(common, dataset, session, entry, dataset_id=None):
    from _memlane_policy import NotReady
    from _project_memory import route
    if not numbered(entry):
        raise NotReady('capture_message_id_required')
    message = entry[KEY]
    target = route(dataset, session)
    dataset = target['write']
    # Managed routing already returns an authoritative dataset UUID. The
    # official name-to-ID lookup can be empty for that UUID when the launch
    # record retains a human-readable name; do not treat it as a name again.
    from _dataset_access import dataset_id as parse_dataset_id
    ident = (parse_dataset_id(dataset_id) if dataset_id is not None else
             parse_dataset_id(dataset) or common.dataset_id_for(dataset))
    if not ident:
        raise NotReady('capture_dataset_id_required')
    outgoing = common._sanitize_value({k: v for k, v in entry.items()
                                      if k not in (*common._BUFFER_META_KEYS, KEY)})
    if target.get('node_set'):
        outgoing = {**outgoing, 'node_set': target['node_set']}
    sha = hashlib.sha256(json.dumps(outgoing, sort_keys=True, ensure_ascii=False, allow_nan=False).encode()).hexdigest()
    return message, ident, outgoing, sha


def valid(receipt, message, ident, session, outgoing, sha):
    return (isinstance(receipt, dict) and receipt.get('protocol') == 1 and receipt.get('status') == 'saved'
            and receipt.get('message_id') == message and receipt.get('dataset_id') == ident
            and receipt.get('session_id') == session and receipt.get('entry_type') == outgoing.get('type')
            and receipt.get('payload_sha256') == sha and bool(receipt.get('entry_id')))


def send(common, dataset, session, entry, *, timeout, dataset_id=None):
    from _memlane_policy import require, NotReady
    require('write', dataset)
    message, ident, outgoing, sha = wire(common, dataset, session, entry, dataset_id)
    receipt = common._json_http_request('/api/v1/capture/entries/' + message,
        {'dataset_id': ident, 'session_id': session, 'entry': outgoing}, method='PUT', timeout=timeout)
    if not valid(receipt, message, ident, session, outgoing, sha):
        raise NotReady('capture_receipt_unconfirmed')
    return receipt


def confirmed(common, dataset, session, entry, *, timeout=8):
    from urllib.parse import urlencode
    from _memlane_policy import require
    require('read', dataset)
    if not numbered(entry):
        return False
    message, ident, outgoing, sha = wire(common, dataset, session, entry)
    try:
        receipt = common._json_http_request('/api/v1/capture/entries/' + message + '?' +
            urlencode({'dataset_id': ident, 'session_id': session}), method='GET', timeout=timeout)
    except urllib.error.HTTPError as error:
        if error.code == 404:
            return False
        raise
    return valid(receipt, message, ident, session, outgoing, sha)


def drain(common, dataset, session, *, budget_seconds=None):
    """Reuse the official buffer and sender lease; skip unnumbered unknowns.

    No content-similarity acknowledgement. A timeout retries the same durable
    number after backoff; a legacy unknown never enters this send path.
    """
    import _memlane_policy as policy
    from _capture_policy import allow_tool, sanitize
    def snapshot():
        return policy._pending_snapshot(dataset, session)
    with common._buffer_lock():
        _, _, _, state, pending, _ = snapshot()
        count = len(pending)
        failures = min(8, int(state.get('drain_fail_count') or 0))
        if failures and time.time() - float(state.get('drain_fail_at') or 0) < min(300, 2 ** failures):
            return 0, count
    if not count or not common._try_acquire_drain_lock():
        return 0, count
    lease = common._DRAIN_LOCK.stat()
    deadline = time.monotonic() + max(0, float(budget_seconds if budget_seconds is not None else common._drain_budget_default()))
    saved = 0
    failed = False
    try:
        with policy._state() as state:
            if state.get('blocked', {}).get('reason') not in (None, 'write_result_unknown'):
                return 0, count
        for original in pending:
            if time.monotonic() >= deadline:
                break
            if original.get(common._AMBIGUOUS_KEY) and not numbered(original):
                continue
            policy.require('capture', dataset)
            policy.require('write', dataset)
            policy._pending_identity(dataset, session)
            current = common._DRAIN_LOCK.stat()
            if (current.st_dev, current.st_ino) != (lease.st_dev, lease.st_ino):
                break
            os.utime(common._DRAIN_LOCK, None, follow_symlinks=False)
            entry = dict(original)
            with common._buffer_lock():
                path, key, cache, state, fresh, _ = snapshot()
                if entry not in fresh:
                    continue
                if not numbered(entry):
                    replacement = {**entry, KEY: str(uuid.uuid4())}
                    fresh[fresh.index(entry)] = replacement
                    state['pending_entries'] = fresh
                    cache[key] = state
                    common._write_json_file(path, cache)
                    entry = replacement
            raw = {k: v for k, v in entry.items() if k not in common._BUFFER_META_KEYS}
            if raw.get('type') == 'trace' and not allow_tool(str(raw.get('origin_function', '')), raw.get('method_params', {})):
                continue
            raw = sanitize(raw)
            policy.stamp_pending_entry(dataset, session, entry, True)
            try:
                send(common, dataset, session, raw, timeout=min(12, max(.1, deadline - time.monotonic())))
            except urllib.error.HTTPError as error:
                policy.block(error.code)
                if error.code in (400, 409, 413, 422):
                    policy.hold('capture_request_rejected')
                failed = True
                break
            except Exception:
                failed = True
                break
            with common._buffer_lock():
                path, key, cache, state, fresh, _ = snapshot()
                if entry in fresh:
                    fresh.remove(entry)
                    state['pending_entries'] = fresh
                    cache[key] = state
                    common._write_json_file(path, cache)
            saved += 1
        with common._buffer_lock():
            path, key, cache, state, fresh, _ = snapshot()
            if failed:
                state['drain_fail_count'] = failures + 1
                state['drain_fail_at'] = time.time()
            elif saved:
                state.pop('drain_fail_count', None)
                state.pop('drain_fail_at', None)
            cache[key] = state
            common._write_json_file(path, cache)
            return saved, len(fresh)
    finally:
        try:
            current = common._DRAIN_LOCK.stat()
            if (current.st_dev, current.st_ino) == (lease.st_dev, lease.st_ino):
                common._release_drain_lock()
        except FileNotFoundError:
            pass
