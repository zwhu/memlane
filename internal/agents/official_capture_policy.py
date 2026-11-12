"""Bounded protocol wrapper around the unchanged, hash-pinned upstream policy.

No host config/history reads, networking, cache writes or model calls. Source
bytes arrive from the trusted component owner, never a model tool parameter.
"""
import hashlib
import json
import os
import sys
import types

PIN = "aa49445f3d78880d5784c0b47195994aabc88dc32d0a2824a8232d77ad4719b0"


def apply(source, entries):
    if not isinstance(source, str) or hashlib.sha256(source.encode()).hexdigest() != PIN:
        raise ValueError("policy_identity")
    if not isinstance(entries, list) or len(entries) > 64:
        raise ValueError("bounded_entries")
    policy = types.ModuleType("memlane_pinned_cognee_capture_policy")
    exec(compile(source, "<cognee-1.7.7-_capture_policy.py>", "exec"), policy.__dict__)
    # Validate patterns even for QA-only batches; invalid config must not
    # silently permit one channel while dropping the other.
    if policy.capture_pattern_error():
        raise ValueError("policy_configuration")
    output = []
    for entry in entries:
        if not isinstance(entry, dict):
            raise ValueError("typed_entry")
        kind = entry.get("type")
        if kind == "qa":
            if set(entry) != {"type", "question", "answer", "context"} or not all(
                isinstance(entry[key], str) for key in ("question", "answer", "context")
            ):
                raise ValueError("typed_qa")
        elif kind == "trace":
            if set(entry) != {"type", "origin_function", "status", "method_params", "method_return_value", "error_message", "generate_feedback_with_llm", "memory_context"}:
                raise ValueError("typed_trace")
            if not all(isinstance(entry[key], str) for key in ("origin_function", "status", "error_message", "memory_context")):
                raise ValueError("typed_trace")
            if entry["status"] not in ("success", "error") or entry["generate_feedback_with_llm"] is not False:
                raise ValueError("actual_tool_status")
            if entry["method_params"] is not None and not isinstance(entry["method_params"], dict):
                raise ValueError("typed_parameters")
        else:
            raise ValueError("unsupported_entry")
        if not policy.capture_enabled():
            output.append({"state": "policy_filtered", "reason": "capture_disabled"})
            continue
        if kind == "trace":
            # pi's lower-case builtins map to upstream policy names only for
            # matching. Preserve the actual host name in the resulting trace.
            name = entry["origin_function"]
            policy_name = {"bash": "Bash", "read": "Read", "write": "Write", "edit": "Edit"}.get(name, name)
            params = entry["method_params"]
            if not policy.allow_tool(policy_name, params):
                output.append({"state": "policy_filtered", "reason": "tool_policy"})
                continue
            # Upstream order: allow on unredacted/untruncated input, redact,
            # then recursive-plugin and conservative low-signal checks.
            redacted = policy.redact(entry)
            clean_params = redacted["method_params"]
            command = str(clean_params.get("command", "")) if isinstance(clean_params, dict) else ""
            if policy_name == "Bash" and policy.is_plugin_invocation(command):
                output.append({"state": "policy_filtered", "reason": "self_capture"})
                continue
            # No fabricated exit code or outcome object for pi text results.
            if policy_name == "Bash" and policy.low_signal_bash(command, redacted["method_return_value"], entry["status"]):
                output.append({"state": "policy_filtered", "reason": "low_signal"})
                continue
        else:
            redacted = policy.redact(entry)
        output.append({"state": "accepted", "entry": redacted})
    return {"ok": True, "policy_digest": PIN, "results": output}


def main():
    try:
        raw = sys.stdin.buffer.read(2 * 1024 * 1024 + 1)
        if len(raw) > 2 * 1024 * 1024:
            raise ValueError("input_budget")
        request = json.loads(raw)
        if not isinstance(request, dict) or set(request) != {"source", "entries"}:
            raise ValueError("protocol")
        result = apply(request["source"], request["entries"])
    except Exception:
        # Never echo payloads, user regexes, source paths or exception text.
        result = {"ok": False, "code": "capture_policy_invalid"}
    sys.stdout.write(json.dumps(result, ensure_ascii=False, allow_nan=False))


if __name__ == "__main__":
    main()
