#!/usr/bin/env python3
"""Diff-scoped guardrail against silent error swallowing, temporary hacks, and scope drift.

Designed for Memlane's Swarm + check.py + pre-commit workflow:
- Inspects ONLY added/changed lines in Tier-A production code (Python & Go).
- Blocks high-confidence anti-patterns (DG001-DG006) with zero-dependency stdlib AST/tokenizer analysis.
- Prints a compact audit summary (new try/except, fallbacks, sleep calls, allow-guard exemptions,
  and new imports) so reviewers can strip unnecessary defensive code without manual hunting.
"""
from __future__ import annotations

import argparse
import ast
from fnmatch import fnmatch
import io
import json
from pathlib import Path
import re
import subprocess
import sys
import tokenize
from typing import Any


ROOT = Path(__file__).resolve().parents[1]
MODULE_PREFIX = 'github.com/zwhu/memlane'

# Scripts that belong to Tier-B test/verification scaffolds rather than Tier-A production code.
TIER_B_SCRIPTS = {'scripts/diff_guard.py', 'scripts/lint.py'}

ALLOW_GUARD_RE = re.compile(r'memlane:allow-guard\(([^)]*)\)')

TEMP_COMMENT_RE = re.compile(
    r'(?i)(?:\b(?:TODO|FIXME|HACK|XXX|WORKAROUND)\b'
    r'|\btemp(?:orary)?\s+(?:fix|hack|patch|workaround|fallback|compat)\b'
    r'|临时补丁|临时兜底|临时方案|临时绕过|先凑合)'
)

FALLBACK_AUDIT_RE = re.compile(
    r'(?i)(?:(?<![a-z])(?:fallback|workaround|compat|legacy)(?![a-z])|\btime\.sleep\s*\()'
)

GO_EMPTY_ERR_RE = re.compile(r'\bif\s+err\s*!=\s*nil\s*\{\s*\}')
GO_ERR_BLOCK_START_RE = re.compile(r'\bif\s+err\s*!=\s*nil\s*\{')
GO_SWALLOW_STMT_RE = re.compile(r'^\s*(?:continue|break|return\s*(?:nil|false|""|0)?)\s*$')
GO_TOKEN_RE = re.compile(
    r'(?P<trivia>\s+|//[^\n]*|/\*[\s\S]*?(?:\*/|$))'
    r'|(?P<string>"(?:\\.|[^"\\\n])*"|\x60[^\x60]*\x60)'
    r"|(?P<rune>'(?:\\.|[^'\\\n])*')"
    r'|(?P<word>\w+)|(?P<symbol>.)'
)
GO_IMPORT_ESCAPE_RE = re.compile(r'\\(x[0-9a-fA-F]{2}|u[0-9a-fA-F]{4}|U[0-9a-fA-F]{8}|[0-7]{3}|.)')
PY_IMPORT_LINE_RE = re.compile(r'^\s*(?:import\s+\S|from\s+\S+\s+import\s+)')
HUNK_HEADER_RE = re.compile(r'^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@')

LOG_OR_SIGNAL_CALL_NAMES = {
    'debug', 'info', 'warning', 'warn', 'error', 'exception', 'critical', 'fatal',
    'log', 'print', 'write', 'record', 'emit', 'notify', 'append', 'add', 'set',
    'fail', 'abort', 'exit', 'cancel', 'close',
}


def normalize_rel(path: str) -> str:
    value = path.replace('\\', '/').strip()
    while value.startswith('./'):
        value = value[2:]
    return value


def is_tier_a_production(rel_path: str) -> bool:
    """Return True when rel_path is a Tier-A Python or Go production source file."""
    rel = normalize_rel(rel_path)
    name = Path(rel).name
    if rel.startswith('engines/cognee/') and rel.endswith('.py'):
        return not name.startswith('test_') and '/testdata/' not in rel and '/fixtures/' not in rel
    if rel.startswith('scripts/') and rel.endswith('.py'):
        if name.startswith('test_') or name.startswith('probe_'):
            return False
        return rel not in TIER_B_SCRIPTS
    if rel.startswith(('internal/', 'cmd/', 'kit/', 'core/', 'app/')) and rel.endswith('.go'):
        return not name.endswith('_test.go')
    return False


