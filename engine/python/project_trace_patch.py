"""Codex host-result fields retained by a derived official plugin artifact.

No source reconstruction, hunk splitting, collector or engine/prompt change.
The same input diff, event ids and upstream privacy/byte caps remain in use.
"""


def codex_file_change_output(text):
    anchor = '        calls.append({**call, **common})\n    return calls\n'
    replacement = '''        # FileChange is a host result. Keep its exact returned patch in
        # output as well: the fixed SDK's compact Edit input omits diff.
        output = common["output"]
        if change_type == "update" and isinstance(change.get("unified_diff"), str) and change["unified_diff"]:
            output = {"status": common["output"], "unified_diff": change["unified_diff"]}
        calls.append({**call, **common, "output": output})
    return calls
'''
    if (text.count(anchor) != 1 or 'def _codex_file_changes(item: dict,' not in text
            or 'tool_input["diff"] = change.get("unified_diff")' not in text):
        raise ValueError('fixed Codex FileChange overlay anchor mismatch')
    return text.replace(anchor, replacement, 1)
