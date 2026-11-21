#!/usr/bin/env python3
"""Dependency-free project lint. Reports source locations and never loads the SDK."""
import ast
import re
import sys
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
PARAMETERS = {
    'temperature', 'top_p', 'max_tokens', 'max_output_tokens',
    'llm_temperature', 'llm_max_completion_tokens', 'llm_args',
    'LLM_TEMPERATURE', 'LLM_MAX_COMPLETION_TOKENS', 'LLM_ARGS',
}
ENVIRON_MUTATION_ALLOWLIST = {
    'official_service_dev.py': 'service process bootstrap before importing upstream Cognee',
    'generation_handoff_runtime.py': 'live generation handoff OLLAMA_API_BASE sync',
    'generation_probe.py': 'isolated probe subprocess bootstrap',
    'first_install_store.py': 'isolated first-install store subprocess bootstrap',
    'agent_memory_adapter.py': 'single-invocation CLI adapter entrypoint',
    'antigravity_hook_policy.py': 'single-invocation Antigravity hook script for upstream _plugin_common',
    'project_plugin_policy.py': 'single-invocation plugin hook script for upstream _plugin_common',
}
ENVIRON_SCOPED_ALLOWLIST = {
    'project_backlog_worker.py': '_scoped_improve_cooldown',
}
ENVIRON_MUTATING_METHODS = {'update', 'pop', 'popitem', 'clear', 'setdefault'}
ML007_MESSAGE = '禁止在非白名单模块修改全局 os.environ；请显式传递环境变量字典或限定为白名单入口'
ML013_ASYNC_POLL_MESSAGE = '禁止在后台 while True 循环中使用低于或等于 5s 的 asyncio.sleep 高频轮询；协程让出请使用 asyncio.sleep(0)'
ML013_DEV_TRACK_MESSAGE = '禁止在高频主循环中无节流调用 track_descendants；须通过 monotonic 时间间隔门禁限频'
ML014_MESSAGE = '异步函数内禁止直接调用同步阻塞子进程；请改用 asyncio.to_thread 或 asyncio.create_subprocess_exec'
ML015_BINARY_HASH_MESSAGE = '禁止对可执行二进制直接调用 read_bytes() 全量读入内存哈希；请使用基于 stat 的缓存与流式分块摘要'
ML015_OWNER_CLEANUP_MESSAGE = 'opencode_backlog.execute 必须在 finally 块中调用 owner_path.unlink 清理已退出的只读 worker 临时文件'
ML016_MESSAGE = '启用 SQLite WAL 模式时必须在同一模块显式配置 PRAGMA journal_size_limit，防止 WAL 文件无限膨胀'
ML017_REOPEN_UI_MESSAGE = 'reopen_ui 禁止无条件调用 mygo dev 热重载轮询；默认启动须运行已构建二进制并由 watch 模式门禁守护'
ML017_CLEANUP_RECEIPT_MESSAGE = '写入 build/disk-cleanup 清理回执时必须同时调用回执轮转清理机制（unlink 淘汰超额旧回执）'

BLOCKING_SUBPROCESS_CALLS = {'run', 'check_output', 'call', 'check_call', 'Popen'}
BLOCKING_SAMPLER_NAMES = {'sample_process', 'default_sample_process'}
BINARY_EXPR_NAMES = {'node', 'executable', 'binary', 'bin_path', 'node_executable', 'node_path', 'node_bin'}
WAL_MODE_RE = re.compile(r'pragma\s+journal_mode\s*=\s*wal\b', re.IGNORECASE)
WAL_SIZE_LIMIT_RE = re.compile(r'pragma\s+journal_size_limit\b', re.IGNORECASE)


def _iter_assignment_targets(target):
    if isinstance(target, (ast.Tuple, ast.List)):
        for element in target.elts:
            yield from _iter_assignment_targets(element)
    elif isinstance(target, ast.Starred):
        yield from _iter_assignment_targets(target.value)
    else:
        yield target


