#!/usr/bin/env python3
"""Dependency-free project lint. Reports source locations and never loads the SDK."""
import ast
from collections import deque
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
ENVIRON_MUTATING_METHODS = {'update', 'pop', 'popitem', 'clear', 'setdefault', '__setitem__', '__delitem__'}

ML001_MESSAGE = '使用共享 native_llm_configuration，禁止独立构造 LLMConfig'
ML002_LITERAL_MESSAGE = '模型参数不得注入字面量默认值；从明确配置读取，未配置时省略'
ML002_CALL_MESSAGE = '模型参数不得在调用中硬编码；使用明确配置'
ML003_OR_MESSAGE = '模型参数不得用 or 补回退值；显式0必须保留'
ML003_FALLBACK_MESSAGE = '模型参数不得使用 get/setdefault 回退值；保留未配置状态'
ML006_MESSAGE = '画像生成不得使用固定总时长超时中断；请观察进程活动并提供明确停止'
ML007_MESSAGE = '禁止在非白名单模块修改全局 os.environ；请显式传递环境变量字典或限定为白名单入口'
ML008_ALLOW_MESSAGE = '禁止在非动效模块直接调用 AnimationFrame()；集中到 motion_tokens.go 并受 ReduceMotion 守护'
ML008_GUARD_MESSAGE = 'AnimationFrame() 所在动效函数必须检查 ReduceMotion，禁止空闲无限刷帧'
ML009_MESSAGE = '禁止使用低于 500ms 的 c.After 高频轮询重绘，避免主线程空转耗电'
ML010_MESSAGE = 'Draw/DrawOver 渲染回调禁止执行磁盘 I/O、休眠或网络请求，确保高帧率绘制'
ML011_MESSAGE = 'App.View 渲染热路径禁止同步磁盘 I/O、休眠或网络调用'
ML012_CACHE_MESSAGE = '禁止在桌面端落盘 SWR 首页缓存副本；首页冷启动须走控制面单次只读聚合接口 overviewBootstrap'
ML012_OVERVIEW_MESSAGE = 'loadOverview 必须优先走控制面单次只读聚合接口 overviewBootstrap，禁止冷启动直接扇出多库 HTTP'
ML012_START_MESSAGE = 'App.Start() 首页冷启动必须延迟 RefreshIntegrations()，避免子进程探测与首屏加载争抢帧预算'
ML013_ASYNC_POLL_MESSAGE = '禁止在后台 while True 循环中使用低于或等于 5s 的 asyncio.sleep 高频轮询；协程让出请使用 asyncio.sleep(0)'
ML013_DEV_TRACK_MESSAGE = '禁止在高频主循环中无节流调用 track_descendants；须通过 monotonic 时间间隔门禁限频'
ML014_MESSAGE = '异步函数内禁止直接调用同步阻塞子进程；请改用 asyncio.to_thread 或 asyncio.create_subprocess_exec'
ML015_BINARY_HASH_MESSAGE = '禁止对可执行二进制直接调用 read_bytes() 全量读入内存哈希；请使用基于 stat 的缓存与流式分块摘要'
ML015_OWNER_CLEANUP_MESSAGE = 'opencode_backlog.execute 必须在 finally 块中调用 owner_path.unlink 清理已退出的只读 worker 临时文件'
ML016_MESSAGE = '启用 SQLite WAL 模式时必须在同一模块显式配置 PRAGMA journal_size_limit，防止 WAL 文件无限膨胀'
ML017_REOPEN_UI_MESSAGE = 'reopen_ui 禁止无条件调用 mygo dev 热重载轮询；默认启动须运行已构建二进制并由 watch 模式门禁守护'
ML017_CLEANUP_RECEIPT_MESSAGE = '写入 build/disk-cleanup 清理回执时必须同时调用回执轮转清理机制（unlink 淘汰超额旧回执）'
ML018_DOC_MESSAGE = '正式文档禁止引用本地忽略的 docs/process/ 路径；请将长期结论自包含写入正文，必要静态图移入 docs/images/'

DOCS_PROCESS_REF_RE = re.compile(
    r'(?<![A-Za-z0-9_-])docs/process(?:/|(?![A-Za-z0-9_-]))'
    r'|(?<![A-Za-z0-9_-])(?:\.\./)+process(?:/|(?![A-Za-z0-9_-]))'
    r'|\]\(\s*\.?/?process(?:/|\s*\))'
)
ML018_MESSAGE = '禁止在 Go 桌面端新增对 overviewDocuments/overviewSessions//v1/overview/bootstrap 的全量拉表调用；请走 /v2 游标或点查接口'
ML019_MESSAGE = '/v2 列表接口必须实施 _parse_limit (<=50) 与 JSON 字节预算裁剪 (fit_items_to_json_budget)'
ML020_CONNECT_MESSAGE = '禁止在非白名单模块直接调用 sqlite3.connect；只读访问官方库必须经 sqlite_ro.open_official_sqlite_ro'
ML020_IMMUTABLE_MESSAGE = '禁止在线使用 SQLite URI immutable=1（易引发 WAL 脏页/过期视图）；只读连接须走 ?mode=ro + query_only + set_authorizer'
ML021_MESSAGE = 'catalog_index.py 严禁在表结构中存储正文/why_learned/metadata_json 或建立 catalog_sessions 表'

SQLITE_CONNECT_ALLOWLIST = {
    'sqlite_ro.py',
    'catalog_index.py',
    'reading_state_store.py',
    'provenance_store.py',
    'knowledge_recall.py',
    'external_capture_store.py',
    'session_drift.py',
    'registry_sessions_store.py',
    'local_prepare.py',
    'bench_scaling.py',
}
IMMUTABLE_URI_RE = re.compile(r'\bimmutable\s*=\s*1\b', re.IGNORECASE)
CREATE_TABLE_RE = re.compile(
    r'CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*\((.*?)\)',
    re.IGNORECASE | re.DOTALL,
)
CATALOG_FORBIDDEN_TABLES = {'catalog_sessions'}
CATALOG_FORBIDDEN_COLUMNS = {'body', 'raw_text', 'why_learned', 'metadata_json'}
V2_NON_LIST_ROUTE_MARKERS = ('/body', '/message-summary', '/documents/{doc}', '/sessions/{sid}', '/code-activity')

BLOCKING_SUBPROCESS_CALLS = {'run', 'check_output', 'call', 'check_call', 'Popen'}
BLOCKING_OS_PROCESS_CALLS = {
    'system', 'popen', 'spawnl', 'spawnle', 'spawnlp', 'spawnlpe', 'spawnv', 'spawnve', 'spawnvp', 'spawnvpe',
}
BLOCKING_SAMPLER_NAMES = {'sample_process', 'default_sample_process'}
HASH_DIGEST_NAMES = {'sha256', 'sha512', 'sha1', 'md5'}
BINARY_EXPR_NAMES = {'node', 'executable', 'binary', 'bin_path', 'node_executable', 'node_path', 'node_bin'}
SCRIPT_ARCHIVE_EXPR_NAMES = {'archive', 'tarball', 'dmg', 'installer', 'bundle'}
PROFILE_TIMEOUT_CALLS = {'wait_for', 'timeout', 'timeout_at'}
SQLITE_EXEC_METHODS = {'execute', 'executescript'}
WAL_MODE_RE = re.compile(r"pragma\s+(?:\w+\.)?journal_mode\s*=\s*['\"]?wal['\"]?\b", re.IGNORECASE)
WAL_SIZE_LIMIT_RE = re.compile(r'pragma\s+(?:\w+\.)?journal_size_limit\b', re.IGNORECASE)
PRUNE_HELPER_NAME_HINTS = ('prune', 'rotate', 'cleanup')
SCOPE_DEF_TYPES = (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)


def _iter_assignment_targets(target):
    if isinstance(target, (ast.Tuple, ast.List)):
        for element in target.elts:
            yield from _iter_assignment_targets(element)
    elif isinstance(target, ast.Starred):
        yield from _iter_assignment_targets(target.value)
    else:
        yield target


def _param_names(fn_node):
    if not isinstance(fn_node, (ast.FunctionDef, ast.AsyncFunctionDef)):
        return set()
    names = {arg.arg for arg in (*fn_node.args.posonlyargs, *fn_node.args.args, *fn_node.args.kwonlyargs)}
    if fn_node.args.vararg is not None:
        names.add(fn_node.args.vararg.arg)
    if fn_node.args.kwarg is not None:
        names.add(fn_node.args.kwarg.arg)
    return names