def parse_unified_diff_added_lines(diff_text: str) -> dict[str, set[int]]:
    """Parse `git diff --unified=0` output into {rel_path: {1-based added line numbers}}."""
    added: dict[str, set[int]] = {}
    current_file: str | None = None
    cur_line = 0
    for raw_line in diff_text.splitlines():
        if raw_line.startswith('+++ '):
            target = raw_line[4:].strip()
            if target == '/dev/null':
                current_file = None
            else:
                if target.startswith('b/'):
                    target = target[2:]
                # Handle git C-quoted paths minimally if present.
                if target.startswith('"') and target.endswith('"'):
                    target = target[1:-1]
                current_file = normalize_rel(target)
                added.setdefault(current_file, set())
            continue
        if raw_line.startswith('--- '):
            continue
        if raw_line.startswith('@@ '):
            match = HUNK_HEADER_RE.match(raw_line)
            if match and current_file is not None:
                cur_line = int(match.group(1))
            continue
        if current_file is None:
            continue
        if raw_line.startswith('+') and not raw_line.startswith('+++'):
            added[current_file].add(cur_line)
            cur_line += 1
        elif raw_line.startswith(' '):
            cur_line += 1
    return added


def _run_git(args: list[str], root: Path) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        ['git', *args],
        cwd=str(root),
        capture_output=True,
        text=True,
        check=False,
    )


# Self-exemption only while diff_guard itself is first introduced before initial commit.
SELF_BOOTSTRAP_PY = {
    'scripts/diff_guard.py',
    'scripts/test_diff_guard.py',
}


def collect_diff_context(
    root: Path,
    *,
    staged: bool = False,
    base_ref: str = '',
) -> tuple[list[str], list[str], dict[str, set[int]]]:
    """Return (all_changed_files, newly_added_files, {rel_path: added_line_numbers})."""
    comparison = ['--cached'] if staged else [base_ref.strip() or 'HEAD']

    def paths(*flags):
        result = _run_git(['diff', *flags, '--name-only', '-z', *comparison], root)
        result.check_returncode()
        return {normalize_rel(path) for path in result.stdout.split('\0') if path}

    changed_files = paths()
    new_files = paths('--diff-filter=A')
    diff = _run_git(['-c', 'core.quotePath=false', 'diff', '--unified=0', '--no-color', *comparison], root)
    diff.check_returncode()
    added_lines = parse_unified_diff_added_lines(diff.stdout)
    if not staged:
        untracked = _run_git(['ls-files', '--others', '--exclude-standard', '-z'], root)
        untracked.check_returncode()
        for path in filter(None, untracked.stdout.split('\0')):
            rel = normalize_rel(path)
            changed_files.add(rel)
            new_files.add(rel)
            if is_tier_a_production(rel):
                source = read_target_source(root, rel, staged=False)
                if source is not None:
                    added_lines[rel] = set(range(1, len(source.splitlines()) + 1))
    return sorted(changed_files), sorted(new_files), added_lines


def read_target_source(root: Path, rel_path: str, *, staged: bool) -> str | None:
    if staged:
        proc = _run_git(['show', f':{rel_path}'], root)
        if proc.returncode != 0:
            return None
        return proc.stdout
    path = root / rel_path
    if not path.is_file():
        return None
    try:
        return path.read_text(encoding='utf-8')
    except OSError:
        return None


def _extract_allow_guard(line_text: str) -> str | None:
    """Return non-empty exemption reason if `memlane:allow-guard(<reason>)` is present."""
    match = ALLOW_GUARD_RE.search(line_text)
    if not match:
        return None
    reason = match.group(1).strip()
    return reason if reason else ''


def _line_or_neighbor_exemption(lines: list[str], lineno: int) -> str | None:
    """Check current line and immediately preceding comment line for allow-guard."""
    for idx in (lineno - 1, lineno - 2):
        if 0 <= idx < len(lines):
            reason = _extract_allow_guard(lines[idx])
            if reason is not None:
                return reason
    return None


def _is_broad_exception_handler(handler: ast.ExceptHandler) -> bool:
    if handler.type is None:
        return True
    candidates = handler.type.elts if isinstance(handler.type, ast.Tuple) else [handler.type]
    for item in candidates:
        if isinstance(item, ast.Name) and item.id in ('Exception', 'BaseException'):
            return True
    return False


