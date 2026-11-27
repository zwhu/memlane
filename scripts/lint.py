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
ENGINES_COGNEE_ENVIRON_ALLOWLIST = {
    'engines/cognee/runner.py', 'engines/cognee/handoff.py',
    'engines/cognee/probe.py', 'engines/cognee/first_install_store.py',
}
ENVIRON_MUTATING_METHODS = {'update', 'pop', 'popitem', 'clear', 'setdefault', '__setitem__', '__delitem__'}
ML001_MESSAGE = '使用共享 native_llm_configuration，禁止独立构造 LLMConfig'
ML002_LITERAL_MESSAGE = '模型参数不得注入字面量默认值；从明确配置读取，未配置时省略'
ML003_OR_MESSAGE = '模型参数不得用 or 补回退值；显式0必须保留'
ML003_FALLBACK_MESSAGE = '模型参数不得使用 get/setdefault/pop 非 None 回退值；保留未配置状态'
ML007_MESSAGE = '禁止在非白名单模块修改全局 os.environ；请显式传递环境变量字典或限定为白名单入口'
ML008_ALLOW_MESSAGE = '禁止在非动效模块直接调用 AnimationFrame()；集中到 motion_tokens.go 并受 ReduceMotion 守护'
ML008_GUARD_MESSAGE = 'AnimationFrame() 所在动效函数必须检查 ReduceMotion，禁止空闲无限刷帧'
ML009_MESSAGE = '禁止使用低于 500ms 的 c.After 高频轮询重绘，避免主线程空转耗电'
ML010_MESSAGE = 'Draw/DrawOver 渲染回调禁止执行磁盘 I/O、休眠或网络请求，确保高帧率绘制'
ML011_MESSAGE = 'App.View 渲染热路径禁止同步磁盘 I/O、休眠或网络调用'
ML012_CACHE_MESSAGE = '禁止在桌面端落盘 SWR 首页缓存副本；首页冷启动须走控制面单次只读聚合接口 overviewBootstrap'
ML012_OVERVIEW_MESSAGE = 'loadOverview 必须优先走控制面单次只读聚合接口 overviewBootstrap，禁止冷启动直接扇出多库 HTTP'
ML012_START_MESSAGE = 'App.Start() 首页冷启动必须延迟 RefreshIntegrations()，避免子进程探测与首屏加载争抢帧预算'
ML015_BINARY_HASH_MESSAGE = '禁止对可执行二进制直接调用 read_bytes() 全量读入内存哈希；请使用基于 stat 的缓存与流式分块摘要'
ML016_MESSAGE = '启用 SQLite WAL 模式时必须在同一模块显式配置 PRAGMA journal_size_limit，防止 WAL 文件无限膨胀'
ML018_MESSAGE = '正式文档禁止引用本地忽略的 docs/process/ 路径；请将长期结论自包含写入正文，必要静态图移入 docs/images/'
BINARY_EXPR_NAMES = {'node', 'exe', 'executable', 'binary', 'bin_path', 'node_executable', 'node_path', 'node_bin'}
SCRIPT_ARCHIVE_EXPR_NAMES = {'archive', 'tarball', 'dmg', 'installer', 'bundle'}
WAL_MODE_RE = re.compile(r"pragma\s+(?:\w+\.)?journal_mode\s*=\s*['\"]?wal['\"]?\b", re.IGNORECASE)
WAL_SIZE_LIMIT_RE = re.compile(r'pragma\s+(?:\w+\.)?journal_size_limit\b', re.IGNORECASE)
DOCS_PROCESS_REF_RE = re.compile(
    r'(?<![A-Za-z0-9_-])docs/process(?:/|(?![A-Za-z0-9_-]))'
    r'|(?<![A-Za-z0-9_-])(?:\.\./)+process(?:/|(?![A-Za-z0-9_-]))'
    r'|\]\(\s*\.?/?process(?:/|\s*\))'
)


COGNEE_LOC_LIMIT = 3500
FORBIDDEN_IMPORTS = {'engine', 'sqlite3', 'aiosqlite'}
HASH_NAMES = set('new md5 sha1 sha224 sha256 sha384 sha512 sha3_224 sha3_256 sha3_384 sha3_512 shake_128 shake_256 blake2b blake2s'.split())
SCOPE_TYPES = (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef, ast.Lambda)