def _check_environ_mutations(tree, path, report):
    path_obj = Path(path)
    filename = path_obj.name
    if path_obj.parent.name == 'scripts' or filename in ENVIRON_MUTATION_ALLOWLIST:
        return
    scoped_allowed = set()
    scoped_name = ENVIRON_SCOPED_ALLOWLIST.get(filename)
    if scoped_name:
        scoped_fn = next(
            (node for node in tree.body if isinstance(node, ast.FunctionDef) and node.name == scoped_name),
            None,
        )
        if scoped_fn is not None and any(isinstance(n, ast.Try) and bool(n.finalbody) for n in ast.walk(scoped_fn)):
            scoped_allowed = set(ast.walk(scoped_fn))
    os_names = {'os'}
    environ_names = set()
    putenv_names = set()
    for node in ast.walk(tree):
        if isinstance(node, ast.Import):
            for alias in node.names:
                if alias.name == 'os':
                    os_names.add(alias.asname or alias.name)
        elif isinstance(node, ast.ImportFrom) and node.module == 'os':
            for alias in node.names:
                local = alias.asname or alias.name
                if alias.name in ('environ', 'environb'):
                    environ_names.add(local)
                elif alias.name in ('putenv', 'unsetenv'):
                    putenv_names.add(local)

    def is_environ_ref(expr):
        if isinstance(expr, ast.Name) and expr.id in environ_names:
            return True
        return (
            isinstance(expr, ast.Attribute)
            and expr.attr in ('environ', 'environb')
            and isinstance(expr.value, ast.Name)
            and expr.value.id in os_names
        )

    # Follow direct local aliases (e.g. env = os.environ), never dict copies.
    for _ in range(len(tree.body) + 1):
        previous = set(environ_names)
        for node in ast.walk(tree):
            if isinstance(node, ast.Assign) and is_environ_ref(node.value):
                environ_names.update(t.id for t in node.targets if isinstance(t, ast.Name))
            elif isinstance(node, ast.AnnAssign) and node.value is not None and is_environ_ref(node.value):
                if isinstance(node.target, ast.Name):
                    environ_names.add(node.target.id)
        if environ_names == previous:
            break

    for node in ast.walk(tree):
        if node in scoped_allowed:
            continue
        if isinstance(node, ast.Assign):
            for target in node.targets:
                for leaf in _iter_assignment_targets(target):
                    if isinstance(leaf, ast.Subscript) and is_environ_ref(leaf.value):
                        report(node, 'ML007', ML007_MESSAGE)
        elif isinstance(node, ast.AnnAssign):
            if node.value is not None and isinstance(node.target, ast.Subscript) and is_environ_ref(node.target.value):
                report(node, 'ML007', ML007_MESSAGE)
        elif isinstance(node, ast.AugAssign):
            if (isinstance(node.target, ast.Subscript) and is_environ_ref(node.target.value)) or is_environ_ref(node.target):
                report(node, 'ML007', ML007_MESSAGE)
        elif isinstance(node, ast.Delete):
            for target in node.targets:
                for leaf in _iter_assignment_targets(target):
                    if isinstance(leaf, ast.Subscript) and is_environ_ref(leaf.value):
                        report(node, 'ML007', ML007_MESSAGE)
        elif isinstance(node, ast.Call):
            if (isinstance(node.func, ast.Attribute)
                    and node.func.attr in ENVIRON_MUTATING_METHODS
                    and is_environ_ref(node.func.value)):
                report(node, 'ML007', ML007_MESSAGE)
            elif ((isinstance(node.func, ast.Attribute)
                   and node.func.attr in ('putenv', 'unsetenv')
                   and isinstance(node.func.value, ast.Name)
                   and node.func.value.id in os_names)
                  or (isinstance(node.func, ast.Name) and node.func.id in putenv_names)):
                report(node, 'ML007', ML007_MESSAGE)


def literal(node):
    try:
        ast.literal_eval(node)
        return True
    except (ValueError, TypeError):
        return False


def string(node):
    return node.value if isinstance(node, ast.Constant) and isinstance(node.value, str) else None


def _numeric_literal(node):
    if isinstance(node, ast.Constant) and isinstance(node.value, (int, float)) and not isinstance(node.value, bool):
        return float(node.value)
    return None


def _is_while_true(node):
    return isinstance(node, ast.While) and isinstance(node.test, ast.Constant) and node.test.value is True


