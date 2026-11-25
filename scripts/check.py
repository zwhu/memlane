#!/usr/bin/env python3
"""Unified change-aware and staged local verification gate for Memlane.

Modes:
  --changed [REF]   Run lint and affected Go/Python tests for changed files.
  --staged          Run --changed strictly against staged git index entries.
  --pre-push        Run full repo lint, test_lint, and parallel scripts/test_*.py.
  --full            Run 3-stage serialized full gate (lint -> Go -> parallel Python).
  --release         Run --full followed by scripts/smoke_release_contracts.py (exit 0/1/2/3).

Dependency-free (Python 3.9+ stdlib only) and compliant with ML001-ML022.
"""
from __future__ import annotations

import argparse
import ast
from concurrent.futures import ThreadPoolExecutor, as_completed
import json
import math
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import time
from typing import Any


ROOT = Path(__file__).resolve().parents[1]
SCHEMA_VERSION = 1

MODEL_CONTRACT_TESTS = (
    'engine/python/test_generation_parameters.py',
    'engine/python/test_generation_probe.py',
    'engine/python/test_generation_handoff.py',
    'engine/python/test_generation_startup.py',
)

MODEL_CONTRACT_EXACT_SOURCES = {
    'engine/python/agent_bridge_runtime.py',
    'engine/python/language_egress.py',
    'engine/python/language_settings.py',
    'engine/python/official_service_dev.py',
}

EMBEDDED_RUNTIME_SCRIPTS = {
    'scripts/dev.py',
    'scripts/dev_recovery.py',
    'scripts/local_prepare.py',
    'scripts/build_project_plugin.py',
    'scripts/task.py',
    'scripts/disk-space.py',
    'scripts/uninstall.py',
}

RELEASE_META_FILES = {
    'mygo.json',
    'LICENSE',
    'NOTICE',
    'THIRD_PARTY_LICENSES.md',
}

FULL_ESCALATION_FILES = {
    'go.mod',
    'go.sum',
    'scripts/go.sh',
    'scripts/mygo_sdk.py',
}

DOC_ONLY_EXACT_FILES = {
    'AGENTS.md',
    'CONTEXT.md',
    'README.md',
    'README.en.md',
}

IGNORED_NON_CODE_FILES = {
    '.gitignore',
    '.gitattributes',
    '.DS_Store',
}

STANDALONE_SCRIPT_PROBES = {
    'scripts/bench_scaling.py',
    'scripts/bootstrap.py',
    'scripts/build_opencode_plugin.py',
    'scripts/probe_daily_direct_memory.py',
    'scripts/probe_official_acl_readonly.py',
    'scripts/probe_official_plugin.py',
    'scripts/processing-timings.py',
}

SCRIPT_EXTRA_TESTS: dict[str, tuple[str, ...]] = {
    'scripts/dev.py': (
        'scripts/test_dev.py',
        'scripts/test_dev_streams.py',
        'scripts/test_runtime_hygiene.py',
        'scripts/test_dev_recovery.py',
        'scripts/test_overview_initialization.py',
    ),
    'scripts/dev_recovery.py': (
        'scripts/test_dev_recovery.py',
        'scripts/test_dev.py',
    ),
    'scripts/disk-space.py': (
        'scripts/test_disk_space.py',
        'scripts/test_runtime_hygiene.py',
    ),
    'scripts/task.py': (
        'scripts/test_disk_space.py',
        'scripts/test_runtime_hygiene.py',
        'scripts/test_check.py',
    ),
    'scripts/utm_guard.py': (
        'scripts/test_utm_guard.py',
        'scripts/test_verify_setup_cli.py',
        'scripts/test_smoke_release_contracts.py',
    ),
    'scripts/utm_isolated_env.py': (
        'scripts/test_utm_isolated_env.py',
        'scripts/test_verify_setup_cli.py',
    ),
    'scripts/smoke_release_contracts.py': (
        'scripts/test_smoke_release_contracts.py',
        'scripts/test_build_release.py',
    ),
    'scripts/build_project_plugin.py': (
        'scripts/test_build_release.py',
        'engine/python/test_project_plugin_patch.py',
    ),
}

ENGINE_DOMAIN_TESTS: dict[str, tuple[str, ...]] = {
    'v2_api': (
        'engine/python/test_v2_api.py',
        'engine/python/test_catalog_index.py',
    ),
    'catalog': (
        'engine/python/test_catalog_index.py',
        'engine/python/test_v2_api.py',
    ),
    'trae': (
        'engine/python/test_trae_hooks_migration.py',
        'engine/python/test_trae_ide_connection.py',
        'engine/python/test_trae_ide_hook.py',
    ),
    'opencode': (
        'engine/python/test_opencode_connection.py',
        'engine/python/test_opencode_plugin_patch.py',
        'engine/python/test_opencode_backlog.py',
        'engine/python/test_opencode_control.py',
        'engine/python/test_opencode_installation.py',
    ),
}

DEFAULT_MODULE_WEIGHT_S = 0.5
SOLO_BUCKET_THRESHOLD_S = 3.5
LIGHT_BUCKET_TARGET_S = 2.5

# Static LPT weights (seconds, calibrated from empirical post-P1 timings)
# for unified cross-suite scheduling so the heaviest modules occupy slots at t=0.
MODULE_WEIGHT_HINTS: dict[str, float] = {
    'engine/python/test_agent_connections.py': 19.6,
    'engine/python/test_antigravity_connection.py': 16.0,
    'engine/python/test_project_host_plugin.py': 14.0,
    'engine/python/test_project_backlog_worker.py': 13.0,
    'engine/python/test_generation_probe.py': 12.0,
    'engine/python/test_capture_receipts.py': 11.0,
    'engine/python/test_plugin_capture_drain.py': 10.0,
    'engine/python/test_codex_completion_endpoint.py': 9.5,
    'engine/python/test_codex_trace_patch.py': 9.0,
    'engine/python/test_generation_ollama.py': 9.0,
    'engine/python/test_generation_handoff.py': 7.0,
    'engine/python/test_generation_capacity.py': 6.4,
    'engine/python/test_project_plugin_patch.py': 6.3,
    'engine/python/test_project_tool_patch.py': 6.1,
    'engine/python/test_user_profiles.py': 6.1,
    'engine/python/test_generation_bridge.py': 5.5,
    'scripts/test_verify_setup_cli.py': 4.5,
    'engine/python/test_project_control.py': 3.6,
    'engine/python/test_antigravity_bridge.py': 3.2,
    'engine/python/test_scaling_adversarial.py': 3.1,
    'engine/python/test_pi_completion.py': 2.5,
    'engine/python/test_trae_hooks_migration.py': 2.3,
    'engine/python/test_registry_sessions_store.py': 2.0,
    'engine/python/test_generation_validation.py': 1.8,
    'engine/python/test_v2_api.py': 1.7,
    'scripts/test_build_release.py': 1.4,
    'scripts/test_dev.py': 0.9,
}

RESULT_MARKER = '__MEMLANE_CHECK_RESULT__='