def name(node):
    if isinstance(node, ast.Name):
        return node.id
    if isinstance(node, ast.Attribute):
        return node.attr
    if isinstance(node, ast.Constant) and isinstance(node.value, str):
        return node.value
    if isinstance(node, ast.Subscript):
        return name(node.slice)
    return None


def assignments(node):
    if isinstance(node, ast.Assign):
        return [(target, node.value) for target in node.targets]
    if isinstance(node, (ast.AnnAssign, ast.NamedExpr)) and node.value is not None:
        return [(node.target, node.value)]
    return []


def scope_nodes(scope):
    pending = list(ast.iter_child_nodes(scope))
    while pending:
        node = pending.pop()
        yield node
        if not isinstance(node, SCOPE_TYPES):
            pending.extend(ast.iter_child_nodes(node))


def aliases(pairs, known, matches):
    while True:
        new = {target.id for target, value in pairs if isinstance(target, ast.Name) and matches(value)}
        if new <= known:
            return
        known.update(new)


def model_value(key, value, report):
    if key not in PARAMETERS or value is None:
        return
    if isinstance(value, ast.Constant) and value.value is None:
        return
    try:
        ast.literal_eval(value)
    except (ValueError, TypeError):
        if (isinstance(value, ast.BinOp)
                and all(isinstance(node, (ast.BinOp, ast.UnaryOp, ast.operator, ast.unaryop))
                        or isinstance(node, ast.Constant) and type(node.value) in (int, float)
                        for node in ast.walk(value))):
            report(value, 'ML002', ML002_LITERAL_MESSAGE)
    else:
        if name(value) not in PARAMETERS:
            report(value, 'ML002', ML002_LITERAL_MESSAGE)
    if (isinstance(value, ast.BoolOp) and isinstance(value.op, ast.Or)
            or isinstance(value, ast.IfExp) and not isinstance(value.test, ast.Compare)
            and ast.dump(value.test) == ast.dump(value.body)):
        report(value, 'ML003', ML003_OR_MESSAGE)


