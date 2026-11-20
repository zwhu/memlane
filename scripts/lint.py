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
        print('Lint passed: 模型参数、环境隔离与 UI 性能门禁 (ML001-ML003, ML007-ML011)')
    return bool(failures)


if __name__ == '__main__':
    sys.exit(main())