_UNITTEST_WORKER = r'''
import io, json, os, sys, time, unittest

suite_dir = sys.argv[1]
modules = sys.argv[2:]
sys.path.insert(0, suite_dir)

loader = unittest.TestLoader()
suite = unittest.TestSuite()
load_errors = []
for mod in modules:
    try:
        suite.addTests(loader.loadTestsFromName(mod))
    except Exception as exc:
        load_errors.append(f"load {mod}: {exc}")

stream = io.StringIO()
t0 = time.perf_counter()
runner = unittest.TextTestRunner(stream=stream, verbosity=1)
result = runner.run(suite)
elapsed = time.perf_counter() - t0

payload = {
    "suite_dir": suite_dir,
    "modules": modules,
    "tests_run": result.testsRun,
    "failures": len(result.failures),
    "errors": len(result.errors) + len(load_errors),
    "skipped": len(result.skipped),
    "ok": result.wasSuccessful() and not load_errors,
    "elapsed_s": round(elapsed, 3),
    "output": ("\n".join(load_errors) + ("\n" if load_errors else "") + stream.getvalue()) if (not result.wasSuccessful() or load_errors) else "",
}
print("__MEMLANE_CHECK_RESULT__=" + json.dumps(payload, ensure_ascii=False), flush=True)
sys.exit(0 if payload["ok"] else 1)
'''


def resolve_python(root: Path = ROOT) -> str:
    candidate = root / '.local/cognee-python/bin/python'
    if candidate.is_file() and os.access(candidate, os.X_OK):
        return str(candidate)
    return sys.executable