def check_engine(tree, path, report):
    factory = next((node for node in tree.body if isinstance(node, ast.FunctionDef)
                    and node.name == 'native_llm_configuration'), None)
    allowed = set(scope_nodes(factory)) if path == 'engines/cognee/llm_config.py' and factory else set()

    def visit(scope, constructors, modules, environ, mutators):
        nodes = list(scope_nodes(scope))
        pairs = [pair for node in nodes for pair in assignments(node)]
        pairs.extend((node, base) for node in nodes if isinstance(node, ast.ClassDef) for base in node.bases)
        constructors, modules, environ, mutators = map(set, (constructors, modules, environ, mutators))
        if isinstance(scope, (ast.FunctionDef, ast.AsyncFunctionDef, ast.Lambda)):
            args = scope.args
            positional = args.posonlyargs + args.args
            for arg, value in list(zip(positional[len(positional) - len(args.defaults):], args.defaults)) + list(
                    zip(args.kwonlyargs, args.kw_defaults)):
                model_value(arg.arg, value, report)
            params = {arg.arg for arg in positional + args.kwonlyargs + [args.vararg, args.kwarg] if arg}
            for known in (constructors, modules, environ, mutators):
                known.difference_update(params)
        for node in nodes:
            if isinstance(node, ast.Import):
                for item in node.names:
                    imported = item.name.split('.')[0]
                    if imported in FORBIDDEN_IMPORTS:
                        report(node, 'ML023', f'Cognee adapter imports forbidden module {item.name}')
                    if item.name == 'os':
                        modules.add(item.asname or item.name)
            elif isinstance(node, ast.ImportFrom) and node.level == 0:
                if (node.module or '').split('.')[0] in FORBIDDEN_IMPORTS:
                    report(node, 'ML023', f'Cognee adapter imports forbidden module {node.module}')
                for item in node.names:
                    local = item.asname or item.name
                    if item.name == 'LLMConfig':
                        constructors.add(local)
                    if node.module == 'os' and item.name in {'environ', 'environb', 'putenv', 'unsetenv'}:
                        (environ if item.name.startswith('environ') else mutators).add(local)

        def is_constructor(expr):
            return (isinstance(expr, ast.Name) and expr.id in constructors
                    or isinstance(expr, ast.Attribute) and expr.attr == 'LLMConfig')

        def is_environ(expr):
            return (isinstance(expr, ast.Name) and expr.id in environ
                    or isinstance(expr, ast.Attribute) and expr.attr in {'environ', 'environb'}
                    and isinstance(expr.value, ast.Name) and expr.value.id in modules)

        def is_env_mutator(expr):
            return (isinstance(expr, ast.Name) and expr.id in mutators
                    or isinstance(expr, ast.Attribute) and (
                        expr.attr in ENVIRON_MUTATING_METHODS and is_environ(expr.value)
                        or expr.attr in {'putenv', 'unsetenv'} and isinstance(expr.value, ast.Name)
                        and expr.value.id in modules))

        while True:
            previous = len(constructors)
            aliases(pairs, constructors, is_constructor)
            constructors.update(target.name for target, value in pairs
                                if isinstance(target, ast.ClassDef) and is_constructor(value))
            if len(constructors) == previous:
                break
        aliases(pairs, environ, is_environ)
        aliases(pairs, mutators, is_env_mutator)
        for node in nodes:
            if isinstance(node, ast.Call):
                if is_constructor(node.func):
                    if not (node in allowed and not node.args and len(node.keywords) == 1
                            and node.keywords[0].arg is None and isinstance(node.keywords[0].value, ast.Call)
                            and isinstance(node.keywords[0].value.func, ast.Name)
                            and node.keywords[0].value.func.id == 'native_llm_kwargs'):
                        report(node, 'ML001', ML001_MESSAGE)
                if (isinstance(node.func, ast.Attribute) and node.func.attr in {'get', 'setdefault', 'pop'}
                        and len(node.args) >= 2 and isinstance(node.args[0], ast.Constant)
                        and name(node.args[0]) in PARAMETERS
                        and not (isinstance(node.args[1], ast.Constant) and node.args[1].value is None)):
                    report(node, 'ML003', ML003_FALLBACK_MESSAGE)
                for kw in node.keywords:
                    model_value(kw.arg, kw.value, report)
            elif isinstance(node, ast.Dict):
                for key, value in zip(node.keys, node.values):
                    if isinstance(key, ast.Constant):
                        model_value(name(key), value, report)
            for target, value in assignments(node):
                model_value(name(target), value, report)
            if path not in ENGINES_COGNEE_ENVIRON_ALLOWLIST:
                if isinstance(node, ast.Call) and is_env_mutator(node.func):
                    report(node, 'ML007', ML007_MESSAGE)
                if (isinstance(node, (ast.Name, ast.Subscript, ast.Attribute))
                        and isinstance(node.ctx, (ast.Store, ast.Del))
                        and (isinstance(node, ast.Subscript) and is_environ(node.value)
                             or isinstance(node, ast.Attribute) and is_environ(node)
                             or isinstance(node, ast.Name) and is_environ(node)
                             and any(isinstance(stmt, ast.AugAssign) and stmt.target is node for stmt in nodes))):
                    report(node, 'ML007', ML007_MESSAGE)
            if isinstance(node, SCOPE_TYPES):
                visit(node, constructors, modules, environ, mutators)

    visit(tree, {'LLMConfig'}, {'os'}, set(), set())


