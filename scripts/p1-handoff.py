#!/usr/bin/env python3
"""Execute the repository handoff skill and write a P1 checkpoint to OS temp."""
import argparse,datetime,subprocess,tempfile
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]
p=argparse.ArgumentParser(description=__doc__);p.add_argument('phase');p.add_argument('--result',required=True);p.add_argument('--next',required=True);a=p.parse_args()
skill=ROOT/'.trae/skills/handoff/SKILL.md'
assert 'name: handoff' in skill.read_text()
commit=subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT,text=True).strip()
status=subprocess.check_output(['git','status','--short'],cwd=ROOT,text=True).strip() or '工作区干净'
folder=Path(tempfile.gettempdir())/'memlane-p1-handoffs';folder.mkdir(parents=True,exist_ok=True)
slug=''.join(c for c in a.phase if c.isalnum() or c in '-_')
path=folder/(datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%S')+'-'+slug+'.md')
path.write_text(f"""# Memlane P1 交接：{a.phase}

## 当前目标
完整执行 P1.1–P1.4，App 内新增项目记忆 → 查看 → 编辑 → 搜索当前版本 → 历史 → 恢复 → 重启读取；首页统计、实际系统截图、受影响测试和完整客户端构建通过。每阶段验证后提交，最终推送。只用合成数据，不操作真实 Agent 配置，不扩大 P2–P4。

## 检查点
{a.result}
提交：{commit}
工作区状态：
{status}

## 明确的继续步骤
{a.next}

## 权威资料与验证产物
仓库：{ROOT}；已读 AGENTS.md、docs/product.md、docs/design.md、docs/technical-plan.md、docs/implementation-plan.md、docs/p0-validation.md、docs/development.md。两张选定设计 JPG 已实际打开。按 docs/p1-validation.md 与实施清单核对完成度，不以夹具或编译代替真实 App 管理闭环。当前报告/截图见 build/p1-screenshots/；P0 未受影响的能力引用 docs/p0-validation.md 与 build/p0-engine/，不重复旁支验收。已有源码、diff、文档与截图不在这里复制。

## suggested skills
- /handoff：{skill}。本脚本实际读取技能并按要求写入系统临时目录，随后继续 Goal。
- 出现难定位故障时按 diagnosing-bugs；如果新增或维护单元测试，按 bits-unit-test-gen 的完整流程；当前窗口与服务接口检查不是单元测试。

## 范围与敏感数据
保留用户无关修改。不要提交真实数据库、聊天、Agent 配置或密钥。恢复产生新版本；过期 revision 不得覆盖当前内容；保存与索引独立展示。Agent、AI 整理、代码图谱和远程管理仍留后续。
""",encoding='utf-8')
print(path)