def default_jobs() -> int:
    cpus = os.cpu_count() or 4
    return max(1, min(6, cpus // 2))


def normalize_rel(path: str) -> str:
    value = path.replace('\\', '/').strip()
    while value.startswith('./'):
        value = value[2:]
    return value


def parse_porcelain_z(raw: bytes) -> list[tuple[str, str]]:
    """Parse `git status --porcelain=v1 -z` into [(rel_path, status_code)].

    Renames/copies yield both the old path ('R_OLD') and new path ('R_NEW').
    """
    entries: list[tuple[str, str]] = []
    if not raw:
        return entries
    tokens = raw.split(b'\0')
    idx = 0
    total = len(tokens)
    while idx < total:
        token = tokens[idx]
        if not token:
            idx += 1
            continue
        if len(token) < 4:
            idx += 1
            continue
        xy = token[:2].decode('utf-8', errors='replace')
        path = normalize_rel(token[3:].decode('utf-8', errors='surrogateescape'))
        if 'R' in xy or 'C' in xy:
            idx += 1
            old_path = ''
            if idx < total and tokens[idx]:
                old_path = normalize_rel(tokens[idx].decode('utf-8', errors='surrogateescape'))
            if old_path:
                entries.append((old_path, 'R_OLD'))
            entries.append((path, 'R_NEW'))
            idx += 1
            continue
        if 'D' in xy:
            status = 'D'
        elif xy == '??' or 'A' in xy:
            status = 'A'
        else:
            status = 'M'
        entries.append((path, status))
        idx += 1
    return entries


def parse_name_status_z(raw: bytes) -> list[tuple[str, str]]:
    """Parse `git diff --name-status -z` into [(rel_path, status_code)]."""
    entries: list[tuple[str, str]] = []
    if not raw:
        return entries
    tokens = raw.split(b'\0')
    idx = 0
    total = len(tokens)
    while idx < total:
        token = tokens[idx]
        if not token:
            idx += 1
            continue
        st = token.decode('utf-8', errors='replace')
        if st.startswith(('R', 'C')):
            if idx + 2 < total:
                old_path = normalize_rel(tokens[idx + 1].decode('utf-8', errors='surrogateescape'))
                new_path = normalize_rel(tokens[idx + 2].decode('utf-8', errors='surrogateescape'))
                if old_path:
                    entries.append((old_path, 'R_OLD'))
                if new_path:
                    entries.append((new_path, 'R_NEW'))
            idx += 3
            continue
        if idx + 1 < total:
            path = normalize_rel(tokens[idx + 1].decode('utf-8', errors='surrogateescape'))
            code = st[:1] if st else 'M'
            entries.append((path, code))
        idx += 2
    return entries


def collect_git_changes(
    root: Path = ROOT,
    *,
    staged: bool = False,
    base_ref: str = '',
) -> list[tuple[str, str]]:
    """Collect changed files as [(rel_path, status)] including renames and deletions."""
    if staged:
        proc = subprocess.run(
            ['git', '-C', str(root), 'diff', '--cached', '--name-status', '-z'],
            capture_output=True,
            check=True,
        )
        return parse_name_status_z(proc.stdout)

    if base_ref:
        diff_proc = subprocess.run(
            ['git', '-C', str(root), 'diff', '--name-status', '-z', base_ref],
            capture_output=True,
            check=True,
        )
        entries = parse_name_status_z(diff_proc.stdout)
        untracked_proc = subprocess.run(
            ['git', '-C', str(root), 'ls-files', '--others', '--exclude-standard', '-z'],
            capture_output=True,
            check=True,
        )
        seen = {(p, s) for p, s in entries}
        for raw_item in untracked_proc.stdout.split(b'\0'):
            if not raw_item:
                continue
            rel = normalize_rel(raw_item.decode('utf-8', errors='surrogateescape'))
            pair = (rel, 'A')
            if pair not in seen:
                entries.append(pair)
                seen.add(pair)
        return entries

    proc = subprocess.run(
        ['git', '-C', str(root), 'status', '--porcelain=v1', '-z', '-uall'],
        capture_output=True,
        check=True,
    )
    return parse_porcelain_z(proc.stdout)


def discover_test_modules(root: Path, directory: str) -> list[str]:
    base = root / directory
    if not base.is_dir():
        return []
    return [f'{directory}/{p.name}' for p in sorted(base.glob('test_*.py')) if p.is_file()]


def _imported_module_names(source: str) -> set[str]:
    """Return top-level imported module names from a Python test file via AST."""
    try:
        tree = ast.parse(source)
    except SyntaxError:
        return set()
    names: set[str] = set()
    for node in ast.walk(tree):
        if isinstance(node, ast.Import):
            for alias in node.names:
                names.add(alias.name.split('.')[0])
        elif isinstance(node, ast.ImportFrom) and node.module:
            names.add(node.module.split('.')[0])
    return names


def find_reverse_engine_importers(root: Path, module_stems: set[str]) -> set[str]:
    """Find engine/python/test_*.py files that directly import any stem in module_stems."""
    if not module_stems:
        return set()
    engine_dir = root / 'engine/python'
    if not engine_dir.is_dir():
        return set()
    matched: set[str] = set()
    for test_path in sorted(engine_dir.glob('test_*.py')):
        try:
            imports = _imported_module_names(test_path.read_text(encoding='utf-8'))
        except OSError:
            continue
        if imports & module_stems:
            matched.add(f'engine/python/{test_path.name}')
    return matched


def _add_if_exists(root: Path, target_set: set[str], rel_paths: list[str] | tuple[str, ...]) -> None:
    for rel in rel_paths:
        if (root / rel).is_file():
            target_set.add(rel)


def _existing_go_packages(root: Path, packages: set[str]) -> list[str]:
    result: list[str] = []
    for pkg in sorted(packages):
        if pkg == './...' or pkg == '.':
            result.append(pkg)
            continue
        rel = pkg[2:] if pkg.startswith('./') else pkg
        if (root / rel).is_dir():
            result.append(pkg)
    return result


def build_full_stages(
    root: Path,
    *,
    include_release: bool = False,
    race_pkgs: list[str] | None = None,
    app_path: str = '',
) -> list[dict[str, Any]]:
    """Build the 3-stage serialized full gate plan (plus optional Stage 4 release smoke)."""
    scripts_tests = discover_test_modules(root, 'scripts')
    engine_tests = discover_test_modules(root, 'engine/python')
    stages: list[dict[str, Any]] = [
        {
            'stage': 1,
            'name': 'lint_and_self_test',
            'kind': 'lint',
            'run_lint': True,
            'run_lint_tests': True,
            'description': 'Full-repo scripts/lint.py (ML001-ML022) + scripts/test_lint.py',
        },
        {
            'stage': 2,
            'name': 'go_vet_and_test',
            'kind': 'go',
            'vet_packages': ['./...'],
            'test_packages': ['./...'],
            'root_test_regex': '',
            'race_packages': sorted(set(race_pkgs or [])),
            'description': 'Exclusive-CPU Go vet + go test -count=1 ./... (protects perf_budget_test.go)',
        },
        {
            'stage': 3,
            'name': 'python_parallel_tests',
            'kind': 'python_parallel',
            'scripts_tests': scripts_tests,
            'engine_tests': engine_tests,
            'description': f'Parallel unittest ({len(scripts_tests)} scripts + {len(engine_tests)} engine/python modules)',
        },
    ]
    if include_release:
        stages.append(
            {
                'stage': 4,
                'name': 'release_contract_smoke',
                'kind': 'release_smoke',
                'app_path': app_path,
                'description': 'Level 0 local unmocked contract smoke (scripts/smoke_release_contracts.py)',
            }
        )
    return stages


def build_pre_push_stages(root: Path) -> list[dict[str, Any]]:
    scripts_tests = discover_test_modules(root, 'scripts')
    return [
        {
            'stage': 1,
            'name': 'lint_and_self_test',
            'kind': 'lint',
            'run_lint': True,
            'run_lint_tests': True,
            'description': 'Full-repo scripts/lint.py (ML001-ML022) + scripts/test_lint.py',
        },
        {
            'stage': 2,
            'name': 'scripts_parallel_tests',
            'kind': 'python_parallel',
            'scripts_tests': scripts_tests,
            'engine_tests': [],
            'description': f'Parallel scripts/test_*.py ({len(scripts_tests)} modules)',
        },
    ]


def plan_changed(
    changes: list[tuple[str, str]],
    root: Path = ROOT,
    *,
    race_pkgs: list[str] | None = None,
) -> dict[str, Any]:
    """Map changed paths to minimal required verification steps with fail-safe escalation."""
    normalized = [(normalize_rel(p), s) for p, s in changes if normalize_rel(p)]
    unique_paths = sorted({p for p, _ in normalized})
    deleted_paths = sorted({p for p, s in normalized if s in ('D', 'R_OLD')})

    plan: dict[str, Any] = {
        'changed_files': unique_paths,
        'deleted_files': deleted_paths,
        'no_changed_files': len(unique_paths) == 0,
        'forbidden_paths': [],
        'escalated_to_full': False,
        'escalation_reasons': [],
        'stages': [],
    }

    if not normalized:
        plan['stages'] = [
            {
                'stage': 1,
                'name': 'lint_sanity',
                'kind': 'lint',
                'run_lint': True,
                'run_lint_tests': False,
                'description': 'Clean working tree baseline: full-repo scripts/lint.py',
            }
        ]
        return plan

    run_lint = False
    run_lint_tests = False
    hook_syntax_files: set[str] = set()
    go_vet_pkgs: set[str] = set()
    go_test_pkgs: set[str] = set()
    go_root_tests: set[str] = set()
    go_simpledesktop_regex: set[str] = set()
    scripts_tests: set[str] = set()
    engine_tests: set[str] = set()
    engine_reverse_stems: set[str] = set()
    unmatched_engine_stems: set[str] = set()
    needs_all_scripts_tests = False

    for rel, status in normalized:
        # Hard guard: docs/process/* (except README.md) must never be committed.
        if rel.startswith('docs/process/') and rel != 'docs/process/README.md':
            if status in ('D', 'R_OLD'):
                run_lint = True
            else:
                plan['forbidden_paths'].append(rel)
            continue

        if rel in IGNORED_NON_CODE_FILES:
            continue

        # Escalation files: core Go module or toolchain entrypoints.
        if rel in FULL_ESCALATION_FILES or rel.startswith('scripts/patches/'):
            plan['escalated_to_full'] = True
            plan['escalation_reasons'].append(f'core toolchain/module file changed: {rel}')
            continue

        # Docs & skills (C tier).
        if (
            rel in DOC_ONLY_EXACT_FILES
            or rel.startswith(('docs/', '.agent/', '.agents/'))
        ):
            run_lint = True
            continue

        # Packaging / license metadata (blind spot #1 from #9).
        if rel in RELEASE_META_FILES or rel.startswith('assets/'):
            run_lint = True
            _add_if_exists(root, scripts_tests, ['scripts/test_build_release.py'])
            continue

        # Root Go files.
        if rel == 'lint_gate_test.go':
            run_lint = True
            run_lint_tests = True
            go_vet_pkgs.add('.')
            go_root_tests.add('TestStaticLintGate')
            continue

        if rel in ('runtime_assets.go', 'runtime_assets_test.go'):
            run_lint = True
            go_vet_pkgs.add('.')
            go_root_tests.add('TestRuntimeAssetsMaterializeAndEmbedLocalScriptImports')
            go_test_pkgs.add('./internal/bootstrap')
            continue

        # Git hooks & hook installer.
        if rel.startswith('.githooks/') or rel == 'scripts/install-hooks.sh':
            run_lint = True
            run_lint_tests = True
            if (root / rel).is_file():
                hook_syntax_files.add(rel)
            if rel == '.githooks/pre-push':
                _add_if_exists(root, scripts_tests, ['scripts/test_check.py'])
            continue

        # scripts/ directory.
        if rel.startswith('scripts/'):
            name = Path(rel).name
            if rel in ('scripts/lint.py', 'scripts/test_lint.py'):
                run_lint = True
                run_lint_tests = True
                go_root_tests.add('TestStaticLintGate')
                continue

            if rel.endswith('.py'):
                run_lint = True
                if name.startswith('test_'):
                    _add_if_exists(root, scripts_tests, [rel])
                else:
                    matched_script_test = False
                    if rel in EMBEDDED_RUNTIME_SCRIPTS:
                        go_root_tests.add('TestRuntimeAssetsMaterializeAndEmbedLocalScriptImports')
                    if rel in SCRIPT_EXTRA_TESTS:
                        before = len(scripts_tests) + len(engine_tests)
                        for dep in SCRIPT_EXTRA_TESTS[rel]:
                            if dep.startswith('scripts/'):
                                _add_if_exists(root, scripts_tests, [dep])
                            elif dep.startswith('engine/python/'):
                                _add_if_exists(root, engine_tests, [dep])
                        if rel == 'scripts/build_project_plugin.py':
                            go_test_pkgs.add('./internal/bootstrap')
                        if len(scripts_tests) + len(engine_tests) > before:
                            matched_script_test = True
                    stem = Path(name).stem.replace('-', '_')
                    direct_test = f'scripts/test_{stem}.py'
                    if (root / direct_test).is_file():
                        scripts_tests.add(direct_test)
                        matched_script_test = True
                    if not matched_script_test and rel not in STANDALONE_SCRIPT_PROBES:
                        needs_all_scripts_tests = True
                continue

            if rel.endswith('.sh'):
                if (root / rel).is_file():
                    hook_syntax_files.add(rel)
                run_lint = True
                continue

            if rel in ('scripts/test_pi_extension.mjs', 'scripts/render-app-icon.swift'):
                run_lint = True
                continue

            plan['escalated_to_full'] = True
            plan['escalation_reasons'].append(f'unmapped scripts path: {rel}')
            continue

        # Go packages with dependency propagation from `go list`.
        if rel.startswith('internal/supervisor/'):
            run_lint = True
            go_vet_pkgs.update(['./internal/supervisor', './internal/bootstrap', './internal/setupcli'])
            go_test_pkgs.update(['./internal/supervisor', './internal/bootstrap', './internal/setupcli'])
            continue

        if rel.startswith('internal/controlplane/'):
            run_lint = True
            go_vet_pkgs.update(['./internal/controlplane', './internal/setupcli', './internal/simpledesktop'])
            go_test_pkgs.update(['./internal/controlplane', './internal/setupcli', './internal/simpledesktop'])
            continue

        if rel.startswith('internal/bootstrap/'):
            run_lint = True
            go_vet_pkgs.update(['./internal/bootstrap', './internal/setupcli', './internal/simpledesktop', './cmd/memlane-bootstrap', './cmd/memlane-app'])
            go_test_pkgs.update(['./internal/bootstrap', './internal/setupcli', './cmd/memlane-bootstrap'])
            continue

        if rel.startswith('internal/setupcli/'):
            run_lint = True
            go_vet_pkgs.update(['./internal/setupcli', './cmd/memlane-app'])
            go_test_pkgs.update(['./internal/setupcli', './cmd/memlane-app'])
            continue

        if rel.startswith('internal/simpledesktop/'):
            run_lint = True
            go_vet_pkgs.update(['./internal/simpledesktop', './cmd/memlane-app'])
            go_test_pkgs.add('./internal/simpledesktop')
            continue

        if rel.startswith('cmd/memlane-app/'):
            run_lint = True
            go_vet_pkgs.add('./cmd/memlane-app')
            go_test_pkgs.add('./cmd/memlane-app')
            continue

        if rel.startswith('cmd/memlane-bootstrap/'):
            run_lint = True
            go_vet_pkgs.add('./cmd/memlane-bootstrap')
            go_test_pkgs.add('./cmd/memlane-bootstrap')
            continue

        # engine/ Go & non-python assets (blind spot #2 from #9).
        if rel == 'engine/bootstrap_assets.go':
            run_lint = True
            go_vet_pkgs.update(['./engine', './internal/bootstrap', './internal/setupcli'])
            go_root_tests.add('TestRuntimeAssetsMaterializeAndEmbedLocalScriptImports')
            go_test_pkgs.add('./internal/bootstrap')
            continue

        if rel.startswith('engine/prompts/'):
            run_lint = True
            go_root_tests.add('TestRuntimeAssetsMaterializeAndEmbedLocalScriptImports')
            _add_if_exists(root, engine_tests, [
                'engine/python/test_pi_completion.py',
                'engine/python/test_user_profiles.py',
            ])
            continue

        if rel.startswith('engine/patches/'):
            run_lint = True
            go_root_tests.add('TestRuntimeAssetsMaterializeAndEmbedLocalScriptImports')
            go_test_pkgs.add('./internal/bootstrap')
            _add_if_exists(root, engine_tests, [
                'engine/python/test_project_plugin_patch.py',
                'engine/python/test_project_tool_patch.py',
                'engine/python/test_project_host_plugin.py',
            ])
            continue

        if rel.startswith('engine/agents/pi/'):
            go_root_tests.add('TestRuntimeAssetsMaterializeAndEmbedLocalScriptImports')
            _add_if_exists(root, engine_tests, [
                'engine/python/test_agent_connections.py',
                'engine/python/test_pi_completion.py',
            ])
            continue

        if rel.startswith('engine/agents/opencode/'):
            go_root_tests.add('TestRuntimeAssetsMaterializeAndEmbedLocalScriptImports')
            _add_if_exists(root, engine_tests, [
                'engine/python/test_opencode_connection.py',
                'engine/python/test_opencode_plugin_patch.py',
                'engine/python/test_opencode_backlog.py',
            ])
            continue

        if rel.startswith('engine/') and rel.count('/') == 1 and rel.endswith('.json'):
            run_lint = True
            go_root_tests.add('TestRuntimeAssetsMaterializeAndEmbedLocalScriptImports')
            go_test_pkgs.add('./internal/bootstrap')
            _add_if_exists(root, engine_tests, ['engine/python/test_official_service_dev.py'])
            _add_if_exists(root, scripts_tests, ['scripts/test_build_release.py'])
            continue

        # engine/python/** (blind spot #3 + model parameter contract + 1-hop reverse import).
        if rel.startswith('engine/python/'):
            run_lint = True
            sub = rel[len('engine/python/'):]
            name = Path(sub).name

            # Embedded production python modules or lockfiles: verify //go:embed on add/delete/rename.
            if (rel.endswith('.py') and not name.startswith('test_')) or rel.endswith(('.lock', '.in')):
                if status in ('A', 'D', 'R_OLD', 'R_NEW') or rel.endswith(('.lock', '.in')):
                    go_root_tests.add('TestRuntimeAssetsMaterializeAndEmbedLocalScriptImports')

            if sub.startswith('testdata/') or sub.startswith('fixtures/'):
                _add_if_exists(root, engine_tests, ['engine/python/test_codex_completion_endpoint.py'])
                continue

            if sub.startswith('user_profiles/'):
                _add_if_exists(root, engine_tests, ['engine/python/test_user_profiles.py'])
                continue

            if name.startswith('test_') and rel.endswith('.py'):
                _add_if_exists(root, engine_tests, [rel])
                continue

            if rel.endswith('.py'):
                stem = Path(name).stem
                matched_engine = False

                # Mandatory 4-test model parameter regression trigger (including official_service_dev.py).
                if name.startswith('generation_') or rel in MODEL_CONTRACT_EXACT_SOURCES:
                    _add_if_exists(root, engine_tests, MODEL_CONTRACT_TESTS)
                    matched_engine = True

                # Direct 1:1 test module match.
                direct = f'engine/python/test_{stem}.py'
                if (root / direct).is_file():
                    engine_tests.add(direct)
                    matched_engine = True

                # Domain fan-out groups.
                if name.startswith(('v2_api', 'v2_lookups')) or stem in {
                    'sqlite_ro', 'reading_state_store', 'provenance_store',
                    'knowledge_recall', 'local_body_reader', 'raw_records_read', 'cache_read_v2',
                }:
                    _add_if_exists(root, engine_tests, ENGINE_DOMAIN_TESTS['v2_api'])
                    matched_engine = True
                if name.startswith('catalog_'):
                    _add_if_exists(root, engine_tests, ENGINE_DOMAIN_TESTS['catalog'])
                    matched_engine = True
                if name.startswith('trae'):
                    _add_if_exists(root, engine_tests, ENGINE_DOMAIN_TESTS['trae'])
                    matched_engine = True
                if name.startswith('opencode_'):
                    _add_if_exists(root, engine_tests, ENGINE_DOMAIN_TESTS['opencode'])
                    matched_engine = True

                engine_reverse_stems.add(stem)
                if not matched_engine:
                    unmatched_engine_stems.add(stem)
                    # Also ensure runtime asset test checks new/unpaired modules.
                    go_root_tests.add('TestRuntimeAssetsMaterializeAndEmbedLocalScriptImports')
                continue

            if rel.endswith(('.lock', '.in')):
                go_test_pkgs.add('./internal/bootstrap')
                continue

            plan['escalated_to_full'] = True
            plan['escalation_reasons'].append(f'unmapped engine/python path: {rel}')
            continue

        # Official graph web bundle.
        if rel.startswith('web/official-graph/'):
            go_root_tests.add('TestRuntimeAssetsMaterializeAndEmbedLocalScriptImports')
            go_simpledesktop_regex.add('Graph')
            _add_if_exists(root, scripts_tests, ['scripts/test_build_release.py'])
            continue

        # Fail-safe catch-all: any unmapped path escalates to full gate!
        plan['escalated_to_full'] = True
        plan['escalation_reasons'].append(f'unmapped path: {rel}')

    if plan['forbidden_paths']:
        return plan

    if plan['escalated_to_full']:
        plan['stages'] = build_full_stages(root, include_release=False, race_pkgs=race_pkgs)
        return plan

    if engine_reverse_stems:
        importers = find_reverse_engine_importers(root, engine_reverse_stems)
        engine_tests.update(importers)
        still_unmatched: list[str] = []
        for stem in sorted(unmatched_engine_stems):
            if not find_reverse_engine_importers(root, {stem}):
                still_unmatched.append(stem)
        if still_unmatched:
            plan['escalated_to_full'] = True
            plan['escalation_reasons'].append(
                'no engine/python tests matched stems: ' + ', '.join(still_unmatched)
            )
            plan['stages'] = build_full_stages(root, include_release=False, race_pkgs=race_pkgs)
            return plan

    if needs_all_scripts_tests:
        scripts_tests.update(discover_test_modules(root, 'scripts'))

    # Assemble ordered stages for --changed (mirrors 3-stage separation).
    stages: list[dict[str, Any]] = []
    stage_num = 1

    if run_lint or run_lint_tests or hook_syntax_files:
        stages.append(
            {
                'stage': stage_num,
                'name': 'lint_and_hooks',
                'kind': 'lint',
                'run_lint': run_lint,
                'run_lint_tests': run_lint_tests,
                'hook_syntax_files': sorted(hook_syntax_files),
                'description': 'Static lint / hook syntax gate',
            }
        )
        stage_num += 1

    vet_list = _existing_go_packages(root, go_vet_pkgs)
    test_list = _existing_go_packages(root, go_test_pkgs)
    root_regex = '^(' + '|'.join(sorted(go_root_tests)) + ')$' if go_root_tests else ''
    sd_regex = '|'.join(sorted(go_simpledesktop_regex)) if (go_simpledesktop_regex and './internal/simpledesktop' not in test_list) else ''
    race_list = sorted(set(race_pkgs or []))

    if vet_list or test_list or root_regex or sd_regex or race_list:
        stages.append(
            {
                'stage': stage_num,
                'name': 'go_targeted',
                'kind': 'go',
                'vet_packages': vet_list,
                'test_packages': test_list,
                'root_test_regex': root_regex,
                'simpledesktop_test_regex': sd_regex,
                'race_packages': race_list,
                'description': 'Targeted Go vet & tests',
            }
        )
        stage_num += 1

    if scripts_tests or engine_tests:
        stages.append(
            {
                'stage': stage_num,
                'name': 'python_targeted',
                'kind': 'python_parallel',
                'scripts_tests': sorted(scripts_tests),
                'engine_tests': sorted(engine_tests),
                'description': f'Targeted Python tests ({len(scripts_tests)} scripts + {len(engine_tests)} engine)',
            }
        )

    # Final fail-safe invariant: non-empty changes must produce at least one real check stage
    # unless every changed file was in IGNORED_NON_CODE_FILES.
    if not stages:
        stages.append(
            {
                'stage': 1,
                'name': 'lint_sanity',
                'kind': 'lint',
                'run_lint': True,
                'run_lint_tests': False,
                'description': 'Metadata-only change baseline: full-repo scripts/lint.py',
            }
        )

    plan['stages'] = stages
    return plan


def module_weight(rel: str) -> float:
    """Return the static LPT weight estimate (seconds) for a Python test module."""
    return MODULE_WEIGHT_HINTS.get(normalize_rel(rel), DEFAULT_MODULE_WEIGHT_S)


def bucket_weight(bucket: list[str]) -> float:
    """Return the aggregate static LPT weight estimate (seconds) for a module bucket."""
    return round(sum(module_weight(rel) for rel in bucket), 3)


def bucket_python_modules(modules: list[str], max_workers: int) -> list[list[str]]:
    """Pack Python test module paths into LPT-balanced buckets for parallel execution."""
    if not modules:
        return []
    workers = max(1, min(max_workers, len(modules)))
    weighted = sorted(
        modules,
        key=lambda rel: (-module_weight(rel), rel),
    )
    buckets: list[list[str]] = []
    light: list[tuple[float, str]] = []
    for rel in weighted:
        w = module_weight(rel)
        if w >= SOLO_BUCKET_THRESHOLD_S and workers > 1:
            buckets.append([rel])
        else:
            light.append((w, rel))

    if light:
        if workers <= 1:
            light_bucket_count = 1
        else:
            total_light = sum(w for w, _ in light)
            target_bins = max(1, math.ceil(total_light / LIGHT_BUCKET_TARGET_S))
            if not buckets:
                light_bucket_count = min(len(light), max(workers, target_bins))
            else:
                light_bucket_count = min(len(light), target_bins)
        light_bins: list[tuple[float, list[str]]] = [(0.0, []) for _ in range(light_bucket_count)]
        for w, rel in light:
            light_bins.sort(key=lambda item: (item[0], len(item[1])))
            load, items = light_bins[0]
            items.append(rel)
            light_bins[0] = (load + w, items)
        for _, items in sorted(light_bins, key=lambda item: (-item[0], item[1])):
            if items:
                buckets.append(items)
    buckets.sort(key=lambda items: (-bucket_weight(items), items))
    return buckets


def schedule_python_work_items(
    scripts_tests: list[str],
    engine_tests: list[str],
    max_workers: int,
) -> list[tuple[str, list[str]]]:
    """Build unified cross-suite buckets sorted in global LPT descending order."""
    work_items: list[tuple[str, list[str]]] = []
    for bucket in bucket_python_modules(scripts_tests, max_workers):
        work_items.append(('scripts', bucket))
    for bucket in bucket_python_modules(engine_tests, max_workers):
        work_items.append(('engine/python', bucket))
    work_items.sort(key=lambda item: (-bucket_weight(item[1]), item[0], item[1]))
    return work_items


def _run_cmd(
    command: list[str],
    *,
    cwd: Path,
    env: dict[str, str] | None = None,
    quiet_on_success: bool = False,
) -> dict[str, Any]:
    started = time.perf_counter()
    proc = subprocess.run(
        command,
        cwd=str(cwd),
        env=env,
        capture_output=quiet_on_success,
        text=True,
        check=False,
    )
    elapsed = round(time.perf_counter() - started, 3)
    output = ''
    if quiet_on_success and proc.returncode != 0:
        output = (proc.stdout or '') + (proc.stderr or '')
    return {
        'command': command,
        'exit_code': proc.returncode,
        'ok': proc.returncode == 0,
        'elapsed_s': elapsed,
        'output': output,
    }


def _run_python_bucket(
    python_bin: str,
    root: Path,
    suite_dir: str,
    module_paths: list[str],
) -> dict[str, Any]:
    mod_names = [Path(rel).stem for rel in module_paths]
    started = time.perf_counter()
    with tempfile.TemporaryDirectory(prefix='memlane-check-worker-') as worker_tmp:
        env = dict(
            os.environ,
            TMPDIR=worker_tmp + '/',
            TMP=worker_tmp,
            TEMP=worker_tmp,
            PYTHONDONTWRITEBYTECODE='1',
        )
        proc = subprocess.run(
            [python_bin, '-B', '-c', _UNITTEST_WORKER, str(root / suite_dir), *mod_names],
            cwd=str(root),
            env=env,
            capture_output=True,
            text=True,
            check=False,
        )
    elapsed = round(time.perf_counter() - started, 3)
    parsed: dict[str, Any] | None = None
    for line in reversed(proc.stdout.splitlines()):
        if line.startswith(RESULT_MARKER):
            try:
                parsed = json.loads(line[len(RESULT_MARKER):])
            except ValueError:
                parsed = None
            break
    if parsed is None:
        return {
            'suite_dir': suite_dir,
            'modules': mod_names,
            'tests_run': 0,
            'failures': 0,
            'errors': 1,
            'skipped': 0,
            'ok': False,
            'exit_code': proc.returncode or 1,
            'elapsed_s': elapsed,
            'output': (proc.stdout or '') + '\n' + (proc.stderr or ''),
        }
    parsed['suite_dir'] = suite_dir
    parsed['exit_code'] = proc.returncode
    parsed['ok'] = bool(parsed.get('ok')) and proc.returncode == 0
    parsed['wall_elapsed_s'] = elapsed
    if not parsed['ok']:
        parts: list[str] = []
        if parsed.get('output'):
            parts.append(str(parsed['output']))
        if proc.returncode != 0:
            parts.append(f'worker exited with non-zero code {proc.returncode}')
        if proc.stderr:
            parts.append(proc.stderr)
        parsed['output'] = '\n'.join(parts)
    return parsed


def _summarize_suite_group(results: list[dict[str, Any]], suite_dir: str) -> dict[str, Any]:
    group = [r for r in results if r.get('suite_dir') == suite_dir]
    ok = all(bool(r.get('ok')) and r.get('exit_code', 0) == 0 for r in group)
    return {
        'suite_dir': suite_dir,
        'bucket_count': len(group),
        'module_count': sum(len(r.get('modules') or []) for r in group),
        'tests_run': sum(int(r.get('tests_run', 0)) for r in group),
        'failures': sum(int(r.get('failures', 0)) for r in group),
        'errors': sum(int(r.get('errors', 0)) for r in group),
        'ok': ok,
        'elapsed_s': round(sum(float(r.get('elapsed_s', 0.0)) for r in group), 3),
    }


def run_python_suite_parallel(
    python_bin: str,
    root: Path,
    scripts_tests: list[str],
    engine_tests: list[str],
    *,
    jobs: int,
    json_mode: bool = False,
) -> dict[str, Any]:
    started = time.perf_counter()
    work_items = schedule_python_work_items(scripts_tests, engine_tests, jobs)

    if not work_items:
        return {
            'ok': True,
            'exit_code': 0,
            'elapsed_s': 0.0,
            'tests_run': 0,
            'workers': 0,
            'suites': {
                'scripts': _summarize_suite_group([], 'scripts'),
                'engine_python': _summarize_suite_group([], 'engine/python'),
            },
            'buckets': [],
        }

    worker_count = max(1, min(jobs, len(work_items)))
    results: list[dict[str, Any]] = []
    with ThreadPoolExecutor(max_workers=worker_count) as pool:
        future_map = {
            pool.submit(_run_python_bucket, python_bin, root, suite_dir, bucket): (suite_dir, bucket)
            for suite_dir, bucket in work_items
        }
        for fut in as_completed(future_map):
            res = fut.result()
            results.append(res)
            if not json_mode:
                status = 'PASS' if res['ok'] else 'FAIL'
                label = ','.join(res['modules'][:3])
                if len(res['modules']) > 3:
                    label += f',+{len(res["modules"]) - 3}'
                print(
                    f"  [{status}] {res['suite_dir']}: {label} "
                    f"({res['tests_run']} tests, {res['elapsed_s']:.2f}s)",
                    flush=True,
                )
                if not res['ok'] and res.get('output'):
                    print(res['output'], file=sys.stderr, flush=True)

    results.sort(key=lambda r: (r['suite_dir'], r['modules']))
    total_tests = sum(int(r.get('tests_run', 0)) for r in results)
    ok = all(bool(r.get('ok')) and r.get('exit_code', 0) == 0 for r in results)
    return {
        'ok': ok,
        'exit_code': 0 if ok else 1,
        'elapsed_s': round(time.perf_counter() - started, 3),
        'tests_run': total_tests,
        'workers': worker_count,
        'suites': {
            'scripts': _summarize_suite_group(results, 'scripts'),
            'engine_python': _summarize_suite_group(results, 'engine/python'),
        },
        'buckets': [
            {
                'suite_dir': r['suite_dir'],
                'modules': r['modules'],
                'tests_run': r['tests_run'],
                'failures': r['failures'],
                'errors': r['errors'],
                'ok': r['ok'],
                'exit_code': r.get('exit_code', 0),
                'elapsed_s': r['elapsed_s'],
            }
            for r in results
        ],
    }


def _resolve_app_executable(app_path: str) -> Path:
    path = Path(app_path).expanduser().resolve()
    if path.is_dir() and path.suffix == '.app':
        candidate = path / 'Contents/MacOS/Memlane'
        if candidate.is_file():
            return candidate
    return path


def _execute_release_smoke(
    python_bin: str,
    root: Path,
    app_path: str,
    *,
    json_mode: bool,
) -> dict[str, Any]:
    smoke_script = root / 'scripts/smoke_release_contracts.py'
    if not smoke_script.is_file():
        msg = f'release smoke script missing: {smoke_script}'
        if not json_mode:
            print(msg, file=sys.stderr)
        return {'ok': False, 'exit_code': 2, 'elapsed_s': 0.0, 'error': msg}

    started = time.perf_counter()
    with tempfile.TemporaryDirectory(prefix='memlane-release-smoke-bin-') as tmp_dir:
        if app_path:
            executable = _resolve_app_executable(app_path)
            if not executable.is_file():
                msg = f'release executable not found: {executable}'
                if not json_mode:
                    print(msg, file=sys.stderr)
                return {'ok': False, 'exit_code': 2, 'elapsed_s': round(time.perf_counter() - started, 3), 'error': msg}
        else:
            executable = Path(tmp_dir) / 'Memlane'
            build_res = _run_cmd(
                ['sh', str(root / 'scripts/go.sh'), 'build', '-o', str(executable), './cmd/memlane-app'],
                cwd=root,
                quiet_on_success=json_mode,
            )
            if not build_res['ok']:
                return {
                    'ok': False,
                    'exit_code': 1,
                    'elapsed_s': round(time.perf_counter() - started, 3),
                    'error': 'go build ./cmd/memlane-app failed',
                    'output': build_res.get('output', ''),
                }

        cmd = [python_bin, str(smoke_script), '--executable', str(executable), '--json']
        proc = subprocess.run(cmd, cwd=str(root), capture_output=True, text=True, check=False)
        elapsed = round(time.perf_counter() - started, 3)
        if not json_mode:
            if proc.stdout:
                print(proc.stdout.strip(), flush=True)
            if proc.stderr:
                print(proc.stderr.strip(), file=sys.stderr, flush=True)
        smoke_receipt: Any = None
        if proc.stdout.strip():
            try:
                smoke_receipt = json.loads(proc.stdout)
            except ValueError:
                smoke_receipt = None
        return {
            'ok': proc.returncode == 0,
            'exit_code': proc.returncode,
            'elapsed_s': elapsed,
            'command': cmd,
            'smoke_receipt': smoke_receipt,
            'stderr': proc.stderr.strip() if proc.returncode != 0 else '',
        }


def execute_stages(
    stages: list[dict[str, Any]],
    root: Path = ROOT,
    *,
    python_bin: str | None = None,
    jobs: int | None = None,
    json_mode: bool = False,
) -> tuple[int, list[dict[str, Any]]]:
    py = python_bin or resolve_python(root)
    workers = jobs or default_jobs()
    results: list[dict[str, Any]] = []

    for stage in stages:
        kind = stage['kind']
        name = stage['name']
        if not json_mode:
            print(f"==> Stage {stage['stage']}: {name} ({stage.get('description', '')})", flush=True)

        stage_start = time.perf_counter()
        if kind == 'lint':
            sub_results = []
            for hook in stage.get('hook_syntax_files', []):
                res = _run_cmd(['sh', '-n', str(root / hook)], cwd=root, quiet_on_success=json_mode)
                sub_results.append(res)
                if not res['ok']:
                    if not json_mode and res['output']:
                        print(res['output'], file=sys.stderr)
                    results.append({
                        'stage': stage['stage'], 'name': name, 'kind': kind,
                        'ok': False, 'exit_code': res['exit_code'],
                        'elapsed_s': round(time.perf_counter() - stage_start, 3),
                        'steps': sub_results,
                    })
                    return res['exit_code'] or 1, results
            if stage.get('run_lint'):
                res = _run_cmd([py, str(root / 'scripts/lint.py')], cwd=root, quiet_on_success=json_mode)
                sub_results.append(res)
                if not res['ok']:
                    if not json_mode and res['output']:
                        print(res['output'], file=sys.stderr)
                    results.append({
                        'stage': stage['stage'], 'name': name, 'kind': kind,
                        'ok': False, 'exit_code': res['exit_code'],
                        'elapsed_s': round(time.perf_counter() - stage_start, 3),
                        'steps': sub_results,
                    })
                    return res['exit_code'] or 1, results
            if stage.get('run_lint_tests'):
                res = _run_cmd(
                    [py, '-m', 'unittest', 'discover', '-s', 'scripts', '-p', 'test_lint.py'],
                    cwd=root,
                    quiet_on_success=json_mode,
                )
                sub_results.append(res)
                if not res['ok']:
                    if not json_mode and res['output']:
                        print(res['output'], file=sys.stderr)
                    results.append({
                        'stage': stage['stage'], 'name': name, 'kind': kind,
                        'ok': False, 'exit_code': res['exit_code'],
                        'elapsed_s': round(time.perf_counter() - stage_start, 3),
                        'steps': sub_results,
                    })
                    return res['exit_code'] or 1, results
            results.append({
                'stage': stage['stage'], 'name': name, 'kind': kind,
                'ok': True, 'exit_code': 0,
                'elapsed_s': round(time.perf_counter() - stage_start, 3),
                'steps': sub_results,
            })

        elif kind == 'go':
            go_sh = str(root / 'scripts/go.sh')
            sub_results = []
            commands: list[list[str]] = []
            vet_pkgs = stage.get('vet_packages') or []
            if vet_pkgs:
                commands.append(['sh', go_sh, 'vet', *vet_pkgs])
            test_pkgs = stage.get('test_packages') or []
            if test_pkgs:
                commands.append(['sh', go_sh, 'test', '-count=1', *test_pkgs])
            root_regex = stage.get('root_test_regex') or ''
            if root_regex and './...' not in test_pkgs and '.' not in test_pkgs:
                commands.append(['sh', go_sh, 'test', '-count=1', '.', '-run', root_regex])
            sd_regex = stage.get('simpledesktop_test_regex') or ''
            if sd_regex and './...' not in test_pkgs and './internal/simpledesktop' not in test_pkgs:
                commands.append(['sh', go_sh, 'test', '-count=1', './internal/simpledesktop', '-run', sd_regex])
            race_pkgs = stage.get('race_packages') or []
            if race_pkgs:
                commands.append(['sh', go_sh, 'test', '-race', '-count=1', *race_pkgs])
            for cmd in commands:
                res = _run_cmd(cmd, cwd=root, quiet_on_success=json_mode)
                sub_results.append(res)
                if not res['ok']:
                    if not json_mode and res['output']:
                        print(res['output'], file=sys.stderr)
                    results.append({
                        'stage': stage['stage'], 'name': name, 'kind': kind,
                        'ok': False, 'exit_code': res['exit_code'],
                        'elapsed_s': round(time.perf_counter() - stage_start, 3),
                        'steps': sub_results,
                    })
                    return res['exit_code'] or 1, results
            results.append({
                'stage': stage['stage'], 'name': name, 'kind': kind,
                'ok': True, 'exit_code': 0,
                'elapsed_s': round(time.perf_counter() - stage_start, 3),
                'steps': sub_results,
            })

        elif kind == 'python_parallel':
            py_res = run_python_suite_parallel(
                py,
                root,
                stage.get('scripts_tests') or [],
                stage.get('engine_tests') or [],
                jobs=workers,
                json_mode=json_mode,
            )
            results.append({
                'stage': stage['stage'], 'name': name, 'kind': kind,
                **py_res,
            })
            if not py_res['ok']:
                return py_res['exit_code'] or 1, results

        elif kind == 'release_smoke':
            smoke_res = _execute_release_smoke(
                py,
                root,
                stage.get('app_path', ''),
                json_mode=json_mode,
            )
            results.append({
                'stage': stage['stage'], 'name': name, 'kind': kind,
                **smoke_res,
            })
            if not smoke_res['ok']:
                # Strictly propagate smoke exit codes (1/2/3).
                return int(smoke_res['exit_code']), results
        else:
            raise ValueError(f'unknown stage kind: {kind}')

    return 0, results


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog='check.py',
        description='Memlane 统一本地门禁入口（改动感知 / 三阶段全量 / 发版冒烟 / pre-push）。',
    )
    mode = parser.add_mutually_exclusive_group(required=False)
    mode.add_argument(
        '--changed',
        nargs='?',
        const='',
        default=None,
        metavar='BASE_REF',
        help='按改动路径执行最小必跑门禁；可选传 git ref（默认检查工作区全部未提交改动，可配 --staged）',
    )
    mode.add_argument(
        '--pre-push',
        action='store_true',
        help='Git pre-push 门禁：全仓 lint.py + test_lint.py + 并行 scripts/test_*.py',
    )
    mode.add_argument(
        '--full',
        action='store_true',
        help='冻结后三阶段全量门禁（Stage 1 lint -> Stage 2 Go 独占 -> Stage 3 Python 并行）',
    )
    mode.add_argument(
        '--release',
        action='store_true',
        help='--full 后追加 Level 0 本地无 Mock 契约冒烟（透传退出码 0/1/2/3）',
    )
    parser.add_argument(
        '--staged',
        action='store_true',
        help='仅按已暂存改动（git diff --cached）执行改动感知门禁（可单用或与 --changed 同用）',
    )
    parser.add_argument(
        '--race-pkg',
        action='append',
        default=[],
        metavar='PKG',
        help='追加 go test -race 的目标包（可多次指定）',
    )
    parser.add_argument(
        '--app',
        default='',
        metavar='PATH',
        help='--release 模式下指定待测 .app 或二进制路径（省略则在 $TMPDIR 临时编译）',
    )
    parser.add_argument(
        '--jobs',
        type=int,
        default=default_jobs(),
        help='Python 并行 worker 数（默认 min(6, cpu//2)）',
    )
    parser.add_argument(
        '--dry-run',
        action='store_true',
        help='仅输出规划矩阵，不实际执行命令',
    )
    parser.add_argument(
        '--json',
        action='store_true',
        dest='json_output',
        help='以 JSON 结构输出门禁计划或回执',
    )
    return parser


def _reexec_under_task(argv: list[str], root: Path = ROOT) -> int:
    task_script = root / 'scripts/task.py'
    return subprocess.call(
        [sys.executable, str(task_script), '--', sys.executable, str(Path(__file__).resolve()), *argv],
        cwd=str(root),
    )


def main(
    argv: list[str] | None = None,
    *,
    root: Path = ROOT,
    allow_task_wrap: bool = True,
) -> int:
    raw_argv = list(sys.argv[1:] if argv is None else argv)
    parser = build_parser()
    args = parser.parse_args(raw_argv)

    if args.jobs < 1:
        parser.error('--jobs must be >= 1')
    has_mode = any((args.changed is not None, args.staged, args.pre_push, args.full, args.release))
    if not has_mode:
        parser.error('one of --changed, --staged, --pre-push, --full, --release is required')
    if args.staged and (args.pre_push or args.full or args.release):
        parser.error('--staged cannot be combined with --pre-push, --full, or --release')
    if args.staged and args.changed:
        parser.error('--staged cannot be combined with an explicit BASE_REF')

    # Ensure every non-dry-run execution honors scripts/task.py's 50/55 GiB disk guard.
    if allow_task_wrap and not args.dry_run and os.environ.get('MEMLANE_DISK_TASK') != '1':
        return _reexec_under_task(raw_argv, root=root)

    started = time.perf_counter()
    if args.staged:
        mode_name = 'staged'
        changes = collect_git_changes(root, staged=True)
        plan = plan_changed(changes, root=root, race_pkgs=args.race_pkg)
    elif args.changed is not None:
        mode_name = 'changed'
        changes = collect_git_changes(root, staged=False, base_ref=args.changed)
        plan = plan_changed(changes, root=root, race_pkgs=args.race_pkg)
    elif args.pre_push:
        mode_name = 'pre-push'
        plan = {
            'changed_files': [],
            'deleted_files': [],
            'no_changed_files': False,
            'forbidden_paths': [],
            'escalated_to_full': False,
            'escalation_reasons': [],
            'stages': build_pre_push_stages(root),
        }
    elif args.full:
        mode_name = 'full'
        plan = {
            'changed_files': [],
            'deleted_files': [],
            'no_changed_files': False,
            'forbidden_paths': [],
            'escalated_to_full': False,
            'escalation_reasons': [],
            'stages': build_full_stages(root, include_release=False, race_pkgs=args.race_pkg),
        }
    else:
        mode_name = 'release'
        plan = {
            'changed_files': [],
            'deleted_files': [],
            'no_changed_files': False,
            'forbidden_paths': [],
            'escalated_to_full': False,
            'escalation_reasons': [],
            'stages': build_full_stages(
                root,
                include_release=True,
                race_pkgs=args.race_pkg,
                app_path=args.app,
            ),
        }

    if plan['forbidden_paths']:
        receipt = {
            'schema_version': SCHEMA_VERSION,
            'mode': mode_name,
            'dry_run': args.dry_run,
            'ok': False,
            'exit_code': 1,
            'error': 'forbidden process artifact paths detected',
            **plan,
            'elapsed_s': round(time.perf_counter() - started, 3),
        }
        if args.json_output:
            print(json.dumps(receipt, ensure_ascii=False, indent=2))
        else:
            print(
                'check.py: 禁止包含过程文档目录产物（仅允许 docs/process/README.md）：'
                + ', '.join(plan['forbidden_paths']),
                file=sys.stderr,
            )
        return 1

    if args.dry_run:
        receipt = {
            'schema_version': SCHEMA_VERSION,
            'mode': mode_name,
            'dry_run': True,
            'ok': True,
            'exit_code': 0,
            'jobs': args.jobs,
            **plan,
            'elapsed_s': round(time.perf_counter() - started, 3),
        }
        if args.json_output:
            print(json.dumps(receipt, ensure_ascii=False, indent=2))
        else:
            print(f"check.py --{mode_name} --dry-run (jobs={args.jobs})")
            if plan['changed_files']:
                print(f"  changed ({len(plan['changed_files'])}): {', '.join(plan['changed_files'][:12])}"
                      + (f" (+{len(plan['changed_files']) - 12} more)" if len(plan['changed_files']) > 12 else ''))
            if plan['escalated_to_full']:
                print(f"  escalated_to_full: {', '.join(plan['escalation_reasons'])}")
            for stage in plan['stages']:
                print(f"  Stage {stage['stage']} [{stage['kind']}] {stage['name']}: {stage.get('description', '')}")
        return 0

    exit_code, stage_results = execute_stages(
        plan['stages'],
        root=root,
        python_bin=resolve_python(root),
        jobs=args.jobs,
        json_mode=args.json_output,
    )
    elapsed_total = round(time.perf_counter() - started, 3)
    receipt = {
        'schema_version': SCHEMA_VERSION,
        'mode': mode_name,
        'dry_run': False,
        'ok': exit_code == 0,
        'exit_code': exit_code,
        'jobs': args.jobs,
        'changed_files': plan['changed_files'],
        'deleted_files': plan['deleted_files'],
        'escalated_to_full': plan['escalated_to_full'],
        'escalation_reasons': plan['escalation_reasons'],
        'stages': stage_results,
        'elapsed_s': elapsed_total,
    }
    if args.json_output:
        print(json.dumps(receipt, ensure_ascii=False, indent=2))
    else:
        verdict = 'PASSED' if exit_code == 0 else f'FAILED (exit {exit_code})'
        print(f"check.py --{mode_name}: {verdict} in {elapsed_total:.2f}s", flush=True)
    return exit_code


if __name__ == '__main__':
    raise SystemExit(main())
