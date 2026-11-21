// 合成示例数据：形态模仿真实记忆（无独立标题、正文含代码路径、召回次数、官方分类），不代表真实记忆库。
// 同时挂载在 window.MEMLANE_DATA 与 window.DATA，供外壳与扩展页面共用。
window.MEMLANE_DATA = (() => {
  const projects = [
    { id: "memlane", name: "memlane", path: "~/work/AI/memlane" },
    { id: "item-fe", name: "item-fe", path: "~/work/shop/item-fe" },
    { id: "daily-cli", name: "daily-cli", path: "~/work/tools/daily-cli" },
    { id: "dcim-fe", name: "dcim-fe", path: "~/work/infra/dcim-fe" },
  ];
  const projectTotal = 8;

  const agents = {
    gemini: { name: "Antigravity", mark: "✦", color: "linear-gradient(135deg,#4F8DF7,#9B6CF2 55%,#F27B6C)", fg: "#fff" },
    codex: { name: "Codex", mark: "C", color: "#111", fg: "#fff" },
    claude: { name: "Claude Code", mark: "✳", color: "#D97757", fg: "#fff" },
    cursor: { name: "Cursor", mark: "⌘", color: "#2B2D38", fg: "#fff" },
    opencode: { name: "OpenCode", mark: "O", color: "#2563EB", fg: "#fff" },
    pi: { name: "pi", mark: "π", color: "#0D9488", fg: "#fff" },
    chatgpt: { name: "ChatGPT", mark: "G", color: "#10A37F", fg: "#fff" },
    perplexity: { name: "Perplexity", mark: "P", color: "#1F8A9C", fg: "#fff" },
  };

  const categories = ["项目状态", "踩坑经验", "项目约定", "代码位置与关联", "代码变更", "约束条件", "其他"];
  const rules = {
    "项目状态": "功能现状：记录该功能当前涉及的实现范围。",
    "踩坑经验": "踩坑经验：记录问题现象、根因与已验证的处理方式。",
    "项目约定": "项目约定：团队明确要求遵守的做法。",
    "代码位置与关联": "代码位置：记录实现所在文件及其调用关系。",
    "代码变更": "代码变更：记录一次修改的范围与原因。",
    "约束条件": "约束条件：实现时必须满足的限制。",
    "其他": "未归入以上分类的记忆。",
  };

  const M = (id, project, agent, cat, recall, date, body, source, unread = false) => ({ id, project, agent, cat, recall, date, body, source, unread, rule: rules[cat] });
  const memories = [
    M("m1", "memlane", "gemini", "项目状态", 1, "今天", "`engine/python/antigravity_hook_policy.py` 和 `engine/python/antigravity_plugin.py` 原子写入权限为 `0600`、仅含元数据的 `last-hook-error.json`，由 `engine/python/antigravity_connection.py` 转交 `internal/simpledesktop/agent_capture_ui.go` 展示，不放宽 Hook 策略。", "来源会话 · Antigravity · 今天 19:12"),
    M("m2", "memlane", "gemini", "代码位置与关联", 0, "今天", "`engine/python/lesson_provenance_patch.py` 在 `.local/daily/engine-components/` 下生成 Cognee 源码副本，中文提炼提示词和服务端补丁只应用于副本，不修改原始安装。", "来源会话 · Antigravity · 今天 18:40"),
    M("m3", "memlane", "gemini", "踩坑经验", 48, "今天", "`internal/simpledesktop/overview_ui_test.go` 的 `overviewFixture` 直接执行 `a.Update` 回调时与后台刷新发生数据竞争 → 同步回调可通过 `-race` 检查。", "来源会话 · Antigravity · 今天 17:55", true),
    M("m4", "memlane", "codex", "代码位置与关联", 1, "今天", "`engine/python/antigravity_hook_policy.py` 将 `COGNEE_SESSION_PREFIX` 设为 `codex`，匹配 `engine/python/connection_capture.py` 查询 ID 的 `codex_` 前缀；`codex_antigravity-` 前缀的历史会话仍按旧规则归档。", "来源会话 · Codex · 今天 16:20", true),
    M("m5", "memlane", "codex", "项目状态", 0, "今天", "应用入口接入 `internal/supervisor`，`internal/bootstrap/first_runtime.go` 将首次运行的进程、日志和轮询交由其管理，同时保留 `bootstrap` 公开 API、`.service-owner` 兼容性和 `scripts/dev.py` 开发流程。", "来源会话 · Codex · 今天 15:02", true),
    M("m6", "memlane", "gemini", "代码位置与关联", 0, "今天", "`scripts/dev.py` 日常模式启动 `internal/simpledesktop` 和三个回环服务：`engine/python/official_service_dev.py` 是知识与图谱的唯一数据源，`engine/python/project_control_server.py` 负责项目映射和访问控制，Agent 整理通道独立运行。", "来源会话 · Antigravity · 今天 14:31", true),
    M("m7", "memlane", "claude", "项目约定", 12, "今天", "未配置的模型选项保持未配置，不补客户端显式采样／输出默认值；用户显式填写的 `0` 必须保留。启动、验证和切换统一使用 `generation_config.native_llm_configuration`。", "来源会话 · Claude Code · 今天 11:08", true),
    M("m8", "memlane", "codex", "约束条件", 5, "昨天", "开发阶段至少保留 50 GiB 可用空间；所有验收／组件准备经 `python3 scripts/task.py -- <命令>` 执行，运行中余量低于 55 GiB 停止受管任务，禁止清空 `build/`。", "来源会话 · Codex · 昨天 21:40"),
    M("m9", "memlane", "claude", "踩坑经验", 3, "昨天", "MyGo 自绘循环在窗口遮挡时仍请求 `AnimationFrame()` 会持续占用 CPU；改为只在确有运动时请求下一帧，减少动态效果开启时直接跳到终态。", "来源会话 · Claude Code · 昨天 18:02", true),
    M("m10", "memlane", "gemini", "代码变更", 0, "11月18日", "`internal/simpledesktop/knowledge_waterfall.go` 卡片高度改为按正文行数与字段数预估，`cardHeight` 缓存宽度变化前的结果，避免瀑布流重排时跳动。", "来源会话 · Antigravity · 11月18日"),
    M("m11", "memlane", "codex", "其他", 0, "11月17日", "设置页按「记忆处理 → Agent 与模型 → 服务」组织，列表卡片外用 `Segmented` 切换处理来源，编辑与确认使用 `Modal`。", "来源会话 · Codex · 11月17日"),
    M("m12", "item-fe", "codex", "踩坑经验", 9, "今天", "并发下先查库存再扣减会产生竞争窗口；把条件写进 `UPDATE stock SET quantity = quantity - :count WHERE sku = :sku AND quantity >= :count`，按影响行数判断成功。", "来源会话 · Codex · 今天 10:58", true),
    M("m13", "item-fe", "claude", "项目约定", 2, "昨天", "统一使用 `pnpm` 管理依赖，`pnpm-lock.yaml` 随变更提交，CI 使用 `pnpm install --frozen-lockfile`。", "来源会话 · Claude Code · 昨天"),
    M("m14", "daily-cli", "gemini", "代码位置与关联", 0, "11月16日", "`cmd/daily/main.go` 解析子命令后交给 `internal/report/render.go`，Markdown 与终端输出共用同一份 `Section` 模型。", "来源会话 · Antigravity · 11月16日"),
    M("m15", "dcim-fe", "codex", "约束条件", 1, "11月15日", "机柜视图的 U 位坐标以底部为 1 起算，`src/rack/layout.ts` 中所有换算必须经过 `toRackUnit`，禁止直接用像素反推。", "来源会话 · Codex · 11月15日"),
  ];

  /* 原始记录（会话 / 文档）：使用可读会话主题，不暴露裸 UUID */
  const rawSessions = [
    {
      id: "raw-1", project: "memlane", agent: "gemini", kind: "session",
      title: "排查 Antigravity Hook 错误文件权限与写入原子性",
      summary: "确认 `last-hook-error.json` 使用 `0600` 原子写入，不放宽 Hook 策略并在 UI 呈现元数据摘要。",
      turns: 14, date: "今天 19:12",
      messages: [
        { role: "user", text: "检查 `antigravity_hook_policy.py` 在写入 `last-hook-error.json` 时是否保证 `0600` 权限与原子替换。" },
        { role: "assistant", text: "已定位到写入点，改用临时文件 `os.open(..., 0o600)` + `os.replace`，并确保不记录正文内容只保留错误码与时间戳。", tools: [{ name: "read_file", summary: "engine/python/antigravity_hook_policy.py", ok: true }, { name: "edit_file", summary: "改为 0600 原子写入", ok: true }] },
      ],
    },
    {
      id: "raw-2", project: "memlane", agent: "claude", kind: "session",
      title: "修复 MyGo 自绘循环窗口遮挡时持续请求 AnimationFrame",
      summary: "只在弹簧速度或位移未收敛时请求下一帧；当 `ReduceMotion` 为真时直接落到终态，CPU 占用回落至 0.4%。",
      turns: 9, date: "昨天 18:02",
      messages: [
        { role: "user", text: "窗口退到后台后 `memlane-app` 的 CPU 依然占 12%，排查 `AnimationFrame()` 触发条件。" },
        { role: "assistant", text: "发现空态漂浮块和指示条在静止时仍持续调用 `AnimationFrame()`，已加上活跃动画计数守卫并在 `ReduceMotion` 开启时立即结算。", tools: [{ name: "grep", summary: "AnimationFrame in internal/simpledesktop", ok: true }] },
      ],
    },
    {
      id: "raw-3", project: "memlane", agent: "codex", kind: "session",
      title: "将首次运行守护进程接入 internal/supervisor",
      summary: "保留 `.service-owner` flock 单实例独占锁和 `bootstrap` 公开 API，收敛重复轮询与日志截尾逻辑。",
      turns: 18, date: "今天 15:02",
      messages: [
        { role: "user", text: "把 `first_runtime.go` 的子进程拉起与健康检查统一到 `internal/supervisor`。" },
        { role: "assistant", text: "已完成迁移，进程组使用 `Setsid` 独立管理，20 MiB 活 inode 日志截尾脱敏保持兼容。", tools: [{ name: "run_command", summary: "sh scripts/go.sh test ./internal/supervisor/...", ok: true }] },
      ],
    },
    {
      id: "raw-4", project: "item-fe", agent: "codex", kind: "session",
      title: "商品扣减库存并发竞争条件修复方案",
      summary: "取消应用层先查后改，把库存下限校验下推到单条 SQL `WHERE quantity >= :count` 并校验受影响行数。",
      turns: 7, date: "今天 10:58",
      messages: [
        { role: "user", text: "秒杀压测时出现负库存，看下扣减事务里哪里有竞态窗口。" },
        { role: "assistant", text: "原代码先 `SELECT quantity` 再 `UPDATE`，并发请求会读到同一旧值。改为条件更新并检查 `RowsAffected == 1`。", tools: [{ name: "read_file", summary: "src/inventory/deduct.ts", ok: true }] },
      ],
    },
    {
      id: "raw-5", project: "memlane", agent: "gemini", kind: "doc",
      title: "视觉规范 V4 与剩余页面交互决议摘要",
      summary: "汇总 Q2–Q8：配置页去卡片改分组列表、⌘K 复用项目行样式、用户画像拆日期列、统一 v4Dialog 480/640。",
      turns: 1, date: "今天 09:30",
      messages: [
        { role: "assistant", text: "规范确立：仅用 tokens.css 变量，分组列表最大宽 680，弹窗删除类不绑定回车确认。" },
      ],
    },
  ];

  /* 用户画像合成数据（Q4：按「编码 Agent」和「其他 AI」分组，正文含二级标题与 `[日期] - …` 条目） */
  const profiles = [
    {
      id: "prof-antigravity",
      agent: "gemini",
      group: "编码 Agent",
      name: "Antigravity",
      status: "ready",
      method: "auto",
      canGenerate: true,
      updated: "2026-11-20",
      count: 10,
      body: `## 工程偏好与架构习惯
- [2026-11-20] - 优先从全局架构最优解出发，不被历史临时实现束缚；若与旧文档冲突会先指出差异再同步文档。
- [2026-11-18] - 强调上游 Cognee 零侵入原则，所有扩展一律放在外置网关、私有 SQLite WAL 或宿主插件层。
- [2026-11-14] - 界面组件重视像素级克制感与真实原生工具气质（参考 Things 3、Linear、Raycast），反对多余的边框卡片嵌套。
- [2026-11-09] - 遇到跨模块任务时倾向先梳理影响范围和验证顺序，再分阶段推进并保留可追溯证据。

## 协作方式与沟通风格
- [2026-11-19] - 默认使用简洁中文交流，汇报结论先给可执行的判断，再补关键路径与验证数据。
- [2026-11-16] - 要求严格区分「已验证事实」与「待核对假设」，不接受未实测就宣称通过。
- [2026-11-11] - 涉及删除运行包、修改本机配置或覆盖已有知识时，必须先列出影响范围并等待明确确认。

## 近期关注的技术主题
- [2026-11-20] - MyGo Native 客户端 V4 视觉与动效体系落地（帧耗时 < 16ms、切页堆内存 <= 25 MiB）。
- [2026-11-17] - 多 Coding Agent 电脑级接入与队列隔离，确保不同 Agent 的捕获与召回互不串扰。
- [2026-11-12] - 本地守护进程稳定性：单实例 flock 锁、活 inode 20 MiB 日志截尾脱敏与防抖健康检查。`,
    },
    {
      id: "prof-codex",
      agent: "codex",
      group: "编码 Agent",
      name: "Codex",
      status: "ready",
      method: "auto",
      canGenerate: true,
      updated: "2026-11-19",
      count: 5,
      body: `## 代码质量与测试约束
- [2026-11-19] - Go 改动完成后习惯同时跑 \`go test ./...\` 与 \`go vet ./...\`，并发代码重点验证 \`-race\`。
- [2026-11-17] - 零依赖静态检查规则（ML001–ML011）用来守住模型边界、环境变量隔离和 UI 性能红线。
- [2026-11-15] - 不喜欢为了凑测试通过而删减断言，坚持先查清根因再决定修实现还是同步更新契约。

## 工具链与运行习惯
- [2026-11-18] - 受管任务一律通过 \`python3 scripts/task.py -- <命令>\` 执行，严格遵守 50/55 GiB 磁盘水位保护。
- [2026-11-13] - 明确禁止清理 \`build/\` 目录，尤其保护 \`build/p3-prototype\` 里的未提交工作树。`,
    },
    {
      id: "prof-claude",
      agent: "claude",
      group: "编码 Agent",
      name: "Claude Code",
      status: "ready",
      method: "auto",
      canGenerate: true,
      updated: "2026-11-16",
      count: 3,
      body: `## 前端与交互细节
- [2026-11-16] - 关注键盘可达性与微交互反馈：列表支持 ↑↓ 选中、弹窗支持 Esc 取消，但危险删除动作刻意不绑定回车。
- [2026-11-14] - 动效遵循「有运动才请求下一帧」，在系统开启「减少动态效果」时立即直达终态。

## 模型配置原则
- [2026-11-12] - 未配置的采样与输出参数必须保持省略，绝不偷偷填充客户端默认值；用户显式填写的 \`0\` 必须原样透传。`,
    },
    {
      id: "prof-cursor",
      agent: "cursor",
      group: "编码 Agent",
      name: "Cursor",
      status: "failed",
      method: "auto",
      canGenerate: true,
      updated: "",
      count: 0,
      error: "Agent 登录失效（authentication_required）",
      body: "",
    },
    {
      id: "prof-chatgpt",
      agent: "chatgpt",
      group: "其他 AI",
      name: "ChatGPT",
      status: "ready",
      method: "manual",
      canGenerate: false,
      updated: "2026-11-15",
      count: 3,
      body: `## 长期技术背景
- [2026-11-15] - 熟悉 Go、Python 与 TypeScript 全栈开发，长期关注本地优先桌面应用与个人知识管理工具。
- [2026-11-10] - 设计文档偏好结构化表格与清晰的决策边界，避免冗长铺垫。

## 写作与表达偏好
- [2026-11-08] - 喜欢直接给出方案权衡与取舍依据，对空泛套话容忍度低。`,
    },
    {
      id: "prof-perplexity",
      agent: "perplexity",
      group: "其他 AI",
      name: "Perplexity",
      status: "empty",
      method: "manual",
      canGenerate: false,
      updated: "",
      count: 0,
      body: "",
    },
  ];

  const profilePrompt = `请基于你对我的历史对话印象，整理一份客观、克制的「用户画像」Markdown 文档：
1. 按二级标题（## 工程偏好与架构习惯、## 协作方式与沟通风格、## 近期关注的技术主题）分组；
2. 每条尽量用「- [YYYY-MM-DD] - 具体事实或稳定偏好」的格式写出；
3. 只写有依据的长期特征，不编造细节，不要包含敏感凭据或私钥。`;

  /* 服务状态快照（侧栏小浮层 + 设置 › 服务 页共用） */
  const serviceStatus = {
    overall: "ready", // "ready" | "warning" | "degraded" | "exited"
    label: "服务 · 已连接",
    checkedAt: "刚刚",
    components: [
      { id: "cognee", name: "Cognee 引擎", state: "ok", statusText: "已连接", metrics: "延迟 14 ms · PID 48210 · 内存 312 MB" },
      { id: "control", name: "控制面", state: "ok", statusText: "已连接", metrics: "8 个项目 · 最近请求 9 ms · 内存 64 MB" },
      { id: "agent-channel", name: "Agent 整理通道", state: "ok", statusText: "运行中", metrics: "Antigravity 默认 · 配置回执一致" },
      { id: "disk", name: "磁盘空间", state: "ok", statusText: "118.4 GiB 可用", metrics: "预警线 55 GiB · 日常库 1.2 GiB" },
    ],
    logs: [
      { level: "info", text: "19:36:12 [control] POST /cognee/search project=memlane hits=2 latency=11ms" },
      { level: "info", text: "19:35:48 [official] healthcheck ok rss=312MB latency=14ms" },
      { level: "warning", text: "19:35:04 [agent-channel] retry codex completion subprocess (attempt 1/2)" },
      { level: "error", text: "19:35:02 [agent-channel] SubprocessTransportError: transport closed before EOF" },
      { level: "info", text: "19:12:19 [control] receipt verified dataset=memlane items=4" },
      { level: "info", text: "19:00:01 [supervisor] disk free=118.4GiB threshold=55GiB status=ok" },
    ],
    diagnosticText: `Memlane Service Diagnostics (2026-11-20 19:36)
Cognee: ready | latency=14ms | pid=48210 | rss=312MB
Control: ready | projects=8 | recent=9ms | rss=64MB
AgentChannel: active=antigravity | receipt=verified
Disk: free=118.4GiB | threshold=55GiB`,
  };

  /* 项目管理默认权限（Q5：供 pages-settings / 项目「管理」标签共用） */
  const projectAccess = {
    memlane: { gemini: "rw", codex: "rw", claude: "ro", cursor: "none", opencode: "ro", pi: "none" },
    "item-fe": { gemini: "ro", codex: "rw", claude: "rw", cursor: "none", opencode: "none", pi: "none" },
    "daily-cli": { gemini: "rw", codex: "ro", claude: "none", cursor: "none", opencode: "none", pi: "none" },
    "dcim-fe": { gemini: "ro", codex: "rw", claude: "none", cursor: "ro", opencode: "none", pi: "none" },
  };

  /* 设置页合成默认配置（Q2 / Q6：供 pages-settings.js 使用） */
  const settings = {
    sourceMode: "cli", // "cli" | "provider"
    activeCliAgent: "gemini",
    activeProvider: "openai-compat",
    autoOrganize: true,
    autoRecall: true,
    sessionDateLock: true,
    graphToolMode: "summary", // "off" | "summary" | "full"
    cliAgents: [
      { id: "gemini", name: "Antigravity", model: "默认模型（由宿主决定）", connected: true, hookState: "ok", hookNote: "Hook 已接入 · 最近采集 今天 19:12" },
      { id: "codex", name: "Codex", model: "gpt-5-codex", connected: true, hookState: "ok", hookNote: "Hook 已接入 · 最近采集 今天 16:20" },
      { id: "claude", name: "Claude Code", model: "claude-sonnet-4", connected: true, hookState: "ok", hookNote: "Hook 已接入 · 最近采集 今天 11:08" },
      { id: "cursor", name: "Cursor", model: "未配置", connected: false, hookState: "fail", hookNote: "未接入 · 需在宿主安装插件" },
      { id: "opencode", name: "OpenCode", model: "默认模型", connected: true, hookState: "ok", hookNote: "Hook 已接入 · 仅只读项目" },
    ],
    providers: [
      { id: "openai-compat", name: "OpenAI Compatible", baseUrl: "https://api.example.com/v1", model: "gpt-4.1-mini", temperature: "0", configured: true },
      { id: "ollama", name: "Ollama（本地）", baseUrl: "http://127.0.0.1:11434", model: "qwen2.5:14b", temperature: "", configured: false },
    ],
  };

  /* 首次启动合成步骤与依赖（Q7：供 pages-setup.js 使用） */
  const setupSteps = [
    { id: "env", title: "环境检查", desc: "Python、磁盘空间与回环端口" },
    { id: "engine", title: "安装记忆引擎", desc: "官方 Cognee 1.6.7 源码校验与虚拟环境" },
    { id: "model", title: "配置记忆处理", desc: "选择用已连接的 Agent 整理或直接调用模型 API" },
    { id: "agents", title: "接入编码 Agent", desc: "自动发现本机 Coding Agents 并写入受管凭据" },
    { id: "ready", title: "启动日常服务", desc: "拉起 Cognee、控制面与 Agent 整理通道并验证健康状态" },
  ];

  const activity = [
    { kind: "recall", state: "ok", title: "Gemini App 自动召回", meta: "memlane · 2 条记忆", time: "19:36", project: "memlane", hits: ["m3", "m7"] },
    { kind: "fail", state: "fail", title: "全局 失败 ×3", meta: "全局 · SubprocessTransportError", time: "19:36", more: true },
    { kind: "fail", state: "fail", title: "memlane 失败 ×3", meta: "memlane · SubprocessTransportError", time: "19:35", more: true },
    { kind: "recall", state: "ok", title: "Gemini App 自动召回", meta: "memlane · 3 条记忆", time: "19:35", project: "memlane", hits: ["m3", "m9", "m8"] },
    { kind: "save", state: "ok", title: "整理完成", meta: "memlane · 4 条记忆", time: "19:12" },
    { kind: "recall", state: "ok", title: "Codex 自动召回", meta: "item-fe · 1 条记忆", time: "17:40", project: "item-fe", hits: ["m12"] },
    { kind: "save", state: "ok", title: "采集会话", meta: "memlane · Antigravity", time: "17:31" },
  ];
  const searches = [
    { state: "ok", title: "AnimationFrame CPU", meta: "memlane · 2 条记忆", time: "17:32" },
    { state: "ok", title: "库存 并发 扣减", meta: "全部项目 · 3 条记忆", time: "14:08" },
    { state: "ok", title: "COGNEE_SESSION_PREFIX", meta: "memlane · 1 条记忆", time: "昨天" },
  ];

  // 确定性伪随机热力分布（过去 365 天）
  const heat = {};
  let seed = 11;
  const rand = () => (seed = (seed * 16807) % 2147483647) / 2147483647;
  const today = new Date(); today.setHours(0, 0, 0, 0);
  const key = d => d.getFullYear() + "-" + String(d.getMonth() + 1).padStart(2, "0") + "-" + String(d.getDate()).padStart(2, "0");
  for (let i = 364; i >= 0; i--) {
    const d = new Date(today); d.setDate(d.getDate() - i);
    const ramp = 0.2 + 0.8 * ((364 - i) / 364);
    heat[key(d)] = rand() < 0.4 * ramp + (d.getDay() % 6 ? 0.08 : -0.12) ? 1 + Math.floor(rand() * rand() * 12 * ramp) : 0;
  }
  heat[key(today)] = 7;
  const y = new Date(today); y.setDate(y.getDate() - 1); heat[key(y)] = 3;

  return {
    projects,
    projectTotal,
    agents,
    categories,
    rules,
    memories,
    rawSessions,
    profiles,
    profilePrompt,
    serviceStatus,
    projectAccess,
    settings,
    setupSteps,
    activity,
    searches,
    heat,
    key,
    stats: { days: 128, memories: 467, raw: 83 },
  };
})();
window.DATA = window.MEMLANE_DATA;
