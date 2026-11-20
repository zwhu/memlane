"""Versioned overlay for the existing managed official buffer and sender."""
import ast


from _runtime_common import replace_exact


def replace(text, old, new):
    return replace_exact(text, old, new, label='capture plugin anchor')


def apply(text):
    tree = ast.parse(text)
    node = next(n for n in tree.body if isinstance(n, ast.FunctionDef) and n.name == 'remember_entry_via_http')
    first = node.body[0]
    index = first.end_lineno if isinstance(first, ast.Expr) and isinstance(first.value, ast.Constant) else first.lineno-1
    lines = text.splitlines(True)
    lines.insert(index, '''    from _memlane_policy import managed
    from _memlane_capture import numbered, send
    if managed() and numbered(entry):
        return send(__import__(__name__), dataset, session_id, entry, timeout=timeout, dataset_id=dataset_id)
''')
    text = ''.join(lines)
    tree = ast.parse(text)
    node = next(n for n in tree.body if isinstance(n, ast.FunctionDef) and n.name == 'drain_warmup_entries')
    first = node.body[0]
    index = first.end_lineno if isinstance(first, ast.Expr) and isinstance(first.value, ast.Constant) else first.lineno-1
    lines = text.splitlines(True)
    lines.insert(index, '''    from _memlane_policy import managed
    if managed():
        from _memlane_capture import drain
        return drain(__import__(__name__), dataset, session_id, budget_seconds=budget_seconds)
''')
    text = ''.join(lines)
    text = replace(text, '    entry[_BUFFERED_AT_KEY] = time.time()\n', '''    entry[_BUFFERED_AT_KEY] = time.time()
    from _memlane_policy import managed
    if managed() and not ambiguous:
        # Persist before the first request. Never invent IDs for unknown writes.
        entry["_capture_id"] = str(__import__("uuid").uuid4())
''')
    text = replace(text, '            if managed() and ambiguous and (not verified or _entry_fingerprint(send_entry) not in server_prints):\n', '''            from _memlane_capture import numbered
            if managed() and ambiguous and not numbered(entry):
''')
    text = replace(text, '            if ambiguous and verified and _entry_fingerprint(send_entry) in server_prints:\n',
        '            if not managed() and ambiguous and verified and _entry_fingerprint(send_entry) in server_prints:\n')
    # Numbered sends retain the existing buffer/backoff, and never fall back to
    # the non-idempotent endpoint when the new endpoint fails or is unavailable.
    compile(text, '_plugin_common.py', 'exec')
    return text
