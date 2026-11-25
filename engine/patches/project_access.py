"""Exact edits to the pinned Codex plugin; applied only to a separate artifact.

Every anchor must match exactly once. Unmanaged behavior stays upstream; managed
installs delegate policy, credential, and access gates to standalone helpers
(_memlane_policy.py and _memlane_access.py).
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
        text = body(text, 'index_repo', '''from _env_file import load_env_file
from _memlane_access import gate_code_index
dataset, _err = gate_code_index(load_env_file, dataset, spec, error_fn=_error)
if _err is not None:
    return _err''')
        text = body(text, 'do_index_repo', '''from _env_file import load_env_file
from _memlane_access import code_dataset_field, gate_code_do_index
dataset, service_url, api_key, _err = gate_code_do_index(load_env_file, dataset, repo_spec, service_url, api_key, os.environ, error_fn=_error)
if _err is not None:
    return _err''')
        text = replace(text, '("datasetName", dataset),', '(code_dataset_field(), dataset),')
        for function in ('autoindex_on_session_start', 'reingest_if_changed'):
            text = body(text, function, '''from _env_file import load_env_file
from _memlane_access import gate_code_auto
if not gate_code_auto(load_env_file, "write", automatic=True):
    return {}''')
        text = body(text, 'auto_code_lane', '''from _env_file import load_env_file
from _memlane_access import gate_code_auto, gate_code_lane_dataset
if not gate_code_auto(load_env_file, "read"):
    return {}''')
        text = replace(text, '    return {\n        "dataset": str(state["dataset"]),',
                       '    if not gate_code_lane_dataset(str(state["dataset"])):\n        return {}\n    return {\n        "dataset": str(state["dataset"]),')
    elif name == '_env_file.py':
        text = replace(text, '    global _loaded\n    if _loaded:\n',
                       '    global _loaded\n    if not _loaded:\n        from _memlane_policy import prepare_environment\n        prepare_environment(parse_env_file)\n    if _loaded:\n')
    elif name == 'config.py':
        text = replace(text, '    return config\n',
                       '    from _memlane_access import configure_managed_config\n    return configure_managed_config(config, os.environ)\n')
        text = body(text, 'get_dataset', 'from _memlane_policy import managed, dataset\nif managed():\n    return dataset()')
        text = body(text, 'ensure_dataset_ready_via_api', 'from _memlane_policy import managed, require\nif managed():\n    require("write", dataset)\n    return')
    elif name == 'session-start.py':
        text = replace(text, '    os.environ["COGNEE_SESSION_KEY"] = session_key\n\n    # Resolve (and persist)',
                       '    os.environ["COGNEE_SESSION_KEY"] = session_key\n'
                       '    from _memlane_access import bind_session_start\n'
                       '    _early = bind_session_start(config, cwd, session_key, hook_log)\n'
                       '    if _early is not None:\n'
                       '        return _early\n\n'
                       '    # Resolve (and persist)')
    elif name == '_coding_memory.py':
        text = body(text, 'begin_turn', 'from _memlane_policy import managed, permit\nif managed() and not permit("capture"):\n    return')
        text = replace(text, 'def _iter_jsonl(path: Path):\n', 'def _iter_jsonl(path: Path, start_offset: int = 0):\n')
        text = replace(text, '        with open(path, encoding="utf-8", errors="replace") as handle:\n            for line in handle:\n',
                       '        from _memlane_access import iter_jsonl_lines\n        for line in iter_jsonl_lines(path, start_offset):\n')
        text = replace(text, 'def parse_codex(transcript_path: str) -> dict:\n', 'def parse_codex(transcript_path: str, start_offset: int = 0) -> dict:\n')
        text = replace(text, '    for record in _iter_jsonl(path):\n', '    for record in _iter_jsonl(path, start_offset):\n')
        text = replace(text, '        kind = payload.get("type")\n        if kind == "task_complete"',
                       '        kind = payload.get("type")\n'
                       '        from _memlane_access import skip_pre_boundary_codex_record\n'
                       '        if skip_pre_boundary_codex_record(start_offset, units, payload):\n'
                       '            continue\n'
                       '        if kind == "task_complete"')
        text = replace(text, 'def parse_transcript(transcript_path: str) -> dict:\n    return parse_codex(transcript_path) if agent() == "codex" else parse_claude(transcript_path)\n',
                       'def parse_transcript(transcript_path: str, start_offset: int = 0) -> dict:\n    return parse_codex(transcript_path, start_offset) if agent() == "codex" else parse_claude(transcript_path)\n')
        text = replace(text, '    if not capture_enabled():\n',
                       '    from _memlane_access import resolve_capture_offset\n'
                       '    start_offset = resolve_capture_offset(payload, capture_enabled())\n'
                       '    if start_offset is None:\n'
                       '        return {"skipped": "managed_capture_paused"}\n'
                       '    if not capture_enabled():\n')
        text = replace(text, '    parsed = parse_transcript(str(payload.get("transcript_path") or ""))\n',
                       '    parsed = parse_transcript(str(payload.get("transcript_path") or ""), start_offset)\n')
        text = body(text, '_deliver', 'from _memlane_policy import require\nrequire("capture", dataset)')
        text = body(text, 'build_block', 'from _memlane_policy import managed, permit\nif managed() and not permit("read"):\n    return ""')
        text = replace(text, 'def build_block(query: str, cwd: str) -> str:\n', 'def _project_build_block(query: str, cwd: str, deadline=None) -> str:\n')
        text = replace(text, '    deadline = time.monotonic() + DELIVERY_BUDGET_SECONDS\n    session_id, dataset = _session()\n',
                       '    deadline = deadline or (time.monotonic() + DELIVERY_BUDGET_SECONDS)\n    session_id, dataset = _session()\n')
        text = replace(text, '    return render_block(dataset, max(1, sessions), chosen, root, in_git, line_for)\n',
                       '    from _memlane_access import stage_chosen_recall\n'
                       '    stage_chosen_recall(dataset_id, chosen, line_for)\n'
                       '    return render_block(dataset, max(1, sessions), chosen, root, in_git, line_for)\n')
        text = body(text, 'resolve_dataset_id', 'from _memlane_policy import managed\nif managed():\n    from _dataset_access import dataset_id as parse_dataset_id\n    return parse_dataset_id(dataset)')
        text = body(text, '_remaining', 'from _memlane_policy import managed, NotReady\nif managed() and deadline <= time.monotonic():\n    raise NotReady("recall_budget_exhausted")')
        text += '\n\ndef build_block(query: str, cwd: str) -> str:\n    from _memlane_policy import managed, merge_block\n    return merge_block(query, cwd, _project_build_block) if managed() else _project_build_block(query, cwd)\n'
        text = body(text, 'deliver', 'from _memlane_policy import managed, permit\nif managed() and not permit("recall"):\n    reset_delivery(session_key)\n    return ""')
        text = body(text, 'warm_lessons', 'from _memlane_policy import managed, permit\nif managed() and not permit("read"):\n    return 0')
    elif name == '_plugin_common.py':
        text = body(text, 'shared_memory_enabled', 'from _memlane_policy import managed\nif managed():\n    return False')
        text = body(text, 'plugin_identity_mode', 'from _memlane_policy import managed\nif managed():\n    return "disabled"')
        text = body(text, '_api_key_with_source', 'from _memlane_access import managed_api_key_with_source\n_managed_key = managed_api_key_with_source(os.environ)\nif _managed_key is not None:\n    return _managed_key')
        text = body(text, 'active_agent_key', 'from _memlane_policy import managed\nif managed():\n    return str(os.environ.get("COGNEE_API_KEY") or "").strip()')
        text = replace(text, '    dataset_id: str | None = None,\n    timeout: float = 30.0,\n',
                       '    dataset_id: str | None = None,\n    timeout: float = 30.0,\n    capture_mode: str = "automatic",\n')
        text = body(text, 'remember_entry_via_http', 'from _memlane_policy import require_capture_write\nrequire_capture_write(dataset, capture_mode)')
        if '    return _json_http_request("/api/v1/remember/entry", payload, timeout=timeout)\n' in text:
            text = replace(text, '    return _json_http_request("/api/v1/remember/entry", payload, timeout=timeout)\n',
                           '    return _json_http_request("/api/v1/remember/entry", payload, timeout=timeout, capture_mode=capture_mode)\n')
        else:
            text = replace(text, '        {"entry": entry, **fields, "session_id": session_id},\n        timeout=timeout,\n',
                           '        {"entry": entry, **fields, "session_id": session_id},\n        timeout=timeout, capture_mode=capture_mode,\n')
        text = body(text, 'append_warmup_entry', 'from _memlane_policy import require\nrequire("capture", dataset)')
        text = replace(text, '    data[turn_key] = entry\n    data[session_key] = entry\n',
                       '    from _memlane_policy import managed\n'
                       '    if managed():\n'
                       '        from _memlane_access import pending_capture_allowed\n'
                       '        from _capture_policy import capture_enabled\n'
                       '        pending_capture_allowed(session_id, enabled=capture_enabled(), turn_id=turn_id,\n'
                       '                                new_turn=True, pending_entry=entry)\n'
                       '        return\n'
                       '    data[turn_key] = entry\n    data[session_key] = entry\n')
        text = body(text, 'ensure_dataset_via_http', 'from _memlane_policy import managed, require\nif managed():\n    require("write", dataset)\n    return')
        text = body(text, 'improve_session_via_http', 'from _memlane_policy import require\nrequire("process", dataset)')
        text = body(text, 'run_session_improve_detailed', 'from _memlane_policy import managed, permit\nif managed() and not permit("process", dataset, automatic=trigger not in ("manual", "switch")):\n    return {"ok": False, "reason": "managed_policy_paused", "error": "automatic_processing_paused"}')
        text = replace(text, '    dataset: str, session_id: str, budget_seconds: float | None = None\n) -> tuple:',
                       '    dataset: str, session_id: str, budget_seconds: float | None = None, *, capture_mode: str = "automatic"\n) -> tuple:')
        text = body(text, 'drain_warmup_entries', 'from _memlane_policy import managed, permit\nfrom _memlane_access import managed_drain_precheck\n_drain_operation = "write" if capture_mode == "manual" else "drain"\n_paused = managed_drain_precheck(dataset, session_id, _load_json_file, _bridge_file, _bridge_cache_key, operation=_drain_operation)\nif _paused is not None:\n    return _paused')
        text = replace(text, '        for entry in snapshot:\n            budget_left',
                       '        for entry in snapshot:\n            if managed() and not permit(_drain_operation, dataset):\n                break\n            budget_left')
        text = replace(text, '            if ambiguous and verified and _entry_fingerprint(send_entry) in server_prints:\n',
                       '            if managed() and ambiguous and (not verified or _entry_fingerprint(send_entry) not in server_prints):\n'
                       '                from _memlane_policy import hold\n'
                       '                hold("write_result_unknown")\n'
                       '                break\n'
                       '            if ambiguous and verified and _entry_fingerprint(send_entry) in server_prints:\n')
        text = replace(text, '            except urllib.error.HTTPError as exc:\n                http_failure = True\n',
                       '            except urllib.error.HTTPError as exc:\n'
                       '                from _memlane_access import handle_drain_http_error\n'
                       '                if handle_drain_http_error(dataset, session_id, entry, exc, was_ambiguous=ambiguous):\n'
                       '                    break\n'
                       '                http_failure = True\n')
        text = replace(text, '                send_entry = sanitize(send_entry)\n                remember_entry_via_http(\n',
                       '                send_entry = sanitize(send_entry)\n'
                       '                from _memlane_access import stamp_before_send\n'
                       '                stamp_before_send(dataset, session_id, entry)\n'
                       '                remember_entry_via_http(\n')
        text = replace(text, '                    send_entry,\n                    timeout=min(30.0, max(1.0, budget_left)),\n',
                       '                    send_entry,\n                    timeout=min(30.0, max(1.0, budget_left)),\n'
                       '                    capture_mode=capture_mode,\n')
        text = replace(text,
                       '    api_key: str | None = None,\n):\n    # base_url overrides',
                       '    api_key: str | None = None,\n    capture_mode: str | None = None,\n):\n    # base_url overrides')
        text = replace(text, '    headers = {"Content-Type": "application/json"}\n    if api_key:',
                       '    headers = {"Content-Type": "application/json"}\n'
                       '    if capture_mode in ("manual", "automatic"):\n'
                       '        headers["X-Memlane-Capture-Mode"] = capture_mode\n'
                       '    if api_key:')
        text = replace(text, '    with urlopen_following_307(req, timeout=timeout, context=_https_context()) as resp:\n        body = resp.read().decode("utf-8")\n        if not body:\n            return None\n        return json.loads(body)\n',
                       '    try:\n'
                       '        with urlopen_following_307(req, timeout=timeout, context=_https_context()) as resp:\n'
                       '            body = resp.read().decode("utf-8")\n'
                       '            if not body:\n'
                       '                return None\n'
                       '            return json.loads(body)\n'
                       '    except urllib.error.HTTPError as exc:\n'
                       '        from _memlane_access import record_managed_http_denial\n'
                       '        record_managed_http_denial(method, path, exc)\n'
                       '        raise\n')
    elif name == '_remember_http.py':
        text = body(text, 'do_remember', '''from _env_file import load_env_file
from _memlane_access import is_managed as managed, prepare_managed_remember
_err, api_key = prepare_managed_remember(load_env_file, service_url, api_key, dataset_id or dataset, os.environ, error_fn=_error)
if _err is not None:
    return _err''')
        text = replace(text, '    except urllib.error.HTTPError as e:\n        if e.code in (401, 403):\n',
                       '    except urllib.error.HTTPError as e:\n        from _memlane_policy import block, is_capture_paused\n        if not is_capture_paused(e):\n            block(e.code)\n        if e.code in (401, 403):\n')
        if text.count('        return UNREACHABLE\n') != 2:
            raise ValueError('remember fallback anchors changed')
        text = text.replace('        return UNREACHABLE\n', '        return _error(0, "managed_server_unreachable") if managed() else UNREACHABLE\n')
    elif name == '_recall_http.py':
        text = body(text, 'do_recall', '''from _env_file import load_env_file
from _memlane_access import is_managed as managed, prepare_managed_recall
_err, api_key, dataset, dataset_ids, session_id, scope = prepare_managed_recall(load_env_file, service_url, api_key, dataset, dataset_ids, session_id, scope, os.environ, error_fn=_error)
if _err is not None:
    return _err''')
        text = replace(text, '    if isinstance(data, list):\n        return data\n    return [data]\n',
                       '    from _memlane_access import report_managed_explicit_recall\n    report_managed_explicit_recall(data)\n    if isinstance(data, list):\n        return data\n    return [data]\n')
    elif name in ('cognee-remember.sh', 'cognee-search.sh', 'cognee-forget.sh', 'cognee-index-repo.sh'):
        text = replace(text, 'except Exception:\n    pass\nservice_url =', 'except Exception:\n    raise SystemExit("managed_configuration_required")\nservice_url =')
        text = replace(text, 'SERVICE_URL="$(python3 -',
                       'if [ -z "$runtime_json" ]; then\n    echo \'{"error":"managed_configuration_required"}\' >&2\n    exit 2\nfi\nSERVICE_URL="$(python3 -')
        if name == 'cognee-forget.sh':
            text = replace(text, '"${PLUGIN_DIR}" "${SELF_DIR}" 2>/dev/null || true', '"${PLUGIN_DIR}" "${SELF_DIR}" "${1:-}" "${2:-}" 2>/dev/null || true')
            text = replace(text, '    load_env_file()\n',
                           '    load_env_file()\n    from _memlane_access import check_forget_access\n    check_forget_access(sys.argv[3], sys.argv[4])\n')
        if name != 'cognee-remember.sh':
            if name == 'cognee-search.sh':
                text = replace(text, 'elif [ "$MODE" = "code" ] || [ -n "$FOREIGN" ]; then\n',
                               'elif [ -f "${SELF_DIR}/_memlane_policy.py" ]; then\n    echo \'{"error":"managed_recall_unconfirmed","status":0,"authoritative":false}\'\n    exit 1\nelif [ "$MODE" = "code" ] || [ -n "$FOREIGN" ]; then\n')
            return text
        text = replace(text, 'elif [ -n "$FILE_PATH" ]; then\n',
                       'elif [ -f "${SELF_DIR}/_memlane_policy.py" ]; then\n    echo \'{"error":"managed_server_unreachable","status":0}\'\n    exit 1\nelif [ -n "$FILE_PATH" ]; then\n')
    elif name == 'store-user-prompt.py':
        text = body(text, '_store', 'from _memlane_access import pending_capture_allowed\n'
                    'from _capture_policy import capture_enabled\n'
                    'if not pending_capture_allowed(enabled=capture_enabled(), new_turn=True):\n    return')
    elif name == 'sync-session-to-graph.py':
        text = replace(text, '            elif not wrote:\n                incomplete.append', '            elif not wrote and why != "managed_policy_paused":\n                incomplete.append')
        text = replace(text, '    # --strict: the caller',
                       '    from _memlane_policy import managed\n    if managed():\n        attempts = 1\n\n    # --strict: the caller')
    else:
        raise ValueError('file is outside managed patch allowlist')
    return text


FILES = ('config.py', 'session-start.py', '_coding_memory.py', '_plugin_common.py',
         '_remember_http.py', 'cognee-remember.sh', 'store-user-prompt.py', 'sync-session-to-graph.py',
         '_env_file.py', '_recall_http.py', 'cognee-search.sh', '_code_graph.py',
         'cognee-forget.sh', 'cognee-index-repo.sh')