def _iter_scope_nodes(root, *, skip_to_thread_lambda=False, to_thread_check=None):
    """Yield AST nodes inside a function/loop body without descending into nested def/class definitions."""
    stack = list(getattr(root, 'body', []))
    while stack:
        current = stack.pop(0)
        if isinstance(current, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)):
            continue
        yield current
        if skip_to_thread_lambda and isinstance(current, ast.Call) and to_thread_check and to_thread_check(current):
            for arg in current.args[1:]:
                stack.append(arg)
            for kw in current.keywords:
                stack.append(kw.value)
            if current.args and not isinstance(current.args[0], ast.Lambda):
                stack.append(current.args[0])
            continue
        for child in ast.iter_child_nodes(current):
            if isinstance(child, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)):
                continue
            stack.append(child)


def _node_references_watch(node):
    for sub in ast.walk(node):
        if isinstance(sub, ast.Name) and 'watch' in sub.id.lower():
            return True
        if isinstance(sub, ast.Attribute) and 'watch' in sub.attr.lower():
            return True
    return False


def _node_references_monotonic(node):
    for sub in ast.walk(node):
        if isinstance(sub, ast.Name) and 'monotonic' in sub.id.lower():
            return True
        if isinstance(sub, ast.Attribute) and 'monotonic' in sub.attr.lower():
            return True
    return False


