#!/usr/bin/env python3
"""按 handoff 文档格式直接生成 P3 临时交接，不调用禁止自动调用的 skill。"""
import argparse
from datetime import datetime, timezone
from pathlib import Path
import re
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]

def redact(text):
    return re.sub(r'(?i)(Bearer\s+\S+|sk-[A-Za-z0-9_-]+|(?:api[_-]?key|password|access_token|secret)\s*[=:]\s*\S+)', '[redacted]', text)

def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('phase', help='当前切片或检查点')
    p.add_argument('--result', required=True, help='简短结果和证据路径，不传正文或凭据')
    p.add_argument('--next', required=True, help='下一步具体任务或阻塞解除条件')
    a = p.parse_args()
    skill = ROOT / '.trae/skills/handoff/SKILL.md'
    instructions = skill.read_text()
    if 'name: handoff' not in instructions:
        raise SystemExit('handoff skill missing or renamed')
    sha = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
    branch = subprocess.check_output(['git', 'branch', '--show-current'], cwd=ROOT, text=True).strip()
    status = subprocess.check_output(['git', 'status', '--short'], cwd=ROOT, text=True).strip() or '工作区干净'
    folder = Path(tempfile.gettempdir()) / 'memlane-p3-handoffs'
    folder.mkdir(parents=True, exist_ok=True, mode=0o700)
    slug = ''.join(c for c in a.phase if c.isalnum() or c in '-_')
    if not slug:
        p.error('phase must contain a filename-safe character')
    path = folder / (datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%S.%fZ') + '-' + slug + '.md')
    content = f'''# Memlane P3 handoff：{a.phase}

## 生成方式
本脚本读取 {skill} 作为交接格式依据，直接生成文档；没有通过 Skill 工具调用 handoff。源技能禁止自动模型调用时也不修改其 frontmatter。交接只保存在系统临时目录。

## 当前检查点
仓库：{ROOT}
分支：{branch}
提交：{sha}
结果：{a.result}
工作区：
{status}

## 下一步与阻塞
{a.next}
恢复时先核对真实工作区、HEAD 与证据，再推进；不把旧记录当成当前状态。

## 权威资料
- AGENTS.md、docs/product.md、docs/design.md：确认规则。
- docs/technical-plan.md、docs/implementation-plan.md、docs/p3-architecture.md、docs/p3-technical-analysis.md：实现与边界。
- docs/p3-validation.md、build/p3/goal-completion-audit.json：已通过的隔离实验。
- 下一阶段目标以用户当前会话指令和 --next 参数为准。
- build/p3/：真实 traex 正常/应用/取消、隔离、Rust 重试与全仓检查。对应生产切片新增报告应在验证文档列明。
不重复复制代码、规范、差异、模型输入或详细测试日志，只引用这些产物。

## suggested skills
- handoff：{skill}；只有用户显式调用或当前工具契约允许时才真正调用；否则用本脚本生成并注明方式。
- traex-guide：核对 TRAE CLI 安装版本、认证、参数和协议，按当前 skill catalog 定位。
- diagnosing-bugs：出现难定位故障时使用。
- bits-unit-test-gen：任务明确涉及单元测试生成/修改时按适用契约使用；与服务/协议集成测试区分。

## 持续约束
后台 Agent 仅返回提案，合并需明确确认；任一目标过期则整项拒绝。取消不撤销已提交事实，重复回执只返回一次应用。只用合成验收库，不读取真实记忆/聊天，不提交凭据或本机 Agent 配置。P3-C1 不扩展 UI、调度、排名、初始化、代码图谱或其他 Agent；不替换 Rust worker/本地模型。验收与提交按当前用户授权范围执行，保留用户无关改动。
'''
    path.write_text(redact(content), encoding='utf-8')
    path.chmod(0o600)
    print(path)

if __name__ == '__main__':
    main()
