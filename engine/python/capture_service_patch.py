"""Exact native insert overlay: preserve official cache and downstream behavior."""
import ast

MANAGER = 'cognee/infrastructure/session/session_manager.py'
ADAPTER = 'cognee/infrastructure/databases/cache/sql/SqlCacheAdapter.py'


def patch(name, text):
    if name == MANAGER:
        for kind, method in [('qa', 'create_qa_entry'), ('trace', 'append_agent_trace_step')]:
            old = f'{kind}_id = str(uuid.uuid4())'
            if text.count(old) != 1:
                raise ValueError('capture manager ID anchor changed')
            text = text.replace(old, f'{kind}_id = __import__("capture_receipts").entry_id()', 1)
            tree = ast.parse(text)
            calls = [n for n in ast.walk(tree) if isinstance(n, ast.Expr)
                     and isinstance(n.value, ast.Await) and isinstance(n.value.value, ast.Call)
                     and isinstance(n.value.value.func, ast.Attribute) and n.value.value.func.attr == method]
            if len(calls) != 1:
                raise ValueError('capture manager call anchor changed')
            node = calls[0]
            lines = text.splitlines(True)
            indent = ' ' * node.col_offset
            original = lines[node.lineno-1:node.end_lineno]
            lines[node.lineno-1:node.end_lineno] = [indent + 'from capture_receipts import CaptureDuplicate\n',
                indent + 'try:\n'] + ['    ' + line for line in original] + [indent + 'except CaptureDuplicate:\n',
                indent + f'    return {kind}_id\n']
            text = ''.join(lines)
    elif name == ADAPTER:
        tree = ast.parse(text)
        node = next(n for n in tree.body if isinstance(n, ast.ClassDef) and n.name == 'SqlCacheAdapter')
        first = node.body[0]
        index = first.end_lineno if isinstance(first, ast.Expr) and isinstance(first.value, ast.Constant) else first.lineno-1
        lines = text.splitlines(True)
        lines.insert(index, '    __memlane_capture_protocol__ = 1\n')
        text = ''.join(lines)
        for kind, table in [('qa', 'cache_qa_entries'), ('trace', 'cache_trace_entries')]:
            method = 'create_qa_entry' if kind == 'qa' else 'append_agent_trace_step'
            node = next(n for n in ast.walk(ast.parse(text)) if isinstance(n, ast.AsyncFunctionDef) and n.name == method)
            lines = text.splitlines(True)
            fragment = ''.join(lines[node.lineno-1:node.end_lineno])
            anchor = f'                await self._lock_session_writes(session, {table}, user_id, session_id)\n'
            if fragment.count(anchor) != 1:
                raise ValueError('capture cache transaction anchor changed')
            fragment = fragment.replace(anchor, anchor + f'                from capture_receipts import reserve\n                await reserve(session, user_id, session_id, {kind}_entry["{kind}_id"])\n', 1)
            lines[node.lineno-1:node.end_lineno] = [fragment]
            text = ''.join(lines)
        # The native adapter normally wraps all errors. Keep only our duplicate
        # and conflict signals intact; unrelated failures remain native errors.
        for message in ('Unexpected error while adding Q&A to SQL cache:',
                        'Unexpected error while appending agent trace step to SQL cache:'):
            old = '        except Exception as error:\n            error_msg = f"' + message
            if text.count(old) != 1:
                raise ValueError('capture cache error anchor changed')
            text = text.replace(old, '        except (__import__("capture_receipts").CaptureDuplicate, __import__("capture_receipts").CaptureConflict):\n            raise\n' + old, 1)
    compile(text, name, 'exec')
    return text