def _check_runtime_hygiene(tree, path, report):
    path_obj = Path(path)
    filename = path_obj.name
    is_scripts = path_obj.parent.name == 'scripts' or filename in {'dev.py', 'disk-space.py', 'task.py', 'bootstrap.py'}

    asyncio_names = {'asyncio'}
    asyncio_sleep_names = set()
    to_thread_names = set()
    time_names = {'time'}
    time_sleep_names = set()
    subprocess_names = {'subprocess'}
    subprocess_call_names = set()
    hashlib_names = {'hashlib'}
    sha256_names = set()

    for node in ast.walk(tree):
        if isinstance(node, ast.Import):
            for alias in node.names:
                local = alias.asname or alias.name
                if alias.name == 'asyncio':
                    asyncio_names.add(local)
                elif alias.name == 'time':
                    time_names.add(local)
                elif alias.name == 'subprocess':
                    subprocess_names.add(local)
                elif alias.name == 'hashlib':
                    hashlib_names.add(local)
        elif isinstance(node, ast.ImportFrom):
            if node.module == 'asyncio':
                for alias in node.names:
                    local = alias.asname or alias.name
                    if alias.name == 'sleep':
                        asyncio_sleep_names.add(local)
                    elif alias.name == 'to_thread':
                        to_thread_names.add(local)
            elif node.module == 'time':
                for alias in node.names:
                    if alias.name == 'sleep':
                        time_sleep_names.add(alias.asname or alias.name)
            elif node.module == 'subprocess':
                for alias in node.names:
                    if alias.name in BLOCKING_SUBPROCESS_CALLS:
                        subprocess_call_names.add(alias.asname or alias.name)
            elif node.module == 'hashlib':
                for alias in node.names:
                    if alias.name == 'sha256':
                        sha256_names.add(alias.asname or alias.name)

    def is_asyncio_sleep(call):
        if not isinstance(call, ast.Call):
            return False
        if isinstance(call.func, ast.Name) and call.func.id in asyncio_sleep_names:
            return True
        return (
            isinstance(call.func, ast.Attribute)
            and call.func.attr == 'sleep'
            and isinstance(call.func.value, ast.Name)
            and call.func.value.id in asyncio_names
        )

    def is_time_sleep(call):
        if not isinstance(call, ast.Call):
            return False
        if isinstance(call.func, ast.Name) and call.func.id in time_sleep_names:
            return True
        return (
            isinstance(call.func, ast.Attribute)
            and call.func.attr == 'sleep'
            and isinstance(call.func.value, ast.Name)
            and call.func.value.id in time_names
        )

    def is_to_thread_call(call):
        if not isinstance(call, ast.Call):
            return False
        if isinstance(call.func, ast.Name) and call.func.id in to_thread_names:
            return True
        if isinstance(call.func, ast.Attribute):
            if call.func.attr == 'to_thread' and isinstance(call.func.value, ast.Name) and call.func.value.id in asyncio_names:
                return True
            if call.func.attr == 'run_in_executor':
                return True
        return False

    def is_blocking_subprocess_func(func_expr):
        if isinstance(func_expr, ast.Name) and (func_expr.id in subprocess_call_names or func_expr.id in BLOCKING_SAMPLER_NAMES):
            return True
        if isinstance(func_expr, ast.Attribute):
            if (func_expr.attr in BLOCKING_SUBPROCESS_CALLS
                    and isinstance(func_expr.value, ast.Name)
                    and func_expr.value.id in subprocess_names):
                return True
            if func_expr.attr in BLOCKING_SAMPLER_NAMES:
                return True
        return False

    def is_sha256_call(call):
        if not isinstance(call, ast.Call):
            return False
        if isinstance(call.func, ast.Name) and call.func.id in sha256_names:
            return True
        return (
            isinstance(call.func, ast.Attribute)
            and call.func.attr == 'sha256'
            and isinstance(call.func.value, ast.Name)
            and call.func.value.id in hashlib_names
        )

    # ML013 (part 2): In scripts/dev.py (or while True loops with time.sleep(<1.0)),
    # reject unthrottled track_descendants(...) calls.
    for node in ast.walk(tree):
        if _is_while_true(node):
            has_short_time_sleep = False
            for sub in _iter_scope_nodes(node):
                if is_time_sleep(sub) and sub.args:
                    delay = _numeric_literal(sub.args[0])
                    if delay is not None and 0 <= delay < 1.0:
                        has_short_time_sleep = True
                        break
            if has_short_time_sleep:
                loop_has_monotonic = _node_references_monotonic(node)
                if_enclosed_nodes = set()
                for sub in _iter_scope_nodes(node):
                    if isinstance(sub, ast.If):
                        for child in _iter_scope_nodes(sub):
                            if_enclosed_nodes.add(child)
                for sub in _iter_scope_nodes(node):
                    if (isinstance(sub, ast.Call)
                            and isinstance(sub.func, ast.Name)
                            and sub.func.id == 'track_descendants'):
                        if not (loop_has_monotonic and sub in if_enclosed_nodes):
                            report(sub, 'ML013', ML013_DEV_TRACK_MESSAGE)

    # ML017 (part 1): In scripts/dev.py reopen_ui(...), reject unconditional ['tool', 'mygo', 'dev'].
    if filename == 'dev.py':
        for node in ast.walk(tree):
            if isinstance(node, ast.FunctionDef) and node.name == 'reopen_ui':
                has_watch_branch = any(
                    isinstance(sub, (ast.If, ast.IfExp)) and _node_references_watch(sub.test)
                    for sub in ast.walk(node)
                )
                for sub in ast.walk(node):
                    if isinstance(sub, (ast.List, ast.Tuple)):
                        items = [string(el) for el in sub.elts]
                        if 'mygo' in items and 'dev' in items and items.index('mygo') < items.index('dev'):
                            if not has_watch_branch:
                                report(sub, 'ML017', ML017_REOPEN_UI_MESSAGE)

    # ML017 (part 2): In scripts/disk-space.py, writing JSON receipts into build/disk-cleanup
    # must also invoke a receipt pruning/rotation mechanism (.unlink dedicated to cleanup receipts).
    if filename == 'disk-space.py':
        functions = {node.name: node for node in ast.walk(tree) if isinstance(node, ast.FunctionDef)}
        pruning_helpers = set()
        for fn_name, fn_node in functions.items():
            if fn_name == 'prune_history':
                continue
            if any(isinstance(sub, ast.Call) and isinstance(sub.func, ast.Attribute) and sub.func.attr == 'unlink'
                   for sub in ast.walk(fn_node)):
                pruning_helpers.add(fn_name)
        for fn_name, fn_node in functions.items():
            if fn_name == 'prune_history':
                continue
            write_calls = [
                sub for sub in ast.walk(fn_node)
                if isinstance(sub, ast.Call) and isinstance(sub.func, ast.Attribute) and sub.func.attr == 'write_text'
            ]
            if not write_calls:
                continue
            invokes_pruning = (
                fn_name in pruning_helpers
                or any(
                    isinstance(sub, ast.Call)
                    and isinstance(sub.func, ast.Name)
                    and sub.func.id in pruning_helpers
                    for sub in ast.walk(fn_node)
                )
            )
            if not invokes_pruning:
                for write_call in write_calls:
                    report(write_call, 'ML017', ML017_CLEANUP_RECEIPT_MESSAGE)

    if is_scripts:
        return

    # Engine Python rules: ML013 (part 1), ML014, ML015, ML016.
    for node in ast.walk(tree):
        if isinstance(node, ast.AsyncFunctionDef):
            # ML013 (part 1): Reject asyncio.sleep(N) with 0 < N <= 5 inside background while True loops.
            for sub in _iter_scope_nodes(node):
                if _is_while_true(sub):
                    for wsub in _iter_scope_nodes(sub):
                        if is_asyncio_sleep(wsub) and wsub.args:
                            delay = _numeric_literal(wsub.args[0])
                            if delay is not None and 0 < delay <= 5:
                                report(wsub, 'ML013', ML013_ASYNC_POLL_MESSAGE)
            # ML014: Reject direct blocking subprocess calls inside async def.
            awaited_calls = {
                sub.value for sub in _iter_scope_nodes(node, skip_to_thread_lambda=True, to_thread_check=is_to_thread_call)
                if isinstance(sub, ast.Await) and isinstance(sub.value, ast.Call)
            }
            for sub in _iter_scope_nodes(node, skip_to_thread_lambda=True, to_thread_check=is_to_thread_call):
                if isinstance(sub, ast.Call) and is_blocking_subprocess_func(sub.func):
                    is_sampler = (
                        (isinstance(sub.func, ast.Name) and sub.func.id in BLOCKING_SAMPLER_NAMES)
                        or (isinstance(sub.func, ast.Attribute) and sub.func.attr in BLOCKING_SAMPLER_NAMES)
                    )
                    if is_sampler and sub in awaited_calls:
                        continue
                    report(sub, 'ML014', ML014_MESSAGE)

    # ML015 (part 1): Reject hashlib.sha256(<binary_expr>.read_bytes()).
    for node in ast.walk(tree):
        if is_sha256_call(node) and node.args:
            for sub in ast.walk(node.args[0]):
                if isinstance(sub, ast.Call) and isinstance(sub.func, ast.Attribute) and sub.func.attr == 'read_bytes':
                    target_expr = sub.func.value
                    has_binary_ref = any(
                        (isinstance(t, ast.Name) and t.id in BINARY_EXPR_NAMES)
                        or (isinstance(t, ast.Attribute) and t.attr in BINARY_EXPR_NAMES)
                        or (isinstance(t, ast.Constant) and isinstance(t.value, str) and t.value in BINARY_EXPR_NAMES)
                        for t in ast.walk(target_expr)
                    )
                    if has_binary_ref:
                        report(sub, 'ML015', ML015_BINARY_HASH_MESSAGE)

    # ML015 (part 2): In opencode_backlog.py, execute() must unlink owner_path in a finally block.
    if filename == 'opencode_backlog.py':
        for node in ast.walk(tree):
            if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)) and node.name == 'execute':
                try_with_finally = [sub for sub in ast.walk(node) if isinstance(sub, ast.Try) and sub.finalbody]
                has_owner_unlink = False
                for try_node in try_with_finally:
                    for final_stmt in try_node.finalbody:
                        for sub in ast.walk(final_stmt):
                            if (isinstance(sub, ast.Call)
                                    and isinstance(sub.func, ast.Attribute)
                                    and sub.func.attr == 'unlink'
                                    and any(isinstance(t, ast.Name) and t.id == 'owner_path' for t in ast.walk(sub.func.value))):
                                has_owner_unlink = True
                                break
                if not has_owner_unlink:
                    anchor = try_with_finally[0].finalbody[0] if try_with_finally else node
                    report(anchor, 'ML015', ML015_OWNER_CLEANUP_MESSAGE)

    # ML016: Files with PRAGMA journal_mode=WAL must also configure PRAGMA journal_size_limit.
    string_nodes = [node for node in ast.walk(tree) if isinstance(node, ast.Constant) and isinstance(node.value, str)]
    wal_nodes = [node for node in string_nodes if WAL_MODE_RE.search(node.value)]
    if wal_nodes and not any(WAL_SIZE_LIMIT_RE.search(node.value) for node in string_nodes):
        for wal_node in wal_nodes:
            report(wal_node, 'ML016', ML016_MESSAGE)