def _is_trivial_fallback_expr(expr: ast.expr | None) -> bool:
    if expr is None:
        return True
    if isinstance(expr, ast.Constant):
        return expr.value in (None, False, True, '', 0, 0.0)
    if isinstance(expr, (ast.List, ast.Tuple, ast.Set)):
        return len(expr.elts) == 0
    if isinstance(expr, ast.Dict):
        return len(expr.keys) == 0
    return False


def _is_silent_handler_body(body: list[ast.stmt]) -> bool:
    """Return True if handler body only swallows the exception without raising or signaling."""
    if not body:
        return True
    for stmt in body:
        if isinstance(stmt, (ast.Pass, ast.Continue, ast.Break)):
            continue
        if isinstance(stmt, ast.Expr) and isinstance(stmt.value, ast.Constant):
            # Ellipsis or bare docstring/literal
            continue
        if isinstance(stmt, ast.Return) and _is_trivial_fallback_expr(stmt.value):
            continue
        return False
    return True


def _extract_python_comments(source: str) -> list[tuple[int, str]]:
    comments: list[tuple[int, str]] = []
    try:
        tokens = tokenize.generate_tokens(io.StringIO(source).readline)
        for tok in tokens:
            if tok.type == tokenize.COMMENT:
                comments.append((tok.start[0], tok.string))
    except tokenize.TokenError:
        return comments
    return comments


def check_comments(rel_path, comments, added_lines):
    blocks, audits = [], []
    for lineno, comment in comments:
        if added_lines.isdisjoint(range(lineno, lineno + comment.count('\n') + 1)):
            continue
        exemption = _extract_allow_guard(comment)
        if exemption:
            audits.append(audit('allow_guard', rel_path, lineno, f'豁免声明: {exemption}'))
        elif exemption == '':
            blocks.append(block('DG003', rel_path, lineno, 'memlane:allow-guard() 必须填写具体理由，禁止空白豁免'))
        elif TEMP_COMMENT_RE.search(comment):
            first_line = comment.splitlines()[0].strip()
            blocks.append(block('DG003', rel_path, lineno, f'新增生产代码包含临时补丁/TODO 注释: {first_line}'))
    return blocks, audits


def block(code, path, line, message):
    return {'code': code, 'path': path, 'line': line, 'message': message}


def audit(kind, path, line, detail):
    return {'kind': kind, 'path': path, 'line': line, 'detail': detail}


def analyze_python_diff(
    rel_path: str,
    source: str,
    added_lines: set[int],
) -> tuple[list[dict[str, Any]], list[dict[str, Any]]]:
    blocks, audits = check_comments(rel_path, _extract_python_comments(source), added_lines)
    lines = source.splitlines()

    # 2. AST-level checks (DG001 silent broad except + audit new except/import/fallback).
    try:
        tree = ast.parse(source, filename=rel_path)
    except SyntaxError:
        tree = None

    if tree is not None:
        for node in ast.walk(tree):
            if isinstance(node, ast.ExceptHandler):
                end_line = getattr(node, 'end_lineno', node.lineno) or node.lineno
                handler_lines = set(range(node.lineno, end_line + 1))
                if not (handler_lines & added_lines):
                    continue
                is_broad = _is_broad_exception_handler(node)
                is_silent = _is_silent_handler_body(node.body)
                exemption = _line_or_neighbor_exemption(lines, node.lineno)
                if is_broad and is_silent:
                    if exemption:
                        audits.append(audit('except_swallow_exempted', rel_path, node.lineno, f'宽泛静默 except 已豁免 ({exemption})'))
                    else:
                        blocks.append(block('DG001', rel_path, node.lineno,
                            '新增宽泛 except 静默吞掉异常（无日志/无上抛）；请删掉多余兜底、显式处理或写明 memlane:allow-guard(理由)'))
                else:
                    header = lines[node.lineno - 1].strip() if 0 <= node.lineno - 1 < len(lines) else 'except:'
                    audits.append(audit('new_except', rel_path, node.lineno, header))

    # 3. Line-level audit for new imports & fallback/sleep markers.
    for lineno in sorted(added_lines):
        if lineno < 1 or lineno > len(lines):
            continue
        raw = lines[lineno - 1]
        stripped = raw.strip()
        if not stripped or stripped.startswith('#'):
            continue
        if PY_IMPORT_LINE_RE.match(raw):
            audits.append(audit('new_import', rel_path, lineno, stripped))
        elif FALLBACK_AUDIT_RE.search(stripped):
            audits.append(audit('fallback_or_sleep', rel_path, lineno, stripped[:120]))

    return blocks, audits


