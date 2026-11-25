// Memlane 初始化向导重设计原型（2026-11-25）
// 流程：欢迎 → ① 选择 Agent → ② 准备环境 → ③ 接入 Agent → ④ 记忆提炼 → 完成
// 后端阶段保持不变：①=select_agent，②=prepare_env，③④ 同属 connect_and_enable（仅 UI 拆分）。
(() => {
  const $ = (s, el = document) => el.querySelector(s);
  const $$ = (s, el = document) => [...el.querySelectorAll(s)];
  const root = document.documentElement;
  const ICON_DIR = "../../../internal/simpledesktop/agent_icons/";
  const BRAND = "../../../internal/simpledesktop/brand_icons/sidebar_brand.png";

  const I = {
    check: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"><path d="M5 12.5l4.5 4.5L19 7.5"/></svg>',
    x: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round"><path d="M6 6l12 12M18 6L6 18"/></svg>',
    chev: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><path d="M9 6l6 6-6 6"/></svg>',
    refresh: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M20 11a8 8 0 10-2.3 5.7M20 4v7h-7"/></svg>',
    copy: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="11" height="11" rx="2"/><path d="M5 15V5a1 1 0 011-1h10"/></svg>',
    help: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="9"/><path d="M9.5 9.5a2.5 2.5 0 114 2c-.9.6-1.5 1.1-1.5 2.2M12 17h.01"/></svg>',
    shield: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3l7 3v6c0 4.5-3 7.7-7 9-4-1.3-7-4.5-7-9V6l7-3z"/></svg>',
    capture: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M4 5h16v11H8l-4 4V5z"/></svg>',
    spark: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3v4M12 17v4M3 12h4M17 12h4M6 6l2.5 2.5M15.5 15.5L18 18M6 18l2.5-2.5M15.5 8.5L18 6"/></svg>',
    recall: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M3 12a9 9 0 109-9 9.7 9.7 0 00-6.7 2.8L3 8"/><path d="M3 3v5h5M12 7v5l3 2"/></svg>',
    download: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 4v11M7 10l5 5 5-5M5 20h14"/></svg>',
    terminal: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="16" rx="2"/><path d="M7 9l3 3-3 3M13 15h4"/></svg>',
    key: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="8" cy="15" r="4"/><path d="M11 12l9-9M17 6l3 3"/></svg>',
    more: '<svg viewBox="0 0 24 24" fill="currentColor"><circle cx="5" cy="12" r="1.8"/><circle cx="12" cy="12" r="1.8"/><circle cx="19" cy="12" r="1.8"/></svg>',
    restart: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3v9"/><path d="M6.3 6.3a8 8 0 1011.4 0"/></svg>',
    bulb: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M9 18h6M10 21h4M12 3a6 6 0 00-3.5 10.9c.6.5 1 1.2 1 2.1h5c0-.9.4-1.6 1-2.1A6 6 0 0012 3z"/></svg>',
    folder: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M3 6a1 1 0 011-1h5l2 2h9a1 1 0 011 1v10a1 1 0 01-1 1H4a1 1 0 01-1-1V6z"/></svg>',
    skip: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M5 5l9 7-9 7V5zM18 5v14"/></svg>',
    doc: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M14 3H6a1 1 0 00-1 1v16a1 1 0 001 1h12a1 1 0 001-1V8l-5-5z"/><path d="M14 3v5h5"/></svg>',
    agent: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="4" y="7" width="16" height="12" rx="3"/><path d="M12 3v4M9 13h.01M15 13h.01"/></svg>',
  };

  const STAGES = ["welcome", "agents", "env", "connect", "process", "done"];
  const STEP_DEFS = [
    { id: "agents", label: "选择 Agent" },
    { id: "env", label: "准备环境" },
    { id: "connect", label: "接入" },
    { id: "process", label: "记忆提炼" },
  ];

  const AGENTS = [
    { id: "trae_ide", name: "TRAE CN", ver: "3.4.24", icon: "trae-color.svg", detected: true, cli: false, cfg: "~/.trae-cn/hooks.json", get: { url: "trae.cn", label: "下载 TRAE CN" }, extra: "还需一步：在 TRAE 设置 → Hooks → 全局 中打开开关，并确认安全提示" },
    { id: "codex", name: "Codex", ver: "0.172.0", icon: "codex-color.svg", detected: true, cli: true, login: true, model: "gpt-5.1-codex", cfg: "~/.codex/config.toml", get: { cmd: "npm i -g @openai/codex" } },
    { id: "pi", name: "pi", ver: "0.84.1", icon: "pi.svg", detected: true, cli: true, login: true, model: "anthropic · claude-sonnet-4.5", cfg: "~/.pi/agent/extensions/memlane-cognee/index.ts", get: { cmd: "npm i -g @mariozechner/pi-coding-agent" } },
    { id: "traex", name: "TRAE CLI", ver: "1.2.0", icon: "trae-color.svg", detected: true, cli: true, login: false, cfg: "~/.trae/settings.json", loginCmd: "traecli login", get: { cmd: "curl -fsSL https://lf-cdn.trae.com.cn/obj/trae-com-cn/pkg/cli/install.sh | bash" } },
    { id: "antigravity", name: "Antigravity", ver: "", icon: "gemini-color.svg", detected: false, cli: true, get: { url: "gemini.google.com", label: "下载 Gemini App" } },
  ];
  const RECOMMENDED_CLI = [
    { name: "Codex", icon: "codex-color.svg", cmd: "npm i -g @openai/codex", hint: "安装后在终端运行 codex 完成登录" },
    { name: "pi", icon: "pi.svg", cmd: "npm i -g @mariozechner/pi-coding-agent", hint: "安装后运行 pi 并配置模型登录" },
    { name: "Antigravity", icon: "gemini-color.svg", cmd: "", hint: "安装并登录 Gemini App 后即可使用内置 agy CLI" },
  ];
  const ENV_ITEMS = [
    { id: "runtime", title: "隔离运行环境", desc: "Python 3.12 · uv · 独立虚拟环境，不改系统 Python", dl: 0, have: true },
    { id: "engine", title: "本地记忆引擎", desc: "把对话整理成结构化记忆与关系图谱", dl: 18.4, have: false },
    { id: "embed", title: "本地语义搜索模型", desc: "离线理解中文与代码含义，不上传云端", dl: 91.2, have: false },
    { id: "plugin", title: "Agent 接入组件", desc: "让 Agent 自动写入与召回项目记忆", dl: 0, have: true },
  ];
  const TECH = [
    ["uv", "0.7.8", "~/Library/Application Support/Memlane/engine-components/uv", "已复用本机"],
    ["Python", "3.12.9", "~/Library/Application Support/Memlane/python/cpython-3.12.9", "已复用本机"],
    ["运行虚拟环境", "venv · 3.12", "~/Library/Application Support/Memlane/venv", "已复用本机"],
    ["Cognee 源码", "1.6.7 (ec6f408) · SHA-256 3f9a1c0e2b7d", "~/Library/Application Support/Memlane/engine-components/cognee-1.6.7", "下载 18.4 MiB · 占用 62.0 MiB"],
    ["向量模型", "BAAI/bge-small-zh-v1.5 · SHA-256 8d02ab51c9e4", "~/Library/Application Support/Memlane/model-cache/bge-small-zh-v1.5", "下载 91.2 MiB · 占用 96.0 MiB"],
    ["Agent 接入插件", "memlane-hook 0.1.0-beta.3", "~/Library/Application Support/Memlane/plugins", "已复用本机"],
  ];

  const P = { theme: "light", hosts: "some", env: "missing", cli: "yes", verify: "pass", reduce: false };
  let S;
  let timers = [];
  let lastStage = null;

  function fresh() {
    return {
      stage: "welcome",
      detect: "idle", // idle | running | done
      selected: new Set(),
      selectedTouched: false,
      showUndetected: false,
      env: { status: "detecting", comp: {}, source: "cn", error: "", log: [] },
      conn: {},
      proc: { mode: null, cli: null, api: { preset: "openai", base: "https://api.openai.com/v1", key: "", model: "gpt-4.1-mini" }, chain: null, advanced: false },
      open: new Set(),
      done: new Set(),
    };
  }

  const LOG_ID = { runtime: "runtime", engine: "cognee-source", embed: "embedding", plugin: "agent-plugin" };
  const SRC = () => S.env.source === "cn" ? "ModelScope 镜像" : "HuggingFace 官方";
  function logLine(msg) { S.env.log.push(`${new Date().toTimeString().slice(0, 8)}  ${msg}`); }
  function later(fn, ms) {
    const t = setTimeout(fn, P.reduce ? Math.min(ms, 60) : ms);
    timers.push(t);
    return t;
  }
  function clearTimers() { timers.forEach(clearTimeout); timers = []; }
  const esc = s => String(s ?? "").replace(/[&<>"]/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
  const agentById = id => AGENTS.find(a => a.id === id);
  const isDetected = a => P.hosts !== "none" && a.detected;
  const detectedAgents = () => AGENTS.filter(isDetected);
  const icon = (file, size = 22) => `<img src="${ICON_DIR}${file}" width="${size}" height="${size}" alt="" />`;
  const processingCLIs = () => P.cli === "none" ? [] : AGENTS.filter(a => isDetected(a) && a.cli);
  const readyCLIs = () => processingCLIs().filter(a => a.login);

  function toast(msg) {
    const el = $("#obToast");
    el.textContent = msg;
    el.classList.add("show");
    clearTimeout(toast.t);
    toast.t = setTimeout(() => el.classList.remove("show"), 2200);
  }

  /* ---------- 检测（欢迎页即后台开始） ---------- */
  function startDetect() {
    if (S.detect !== "idle") return;
    S.detect = "running";
    later(() => {
      S.detect = "done";
      if (!S.selectedTouched) detectedAgents().forEach(a => S.selected.add(a.id));
      resolveEnvDetect();
      render();
    }, 900);
  }
  function resolveEnvDetect() {
    const missing = P.env !== "ready";
    ENV_ITEMS.forEach(it => { S.env.comp[it.id] = { st: (!missing || it.have) ? "ok" : "missing", p: 0 }; });
    S.env.comp.service = { st: "pending", p: 0 };
    S.env.status = missing ? "missing" : "ready";
  }

  /* ---------- 导航 ---------- */
  function go(stage) {
    if (!STAGES.includes(stage)) return;
    const idx = STAGES.indexOf(stage);
    STAGES.slice(1, idx).forEach(s => S.done.add(s));
    S.stage = stage;
    closeLayer();
    startDetect();
    if (stage === "connect") STEP_DEFS.slice(0, 2).forEach(s => S.done.add(s.id));
    if (stage === "process" && !S.proc.mode) {
      S.proc.mode = readyCLIs().length ? "agent" : "api";
      S.proc.cli = readyCLIs()[0]?.id || null;
    }
    render();
  }
  function busy() {
    return ["installing", "starting"].includes(S.env.status) ||
      Object.values(S.conn).includes("writing") ||
      (S.proc.chain && S.proc.chain.running);
  }

  /* ---------- 顶栏 ---------- */
  function renderBar() {
    const stepIdx = STEP_DEFS.findIndex(s => s.id === S.stage);
    const hideSteps = S.stage === "welcome" || S.stage === "done";
    $("#obBar").className = "ob-bar" + (hideSteps ? "" : " has-line");
    $("#obBar").innerHTML = `
      <div class="ob-bar-left"><div class="traffic"><i></i><i></i><i></i></div>
        <div class="brand"><img src="${BRAND}" alt="" />Memlane</div></div>
      <nav class="stepper${hideSteps ? " is-hidden" : ""}" aria-label="设置进度">
        ${STEP_DEFS.map((s, i) => {
          const on = i === stepIdx, done = S.done.has(s.id) && !on;
          const link = done && !busy();
          return `${i ? `<span class="step-line${S.done.has(STEP_DEFS[i - 1].id) ? " is-done" : ""}"></span>` : ""}
            <button class="step${on ? " is-on" : ""}${done ? " is-done" : ""}${link ? " is-link" : ""}" ${link ? `data-go="${s.id}"` : "tabindex=-1"} aria-current="${on ? "step" : "false"}">
              <span class="step-dot">${done ? I.check : i + 1}</span>${s.label}</button>`;
        }).join("")}
      </nav>
      <div class="ob-bar-right">
        ${S.stage === "done" ? "" : `<button class="btn btn--ghost btn--sm" id="btnHelp" aria-haspopup="menu">${I.help}遇到问题</button>`}
      </div>`;
  }

  /* ---------- 页面 ---------- */
  function header(n, title, lead) {
    return `<div class="eyebrow">第 ${n} 步，共 4 步</div><h1 class="h1">${title}</h1><p class="lead">${lead}</p>`;
  }

  function pageWelcome() {
    const detecting = S.detect === "running";
    return `<div class="ob-col is-center"><div class="welcome">
      <img class="mascot" src="${BRAND}" alt="" />
      <h1 class="h1">换个 Agent，接着做。</h1>
      <p class="lead">项目记忆留在你的 Mac 上。<br/>换一个编码 Agent，也能用上之前积累的项目知识。</p>
      <div class="values">
        <div class="value"><div class="value-ic">${I.capture}</div><b>自动采集</b><span>接入后，Agent 的对话与工具调用自动记录，无需手动整理。</span></div>
        <div class="value"><div class="value-ic">${I.spark}</div><b>本地提炼</b><span>把零散对话提炼成项目约定、踩坑经验和代码关联。</span></div>
        <div class="value"><div class="value-ic">${I.recall}</div><b>按需召回</b><span>新会话里 Agent 会自动找回相关记忆，少走回头路。</span></div>
      </div>
      <div class="welcome-cta">
        <div class="cta-row">
          <button class="btn btn--primary btn--lg" data-act="start">开始设置 <span class="kbd">↵</span></button>
          ${S.detect === "done" && !detectedAgents().length ? "" : `<button class="btn btn--lg btn--accent-outline" data-act="agentInstall">${I.terminal}让 Agent 代我安装</button>`}
        </div>
        <span class="promise">${I.shield}数据与模型只留在本机 · 随时可一键卸载</span>
        <span class="detect-chip">${detecting ? `<span class="spinner"></span>正在检测本机已有的 Agent 与组件…` : S.detect === "done" ? `${I.check.replace('stroke-width="3"', 'stroke-width="3" width="12" height="12" style="color:var(--ok)"')}已检测到 ${detectedAgents().length} 个 Agent · 大约 3 分钟完成设置` : "大约 3 分钟完成设置"}</span>
      </div>
    </div></div>`;
  }

  function getAction(a) {
    if (!a.get) return "";
    return a.get.cmd
      ? `<button class="btn btn--sm" data-act="copy" data-text="${esc(a.get.cmd)}">${I.copy}复制安装命令</button>`
      : `<button class="btn btn--sm" data-act="toast" data-text="已在浏览器打开 ${esc(a.get.url)}">${esc(a.get.label)}</button>`;
  }
  function supportRows(onlyMissing) {
    return `<div class="rows">${AGENTS.filter(a => !onlyMissing || !isDetected(a)).map(a => `<div class="row"><div class="row-ic">${icon(a.icon)}</div>
      <div class="row-main"><div class="row-title">${esc(a.name)}</div><div class="row-desc">${a.cli ? "自动采集与召回 · 也可用于提炼记忆" : "自动采集与召回"}</div>
      ${!isDetected(a) && a.get?.cmd ? `<div class="row-desc mono" style="font-size:var(--fs-caption);color:var(--text-2)">${esc(a.get.cmd)}</div>` : ""}</div>
      <div class="row-ctrl">${isDetected(a) ? `<span class="badge badge--ok">${I.check}已检测到</span>` : getAction(a)}</div></div>`).join("")}</div>`;
  }
  function openSupportList() {
    openDialog({ title: "Memlane 支持的 Agent", hint: "安装完成后回到这里点「重新检测」", confirm: "重新检测", onConfirm: () => toast("已重新检测本机 Agent"),
      body: `<p style="margin:0 0 6px">公测阶段重点支持以下 Agent。检测依据是本机能找到对应程序（命令行工具或 /Applications 中的应用）。</p>${supportRows(false)}` });
  }

  function pageAgents() {
    const detected = detectedAgents();
    const loading = S.detect !== "done";
    const card = a => {
      const on = S.selected.has(a.id);
      const cap = a.cli && P.cli !== "none" ? `<div class="cap">${I.spark}也可用于提炼记忆</div>` : `<div class="cap">自动采集与召回</div>`;
      return `<button class="agent-card${on ? " on" : ""}" data-toggle="${a.id}" role="checkbox" aria-checked="${on}">
        ${icon(a.icon, 28)}
        <div class="meta"><div class="name">${esc(a.name)}<span class="ver">${esc(a.ver)}</span></div>${cap}</div>
        <span class="check${on ? " on" : ""}">${I.check}</span></button>`;
    };
    const skel = `<div class="agent-grid">${[0, 1, 2, 3].map(() => `<div class="agent-card" style="cursor:default"><span style="width:28px;height:28px;border-radius:7px;background:var(--skeleton-base)"></span><div class="meta"><div style="height:10px;width:60%;border-radius:4px;background:var(--skeleton-base)"></div><div style="height:8px;width:40%;border-radius:4px;background:var(--skeleton-base);margin-top:8px"></div></div></div>`).join("")}</div>`;
    if (!loading && !detected.length) {
      return `<div class="ob-col">
        ${header(1, "还没有检测到支持的 Agent", "安装下面任一 Agent 后点「重新检测」；也可以先跳过，环境准备好后再到「设置 › Agent 与模型」接入。")}
        <div class="sec"><div class="caption-row"><span>Memlane 支持的 Agent</span>
          <span class="actions"><button class="btn btn--ghost btn--sm" data-act="redetect">${I.refresh}重新检测</button></span></div>
          ${supportRows(false)}</div></div>`;
    }
    return `<div class="ob-col">
      ${header(1, "你在用哪些 Agent？", "选中的 Agent 会接入 Memlane：对话自动采集，新会话自动召回记忆。这一步只记录选择，不会改动任何配置。")}
      <div class="sec">
        <div class="caption-row"><span>本机已检测到 ${loading ? "…" : detected.length + " 个"}</span>
          <span class="actions"><button class="btn btn--ghost btn--sm" data-act="redetect">${I.refresh}重新检测</button></span></div>
        ${loading ? skel : `<div class="agent-grid">${detected.map(card).join("")}</div>`}
      </div>
      ${loading ? "" : `<div class="select-summary">${S.selected.size ? `${I.check}已选 ${S.selected.size} 个，第 3 步统一确认接入` : `<span class="muted">不选也可以继续，之后在「设置 › Agent 与模型」里接入</span>`}</div>
      <p class="muted" style="margin-top:var(--s5)">没找到你的 Agent？<button class="link" data-act="supportList">查看支持列表</button></p>`}
    </div>`;
  }

  function envTotals() {
    const need = ENV_ITEMS.filter(it => S.env.comp[it.id]?.st !== "ok" || S.env.status === "installing" || S.env.status === "fail");
    const total = ENV_ITEMS.filter(it => !it.have || P.env === "fail").reduce((s, it) => s + it.dl, 0);
    const done = ENV_ITEMS.reduce((s, it) => s + it.dl * ((S.env.comp[it.id]?.p || 0) / 100), 0);
    return { need, total: P.env === "ready" ? 0 : ENV_ITEMS.reduce((s, it) => s + (it.have ? 0 : it.dl), 0), done };
  }

  function compRow(it) {
    const c = S.env.comp[it.id] || { st: "pending", p: 0 };
    let state = "";
    if (c.st === "ok") state = `<span class="comp-state ok">${I.check}${it.id === "service" ? "运行中" : "已就绪"}</span>`;
    else if (c.st === "missing") state = `<span class="comp-state">${it.dl ? `待下载 ${it.dl} MiB` : "待安装"}</span>`;
    else if (c.st === "run") state = `<span class="comp-state run"><span class="spinner"></span>${it.id === "service" ? "启动中" : Math.round(c.p) + "%"}</span>`;
    else if (c.st === "fail") state = `<span class="comp-state fail">${I.x}失败</span>`;
    else state = `<span class="comp-state">等待</span>`;
    const bar = c.st === "run" && it.id !== "service" ? `<div class="row-bar"><i style="width:${c.p}%"></i></div>` : "";
    return `<div class="row"><div class="row-ic"><span class="dot ${c.st === "ok" ? "ok" : c.st === "run" ? "run" : c.st === "fail" ? "fail" : c.st === "missing" ? "warn" : ""}"></span></div>
      <div class="row-main"><div class="row-title">${it.title}</div><div class="row-desc">${it.desc}</div>${bar}</div>
      <div class="row-ctrl">${state}</div></div>`;
  }

  function pageEnv() {
    const st = S.env.status;
    const t = envTotals();
    let hero = "";
    if (st === "detecting") {
      hero = `<div class="hero"><span class="spinner" style="width:20px;height:20px"></span><div class="hero-main"><div style="font-weight:600">正在检测本机已有组件…</div><div class="hero-sub">已有的兼容组件会直接复用，只下载缺少的部分。</div></div></div>`;
    } else if (st === "missing") {
      hero = `<div class="hero"><div class="hero-ic">${I.download}</div><div class="hero-main">
        <div class="hero-num">需下载 ${t.total.toFixed(1)} MiB</div>
        <div class="hero-sub">已复用本机 4 项组件。新组件全部放在 Memlane 应用目录，不改系统 Python 和环境变量，可一键卸载。</div></div></div>`;
    } else if (st === "ready") {
      hero = `<div class="hero"><div class="hero-ic ok">${I.check}</div><div class="hero-main">
        <div class="hero-num">无需下载</div><div class="hero-sub">本机已有全部组件，并已通过校验。下一步启动本地记忆服务。</div></div></div>`;
    } else if (st === "installing" || st === "starting") {
      const cur = ENV_ITEMS.find(it => S.env.comp[it.id]?.st === "run");
      const pct = t.total ? Math.min(100, t.done / t.total * 100) : 100;
      const title = st === "starting" ? "正在启动本地记忆服务" : `正在下载 · ${cur ? cur.title : "组件"}`;
      const remain = t.total ? Math.max(1, Math.round((t.total - t.done) / 9.6)) : 0;
      const metrics = st === "starting" ? "校验上游源码 SHA-256 · 拉起本机回环服务" : `${t.done.toFixed(1)} / ${t.total.toFixed(1)} MiB · 9.6 MiB/s · 来源：${S.env.source === "cn" ? "ModelScope 镜像" : "HuggingFace 官方"} · 剩余约 ${remain} 秒`;
      hero = `<div class="hero"><div class="blocks"><i></i><i></i><i></i><i></i></div><div class="hero-main">
        <div style="font-weight:600;font-size:var(--fs-body)">${title}</div>
        <div class="bar"><i style="width:${st === "starting" ? 100 : pct}%"></i></div>
        <div class="hero-sub mono" style="font-size:var(--fs-caption)">${metrics}</div></div>
        ${st === "installing" ? `<button class="btn btn--ghost btn--sm" data-act="stopInstall">停止</button>` : ""}</div>`;
    } else if (st === "fail") {
      hero = `<div class="hero"><div class="hero-ic fail">${I.x}</div><div class="hero-main">
        <div style="font-weight:600;font-size:var(--fs-body)">本地语义搜索模型下载中断</div>
        <div class="hero-sub">镜像连接超时，已自动尝试 3/3 个备用源。已下载完成的组件会保留，重试只补齐缺少的部分。</div>
        <div style="display:flex;gap:8px;margin-top:10px">
          <button class="btn btn--sm" data-act="retryOfficial">改用官方源重试</button>
          <button class="btn btn--ghost btn--sm" data-act="copyDiag">${I.copy}复制诊断，交给 Agent 排查</button></div></div></div>`;
    } else if (st === "done") {
      hero = `<div class="hero"><div class="hero-ic ok">${I.check}</div><div class="hero-main"><div class="hero-num">本地记忆环境已就绪</div><div class="hero-sub">记忆服务已在本机启动，正在进入下一步…</div></div></div>`;
    }
    const showService = ["installing", "starting", "done", "fail"].includes(st) || (st === "ready");
    const items = st === "detecting" ? "" : `<div class="sec"><div class="caption-row"><span>准备内容</span></div><div class="rows">
      ${ENV_ITEMS.map(compRow).join("")}${showService ? compRow({ id: "service", title: "启动本地记忆服务", desc: "仅监听本机回环地址，退出窗口后可保持后台运行" }) : ""}</div>
      ${st === "missing" ? `<div class="inline-opt"><span>下载来源</span><div class="seg" role="radiogroup">
        <button class="${S.env.source === "cn" ? "on" : ""}" data-src="cn">国内镜像（推荐）</button>
        <button class="${S.env.source === "official" ? "on" : ""}" data-src="official">官方源</button></div></div>` : ""}
      ${S.env.log.length ? disclosure("log", `安装日志（${S.env.log.length} 行）`, `<div class="log" id="envLog">${S.env.log.map(l => `<div class="${/失败|超时|中断/.test(l) ? "err" : ""}">${esc(l)}</div>`).join("")}</div>
        <div style="display:flex;align-items:center;gap:8px;margin-top:8px"><button class="btn btn--sm" data-act="copyLog">${I.copy}复制日志</button><span class="muted">已脱敏，不含密钥或聊天内容</span></div>`) : ""}
      ${disclosure("tech", "技术清单（版本、校验值与安装位置）", `<div class="rows tech">${TECH.map(r => `<div class="row"><div class="row-main"><div class="row-title">${r[0]} <span class="ver">${r[1]}</span></div><div class="row-desc">${r[2]}</div></div><div class="row-ctrl"><span class="muted">${r[3]}</span></div></div>`).join("")}</div>
        <div style="display:flex;gap:8px;margin-top:10px"><button class="btn btn--sm" data-act="copyCmd">${I.copy}复制安装命令，自己在终端执行</button><button class="btn btn--sm" data-act="manualPath">${I.folder}指定已有组件路径…</button></div>`)}
    </div>`;
    return `<div class="ob-col">
      ${header(2, st === "fail" ? "准备未完成，可以直接重试" : "准备本地记忆环境", "Memlane 在本机运行记忆引擎和语义搜索模型，记忆正文、向量与图谱都不会上传。")}
      ${hero}${items}</div>`;
  }

  function disclosure(key, label, body) {
    return `<div class="disclosure${S.open.has(key) ? " open" : ""}"><button class="disclosure-btn" data-disc="${key}">${I.chev}${label}</button><div class="disclosure-body">${body}</div></div>`;
  }

  function connRow(a) {
    const st = S.conn[a.id] || "todo";
    const map = {
      todo: ["将写入 " + a.cfg, `<span class="badge">待接入</span>`],
      writing: ["正在写入接入配置并备份原文件…", `<span class="badge badge--accent"><span class="spinner" style="width:10px;height:10px;border-width:1.5px"></span>写入中</span>`],
      pending: ["已写入配置 · 等你在新会话里发一条消息", `<span class="badge badge--accent">待新会话</span>`],
      verified: ["已收到第一条会话，自动采集正常", `<span class="badge badge--ok">${I.check}已生效</span>`],
      skipped: ["已跳过，之后可在「设置 › Agent 与模型」接入", `<span class="badge badge--plain">已跳过</span>`],
    };
    const [desc, badge] = map[st];
    const extra = st === "pending" && a.extra ? `<div class="row-extra" style="color:var(--warning)">${a.extra}</div>` : "";
    return `<div class="row${st === "skipped" ? " is-dim" : ""}"><div class="row-ic">${icon(a.icon)}</div>
      <div class="row-main"><div class="row-title">${esc(a.name)}<span class="ver">${esc(a.ver)}</span></div><div class="row-desc ${st === "todo" ? "mono" : ""}" ${st === "todo" ? 'style="font-size:var(--fs-caption)"' : ""}>${desc}</div>${extra}</div>
      <div class="row-ctrl">${badge}<button class="more-btn" data-more="${a.id}" aria-label="${esc(a.name)} 更多操作">${I.more}</button></div></div>`;
  }

  function pageConnect() {
    const list = [...S.selected].map(agentById);
    const anyPending = list.some(a => ["pending", "verified"].includes(S.conn[a.id]));
    if (!list.length) {
      return `<div class="ob-col">${header(3, "接入 Agent", "你在第 1 步没有选择 Agent。")}
        <div class="hero"><div class="hero-ic">${I.agent}</div><div class="hero-main"><div style="font-weight:600">暂不接入也可以</div><div class="hero-sub">记忆提炼仍可先配置好。之后在「设置 › Agent 与模型」随时接入。</div></div>
        <button class="btn btn--sm" data-go="agents">返回选择</button></div></div>`;
    }
    return `<div class="ob-col">
      ${header(3, "接入你选择的 Agent", "Memlane 会在这些 Agent 的配置里加入记忆采集与召回入口。只改动下面列出的文件，改动前自动备份。")}
      <div class="sec"><div class="caption-row"><span>${list.length} 个 Agent</span><span class="actions"><button class="btn btn--ghost btn--sm" data-go="agents">调整选择</button></span></div>
        <div class="rows">${list.map(connRow).join("")}</div></div>
      ${anyPending ? `<div class="callout"><div class="callout-ic">${I.restart}</div><div>
        <b>最后一步：重启已接入的 Agent</b>
        <ol><li>在 Agent 里按 <kbd>⌘Q</kbd> 完全退出，再重新打开</li><li>在项目里新建会话，正常发一条消息</li></ol>
        <p style="margin-top:6px;color:var(--text-3)">不用在这里等待：验证会在后台自动完成，结果显示在总览和菜单栏。</p></div></div>` : ""}
    </div>`;
  }

  function pageProcess() {
    const p = S.proc;
    const clis = processingCLIs();
    const hasReady = readyCLIs().length > 0;
    const chain = p.chain;
    const locked = chain && (chain.running || chain.ok);
    let body = "";
    if (p.mode === "agent") {
      if (!clis.length) {
        body = `<div class="sec"><div class="caption-row"><span>未检测到可用于提炼的本机 CLI</span><span class="actions"><button class="btn btn--ghost btn--sm" data-act="redetect">${I.refresh}重新检测</button></span></div>
          <p class="muted" style="margin:10px 0 4px">安装下面任一 CLI 并登录，即可复用它的账号额度提炼记忆；也可以<button class="link" data-mode="api">改用模型 API</button>。</p>
          <div class="rows">${RECOMMENDED_CLI.map(r => `<div class="row"><div class="row-ic">${icon(r.icon)}</div><div class="row-main"><div class="row-title">${r.name}</div><div class="row-desc">${r.hint}</div>${r.cmd ? `<div class="row-desc mono" style="font-size:var(--fs-caption);color:var(--text-2)">${esc(r.cmd)}</div>` : ""}</div>
            <div class="row-ctrl">${r.cmd ? `<button class="btn btn--sm" data-act="copy" data-text="${esc(r.cmd)}">${I.copy}复制命令</button>` : `<button class="btn btn--sm" data-act="toast" data-text="已在浏览器打开下载页">下载</button>`}</div></div>`).join("")}</div></div>`;
      } else {
        body = `<div class="sec"><div class="caption-row"><span>用哪个 Agent 提炼</span><span class="actions"><button class="btn btn--ghost btn--sm" data-act="redetect">${I.refresh}重新检测</button></span></div>
          <div class="rows">${clis.map(a => {
            const ok = a.login, on = p.cli === a.id;
            return `<div class="row${ok && !locked ? " is-click" : ""}${ok ? "" : " is-dim"}" ${ok && !locked ? `data-cli="${a.id}"` : ""} role="radio" aria-checked="${on}">
              <span class="check radio${on ? " on" : ""}" style="${ok ? "" : "opacity:.4"}">${I.check}</span><div class="row-ic">${icon(a.icon)}</div>
              <div class="row-main"><div class="row-title">${esc(a.name)}</div><div class="row-desc">${ok ? `${esc(a.model)} · 已登录` : `尚未登录 · 在终端运行 <span class="mono">${esc(a.loginCmd)}</span> 后重新检测`}</div></div>
              <div class="row-ctrl">${ok ? (on ? `<span class="badge badge--accent">将使用</span>` : "") : `<button class="btn btn--sm" data-act="copy" data-text="${esc(a.loginCmd)}">${I.copy}复制命令</button>`}</div></div>`;
          }).join("")}</div>
          <p class="muted" style="margin-top:10px">提炼使用该 Agent 当前登录的账号与默认模型，会消耗它的额度；不需要填写 API Key。</p></div>`;
      }
    } else if (p.mode === "api") {
      const a = p.api;
      body = `<div class="sec"><div class="caption-row"><span>模型接口</span></div>
        <div class="form">
          <div class="field"><label>服务类型</label><select class="select" data-api="preset" ${locked ? "disabled" : ""}>
            <option value="openai" ${a.preset === "openai" ? "selected" : ""}>OpenAI 兼容接口</option>
            <option value="ollama" ${a.preset === "ollama" ? "selected" : ""}>本地 Ollama</option></select></div>
          <div class="field"><label>模型</label><input class="input mono" data-api="model" value="${esc(a.model)}" ${locked ? "disabled" : ""} /></div>
          <div class="field full"><label>接口地址</label><input class="input mono" data-api="base" value="${esc(a.base)}" ${locked ? "disabled" : ""} /></div>
          ${a.preset === "ollama" ? "" : `<div class="field full"><label>API Key</label><input class="input mono" type="password" data-api="key" placeholder="sk-…（只保存在本机受管凭据中）" value="${esc(a.key)}" ${locked ? "disabled" : ""} /></div>`}
        </div>
        ${disclosure("adv", "高级参数（采样温度、最大输出 Token，留空即不发送）", `<div class="form" style="margin-top:0"><div class="field"><label>采样温度</label><input class="input mono" placeholder="未设置" /></div><div class="field"><label>最大输出 Token</label><input class="input mono" placeholder="未设置" /></div></div>`)}
      </div>`;
    }
    return `<div class="ob-col">
      ${header(4, "选择如何提炼记忆", "Memlane 需要一个大模型，把采集到的对话整理成可召回的记忆。以后可在「设置 › 记忆提炼」随时切换。")}
      <div class="opt-grid" role="radiogroup">
        <button class="opt${p.mode === "agent" ? " on" : ""}" data-mode="agent" ${locked ? "disabled" : ""}><div class="opt-ic">${I.terminal}</div>
          <b>用已连接的 Agent${hasReady ? ` <span class="badge badge--accent">推荐</span>` : ""}</b><span class="d">复用 Codex、pi 等 CLI 的账号与额度，无需 API Key。</span>
          <span class="check radio${p.mode === "agent" ? " on" : ""}">${I.check}</span></button>
        <button class="opt${p.mode === "api" ? " on" : ""}" data-mode="api" ${locked ? "disabled" : ""}><div class="opt-ic">${I.key}</div>
          <b>直接调用模型 API</b><span class="d">填写 OpenAI 兼容接口或本地 Ollama，由 Memlane 直接调用。</span>
          <span class="check radio${p.mode === "api" ? " on" : ""}">${I.check}</span></button>
      </div>
      ${body}
      ${chain ? renderChain(chain) : ""}
    </div>`;
  }

  function renderChain(c) {
    const row = (k, label) => {
      const s = c.steps[k];
      const ic = s === "ok" ? I.check : s === "fail" ? I.x : s === "run" ? '<span class="spinner"></span>' : '<span class="dot"></span>';
      const cls = s === "ok" ? "ok" : s === "fail" ? "fail" : s === "run" ? "" : "wait";
      const tt = k === "probe" && (s === "run" || s === "ok" || s === "fail") ? `<span class="t">${c.elapsed.toFixed(1)} 秒</span>` : "";
      return `<div class="chain-row ${cls}"><span class="ic">${ic}</span><span class="lbl">${label}</span>${tt}</div>`;
    };
    let note = "";
    if (c.steps.probe === "run" && c.elapsed >= 15) note = `<div class="chain-note warn">响应较慢：模型或网络仍在处理，无需重复点击。可继续等待或<button class="link" data-act="stopVerify">停止验证</button>。</div>`;
    if (c.steps.probe === "fail") note = `<div class="chain-note"><b style="color:var(--danger)">验证未通过</b> · HTTP 401 · 耗时 ${c.elapsed.toFixed(1)} 秒<br/>请检查 API Key 与模型权限。已保存当前配置，无需重新填写。</div>`;
    return `<div class="chain" id="chain" aria-live="polite">
      ${row("save", "保存配置")}
      ${row("probe", "发送一次简短测试请求")}
      ${note}
      ${row("enable", "启用并启动记忆提炼")}
    </div>`;
  }

  function pageDone() {
    const list = [...S.selected].map(agentById);
    const verified = list.filter(a => S.conn[a.id] === "verified").length;
    const pending = list.filter(a => S.conn[a.id] === "pending").length;
    const skipped = list.filter(a => !S.conn[a.id] || S.conn[a.id] === "skipped" || S.conn[a.id] === "todo").length;
    const proc = S.proc.deferred ? "稍后配置（采集照常进行，记忆待提炼）" : S.proc.mode === "agent" ? `${agentById(S.proc.cli)?.name || "Codex"} · ${agentById(S.proc.cli)?.model || ""}` : `${S.proc.api.preset === "ollama" ? "Ollama" : "OpenAI 兼容接口"} · ${S.proc.api.model}`;
    return `<div class="ob-col is-center"><div style="text-align:center">
      <div class="done-mark"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round"><path d="M5 12.5l4.5 4.5L19 7.5"/></svg></div>
      <h1 class="h1">Memlane 已就绪</h1>
      <p class="lead">本地记忆服务正在运行。关闭窗口后它会在菜单栏继续工作。</p>
      <div class="summary"><div class="rows">
        <div class="row"><div class="row-ic"><span class="dot ok"></span></div><div class="row-main"><div class="row-title">本地记忆服务</div><div class="row-desc">记忆引擎与接入网关 · 仅本机访问</div></div><div class="row-ctrl"><span class="comp-state ok">${I.check}运行中</span></div></div>
        <div class="row"><div class="row-ic"><span class="dot ${pending ? "run" : list.length ? "ok" : ""}"></span></div><div class="row-main"><div class="row-title">Agent 接入</div><div class="row-desc">${list.length ? `${verified} 个已生效 · ${pending} 个待新会话${skipped ? ` · ${skipped} 个未接入` : ""}` : "暂未接入"}</div></div>
          <div class="row-ctrl" style="gap:2px">${list.map(a => `<img src="${ICON_DIR}${a.icon}" width="18" height="18" style="opacity:${S.conn[a.id] === "verified" || S.conn[a.id] === "pending" ? 1 : .35}" alt="${esc(a.name)}" />`).join("")}</div></div>
        <div class="row"><div class="row-ic"><span class="dot ${S.proc.deferred ? "warn" : "ok"}"></span></div><div class="row-main"><div class="row-title">记忆提炼</div><div class="row-desc">${esc(proc)}</div></div></div>
      </div></div>
      ${pending ? `<div class="next-tip">${I.bulb}<div>下一步：重启已接入的 Agent，在项目里新建会话、正常提问。第一条记忆通常会在几分钟内出现在总览里。</div></div>` : ""}
      <div style="margin-top:var(--s8)"><button class="btn btn--primary btn--lg" data-act="finish">进入 Memlane <span class="kbd">↵</span></button></div>
    </div></div>`;
  }

  /* ---------- 底栏 ---------- */
  function renderFoot() {
    const f = $("#obFoot");
    const st = S.stage;
    f.className = "ob-foot" + (st === "welcome" || st === "done" ? " is-hidden" : "");
    let left = "", hint = "", prev = "", primary = "";
    const b = busy();
    if (st === "agents") {
      prev = `<button class="btn" data-go="welcome">上一步</button>`;
      primary = `<button class="btn btn--primary" data-act="next" ${S.detect !== "done" ? "disabled" : ""}>${S.detect === "done" && !detectedAgents().length ? "先跳过，继续准备环境" : "继续"} <span class="kbd">↵</span></button>`;
    } else if (st === "env") {
      const e = S.env.status;
      hint = e === "missing" ? "点击即表示同意安装上方「待下载」的组件" : e === "installing" ? "可以离开这个窗口，安装会继续进行" : e === "fail" ? "已完成的组件会保留" : "";
      prev = `<button class="btn" data-go="agents" ${b ? "disabled" : ""}>上一步</button>`;
      const label = { detecting: "检测中…", missing: `下载并安装 · ${envTotals().total.toFixed(1)} MiB`, ready: "启动服务并继续", installing: "正在安装…", starting: "正在启动…", fail: "重试", done: "继续" }[e];
      primary = `<button class="btn btn--primary" data-act="envPrimary" ${["detecting", "installing", "starting"].includes(e) ? "disabled" : ""}>${label}${["missing", "ready", "fail", "done"].includes(e) ? ' <span class="kbd">↵</span>' : ""}</button>`;
    } else if (st === "connect") {
      const list = [...S.selected];
      const todo = list.filter(id => !S.conn[id] || S.conn[id] === "todo");
      left = list.length && todo.length ? `<button class="btn btn--ghost btn--sm" data-act="skipConnect">稍后接入</button>` : "";
      hint = todo.length ? "写入前会自动备份原配置文件" : "";
      prev = `<button class="btn" data-go="env" ${b ? "disabled" : ""}>上一步</button>`;
      primary = todo.length
        ? `<button class="btn btn--primary" data-act="connectAll" ${b ? "disabled" : ""}>${todo.length === list.length ? "全部接入" : "接入其余"}（${todo.length}） <span class="kbd">↵</span></button>`
        : `<button class="btn btn--primary" data-act="next" ${b ? "disabled" : ""}>继续 <span class="kbd">↵</span></button>`;
    } else if (st === "process") {
      const c = S.proc.chain;
      const can = S.proc.mode === "agent" ? !!S.proc.cli && readyCLIs().length : S.proc.mode === "api" && (S.proc.api.preset === "ollama" || S.proc.api.key.trim());
      left = `<button class="btn btn--ghost btn--sm" data-act="defer" ${b ? "disabled" : ""}>稍后配置，先进入 Memlane</button>`;
      hint = c && c.steps.probe === "fail" ? "" : "会发送一次简短测试请求，可能消耗少量额度";
      prev = `<button class="btn" data-go="connect" ${b ? "disabled" : ""}>上一步</button>`;
      if (c && c.steps.probe === "fail") primary = `<button class="btn" data-act="editConfig">修改配置</button><button class="btn btn--primary" data-act="verify">重新验证 <span class="kbd">↵</span></button>`;
      else primary = `<button class="btn btn--primary" data-act="verify" ${!can || b ? "disabled" : ""}>${c && c.running ? "正在验证…" : "验证并启用"}${c && c.running ? "" : ' <span class="kbd">↵</span>'}</button>`;
    }
    f.innerHTML = `${left}<span class="hint">${hint}</span>${prev}${primary}`;
  }

  /* ---------- 渲染 ---------- */
  function render() {
    renderBar();
    const body = $("#obBody");
    const page = { welcome: pageWelcome, agents: pageAgents, env: pageEnv, connect: pageConnect, process: pageProcess, done: pageDone }[S.stage]();
    const scroll = body.scrollTop;
    body.innerHTML = page;
    if (lastStage !== S.stage) {
      const col = $(".ob-col", body);
      if (col) col.classList.add("pane-enter");
      body.scrollTop = 0;
      lastStage = S.stage;
    } else body.scrollTop = scroll;
    const lg = $("#envLog", body);
    if (lg) lg.scrollTop = lg.scrollHeight;
    const chain = $("#chain", body);
    const sig = chain ? JSON.stringify(S.proc.chain.steps) : "";
    if (chain && sig !== render.chainSig) chain.scrollIntoView({ block: "nearest", behavior: P.reduce ? "auto" : "smooth" });
    render.chainSig = sig;
    renderFoot();
    $("#pStage").value = S.stage;
  }

  /* ---------- 动作 ---------- */
  function envInstall() {
    S.env.status = "installing";
    S.env.error = "";
    logLine("已同意安装计划 · 计划摘要 sha256:7c1e04…a90b · 下载来源：" + (S.env.source === "cn" ? "国内镜像" : "官方源"));
    const queue = ENV_ITEMS.filter(it => S.env.comp[it.id].st !== "ok");
    const step = idx => {
      if (S.stage === "env" && S.env.status !== "installing") return;
      if (idx >= queue.length) return envStart();
      const it = queue[idx];
      const c = S.env.comp[it.id];
      c.st = "run";
      const lid = LOG_ID[it.id];
      logLine(`[${lid}] 开始下载 ${it.dl} MiB · 来源：${it.id === "embed" ? SRC() : "清华 PyPI 镜像"}`);
      let mark = 25;
      const tick = () => {
        if (S.env.status !== "installing") return;
        c.p = Math.min(100, c.p + (it.dl > 50 ? 4.5 : 11));
        if (c.p >= mark && c.p < 100) { logLine(`[${lid}] 已下载 ${(it.dl * c.p / 100).toFixed(1)} / ${it.dl} MiB`); mark += 25; }
        if (P.env === "fail" && it.id === "embed" && c.p >= 62) {
          logLine(`[${lid}] 读取超时（${SRC()}）· 自动切换备用源 2/3`);
          logLine(`[${lid}] 读取超时（hf-mirror.com）· 自动切换备用源 3/3`);
          logLine(`[${lid}] 失败：3/3 个备用源均超时，已保留已下载的 56.5 MiB，重试将断点续传`);
          S.open.add("log");
          c.st = "fail";
          S.env.status = "fail";
          S.env.comp.service.st = "pending";
          render();
          return;
        }
        if (c.p >= 100) { logLine(`[${lid}] SHA-256 校验通过 · 已解包到 ~/Library/Application Support/Memlane/${it.id === "embed" ? "model-cache" : "engine-components"}`); c.st = "ok"; render(); later(() => step(idx + 1), 200); return; }
        render();
        later(tick, 120);
      };
      render();
      later(tick, 120);
    };
    step(0);
  }
  function envStart() {
    S.env.status = "starting";
    S.env.comp.service.st = "run";
    logLine("[runtime] 校验上游源码 cognee 1.6.7 (ec6f408) 逐文件 SHA-256 · 通过");
    logLine("[runtime] 启动记忆引擎与接入网关 · 监听 127.0.0.1");
    render();
    later(() => {
      logLine("[runtime] 健康检查通过 · 本地记忆服务已就绪");
      S.env.comp.service.st = "ok";
      S.env.status = "done";
      S.done.add("env");
      render();
      later(() => { if (S.stage === "env") { go("connect"); toast("本地记忆环境已就绪"); } }, 1100);
    }, 1400);
  }

  function connectAll() {
    const ids = [...S.selected].filter(id => !S.conn[id] || S.conn[id] === "todo");
    ids.forEach(id => { S.conn[id] = "writing"; });
    render();
    ids.forEach((id, i) => later(() => {
      S.conn[id] = "pending";
      render();
      if (i === ids.length - 1) toast(`已接入 ${ids.length} 个 Agent，原配置已备份`);
    }, 450 * (i + 1)));
    // 模拟后台验证：Codex 在用户新开会话后自动转为「已生效」
    if (ids.includes("codex")) later(() => { if (S.conn.codex === "pending") { S.conn.codex = "verified"; render(); } }, 5200);
  }

  function runVerify() {
    const p = S.proc;
    p.chain = { running: true, ok: false, steps: { save: "run", probe: "wait", enable: "wait" }, elapsed: 0 };
    render();
    later(() => {
      p.chain.steps.save = "ok"; p.chain.steps.probe = "run"; render();
      const total = P.verify === "slow" ? 17 : 2.6;
      const tick = () => {
        if (!p.chain || !p.chain.running) return;
        p.chain.elapsed += P.verify === "slow" ? 1 : 0.2;
        if (p.chain.elapsed >= total) {
          if (P.verify === "fail" || (p.mode === "api" && p.api.key.trim() === "bad")) {
            p.chain.steps.probe = "fail"; p.chain.running = false; render(); return;
          }
          p.chain.steps.probe = "ok"; p.chain.steps.enable = "run"; render();
          later(() => {
            p.chain.steps.enable = "ok"; p.chain.running = false; p.chain.ok = true; S.done.add("process"); render();
            later(() => go("done"), 700);
          }, 1300);
          return;
        }
        render();
        later(tick, P.verify === "slow" ? 250 : 200);
      };
      later(tick, 200);
    }, 500);
  }

  function openMenu(anchor, items) {
    closeLayer();
    const win = $("#win").getBoundingClientRect();
    const r = anchor.getBoundingClientRect();
    const scale = win.width / 1200;
    const top = (r.bottom - win.top) / scale + 4;
    const right = (win.right - r.right) / scale;
    $("#obLayer").innerHTML = `<div class="menu" role="menu" style="top:${top}px;right:${right}px">${items.map(it => it === "-" ? "<hr/>" : `<button role="menuitem" data-menu="${it.id}">${it.icon || ""}${it.label}</button>`).join("")}</div>`;
    $$("[data-menu]", $("#obLayer")).forEach(b => b.onclick = e => {
      e.stopPropagation();
      const it = items.find(x => x.id === b.dataset.menu);
      closeLayer();
      it.run();
    });
  }
  function openDialog({ title, body, confirm, onConfirm, hint = "", wide = false }) {
    closeLayer();
    $("#obLayer").innerHTML = `<div class="scrim" data-scrim><div class="dialog${wide ? " w640" : ""}" role="dialog" aria-modal="true">
      <div class="dialog-head">${title}</div><div class="dialog-body">${body}</div>
      <div class="dialog-foot"><span class="hint">${hint}</span><button class="btn" data-dlg="cancel">关闭</button>${confirm ? `<button class="btn btn--primary" data-dlg="ok">${confirm}</button>` : ""}</div></div></div>`;
    $("[data-scrim]").onclick = e => { if (e.target.dataset.scrim !== undefined) closeLayer(); };
    $("[data-dlg=cancel]").onclick = closeLayer;
    const ok = $("[data-dlg=ok]");
    if (ok) { ok.onclick = () => { closeLayer(); onConfirm && onConfirm(); }; ok.focus(); }
  }
  function closeLayer() { const l = $("#obLayer"); if (l) l.innerHTML = ""; }

  const helpItems = () => [
    { id: "details", label: "查看环境详情", icon: I.doc, run: () => openDialog({ wide: true, title: "环境详情", hint: "路径与校验结果已脱敏，不含密钥或聊天内容", confirm: "复制诊断材料", onConfirm: () => toast("脱敏诊断材料已复制"),
      body: `<div class="rows tech">${TECH.map(r => `<div class="row"><div class="row-main"><div class="row-title">${r[0]} <span class="ver">${r[1]}</span></div><div class="row-desc">${r[2]}</div></div></div>`).join("")}</div>` }) },
    { id: "diag", label: "复制诊断，交给 Agent 排查", icon: I.copy, run: () => toast("已复制排查提示词，请在 Agent 新会话中粘贴") },
    { id: "export", label: "导出诊断包…", icon: I.download, run: () => toast("诊断包已导出") },
    "-",
    { id: "paths", label: "指定已有组件路径…", icon: I.folder, run: () => toast("（原型）打开路径对话框") },
  ];

  /* ---------- 事件 ---------- */
  document.addEventListener("click", e => {
    const t = e.target.closest("[data-go],[data-act],[data-toggle],[data-disc],[data-src],[data-mode],[data-cli],[data-more],#btnHelp");
    if (!t) { if (!e.target.closest(".menu,.dialog")) closeLayer(); return; }
    if (t.id === "btnHelp") { e.stopPropagation(); return openMenu(t, helpItems()); }
    if (t.dataset.go) return go(t.dataset.go);
    if (t.dataset.toggle) {
      const id = t.dataset.toggle;
      S.selectedTouched = true;
      S.selected.has(id) ? S.selected.delete(id) : S.selected.add(id);
      return render();
    }
    if (t.dataset.disc) { const k = t.dataset.disc; S.open.has(k) ? S.open.delete(k) : S.open.add(k); return render(); }
    if (t.dataset.src) { S.env.source = t.dataset.src; return render(); }
    if (t.dataset.mode) { if (S.proc.chain?.running) return; S.proc.mode = t.dataset.mode; S.proc.chain = null; if (S.proc.mode === "agent" && !S.proc.cli) S.proc.cli = readyCLIs()[0]?.id || null; return render(); }
    if (t.dataset.cli) { S.proc.cli = t.dataset.cli; S.proc.chain = null; return render(); }
    if (t.dataset.more) {
      e.stopPropagation();
      const id = t.dataset.more, a = agentById(id), st = S.conn[id] || "todo";
      const items = [];
      if (st === "todo" || st === "skipped") items.push({ id: "one", label: "只接入这个", icon: I.check, run: () => { S.conn[id] = "writing"; render(); later(() => { S.conn[id] = "pending"; render(); }, 500); } });
      if (st === "todo") items.push({ id: "skip", label: "跳过这个", icon: I.skip, run: () => { S.conn[id] = "skipped"; render(); } });
      items.push({ id: "dir", label: "更换配置目录…", icon: I.folder, run: () => toast("（原型）选择 " + a.name + " 配置目录") });
      if (st === "pending") items.push({ id: "recheck", label: "立即重新检测", icon: I.refresh, run: () => toast("仍在等待新会话，稍后会自动更新") });
      return openMenu(t, items);
    }
    const act = t.dataset.act;
    switch (act) {
      case "start": go("agents"); break;
      case "redetect": toast("已重新检测本机 Agent"); break;
      case "supportList": openSupportList(); break;
      case "next": go(STAGES[STAGES.indexOf(S.stage) + 1]); break;
      case "agentInstall": {
        const names = (S.selected.size ? [...S.selected] : detectedAgents().map(a => a.id)).map(id => agentById(id)?.name).filter(Boolean);
        const prompt = `帮我完成本机 Memlane 的首次安装与配置（无需安装 Skill）：
1. 先只读运行：/Applications/Memlane.app/Contents/MacOS/Memlane doctor --json
2. 按 next_actions 顺序推进：所有 requires=explicit_confirmation 的 --apply 操作，先给我看 --plan / --dry-run 预览并等我确认；若返回退出码 20，按 retry_after_ms 用 doctor --operation-id <id> --json 轮询进度直到完成。
3. 严禁读取或输出任何 token/密钥/.local/聊天内容，严禁手动修改 hooks.json/Cognee 源码。若返回退出码 11 或同一问题重试 3 次仍失败，立刻停止并把 human_steps 交给我。${names.length ? `\n（我计划优先接入的 Agent：${names.join("、")}）` : ""}`;
        openDialog({ title: "让 Agent 代我安装", confirm: "复制指令", onConfirm: () => toast("已复制，请在 Agent 的新会话中粘贴"), hint: "完成后回到这里，Memlane 会自动识别",
          body: `<p style="margin:0">复制一段指令，粘贴到 ${esc(names.slice(0, 2).join("、") || "Codex、TRAE CN")} 等 Agent 的新会话里，它会在终端里替你完成全部准备：</p>
          <ol class="agent-plan"><li>先检查本机环境，告诉你还缺什么</li><li>每次要改动前先给你看预览，等你确认</li><li>下载组件、接入 Agent、启动本地记忆服务</li><li>遇到需要你亲自操作的步骤（比如重启 Agent）会停下来告诉你</li></ol>
          <p class="muted" style="margin:0">指令不会读取密钥或聊天内容，也不会手动改动配置文件。</p>
          <details class="full-prompt"><summary>查看完整指令</summary><pre>${esc(prompt)}</pre></details>` });
        break;
      }
      case "envPrimary": {
        const st = S.env.status;
        if (st === "missing" || st === "fail") {
          if (st === "fail") ENV_ITEMS.forEach(it => { const c = S.env.comp[it.id]; if (c.st === "fail") { c.st = "missing"; } });
          if (st === "fail") P.env = "missing";
          envInstall();
        } else if (st === "ready") envStart();
        else if (st === "done") go("connect");
        break;
      }
      case "stopInstall":
        clearTimers();
        ENV_ITEMS.forEach(it => { const c = S.env.comp[it.id]; if (c.st === "run") c.st = "missing"; });
        S.env.status = "missing"; render(); toast("已停止，已完成的组件会保留"); break;
      case "retryOfficial": S.env.source = "official"; P.env = "missing"; ENV_ITEMS.forEach(it => { const c = S.env.comp[it.id]; if (c.st === "fail") c.st = "missing"; }); envInstall(); break;
      case "copyDiag": toast("已复制排查提示词，请在 Agent 新会话中粘贴"); break;
      case "copyLog": toast("已复制脱敏安装日志"); break;
      case "copyCmd": toast("已复制全部安装命令"); break;
      case "manualPath": toast("（原型）打开「指定已有组件路径」对话框"); break;
      case "connectAll": connectAll(); break;
      case "skipConnect": [...S.selected].forEach(id => { if (!S.conn[id] || S.conn[id] === "todo") S.conn[id] = "skipped"; }); go("process"); break;
      case "verify": runVerify(); break;
      case "stopVerify": S.proc.chain = null; clearTimers(); render(); toast("已停止验证，配置已保存"); break;
      case "editConfig": S.proc.chain = null; render(); break;
      case "defer": S.proc.deferred = true; S.done.add("process"); go("done"); break;
      case "finish": toast("（原型）进入总览"); break;
      case "copy": toast("已复制：" + t.dataset.text); break;
      case "toast": toast(t.dataset.text); break;
    }
  });
  document.addEventListener("input", e => {
    const k = e.target.dataset.api;
    if (!k) return;
    S.proc.api[k] = e.target.value;
    if (k === "preset") {
      Object.assign(S.proc.api, e.target.value === "ollama" ? { base: "http://127.0.0.1:11434", model: "qwen3:8b" } : { base: "https://api.openai.com/v1", model: "gpt-4.1-mini" });
      render();
    } else renderFoot();
  });
  document.addEventListener("keydown", e => {
    if (e.key === "Escape") return closeLayer();
    if (e.key !== "Enter" || e.isComposing) return;
    if ($("#obLayer").children.length) return;
    if (["INPUT", "SELECT", "TEXTAREA"].includes(document.activeElement?.tagName) && document.activeElement.closest("#protoBar")) return;
    const primary = S.stage === "welcome" ? $("[data-act=start]") : S.stage === "done" ? $("[data-act=finish]") : $("#obFoot .btn--primary");
    if (primary && !primary.disabled) { e.preventDefault(); primary.click(); }
  });

  /* ---------- 原型控制 ---------- */
  function fit() {
    const wrap = $("#stageWrap").getBoundingClientRect();
    const s = Math.min(1, (wrap.width - 32) / 1200, (wrap.height - 32) / 820);
    $("#win").style.transform = `scale(${s})`;
  }
  function applyProto() {
    root.dataset.theme = P.theme;
    root.classList.toggle("reduce", P.reduce);
  }
  function reset(stage = "welcome") {
    clearTimers();
    S = fresh();
    lastStage = null;
    if (stage !== "welcome") {
      S.detect = "done";
      detectedAgents().forEach(a => S.selected.add(a.id));
      resolveEnvDetect();
      if (["connect", "process", "done"].includes(stage)) { S.env.status = "done"; Object.values(S.env.comp).forEach(c => c.st = "ok"); }
      if (["process", "done"].includes(stage)) { S.conn = { trae_ide: "pending", codex: "verified", pi: "pending", traex: "pending" }; }
      if (stage === "done") { S.proc.mode = readyCLIs().length ? "agent" : "api"; S.proc.cli = readyCLIs()[0]?.id || null; }
    }
    go(stage);
  }

  const params = new URLSearchParams(location.search);
  P.theme = params.get("theme") || "light";
  P.env = params.get("env") || "missing";
  P.hosts = params.get("hosts") || "some";
  P.cli = params.get("cli") || "yes";
  P.verify = params.get("verify") || "pass";
  P.reduce = params.get("reduce") === "1";
  $("#pTheme").value = P.theme; $("#pHosts").value = P.hosts; $("#pEnv").value = P.env; $("#pCli").value = P.cli; $("#pVerify").value = P.verify; $("#pReduce").checked = P.reduce;
  $("#pTheme").onchange = e => { P.theme = e.target.value; applyProto(); };
  $("#pReduce").onchange = e => { P.reduce = e.target.checked; applyProto(); };
  $("#pHosts").onchange = e => { P.hosts = e.target.value; reset(S.stage === "welcome" ? "welcome" : "agents"); };
  $("#pEnv").onchange = e => { P.env = e.target.value; reset(S.stage === "welcome" ? "welcome" : "env"); };
  $("#pCli").onchange = e => { P.cli = e.target.value; S.proc.mode = null; S.proc.chain = null; render(); if (S.stage === "process") go("process"); };
  $("#pVerify").onchange = e => { P.verify = e.target.value; };
  $("#pStage").onchange = e => reset(e.target.value);
  $("#pReset").onclick = () => reset("welcome");
  window.addEventListener("resize", fit);
  window.__ob = { reset, go, get S() { return S; }, P, connectAll, runVerify, render };

  applyProto();
  fit();
  reset(params.get("stage") || "welcome");
})();