def _build_ast_index(tree):
    """Single-pass structural index over the module AST to eliminate repeated walks."""
    parent_map = {}
    all_nodes = []
    docstring_ids = set()
    functions = []
    scope_nodes = {}
    scope_children = {}

    def walk_scope(scope_node):
        all_nodes.append(scope_node)
        nodes = []
        children_defs = []
        if isinstance(scope_node, (ast.Module, ast.ClassDef, ast.FunctionDef, ast.AsyncFunctionDef)):
            if isinstance(scope_node, (ast.FunctionDef, ast.AsyncFunctionDef)):
                functions.append(scope_node)
            body = getattr(scope_node, 'body', None)
            if (body and isinstance(body[0], ast.Expr)
                    and isinstance(body[0].value, ast.Constant)
                    and isinstance(body[0].value.value, str)):
                docstring_ids.add(id(body[0].value))

        def walk_expr(expr):
            stack = [expr]
            while stack:
                cur = stack.pop()
                all_nodes.append(cur)
                if not isinstance(cur, ast.arg):
                    nodes.append(cur)
                for ch in ast.iter_child_nodes(cur):
                    parent_map[id(ch)] = cur
                    if isinstance(ch, SCOPE_DEF_TYPES):
                        children_defs.append(ch)
                        walk_scope(ch)
                    else:
                        stack.append(ch)

        if isinstance(scope_node, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)):
            for attr in ('decorator_list', 'bases', 'keywords', 'args', 'returns', 'type_params'):
                val = getattr(scope_node, attr, None)
                if isinstance(val, list):
                    for item in val:
                        parent_map[id(item)] = scope_node
                        if isinstance(item, SCOPE_DEF_TYPES):
                            children_defs.append(item)
                            walk_scope(item)
                        else:
                            walk_expr(item)
                elif isinstance(val, ast.AST):
                    parent_map[id(val)] = scope_node
                    walk_expr(val)

        queue = deque()
        for field in ('body', 'orelse', 'handlers', 'finalbody'):
            items = getattr(scope_node, field, None)
            if isinstance(items, list):
                for item in items:
                    parent_map[id(item)] = scope_node
                    queue.append(item)
        while queue:
            cur = queue.popleft()
            if isinstance(cur, SCOPE_DEF_TYPES):
                children_defs.append(cur)
                walk_scope(cur)
                continue
            nodes.append(cur)
            all_nodes.append(cur)
            for ch in ast.iter_child_nodes(cur):
                parent_map[id(ch)] = cur
                queue.append(ch)

        scope_nodes[id(scope_node)] = nodes
        scope_children[id(scope_node)] = children_defs

    walk_scope(tree)
    return {
        'all_nodes': all_nodes,
        'parent_map': parent_map,
        'docstring_ids': docstring_ids,
        'functions': functions,
        'scope_nodes': scope_nodes,
        'scope_children': scope_children,
    }


def _iter_scope_nodes(root, *, skip_to_thread_lambda=False, to_thread_check=None, include_defs=False):
    """Yield AST nodes inside a function/loop/if body without descending into nested def/class definitions."""
    queue = deque()
    for field in ('body', 'orelse', 'handlers', 'finalbody'):
        items = getattr(root, field, None)
        if isinstance(items, list):
            for item in items:
                if not include_defs and isinstance(item, SCOPE_DEF_TYPES):
                    continue
                queue.append(item)
    while queue:
        current = queue.popleft()
        yield current
        if skip_to_thread_lambda and isinstance(current, ast.Call) and to_thread_check is not None:
            skip_idx = to_thread_check(current)
            if skip_idx is not None:
                if include_defs or not isinstance(current.func, SCOPE_DEF_TYPES):
                    queue.append(current.func)
                for idx, arg in enumerate(current.args):
                    if idx == skip_idx and isinstance(arg, ast.Lambda):
                        continue
                    if include_defs or not isinstance(arg, SCOPE_DEF_TYPES):
                        queue.append(arg)
                for kw in current.keywords:
                    if include_defs or not isinstance(kw.value, SCOPE_DEF_TYPES):
                        queue.append(kw.value)
                continue
        for child in ast.iter_child_nodes(current):
            if not include_defs and isinstance(child, SCOPE_DEF_TYPES):
                continue
            queue.append(child)


def _iter_block_children(root):
    """Yield direct child FunctionDef/AsyncFunctionDef/ClassDef defined inside root's own scope."""
    queue = deque()
    for field in ('body', 'orelse', 'handlers', 'finalbody'):
        items = getattr(root, field, None)
        if isinstance(items, list):
            queue.extend(items)
    while queue:
        current = queue.popleft()
        if isinstance(current, SCOPE_DEF_TYPES):
            yield current
            continue
        for child in ast.iter_child_nodes(current):
            queue.append(child)


def _is_numeric_constant(node):
    return (
        isinstance(node, ast.Constant)
        and isinstance(node.value, (int, float))
        and not isinstance(node.value, bool)
    )


def _fold_numeric(node):
    if _is_numeric_constant(node):
        return float(node.value)
    if isinstance(node, ast.UnaryOp) and isinstance(node.op, (ast.UAdd, ast.USub)):
        operand = _fold_numeric(node.operand)
        if operand is not None:
            return operand if isinstance(node.op, ast.UAdd) else -operand
    if isinstance(node, ast.BinOp) and isinstance(node.op, (ast.Add, ast.Sub, ast.Mult, ast.Div)):
        left = _fold_numeric(node.left)
        right = _fold_numeric(node.right)
        if left is not None and right is not None:
            if isinstance(node.op, ast.Add):
                return left + right
            if isinstance(node.op, ast.Sub):
                return left - right
            if isinstance(node.op, ast.Mult):
                return left * right
            if right != 0:
                return left / right
    return None


def literal(node):
    if isinstance(node, ast.Constant) and node.value is None:
        return False
    if _fold_numeric(node) is not None:
        return True
    try:
        ast.literal_eval(node)
        return True
    except (ValueError, TypeError):
        return False


def string(node):
    return node.value if isinstance(node, ast.Constant) and isinstance(node.value, str) else None


def _numeric_literal(node, constants=None):
    folded = _fold_numeric(node)
    if folded is not None:
        return folded
    if constants and isinstance(node, ast.Name) and node.id in constants:
        return constants[node.id]
    return None


def _is_truthy_ifexp(node):
    return (
        isinstance(node, ast.IfExp)
        and not isinstance(node.test, ast.Compare)
        and ast.dump(node.body) == ast.dump(node.test)
    )


def _is_while_true(node):
    return (
        isinstance(node, ast.While)
        and isinstance(node.test, ast.Constant)
        and bool(node.test.value) is True
        and not isinstance(node.test.value, str)
    )


def _node_references_name_substr(node, substr):
    target = substr.lower()
    for sub in ast.walk(node):
        if isinstance(sub, ast.Name) and target in sub.id.lower():
            return True
        if isinstance(sub, ast.Attribute) and target in sub.attr.lower():
            return True
    return False


def _collect_module_numeric_constants(tree):
    constants = {}
    for stmt in tree.body:
        if isinstance(stmt, ast.Assign) and len(stmt.targets) == 1 and isinstance(stmt.targets[0], ast.Name):
            val = _fold_numeric(stmt.value)
            if val is not None:
                constants[stmt.targets[0].id] = val
            else:
                constants.pop(stmt.targets[0].id, None)
        elif isinstance(stmt, ast.AnnAssign) and isinstance(stmt.target, ast.Name) and stmt.value is not None:
            val = _fold_numeric(stmt.value)
            if val is not None:
                constants[stmt.target.id] = val
            else:
                constants.pop(stmt.target.id, None)
    return constants


def _find_environ_mutations_in_nodes(nodes, os_names, environ_names, putenv_names):
    """Return list of (ast_node, constant_key_or_None) for environ mutations found in nodes."""
    def is_environ_ref(expr):
        if isinstance(expr, ast.Name) and expr.id in environ_names:
            return True
        return (
            isinstance(expr, ast.Attribute)
            and expr.attr in ('environ', 'environb')
            and isinstance(expr.value, ast.Name)
            and expr.value.id in os_names
        )

    hits = []
    for node in nodes:
        if isinstance(node, ast.Assign):
            for target in node.targets:
                for leaf in _iter_assignment_targets(target):
                    if isinstance(leaf, ast.Subscript) and is_environ_ref(leaf.value):
                        hits.append((node, string(leaf.slice)))
        elif isinstance(node, ast.AnnAssign):
            if node.value is not None and isinstance(node.target, ast.Subscript) and is_environ_ref(node.target.value):
                hits.append((node, string(node.target.slice)))
        elif isinstance(node, ast.AugAssign):
            if isinstance(node.target, ast.Subscript) and is_environ_ref(node.target.value):
                hits.append((node, string(node.target.slice)))
            elif is_environ_ref(node.target):
                hits.append((node, None))
        elif isinstance(node, ast.Delete):
            for target in node.targets:
                for leaf in _iter_assignment_targets(target):
                    if isinstance(leaf, ast.Subscript) and is_environ_ref(leaf.value):
                        hits.append((node, string(leaf.slice)))
        elif isinstance(node, ast.Call):
            if (isinstance(node.func, ast.Attribute)
                    and node.func.attr in ENVIRON_MUTATING_METHODS
                    and is_environ_ref(node.func.value)):
                key = string(node.args[0]) if node.args and node.func.attr in (
                    'pop', 'setdefault', '__setitem__', '__delitem__'
                ) else None
                hits.append((node, key))
            elif ((isinstance(node.func, ast.Attribute)
                   and node.func.attr in ('putenv', 'unsetenv')
                   and isinstance(node.func.value, ast.Name)
                   and node.func.value.id in os_names)
                  or (isinstance(node.func, ast.Name) and node.func.id in putenv_names)):
                key = string(node.args[0]) if node.args else None
                hits.append((node, key))
    return hits


