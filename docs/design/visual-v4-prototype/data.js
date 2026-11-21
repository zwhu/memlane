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
    M("m6", "memlane", "gemini", "代码位置与关联", 0, "今天", "`scripts/dev.py` 日常模式启动 `internal/simpledesktop` 和三个回环服务：`engine/python/official_service_dev.py` 是知识与图谱的唯一数据源，`engine/python/project_control_server.py` 负责项目映射和访问控制，Agent 模型代理独立运行。", "来源会话 · Antigravity · 今天 14:31", true),
    M("m7", "memlane", "claude", "项目约定", 12, "今天", "未配置的模型选项保持未配置，不补客户端显式采样／输出默认值；用户显式填写的 `0` 必须保留。启动、验证和切换统一使用 `generation_config.native_llm_configuration`。", "来源会话 · Claude Code · 今天 11:08", true),
    M("m8", "memlane", "codex", "约束条件", 5, "昨天", "开发阶段至少保留 50 GiB 可用空间；所有验收／组件准备经 `python3 scripts/task.py -- <命令>` 执行，运行中余量低于 55 GiB 停止受管任务，禁止清空 `build/`。", "来源会话 · Codex · 昨天 21:40"),
    M("m9", "memlane", "claude", "踩坑经验", 3, "昨天", "MyGo 自绘循环在窗口遮挡时仍请求 `AnimationFrame()` 会持续占用 CPU；改为只在确有运动时请求下一帧，减少动态效果开启时直接跳到终态。", "来源会话 · Claude Code · 昨天 18:02", true),
    M("m10", "memlane", "gemini", "代码变更", 0, "11月18日", "`internal/simpledesktop/knowledge_waterfall.go` 卡片高度改为按正文行数与字段数预估，`cardHeight` 缓存宽度变化前的结果，避免瀑布流重排时跳动。", "来源会话 · Antigravity · 11月18日"),
    M("m11", "memlane", "codex", "其他", 0, "11月17日", "设置页按「记忆提炼 → Agent 与模型 → 服务」组织，列表卡片外用 `Segmented` 切换提炼来源，编辑与确认使用 `Modal`。", "来源会话 · Codex · 11月17日"),
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

  /* 用户画像合成数据（按「编码 Agent」和「其他 AI」分组；各来源保留原始 Markdown 格式，展示层统一按 V4 Markdown 排版） */
  const profiles = [
    {
      id: "prof-antigravity",
      agent: "gemini",
      group: "编码 Agent",
      name: "Antigravity",
      status: "ready",
      method: "auto",
      canGenerate: true,
      updated: "2026-11-21",
      body: `## 协作指令
- 默认使用简洁中文交流，先给可执行的判断结论，再补关键文件路径与验证数据。
- 严格区分**已验证事实**与**待核对假设**，未实测的改动不得宣称通过。
- 涉及删除运行包、修改本机配置或覆盖已有知识时，先列出影响范围并等待明确确认。

## 个人身份
- 长期从事 macOS 本地优先桌面工具与全栈工程开发，同时维护 Go、Python 与前端交互系统。

## 职业经历
- 具有多语言客户端架构重构与多 Coding Agent 接入编排经验，重视工程闭环与可回溯证据。

## 核心项目
- 正在推进 **Memlane**（MyGo Native 桌面端 + 官方 Cognee 1.6.7 引擎），重点落地 V4 视觉与动效体系（帧耗时 \`< 16ms\`、切页堆内存 \`<= 25 MiB\`）。
- 在 \`engine/python/cognee_extension_gateway.py\` 外置网关层维护电脑级多 Agent 接入与队列隔离。
- 维护原生守护进程稳定性：单实例 \`flock\` 锁、活 inode 20 MiB 日志截尾脱敏与防抖健康检查。

## 通用偏好
- 优先从全局架构最优解出发，不被历史临时实现束缚；严守上游 Cognee 零侵入红线。
- 界面组件重视像素级克制感与真实原生工具气质（参考 Things 3、Linear、Raycast），反对多余的边框卡片嵌套。`,
    },
    {
      id: "prof-codex",
      agent: "codex",
      group: "编码 Agent",
      name: "Codex",
      status: "ready",
      method: "auto",
      canGenerate: true,
      updated: "2026-11-20",
      body: `## Instructions
- Always run \`sh scripts/go.sh test ./...\` and \`sh scripts/go.sh vet ./...\` after Go edits, with \`-race\` on concurrent code paths.
- Never weaken or delete assertions to make tests pass; diagnose the root cause first.
- Keep responses concise and report exact file paths and command output.

## Codebase Habits
- Prefers zero-dependency static gates (\`scripts/lint.py\`, rules \`ML001\`–\`ML017\`) to guard model parameter boundaries, env isolation, and UI perf budgets.
- Runs all managed tasks through \`python3 scripts/task.py -- <cmd>\` to respect the 50/55 GiB disk watermark.
- Explicitly forbids cleaning \`build/\`, especially \`build/p3-prototype\`.

\`\`\`bash
python3 scripts/task.py -- python3 scripts/lint.py
\`\`\`

## Active Projects
- **memlane**: macOS native memory client with MyGo UI (\`internal/simpledesktop/\`), native supervisor (\`internal/supervisor/\`), and external Python control plane.`,
    },
    {
      id: "prof-claude",
      agent: "claude",
      group: "编码 Agent",
      name: "Claude Code",
      status: "ready",
      method: "auto",
      canGenerate: true,
      updated: "2026-11-18",
      body: `这位开发者在桌面端交互和模型配置边界上非常严谨，倾向于用最小的视觉元素表达最清晰的状态层级。

### 交互与动效习惯
1. 列表与弹层必须具备完整的键盘可达性：支持 \`↑↓\` 选中、\`Esc\` 关闭，但**删除类危险弹窗刻意不绑定回车确认**。
2. 自绘动效坚持「有运动才请求下一帧」，系统开启「减少动态效果」时立即直达终态，避免后台空耗 CPU。
3. 常用操作使用短促弹簧（\`Snappy\`），低频反馈（如新记忆入库）才使用更有弹性但克制的 \`Bouncy\`。

### 模型参数边界
> 未配置的采样与输出参数必须保持省略，绝不偷偷填充客户端默认值；用户显式填写的 \`0\` 必须原样透传。

在修改模型设置或热切换路径时，会习惯先核对 \`generation_config.native_llm_configuration\` 是否与服务端回执完全一致。`,
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
      body: `你是一位偏爱本地优先架构与原生工具体验的全栈工程师，日常在 Go、Python 与 TypeScript 之间切换，对信息架构的清晰度和长期可维护性要求很高。

- 讨论设计方案时喜欢直接看到*权衡与取舍依据*，对空泛铺垫容忍度低。
- 写文档偏好结构化的小节、清晰的边界说明和可直接落地的检查项。
- 更信任可复现的实测证据与截图，而不是口头上的「应该没问题」。`,
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
      body: "",
    },
  ];

  const profilePrompt = `请基于你对我的历史对话与记忆，整理一份客观、克制的「用户画像」Markdown 文档：
1. 建议按以下 5 个二级标题组织内容：
   - ## 协作指令
   - ## 个人身份
   - ## 职业经历
   - ## 核心项目
   - ## 通用偏好
2. 如果某个分组没有可靠信息，直接省略该分组，不要写空标题或占位句。
3. 尽量保留用户原话与准确术语，不要编造或过度推断，不要包含敏感凭据或私钥。
4. 使用清晰自然的 Markdown 段落或列表即可，无需强行添加固定日期前缀。
5. 请将整份结果包裹在单个 Markdown 代码块中输出，方便直接复制导入。`;

  /* 服务状态快照（侧栏小浮层 + 设置 › 服务 页共用） */
  const serviceStatus = {
    overall: "ready", // "ready" | "warning" | "degraded" | "exited"
    label: "服务 · 已连接",
    checkedAt: "刚刚",
    components: [
      { id: "cognee", name: "记忆引擎（Cognee）", state: "ok", statusText: "已连接", metrics: "延迟 14 ms · PID 48210 · 内存 312 MB" },
      { id: "control", name: "接入网关", state: "ok", statusText: "已连接", metrics: "8 个项目 · 最近请求 9 ms · 内存 64 MB" },
      { id: "agent-channel", name: "Agent 模型代理", state: "ok", statusText: "运行中", metrics: "Antigravity 默认 · 配置回执一致" },
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
    { id: "model", title: "配置记忆提炼", desc: "选择用已连接的 Agent 提炼或直接调用模型 API" },
    { id: "agents", title: "接入编码 Agent", desc: "自动发现本机 Coding Agents 并写入受管凭据" },
    { id: "ready", title: "启动日常服务", desc: "拉起记忆引擎（Cognee）、接入网关与 Agent 模型代理并验证健康状态" },
  ];

  const activity = [
    { kind: "recall", state: "ok", title: "Gemini App 自动召回", meta: "memlane · 2 条记忆", time: "19:36", project: "memlane", hits: ["m3", "m7"] },
    { kind: "fail", state: "fail", title: "全局 失败 ×3", meta: "全局 · SubprocessTransportError", time: "19:36", more: true },
    { kind: "fail", state: "fail", title: "memlane 失败 ×3", meta: "memlane · SubprocessTransportError", time: "19:35", more: true },
    { kind: "recall", state: "ok", title: "Gemini App 自动召回", meta: "memlane · 3 条记忆", time: "19:35", project: "memlane", hits: ["m3", "m9", "m8"] },
    { kind: "save", state: "ok", title: "提炼完成", meta: "memlane · 4 条记忆", time: "19:12" },
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

  /* 项目消息与采集状态（供侧栏黄点、项目「管理」标签、macOS 状态栏浮层共用） */
  const projectMessages = {
    memlane: {
      autoSave: true,
      notices: [
        {
          id: "msg-memlane-replay",
          group: "action",
          title: "会话消息待确认补交",
          tag: "待补交",
          agent: "gemini",
          time: "今天 19:24",
          summary: "上次 Antigravity 会话因服务重启中断，有 3 条本地消息尚未确认入库，检查摘要后可手动补交。",
          primaryAction: { id: "replay-1", label: "查看并补交", kind: "replay" },
          ack: false,
          details: {
            sessionLabel: "排查 Antigravity Hook 错误文件权限与写入原子性",
            sessionId: "ag_20261121_1912_8f3a",
            diagCode: "RECEIPT_PARTIAL_PENDING",
            safetyNote: "状态未知的消息不会自动重发，避免写入重复记忆；请核对下方本地摘要后再决定是否补交。",
            messages: [
              { index: 1, kind: "用户指令", status: "pending", summary: "检查 `antigravity_hook_policy.py` 写入 `last-hook-error.json` 时是否保证 `0600` 权限与原子替换。" },
              { index: 2, kind: "助手结论", status: "unknown", summary: "已改用 `os.open(..., 0o600)` 配合 `os.replace` 原子写入，只记录错误码与时间戳，不落盘会话正文。该条因回执超时处于未知状态，请在项目记忆中核对后再补交。" },
              { index: 3, kind: "工具调用", status: "filtered", summary: "`read_file` 与 `edit_file` 原始输出超过阈值，已按脱敏规则压缩为元数据摘要。" },
            ],
          },
        },
        {
          id: "msg-memlane-retry",
          group: "error",
          title: "提炼任务回执核对超时",
          tag: "回执异常",
          agent: "codex",
          time: "今天 16:28",
          summary: "Codex 会话批次提炼时子进程提前断开，本地收据未收到引擎确认，可重试核对收据状态。",
          primaryAction: { id: "retry-1", label: "重试核对", kind: "retry" },
          ack: false,
          details: {
            sessionLabel: "将首次运行守护进程接入 internal/supervisor",
            sessionId: "codex_20261121_1502_4c91",
            diagCode: "COGNEE_RECEIPT_TIMEOUT",
            safetyNote: "重试核对仅向本地接入网关查询该批次的写入回执，不会重复调用模型或产生额外扣费。",
            messages: [
              { index: 1, kind: "会话批次", status: "unknown", summary: "批次 #04（共 6 轮对话）提交后出现 `SubprocessTransportError: transport closed before EOF`，无法确认是否已完成写入。" },
              { index: 2, kind: "安全拦截", status: "rejected", summary: "检测到 1 段包含本地环境变量导出的终端日志，已由采集策略拦截，未上传至引擎。" },
            ],
          },
        },
        {
          id: "msg-memlane-hist",
          group: "action",
          title: "Claude Code 当前为只读，已跳过自动采集",
          tag: "权限受限",
          agent: "claude",
          time: "昨天 20:15",
          summary: "该项目对 Claude Code 仅开放只读权限，昨天的 1 次会话总结未自动入库。",
          primaryAction: { id: "perm-hist", label: "去授权", kind: "permissions" },
          ack: true,
          details: {
            sessionLabel: "修复 MyGo 自绘循环窗口遮挡时持续请求 AnimationFrame",
            sessionId: "claude_20261120_1802_1a7b",
            diagCode: "ACL_READ_ONLY_SKIP",
            safetyNote: "只读权限下 Agent 仍可自动召回项目记忆，但不会写入新记录；如需自动采集请在下方调整为「读写」。",
            messages: [
              { index: 1, kind: "会话总结", status: "rejected", summary: "因项目访问权限配置为 `ro`（只读），网关按策略跳过本次写入请求。" },
            ],
          },
        },
      ],
      queuedDocs: [
        {
          id: "qdoc-memlane-1",
          title: "V4 视觉规范与侧栏状态栏联动决议",
          date: "今天 19:30",
          status: "等待提炼",
          body: "确认在侧栏项目行右侧增加 6px 警告圆点（使用 `var(--warning)`），与未读数字徽标保持独立间距；点击黄点直接进入该项目「管理」标签并将「消息与采集状态」自动置顶，同时与顶部状态栏 300px 浮层双向同步。",
        },
        {
          id: "qdoc-memlane-2",
          title: "Cognee 1.6.7 外置网关边界核对记录",
          date: "今天 18:10",
          status: "排队中",
          body: "复核 `engine/python/cognee_extension_gateway.py` 与 `official_service_dev.py` 的启动 SHA256 校验，确保 ACL 门禁、召回合并、Diff/日期注入与收据溯源全部保留在外置层，不触碰上游源码。",
        },
      ],
    },
    "item-fe": {
      autoSave: true,
      notices: [
        {
          id: "msg-itemfe-perm",
          group: "action",
          title: "Antigravity 写入权限未开启",
          tag: "需授权",
          agent: "gemini",
          time: "今天 11:20",
          summary: "Antigravity 在 item-fe 完成了库存扣减排查，但当前仅为只读权限，2 条总结暂未采集。",
          primaryAction: { id: "perm-item-fe", label: "去授权", kind: "permissions" },
          ack: false,
          details: {
            sessionLabel: "商品扣减库存并发竞争条件修复方案",
            sessionId: "ag_20261121_1058_7d20",
            diagCode: "ACL_WRITE_REQUIRED",
            safetyNote: "开启「读写」权限后，后续会话将自动采集；本次未采集的本地摘要可在授权后补交。",
            messages: [
              { index: 1, kind: "排查结论", status: "pending", summary: "取消应用层先 `SELECT` 再 `UPDATE` 的竞态逻辑，将库存校验下推至单条条件更新 SQL 并检查受影响行数。" },
              { index: 2, kind: "验证记录", status: "pending", summary: "压测 200 并发扣减库存未再出现负库存，事务冲突重试耗时保持在 18ms 内。" },
            ],
          },
        },
      ],
      queuedDocs: [
        {
          id: "qdoc-itemfe-1",
          title: "pnpm 依赖锁定与 CI 缓存约定",
          date: "昨天 17:45",
          status: "等待提炼",
          body: "项目统一使用 `pnpm install --frozen-lockfile`，禁止在本地提交 `package-lock.json` 或 `yarn.lock`，CI 缓存键与 `pnpm-lock.yaml` 哈希绑定。",
        },
      ],
    },
    "daily-cli": {
      autoSave: true,
      notices: [],
      queuedDocs: [],
    },
    "dcim-fe": {
      autoSave: true,
      notices: [],
      queuedDocs: [],
    },
  };

  /* 桌面端运行方式（macOS 菜单栏状态栏与 设置 › 服务 双向同步） */
  const traySettings = {
    keepAliveOnQuit: true,
    openAtLogin: false,
    isDevBundle: true,
    loginHint: "当前为开发直接运行模式（非 .app 安装包），系统登录项受 macOS 限制时将保持关闭并如实提示。",
  };

  /* 通用记忆示例数据（跨项目共享的个人偏好与通用经验） */
  const commonMemories = [
    {
      id: "cm-1",
      category: "preference",
      title: "中文技术沟通先给结论，再给路径与验证结果",
      body: "跨项目协作时默认使用简洁中文输出：第一句先给明确判断或改动范围，随后列出关键文件路径（如 `internal/simpledesktop/`）与实测命令结果，避免长篇铺垫与空泛客套。",
      applicability: "所有编程协作与代码评审场景；若特定仓库要求英文 PR 模板，则以项目约定为准。",
      source: "Antigravity 历史对话总结 · 2026-11-19",
      recall: 24,
      date: "今天",
      agent: "gemini",
      canonical: true,
    },
    {
      id: "cm-2",
      category: "preference",
      title: "界面保持原生克制感，不用嵌套边框卡片堆砌",
      body: "桌面端与 Web 工具界面优先参考 Things 3、Linear、Raycast：用 1px 发丝分隔线、留白和字号层级分区，避免卡片套卡片；颜色仅承载语义，分类标签保持中性灰。",
      applicability: "所有 macOS Native 与桌面端 UI 方案评审与实现。",
      source: "V4 视觉访谈决议 · 2026-11-20",
      recall: 18,
      date: "今天",
      agent: "claude",
      canonical: true,
    },
    {
      id: "cm-3",
      category: "experience",
      title: "并发扣减类逻辑下推至单条条件 SQL，避免先查后改竞态",
      body: "在高并发库存、配额或余额扣减场景，不要在应用层先 `SELECT` 再判断后 `UPDATE`。改为单条原子条件更新（如 `UPDATE ... SET qty = qty - :n WHERE id = :id AND qty >= :n`），通过 `RowsAffected` 判断是否成功。",
      applicability: "关系型数据库（MySQL / PostgreSQL / SQLite WAL）单行原子扣减场景。",
      source: "item-fe 压测复盘 · Codex",
      recall: 15,
      date: "昨天",
      agent: "codex",
      canonical: true,
    },
    {
      id: "cm-4",
      category: "experience",
      title: "自绘动画循环必须按活跃状态按需请求下一帧",
      body: "桌面自绘框架中，如果空态漂浮元素或弹簧指示器在收敛后仍持续调用 `AnimationFrame()`，窗口退到后台也会空耗 CPU。应在所有动画收敛或系统开启 `ReduceMotion` 时立即停止请求下一帧。",
      applicability: "MyGo / Metal / Canvas / requestAnimationFrame 自绘 UI 循环。",
      source: "memlane CPU 排查 · Claude Code",
      recall: 11,
      date: "昨天",
      agent: "claude",
      canonical: true,
    },
    {
      id: "cm-5",
      category: "experience",
      title: "含敏感元数据的本地状态文件使用 0600 临时文件原子替换",
      body: "写入本地错误摘要、收据或凭据文件时，先用 `os.open(tmp, O_WRONLY|O_CREAT|O_TRUNC, 0o600)` 写盘并 `fsync`，再通过 `os.replace` 原子切换，防止并发读到半截 JSON 或权限过宽。",
      applicability: "macOS / Linux 本地守护进程与 CLI 状态文件持久化。",
      source: "memlane Hook 策略加固 · Antigravity",
      recall: 7,
      date: "11月18日",
      agent: "gemini",
      canonical: false,
    },
    {
      id: "cm-6",
      category: "preference",
      title: "删除类危险弹窗不绑定回车确认，并保留原焦点回退",
      body: "所有删除配置、清空数据或停止后台服务的危险确认弹窗，统一使用危险色主按钮写明具体动作，明确禁止 `Enter` 键直接触发确认（并拦截回车防止击穿背景按钮），仅保留 `Esc` 取消。",
      applicability: "桌面客户端与控制台的所有破坏性操作交互。",
      source: "V4 剩余页面评审 · 2026-11-21",
      recall: 4,
      date: "11月17日",
      agent: "codex",
      canonical: true,
    },
  ];

  /* 项目图谱数据（供「图谱」标签的实体关系图谱与代码图谱使用） */
  const graphData = {
    memlane: {
      codeEnabled: false,
      pendingHint: "有 2 份文档尚未提炼进图谱 · 可在「管理」查看",
      nodes: [
        { id: "n-cognee", label: "Cognee 1.6.7 引擎", source: "架构文档", x: 390, y: 210, color: "#7C86E8", docTitle: "official_service_dev.py 与唯一数据源边界", docId: "m6" },
        { id: "n-gateway", label: "外置扩展网关", source: "架构文档", x: 245, y: 155, color: "#5FA8F5", docTitle: "cognee_extension_gateway.py 零侵入扩展层", docId: "m6" },
        { id: "n-control", label: "项目接入网关", source: "Codex 会话", x: 240, y: 290, color: "#5FA8F5", docTitle: "project_control_server.py 项目映射与 ACL", docId: "m6" },
        { id: "n-supervisor", label: "internal/supervisor", source: "Codex 会话", x: 115, y: 245, color: "#3FBF73", docTitle: "首次运行守护进程接入 internal/supervisor", docId: "m5" },
        { id: "n-hook", label: "Antigravity Hook 策略", source: "Antigravity 会话", x: 145, y: 105, color: "#E5A93C", docTitle: "last-hook-error.json 0600 原子写入", docId: "m1" },
        { id: "n-prefix", label: "COGNEE_SESSION_PREFIX", source: "Codex 会话", x: 345, y: 90, color: "#E5A93C", docTitle: "会话 ID 前缀匹配与历史归档规则", docId: "m4" },
        { id: "n-mygo", label: "MyGo Native 桌面端", source: "Claude 会话", x: 540, y: 155, color: "#B084F7", docTitle: "AnimationFrame 按需渲染与 ReduceMotion", docId: "m9" },
        { id: "n-overview", label: "overviewFixture 竞态修复", source: "Antigravity 会话", x: 645, y: 105, color: "#EF6363", docTitle: "a.Update 同步回调通过 -race 检查", docId: "m3" },
        { id: "n-llm", label: "native_llm_configuration", source: "Claude 会话", x: 545, y: 290, color: "#3FBF73", docTitle: "模型参数未配置保持省略、0 必须保留", docId: "m7" },
        { id: "n-disk", label: "50/55 GiB 磁盘门禁", source: "Codex 会话", x: 395, y: 340, color: "#E5A93C", docTitle: "scripts/task.py 磁盘水位与 build/ 保护", docId: "m8" },
      ],
      links: [
        ["n-gateway", "n-cognee"],
        ["n-control", "n-cognee"],
        ["n-control", "n-gateway"],
        ["n-supervisor", "n-control"],
        ["n-hook", "n-gateway"],
        ["n-hook", "n-prefix"],
        ["n-prefix", "n-cognee"],
        ["n-mygo", "n-cognee"],
        ["n-mygo", "n-overview"],
        ["n-llm", "n-cognee"],
        ["n-llm", "n-mygo"],
        ["n-disk", "n-supervisor"],
        ["n-disk", "n-control"],
      ],
    },
    "item-fe": {
      codeEnabled: true,
      pendingHint: "有 1 份文档等待提炼入图",
      nodes: [
        { id: "if-stock", label: "stock 条件更新 SQL", source: "Codex 会话", x: 350, y: 195, color: "#7C86E8", docTitle: "商品扣减库存并发竞争条件修复", docId: "m12" },
        { id: "if-deduct", label: "src/inventory/deduct.ts", source: "代码图谱", x: 190, y: 145, color: "#5FA8F5", docTitle: "库存扣减事务入口", docId: "m12" },
        { id: "if-sku", label: "SKU 库存校验", source: "Codex 会话", x: 510, y: 145, color: "#3FBF73", docTitle: "quantity >= :count 下限约束", docId: "m12" },
        { id: "if-pnpm", label: "pnpm-lock.yaml", source: "Claude 会话", x: 255, y: 300, color: "#E5A93C", docTitle: "统一使用 pnpm 与 frozen-lockfile", docId: "m13" },
        { id: "if-ci", label: "CI 依赖安装流水线", source: "Claude 会话", x: 455, y: 300, color: "#B084F7", docTitle: "CI 缓存键与依赖锁定约定", docId: "m13" },
      ],
      links: [
        ["if-deduct", "if-stock"],
        ["if-stock", "if-sku"],
        ["if-pnpm", "if-ci"],
        ["if-deduct", "if-pnpm"],
      ],
    },
    "daily-cli": {
      codeEnabled: false,
      pendingHint: "",
      nodes: [
        { id: "dc-main", label: "cmd/daily/main.go", source: "Antigravity 会话", x: 250, y: 185, color: "#7C86E8", docTitle: "子命令解析入口", docId: "m14" },
        { id: "dc-render", label: "internal/report/render.go", source: "Antigravity 会话", x: 440, y: 185, color: "#5FA8F5", docTitle: "Markdown 与终端输出共用渲染器", docId: "m14" },
        { id: "dc-section", label: "Section 数据模型", source: "Antigravity 会话", x: 345, y: 300, color: "#3FBF73", docTitle: "统一报告分节结构", docId: "m14" },
      ],
      links: [
        ["dc-main", "dc-render"],
        ["dc-render", "dc-section"],
        ["dc-main", "dc-section"],
      ],
    },
    "dcim-fe": {
      codeEnabled: false,
      pendingHint: "",
      nodes: [
        { id: "df-rack", label: "src/rack/layout.ts", source: "Codex 会话", x: 280, y: 185, color: "#7C86E8", docTitle: "机柜视图 U 位坐标换算", docId: "m15" },
        { id: "df-unit", label: "toRackUnit()", source: "Codex 会话", x: 470, y: 185, color: "#E5A93C", docTitle: "底部为 1 起算，禁止像素反推", docId: "m15" },
      ],
      links: [
        ["df-rack", "df-unit"],
      ],
    },
  };

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
    projectMessages,
    traySettings,
    commonMemories,
    graphData,
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
