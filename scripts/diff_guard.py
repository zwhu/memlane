#!/usr/bin/env python3
"""Diff-scoped guardrail against silent error swallowing, temporary hacks, and scope drift.

Designed for Memlane's Swarm + check.py + pre-commit workflow:
- Inspects ONLY added/changed lines in Tier-A production code (Python & Go).
- Blocks high-confidence anti-patterns (DG001-DG004) with zero-dependency stdlib AST/tokenizer analysis.
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

# Scripts that belong to Tier-B test/verification scaffolds rather than Tier-A production code.
TIER_B_SCRIPTS = {
    'scripts/verify_setup_cli.py',
    'scripts/smoke_release_contracts.py',
    'scripts/utm_isolated_env.py',
    'scripts/bench_scaling.py',
    'scripts/processing-timings.py',
    'scripts/diff_guard.py',
    'scripts/lint.py',
}

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
GO_IMPORT_LINE_RE = re.compile(r'^\s*(?:import\s+)?(?:[A-Za-z_][A-Za-z0-9_]*\s+|\.\s+|_\s+)?"[^"]+"')
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
    if rel.startswith('engine/python/') and rel.endswith('.py'):
        return not name.startswith('test_') and '/testdata/' not in rel and '/fixtures/' not in rel
    if rel.startswith('scripts/') and rel.endswith('.py'):
        if name.startswith('test_') or name.startswith('probe_'):
            return False
        return rel not in TIER_B_SCRIPTS
    if (rel.startswith('internal/') or rel.startswith('cmd/')) and rel.endswith('.go'):
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
    changed_files: set[str] = set()
    new_files: set[str] = set()
    added_lines: dict[str, set[int]] = {}

    if staged:
        name_proc = _run_git(['diff', '--cached', '--name-only'], root)
        for line in name_proc.stdout.splitlines():
            rel = normalize_rel(line)
            if rel:
                changed_files.add(rel)
        added_proc = _run_git(['diff', '--cached', '--diff-filter=A', '--name-only'], root)
        for line in added_proc.stdout.splitlines():
            rel = normalize_rel(line)
            if rel:
                new_files.add(rel)
        diff_proc = _run_git(['diff', '--cached', '--unified=0', '--no-color'], root)
        added_lines.update(parse_unified_diff_added_lines(diff_proc.stdout))
        return sorted(changed_files), sorted(new_files), added_lines

    ref = base_ref.strip() or 'HEAD'
    name_proc = _run_git(['diff', '--name-only', ref], root)
    for line in name_proc.stdout.splitlines():
        rel = normalize_rel(line)
        if rel:
            changed_files.add(rel)
    added_proc = _run_git(['diff', '--diff-filter=A', '--name-only', ref], root)
    for line in added_proc.stdout.splitlines():
        rel = normalize_rel(line)
        if rel:
            new_files.add(rel)
    diff_proc = _run_git(['diff', '--unified=0', '--no-color', ref], root)
    added_lines.update(parse_unified_diff_added_lines(diff_proc.stdout))

    # Include untracked files when inspecting working tree against HEAD or base_ref.
    untracked_proc = _run_git(['ls-files', '--others', '--exclude-standard'], root)
    for line in untracked_proc.stdout.splitlines():
        rel = normalize_rel(line)
        if not rel:
            continue
        changed_files.add(rel)
        new_files.add(rel)
        file_path = root / rel
        if file_path.is_file() and is_tier_a_production(rel):
            try:
                line_count = len(file_path.read_text(encoding='utf-8').splitlines())
            except OSError:
                continue
            added_lines[rel] = set(range(1, line_count + 1))

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


def analyze_python_diff(
    rel_path: str,
    source: str,
    added_lines: set[int],
) -> tuple[list[dict[str, Any]], list[dict[str, Any]]]:
    blocks: list[dict[str, Any]] = []
    audits: list[dict[str, Any]] = []
    lines = source.splitlines()

    # 1. Comment-level checks (DG003 temporary hack comments & allow-guard recording).
    for lineno, comment_text in _extract_python_comments(source):
        if lineno not in added_lines:
            continue
        exemption = _extract_allow_guard(comment_text)
        if exemption is not None:
            if not exemption:
                blocks.append({
                    'code': 'DG003',
                    'path': rel_path,
                    'line': lineno,
                    'message': 'memlane:allow-guard() 必须填写具体理由，禁止空白豁免',
                })
            else:
                audits.append({
                    'kind': 'allow_guard',
                    'path': rel_path,
                    'line': lineno,
                    'detail': f'豁免声明: {exemption}',
                })
            continue
        if TEMP_COMMENT_RE.search(comment_text):
            blocks.append({
                'code': 'DG003',
                'path': rel_path,
                'line': lineno,
                'message': f'新增生产代码包含临时补丁/TODO 注释: {comment_text.strip()}',
            })

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
                        audits.append({
                            'kind': 'except_swallow_exempted',
                            'path': rel_path,
                            'line': node.lineno,
                            'detail': f'宽泛静默 except 已豁免 ({exemption})',
                        })
                    else:
                        blocks.append({
                            'code': 'DG001',
                            'path': rel_path,
                            'line': node.lineno,
                            'message': '新增宽泛 except 静默吞掉异常（无日志/无上抛）；请删掉多余兜底、显式处理或写明 memlane:allow-guard(理由)',
                        })
                else:
                    header = lines[node.lineno - 1].strip() if 0 <= node.lineno - 1 < len(lines) else 'except:'
                    audits.append({
                        'kind': 'new_except',
                        'path': rel_path,
                        'line': node.lineno,
                        'detail': header,
                    })

    # 3. Line-level audit for new imports & fallback/sleep markers.
    for lineno in sorted(added_lines):
        if lineno < 1 or lineno > len(lines):
            continue
        raw = lines[lineno - 1]
        stripped = raw.strip()
        if not stripped or stripped.startswith('#'):
            continue
        if PY_IMPORT_LINE_RE.match(raw):
            audits.append({
                'kind': 'new_import',
                'path': rel_path,
                'line': lineno,
                'detail': stripped,
            })
        elif FALLBACK_AUDIT_RE.search(stripped):
            audits.append({
                'kind': 'fallback_or_sleep',
                'path': rel_path,
                'line': lineno,
                'detail': stripped[:120],
            })

    return blocks, audits


def _extract_go_comments(source: str) -> list[tuple[int, str]]:
    """Extract Go // and /* */ comments with 1-based start line numbers without touching strings."""
    comments: list[tuple[int, str]] = []
    i = 0
    n = len(source)
    line = 1
    while i < n:
        ch = source[i]
        nxt = source[i + 1] if i + 1 < n else ''
        if ch == '"':
            i += 1
            while i < n:
                if source[i] == '\\':
                    i += 2
                    continue
                if source[i] == '"':
                    i += 1
                    break
                if source[i] == '\n':
                    line += 1
                i += 1
            continue
        if ch == '`':
            i += 1
            while i < n:
                if source[i] == '\n':
                    line += 1
                if source[i] == '`':
                    i += 1
                    break
                i += 1
            continue
        if ch == "'":
            i += 1
            while i < n:
                if source[i] == '\\':
                    i += 2
                    continue
                if source[i] == "'":
                    i += 1
                    break
                i += 1
            continue
        if ch == '/' and nxt == '/':
            start = i
            start_line = line
            i += 2
            while i < n and source[i] != '\n':
                i += 1
            comments.append((start_line, source[start:i]))
            continue
        if ch == '/' and nxt == '*':
            start = i
            start_line = line
            i += 2
            while i < n - 1 and not (source[i] == '*' and source[i + 1] == '/'):
                if source[i] == '\n':
                    line += 1
                i += 1
            i = min(n, i + 2)
            comments.append((start_line, source[start:i]))
            continue
        if ch == '\n':
            line += 1
        i += 1
    return comments


