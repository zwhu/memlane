"""Build host-specific PA patches from the fixed official integration archive.

The original Codex artifact/manifest is untouched. All upstream bytes are
verified by the pinned archive digest before any host-specific edit is applied.
"""
import ast
import hashlib
import importlib.util
import json
from pathlib import Path
import tarfile

ENGINE = Path(__file__).resolve().parents[1]
ROOT = ENGINE.parent


def shared_patch():
    spec = importlib.util.spec_from_file_location('memlane_project_access_patch', ENGINE / 'patches/project_access.py')
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def claude_capture(text, patch):
    text = patch.apply('_coding_memory.py', text)
    text = patch.replace(text, 'def parse_claude(transcript_path: str) -> dict:',
                         'def parse_claude(transcript_path: str, start_offset: int = 0) -> dict:')
    text = patch.replace(text, '    for index, path in enumerate(_claude_files(transcript)):', '''    from _memlane_policy import managed
    # Sidechain files have independent histories. Managed capture only reads
    # this host session's bound transcript; subagents must bind independently.
    files = [transcript] if managed() else _claude_files(transcript)
    for index, path in enumerate(files):''')
    text = patch.replace(text, '        for record in _iter_jsonl(path):', '        for record in _iter_jsonl(path, start_offset if main else 0):')
    text = patch.replace(text, '            kind = record.get("type")\n            message = record.get("message")', '''            kind = record.get("type")
            if start_offset and not units and kind != "user":
                continue
            message = record.get("message")''')
    text = patch.replace(text, 'else parse_claude(transcript_path)\n', 'else parse_claude(transcript_path, start_offset)\n')
    return text


def claude_start(text, patch):
    return patch.replace(text, '    os.environ["COGNEE_SESSION_KEY"] = session_key\n    if payload.get("source")', '''    os.environ["COGNEE_SESSION_KEY"] = session_key
    from _memlane_policy import bind, begin_capture_boundary, NotReady
    try:
        config["dataset"] = bind(cwd, session_key)
        begin_capture_boundary(str(payload.get("transcript_path") or ""))
    except (NotReady, OSError, ValueError):
        return {"hookSpecificOutput": {"hookEventName": "SessionStart"}}
    if payload.get("source")''')


def disable_python_fallback(text, patch, *, remember=False):
    text = patch.body(text, '_runtime', '''from _env_file import load_env_file
load_env_file()
from _memlane_policy import managed, dataset as managed_dataset
if managed():
    from _plugin_common import get_session_key, set_session_key, resolve_cognee_session_id, shell_runtime_overrides
    runtime = shell_runtime_overrides(os.environ["COGNEE_BASE_URL"])
    if not runtime.get("host_key"):
        raise RuntimeError("managed_host_session_required")
    set_session_key(runtime["host_key"])
    target = managed_dataset()
    return {"service_url": os.environ["COGNEE_BASE_URL"], "api_key": os.environ["COGNEE_API_KEY"],
            "dataset": target, "dataset_id": target, "dataset_ids": target,
            "session_id": resolve_cognee_session_id(get_session_key()), "recall_auto": ""}''')
    if not remember:
        text = patch.body(text, '_cli_recall', '''from _memlane_policy import managed
if managed():
    return json.dumps({"error": "managed_server_unavailable", "authoritative": False})''')
    return text


CAPTURE_BOUNDARY = '''

def begin_capture_boundary(transcript):
    """Record only a stat at installation/session activation; never read history."""
    with _state() as state:
        if state.get("host_boundary_established"):
            return
        if not transcript:
            raise NotReady("host_transcript_required")
        try:
            state["cursor"] = _position(transcript)
        except FileNotFoundError:
            # Unknown material arriving later cannot be assumed to be new.
            # The first live capture establishes its stat and skips that turn.
            state["paused"] = True
        state["host_boundary_established"] = True
'''