def check_resources(tree, is_script, report):
    all_nodes = list(ast.walk(tree))
    hash_modules = {'hashlib'}
    hash_functions = set()
    for node in all_nodes:
        if isinstance(node, ast.Import):
            hash_modules.update(item.asname or item.name for item in node.names if item.name == 'hashlib')
        if isinstance(node, ast.ImportFrom) and node.module == 'hashlib':
            hash_functions.update(item.asname or item.name for item in node.names if item.name in HASH_NAMES)
    binary_names = BINARY_EXPR_NAMES | (SCRIPT_ARCHIVE_EXPR_NAMES if is_script else set())
    archive_suffix = ('.dmg', '.pkg', '.zip', '.tar', '.tgz', '.gz', '.xz') if is_script else ()

    def binary_part(part):
        candidate = (name(part) or '').lower()
        return (candidate in binary_names or not binary_names.isdisjoint(candidate.split('_'))
                or candidate.endswith(archive_suffix))

    def is_binary_read(node):
        return (isinstance(node, ast.Call) and isinstance(node.func, ast.Attribute)
                and node.func.attr == 'read_bytes' and any(map(binary_part, ast.walk(node.func.value))))

    def is_hash(expr):
        return (isinstance(expr, ast.Name) and expr.id in hash_functions
                or isinstance(expr, ast.Attribute) and isinstance(expr.value, ast.Name)
                and expr.value.id in hash_modules and expr.attr in HASH_NAMES)

    for scope in [tree, *(node for node in all_nodes if isinstance(node, SCOPE_TYPES))]:
        nodes = list(scope_nodes(scope))
        pairs = [pair for node in nodes for pair in assignments(node)]
        aliases(pairs, hash_functions, is_hash)
        binary_bytes = set()
        aliases(pairs, binary_bytes, lambda value: is_binary_read(value)
                or isinstance(value, ast.Name) and value.id in binary_bytes)
        for node in nodes:
            if isinstance(node, ast.Call) and is_hash(node.func):
                for arg in node.args + [kw.value for kw in node.keywords]:
                    if any(is_binary_read(part) or isinstance(part, ast.Name) and part.id in binary_bytes
                           for part in ast.walk(arg)):
                        report(node, 'ML015', ML015_BINARY_HASH_MESSAGE)
                        break

    docstrings = {node.body[0].value for node in all_nodes
                  if isinstance(node, (ast.Module, ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef))
                  and node.body and isinstance(node.body[0], ast.Expr)}
    strings = [(node, node.value) for node in all_nodes if node not in docstrings
               and isinstance(node, ast.Constant) and isinstance(node.value, str)]
    if not any(WAL_SIZE_LIMIT_RE.search(value) for _, value in strings):
        for node, value in strings:
            if WAL_MODE_RE.search(value):
                report(node, 'ML016', ML016_MESSAGE)


def check_source(source, path):
    try:
        tree = ast.parse(source, filename=str(path))
    except SyntaxError as error:
        return [(error.lineno, 'ML000', error.msg)]
    issues = []

    def report(node, code, message):
        issues.append((node.lineno, code, message))

    path = Path(path)
    if path.is_absolute() and path.is_relative_to(ROOT):
        path = path.relative_to(ROOT)
    path = path.as_posix()
    is_script = path.startswith('scripts/')
    if not is_script:
        check_engine(tree, path, report)
    check_resources(tree, is_script, report)
    return sorted(set(issues))


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


def _extract_brace_block(clean, start):
    depth = 0
    for idx in range(start, len(clean)):
        if clean[idx] == '{':
            depth += 1
        elif clean[idx] == '}':
            depth -= 1
            if depth == 0:
                return clean[start:idx + 1]
    return clean[start:]


GO_HIDDEN = re.compile(r'//[^\n]*|/\*[\s\S]*?(?:\*/|$)|`[^`]*(?:`|$)|"(?:\\[^\n]|[^"\\\n])*(?:"|(?=\n|$))|\'(?:\\[^\n]|[^\'\\\n])*(?:\'|(?=\n|$))')


def _strip_go_comments(source):
    """Preserve offsets and line numbers while masking comments and literal contents."""
    def mask(match):
        token = match.group()
        if token[0] in ('"', "'", '`'):
            end = token[-1] == token[0] and len(token) > 1
            body = token[1:-1] if end else token[1:]
            return token[0] + re.sub(r'[^\n]', ' ', body) + (token[-1] if end else '')
        return re.sub(r'[^\n]', ' ', token)

    return GO_HIDDEN.sub(mask, source)


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


def _in_block(source, pattern, offset, predicate):
    for match in re.finditer(pattern, source[:offset]):
        start = match.end() - 1
        body = _extract_brace_block(source, start)
        if offset < start + len(body) and predicate(body):
            return True
    return False