def check_source(source, path):
    """Check model transport modules, including the shared configuration boundary."""
    try:
        tree = ast.parse(source, filename=str(path))
    except SyntaxError as error:
        return [(error.lineno, 'ML000', error.msg)]
    issues = []
    constructors = {'LLMConfig'}
    for node in ast.walk(tree):
        if isinstance(node, ast.ImportFrom):
            constructors.update(alias.asname or alias.name for alias in node.names if alias.name == 'LLMConfig')
    # Follow simple local aliases as well as import aliases.
    for _ in range(len(tree.body) + 1):
        previous = set(constructors)
        for node in ast.walk(tree):
            if isinstance(node, ast.Assign) and (
                    (isinstance(node.value, ast.Name) and node.value.id in constructors) or
                    (isinstance(node.value, ast.Attribute) and node.value.attr == 'LLMConfig')):
                constructors.update(target.id for target in node.targets if isinstance(target, ast.Name))
        if constructors == previous:
            break
    allowed_factory = None
    if Path(path).name == 'generation_config.py':
        allowed_factory = next((node for node in tree.body if isinstance(node, ast.FunctionDef)
                                and node.name == 'native_llm_configuration'), None)
    factory_calls = set(ast.walk(allowed_factory)) if allowed_factory else set()

    def report(node, code, message):
        issues.append((node.lineno, code, message))

    def injected(node, key, value):
        # The environment-to-SDK field-name table is metadata, not a default.
        if string(key) in PARAMETERS and literal(value) and string(value) not in PARAMETERS:
            report(node, 'ML002', '模型参数不得注入字面量默认值；从明确配置读取，未配置时省略')
        if string(key) in PARAMETERS and isinstance(value, ast.BoolOp) and isinstance(value.op, ast.Or):
            report(node, 'ML003', '模型参数不得用 or 补回退值；显式0必须保留')

    for node in ast.walk(tree):
        if isinstance(node, ast.Call):
            constructor = ((isinstance(node.func, ast.Name) and node.func.id in constructors) or
                           (isinstance(node.func, ast.Attribute) and node.func.attr == 'LLMConfig'))
            if constructor:
                valid = (node in factory_calls and not node.args and len(node.keywords) == 1 and
                         node.keywords[0].arg is None and isinstance(node.keywords[0].value, ast.Call) and
                         isinstance(node.keywords[0].value.func, ast.Name) and
                         node.keywords[0].value.func.id == 'native_llm_kwargs')
                if not valid:
                    report(node, 'ML001', '使用共享 native_llm_configuration，禁止独立构造 LLMConfig')
            if isinstance(node.func, ast.Attribute) and node.func.attr in ('get', 'setdefault'):
                if len(node.args) >= 2 and string(node.args[0]) in PARAMETERS:
                    report(node, 'ML003', '模型参数不得使用 get/setdefault 回退值；保留未配置状态')
            for keyword in node.keywords:
                if keyword.arg in PARAMETERS and literal(keyword.value):
                    report(node, 'ML002', '模型参数不得在调用中硬编码；使用明确配置')
        elif isinstance(node, ast.Dict):
            for key, value in zip(node.keys, node.values):
                injected(node, key, value)
        elif isinstance(node, (ast.Assign, ast.AnnAssign)):
            targets = node.targets if isinstance(node, ast.Assign) else [node.target]
            for target in targets:
                if node.value is not None and isinstance(target, ast.Subscript):
                    injected(node, target.slice, node.value)
                elif node.value is not None and isinstance(target, ast.Attribute):
                    injected(node, ast.Constant(value=target.attr), node.value)
    _check_environ_mutations(tree, path, report)
    _check_runtime_hygiene(tree, path, report)
    return sorted(set(issues))