def _compute_scoped_allow_nodes(tree, filename, parent_map):
    scoped_name = ENVIRON_SCOPED_ALLOWLIST.get(filename)
    if not scoped_name:
        return set()
    scoped_fn = next(
        (node for node in tree.body if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)) and node.name == scoped_name),
        None,
    )
    if scoped_fn is None:
        return set()

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

    allowed = set()
    for try_node in (n for n in ast.walk(scoped_fn) if isinstance(n, ast.Try) and n.finalbody):
        final_nodes = []
        for stmt in try_node.finalbody:
            final_nodes.extend(ast.walk(stmt))
        final_hits = _find_environ_mutations_in_nodes(final_nodes, os_names, environ_names, putenv_names)
        if not final_hits:
            continue
        has_dynamic = any(key is None for _, key in final_hits)
        final_keys = {key for _, key in final_hits if key is not None}

        candidate_nodes = []
        for stmt in try_node.body + try_node.finalbody:
            candidate_nodes.extend(ast.walk(stmt))

        # Include contiguous setup statements immediately preceding this try block.
        parent = parent_map.get(id(try_node))
        if parent is not None:
            for field in ('body', 'orelse'):
                seq = getattr(parent, field, None)
                if isinstance(seq, list) and try_node in seq:
                    idx = seq.index(try_node)
                    for prev in reversed(seq[:idx]):
                        if isinstance(prev, (ast.Return, ast.Yield, ast.YieldFrom, ast.If, ast.For, ast.While, ast.Try)):
                            break
                        candidate_nodes.extend(ast.walk(prev))
                    break

        candidate_hits = _find_environ_mutations_in_nodes(candidate_nodes, os_names, environ_names, putenv_names)
        for hit_node, hit_key in candidate_hits:
            if has_dynamic or (hit_key is not None and hit_key in final_keys):
                allowed.add(hit_node)
    return allowed


def _check_environ_mutations(tree, path, ast_index, report):
    path_obj = Path(path)
    filename = path_obj.name
    if path_obj.parent.name == 'scripts' or filename in ENVIRON_MUTATION_ALLOWLIST:
        return
    parent_map = ast_index['parent_map']
    scope_nodes_map = ast_index['scope_nodes']
    scope_children_map = ast_index['scope_children']
    scoped_allowed = _compute_scoped_allow_nodes(tree, filename, parent_map)

    def walk_scope(scope_root, base_os, base_environ, base_putenv):
        params = _param_names(scope_root)
        os_names = set(base_os) - params
        environ_names = set(base_environ) - params
        putenv_names = set(base_putenv) - params
        nodes = scope_nodes_map[id(scope_root)]

        for node in nodes:
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

        for _ in range(len(nodes) + 1):
            previous = set(environ_names)
            for node in nodes:
                if isinstance(node, ast.Assign) and is_environ_ref(node.value):
                    environ_names.update(t.id for t in node.targets if isinstance(t, ast.Name))
                elif isinstance(node, ast.AnnAssign) and node.value is not None and is_environ_ref(node.value):
                    if isinstance(node.target, ast.Name):
                        environ_names.add(node.target.id)
            if environ_names == previous:
                break

        for hit_node, _ in _find_environ_mutations_in_nodes(nodes, os_names, environ_names, putenv_names):
            if hit_node not in scoped_allowed:
                report(hit_node, 'ML007', ML007_MESSAGE)

        for child_def in scope_children_map[id(scope_root)]:
            walk_scope(child_def, os_names, environ_names, putenv_names)

    walk_scope(tree, {'os'}, set(), set())


def _check_model_boundaries(tree, path, ast_index, report):
    scope_nodes_map = ast_index['scope_nodes']
    scope_children_map = ast_index['scope_children']
    filename = Path(path).name
    allowed_factory = None
    if filename == 'generation_config.py':
        allowed_factory = next(
            (node for node in tree.body if isinstance(node, ast.FunctionDef) and node.name == 'native_llm_configuration'),
            None,
        )
    factory_calls = set(ast.walk(allowed_factory)) if allowed_factory else set()

    def is_injected_or_ifexp(value):
        return (isinstance(value, ast.BoolOp) and isinstance(value.op, ast.Or)) or _is_truthy_ifexp(value)

    def injected(node, key, value):
        key_name = key if isinstance(key, str) else string(key)
        if key_name in PARAMETERS:
            if literal(value) and string(value) not in PARAMETERS:
                report(node, 'ML002', ML002_LITERAL_MESSAGE)
            if is_injected_or_ifexp(value):
                report(node, 'ML003', ML003_OR_MESSAGE)

    # Scope-aware ML001 constructor/subclass check & ML002/ML003 checks.
    def walk_scope(scope_root, inherited_constructors):
        constructors = set(inherited_constructors) - _param_names(scope_root)
        nodes = scope_nodes_map[id(scope_root)]

        for node in nodes:
            if isinstance(node, ast.ImportFrom):
                constructors.update(alias.asname or alias.name for alias in node.names if alias.name == 'LLMConfig')

        child_defs = scope_children_map[id(scope_root)]
        for _ in range(len(nodes) + len(child_defs) + 1):
            previous = set(constructors)
            for node in nodes:
                if isinstance(node, ast.Assign) and (
                    (isinstance(node.value, ast.Name) and node.value.id in constructors)
                    or (isinstance(node.value, ast.Attribute) and node.value.attr == 'LLMConfig')
                ):
                    constructors.update(target.id for target in node.targets if isinstance(target, ast.Name))
                elif isinstance(node, ast.AnnAssign) and node.value is not None and isinstance(node.target, ast.Name):
                    if (isinstance(node.value, ast.Name) and node.value.id in constructors) or (
                        isinstance(node.value, ast.Attribute) and node.value.attr == 'LLMConfig'
                    ):
                        constructors.add(node.target.id)
            for child in child_defs:
                if isinstance(child, ast.ClassDef):
                    for base in child.bases:
                        if (isinstance(base, ast.Name) and base.id in constructors) or (
                            isinstance(base, ast.Attribute) and base.attr == 'LLMConfig'
                        ):
                            constructors.add(child.name)
            if constructors == previous:
                break

        if isinstance(scope_root, (ast.FunctionDef, ast.AsyncFunctionDef)):
            positional = list(scope_root.args.posonlyargs) + list(scope_root.args.args)
            if scope_root.args.defaults:
                for arg, default in zip(positional[-len(scope_root.args.defaults):], scope_root.args.defaults):
                    if arg.arg in PARAMETERS:
                        if literal(default) and string(default) not in PARAMETERS:
                            report(default, 'ML002', ML002_LITERAL_MESSAGE)
                        if is_injected_or_ifexp(default):
                            report(default, 'ML003', ML003_OR_MESSAGE)
            for arg, default in zip(scope_root.args.kwonlyargs, scope_root.args.kw_defaults):
                if default is not None and arg.arg in PARAMETERS:
                    if literal(default) and string(default) not in PARAMETERS:
                        report(default, 'ML002', ML002_LITERAL_MESSAGE)
                    if is_injected_or_ifexp(default):
                        report(default, 'ML003', ML003_OR_MESSAGE)

        for node in nodes:
            if isinstance(node, ast.Call):
                is_ctor = (
                    (isinstance(node.func, ast.Name) and node.func.id in constructors)
                    or (isinstance(node.func, ast.Attribute) and node.func.attr == 'LLMConfig')
                )
                if is_ctor:
                    valid = (
                        node in factory_calls
                        and not node.args
                        and len(node.keywords) == 1
                        and node.keywords[0].arg is None
                        and isinstance(node.keywords[0].value, ast.Call)
                        and isinstance(node.keywords[0].value.func, ast.Name)
                        and node.keywords[0].value.func.id == 'native_llm_kwargs'
                    )
                    if not valid:
                        report(node, 'ML001', ML001_MESSAGE)
                if isinstance(node.func, ast.Attribute):
                    if node.func.attr in ('get', 'setdefault'):
                        if len(node.args) >= 2 and string(node.args[0]) in PARAMETERS:
                            report(node, 'ML003', ML003_FALLBACK_MESSAGE)
                    elif node.func.attr == 'pop':
                        if len(node.args) >= 2 and string(node.args[0]) in PARAMETERS:
                            if not (isinstance(node.args[1], ast.Constant) and node.args[1].value is None):
                                report(node, 'ML003', ML003_FALLBACK_MESSAGE)
                for keyword in node.keywords:
                    if keyword.arg in PARAMETERS:
                        if literal(keyword.value) and string(keyword.value) not in PARAMETERS:
                            report(node, 'ML002', ML002_CALL_MESSAGE)
                        elif is_injected_or_ifexp(keyword.value):
                            report(node, 'ML003', ML003_OR_MESSAGE)
            elif isinstance(node, ast.Dict):
                for key, value in zip(node.keys, node.values):
                    if key is not None:
                        injected(node, key, value)
            elif isinstance(node, ast.Assign):
                for target in node.targets:
                    for leaf in _iter_assignment_targets(target):
                        if isinstance(leaf, ast.Subscript):
                            injected(node, leaf.slice, node.value)
                        elif isinstance(leaf, ast.Attribute):
                            injected(node, leaf.attr, node.value)
                        elif isinstance(leaf, ast.Name):
                            injected(node, leaf.id, node.value)
            elif isinstance(node, ast.AnnAssign) and node.value is not None:
                target = node.target
                if isinstance(target, ast.Subscript):
                    injected(node, target.slice, node.value)
                elif isinstance(target, ast.Attribute):
                    injected(node, target.attr, node.value)
                elif isinstance(target, ast.Name):
                    injected(node, target.id, node.value)

        for child_def in child_defs:
            walk_scope(child_def, constructors)

    walk_scope(tree, {'LLMConfig'})


