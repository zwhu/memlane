"""Build host-specific PA patches from the fixed official integration archive.

The original Codex artifact/manifest is untouched. All upstream bytes are
verified by the pinned archive digest before any host-specific edit is applied.
Host runtime logic lives in standalone modules (project_access_hooks.py and
project_host_hooks.py), keeping upstream replacements minimal.
"""
import ast
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import stat
import tarfile

ENGINE = Path(__file__).resolve().parents[1]
ROOT = ENGINE.parent

_ARCHIVE_DIGEST_CACHE = {}


def _file_sha256_cached(path):
    path = Path(path)
    st = os.lstat(path)
    if stat.S_ISLNK(st.st_mode) or not stat.S_ISREG(st.st_mode):
        return None
    key = (str(path), st.st_dev, st.st_ino, st.st_size, st.st_mtime_ns)
    cached = _ARCHIVE_DIGEST_CACHE.get(key)
    if cached is not None:
        return cached
    digest = hashlib.sha256()
    with path.open('rb') as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b''):
            digest.update(chunk)
    value = digest.hexdigest()
    if len(_ARCHIVE_DIGEST_CACHE) >= 32:
        _ARCHIVE_DIGEST_CACHE.clear()
    _ARCHIVE_DIGEST_CACHE[key] = value
    return value


def resolve_official_archive(source=None):
    manifest = json.loads((ENGINE / 'coding-memory-components.json').read_text())['plugin']
    candidates = []
    if isinstance(source, (str, Path)) and str(source):
        src = Path(source)
        if src.is_absolute() and not src.is_symlink():
            if src.suffix == '.gz' and src.is_file():
                candidates.append(src)
            else:
                candidates.append(src.parent / 'integrations-upstream' / 'service.tar.gz')
                candidates.append(src.parent / 'coding-memory-components' / (manifest['commit'] + '.tar.gz'))
                candidates.append(src.parent / (manifest['commit'] + '.tar.gz'))
    for candidate in candidates:
        if candidate.exists() or candidate.is_symlink():
            return candidate
    return ROOT / '.cache/coding-memory-components' / (manifest['commit'] + '.tar.gz')


def archive_available(source=None):
    manifest = json.loads((ENGINE / 'coding-memory-components.json').read_text())['plugin']
    archive = resolve_official_archive(source)
    try:
        return _file_sha256_cached(archive) == manifest['archive_sha256']
    except OSError:
        return False


def shared_patch():
    spec = importlib.util.spec_from_file_location('memlane_project_access_patch', ENGINE / 'patches/project_access.py')
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def claude_capture(text, patch):
    text = patch.apply('_coding_memory.py', text)
    text = patch.replace(text, 'def parse_claude(transcript_path: str) -> dict:',
                         'def parse_claude(transcript_path: str, start_offset: int = 0) -> dict:')
    text = patch.replace(text, '    for index, path in enumerate(_claude_files(transcript)):',
                         '    from _memlane_host import claude_transcript_files\n'
                         '    for index, path in enumerate(claude_transcript_files(transcript, _claude_files)):')
    text = patch.replace(text, '        for record in _iter_jsonl(path):',
                         '        for record in _iter_jsonl(path, start_offset if main else 0):')
    text = patch.replace(text, '            kind = record.get("type")\n            message = record.get("message")',
                         '            kind = record.get("type")\n'
                         '            if start_offset and not units and kind != "user":\n'
                         '                continue\n'
                         '            message = record.get("message")')
    text = patch.replace(text, 'else parse_claude(transcript_path)\n', 'else parse_claude(transcript_path, start_offset)\n')
    return text


def claude_start(text, patch):
    return patch.replace(
        text, '    os.environ["COGNEE_SESSION_KEY"] = session_key\n    if payload.get("source")',
        '    os.environ["COGNEE_SESSION_KEY"] = session_key\n'
        '    from _memlane_host import claude_session_start_bind\n'
        '    _early = claude_session_start_bind(config, payload, cwd, session_key)\n'
        '    if _early is not None:\n'
        '        return _early\n'
        '    if payload.get("source")',
    )


def disable_python_fallback(text, patch, *, remember=False):
    text = patch.body(
        text, '_runtime',
        'from _env_file import load_env_file\n'
        'from _memlane_host import managed_python_tool_runtime\n'
        '_managed_rt = managed_python_tool_runtime(load_env_file, os.environ)\n'
        'if _managed_rt is not None:\n'
        '    return _managed_rt',
    )
    if not remember:
        text = patch.body(
            text, '_cli_recall',
            'from _memlane_host import managed_cli_recall_fallback\n'
            '_fallback = managed_cli_recall_fallback()\n'
            'if _fallback is not None:\n'
            '    return _fallback',
        )
    return text