ANIMATION_FRAME_ALLOWLIST = {'motion_tokens.go'}
PERF_FORBIDDEN_DRAW_CALLS = re.compile(
    r'\b(?:http\.(?:Get|Post|Do|NewRequest)|os\.(?:ReadFile|WriteFile|Create|Open)|time\.Sleep|exec\.Command)\b'
)
SHORT_AFTER_RE = re.compile(r'\.After\(\s*(\d+)\s*\*\s*time\.(Millisecond|Microsecond|Nanosecond)\b')
DRAW_CALLBACK_RE = re.compile(r'\.(?:Draw|DrawOver)\(\s*func\s*\([^)]*\)\s*\{')


def _extract_brace_block(source, open_brace_index):
    depth = 0
    in_str = None
    escaped = False
    for idx in range(open_brace_index, len(source)):
        ch = source[idx]
        if in_str is not None:
            if in_str == '"':
                if escaped:
                    escaped = False
                elif ch == '\\':
                    escaped = True
                elif ch == '"':
                    in_str = None
            elif ch == '`':
                in_str = None
            continue
        if ch in ('"', '`'):
            in_str = ch
        elif ch == '{':
            depth += 1
        elif ch == '}':
            depth -= 1
            if depth == 0:
                return source[open_brace_index:idx + 1]
    return source[open_brace_index:]


