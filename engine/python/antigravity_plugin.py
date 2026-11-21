"""Adapt the pinned official Antigravity plugin to the existing managed service."""
import ast
import hashlib
import json
from pathlib import Path
import tarfile

ENGINE = Path(__file__).resolve().parents[1]


def files_for(archive=None):
    from project_host_plugin import shared_patch
    manifest = json.loads((ENGINE / 'coding-memory-components.json').read_text())['plugin']
    archive = Path(archive or ENGINE.parent / '.cache/coding-memory-components' / (manifest['commit'] + '.tar.gz'))
    if archive.is_symlink() or hashlib.sha256(archive.read_bytes()).hexdigest() != manifest['archive_sha256']:
        raise ValueError('fixed official host archive mismatch')
    files, coding = {}, None
    with tarfile.open(archive) as bundle:
        for member in bundle:
            name = '/'.join(Path(member.name).parts[1:])
            if name == 'integrations/codex/plugins/cognee/scripts/_coding_memory.py' and member.isfile():
                coding = bundle.extractfile(member).read()
            prefix = 'integrations/antigravity/'
            if not name.startswith(prefix):
                continue
            relative = name[len(prefix):]
            if member.issym() or member.islnk() or '..' in Path(relative).parts:
                raise ValueError('unsupported upstream host archive member')
            if relative and member.isfile():
                files[relative] = bundle.extractfile(member).read()
    upstream = {name: hashlib.sha256(raw).hexdigest() for name, raw in files.items()}
    patch = shared_patch()
    # The same upstream commit's coding recall library is already reused by
    # other managed hosts. Keep Antigravity's native parser and capture scripts.
    files['scripts/_coding_memory.py'] = patch.apply('_coding_memory.py', coding.decode()).encode()
    common = files['scripts/_plugin_common.py'].decode()
    common += '''

def live_server_record(port):
    if not _live_server_pid(port):
        return {}
    try:
        record = json.loads(_server_pidfile(port).read_text(encoding="utf-8"))
    except Exception:
        return {}
    return record if isinstance(record, dict) else {}

def run_session_improve_detailed(dataset, session_id, *, trigger="final"):
    remaining = 0
    with improve_session_lock(session_id, "run_session_improve_detailed") as claimed:
        if not claimed:
            return {"ok": False, "reason": "busy", "error": ""}
        if not _backend_reachable(_local_api_url()):
            result = {"ok": False, "error": "backend unreachable"}
            reason = "unreachable"
        else:
            final_budget = _improve_float_env("COGNEE_DRAIN_BUDGET_FINAL", 120.0)
            _, remaining = drain_warmup_entries(dataset, session_id, budget_seconds=final_budget)
            if remaining:
                time.sleep(_DRAIN_RETRY_PAUSE_SECONDS)
                _, remaining = drain_warmup_entries(dataset, session_id, budget_seconds=final_budget)
            ensure_dataset_via_http(dataset)
            result = improve_session_via_http(dataset, session_id)
            if result.get("unsupported"):
                hook_log(
                    "improve_unsupported",
                    {"dataset": dataset, "session": session_id, "status": result.get("status")},
                )
            if result.get("ok"):
                reason = "incomplete_drain" if remaining else ""
            elif result.get("busy"):
                reason = "busy"
            else:
                reason = "failed"
        ok = bool(result.get("ok"))
        error = str(result.get("error") or "")
        if result.get("status") == 402:
            record_payment_required("improve")
        elif ok:
            clear_payment_required()
        hook_log(
            "improve_fired",
            {
                "dataset": dataset,
                "session": session_id,
                "trigger": trigger,
                "ok": ok,
                "busy": bool(result.get("busy")),
                "reason": reason,
                "error": error[:120],
            },
        )
        if ok:
            record_improve_success(session_id, dataset, trigger)
            refresh_credits("improve")
        if remaining:
            hook_log(
                "improve_incomplete_drain",
                {
                    "dataset": dataset,
                    "session": session_id,
                    "remaining": remaining,
                    "improve_ok": ok,
                },
            )
        return {"ok": ok and not remaining, "reason": reason, "error": error}
'''
    common = patch.apply('_plugin_common.py', common)
    common = patch.body(common, 'run_session_improve', '''from _memlane_policy import permit
if not permit("process", dataset, automatic=trigger not in ("manual", "switch")):
    return False''')
    # Persist a new number before any first send, using the official buffer.
    common = patch.body(common, 'remember_entry_via_http', '''from _memlane_policy import NotReady, managed
from _memlane_capture import numbered
if managed() and not numbered(entry):
    append_warmup_entry(dataset, session_id, entry)
    try:
        drained, remaining = drain_warmup_entries(dataset, session_id)
    except NotReady as exc:
        hook_log("drain_deferred_policy", {"dataset": dataset, "session": session_id, "error": str(exc)[:200]})
        return {"drained": 0, "buffered": 1}
    return {"drained": drained, "buffered": remaining}''')
    from project_capture_patch import apply as capture_patch
    files['scripts/_plugin_common.py'] = capture_patch(common).encode()
    for name in ('config.py', '_env_file.py', '_remember_http.py', '_recall_http.py', '_code_graph.py',
                 'cognee-forget.sh', 'cognee-index-repo.sh', 'store-user-prompt.py'):
        files['scripts/' + name] = patch.apply(name, files['scripts/' + name].decode()).encode()
    prompt_text = files['scripts/store-user-prompt.py'].decode()
    prompt_text = patch.replace(
        prompt_text,
        '        "transcript_path": payload.get("transcript_path"),\n',
        '        "transcript_path": payload.get("transcript_path"),\n'
        '        "date": __import__("datetime").datetime.now().astimezone().strftime("%Y-%m-%d"),\n',
    )
    files['scripts/store-user-prompt.py'] = prompt_text.encode()
    # Bootstrap is owned by dev.py; no official fallback may start a second
    # service, provision another principal, or install dependencies.
    text = files['scripts/session-start.py'].decode()
    text = patch.body(text, '_start', 'return {}')
    text = patch.body(text, '_run_bootstrap', 'return')
    text = patch.body(text, '_ensure_local_server_running', 'raise RuntimeError("managed_service_only")')
    files['scripts/session-start.py'] = text.encode()
    text = files['scripts/session-context-lookup.py'].decode()
    text = patch.body(text, '_run', '''from _coding_memory import build_block
context = build_block(prompt, cwd)
return {"hookSpecificOutput": {"hookEventName": "UserPromptSubmit", "additionalContext": context}}''')
    files['scripts/session-context-lookup.py'] = text.encode()
    text = files['scripts/store-to-session.py'].decode()
    for name in ('_store_tool_call', '_store_assistant_stop'):
        text = patch.body(text, name, '''from _memlane_policy import permit
if not permit("capture"):
    return''')
    text = patch.replace(text, '    pending = pop_pending_prompt(session_id, turn_id=str(payload.get("turn_id") or ""))\n',
                         '    pending = pop_pending_prompt(session_id, turn_id=str(payload.get("turn_id") or ""))\n    if not pending.get("prompt"):\n        return\n')
    text = patch.replace(
        text,
        '        status_code = exc.code if isinstance(exc, urllib.error.HTTPError) else None\n',
        '        from _memlane_policy import NotReady, managed\n'
        '        if managed() and isinstance(exc, NotReady):\n'
        '            hook_log(\n'
        '                "trace_store_error",\n'
        '                {"tool": tool_name, "error": str(exc)[:200], "status": None, "buffered": False},\n'
        '            )\n'
        '            return\n'
        '        status_code = exc.code if isinstance(exc, urllib.error.HTTPError) else None\n',
    )
    text = patch.replace(
        text,
        '        status = exc.code if isinstance(exc, urllib.error.HTTPError) else None\n',
        '        from _memlane_policy import NotReady, managed\n'
        '        if managed() and isinstance(exc, NotReady):\n'
        '            hook_log(\n'
        '                "stop_store_error",\n'
        '                {"error": str(exc)[:200], "status": None, "buffered": False},\n'
        '            )\n'
        '            return\n'
        '        status = exc.code if isinstance(exc, urllib.error.HTTPError) else None\n',
    )
    files['scripts/store-to-session.py'] = text.encode()
    text = files['scripts/sync-session-to-graph.py'].decode()
    text = patch.body(text, '_spawn_detached_sync', 'return False')
    text = patch.body(text, 'main', '''from _memlane_policy import permit
if not permit("process", automatic=True):
    return 0
if _EXECUTION_STOP_ARG in sys.argv:
    payload = json.loads(sys.stdin.read() or "{}")
    if payload.get("fullyIdle") is not True:
        return 0
    session_id, dataset, *_ = _load_resolved()
    run_session_improve(dataset, session_id, trigger="auto")
    return 0''')
    files['scripts/sync-session-to-graph.py'] = text.encode()
    text = files['scripts/agy_hook.py'].decode()
    text = patch.replace(text, '    args = list(sys.argv[1:] if argv is None else argv)\n',
                         '    args = list(sys.argv[1:] if argv is None else argv)\n'
                         '    if os.environ.get("MEMLANE_PROCESSING_SESSION") == "1":\n'
                         '        print("{}")\n'
                         '        return 0\n')
    text = patch.replace(text, '    stop = "--stop" in args[1:]\n    try:\n',
                         '    stop = "--stop" in args[1:]\n    payload = {}\n    try:\n')
    text = patch.replace(text, '        normalized = normalize_payload(payload, script, stop=stop)\n',
                         '        from _memlane_antigravity import prepare_payload\n        payload = prepare_payload(payload, script)\n        normalized = normalize_payload(payload, script, stop=stop)\n')
    text = patch.replace(text, '    except Exception:\n        translated = {}\n',
                         '    except Exception as error:\n        try:\n            from _memlane_antigravity import record_hook_error\n            record_hook_error(script, payload, error)\n        except Exception:\n            pass\n        translated = {}\n')
    files['scripts/agy_hook.py'] = text.encode()
    policy = (ENGINE / 'python/project_plugin_policy.py').read_text()
    policy = patch.replace(policy, "'codex': ('CODEX_HOME',), 'claude_code': ('CLAUDE_CONFIG_DIR',)}",
                           "'codex': ('CODEX_HOME',), 'claude_code': ('CLAUDE_CONFIG_DIR',),\n                         'antigravity': ('MEMLANE_ANTIGRAVITY_PROFILE',)}")
    policy = policy.replace("manifest = Path(__file__).resolve().parents[1] / '.codex-plugin/plugin.json'",
                            "manifest = Path(__file__).resolve().parents[1] / 'plugin.json'")
    files['scripts/_memlane_policy.py'] = policy.encode()
    files['scripts/_memlane_access.py'] = (ENGINE / 'python/project_access_hooks.py').read_bytes()
    files['scripts/_memlane_antigravity.py'] = (ENGINE / 'python/antigravity_hook_policy.py').read_bytes()
    files['scripts/_memlane_capture.py'] = (ENGINE / 'python/plugin_capture.py').read_bytes()
    # A direct native manifest keeps installation separate from other plugins.
    plugin = json.loads(files['plugin.json'])
    plugin['name'] = 'memlane-cognee'
    files['plugin.json'] = json.dumps(plugin, indent=2).encode()
    for name, raw in files.items():
        if name.endswith('.py'):
            ast.parse(raw.decode(), filename=name)
    return files, {'plugin_commit': manifest['commit'], 'host_kind': 'antigravity',
                   'archive_sha256': manifest['archive_sha256'], 'upstream_files': upstream,
                   'host_patch_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest()}