def _extract_go_comments(source: str) -> list[tuple[int, str]]:
    """Extract Go // and /* */ comments with 1-based start line numbers without touching strings."""
    comments = []
    line = 1
    for match in GO_TOKEN_RE.finditer(source):
        token = match.group()
        if match.lastgroup == 'trivia' and token.startswith(('//', '/*')):
            comments.append((line, token))
        line += token.count('\n')
    return comments


def _extract_go_imports(source: str) -> list[tuple[int, str]]:
    """Return the physical line and decoded path of each Go import spec."""
    imports: list[tuple[int, str]] = []
    line = 1
    pending = grouped = False
    for match in GO_TOKEN_RE.finditer(source):
        token = match.group()
        token_line = line
        line += token.count('\n')
        if match.lastgroup == 'trivia':
            continue
        if token == 'import':
            pending = True
        elif pending and token == '(':
            grouped = True
            pending = False
        elif grouped and token == ')':
            grouped = False
        elif (pending or grouped) and match.lastgroup == 'string':
            path = token[1:-1]
            if token.startswith('"'):
                # Decode numeric escapes so they cannot hide a module path.
                def decode_escape(escape: re.Match[str]) -> str:
                    value = escape[1]
                    if len(value) == 1:
                        return {'\\': '\\', '"': '"'}.get(value, escape[0])
                    if value[0] in 'xuU':
                        codepoint = int(value[1:], 16)
                        return chr(codepoint) if codepoint <= 0x10FFFF else escape[0]
                    return chr(int(value, 8))

                path = GO_IMPORT_ESCAPE_RE.sub(decode_escape, path)
            else:
                path = path.replace('\r', '')
            imports.append((token_line, path))
            pending = False
    return imports


def _check_go_import_boundary(rel_path: str, import_path: str) -> str | None:
    def in_package(package: str) -> bool:
        prefix = f'{MODULE_PREFIX}/{package}'
        return import_path == prefix or import_path.startswith(prefix + '/')

    if in_package('core/engine/cognee') and not (
        rel_path.startswith(('core/engine/cognee/', 'cmd/'))
        or Path(rel_path).parent.as_posix() == 'core/engine'
    ):
        return f'只有 Cognee 适配器、引擎工厂和 cmd 可以导入 {import_path}'
    if rel_path.startswith('kit/') and (
        import_path == MODULE_PREFIX or any(in_package(pkg) for pkg in ('core', 'app', 'internal'))
    ):
        return f'kit 禁止导入 {import_path}'
    if rel_path.startswith('core/') and any(in_package(pkg) for pkg in ('app', 'kit/ui')):
        return f'core 禁止导入 {import_path}'
    return None


def analyze_go_diff(
    rel_path: str,
    source: str,
    added_lines: set[int],
) -> tuple[list[dict[str, Any]], list[dict[str, Any]]]:
    blocks, audits = check_comments(rel_path, _extract_go_comments(source), added_lines)
    lines = source.splitlines()

    # 2. Import audits and hard architecture blocks (DG006).
    added_import_lines = set()
    for lineno, import_path in _extract_go_imports(source):
        if lineno not in added_lines:
            continue
        if lineno not in added_import_lines:
            audits.append(audit('new_import', rel_path, lineno, lines[lineno - 1].strip()))
            added_import_lines.add(lineno)
        if is_tier_a_production(rel_path):
            violation = _check_go_import_boundary(normalize_rel(rel_path), import_path)
            if violation:
                blocks.append({'code': 'DG006', 'path': rel_path, 'line': lineno, 'message': violation})

    # 3. Go empty `if err != nil {}` block check (DG002) & audits.
    for lineno in sorted(added_lines):
        if lineno < 1 or lineno > len(lines):
            continue
        raw = lines[lineno - 1]
        stripped = raw.strip()
        if not stripped or stripped.startswith('//'):
            continue

        code_part = stripped.split('//', 1)[0].strip()
        exemption = _line_or_neighbor_exemption(lines, lineno)

        # Single-line empty `if err != nil {}`
        if GO_EMPTY_ERR_RE.search(code_part):
            if not exemption:
                blocks.append(block('DG002', rel_path, lineno,
                    '新增空的 if err != nil {} 静默吞错；请返回错误、显式处理或写明 memlane:allow-guard(理由)'))
            continue

        # Multi-line `if err != nil {` followed immediately by `}` (ignoring blank lines/comments)
        if GO_ERR_BLOCK_START_RE.search(code_part) and code_part.endswith('{'):
            body_lines: list[str] = []
            closed = False
            for probe in range(lineno, min(len(lines), lineno + 6)):
                probe_code = lines[probe].split('//', 1)[0].strip()
                if not probe_code:
                    continue
                if probe_code == '}':
                    closed = True
                    break
                body_lines.append(probe_code)
            if closed and not body_lines:
                if not exemption:
                    blocks.append(block('DG002', rel_path, lineno,
                        '新增空的 if err != nil { } 静默吞错；请返回错误、显式处理或写明 memlane:allow-guard(理由)'))
                continue
            if closed and len(body_lines) == 1 and GO_SWALLOW_STMT_RE.match(body_lines[0]):
                audits.append(audit('go_err_swallow_candidate', rel_path, lineno, f'{code_part} {body_lines[0]} }}'))

        if lineno not in added_import_lines and FALLBACK_AUDIT_RE.search(code_part):
            audits.append(audit('fallback_or_sleep', rel_path, lineno, stripped[:120]))

    return blocks, audits


