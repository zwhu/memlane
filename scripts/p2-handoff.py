#!/usr/bin/env python3
"""按仓库 handoff 技能生成系统临时目录中的 P2 检查点。"""
import argparse
import datetime
import subprocess
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
p = argparse.ArgumentParser(description=__doc__)
p.add_argument('phase')
p.add_argument('--result', required=True)
p.add_argument('--next', required=True)
a = p.parse_args()
skill = ROOT / '.trae/skills/handoff/SKILL.md'
assert 'name: handoff' in skill.read_text()
commit = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
status = subprocess.check_output(['git', 'status', '--short'], cwd=ROOT, text=True).strip() or '工作区干净'
folder = Path(tempfile.gettempdir()) / 'memlane-p2-handoffs'
folder.mkdir(parents=True, exist_ok=True)
slug = ''.join(c for c in a.phase if c.isalnum() or c in '-_')
path = folder / (datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%S.%fZ') + '-' + slug + '.md')
path.write_text(f"""# Memlane P2 交接：{a.phase}

## 目标与检查点
完整完成 Codex 保存项目经验 → App 查看并编辑 → pi 检索当前正文、来源和版本。
{a.result}
提交：{commit}
工作区：{status}

## 继续步骤
{a.next}

## 权威资料
仓库：{ROOT}。按 AGENTS.md、docs/product.md、docs/design.md、docs/technical-plan.md、docs/implementation-plan.md、docs/development.md、docs/research-agents.md 执行。
P1 基线为 0a39b40，证据见 docs/p1-validation.md 与 build/p1/final-audit.json；两张选定设计图已实际打开。P2 报告见 docs/p2-validation.md 与 build/p2/，代码、提交、截图不重复复制。

## suggested skills
- /handoff：{skill}。当前环境无 Skill 工具，本脚本实际读取技能并执行其临时交接产物要求。
- 难定位故障使用 diagnosing-bugs；新增或维护单元测试须执行 bits-unit-test-gen。

## 边界
只用隔离合成数据和 Agent 配置；不读取真实聊天，不提交凭据或本机配置。不扩展 P3 整理、代码图谱、TRAE/traex、远程管理或 VoiceOver 旁支。
保持主动停止语义，MCP/CLI 桥不自动启动服务。Agent 不能覆盖已有知识。只有持久化回执才报告保存；索引单列。每阶段验证后提交，最终推送并核对远端。缺少真实 Agent 运行条件时记录准确证据，协议模拟不等于真实 Agent 验收。
""", encoding='utf-8')
print(path)