def _strip_go_comments(source):
    """Replace Go // and /* */ comments with spaces while preserving line numbers."""
    out = []
    i = 0
    n = len(source)
    in_str = None
    escaped = False
    while i < n:
        ch = source[i]
        if in_str is not None:
            out.append(ch)
            if in_str == '"':
                if escaped:
                    escaped = False
                elif ch == '\\':
                    escaped = True
                elif ch == '"':
                    in_str = None
            elif ch == '`':
                in_str = None
            i += 1
            continue
        if ch in ('"', '`'):
            in_str = ch
            out.append(ch)
            i += 1
        elif ch == '/' and i + 1 < n and source[i + 1] == '/':
            while i < n and source[i] != '\n':
                out.append(' ')
                i += 1
        elif ch == '/' and i + 1 < n and source[i + 1] == '*':
            out.append('  ')
            i += 2
            while i < n:
                if source[i] == '*' and i + 1 < n and source[i + 1] == '/':
                    out.append('  ')
                    i += 2
                    break
                out.append('\n' if source[i] == '\n' else ' ')
                i += 1
        else:
            out.append(ch)
            i += 1
    return ''.join(out)


def check_go_performance(source, path):
    """Static performance guardrails for Native UI frame rate, idle CPU, and cold start."""
    filename = Path(path).name
    clean = _strip_go_comments(source)
    issues = []

    # ML008: AnimationFrame() is restricted to motion_tokens.go and must honor ReduceMotion.
    for match in re.finditer(r'\.AnimationFrame\(\)', clean):
        line = clean[:match.start()].count('\n') + 1
        if filename not in ANIMATION_FRAME_ALLOWLIST:
            issues.append((line, 'ML008', '禁止在非动效模块直接调用 AnimationFrame()；集中到 motion_tokens.go 并受 ReduceMotion 守护'))
        else:
            func_start = clean.rfind('\nfunc ', 0, match.start())
            func_body = clean[func_start if func_start >= 0 else 0:match.end()]
            if 'ReduceMotion' not in func_body:
                issues.append((line, 'ML008', 'AnimationFrame() 所在动效函数必须检查 ReduceMotion，禁止空闲无限刷帧'))

    # ML009: Reject high-frequency c.After (<500ms) polling.
    for match in SHORT_AFTER_RE.finditer(clean):
        amount = int(match.group(1))
        unit = match.group(2)
        ms = amount if unit == 'Millisecond' else (amount / 1000.0 if unit == 'Microsecond' else amount / 1e6)
        if ms < 500:
            line = clean[:match.start()].count('\n') + 1
            issues.append((line, 'ML009', '禁止使用低于 500ms 的 c.After 高频轮询重绘，避免主线程空转耗电'))

    # ML010: Paint callbacks (Draw / DrawOver) must never block on I/O, sleep, or network.
    for match in DRAW_CALLBACK_RE.finditer(clean):
        brace_idx = match.end() - 1
        block = _extract_brace_block(clean, brace_idx)
        for bad in PERF_FORBIDDEN_DRAW_CALLS.finditer(block):
            line = clean[:brace_idx + bad.start()].count('\n') + 1
            issues.append((line, 'ML010', 'Draw/DrawOver 渲染回调禁止执行磁盘 I/O、休眠或网络请求，确保高帧率绘制'))

    # ML011: Reject blocking I/O, sleep, or network calls on App.View hot path.
    if filename == 'app.go':
        view_start = clean.find('func (a *App) View(')
        if view_start >= 0:
            view_end = clean.find('\nfunc ', view_start + 1)
            view_body = clean[view_start:view_end if view_end >= 0 else len(clean)]
            for bad in PERF_FORBIDDEN_DRAW_CALLS.finditer(view_body):
                line = clean[:view_start + bad.start()].count('\n') + 1
                issues.append((line, 'ML011', 'App.View 渲染热路径禁止同步磁盘 I/O、休眠或网络调用'))

    # ML012: Cold-start Overview performance & single-source guardrails.
    for match in re.finditer(r'\b(overview-ui-cache|hydrateOverviewFromCache|saveOverviewCache)\b', clean):
        line = clean[:match.start()].count('\n') + 1
        issues.append((line, 'ML012', '禁止在桌面端落盘 SWR 首页缓存副本；首页冷启动须走控制面单次只读聚合接口 overviewBootstrap'))
    if filename == 'overview_ui.go':
        load_start = clean.find('func (a *App) loadOverview(')
        if load_start >= 0:
            load_end = clean.find('\nfunc ', load_start + 1)
            load_body = clean[load_start:load_end if load_end >= 0 else len(clean)]
            if 'overviewBootstrap(' not in load_body:
                line = clean[:load_start].count('\n') + 1
                issues.append((line, 'ML012', 'loadOverview 必须优先走控制面单次只读聚合接口 overviewBootstrap，禁止冷启动直接扇出多库 HTTP'))
    if filename == 'app.go':
        start_idx = clean.find('func (a *App) Start(')
        if start_idx >= 0:
            start_end = clean.find('\nfunc ', start_idx + 1)
            start_body = clean[start_idx:start_end if start_end >= 0 else len(clean)]
            tail_idx = start_body.find('a.RefreshProjectDirectory()')
            if tail_idx >= 0:
                tail = start_body[tail_idx:]
                if 'a.RefreshIntegrations()' in tail and 'AfterFunc' not in tail:
                    line = clean[:start_idx + tail_idx].count('\n') + 1
                    issues.append((line, 'ML012', 'App.Start() 首页冷启动必须延迟 RefreshIntegrations()，避免子进程探测与首屏加载争抢帧预算'))

    return issues


