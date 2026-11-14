# 日常 Agent 使用连续证据准备（2026-11-11）

新增 `scripts/verify-daily-agent-usage.py`，默认只读准备，不启动服务、不调用模型。固定合成根标记、项目 `5ed9007e-4d9e-4e3e-b2cd-1921958e51e6`、pi 连接 `1339f6af-3a05-467c-98df-73e6d46315b2` generation 3；调用前后守护原 pi 配置和隔离配置 hash。需由验收人明确选定当前有效知识 ID/版本。

```sh
python3 scripts/task.py -- python3 scripts/verify-daily-agent-usage.py --evidence build/goal-concentrated-20261111-004 --memory <知识UUID> --revision <当前版本>
```

受控验收在服务已准备好后，由主验收人向同一命令追加 `--execute --out build/<全新报告目录>`，会真实调用已配置 pi/DeepSeek，一次最多180秒、4MiB输出，不自动重试。只提供检索、读取、采用三个工具，禁止读文件/命令/保存工具；采用相同参数重放一次检查幂等。守护进程组由包内 proposal guard 与父管道管理。

报告为输出目录的 `report.json`，只把结构化工具回执与只读 usage_events durable 行、usage 聚合差值和首页精确事件 ID 作为成功证据。必须同 project/knowledge ID/current revision、daily channel、当前连接授权与可信 workspace；拒绝模型文字成功、processing/verification 通道、缺 durable 行、伪造 exposure 或范围/版本漂移。成功要求 returned/read/adopted 各新增一次，知识正文/版本不改变，原配置不改变。

本批仅准备代码与7项模拟契约测试，受管测试46894 exit0；没有真实模型调用，没有 Native 操作，004当时停服。首页与使用详情的真实窗口连续证据仍待主验收人运行后补充，脚本报告始终标明 Native 未验证。不读取真实聊天记录，不保存原始provider stderr。