def check_scope_locks(
    changed_files: list[str],
    *,
    locked_patterns: list[str] | None = None,
    allowed_patterns: list[str] | None = None,
) -> list[dict[str, Any]]:
    """Enforce optional skeleton lock (`--lock`) and writable scope (`--allow`) patterns."""
    blocks: list[dict[str, Any]] = []
    locks = [normalize_rel(p) for p in (locked_patterns or []) if p.strip()]
    allows = [normalize_rel(p) for p in (allowed_patterns or []) if p.strip()]

    for rel in changed_files:
        for pat in locks:
            if rel == pat or fnmatch(rel, pat) or (pat.endswith('/') and rel.startswith(pat)):
                blocks.append(block('DG004', rel, 1, f'禁止修改已锁定的骨架/契约文件 (匹配 --lock {pat})；若架子撑不住请停下来找主控改设计'))
                break
        if allows:
            matched = any(
                rel == pat or fnmatch(rel, pat) or (pat.endswith('/') and rel.startswith(pat))
                for pat in allows
            )
            if not matched:
                blocks.append(block('DG004', rel, 1, f'改动越界：文件不在允许范围 (--allow {", ".join(allows)}) 内'))
    return blocks


def check_new_python_freeze(
    root: Path,
    new_files: list[str],
    *,
    allow_new_py: list[str] | None = None,
) -> tuple[list[dict[str, Any]], list[dict[str, Any]]]:
    """Block arbitrary new .py files (DG005); only allow 1:1 tests for existing .py or explicit approval."""
    blocks: list[dict[str, Any]] = []
    audits: list[dict[str, Any]] = []
    allowed = [normalize_rel(p) for p in (allow_new_py or []) if p.strip()]
    new_set = set(new_files)

    for rel in new_files:
        if not rel.endswith('.py'):
            continue
        if rel in SELF_BOOTSTRAP_PY:
            continue
        if any(rel == pat or fnmatch(rel, pat) for pat in allowed):
            audits.append(audit('approved_new_py', rel, 1,
                '已通过 --allow-new-py 放行新增 Python 文件（审查需确认确属 Cognee 胶水边界）'))
            continue
        # Allow adding a 1:1 test_<stem>.py strictly for an ALREADY-EXISTING production .py file.
        p = Path(rel)
        if p.name.startswith('test_'):
            stem = p.name[len('test_'):-len('.py')]
            candidates = [
                normalize_rel(str(p.parent / f'{stem}.py')),
                normalize_rel(str(p.parent / f"{stem.replace('_', '-')}.py")),
            ]
            if any((root / cand).is_file() and cand not in new_set for cand in candidates):
                continue
        blocks.append(block('DG005', rel, 1,
            '禁止随意新增 Python 文件（非 Cognee 胶水已冻结 Python 扩张）；新功能/工具请用 Go 实现，确属 Cognee 底层胶水请先经人工确认并通过 --allow-new-py 放行'))
    return blocks, audits