def analyze_go_diff(
    rel_path: str,
    source: str,
    added_lines: set[int],
) -> tuple[list[dict[str, Any]], list[dict[str, Any]]]:
    blocks: list[dict[str, Any]] = []
    audits: list[dict[str, Any]] = []
    lines = source.splitlines()

    # 1. Comment-level checks (DG003 & allow-guard recording).
    for lineno, comment_text in _extract_go_comments(source):
        comment_span = set(range(lineno, lineno + comment_text.count('\n') + 1))
        if not (comment_span & added_lines):
            continue
        exemption = _extract_allow_guard(comment_text)
        if exemption is not None:
            if not exemption:
                blocks.append({
                    'code': 'DG003',
                    'path': rel_path,
                    'line': lineno,
                    'message': 'memlane:allow-guard() 必须填写具体理由，禁止空白豁免',
                })
            else:
                audits.append({
                    'kind': 'allow_guard',
                    'path': rel_path,
                    'line': lineno,
                    'detail': f'豁免声明: {exemption}',
                })
            continue
        if TEMP_COMMENT_RE.search(comment_text):
            first_line = comment_text.splitlines()[0].strip()
            blocks.append({
                'code': 'DG003',
                'path': rel_path,
                'line': lineno,
                'message': f'新增生产代码包含临时补丁/TODO 注释: {first_line}',
            })

    # 2. Go empty `if err != nil {}` block check (DG002) & audits.
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
                blocks.append({
                    'code': 'DG002',
                    'path': rel_path,
                    'line': lineno,
                    'message': '新增空的 if err != nil {} 静默吞错；请返回错误、显式处理或写明 memlane:allow-guard(理由)',
                })
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
                    blocks.append({
                        'code': 'DG002',
                        'path': rel_path,
                        'line': lineno,
                        'message': '新增空的 if err != nil { } 静默吞错；请返回错误、显式处理或写明 memlane:allow-guard(理由)',
                    })
                continue
            if closed and len(body_lines) == 1 and GO_SWALLOW_STMT_RE.match(body_lines[0]):
                audits.append({
                    'kind': 'go_err_swallow_candidate',
                    'path': rel_path,
                    'line': lineno,
                    'detail': f'{code_part} {body_lines[0]} }}',
                })

        if GO_IMPORT_LINE_RE.match(raw):
            audits.append({
                'kind': 'new_import',
                'path': rel_path,
                'line': lineno,
                'detail': stripped,
            })
        elif FALLBACK_AUDIT_RE.search(code_part):
            audits.append({
                'kind': 'fallback_or_sleep',
                'path': rel_path,
                'line': lineno,
                'detail': stripped[:120],
            })

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
                blocks.append({
                    'code': 'DG004',
                    'path': rel,
                    'line': 1,
                    'message': f'禁止修改已锁定的骨架/契约文件 (匹配 --lock {pat})；若架子撑不住请停下来找主控改设计',
                })
                break
        if allows:
            matched = any(
                rel == pat or fnmatch(rel, pat) or (pat.endswith('/') and rel.startswith(pat))
                for pat in allows
            )
            if not matched:
                blocks.append({
                    'code': 'DG004',
                    'path': rel,
                    'line': 1,
                    'message': f'改动越界：文件不在允许范围 (--allow {", ".join(allows)}) 内',
                })
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
            audits.append({
                'kind': 'approved_new_py',
                'path': rel,
                'line': 1,
                'detail': '已通过 --allow-new-py 放行新增 Python 文件（审查需确认确属 Cognee 胶水边界）',
            })
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
        blocks.append({
            'code': 'DG005',
            'path': rel,
            'line': 1,
            'message': '禁止随意新增 Python 文件（非 Cognee 胶水已冻结 Python 扩张）；新功能/工具请用 Go 实现，确属 Cognee 底层胶水请先经人工确认并通过 --allow-new-py 放行',
        })
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
    parser.add_argument(
        '--staged',
        action='store_true',
        help='仅检查 git 暂存区 (git diff --cached)',
    )
    parser.add_argument(
        '--base',
        default='',
        metavar='REF',
        help='与指定 git ref 比较工作区改动（默认 HEAD）',
    )
    parser.add_argument(
        '--lock',
        action='append',
        default=[],
        metavar='PATH_OR_GLOB',
        help='锁定不准修改的骨架/契约文件或通配符（可多次指定）',
    )
    parser.add_argument(
        '--allow',
        action='append',
        default=[],
        metavar='PATH_OR_GLOB',
        help='限定允许修改的文件或目录前缀/通配符（可多次指定）',
    )
    parser.add_argument(
        '--allow-new-py',
        action='append',
        default=[],
        metavar='PATH_OR_GLOB',
        help='人工批准放行的新增 Python 文件（仅限确属 Cognee 底层胶水，可多次指定）',
    )
    parser.add_argument(
        '--json',
        action='store_true',
        dest='json_output',
        help='以 JSON 格式输出完整阻断项与审计清单',
    )
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