def _extract_sql_text(node):
    if isinstance(node, ast.Constant) and isinstance(node.value, str):
        return node.value
    if isinstance(node, ast.JoinedStr):
        parts = []
        for value in node.values:
            if isinstance(value, ast.Constant) and isinstance(value.value, str):
                parts.append(value.value)
            elif isinstance(value, ast.FormattedValue):
                inner = _extract_sql_text(value.value)
                parts.append(inner if inner is not None else '{}')
        return ''.join(parts)
    if isinstance(node, ast.BinOp) and isinstance(node.op, ast.Add):
        left = _extract_sql_text(node.left)
        right = _extract_sql_text(node.right)
        if left is not None and right is not None:
            return left + right
    return None


def _is_dead_false_branch(test):
    return isinstance(test, ast.Constant) and not bool(test.value)


def _walk_live_nodes(stmt):
    """Yield AST nodes beneath stmt while skipping statically dead `if False:` bodies."""
    stack = [stmt]
    while stack:
        current = stack.pop()
        yield current
        if isinstance(current, ast.If) and _is_dead_false_branch(current.test):
            stack.append(current.test)
            stack.extend(reversed(current.orelse))
            continue
        children = list(ast.iter_child_nodes(current))
        stack.extend(reversed(children))


def _expr_references_binary(expr, extra_names=None):
    target_names = BINARY_EXPR_NAMES if not extra_names else (BINARY_EXPR_NAMES | extra_names)
    return any(
        (isinstance(t, ast.Name) and t.id in target_names)
        or (isinstance(t, ast.Attribute) and t.attr in target_names)
        or (isinstance(t, ast.Constant) and isinstance(t.value, str) and t.value in target_names)
        for t in ast.walk(expr)
    )


def _is_descendant_of_watch_if(node, root_fn, parent_map):
    child = node
    while child is not None and child is not root_fn:
        parent = parent_map.get(id(child))
        if parent is None:
            break
        if isinstance(parent, (ast.If, ast.IfExp)) and _node_references_name_substr(parent.test, 'watch'):
            negated = isinstance(parent.test, ast.UnaryOp) and isinstance(parent.test.op, ast.Not)
            if isinstance(parent, ast.IfExp):
                if (child is parent.orelse) if negated else (child is parent.body):
                    return True
            else:
                if (child in parent.orelse) if negated else (child in parent.body):
                    return True
        child = parent
    return False


