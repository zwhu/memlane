#!/bin/sh
set -eu

REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$REPO_ROOT"

chmod +x \
    .githooks/_chain_global.sh \
    .githooks/pre-commit \
    .githooks/pre-push \
    .githooks/commit-msg \
    .githooks/prepare-commit-msg \
    .githooks/pre-merge-commit \
    scripts/install-hooks.sh

git config --local core.hooksPath .githooks

echo "已仅在当前仓库启用局部 .githooks（git config --local core.hooksPath .githooks，不修改全局 ~/.gitconfig，不影响其他受管仓库）："
echo "  - pre-commit: 按暂存快照运行 lint.py（修改 lint 规则或单测时同步运行 test_lint.py），并透明串联全局 pre-commit"
echo "  - pre-push: 运行全仓 lint.py 与 scripts/test_*.py，并透明串联全局 pre-push"
echo "  - commit-msg / prepare-commit-msg / pre-merge-commit: 透明转发至已有全局对应 hook"
echo "如需停用局部 hook 配置，可执行: git config --local --unset core.hooksPath"
