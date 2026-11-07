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
    content = f'''# Memlane 交付 handoff：{a.phase}

## 生成方式
本脚本读取 {skill} 作为交接格式依据，直接生成文档；没有通过 Skill 工具调用 handoff。源技能禁止自动模型调用时也不修改其 frontmatter。交接只保存在系统临时目录。

## 目标与范围
完成本机 macOS 记忆客户端剩余开发、真实验收与交付准备。阶段覆盖栏内整理审核、完整整理动作、全局经验、计划与后台恢复、使用记录和初始化、日常 Agent 接入、代码图谱与正式分发。当前会话指令是总目标的授权来源；仓库仅保存实施与验收资料，不保存 Goal prompt。阶段通过或生成本交接后继续总目标。

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
- docs/p3-delivery-plan.md：完整切片、依赖、验收矩阵、剩余工作与外部条件。
- docs/p3-validation.md：历史实验、正式 C1 和后续切片的证据与能力边界。
- 下一阶段目标以用户当前会话指令和 --next 参数为准。
- build/p3/ 与 build/p3-c1/：历史实验和 C1 服务证据；后续证据以各切片验证文档列出的路径为准。
- docs/p0-validation.md、docs/p1-validation.md、docs/p2-validation.md：已通过基线；受影响部分重新验证。
不重复复制代码、规范、差异、模型输入或详细测试日志，只引用这些产物。

## suggested skills
- handoff：{skill}；只有用户显式调用或当前工具契约允许时才真正调用；否则用本脚本生成并注明方式。
- traex-guide：核对 TRAE CLI 安装版本、认证、参数和协议，按当前 skill catalog 定位。
- bits-code-guard：本地 diff 的产品、来源、版本、并发、迁移与恢复审查；按当前 skill catalog 定位。
- diagnosing-bugs：出现难定位故障时使用。
- bits-unit-test-gen：任务明确涉及单元测试生成/修改时按适用契约使用；与服务/协议集成测试区分。

## 持续约束
AGENTS.md 与产品规则持续有效。后台 Agent 仅返回结构化结果；新增有出处摘要/标签可按正式规则自动应用，改写、合并、覆盖、删除、跨项目移动及 AI 全局提炼需确认。版本、来源、范围、取消、幂等及索引门禁由受信本机服务执行。只用隔离合成验收库，不读取真实记忆/聊天，不提交凭据或本机 Agent 配置。采用选定 Rust worker 路线；未启用项目不扫描代码。每切片审查、验证、记录、提交后继续；最终普通推送授权分支，不强推。证书、公证、正式源与干净系统必须实证，缺必需项不能标记总目标完成。保留用户无关修改。
'''
    path.write_text(redact(content), encoding='utf-8')
    path.chmod(0o600)
    if not path.is_file() or path.stat().st_size == 0:
        raise SystemExit('handoff file verification failed')
    print(path)

if __name__ == '__main__':
    main()