def _check_runtime_hygiene(tree, path, ast_index, report):
    path_obj = Path(path)
    filename = path_obj.name
    is_scripts = path_obj.parent.name == 'scripts' or (
        len(path_obj.parts) == 1 and filename in {'dev.py', 'disk-space.py', 'task.py', 'bootstrap.py'}
    )
    parent_map = ast_index['parent_map']
    all_nodes = ast_index['all_nodes']
    docstring_ids = ast_index['docstring_ids']
    functions = ast_index['functions']
    scope_nodes_map = ast_index['scope_nodes']
    numeric_constants = _collect_module_numeric_constants(tree)

    base_asyncio = {'asyncio'}
    base_asyncio_sleep = set()
    base_to_thread = set()
    base_time = {'time'}
    base_time_sleep = set()
    base_subprocess = {'subprocess'}
    base_subprocess_calls = set()
    base_os = {'os'}
    base_os_proc_calls = set()
    base_hashlib = {'hashlib'}
    base_hash_funcs = set()

    for node in all_nodes:
        if isinstance(node, ast.Import):
            for alias in node.names:
                local = alias.asname or alias.name
                if alias.name == 'asyncio':
                    base_asyncio.add(local)
                elif alias.name == 'time':
                    base_time.add(local)
                elif alias.name == 'subprocess':
                    base_subprocess.add(local)
                elif alias.name == 'os':
                    base_os.add(local)
                elif alias.name == 'hashlib':
                    base_hashlib.add(local)
        elif isinstance(node, ast.ImportFrom):
            if node.module == 'asyncio':
                for alias in node.names:
                    local = alias.asname or alias.name
                    if alias.name == 'sleep':
                        base_asyncio_sleep.add(local)
                    elif alias.name == 'to_thread':
                        base_to_thread.add(local)
            elif node.module == 'time':
                for alias in node.names:
                    if alias.name == 'sleep':
                        base_time_sleep.add(alias.asname or alias.name)
            elif node.module == 'subprocess':
                for alias in node.names:
                    if alias.name in BLOCKING_SUBPROCESS_CALLS:
                        base_subprocess_calls.add(alias.asname or alias.name)
            elif node.module == 'os':
                for alias in node.names:
                    if alias.name in BLOCKING_OS_PROCESS_CALLS:
                        base_os_proc_calls.add(alias.asname or alias.name)
            elif node.module == 'hashlib':
                for alias in node.names:
                    if alias.name in HASH_DIGEST_NAMES:
                        base_hash_funcs.add(alias.asname or alias.name)

    def scope_aliases(scope_node):
        params = _param_names(scope_node)
        nodes = scope_nodes_map[id(scope_node)]
        asyncio_names = set(base_asyncio) - params
        asyncio_sleep_names = set(base_asyncio_sleep) - params
        to_thread_names = set(base_to_thread) - params
        time_names = set(base_time) - params
        time_sleep_names = set(base_time_sleep) - params
        subprocess_names = set(base_subprocess) - params
        subprocess_call_names = set(base_subprocess_calls) - params
        os_names = set(base_os) - params
        os_proc_names = set(base_os_proc_calls) - params

        # Propagate simple local function aliases inside this scope.
        for _ in range(3):
            for sub in nodes:
                if isinstance(sub, ast.Assign) and isinstance(sub.value, (ast.Name, ast.Attribute)):
                    targets = [t.id for t in sub.targets if isinstance(t, ast.Name)]
                    val = sub.value
                    if ((isinstance(val, ast.Name) and val.id in asyncio_sleep_names) or (
                        isinstance(val, ast.Attribute) and val.attr == 'sleep'
                        and isinstance(val.value, ast.Name) and val.value.id in asyncio_names
                    )):
                        asyncio_sleep_names.update(targets)
                    if ((isinstance(val, ast.Name) and val.id in subprocess_call_names) or (
                        isinstance(val, ast.Attribute) and val.attr in BLOCKING_SUBPROCESS_CALLS
                        and isinstance(val.value, ast.Name) and val.value.id in subprocess_names
                    )):
                        subprocess_call_names.update(targets)
                    if ((isinstance(val, ast.Name) and val.id in os_proc_names) or (
                        isinstance(val, ast.Attribute) and val.attr in BLOCKING_OS_PROCESS_CALLS
                        and isinstance(val.value, ast.Name) and val.value.id in os_names
                    )):
                        os_proc_names.update(targets)

        return {
            'nodes': nodes,
            'asyncio_names': asyncio_names,
            'asyncio_sleep_names': asyncio_sleep_names,
            'to_thread_names': to_thread_names,
            'time_names': time_names,
            'time_sleep_names': time_sleep_names,
            'subprocess_names': subprocess_names,
            'subprocess_call_names': subprocess_call_names,
            'os_names': os_names,
            'os_proc_names': os_proc_names,
        }

    def is_asyncio_sleep(call, env):
        if not isinstance(call, ast.Call):
            return False
        if isinstance(call.func, ast.Name) and call.func.id in env['asyncio_sleep_names']:
            return True
        return (
            isinstance(call.func, ast.Attribute)
            and call.func.attr == 'sleep'
            and isinstance(call.func.value, ast.Name)
            and call.func.value.id in env['asyncio_names']
        )

    def is_time_sleep(call, env):
        if not isinstance(call, ast.Call):
            return False
        if isinstance(call.func, ast.Name) and call.func.id in env['time_sleep_names']:
            return True
        return (
            isinstance(call.func, ast.Attribute)
            and call.func.attr == 'sleep'
            and isinstance(call.func.value, ast.Name)
            and call.func.value.id in env['time_names']
        )

    def sleep_delay_seconds(call):
        expr = None
        if call.args:
            expr = call.args[0]
        else:
            for kw in call.keywords:
                if kw.arg == 'delay':
                    expr = kw.value
                    break
        if expr is None:
            return None
        return _numeric_literal(expr, numeric_constants)

    def offload_callable_index(call, env):
        if not isinstance(call, ast.Call):
            return None
        if isinstance(call.func, ast.Name) and call.func.id in env['to_thread_names']:
            return 0
        if isinstance(call.func, ast.Attribute):
            if (call.func.attr == 'to_thread'
                    and isinstance(call.func.value, ast.Name)
                    and call.func.value.id in env['asyncio_names']):
                return 0
            if call.func.attr == 'run_in_executor':
                return 1
        return None

    def is_blocking_subprocess_func(func_expr, env):
        if isinstance(func_expr, ast.Name) and (
            func_expr.id in env['subprocess_call_names']
            or func_expr.id in env['os_proc_names']
            or func_expr.id in BLOCKING_SAMPLER_NAMES
        ):
            return True
        if isinstance(func_expr, ast.Attribute):
            if (func_expr.attr in BLOCKING_SUBPROCESS_CALLS
                    and isinstance(func_expr.value, ast.Name)
                    and func_expr.value.id in env['subprocess_names']):
                return True
            if (func_expr.attr in BLOCKING_OS_PROCESS_CALLS
                    and isinstance(func_expr.value, ast.Name)
                    and func_expr.value.id in env['os_names']):
                return True
            if func_expr.attr in BLOCKING_SAMPLER_NAMES:
                return True
        return False

    def is_hash_call(call):
        if not isinstance(call, ast.Call):
            return False
        if isinstance(call.func, ast.Name) and call.func.id in base_hash_funcs:
            return True
        return (
            isinstance(call.func, ast.Attribute)
            and call.func.attr in HASH_DIGEST_NAMES
            and isinstance(call.func.value, ast.Name)
            and call.func.value.id in base_hashlib
        )

    # Pre-index sync helper functions and check ML013 (part 2) track_descendants throttling.
    sync_func_map = {fn.name: fn for fn in functions if isinstance(fn, ast.FunctionDef)}

    def helper_has_throttled_track(fn):
        if not _node_references_name_substr(fn, 'monotonic'):
            return False
        fn_if_enclosed = set()
        for sub in _iter_scope_nodes(fn):
            if isinstance(sub, ast.If):
                for child in _iter_scope_nodes(sub):
                    fn_if_enclosed.add(child)
        # Early-return monotonic gate before track_descendants inside block statements.
        early_return_guarded_stmts = set()
        for blk_parent in (fn, *fn_if_enclosed):
            for field in ('body', 'orelse'):
                seq = getattr(blk_parent, field, None)
                if not isinstance(seq, list):
                    continue
                seen_monotonic_return = False
                for stmt in seq:
                    if seen_monotonic_return:
                        early_return_guarded_stmts.update(ast.walk(stmt))
                    if isinstance(stmt, ast.If) and any(isinstance(n, ast.Return) for n in _iter_scope_nodes(stmt)):
                        if (_node_references_name_substr(stmt.test, 'monotonic')
                                or _node_references_name_substr(stmt.test, 'interval')
                                or _node_references_name_substr(stmt.test, 'last')):
                            seen_monotonic_return = True

        for sub in _iter_scope_nodes(fn):
            if isinstance(sub, ast.Call) and isinstance(sub.func, ast.Name) and sub.func.id == 'track_descendants':
                if not (sub in fn_if_enclosed or sub in early_return_guarded_stmts):
                    return False
        return True

    helpers_calling_track = {
        fn_name for fn_name, fn_node in sync_func_map.items()
        if any(
            isinstance(sub, ast.Call) and isinstance(sub.func, ast.Name) and sub.func.id == 'track_descendants'
            for sub in scope_nodes_map[id(fn_node)]
        )
    }

    mod_env = scope_aliases(tree)
    for fn_or_mod in [tree, *functions]:
        cur_env = mod_env if fn_or_mod is tree else scope_aliases(fn_or_mod)
        for node in cur_env['nodes']:
            if not _is_while_true(node):
                continue
            has_short_time_sleep = False
            for sub in _iter_scope_nodes(node):
                if is_time_sleep(sub, cur_env):
                    delay = sleep_delay_seconds(sub)
                    if delay is not None and 0 <= delay < 1.0:
                        has_short_time_sleep = True
                        break
            if not has_short_time_sleep:
                continue
            loop_has_monotonic = _node_references_name_substr(node, 'monotonic')
            if_enclosed_nodes = set()
            for sub in _iter_scope_nodes(node):
                if isinstance(sub, ast.If):
                    for child in _iter_scope_nodes(sub):
                        if_enclosed_nodes.add(child)
            for sub in _iter_scope_nodes(node):
                if isinstance(sub, ast.Call) and isinstance(sub.func, ast.Name):
                    if sub.func.id == 'track_descendants':
                        if not (loop_has_monotonic and sub in if_enclosed_nodes):
                            report(sub, 'ML013', ML013_DEV_TRACK_MESSAGE)
                    elif sub.func.id in helpers_calling_track:
                        helper_fn = sync_func_map[sub.func.id]
                        loop_throttled = loop_has_monotonic and sub in if_enclosed_nodes
                        if not loop_throttled and not helper_has_throttled_track(helper_fn):
                            report(sub, 'ML013', ML013_DEV_TRACK_MESSAGE)

    # ML017 (part 1): In scripts/dev.py reopen_ui(...), require ['mygo', 'dev'] inside a watch branch.
    if filename == 'dev.py':
        for node in functions:
            if isinstance(node, ast.FunctionDef) and node.name == 'reopen_ui':
                for sub in scope_nodes_map[id(node)]:
                    if isinstance(sub, (ast.List, ast.Tuple)):
                        items = [string(el) for el in sub.elts]
                        if 'mygo' in items and 'dev' in items and items.index('mygo') < items.index('dev'):
                            if not _is_descendant_of_watch_if(sub, node, parent_map):
                                report(sub, 'ML017', ML017_REOPEN_UI_MESSAGE)

    # ML017 (part 2): In scripts/disk-space.py, writing receipts into build/disk-cleanup must invoke pruning.
    if filename == 'disk-space.py':
        fn_map = {fn.name: fn for fn in functions if isinstance(fn, ast.FunctionDef) and fn.name != 'prune_history'}
        direct_unlink = set()
        call_sites = {name: [] for name in fn_map}
        fn_loop_nodes = {}
        call_graph = {name: set() for name in fn_map}
        for fn_name, fn_node in fn_map.items():
            loop_nodes = set()
            fn_nodes = scope_nodes_map[id(fn_node)]
            for sub in fn_nodes:
                if isinstance(sub, (ast.For, ast.AsyncFor, ast.While, ast.ListComp, ast.SetComp, ast.GeneratorExp)):
                    loop_nodes.update(ast.walk(sub))
            fn_loop_nodes[fn_name] = loop_nodes
            for sub in fn_nodes:
                if isinstance(sub, ast.Call):
                    if isinstance(sub.func, ast.Name) and sub.func.id in fn_map:
                        call_graph[fn_name].add(sub.func.id)
                        call_sites[fn_name].append((sub.func.id, sub))
                    if isinstance(sub.func, ast.Attribute) and sub.func.attr == 'unlink':
                        direct_unlink.add(fn_name)

        unlink_reachable = set(direct_unlink)
        for _ in range(len(fn_map) + 1):
            changed = False
            for fn_name, callees in call_graph.items():
                if fn_name not in unlink_reachable and (callees & unlink_reachable):
                    unlink_reachable.add(fn_name)
                    changed = True
            if not changed:
                break

        pruning_helpers = set()
        for fn_name in unlink_reachable:
            fn_node = fn_map[fn_name]
            name_hint = any(hint in fn_name.lower() for hint in PRUNE_HELPER_NAME_HINTS)
            loop_nodes = fn_loop_nodes[fn_name]
            direct_loop_unlink = any(
                isinstance(sub, ast.Call) and isinstance(sub.func, ast.Attribute) and sub.func.attr == 'unlink'
                and sub in loop_nodes
                for sub in scope_nodes_map[id(fn_node)]
            )
            callee_loop_unlink = any(
                callee in unlink_reachable and (name_hint or site in loop_nodes)
                for callee, site in call_sites[fn_name]
            )
            if name_hint or direct_loop_unlink or callee_loop_unlink:
                pruning_helpers.add(fn_name)

        for _ in range(len(fn_map) + 1):
            changed = False
            for fn_name, callees in call_graph.items():
                if fn_name not in pruning_helpers and (callees & pruning_helpers):
                    pruning_helpers.add(fn_name)
                    changed = True
            if not changed:
                break

        def is_cleanup_write(call, fn_has_cleanup_ref):
            if not isinstance(call, ast.Call):
                return False
            is_write = False
            if isinstance(call.func, ast.Attribute) and call.func.attr in ('write_text', 'write_bytes'):
                is_write = True
            elif isinstance(call.func, ast.Name) and call.func.id == 'open':
                mode_expr = call.args[1] if len(call.args) >= 2 else next(
                    (kw.value for kw in call.keywords if kw.arg == 'mode'), None
                )
                mode_val = string(mode_expr) if mode_expr is not None else ''
                if any(m in mode_val for m in ('w', 'a', 'x', '+')):
                    is_write = True
            elif isinstance(call.func, ast.Attribute) and call.func.attr == 'open':
                mode_expr = call.args[0] if call.args else next(
                    (kw.value for kw in call.keywords if kw.arg == 'mode'), None
                )
                mode_val = string(mode_expr) if mode_expr is not None else ''
                if any(m in mode_val for m in ('w', 'a', 'x', '+')):
                    is_write = True
            return is_write and fn_has_cleanup_ref

        for fn_name, fn_node in fn_map.items():
            fn_nodes = scope_nodes_map[id(fn_node)]
            fn_has_cleanup_ref = any(
                isinstance(s, ast.Constant) and isinstance(s.value, str) and 'disk-cleanup' in s.value
                for s in fn_nodes
            )
            write_calls = [sub for sub in fn_nodes if is_cleanup_write(sub, fn_has_cleanup_ref)]
            if not write_calls:
                continue
            invokes_pruning = fn_name in pruning_helpers or bool(call_graph.get(fn_name, set()) & pruning_helpers)
            if not invokes_pruning:
                for write_call in write_calls:
                    report(write_call, 'ML017', ML017_CLEANUP_RECEIPT_MESSAGE)

    # ML015 (part 1): Reject hashlib.<hash>(<binary_or_archive_expr>.read_bytes()) across both engine and scripts.
    extra_binary_names = SCRIPT_ARCHIVE_EXPR_NAMES if is_scripts else None
    for fn_or_mod in [tree, *functions]:
        nodes = scope_nodes_map[id(fn_or_mod)]
        binary_byte_vars = set()
        for sub in nodes:
            if isinstance(sub, ast.Assign) and len(sub.targets) == 1 and isinstance(sub.targets[0], ast.Name):
                var_name = sub.targets[0].id
                if (isinstance(sub.value, ast.Call)
                        and isinstance(sub.value.func, ast.Attribute)
                        and sub.value.func.attr == 'read_bytes'
                        and _expr_references_binary(sub.value.func.value, extra_binary_names)):
                    binary_byte_vars.add(var_name)
                else:
                    binary_byte_vars.discard(var_name)
            elif is_hash_call(sub):
                payload = sub.args[0] if sub.args else next(
                    (kw.value for kw in sub.keywords if kw.arg == 'data'), None
                )
                if payload is None:
                    continue
                if isinstance(payload, ast.Name) and payload.id in binary_byte_vars:
                    report(sub, 'ML015', ML015_BINARY_HASH_MESSAGE)
                    continue
                for inner in ast.walk(payload):
                    if (isinstance(inner, ast.Call)
                            and isinstance(inner.func, ast.Attribute)
                            and inner.func.attr == 'read_bytes'
                            and _expr_references_binary(inner.func.value, extra_binary_names)):
                        report(inner, 'ML015', ML015_BINARY_HASH_MESSAGE)

    if is_scripts:
        return

    # ML006: User profile generation must not enforce a fixed wall-clock timeout wrapper.
    if filename == 'user_profiles.py' or (path_obj.parent.name == 'user_profiles' and filename == 'runner.py'):
        for node in functions:
            if isinstance(node, ast.AsyncFunctionDef) and node.name == 'generate':
                for sub in _iter_scope_nodes(node):
                    if not isinstance(sub, ast.Call):
                        continue
                    if (isinstance(sub.func, ast.Name) and sub.func.id in PROFILE_TIMEOUT_CALLS) or (
                        isinstance(sub.func, ast.Attribute)
                        and sub.func.attr in PROFILE_TIMEOUT_CALLS
                        and isinstance(sub.func.value, ast.Name)
                        and sub.func.value.id in base_asyncio
                    ):
                        report(sub, 'ML006', ML006_MESSAGE)

    # Same-module sync functions whose direct scope calls a blocking subprocess helper.
    sync_subprocess_helpers = set()
    for fn in functions:
        if isinstance(fn, ast.FunctionDef):
            env = scope_aliases(fn)
            for sub in env['nodes']:
                if isinstance(sub, ast.Call) and is_blocking_subprocess_func(sub.func, env):
                    sync_subprocess_helpers.add(fn.name)
                    break

    # Engine Python rules: ML013 (part 1) & ML014.
    for node in functions:
        if not isinstance(node, ast.AsyncFunctionDef):
            continue
        env = scope_aliases(node)
        scope_nodes = list(_iter_scope_nodes(
            node,
            skip_to_thread_lambda=True,
            to_thread_check=lambda c, e=env: offload_callable_index(c, e),
        ))
        # ML013 (part 1): asyncio.sleep(0 < N <= 5) inside while True loops.
        for sub in scope_nodes:
            if _is_while_true(sub):
                for wsub in _iter_scope_nodes(sub):
                    if is_asyncio_sleep(wsub, env):
                        delay = sleep_delay_seconds(wsub)
                        if delay is not None and 0 < delay <= 5:
                            report(wsub, 'ML013', ML013_ASYNC_POLL_MESSAGE)

        # ML014: Direct blocking subprocess calls or direct sync subprocess helper calls inside async def.
        awaited_calls = set()
        for sub in scope_nodes:
            if isinstance(sub, ast.Await) and isinstance(sub.value, ast.Call):
                awaited_calls.add(sub.value)
                wait_call = sub.value
                if (isinstance(wait_call.func, ast.Attribute)
                        and wait_call.func.attr == 'wait_for'
                        and isinstance(wait_call.func.value, ast.Name)
                        and wait_call.func.value.id in env['asyncio_names']
                        and wait_call.args
                        and isinstance(wait_call.args[0], ast.Call)):
                    awaited_calls.add(wait_call.args[0])

        for sub in scope_nodes:
            if not isinstance(sub, ast.Call):
                continue
            if is_blocking_subprocess_func(sub.func, env):
                is_sampler = (
                    (isinstance(sub.func, ast.Name) and sub.func.id in BLOCKING_SAMPLER_NAMES)
                    or (isinstance(sub.func, ast.Attribute) and sub.func.attr in BLOCKING_SAMPLER_NAMES)
                )
                if is_sampler and sub in awaited_calls:
                    continue
                report(sub, 'ML014', ML014_MESSAGE)
            elif (isinstance(sub.func, ast.Name) and sub.func.id in sync_subprocess_helpers) or (
                isinstance(sub.func, ast.Attribute)
                and sub.func.attr in sync_subprocess_helpers
                and isinstance(sub.func.value, ast.Name)
                and sub.func.value.id in {'self', 'cls'}
            ):
                report(sub, 'ML014', ML014_MESSAGE)

    # ML015 (part 2): In opencode_backlog.py, execute() must unlink owner_path in a live finally block.
    if filename == 'opencode_backlog.py':
        unlink_helpers = {
            fn.name for fn in functions
            if any(
                isinstance(sub, ast.Call) and isinstance(sub.func, ast.Attribute) and sub.func.attr == 'unlink'
                for sub in _walk_live_nodes(fn)
            )
        }
        for node in functions:
            if node.name == 'execute':
                try_with_finally = [sub for sub in _iter_scope_nodes(node) if isinstance(sub, ast.Try) and sub.finalbody]
                has_owner_unlink = False
                for try_node in try_with_finally:
                    for final_stmt in try_node.finalbody:
                        for sub in _walk_live_nodes(final_stmt):
                            if not isinstance(sub, ast.Call):
                                continue
                            if (isinstance(sub.func, ast.Attribute)
                                    and sub.func.attr == 'unlink'
                                    and any(isinstance(t, ast.Name) and t.id == 'owner_path' for t in ast.walk(sub.func.value))):
                                has_owner_unlink = True
                                break
                            if (isinstance(sub.func, ast.Name)
                                    and sub.func.id in unlink_helpers
                                    and any(
                                        any(isinstance(t, ast.Name) and t.id == 'owner_path' for t in ast.walk(arg))
                                        for arg in sub.args
                                    )):
                                has_owner_unlink = True
                                break
                if not has_owner_unlink:
                    anchor = try_with_finally[0].finalbody[0] if try_with_finally else node
                    report(anchor, 'ML015', ML015_OWNER_CLEANUP_MESSAGE)

    # ML016: Files with PRAGMA journal_mode=WAL (excluding docstrings) must also configure PRAGMA journal_size_limit.
    wal_hits = []
    has_size_limit = False
    for node in all_nodes:
        if isinstance(node, ast.Constant) and isinstance(node.value, str) and id(node) not in docstring_ids:
            if WAL_MODE_RE.search(node.value):
                wal_hits.append(node)
            if WAL_SIZE_LIMIT_RE.search(node.value):
                has_size_limit = True
        elif (isinstance(node, ast.Call)
              and isinstance(node.func, ast.Attribute)
              and node.func.attr in SQLITE_EXEC_METHODS
              and node.args):
            sql_text = _extract_sql_text(node.args[0])
            if sql_text is not None:
                if WAL_MODE_RE.search(sql_text):
                    wal_hits.append(node.args[0])
                if WAL_SIZE_LIMIT_RE.search(sql_text):
                    has_size_limit = True
    if wal_hits and not has_size_limit:
        seen_wal = set()
        for wal_node in wal_hits:
            key = (wal_node.lineno, wal_node.col_offset)
            if key not in seen_wal:
                seen_wal.add(key)
                report(wal_node, 'ML016', ML016_MESSAGE)


