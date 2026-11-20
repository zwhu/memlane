// 合成示例数据：形态模仿真实记忆（无独立标题、正文含代码路径、召回次数、官方分类），不代表真实记忆库。
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
    M("m6", "memlane", "gemini", "代码位置与关联", 0, "今天", "`scripts/dev.py` 日常模式启动 `internal/simpledesktop` 和三个回环服务：`engine/python/official_service_dev.py` 是知识与图谱的唯一数据源，`engine/python/project_control_server.py` 负责项目映射和访问控制，补全桥独立运行。", "来源会话 · Antigravity · 今天 14:31", true),
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

  return { projects, projectTotal, agents, categories, memories, activity, searches, heat, key, stats: { days: 128, memories: 467, raw: 83 } };
})();