def files_for(kind, archive=None):
    if kind not in ('claude_code', 'cursor'):
        raise ValueError('unsupported official host')
    manifest = json.loads((ENGINE / 'coding-memory-components.json').read_text())['plugin']
    archive = Path(archive or ROOT / '.cache/coding-memory-components' / (manifest['commit'] + '.tar.gz'))
    if archive.is_symlink() or hashlib.sha256(archive.read_bytes()).hexdigest() != manifest['archive_sha256']:
        raise ValueError('fixed official host archive mismatch')
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
            # Same fixed commit's official coding recall library; Cursor's
            # native adapter/collector remains its own upstream implementation.
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
            text = patch.replace(text, '    last_error: Exception | None = None\n', '''    from _memlane_policy import managed
    if managed():
        attempts = 1
    last_error: Exception | None = None
''')
        else:
            text = patch.apply(name, text)
        if name == '_recall_http.py':
            text = text.replace('return UNREACHABLE', 'return _error(0, "managed_server_unavailable") if managed() else UNREACHABLE')
        files[path] = text.encode()
    policy = (ENGINE / 'python/project_plugin_policy.py').read_text() + CAPTURE_BOUNDARY
    policy = patch.body(policy, 'prepare_environment', '''os.environ.update(COGNEE_DISABLE_AUTO_MEMORY="false", COGNEE_UPDATE_CHECK="off", COGNEE_IDLE_DISABLED="true")''')
    files['scripts/_memlane_policy.py'] = policy.encode()
    if kind == 'claude_code':
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
                   'host_patch_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest()}


def patch_cursor(files, patch):
    text = files['scripts/session-context-lookup.py'].decode()
    text = patch.body(text, '_run', '''from _memlane_policy import managed
if managed():
    from _coding_memory import build_block
    context = build_block(prompt, cwd)
    return {"hookSpecificOutput": {"hookEventName": "UserPromptSubmit", "additionalContext": context}}''')
    files['scripts/session-context-lookup.py'] = text.encode()
    text = files['scripts/store-to-session.py'].decode()
    for function in ('_store_tool_call', '_store_assistant_stop'):
        text = patch.body(text, function, '''from _memlane_policy import permit
from _capture_policy import capture_enabled
if not capture_enabled() or not permit("capture"):
    return''')
    text = patch.replace(text, '    pending = pop_pending_prompt(session_id, turn_id=str(payload.get("turn_id") or ""))\n', '''    pending = pop_pending_prompt(session_id, turn_id=str(payload.get("turn_id") or ""))
    if not pending.get("prompt"):
        return
''')
    files['scripts/store-to-session.py'] = text.encode()
    text = files['scripts/cursor_hook.py'].decode()
    # Tail scraping can read a resumed history before this managed connection.
    text = patch.body(text, 'read_transcript_tail', 'return []')
    # Cursor defers injection to a later tool result. Rebuild at that moment;
    # a permission change must never leave an already-held block readable.
    text = patch.replace(text, 'session: str, turn: str, script: str, text: str, root: Path | None = None\n',
                         'session: str, turn: str, script: str, text: str, root: Path | None = None, *, query: str = "", cwd: str = ""\n')
    text = patch.replace(text, 'json.dump({"turn": str(turn or ""), "context": text}, handle)', '''from _capture_policy import sanitize
            json.dump({"turn": str(turn or ""), "context": "managed_deferred_recall", "query": sanitize(query)[:16000], "cwd": cwd}, handle)''')
    text = patch.replace(text, 'record["session"], record["turn"], script, prompt_context(output)\n',
                         'record["session"], record["turn"], script, prompt_context(output), query=str(normalized.get("prompt") or ""), cwd=str(normalized.get("cwd") or "")\n')
    text = patch.replace(text, '        text = held.get("context")\n', '''        from _env_file import load_env_file
        load_env_file()
        from _plugin_common import set_session_key
        from _coding_memory import build_block
        set_session_key(session)
        text = build_block(str(held.get("query") or ""), str(held.get("cwd") or "")) if "query" in held else ""
''')
    files['scripts/cursor_hook.py'] = text.encode()
    return files
