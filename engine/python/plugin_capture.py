"""Installed as _memlane_capture: numbered sends through the atomic protocol."""
import hashlib
import json
import uuid
import os
import time
import urllib.error

KEY = '_capture_id'


def capture_payload(raw, *, ambiguous=False):
    """Keep blocked patch bodies local while allowing a summary of ordinary file edits."""
    if raw.get('type') != 'trace':
        return raw
    from _capture_policy import allow_tool
    name, params = str(raw.get('origin_function', '')), raw.get('method_params', {})
    if allow_tool(name, params):
        return raw
    if (ambiguous or name != 'apply_patch' or not isinstance(params, dict)
            or set(params) != {'command'}
            or any(os.environ.get(key, '').strip() for key in ('COGNEE_CAPTURE_TOOLS', 'COGNEE_CAPTURE_DENY_TOOLS'))):
        return None
    command = params['command']
    if not isinstance(command, str):
        return None
    lines = command.splitlines()
    if len(lines) < 3 or lines[0] != '*** Begin Patch' or lines[-1] != '*** End Patch':
        return None
    paths = []
    prefixes = ('*** Add File: ', '*** Update File: ', '*** Delete File: ', '*** Move to: ')
    for line in lines[1:-1]:
        if line.startswith(prefixes):
            path = line.split(': ', 1)[1]
            if not path.strip() or any(ord(char) < 32 for char in path):
                return None
            paths.append(path.strip())
        elif line.startswith('***') and line != '*** End of File':
            return None
    if not paths or not allow_tool(name, {'paths': paths}):
        return None
    if not numbered(raw):
        return None
    summary = {key: raw[key] for key in ('type', 'origin_function', 'status', 'time', KEY) if key in raw}
    summary['method_params'] = {'file_count': len(set(paths))}
    summary['method_return_value'] = '补丁正文被采集规则过滤，仅保存操作摘要。'
    return summary


def persist_capture_summary(common, policy, dataset, session, entry, summary):
    """Keep the numbered payload stable across crashes and receipt checks."""
    with common._buffer_lock() as locked:
        if locked is False:
            raise policy.NotReady('pending_entry_changed_before_send')
        path, key, cache, state, pending, _ = policy._pending_snapshot(dataset, session)
        if entry not in pending:
            raise policy.NotReady('pending_entry_changed_before_send')
        replacement = {**summary, **{key: entry[key] for key in common._BUFFER_META_KEYS if key in entry}}
        pending[pending.index(entry)] = replacement
        state['pending_entries'] = pending
        cache[key] = state
        common._write_json_file(path, cache)
        return replacement


def numbered(entry):
    value = entry.get(KEY) if isinstance(entry, dict) else None
    try:
        return bool(value and str(uuid.UUID(value)) == value)
    except (ValueError, TypeError, AttributeError):
        return False


def capture_entry_key(entry):
    if numbered(entry):
        return entry[KEY]
    value = {k: v for k, v in entry.items() if k != '_replay_ambiguous'}
    return hashlib.sha256(json.dumps(value, sort_keys=True, ensure_ascii=False).encode()).hexdigest()


def entry_held(state, entry, *, now=None):
    key = capture_entry_key(entry)
    if key in state.get('capture_rejections', {}):
        return True
    retry = state.get('capture_retries', {}).get(key, {})
    return retry.get('after', 0) > (time.time() if now is None else now)


def release_entry_hold(policy, state):
    if state.get('capture_rejections') or state.get('capture_retries'):
        return
    with policy._state() as current:
        if current.get('blocked', {}).get('scope') == 'entries':
            previous = current.pop('capture_prior_block', None)
            if previous is None:
                current.pop('blocked', None)
            else:
                current['blocked'] = previous


def retain_failure(common, policy, dataset, session, entry, *, rejected=False, status=None, reason='', was_ambiguous=False):
    # Definitive rejection does not erase an earlier unknown write. A conflict
    # can refer to a different body already stored under the same original ID.
    if rejected and status != 409:
        policy.stamp_pending_entry(dataset, session, entry, was_ambiguous)
    with common._buffer_lock():
        path, cache_key, cache, state, pending, _ = policy._pending_snapshot(dataset, session)
        key = capture_entry_key(entry)
        if not any(capture_entry_key(e) == key for e in pending):
            raise policy.NotReady('pending_entry_changed_after_request')
        if rejected:
            state.setdefault('capture_rejections', {})[key] = {'reason': reason or 'capture_request_rejected',
                                                              'http_status': status, 'at': time.time()}
            state.get('capture_retries', {}).pop(key, None)
        else:
            retries = state.setdefault('capture_retries', {})
            attempts = min(8, retries.get(key, {}).get('attempts', 0)+1)
            retries[key] = {'reason': 'capture_retry_pending', 'http_status': status,
                            'attempts': attempts, 'after': time.time()+min(300, 2**attempts)}
        cache[cache_key] = state
        common._write_json_file(path, cache)
    # Older normal senders must not ignore the new per-entry hold metadata.
    # Their existing session guard stops them; new senders recognize its scope.
    with policy._state() as current:
        blocked = current.get('blocked') or {}
        if blocked.get('reason') in (None, 'write_result_unknown') or blocked.get('scope') == 'entries':
            if blocked and blocked.get('scope') != 'entries':
                current['capture_prior_block'] = dict(blocked)
            current['blocked'] = {'reason': 'capture_request_rejected', 'scope': 'entries', 'at': time.time()}


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