def check_go_source(source, path):
    """Performance guardrails for Native UI; presentation behavior is covered by Go UI tests."""
    return sorted(set(check_go_performance(source, path)))


def main():
    failures = 0
    paths = sorted((ROOT / 'engine/python').rglob('*.py'))
    for path in paths:
        if '__pycache__' in path.parts or path.name.startswith('test_'):
            continue
        # The shared boundary applies across engine consumers, not just handoff.
        for line, code, message in check_source(path.read_text(encoding='utf-8'), path):
            print('{}:{}: {} {}'.format(path.relative_to(ROOT), line, code, message))
            failures += 1
    scripts_dir = ROOT / 'scripts'
    if scripts_dir.is_dir():
        for path in sorted(scripts_dir.glob('*.py')):
            if path.name.startswith('test_') or path.name == 'lint.py':
                continue
            for line, code, message in check_source(path.read_text(encoding='utf-8'), path):
                print('{}:{}: {} {}'.format(path.relative_to(ROOT), line, code, message))
                failures += 1
    go_dir = ROOT / 'internal/simpledesktop'
    if go_dir.is_dir():
        for path in sorted(go_dir.glob('*.go')):
            if path.name.endswith('_test.go'):
                continue
            for line, code, message in check_go_source(path.read_text(encoding='utf-8'), path):
                print('{}:{}: {} {}'.format(path.relative_to(ROOT), line, code, message))
                failures += 1
    if not failures:
        print('Lint passed: 模型参数、环境隔离、UI 性能与运行时资源门禁 (ML001-ML003, ML007-ML012, ML013-ML017)')
    return bool(failures)


if __name__ == '__main__':
    sys.exit(main())