def check_go_source(source, path):
    clean = _strip_go_comments(source)
    issues = []

    def report(pos, code, message):
        issues.append((source.count('\n', 0, pos) + 1, code, message))

    for match in ANIMATION_FRAME_RE.finditer(clean):
        pos = match.start()
        if Path(path).name != 'motion_tokens.go':
            report(pos, 'ML008', ML008_ALLOW_MESSAGE)
        else:
            brace, body = _enclosing_go_func_body(clean, pos)
            if not re.search(r'\bReduceMotion\b', body) or _has_inverted_reduce_motion_guard(body, pos - brace):
                report(pos, 'ML008', ML008_GUARD_MESSAGE)
    divisor = {'Millisecond': 1, 'Microsecond': 1000, 'Nanosecond': 1_000_000}
    for match in SHORT_AFTER_RE.finditer(clean):
        amount, unit = match.groups()
        if int(amount or 1) < 500 * divisor[unit]:
            report(match.start(), 'ML009', ML009_MESSAGE)
    for pattern, code, message in ((DRAW_CALLBACK_RE, 'ML010', ML010_MESSAGE),
                                    (GO_VIEW_FUNC_RE, 'ML011', ML011_MESSAGE)):
        for match in pattern.finditer(clean):
            start = match.end() - 1
            for call in PERF_FORBIDDEN_DRAW_CALLS.finditer(_extract_brace_block(clean, start)):
                report(start + call.start(), code, message)
    for token in GO_HIDDEN.finditer(source):
        if token.group()[0] in ('"', '`') and 'overview-ui-cache' in token.group():
            report(token.start(), 'ML012', ML012_CACHE_MESSAGE)
    for match in re.finditer(r'\b(hydrateOverviewFromCache|saveOverviewCache)\b', clean):
        report(match.start(), 'ML012', ML012_CACHE_MESSAGE)
    for match in GO_LOAD_OVERVIEW_RE.finditer(clean):
        body = _extract_brace_block(clean, match.end() - 1)
        if not re.search(r'\boverviewBootstrap\s*\(', body):
            report(match.start(), 'ML012', ML012_OVERVIEW_MESSAGE)
    for match in GO_START_FUNC_RE.finditer(clean):
        start = match.end() - 1
        body = _extract_brace_block(clean, start)
        for call in re.finditer(r'\bRefreshIntegrations\s*\(', body):
            offset = call.start()
            if _in_block(body, r'\bAfterFunc\s*\([^{}]*\bfunc\s*\([^)]*\)\s*\{', offset, bool):
                continue
            if _in_block(body, r'\bif\b[^{}]*\{', offset,
                         lambda block: re.search(r'\breturn\s*}\s*$', block)):
                continue
            report(start + offset, 'ML012', ML012_START_MESSAGE)
    return sorted(set(issues))


def check_markdown_doc(source, path=''):
    """Enforce self-contained formal docs without references to gitignored docs/process/."""
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


def main(root=ROOT):
    failures = 0

    def scan(path, checker):
        source = path.read_text(encoding='utf-8')
        rel = path.relative_to(root).as_posix()
        nonlocal failures
        for line, code, message in checker(source, rel):
            print(f'{rel}:{line}: {code} {message}')
            failures += 1
        return len(source.splitlines())

    cognee = root / 'engines/cognee'
    loc = sum(
        scan(path, check_source)
        for path in sorted(cognee.rglob('*.py'))
        if not path.name.startswith('test_')
        and '__pycache__' not in path.parts
        and not path.is_symlink()
        and path.is_file()
    )
    if loc > COGNEE_LOC_LIMIT:
        print(f'engines/cognee:1: ML023 {loc} non-test LOC exceeds {COGNEE_LOC_LIMIT}')
        failures += 1
    for path in sorted((root / 'scripts').glob('*.py')):
        if not path.name.startswith('test_') and path.name != 'lint.py':
            scan(path, check_source)
    for directory in ('kit/ui', 'app'):
        go_dir = root / directory
        if go_dir.is_dir():
            for path in sorted(go_dir.rglob('*.go')):
                if not path.name.endswith('_test.go'):
                    scan(path, check_go_source)
    for path in iter_formal_markdown_docs(root / 'docs'):
        scan(path, check_markdown_doc)
    if not failures:
        print('Lint passed (ML001–ML003, ML007–ML012, ML015, ML016, ML018, ML023)')
    return bool(failures)


if __name__ == '__main__':
    sys.exit(main())