def _check_scaling_guardrails(tree, path, ast_index, report):
    """ML019 (/v2 list pagination & budget), ML020 (sqlite3.connect allowlist + no immutable=1), ML021 (zero-body catalog DDL)."""
    path_obj = Path(path)
    filename = path_obj.name
    docstring_ids = ast_index['docstring_ids']
    all_nodes = ast_index['all_nodes']
    functions = ast_index['functions']
    scope_nodes_map = ast_index['scope_nodes']
    scope_children_map = ast_index['scope_children']

    # ML020 (part 1): forbid online SQLite URI `immutable=1` outside docstrings.
    for node in all_nodes:
        if isinstance(node, ast.Constant) and isinstance(node.value, str) and id(node) not in docstring_ids:
            if IMMUTABLE_URI_RE.search(node.value):
                report(node, 'ML020', ML020_IMMUTABLE_MESSAGE)
        elif isinstance(node, (ast.JoinedStr, ast.BinOp)):
            sql = _extract_sql_text(node)
            if sql and IMMUTABLE_URI_RE.search(sql):
                report(node, 'ML020', ML020_IMMUTABLE_MESSAGE)

    # ML020 (part 2): restrict direct sqlite3.connect(...) calls to explicit allowlist.
    if filename not in SQLITE_CONNECT_ALLOWLIST:
        def walk_sqlite_scope(scope_root, base_sq, base_conn):
            params = _param_names(scope_root)
            sq_names = set(base_sq) - params
            conn_names = set(base_conn) - params
            nodes = scope_nodes_map[id(scope_root)]
            for node in nodes:
                if isinstance(node, ast.Import):
                    for alias in node.names:
                        if alias.name == 'sqlite3':
                            sq_names.add(alias.asname or alias.name)
                elif isinstance(node, ast.ImportFrom) and node.module == 'sqlite3':
                    for alias in node.names:
                        if alias.name == 'connect':
                            conn_names.add(alias.asname or alias.name)
            for node in nodes:
                if not isinstance(node, ast.Call):
                    continue
                is_sq_connect = (
                    (isinstance(node.func, ast.Name) and node.func.id in conn_names)
                    or (
                        isinstance(node.func, ast.Attribute)
                        and node.func.attr == 'connect'
                        and isinstance(node.func.value, ast.Name)
                        and node.func.value.id in sq_names
                    )
                )
                if is_sq_connect:
                    report(node, 'ML020', ML020_CONNECT_MESSAGE)
            for child_def in scope_children_map[id(scope_root)]:
                walk_sqlite_scope(child_def, sq_names, conn_names)

        walk_sqlite_scope(tree, {'sqlite3'}, set())

    # ML021: catalog_index.py DDL must never store body/raw_text/why_learned/metadata_json or catalog_sessions.
    if filename == 'catalog_index.py':
        for node in all_nodes:
            if id(node) in docstring_ids:
                continue
            if not isinstance(node, (ast.Constant, ast.JoinedStr, ast.BinOp)):
                continue
            sql = _extract_sql_text(node)
            if not sql or 'CREATE' not in sql.upper():
                continue
            for match in CREATE_TABLE_RE.finditer(sql):
                table_name = match.group(1).strip().lower()
                if table_name in CATALOG_FORBIDDEN_TABLES:
                    report(node, 'ML021', ML021_MESSAGE)
                    continue
                cols_body = match.group(2)
                for raw_line in cols_body.split(','):
                    tokens = raw_line.strip().split()
                    if not tokens:
                        continue
                    col_name = tokens[0].strip('"`[]').lower()
                    if col_name in CATALOG_FORBIDDEN_COLUMNS:
                        report(node, 'ML021', ML021_MESSAGE)

    # ML019: v2_api.py list handlers must enforce _parse_limit (max <= 50) & fit_items_to_json_budget.
    if filename == 'v2_api.py':
        numeric_consts = _collect_module_numeric_constants(tree)
        max_limit_val = numeric_consts.get('_MAX_PAGE_LIMIT')
        if max_limit_val is not None and not (1 <= max_limit_val <= 50):
            for node in tree.body:
                if isinstance(node, ast.Assign) and any(
                    isinstance(t, ast.Name) and t.id == '_MAX_PAGE_LIMIT' for t in node.targets
                ):
                    report(node, 'ML019', ML019_MESSAGE)

        fn_by_name = {fn.name: fn for fn in functions}
        for node in all_nodes:
            if not (isinstance(node, ast.Call) and isinstance(node.func, ast.Attribute) and node.func.attr == 'get'):
                continue
            if len(node.args) < 2:
                continue
            route_path = string(node.args[0])
            if not route_path or not route_path.startswith('/v2/'):
                continue
            if any(route_path.endswith(marker) for marker in V2_NON_LIST_ROUTE_MARKERS):
                continue
            handler_arg = node.args[1]
            if not isinstance(handler_arg, ast.Name) or handler_arg.id not in fn_by_name:
                continue
            handler_fn = fn_by_name[handler_arg.id]
            handler_calls = [n for n in ast.walk(handler_fn) if isinstance(n, ast.Call)]
            has_limit = False
            has_budget = False
            bad_limit_bound = False
            for call in handler_calls:
                fname = call.func.id if isinstance(call.func, ast.Name) else (
                    call.func.attr if isinstance(call.func, ast.Attribute) else ''
                )
                if fname == '_parse_limit':
                    has_limit = True
                    # Check explicit max_val on pagination limit (ignore days window parameter).
                    is_days_arg = bool(call.args and 'days' in ast.dump(call.args[0]))
                    if not is_days_arg:
                        for kw in call.keywords:
                            if kw.arg == 'max_val':
                                mv = _numeric_literal(kw.value, numeric_consts)
                                if mv is not None and mv > 50:
                                    bad_limit_bound = True
                elif fname == 'fit_items_to_json_budget':
                    has_budget = True
            if not has_limit or not has_budget or bad_limit_bound:
                report(handler_fn, 'ML019', ML019_MESSAGE)


