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
    if Path(path).name == 'user_profiles.py':
        for function in ast.walk(tree):
            if isinstance(function, ast.AsyncFunctionDef) and function.name == 'generate':
                for call in ast.walk(function):
                    if isinstance(call,ast.Call) and ((isinstance(call.func,ast.Attribute) and call.func.attr=='wait_for') or (isinstance(call.func,ast.Name) and call.func.id=='wait_for')):
                        report(call,'ML006','画像生成不能用总时长超时中断；观察进程/活动并提供明确停止')
    return sorted(set(issues))


GO_TARGETS = {
    'settings_model_cards.go': ('generationProfileCards',),
    'generation_workspace.go': ('chooseGenerationSource', 'selectGenerationProfile'),
    'generation_management.go': ('generationManagement', 'generationManagementContent'),
}


def check_go_source(source, path, *, require_all_targets=False):
    """Presentation architecture checks; Go behavior is covered by Native UI tests."""
    filename = Path(path).name
    if filename not in GO_TARGETS:
        return []
    issues = []
    targets = GO_TARGETS[filename]
    found = []
    for name in targets:
        start = source.find(' ' + name + '(')
        if start < 0:
            if require_all_targets:
                code = 'ML004' if name == 'generationProfileCards' else 'ML005'
                issues.append((1, code, f'未找到受保护函数 {name}；重命名或移动后须同步更新 lint 规则'))
            continue
        found.append(name)
        if name in ('chooseGenerationSource', 'selectGenerationProfile', 'generationProfileCards', 'generationManagement', 'generationManagementContent'):
            end = source.find('\nfunc ', start + 1)
            function = source[start:end if end >= 0 else len(source)]
            for match in re.finditer(r'openGenerationHandoff\(|startGenerationHandoff\(|Generation\.Active\s*=', function):
                issues.append((source[:start + match.start()].count('\n') + 1, 'ML005', '选择或编辑配置不能启用；保存、验证与明确启用分开'))
    if not require_all_targets and not found:
        first = targets[0]
        code = 'ML004' if first == 'generationProfileCards' else 'ML005'
        issues.append((1, code, f'未找到受保护函数 {first}；重命名或移动后须同步更新 lint 规则'))
    if filename == 'generation_workspace.go':
        return issues
    start = source.find('func (a *App) generationProfileCards(')
    if start < 0:
        return issues
    end = source.find('\nfunc ', start + 1)
    body = source[start:end if end >= 0 else len(source)]
    for match in re.finditer(r'\.Profile\.(?:Model|Provider|BridgeKind)\b|\.Generation\.Profiles\b', body):
        line = source[:start + match.start()].count('\n') + 1
        issues.append((line, 'ML004', '配置列表必须使用统一分组投影；Agent按身份展示，Provider按连接分组'))
    if 'generationWorkspaceGroups(' not in body and 'generationProfileGroups(' not in body:
        issues.append((source[:start].count('\n') + 1, 'ML004', '配置列表必须使用 generationProfileGroups'))
    return issues


def main():
    failures = 0
    paths = sorted((ROOT / 'engine/python').glob('*.py'))
    for path in paths:
        if path.name.startswith('test_'):
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
        for filename, targets in GO_TARGETS.items():
            expected_path = go_dir / filename
            if not expected_path.is_file():
                code = 'ML004' if 'generationProfileCards' in targets else 'ML005'
                print('{}:1: {} 未找到受保护文件 {}；重命名或移动后须同步更新 lint 规则'.format(
                    expected_path.relative_to(ROOT), code, filename))
                failures += 1
        for path in sorted(go_dir.glob('*.go')):
            if path.name.endswith('_test.go'):
                continue
            for line, code, message in check_go_source(path.read_text(encoding='utf-8'), path, require_all_targets=True):
                print('{}:{}: {} {}'.format(path.relative_to(ROOT), line, code, message))
                failures += 1
    if not failures:
        print('Lint passed: 模型参数与桥接 / Provider 展示边界')
    return bool(failures)


if __name__ == '__main__':
    sys.exit(main())
