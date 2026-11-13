"""Exact edits to the pinned Codex plugin; applied only to a separate artifact.

Every anchor must match exactly once. Unmanaged behavior stays upstream; managed
installs use independent credentials, UUID routing and operation policy gates.
"""
import ast


def replace(text, old, new):
    if old.startswith(' '):
        old, new = '\n' + old, '\n' + new
    if text.count(old) != 1:
        raise ValueError('fixed upstream patch anchor does not match exactly once')
    return text.replace(old, new, 1)


def body(text, function, code):
    tree = ast.parse(text)
    node = next(item for item in tree.body if isinstance(item, (ast.FunctionDef, ast.AsyncFunctionDef)) and item.name == function)
    first = node.body[0]
    index = first.end_lineno if isinstance(first, ast.Expr) and isinstance(first.value, ast.Constant) and isinstance(first.value.value, str) else first.lineno - 1
    lines = text.splitlines(keepends=True)
    lines.insert(index, ''.join('    ' + line + '\n' if line else '\n' for line in code.strip('\n').splitlines()))
    return ''.join(lines)


def apply(name, text):
    if name == '_code_graph.py':
        # A child can otherwise create its own slug-named code group and become
        # owner. Gate before any repository scan or transport, including direct
        # helper calls; the desktop owner's existing graph path is independent.
        text = body(text, 'index_repo', '''from _env_file import load_env_file
load_env_file()
from _memlane_policy import managed, code_target, NotReady
if managed():
    try:
        dataset = code_target("write", dataset)
    except NotReady as error:
        return _error(403, str(error))''')
        text = body(text, 'do_index_repo', '''from _env_file import load_env_file
load_env_file()
from _memlane_policy import managed, code_target, NotReady
if managed():
    try:
        dataset = code_target("write", dataset)
    except NotReady as error:
        return _error(403, str(error))
    service_url = os.environ["COGNEE_BASE_URL"]
    api_key = os.environ["COGNEE_API_KEY"]''')
        text = replace(text, '("datasetName", dataset),', '("datasetId" if managed() else "datasetName", dataset),')
        for function in ('autoindex_on_session_start', 'reingest_if_changed'):
            text = body(text, function, '''from _env_file import load_env_file
load_env_file()
from _memlane_policy import managed, code_target, NotReady
if managed():
    try:
        code_target("write", automatic=True)
    except NotReady:
        return {}''')
        text = body(text, 'auto_code_lane', '''from _env_file import load_env_file
load_env_file()
from _memlane_policy import managed, code_target, NotReady
if managed():
    try:
        code_target("read")
    except NotReady:
        return {}''')
        text = replace(text, '    return {\n        "dataset": str(state["dataset"]),', '''    if managed():
        try:
            code_target("read", str(state["dataset"]))
        except NotReady:
            return {}
    return {
        "dataset": str(state["dataset"]),''')
    elif name == '_env_file.py':
        text = replace(text, '    global _loaded\n    if _loaded:\n', '''    global _loaded
    if not _loaded:
        from _memlane_policy import prepare_environment
        prepare_environment(parse_env_file)
    if _loaded:
''')
    elif name == 'config.py':
        text = replace(text, '    return config\n', '''    from _memlane_policy import managed
    if managed():
        if not config.get("base_url") or not config.get("api_key"):
            raise RuntimeError("managed_official_connection_required")
        config["plugin_identity"] = "false"
        config["shared_agent_memory"] = False
        config["managed_endpoint"] = True
        os.environ["COGNEE_PLUGIN_IDENTITY"] = "false"
        os.environ["COGNEE_SHARED_AGENT_MEMORY"] = "false"
        os.environ["COGNEE_MANAGED_ENDPOINT"] = "true"
        os.environ["COGNEE_SESSION_COMPANION_DATASET"] = "false"
    return config
''')
        text = body(text, 'get_dataset', '''from _memlane_policy import managed, dataset
if managed():
    return dataset()''')
        text = body(text, 'ensure_dataset_ready_via_api', '''from _memlane_policy import managed, require
if managed():
    require("write", dataset)
    return''')
    elif name == 'session-start.py':
        text = replace(text, '    os.environ["COGNEE_SESSION_KEY"] = session_key\n\n    # Resolve (and persist)', '''    os.environ["COGNEE_SESSION_KEY"] = session_key
    from _memlane_policy import managed, bind, NotReady
    if managed():
        try:
            config["dataset"] = bind(cwd, session_key)
        except NotReady:
            hook_log("managed_project_not_ready", {"code": "project_binding_unconfirmed"})
            return {"hookSpecificOutput": {"hookEventName": "SessionStart"}}

    # Resolve (and persist)''')
    elif name == '_coding_memory.py':
        text = body(text, 'begin_turn', '''from _memlane_policy import managed, permit
if managed() and not permit("capture"):
    return''')
        text = replace(text, 'def _iter_jsonl(path: Path):\n', 'def _iter_jsonl(path: Path, start_offset: int = 0):\n')
        text = replace(text, '        with open(path, encoding="utf-8", errors="replace") as handle:\n            for line in handle:\n', '''        with open(path, "rb") as handle:
            if start_offset:
                handle.seek(start_offset)
                # A stat-only boundary cannot know whether it split a JSONL
                # record. Drop the first suffix, never read back its prefix.
                handle.readline()
            for raw in handle:
                line = raw.decode("utf-8", errors="replace")
''')
        text = replace(text, 'def parse_codex(transcript_path: str) -> dict:\n', 'def parse_codex(transcript_path: str, start_offset: int = 0) -> dict:\n')
        text = replace(text, '    for record in _iter_jsonl(path):\n', '    for record in _iter_jsonl(path, start_offset):\n')
        text = replace(text, '        kind = payload.get("type")\n        if kind == "task_complete"', '''        kind = payload.get("type")
        if start_offset and not units:
            # Ignore a tool/answer whose request began before the allowed
            # boundary, including a partially recorded pre-resume turn.
            item = payload.get("item") or {}
            if kind != "item_completed" or item.get("type") != "UserMessage":
                continue
        if kind == "task_complete"''')
        text = replace(text, 'def parse_transcript(transcript_path: str) -> dict:\n    return parse_codex(transcript_path) if agent() == "codex" else parse_claude(transcript_path)\n', '''def parse_transcript(transcript_path: str, start_offset: int = 0) -> dict:
    return parse_codex(transcript_path, start_offset) if agent() == "codex" else parse_claude(transcript_path)
''')
        text = replace(text, '    if not capture_enabled():\n', '''    from _memlane_policy import managed, capture_boundary, NotReady
    start_offset = 0
    if managed():
        try:
            start_offset = capture_boundary(str(payload.get("transcript_path") or ""), enabled=capture_enabled())
        except (NotReady, OSError, ValueError):
            start_offset = None
        if start_offset is None:
            return {"skipped": "managed_capture_paused"}
    if not capture_enabled():
''')
        text = replace(text, '    parsed = parse_transcript(str(payload.get("transcript_path") or ""))\n', '    parsed = parse_transcript(str(payload.get("transcript_path") or ""), start_offset)\n')
        text = body(text, '_deliver', '''from _memlane_policy import require
require("capture", dataset)''')
        text = body(text, 'build_block', '''from _memlane_policy import managed, permit
if managed() and not permit("read"):
    return ""''')
        text = replace(text, 'def build_block(query: str, cwd: str) -> str:\n', 'def _project_build_block(query: str, cwd: str, deadline=None) -> str:\n')
        text = replace(text, '    deadline = time.monotonic() + DELIVERY_BUDGET_SECONDS\n    session_id, dataset = _session()\n', '    deadline = deadline or (time.monotonic() + DELIVERY_BUDGET_SECONDS)\n    session_id, dataset = _session()\n')
        text = replace(text, '    dataset_id, lessons = list_lessons(dataset, project, deadline)\n', '    dataset_id, lessons = list_lessons(dataset, project, deadline, max_fetches=4 if managed() else MAX_BODY_FETCHES_ON_PROMPT)\n')
        text = replace(text, '    bodies = {data_id: _cached_body(data_id) for _, _, data_id in candidates}\n', '''    from _memlane_policy import managed
    if managed():
        candidates = candidates[:22]
    bodies = {data_id: _cached_body(data_id) for _, _, data_id in candidates}
''')
        text = body(text, 'resolve_dataset_id', '''from _memlane_policy import managed
if managed():
    from _dataset_access import dataset_id as parse_dataset_id
    return parse_dataset_id(dataset)''')
        text = body(text, '_remaining', '''from _memlane_policy import managed, NotReady
if managed() and deadline <= time.monotonic():
    raise NotReady("recall_budget_exhausted")''')
        text += '''

def build_block(query: str, cwd: str) -> str:
    from _memlane_policy import managed, merge_block
    if managed():
        return merge_block(query, cwd, _project_build_block)
    return _project_build_block(query, cwd)
'''
        text = body(text, 'deliver', '''from _memlane_policy import managed, permit
if managed() and not permit("recall"):
    reset_delivery(session_key)
    return ""''')
        text = body(text, 'warm_lessons', '''from _memlane_policy import managed, permit
if managed() and not permit("read"):
    return 0''')
    elif name == '_plugin_common.py':
        text = body(text, 'shared_memory_enabled', '''from _memlane_policy import managed
if managed():
    return False''')
        text = body(text, 'plugin_identity_mode', '''from _memlane_policy import managed
if managed():
    return "disabled"''')
        text = body(text, '_api_key_with_source', '''from _memlane_policy import managed, NotReady
if managed():
    key = str(os.environ.get("COGNEE_API_KEY") or "").strip()
    if not key:
        raise NotReady("managed_agent_credential_required")
    return key, "managed_agent_key"''')
        text = body(text, 'active_agent_key', '''from _memlane_policy import managed
if managed():
    return str(os.environ.get("COGNEE_API_KEY") or "").strip()''')
        text = body(text, 'remember_entry_via_http', '''from _memlane_policy import require
require("write", dataset)''')
        text = body(text, 'append_warmup_entry', '''from _memlane_policy import require
require("capture", dataset)''')
        text = body(text, 'ensure_dataset_via_http', '''from _memlane_policy import managed, require
if managed():
    require("write", dataset)
    return''')
        text = body(text, 'improve_session_via_http', '''from _memlane_policy import require
require("process", dataset)''')
        text = body(text, 'run_session_improve_detailed', '''from _memlane_policy import managed, permit
if managed() and not permit("process", dataset, automatic=trigger not in ("manual", "switch")):
    return {"ok": False, "reason": "managed_policy_paused", "error": "automatic_processing_paused"}''')
        text = body(text, 'drain_warmup_entries', '''from _memlane_policy import managed, permit
if managed() and not permit("drain", dataset):
    snapshot = (_load_json_file(_bridge_file(session_id)).get(_bridge_cache_key(dataset, session_id)) or {}).get("pending_entries") or []
    return 0, len(snapshot)''')
        text = replace(text, '        for entry in snapshot:\n            budget_left', '''        for entry in snapshot:
            if managed() and not permit("drain", dataset):
                break
            budget_left''')
        text = replace(text, '            if ambiguous and verified and _entry_fingerprint(send_entry) in server_prints:\n', '''            if managed() and ambiguous and (not verified or _entry_fingerprint(send_entry) not in server_prints):
                from _memlane_policy import hold
                hold("write_result_unknown")
                break
            if ambiguous and verified and _entry_fingerprint(send_entry) in server_prints:
''')
        text = replace(text, '            except urllib.error.HTTPError as exc:\n                http_failure = True\n', '''            except urllib.error.HTTPError as exc:
                from _memlane_policy import block
                block(exc.code)
                if managed() and exc.code in (401, 403):
                    from _memlane_policy import stamp_pending_entry
                    stamp_pending_entry(dataset, session_id, entry, False)
                http_failure = True
''')
        text = replace(text, '                send_entry = sanitize(send_entry)\n                remember_entry_via_http(\n', '''                send_entry = sanitize(send_entry)
                if managed():
                    from _memlane_policy import stamp_pending_entry
                    stamp_pending_entry(dataset, session_id, entry, True)
                remember_entry_via_http(
''')
        # Permission denial is sticky even if another entry point catches it.
        text = replace(text, '    with urlopen_following_307(req, timeout=timeout, context=_https_context()) as resp:\n        body = resp.read().decode("utf-8")\n        if not body:\n            return None\n        return json.loads(body)\n', '''    try:
        with urlopen_following_307(req, timeout=timeout, context=_https_context()) as resp:
            body = resp.read().decode("utf-8")
            if not body:
                return None
            return json.loads(body)
    except urllib.error.HTTPError as exc:
        from _memlane_policy import block
        if method not in ("GET", "HEAD") and path.split("?")[0] in ("/api/v1/remember/entry", "/api/v1/improve"):
            block(exc.code)
        raise
''')
    elif name == '_remember_http.py':
        text = body(text, 'do_remember', '''from _env_file import load_env_file
load_env_file()
from _memlane_policy import managed, permit
if managed() and not permit("write", dataset_id or dataset):
    return _error(403, "当前 Agent 没有此项目的写入权限")
if managed():
    if service_url.rstrip("/") != os.environ.get("COGNEE_BASE_URL", "").rstrip("/"):
        return _error(403, "managed_service_mismatch")
    api_key = os.environ["COGNEE_API_KEY"]''')
        text = replace(text, '    except urllib.error.HTTPError as e:\n        if e.code in (401, 403):\n', '''    except urllib.error.HTTPError as e:
        from _memlane_policy import block
        block(e.code)
        if e.code in (401, 403):
''')
        if text.count('        return UNREACHABLE\n') != 2:
            raise ValueError('remember fallback anchors changed')
        text = text.replace('        return UNREACHABLE\n', '        return _error(0, "managed_server_unreachable") if managed() else UNREACHABLE\n')
    elif name == '_recall_http.py':
        text = body(text, 'do_recall', '''from _env_file import load_env_file
load_env_file()
from _memlane_policy import managed, recall_targets, NotReady
if managed():
    import os
    if service_url.rstrip("/") != os.environ.get("COGNEE_BASE_URL", "").rstrip("/"):
        return _error(403, "managed_service_mismatch")
    try:
        dataset_ids, project_id = recall_targets(dataset, dataset_ids)
    except NotReady:
        return _error(403, "project_read_denied")
    dataset = ""
    api_key = os.environ["COGNEE_API_KEY"]
    if any(target != project_id for target in dataset_ids):
        session_id, scope = "", ["graph"]''')
    elif name in ('cognee-remember.sh', 'cognee-search.sh'):
        text = replace(text, 'except Exception:\n    pass\nservice_url =', 'except Exception:\n    raise SystemExit("managed_configuration_required")\nservice_url =')
        if name == 'cognee-search.sh':
            return text
        text = replace(text, 'elif [ -n "$FILE_PATH" ]; then\n', '''elif [ -f "${SELF_DIR}/_memlane_policy.py" ]; then
    echo '{"error":"managed_server_unreachable","status":0}'
    exit 1
elif [ -n "$FILE_PATH" ]; then
''')
    elif name == 'store-user-prompt.py':
        text = body(text, '_store', '''from _memlane_policy import managed, permit
if managed() and not permit("capture"):
    return''')
    elif name == 'sync-session-to-graph.py':
        text = replace(text, '            elif not wrote:\n                incomplete.append', '            elif not wrote and why != "managed_policy_paused":\n                incomplete.append')
        text = replace(text, '    # --strict: the caller', '''    from _memlane_policy import managed
    if managed():
        # A submission with unknown/partial outcome must be inspected, never
        # blindly re-driven by the upstream detached final retry loop.
        attempts = 1

    # --strict: the caller''')
    else:
        raise ValueError('file is outside managed patch allowlist')
    return text


FILES = ('config.py', 'session-start.py', '_coding_memory.py', '_plugin_common.py',
         '_remember_http.py', 'cognee-remember.sh', 'store-user-prompt.py', 'sync-session-to-graph.py',
         '_env_file.py', '_recall_http.py', 'cognee-search.sh', '_code_graph.py')