def check_source(source, path):
    """Check model transport modules, including the shared configuration boundary."""
    try:
        tree = ast.parse(source, filename=str(path))
    except SyntaxError as error:
        return [(error.lineno, 'ML000', error.msg)]
    issues = []

    def report(node, code, message):
        issues.append((node.lineno, code, message))

    ast_index = _build_ast_index(tree)
    _check_model_boundaries(tree, path, ast_index, report)
    _check_environ_mutations(tree, path, ast_index, report)
    _check_runtime_hygiene(tree, path, ast_index, report)
    _check_scaling_guardrails(tree, path, ast_index, report)
    return sorted(set(issues))


ANIMATION_FRAME_ALLOWLIST = {'motion_tokens.go'}
PERF_FORBIDDEN_DRAW_CALLS = re.compile(
    r'\b(?:'
    r'http\.(?:Get|Post|PostForm|Head|NewRequest|NewRequestWithContext|DefaultClient\.Do)'
    r'|(?:hc|httpClient|client)\.Do'
    r'|os\.(?:ReadFile|WriteFile|Create|CreateTemp|Open|OpenFile|Stat|Lstat|ReadDir)'
    r'|time\.Sleep'
    r'|exec\.Command(?:Context)?'
    r')\b'
)
SHORT_AFTER_RE = re.compile(r'(?<!\btime)\.After\s*\(\s*(?:(\d+)\s*\*\s*)?time\.(Millisecond|Microsecond|Nanosecond)\b')
DRAW_CALLBACK_RE = re.compile(r'\.(?:Draw|DrawOver)\s*\(\s*func\s*\([^)]*\)\s*\{')
ANIMATION_FRAME_RE = re.compile(r'\.AnimationFrame\s*\(\s*\)')
GO_FUNC_HEADER_RE = re.compile(r'(?:^|\n)func\b[^{]*\{')
GO_VIEW_FUNC_RE = re.compile(r'func\s*\(\s*\w+\s+\*App\s*\)\s*View\s*\([^)]*\)\s*\{')
GO_LOAD_OVERVIEW_RE = re.compile(r'func\s*\(\s*\w+\s+\*App\s*\)\s*loadOverview\s*\([^)]*\)\s*\{')
GO_START_FUNC_RE = re.compile(r'func\s*\(\s*\w+\s+\*App\s*\)\s*Start\s*\([^)]*\)\s*\{')
GO_OVERVIEW_DOCS_CALL_RE = re.compile(r'\.\s*overviewDocuments\s*\(')
GO_OVERVIEW_SESSIONS_CALL_RE = re.compile(r'\.\s*overviewSessions\s*\(')
GO_V1_OVERVIEW_BOOTSTRAP_RE = re.compile(r'/v1/overview/bootstrap')
ML018_LEGACY_OVERVIEW_DOCS_MAX = {
    'overview.go': 1,
    'common_knowledge.go': 1,
    'common_knowledge_page.go': 1,
    'knowledge_waterfall.go': 1,
    'project_queue.go': 1,
    'project_records.go': 1,
    'unified_search.go': 1,
}
ML018_LEGACY_OVERVIEW_SESSIONS_MAX = {'overview.go': 1}
ML018_LEGACY_V1_BOOTSTRAP_MAX = {'overview_bootstrap.go': 1}


def _strip_go_comments_keep_strings(source):
    """Mask Go // and /* */ comments while preserving string literals and line numbers."""
    out = []
    i = 0
    n = len(source)
    while i < n:
        ch = source[i]
        if ch == '"':
            out.append(ch)
            i += 1
            escaped = False
            while i < n:
                c = source[i]
                out.append(c)
                i += 1
                if escaped:
                    escaped = False
                elif c == '\\':
                    escaped = True
                elif c == '"' or c == '\n':
                    break
        elif ch == '`':
            out.append(ch)
            i += 1
            while i < n:
                c = source[i]
                out.append(c)
                i += 1
                if c == '`':
                    break
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


def _extract_brace_block(source, open_brace_index):
    depth = 0
    in_str = None
    escaped = False
    for idx in range(open_brace_index, len(source)):
        ch = source[idx]
        if in_str is not None:
            if in_str in ('"', "'"):
                if escaped:
                    escaped = False
                elif ch == '\\':
                    escaped = True
                elif ch == in_str:
                    in_str = None
            elif ch == '`':
                in_str = None
            continue
        if ch in ('"', "'", '`'):
            in_str = ch
        elif ch == '{':
            depth += 1
        elif ch == '}':
            depth -= 1
            if depth == 0:
                return source[open_brace_index:idx + 1]
    return source[open_brace_index:]


