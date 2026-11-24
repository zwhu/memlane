#!/bin/sh
set -eu

REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$REPO_ROOT"

chmod +x \
    .githooks/_chain_global.sh \
    .githooks/_identity_guard.sh \
    .githooks/pre-commit \
    .githooks/pre-push \
    .githooks/commit-msg \
    .githooks/prepare-commit-msg \
    .githooks/pre-merge-commit \
    scripts/install-hooks.sh

git config --local core.hooksPath .githooks
# 本仓库提交身份固定为 zwhu；只写仓库级配置，独立副本 / Agent 工作区执行本脚本即可对齐。
git config --local user.name zwhu
git config --local user.email 331946763@qq.com

echo "已仅在当前仓库启用局部 .githooks（git config --local core.hooksPath .githooks，不修改全局 ~/.gitconfig，不影响其他受管仓库）："
echo "  - 提交身份: 写入仓库级 user.name=zwhu / user.email=331946763@qq.com；pre-commit / pre-merge-commit 校验当前身份，pre-push 校验全部待推送提交与标签"
echo "  - pre-commit: 按暂存快照运行 lint.py（修改 lint 规则或单测时同步运行 test_lint.py），并透明串联全局 pre-commit"
echo "  - pre-push: 运行全仓 lint.py 与 scripts/test_*.py，并透明串联全局 pre-push"
echo "  - commit-msg / prepare-commit-msg / pre-merge-commit: 透明转发至已有全局对应 hook"
echo "如需停用局部 hook 配置，可执行: git config --local --unset core.hooksPath"