CAPTURE_BOUNDARY = '''

def begin_capture_boundary(transcript):
    import sys
    from _memlane_host import begin_capture_boundary as _begin
    return _begin(sys.modules[__name__], transcript)
'''


CLAUDE_FRESH_BOUNDARY = '''

_existing_capture_boundary = capture_boundary


def begin_capture_boundary(transcript, *, source=None, session_key=None):
    import sys
    from _memlane_host import claude_begin_capture_boundary
    return claude_begin_capture_boundary(sys.modules[__name__], transcript, source=source, session_key=session_key)


def capture_boundary(transcript, *, enabled=True):
    import sys
    from _memlane_host import claude_capture_boundary
    return claude_capture_boundary(sys.modules[__name__], _existing_capture_boundary, transcript, enabled=enabled)
'''


def files_for(kind, archive=None, *, source=None):
    resolved_archive = Path(archive) if archive is not None else resolve_official_archive(source)
    if kind == 'antigravity':
        from antigravity_plugin import files_for as antigravity_files
        return antigravity_files(resolved_archive)
    if kind not in ('claude_code', 'cursor'):
        raise ValueError('unsupported official host')
    manifest = json.loads((ENGINE / 'coding-memory-components.json').read_text())['plugin']
    try:
        actual_sha = _file_sha256_cached(resolved_archive)
    except OSError:
        actual_sha = None
    if actual_sha != manifest['archive_sha256']:
        raise ValueError('fixed official host archive mismatch')
    archive = resolved_archive
    prefix = 'integrations/' + ('claude-code' if kind == 'claude_code' else 'cursor') + '/'
    files = {}
    with tarfile.open(archive) as bundle:
        codex_coding = None
        for member in bundle:
            name = '/'.join(Path(member.name).parts[1:])
            if name == 'integrations/codex/plugins/cognee/scripts/_coding_memory.py' and member.isfile():
                codex_coding = bundle.extractfile(member).read()
            if not name.startswith(prefix):
                continue
            relative = name[len(prefix):]
            if not relative:
                continue
            if member.issym() or member.islnk() or Path(relative).is_absolute() or '..' in Path(relative).parts:
                raise ValueError('unsupported upstream host archive member')
            if member.isfile():
                files[relative] = bundle.extractfile(member).read()
    upstream = {name: hashlib.sha256(raw).hexdigest() for name, raw in files.items()}
    patch = shared_patch()
    for name in patch.FILES:
        path = 'scripts/' + name
        if kind == 'cursor' and name == '_coding_memory.py':
            if codex_coding is None:
                raise ValueError('fixed shared recall library missing')
            files[path] = patch.apply(name, codex_coding.decode()).encode()
            continue
        text = files[path].decode()
        if kind == 'claude_code' and name == 'session-start.py':
            text = claude_start(text, patch)
        elif kind == 'claude_code' and name == '_coding_memory.py':
            text = claude_capture(text, patch)
        elif kind == 'claude_code' and name in ('cognee-remember.sh', 'cognee-search.sh'):
            pass  # Official wrappers delegate to separately patched Python.
        elif kind == 'claude_code' and name == 'sync-session-to-graph.py':
            text = patch.replace(text, '            elif not wrote:\n                incomplete.append',
                                 '            elif not wrote and why != "managed_policy_paused":\n                incomplete.append')
            text = patch.replace(text, '    last_error: Exception | None = None\n',
                                 '    from _memlane_policy import managed\n    if managed():\n        attempts = 1\n    last_error: Exception | None = None\n')
        else:
            text = patch.apply(name, text)
        if name == '_recall_http.py':
            text = text.replace('return UNREACHABLE', 'return _error(0, "managed_server_unavailable") if managed() else UNREACHABLE')
        files[path] = text.encode()
    from project_capture_patch import apply as capture_patch
    common_text = files['scripts/_plugin_common.py'].decode()
    # Route direct unnumbered writes through the numbered warmup buffer so
    # Claude Code and Cursor match QI01/QI03 idempotent per-entry delivery.
    common_text = patch.body(common_text, 'remember_entry_via_http', '''from _memlane_policy import NotReady, managed
from _memlane_capture import numbered
if managed() and not numbered(entry) and capture_mode == "automatic":
    append_warmup_entry(dataset, session_id, entry)
    try:
        drained, remaining = drain_warmup_entries(dataset, session_id)
    except NotReady as exc:
        hook_log("drain_deferred_policy", {"dataset": dataset, "session": session_id, "error": str(exc)[:200]})
        return {"drained": 0, "buffered": 1}
    return {"drained": drained, "buffered": remaining}''')
    files['scripts/_plugin_common.py'] = capture_patch(common_text).encode()
    capture_sources = {name: (ENGINE / 'python' / name).read_bytes()
                       for name in ('project_capture_patch.py', 'plugin_capture.py')}
    files['scripts/_memlane_capture.py'] = capture_sources['plugin_capture.py']
    policy = ((ENGINE / 'python/project_plugin_policy.py').read_text() + CAPTURE_BOUNDARY
              + (CLAUDE_FRESH_BOUNDARY if kind == 'claude_code' else ''))
    policy = patch.body(policy, 'prepare_environment', 'os.environ.update(COGNEE_DISABLE_AUTO_MEMORY="false", COGNEE_UPDATE_CHECK="off", COGNEE_IDLE_DISABLED="true")')
    files['scripts/_memlane_policy.py'] = policy.encode()
    files['scripts/_memlane_access.py'] = (ENGINE / 'python/project_access_hooks.py').read_bytes()
    files['scripts/_memlane_host.py'] = (ENGINE / 'python/project_host_hooks.py').read_bytes()
    if kind == 'claude_code':
        worker = 'scripts/_cloud_worker.py'
        files[worker] = patch.body(files[worker].decode(), 'enabled', 'from _memlane_policy import managed\nif managed():\n    return False').encode()
        for name in ('cognee_remember.py', 'cognee_search.py'):
            files['scripts/' + name] = disable_python_fallback(files['scripts/' + name].decode(), patch,
                                                              remember=name == 'cognee_remember.py').encode()
    else:
        files = patch_cursor(files, patch)
    for name, raw in files.items():
        if name.endswith('.py'):
            ast.parse(raw.decode(), filename=name)
    return files, {'plugin_commit': manifest['commit'], 'host_kind': kind,
                   'archive_sha256': manifest['archive_sha256'], 'upstream_files': upstream,
                   'capture_patch_sha256': hashlib.sha256(b''.join(capture_sources.values())).hexdigest(),
                   'host_patch_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest()}


def patch_cursor(files, patch):
    text = files['scripts/session-context-lookup.py'].decode()
    text = patch.body(
        text, '_run',
        'from _memlane_host import cursor_context_lookup\n'
        '_managed_ctx = cursor_context_lookup(prompt, cwd)\n'
        'if _managed_ctx is not None:\n'
        '    return _managed_ctx',
    )
    files['scripts/session-context-lookup.py'] = text.encode()
    text = files['scripts/store-to-session.py'].decode()
    for function in ('_store_tool_call', '_store_assistant_stop'):
        text = patch.body(
            text, function,
            'from _capture_policy import capture_enabled\n'
            'from _memlane_access import pending_capture_allowed\n'
            'if not pending_capture_allowed(enabled=capture_enabled(), turn_id=str(payload.get("turn_id") or "")):\n'
            '    return',
        )
    text = patch.replace(
        text, '    pending = pop_pending_prompt(session_id, turn_id=str(payload.get("turn_id") or ""))\n',
        '    pending = pop_pending_prompt(session_id, turn_id=str(payload.get("turn_id") or ""))\n    if not pending.get("prompt"):\n        return\n',
    )
    files['scripts/store-to-session.py'] = text.encode()
    text = files['scripts/cursor_hook.py'].decode()
    text = patch.body(text, 'read_transcript_tail', 'return []')
    text = patch.replace(text, 'session: str, turn: str, script: str, text: str, root: Path | None = None\n',
                         'session: str, turn: str, script: str, text: str, root: Path | None = None, *, query: str = "", cwd: str = ""\n')
    text = patch.replace(text, 'json.dump({"turn": str(turn or ""), "context": text}, handle)',
                         'from _capture_policy import sanitize\n'
                         '            from _memlane_host import cursor_hold_record\n'
                         '            json.dump(cursor_hold_record(turn, query, cwd, sanitize), handle)')
    text = patch.replace(text, 'record["session"], record["turn"], script, prompt_context(output)\n',
                         'record["session"], record["turn"], script, prompt_context(output), query=str(normalized.get("prompt") or ""), cwd=str(normalized.get("cwd") or "")\n')
    text = patch.replace(text, '        text = held.get("context")\n',
                         '        from _env_file import load_env_file\n'
                         '        from _memlane_host import cursor_rebuild_held_context\n'
                         '        text = cursor_rebuild_held_context(load_env_file, session, held)\n')
    files['scripts/cursor_hook.py'] = text.encode()
    return files