def _strip_go_comments(source):
    """Mask Go comments, rune literals, strings, and raw strings with spaces while preserving line numbers."""
    out = []
    i = 0
    n = len(source)
    while i < n:
        ch = source[i]
        if ch == '"':
            out.append('"')
            i += 1
            escaped = False
            while i < n:
                c = source[i]
                if escaped:
                    out.append('\n' if c == '\n' else ' ')
                    escaped = False
                    i += 1
                elif c == '\\':
                    out.append(' ')
                    escaped = True
                    i += 1
                elif c == '"':
                    out.append('"')
                    i += 1
                    break
                elif c == '\n':
                    out.append('\n')
                    i += 1
                    break
                else:
                    out.append(' ')
                    i += 1
        elif ch == "'":
            out.append("'")
            i += 1
            escaped = False
            while i < n:
                c = source[i]
                if escaped:
                    out.append('\n' if c == '\n' else ' ')
                    escaped = False
                    i += 1
                elif c == '\\':
                    out.append(' ')
                    escaped = True
                    i += 1
                elif c == "'":
                    out.append("'")
                    i += 1
                    break
                elif c == '\n':
                    out.append('\n')
                    i += 1
                    break
                else:
                    out.append(' ')
                    i += 1
        elif ch == '`':
            out.append('`')
            i += 1
            while i < n:
                c = source[i]
                if c == '`':
                    out.append('`')
                    i += 1
                    break
                out.append('\n' if c == '\n' else ' ')
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


def _enclosing_go_func_body(clean, pos):
    last_block = None
    for match in GO_FUNC_HEADER_RE.finditer(clean, 0, pos + 1):
        brace_idx = match.end() - 1
        block = _extract_brace_block(clean, brace_idx)
        if brace_idx <= pos < brace_idx + len(block):
            last_block = (brace_idx, block)
    if last_block is not None:
        return last_block
    return 0, clean


def _has_inverted_reduce_motion_guard(func_body, call_offset):
    for match in re.finditer(r'\bif\b[^{]*\bReduceMotion\b[^{]*\{', func_body):
        cond = match.group(0)
        if '!' in cond:
            continue
        brace_idx = match.end() - 1
        block = _extract_brace_block(func_body, brace_idx)
        if brace_idx <= call_offset < brace_idx + len(block):
            if not re.search(r'\breturn\b', block):
                return True
    return False


def _inside_early_return_block(head, match_pos):
    innermost = None
    for idx, ch in enumerate(head[:match_pos + 1]):
        if ch == '{':
            block = _extract_brace_block(head, idx)
            if idx <= match_pos < idx + len(block):
                innermost = block
    if innermost is None:
        return False
    return bool(re.search(r'\breturn\b', innermost))


def check_go_performance(source, path):
    """Static performance guardrails for Native UI frame rate, idle CPU, and cold start."""
    filename = Path(path).name
    clean = _strip_go_comments(source)
    issues = []

    # ML008: AnimationFrame() is restricted to motion_tokens.go and must honor ReduceMotion.
    for match in ANIMATION_FRAME_RE.finditer(clean):
        line = clean[:match.start()].count('\n') + 1
        if filename not in ANIMATION_FRAME_ALLOWLIST:
            issues.append((line, 'ML008', ML008_ALLOW_MESSAGE))
        else:
            brace_idx, func_body = _enclosing_go_func_body(clean, match.start())
            rel_offset = match.start() - brace_idx
            if not re.search(r'\bReduceMotion\b', func_body) or _has_inverted_reduce_motion_guard(func_body, rel_offset):
                issues.append((line, 'ML008', ML008_GUARD_MESSAGE))

    # ML009: Reject high-frequency c.After (<500ms) polling while ignoring stdlib time.After.
    for match in SHORT_AFTER_RE.finditer(clean):
        amount = int(match.group(1)) if match.group(1) else 1
        unit = match.group(2)
        ms = amount if unit == 'Millisecond' else (amount / 1000.0 if unit == 'Microsecond' else amount / 1e6)
        if ms < 500:
            line = clean[:match.start()].count('\n') + 1
            issues.append((line, 'ML009', ML009_MESSAGE))

    # ML010: Paint callbacks (Draw / DrawOver) must never block on I/O, sleep, or network.
    for match in DRAW_CALLBACK_RE.finditer(clean):
        brace_idx = match.end() - 1
        block = _extract_brace_block(clean, brace_idx)
        for bad in PERF_FORBIDDEN_DRAW_CALLS.finditer(block):
            line = clean[:brace_idx + bad.start()].count('\n') + 1
            issues.append((line, 'ML010', ML010_MESSAGE))

    # ML011: Reject blocking I/O, sleep, or network calls on App.View hot path.
    if filename == 'app.go':
        view_match = GO_VIEW_FUNC_RE.search(clean)
        if view_match:
            brace_idx = view_match.end() - 1
            view_body = _extract_brace_block(clean, brace_idx)
            for bad in PERF_FORBIDDEN_DRAW_CALLS.finditer(view_body):
                line = clean[:brace_idx + bad.start()].count('\n') + 1
                issues.append((line, 'ML011', ML011_MESSAGE))

    # ML012: Cold-start Overview performance & single-source guardrails.
    for match in re.finditer(r'\b(overview-ui-cache|hydrateOverviewFromCache|saveOverviewCache)\b', clean):
        line = clean[:match.start()].count('\n') + 1
        issues.append((line, 'ML012', ML012_CACHE_MESSAGE))
    if filename == 'overview_ui.go':
        load_match = GO_LOAD_OVERVIEW_RE.search(clean)
        if load_match:
            brace_idx = load_match.end() - 1
            load_body = _extract_brace_block(clean, brace_idx)
            if 'overviewBootstrap(' not in load_body:
                line = clean[:load_match.start()].count('\n') + 1
                issues.append((line, 'ML012', ML012_OVERVIEW_MESSAGE))
    if filename == 'app.go':
        start_match = GO_START_FUNC_RE.search(clean)
        if start_match:
            brace_idx = start_match.end() - 1
            start_body = _extract_brace_block(clean, brace_idx)
            tail_idx = start_body.find('a.RefreshProjectDirectory()')
            if tail_idx >= 0:
                head = start_body[:tail_idx]
                tail = start_body[tail_idx:]
                if 'RefreshIntegrations()' in tail and 'AfterFunc' not in tail:
                    line = clean[:brace_idx + tail_idx].count('\n') + 1
                    issues.append((line, 'ML012', ML012_START_MESSAGE))
                inner_head = head[1:]  # exclude outer Start() opening brace
                for call_match in re.finditer(r'\bRefreshIntegrations\s*\(', inner_head):
                    if not _inside_early_return_block(inner_head, call_match.start()) and 'AfterFunc' not in inner_head:
                        line = clean[:brace_idx + 1 + call_match.start()].count('\n') + 1
                        issues.append((line, 'ML012', ML012_START_MESSAGE))

    # ML018: Ban new Go call sites to legacy full-table endpoints (overviewDocuments, overviewSessions, /v1/overview/bootstrap).
    docs_matches = list(GO_OVERVIEW_DOCS_CALL_RE.finditer(clean))
    max_docs = ML018_LEGACY_OVERVIEW_DOCS_MAX.get(filename, 0)
    for match in docs_matches[max_docs:]:
        line = clean[:match.start()].count('\n') + 1
        issues.append((line, 'ML018', ML018_MESSAGE))

    sess_matches = list(GO_OVERVIEW_SESSIONS_CALL_RE.finditer(clean))
    max_sess = ML018_LEGACY_OVERVIEW_SESSIONS_MAX.get(filename, 0)
    for match in sess_matches[max_sess:]:
        line = clean[:match.start()].count('\n') + 1
        issues.append((line, 'ML018', ML018_MESSAGE))

    no_comments = _strip_go_comments_keep_strings(source)
    v1_boot_matches = list(GO_V1_OVERVIEW_BOOTSTRAP_RE.finditer(no_comments))
    max_v1_boot = ML018_LEGACY_V1_BOOTSTRAP_MAX.get(filename, 0)
    for match in v1_boot_matches[max_v1_boot:]:
        line = no_comments[:match.start()].count('\n') + 1
        issues.append((line, 'ML018', ML018_MESSAGE))

    return issues


def check_go_source(source, path):
    """Performance guardrails for Native UI; presentation behavior is covered by Go UI tests."""
    return sorted(set(check_go_performance(source, path)))


def check_markdown_doc(source, path=''):
    """Enforce self-contained formal docs without references to gitignored docs/process/."""
    _ = path
    issues = []
    for line_no, line in enumerate(source.splitlines(), start=1):
        if DOCS_PROCESS_REF_RE.search(line):
            issues.append((line_no, 'ML018', ML018_MESSAGE))
    return issues


def iter_formal_markdown_docs(docs_dir):
    if not docs_dir.is_dir():
        return
    paths = []
    for entry in docs_dir.iterdir():
        if entry.is_symlink():
            continue
        if entry.is_dir():
            if entry.name in ('process', 'archive', '__pycache__'):
                continue
            for sub in entry.rglob('*.md'):
                if not sub.is_symlink() and sub.is_file():
                    paths.append(sub)
        elif entry.is_file() and entry.suffix.lower() == '.md' and entry.name != 'README.md':
            paths.append(entry)
    yield from sorted(paths)


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
    for path in iter_formal_markdown_docs(ROOT / 'docs'):
        for line, code, message in check_markdown_doc(path.read_text(encoding='utf-8'), path):
            print('{}:{}: {} {}'.format(path.relative_to(ROOT), line, code, message))
            failures += 1
    if not failures:
        print('Lint passed: 模型参数、环境隔离、UI 性能、运行时资源与扩展架构门禁 (ML001-ML003, ML007-ML012, ML013-ML021)')
    return bool(failures)


if __name__ == '__main__':
    sys.exit(main())