def run_diff_guard(
    root: Path = ROOT,
    *,
    staged: bool = False,
    base_ref: str = '',
    locked_patterns: list[str] | None = None,
    allowed_patterns: list[str] | None = None,
    allow_new_py: list[str] | None = None,
) -> dict[str, Any]:
    changed_files, new_files, added_map = collect_diff_context(root, staged=staged, base_ref=base_ref)
    blocks: list[dict[str, Any]] = []
    audits: list[dict[str, Any]] = []

    blocks.extend(
        check_scope_locks(
            changed_files,
            locked_patterns=locked_patterns,
            allowed_patterns=allowed_patterns,
        )
    )
    py_blocks, py_audits = check_new_python_freeze(
        root,
        new_files,
        allow_new_py=allow_new_py,
    )
    blocks.extend(py_blocks)
    audits.extend(py_audits)

    scanned_files: list[str] = []
    for rel in sorted(added_map):
        added_lines = added_map[rel]
        if not added_lines or not is_tier_a_production(rel):
            continue
        source = read_target_source(root, rel, staged=staged)
        if source is None:
            continue
        scanned_files.append(rel)
        if rel.endswith('.py'):
            f_blocks, f_audits = analyze_python_diff(rel, source, added_lines)
        elif rel.endswith('.go'):
            f_blocks, f_audits = analyze_go_diff(rel, source, added_lines)
        else:
            f_blocks, f_audits = [], []
        blocks.extend(f_blocks)
        audits.extend(f_audits)

    return {
        'ok': len(blocks) == 0,
        'changed_files': changed_files,
        'scanned_tier_a_files': scanned_files,
        'blocks': blocks,
        'audits': audits,
    }


def format_report(report: dict[str, Any]) -> str:
    lines: list[str] = []
    blocks = report['blocks']
    audits = report['audits']
    scanned = report['scanned_tier_a_files']

    if blocks:
        lines.append(f'diff_guard FAILED: {len(blocks)} 项阻断违规（只查本次新增 A 类生产代码 / 边界锁）')
        for item in blocks:
            lines.append(f"  {item['path']}:{item['line']}: {item['code']} {item['message']}")
    else:
        lines.append(f'diff_guard passed: 已扫描 {len(scanned)} 个新增/改动 A 类生产文件，无阻断项')

    if audits:
        lines.append(f'diff_guard 审查关注清单 ({len(audits)} 项，审查时逐条确认是否多余防御/越层依赖):')
        for item in audits[:40]:
            lines.append(f"  [{item['kind']}] {item['path']}:{item['line']} -> {item['detail']}")
        if len(audits) > 40:
            lines.append(f'  ... 另有 {len(audits) - 40} 项（使用 --json 查看完整清单）')

    return '\n'.join(lines)


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog='diff_guard.py',
        description='只查本次 git diff 新增 A 类生产代码的防静默吞错、防临时补丁、骨架锁与防御代码审计工具。',
    )
    parser.add_argument('--staged', action='store_true', help='仅检查 git 暂存区')
    parser.add_argument('--base', default='', metavar='REF', help='比较工作区与指定 ref（默认 HEAD）')
    parser.add_argument('--lock', action='append', default=[], metavar='PATH_OR_GLOB', help='锁定契约文件')
    parser.add_argument('--allow', action='append', default=[], metavar='PATH_OR_GLOB', help='限定改动范围')
    parser.add_argument('--allow-new-py', action='append', default=[], metavar='PATH_OR_GLOB', help='放行新增 Python 文件')
    parser.add_argument('--json', action='store_true', dest='json_output', help='输出 JSON 阻断项与审计清单')
    return parser


def main(argv: list[str] | None = None, *, root: Path = ROOT) -> int:
    args = build_parser().parse_args(argv)
    if args.staged and args.base:
        print('diff_guard.py: --staged 与 --base 不能同时使用', file=sys.stderr)
        return 2
    report = run_diff_guard(
        root=root,
        staged=args.staged,
        base_ref=args.base,
        locked_patterns=args.lock,
        allowed_patterns=args.allow,
        allow_new_py=args.allow_new_py,
    )
    if args.json_output:
        print(json.dumps(report, ensure_ascii=False, indent=2))
    else:
        stream = sys.stderr if not report['ok'] else sys.stdout
        print(format_report(report), file=stream)
    return 0 if report['ok'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
