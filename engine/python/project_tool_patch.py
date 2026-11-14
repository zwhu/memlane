"""Small tool-response overlay on a separately prepared PA host artifact.

The fixed Codex input and its manifest remain unchanged. No active cache is
edited. A confirmed local denial is different from an unavailable live policy.
"""


def remember_response(text):
    old = '''from _memlane_policy import managed, permit
    if managed() and not permit("write", dataset_id or dataset):
        return _error(403, "当前 Agent 没有此项目的写入权限")'''
    new = '''from _memlane_policy import managed, current, NotReady
    if managed():
        try:
            decision = current()
        except (NotReady, OSError, ValueError, KeyError, TypeError):
            return _error(0, "project_policy_unconfirmed")
        if (dataset_id or dataset) != decision["dataset_id"]:
            return _error(403, "managed_dataset_mismatch")
        if "write" not in decision["policy"]["permissions"]:
            return _error(403, "当前 Agent 没有此项目的写入权限")'''
    if text.count(old) != 1:
        raise ValueError('fixed managed remember tool overlay anchor mismatch')
    return text.replace(old, new, 1)


def forget_response(text):
    old = '''    from _memlane_policy import require, recall_targets
    command, target = sys.argv[3], sys.argv[4]
    if command in ("data", "raw"):
        recall_targets(target, [target])
    elif command == "forget":
        require("delete", target)
'''
    new = '''    from _memlane_policy import current, NotReady
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
    anchor = 'SERVICE_URL="$(python3 - <<\'PY\' "${runtime_json}" 2>/dev/null || true\n'
    guard = '''# Local policy results have no credentials and must not enter upstream
# runtime parsing or credential fallback. Read via stdin, never add keys to argv.
memlane_local_result="$(printf '%s' "${runtime_json}" | python3 -c 'import json,sys; print("yes" if json.load(sys.stdin).get("memlane_local_result") is True else "no")')"
if [ "$memlane_local_result" = "yes" ]; then
    printf '%s\\n' "${runtime_json}"
    exit 2
fi
'''
    if text.count(old) != 1 or text.count(anchor) != 1:
        raise ValueError('fixed managed forget tool overlay anchor mismatch')
    return text.replace(old, new, 1).replace(anchor, guard + anchor, 1)
