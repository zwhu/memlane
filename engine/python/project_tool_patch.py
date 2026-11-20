"""Small tool-response overlay on a separately prepared PA host artifact.

The fixed Codex input and its manifest remain unchanged. No active cache is
edited. A confirmed local denial is different from an unavailable live policy.
"""


def remember_response(text):
    extracted_old = '''from _memlane_access import is_managed as managed, prepare_managed_remember
    _err, api_key = prepare_managed_remember(load_env_file, service_url, api_key, dataset_id or dataset, os.environ, error_fn=_error)'''
    extracted_new = '''from _memlane_access import is_managed as managed, prepare_managed_remember_strict
    _err, api_key = prepare_managed_remember_strict(load_env_file, service_url, api_key, dataset_id or dataset, os.environ, error_fn=_error)'''
    if text.count(extracted_old) == 1:
        return text.replace(extracted_old, extracted_new, 1)
    legacy_old = '''from _memlane_policy import managed, permit
    if managed() and not permit("write", dataset_id or dataset):
        return _error(403, "当前 Agent 没有此项目的写入权限")'''
    legacy_new = '''from _memlane_policy import managed, current, NotReady
    if managed():
        try:
            decision = current()
        except (NotReady, OSError, ValueError, KeyError, TypeError):
            return _error(0, "project_policy_unconfirmed")
        if (dataset_id or dataset) != decision["dataset_id"]:
            return _error(403, "managed_dataset_mismatch")
        if "write" not in decision["policy"]["permissions"]:
            return _error(403, "当前 Agent 没有此项目的写入权限")'''
    if text.count(legacy_old) != 1:
        raise ValueError('fixed managed remember tool overlay anchor mismatch')
    return text.replace(legacy_old, legacy_new, 1)


def common_recall_response(text):
    """Do not search an empty eligible catalog; preserve official reads and ACLs."""
    old = '''            if query.strip():
                groups = _json_http_request('/api/v1/search', {'query': query, 'search_type': 'CHUNKS','''
    new = '''            if candidates and query.strip():
                groups = _json_http_request('/api/v1/search', {'query': query, 'search_type': 'CHUNKS','''
    # New fixed artifacts already contain this guard. Their full manifest is
    # verified before this overlay; do not apply the same condition twice.
    equivalent = new.replace('if candidates and query.strip():', 'if query.strip() and candidates:')
    if text.count(new) == 1 or text.count(equivalent) == 1:
        return text
    if text.count(old) != 1:
        raise ValueError('fixed common recall overlay anchor mismatch')
    return text.replace(old, new, 1)


def forget_response(text):
    anchor = 'SERVICE_URL="$(python3 - <<\'PY\' "${runtime_json}" 2>/dev/null || true\n'
    guard = '''# Local policy results have no credentials and must not enter upstream
# runtime parsing or credential fallback. Read via stdin, never add keys to argv.
memlane_local_result="$(printf '%s' "${runtime_json}" | python3 -c 'import json,sys; print("yes" if json.load(sys.stdin).get("memlane_local_result") is True else "no")')"
if [ "$memlane_local_result" = "yes" ]; then
    printf '%s\\n' "${runtime_json}"
    exit 2
fi
'''
    if text.count(anchor) != 1:
        raise ValueError('fixed managed forget tool overlay anchor mismatch')
    extracted_old = '''    from _memlane_access import check_forget_access
    check_forget_access(sys.argv[3], sys.argv[4])
'''
    extracted_new = '''    from _memlane_access import check_forget_tool_policy
    _local_result = check_forget_tool_policy(sys.argv[3], sys.argv[4])
    if _local_result is not None:
        print(_local_result)
        raise SystemExit(0)
'''
    if text.count(extracted_old) == 1:
        return text.replace(extracted_old, extracted_new, 1).replace(anchor, guard + anchor, 1)
    legacy_old = '''    from _memlane_policy import require, recall_targets
    command, target = sys.argv[3], sys.argv[4]
    if command in ("data", "raw"):
        recall_targets(target, [target])
    elif command == "forget":
        require("delete", target)
'''
    legacy_new = '''    from _memlane_policy import current, NotReady
    command, target = sys.argv[3], sys.argv[4]
    if command in ("data", "raw", "forget"):
        try:
            decision = current()
            common = decision.get("common") or {}
            if target == decision["dataset_id"]:
                permissions = decision["policy"]["permissions"]
            elif target == common.get("dataset_id"):
                if not common.get("ready"):
                    raise NotReady("common_policy_unconfirmed")
                permissions = ["read"] if common.get("readable") else []
            else:
                permissions = []
        except (NotReady, OSError, ValueError, KeyError, TypeError):
            print(json.dumps({"error": "project_policy_unconfirmed", "status": 0, "memlane_local_result": True}))
            raise SystemExit(0)
        permission = "delete" if command == "forget" else "read"
        if permission not in permissions:
            print(json.dumps({"error": "project_" + permission + "_denied", "status": 403,
                              "local_denial": True, "memlane_local_result": True}))
            raise SystemExit(0)
'''
    if text.count(legacy_old) != 1:
        raise ValueError('fixed managed forget tool overlay anchor mismatch')
    return text.replace(legacy_old, legacy_new, 1).replace(anchor, guard + anchor, 1)
