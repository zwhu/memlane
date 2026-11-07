#!/usr/bin/env python3
"""Execute the user-requested local handoff skill and write its required artifact."""
import argparse
from datetime import datetime, timezone
from pathlib import Path
import tempfile

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("step")
    parser.add_argument("focus")
    parser.add_argument("--result", default="本步骤尚未验收。")
    args = parser.parse_args()
    if not args.step or any(c not in "abcdefghijklmnopqrstuvwxyz0123456789-_" for c in args.step):
        parser.error("step must be a lowercase filename slug")
    skill = ROOT / ".trae/skills/handoff/SKILL.md"
    instructions = skill.read_text()
    if "name: handoff" not in instructions:
        raise SystemExit("handoff skill missing or renamed")
    directory = Path(tempfile.gettempdir()) / "memlane-p0-handoffs"
    directory.mkdir(parents=True, exist_ok=True)
    stamp = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%S.%fZ")
    path = directory / (stamp + "-" + args.step + ".md")
    # User-facing arguments contain only task summaries, not memory content,
    # login state or credentials. Refer to authoritative artifacts, not copies.
    path.write_text(f"""# Memlane P0 handoff：{args.step}

## 下一步用途
{args.focus}

## 当前结果与遗留问题
{args.result}

## 权威资料
仓库：{ROOT}。
规则：AGENTS.md、docs/product.md、docs/design.md。
实现与验收：docs/technical-plan.md、docs/implementation-plan.md、docs/p0-validation.md。
逐步命令与结果：docs/p0-execution.md；实际代码状态用 git diff 和未提交文件核对，不复制已有产物。
合成验证报告：build/p0-engine/；后续产物由对应验证脚本指定。
保留原有用户修改，不读真实记忆、聊天、Agent 配置或凭据。只验收实际调用结果，不降低失败条件。恢复旧版本需产生新版本，AI 待确认操作不得生效。

## suggested skills
每小步、修复及重试均执行 {skill}。如果环境有 Skill 工具，显式调用 handoff；当前环境按读取本 skill 并生成其规定交接文档执行，不能声称调用不存在的工具。
继续按 A 原文索引、B 隔离版本、C worker、D 组件更新生命周期、E 其余验收与体积的依赖推进；不重复已通过且未受改动影响的检查。
""", encoding="utf-8")
    print("Read skill:", skill)
    print("Created handoff:", path)


if __name__ == "__main__":
    main()