def send(common, dataset, session, entry, *, timeout, dataset_id=None, capture_mode='automatic'):
    from _memlane_policy import require_capture_write, remember_capture_pause, NotReady
    require_capture_write(dataset, capture_mode)
    message, ident, outgoing, sha = wire(common, dataset, session, entry, dataset_id)
    try:
        receipt = common._json_http_request('/api/v1/capture/entries/' + message,
            {'dataset_id': ident, 'session_id': session, 'entry': outgoing}, method='PUT',
            timeout=timeout, capture_mode=capture_mode)
    except urllib.error.HTTPError as error:
        if remember_capture_pause(error, dataset):
            raise NotReady('capture_disabled') from error
        raise
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


def drain(common, dataset, session, *, budget_seconds=None, capture_mode='automatic'):
    """Reuse the official buffer and sender lease; skip unnumbered unknowns.

    No content-similarity acknowledgement. A timeout retries the same durable
    number after backoff; a legacy unknown never enters this send path.
    """
    import _memlane_policy as policy
    from _capture_policy import sanitize
    def snapshot():
        return policy._pending_snapshot(dataset, session)
    with common._buffer_lock():
        _, _, _, state, pending, _ = snapshot()
        count = len(pending)
        failures = min(8, int(state.get('drain_fail_count') or 0))
        if failures and time.time() - float(state.get('drain_fail_at') or 0) < min(300, 2 ** failures):
            return 0, count
    release_entry_hold(policy, state)
    if not count or not common._try_acquire_drain_lock():
        return 0, count
    lease = common._DRAIN_LOCK.stat()
    deadline = time.monotonic() + max(0, float(budget_seconds if budget_seconds is not None else common._drain_budget_default()))
    saved = 0
    failed = False
    try:
        with policy._state() as state:
            blocked = state.get('blocked') or {}
            if blocked.get('scope') != 'entries' and blocked.get('reason') not in (None, 'write_result_unknown'):
                return 0, count
        for original in pending:
            if time.monotonic() >= deadline:
                break
            if original.get(common._AMBIGUOUS_KEY) and not numbered(original):
                continue
            try:
                policy.require_capture_write(dataset, capture_mode)
                if capture_mode == 'automatic':
                    policy.require('write', dataset)
            except policy.NotReady:
                break
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
                if entry_held(state, entry):
                    continue
                if not numbered(entry):
                    replacement = {**entry, KEY: str(uuid.uuid4())}
                    fresh[fresh.index(entry)] = replacement
                    state['pending_entries'] = fresh
                    cache[key] = state
                    common._write_json_file(path, cache)
                    entry = replacement
            raw = {k: v for k, v in entry.items() if k not in common._BUFFER_META_KEYS}
            payload = capture_payload(raw, ambiguous=bool(entry.get(common._AMBIGUOUS_KEY)))
            if payload is None:
                continue
            if payload is not raw:
                entry = persist_capture_summary(common, policy, dataset, session, entry, payload)
            raw = sanitize(payload)
            try:
                json.dumps(raw, allow_nan=False)
            except (ValueError, TypeError, RecursionError):
                retain_failure(common, policy, dataset, session, entry, rejected=True,
                               reason='capture_payload_unserializable', was_ambiguous=bool(entry.get(common._AMBIGUOUS_KEY)))
                continue
            was_ambiguous = bool(entry.get(common._AMBIGUOUS_KEY))
            policy.stamp_pending_entry(dataset, session, entry, True)
            try:
                send(common, dataset, session, raw, timeout=min(12, max(.1, deadline - time.monotonic())),
                     capture_mode=capture_mode)
            except policy.NotReady as error:
                if str(error) in ('capture_disabled', 'project_policy_unconfirmed', 'project_write_denied'):
                    policy.stamp_pending_entry(dataset, session, entry, was_ambiguous)
                    break
                retain_failure(common, policy, dataset, session, entry)
                continue
            except urllib.error.HTTPError as error:
                if error.code in (401, 403):
                    policy.stamp_pending_entry(dataset, session, entry, was_ambiguous)
                    policy.block(error.code)
                    failed = True
                    break
                retain_failure(common, policy, dataset, session, entry,
                               rejected=error.code in (400, 409, 413, 422), status=error.code,
                               was_ambiguous=was_ambiguous)
                continue
            except Exception:
                retain_failure(common, policy, dataset, session, entry)
                continue
            with common._buffer_lock():
                path, key, cache, state, fresh, _ = snapshot()
                if entry in fresh:
                    fresh.remove(entry)
                    state['pending_entries'] = fresh
                    state.get('capture_retries', {}).pop(capture_entry_key(entry), None)
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
            release_entry_hold(policy, state)
            return saved, len(fresh)
    finally:
        try:
            current = common._DRAIN_LOCK.stat()
            if (current.st_dev, current.st_ino) == (lease.st_dev, lease.st_ino):
                common._release_drain_lock()
        except FileNotFoundError:
            pass
