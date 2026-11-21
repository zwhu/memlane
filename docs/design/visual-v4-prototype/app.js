(() => {
  const D = window.MEMLANE_DATA;
  const UI = window.MemlaneUI;
  const root = document.documentElement;
  const $ = (s, el = document) => el.querySelector(s);
  const $$ = (s, el = document) => [...el.querySelectorAll(s)];

  /* ---------- 弹簧：与 MyGo SpringOptions{Duration, Bounce} 同义，生成 CSS linear() ---------- */
  function springEasing(duration, bounce) {
    const k = Math.pow((2 * Math.PI) / duration, 2);
    const c = (4 * Math.PI * (1 - bounce)) / duration;
    let x = 0, v = 0, t = 0, settle = 0;
    const dt = 1 / 600, samples = [0];
    while (t < duration * 3) {
      v += (k * (1 - x) - c * v) * dt; x += v * dt; t += dt; samples.push(x);
      if (Math.abs(1 - x) > 0.002 || Math.abs(v) > 0.02) settle = t;
    }
    settle = Math.max(settle, duration * 0.6);
    const pts = [];
    for (let i = 0; i <= 48; i++) pts.push(+samples[Math.min(samples.length - 1, Math.round((settle * i) / 48 / dt))].toFixed(4));
    pts[48] = 1;
    return { easing: `linear(${pts.join(", ")})`, ms: Math.round(settle * 1000) };
  }
  if (CSS.supports("transition-timing-function", "linear(0, 1)")) {
    for (const [name, d, b] of [["smooth", 0.35, 0], ["snappy", 0.3, 0.15], ["bouncy", 0.42, 0.3]]) {
      const s = springEasing(d, b);
      root.style.setProperty(`--spring-${name}`, s.easing);
      root.style.setProperty(`--dur-${name}`, s.ms + "ms");
    }
  }

  /* ---------- 图标：线性；选中切换为实心 ---------- */
  const ICONS = {
    overview: ['<rect x="3" y="3" width="6" height="6" rx="1.5"/><rect x="11" y="3" width="6" height="6" rx="1.5"/><rect x="3" y="11" width="6" height="6" rx="1.5"/><rect x="11" y="11" width="6" height="6" rx="1.5"/>', true],
    common: ['<path d="M5 3.5h10a1 1 0 0 1 1 1v12l-6-3.2-6 3.2v-12a1 1 0 0 1 1-1z" stroke-linejoin="round"/>', true],
    profile: ['<circle cx="10" cy="7" r="3.25"/><path d="M4 16.5c.8-2.9 3.2-4.5 6-4.5s5.2 1.6 6 4.5z" stroke-linejoin="round"/>', true],
    folder: ['<path d="M2.75 5.5a1.5 1.5 0 0 1 1.5-1.5h3.4l1.6 1.75h6.5a1.5 1.5 0 0 1 1.5 1.5v7.25a1.5 1.5 0 0 1-1.5 1.5H4.25a1.5 1.5 0 0 1-1.5-1.5z" stroke-linejoin="round"/>', true],
    settings: ['<circle cx="10" cy="10" r="2.5"/><path d="M10 2.75v2M10 15.25v2M2.75 10h2M15.25 10h2M4.9 4.9l1.4 1.4M13.7 13.7l1.4 1.4M4.9 15.1l1.4-1.4M13.7 6.3l1.4-1.4" stroke-linecap="round"/>', false],
    search: ['<circle cx="9" cy="9" r="5.25"/><path d="M13 13l3.5 3.5" stroke-linecap="round"/>', false],
    chev: ['<path d="M8 5l5 5-5 5" stroke-linecap="round" stroke-linejoin="round"/>', false],
    down: ['<path d="M5 8l5 5 5-5" stroke-linecap="round" stroke-linejoin="round"/>', false],
    doc: ['<path d="M5.5 2.75h6l3.5 3.5v11h-9.5z M11.5 2.75v3.5H15 M8 10h5M8 13h4" stroke-linejoin="round" stroke-linecap="round"/>', false],
    copy: ['<rect x="7" y="7" width="9.5" height="9.5" rx="1.5"/><path d="M13 7V4.75a1.5 1.5 0 0 0-1.5-1.5H4.75a1.5 1.5 0 0 0-1.5 1.5v6.75a1.5 1.5 0 0 0 1.5 1.5H7"/>', false],
    link: ['<path d="M8.5 11.5l3-3M7 9.5l-1.8 1.8a2.5 2.5 0 0 0 3.5 3.5L10.5 13M13 10.5l1.8-1.8a2.5 2.5 0 0 0-3.5-3.5L9.5 7" stroke-linecap="round"/>', false],
    close: ['<path d="M5 5l10 10M15 5L5 15" stroke-linecap="round"/>', false],
    recall: ['<path d="M4 10a6 6 0 1 0 1.8-4.3M4 3.5v3h3" stroke-linecap="round" stroke-linejoin="round"/>', false],
    check: ['<path d="M4.5 10.5l3.5 3.5 7.5-8" stroke-linecap="round" stroke-linejoin="round"/>', false],
    info: ['<circle cx="10" cy="10" r="7"/><path d="M10 9v5M10 6.5v.01" stroke-linecap="round"/>', false],
    refresh: ['<path d="M15.8 10a5.8 5.8 0 1 1-1.6-4M15.5 3.5v3.2h-3.2" stroke-linecap="round" stroke-linejoin="round"/>', false],
    upload: ['<path d="M10 13V4.5M6.5 8L10 4.5 13.5 8M4 15.5h12" stroke-linecap="round" stroke-linejoin="round"/>', false],
    sun: ['<circle cx="10" cy="10" r="3.25"/><path d="M10 2.5v1.75M10 15.75v1.75M2.5 10h1.75M15.75 10h1.75M4.7 4.7l1.25 1.25M14.05 14.05l1.25 1.25M4.7 15.3l1.25-1.25M14.05 5.95l1.25-1.25" stroke-linecap="round"/>', false],
    moon: ['<path d="M15.2 12.6A5.8 5.8 0 0 1 7.4 4.8a6.2 6.2 0 1 0 7.8 7.8z" stroke-linejoin="round"/>', false],
    server: ['<rect x="3.5" y="4" width="13" height="4.5" rx="1.5"/><rect x="3.5" y="11.5" width="13" height="4.5" rx="1.5"/><path d="M6.5 6.25h.01M6.5 13.75h.01" stroke-linecap="round"/>', false],
    shield: ['<path d="M10 3l6 2.5v4.4c0 3.7-2.5 6.4-6 7.6-3.5-1.2-6-3.9-6-7.6V5.5L10 3z" stroke-linejoin="round"/>', false],
    trash: ['<path d="M4.5 6h11M8 6V4.5a1 1 0 0 1 1-1h2a1 1 0 0 1 1 1V6M6 6l.7 9.5a1.5 1.5 0 0 0 1.5 1.4h3.6a1.5 1.5 0 0 0 1.5-1.4L14 6" stroke-linecap="round" stroke-linejoin="round"/>', false],
    plus: ['<path d="M10 4.5v11M4.5 10h11" stroke-linecap="round"/>', false],
    terminal: ['<rect x="3" y="4" width="14" height="12" rx="2"/><path d="M6.5 8.5l2.5 2-2.5 2M10.5 12.5h3" stroke-linecap="round" stroke-linejoin="round"/>', false],
    alert: ['<path d="M10 3.5l7 12.5H3l7-12.5zM10 8.25v3.5M10 14v.01" stroke-linecap="round" stroke-linejoin="round"/>', false],
    arrowLeft: ['<path d="M12.5 5l-5 5 5 5" stroke-linecap="round" stroke-linejoin="round"/>', false],
    key: ['<circle cx="7" cy="10" r="3"/><path d="M10 10h7M14.5 10v2.25M17 10v1.75" stroke-linecap="round"/>', false],
    cpu: ['<rect x="6" y="6" width="8" height="8" rx="1.5"/><path d="M8 3.5V6M12 3.5V6M8 14v2.5M12 14v2.5M3.5 8H6M3.5 12H6M14 8h2.5M14 12h2.5" stroke-linecap="round"/>', false],
  };
  const icon = (name, filled = false) => {
    const entry = ICONS[name] || ICONS.info;
    const [p, can] = entry;
    return `<svg viewBox="0 0 20 20" width="100%" height="100%" fill="${filled && can ? "currentColor" : "none"}" stroke="currentColor" stroke-width="1.5">${p}</svg>`;
  };

  /* ---------- 状态 ---------- */
  const PROJECT_TABS = [
    { id: "memory", label: "记忆" },
    { id: "raw", label: "原始记录" },
    { id: "graph", label: "图谱" },
    { id: "manage", label: "管理" },
  ];
  const state = {
    page: "overview",
    project: "memlane",
    tab: 0,
    cat: "",
    unreadOnly: false,
    sort: "time",
    recall: null,
    selected: {},
    day: "",
    actTab: 0,
    growHeat: false,
    todayExtra: 0,
    // 画像状态
    selectedProfile: D.profiles[0] ? D.profiles[0].id : "",
    profiles: D.profiles.map(p => ({ ...p })),
    profileDrafts: {},
    // 扩展页面共享状态（供 pages-settings / pages-search / pages-setup 使用）
    settingsTab: "processing", // "processing" | "agents" | "service"
    searchQuery: "",
    searchScope: "all",        // "all" | "memory" | "raw"
    searchProjectOnly: false,
    searchSelectedId: "",
    setupStep: 0,
    // 数据副本
    memories: D.memories.map(m => ({ ...m })),
    activity: D.activity.slice(),
  };
  const unreadCount = p => state.memories.filter(m => m.project === p && m.unread).length;

  /* ---------- 页面扩展注册机制（window.MemlanePages） ---------- */
  const registeredPages = new Map();
  const registeredProjectTabs = new Map();
  let commandPaletteHandler = null;
  let activePageId = null;
  let booted = false;

  const MemlanePages = {
    register(def) {
      if (!def || !def.id) throw new Error("MemlanePages.register requires { id }");
      registeredPages.set(def.id, def);
      // 若注册了新页面且控制条里还没有按钮，自动补充
      const bar = $("#protoPages");
      if (bar && def.navLabel && !bar.querySelector(`[data-page-choice="${def.id}"]`)) {
        const b = document.createElement("button");
        b.dataset.pageChoice = def.id;
        b.textContent = def.navLabel;
        b.onclick = () => go(def.id);
        bar.appendChild(b);
      }
      if (booted && state.page === def.id) render(false);
      return def;
    },
    get(id) { return registeredPages.get(id) || null; },
    list() { return [...registeredPages.values()]; },

    registerProjectTab(idOrIndex, def) {
      const key = typeof idOrIndex === "number" ? (PROJECT_TABS[idOrIndex] ? PROJECT_TABS[idOrIndex].id : String(idOrIndex)) : String(idOrIndex);
      registeredProjectTabs.set(key, { id: key, fullWidth: key === "manage", ...def });
      if (booted && state.page === "project" && PROJECT_TABS[state.tab] && PROJECT_TABS[state.tab].id === key) {
        render(false);
      }
      return registeredProjectTabs.get(key);
    },
    getProjectTab(idOrIndex) {
      const key = typeof idOrIndex === "number" ? (PROJECT_TABS[idOrIndex] ? PROJECT_TABS[idOrIndex].id : String(idOrIndex)) : String(idOrIndex);
      return registeredProjectTabs.get(key) || null;
    },

    registerCommandPalette(handler) {
      commandPaletteHandler = handler;
    },
    openCommandPalette(opts = {}) {
      closeServicePopover();
      UI.closeMenus();
      if (commandPaletteHandler && typeof commandPaletteHandler.open === "function") {
        commandPaletteHandler.open(opts);
        return true;
      }
      toast("⌘K 全局搜索面板尚未注册");
      return false;
    },
    closeCommandPalette() {
      if (commandPaletteHandler && typeof commandPaletteHandler.close === "function") {
        commandPaletteHandler.close();
      }
    },
    isCommandPaletteOpen() {
      return !!(commandPaletteHandler && typeof commandPaletteHandler.isOpen === "function" && commandPaletteHandler.isOpen());
    },
  };
  window.MemlanePages = MemlanePages;

  /* ---------- 主题 / 减少动态 ---------- */
  const mq = matchMedia("(prefers-color-scheme: dark)");
  let themeChoice = "light";
  const themeListeners = new Set();
  const getResolvedTheme = () => (themeChoice === "system" ? (mq.matches ? "dark" : "light") : themeChoice);
  const applyTheme = () => {
    root.dataset.theme = getResolvedTheme();
    syncProto();
    updateThemeToggleIcon();
    themeListeners.forEach(fn => { try { fn(themeChoice, root.dataset.theme); } catch (_) {} });
  };
  function setTheme(choice) {
    if (!["light", "dark", "system"].includes(choice)) return;
    themeChoice = choice;
    applyTheme();
  }
  mq.addEventListener("change", applyTheme);
  $$("[data-theme-choice]").forEach(b => b.onclick = () => setTheme(b.dataset.themeChoice));

  const reduce = $("#reduce");
  reduce.checked = matchMedia("(prefers-reduced-motion: reduce)").matches;
  const applyReduce = () => root.classList.toggle("reduce", reduce.checked);
  reduce.onchange = applyReduce;
  applyReduce();

  function syncProto() {
    $$("[data-theme-choice]").forEach(b => b.classList.toggle("on", b.dataset.themeChoice === themeChoice));
    $$("[data-page-choice]").forEach(b => b.classList.toggle("on", b.dataset.pageChoice === state.page));
  }
  $$("[data-page-choice]").forEach(b => b.onclick = () => go(b.dataset.pageChoice));
  $("#openCmdK").onclick = () => MemlanePages.openCommandPalette();

  function updateThemeToggleIcon() {
    const btn = $("#themeToggle");
    if (!btn) return;
    const isDark = getResolvedTheme() === "dark";
    btn.innerHTML = icon(isDark ? "sun" : "moon");
    const modeName = themeChoice === "system" ? "跟随系统" : (isDark ? "深色" : "浅色");
    btn.title = `当前外观：${modeName}（点击切换浅/深色，右键选择三态）`;
  }

  /* ---------- 文本与视觉小件 ---------- */
  const esc = s => String(s ?? "").replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/"/g, "&quot;");
  const fmt = s => esc(s).replace(/`([^`]+)`/g, (_, c) => `<code>${c.replace(/([/_.])/g, "$1<wbr>")}</code>`);
  const plain = s => String(s ?? "").replace(/`/g, "");
  function logo(agent, size = 20) {
    const a = D.agents[agent] || { name: agent, mark: String(agent || "?")[0].toUpperCase(), color: "var(--fill-2)", fg: "var(--text-1)" };
    return `<span class="logo" title="${esc(a.name)}" style="width:${size}px;height:${size}px;font-size:${Math.round(size * .55)}px;border-radius:${Math.round(size * .28)}px;background:${a.color};color:${a.fg}">${esc(a.mark)}</span>`;
  }
  const recallTag = n => `<span class="recall ${n ? "" : "zero"}" title="被 Agent 召回 ${n} 次">${n ? icon("recall") : ""}召回 ${n} 次</span>`;
  const searchBoxHTML = () => `<button type="button" class="search" id="gsearch"><span class="ico">${icon("search")}</span>搜索记忆与原始记录<span class="kbd">⌘K</span></button>`;
  const emptyHTML = t => `<div class="empty" id="empty"><div class="blocks"><i></i><i></i><i></i></div><div>${esc(t)}</div></div>`;
  const stageHTML = (lg = false) => `<span class="stage${lg ? " lg" : ""}">${[0, 1, 2, 3, 4, 5].map(k => `<i style="--k:${k}"></i>`).join("")}</span>`;

  /* ---------- 服务状态悬停小浮层（Q6：悬停 300ms 显示、移出 200ms 收起、移入保持、聚焦显示、单击直达设置·服务） ---------- */
  let servicePopoverEl = null;
  let serviceShowTimer = null;
  let serviceHideTimer = null;
  let servicePointerDown = false;

  function clearServiceTimers() {
    clearTimeout(serviceShowTimer);
    clearTimeout(serviceHideTimer);
    serviceShowTimer = null;
    serviceHideTimer = null;
  }
  function scheduleShowServicePopover(delay = 300) {
    clearTimeout(serviceHideTimer);
    serviceHideTimer = null;
    if (servicePopoverEl || serviceShowTimer) return;
    serviceShowTimer = setTimeout(() => {
      serviceShowTimer = null;
      openServicePopover();
    }, delay);
  }
  function scheduleHideServicePopover(delay = 200) {
    clearTimeout(serviceShowTimer);
    serviceShowTimer = null;
    if (!servicePopoverEl) return;
    clearTimeout(serviceHideTimer);
    serviceHideTimer = setTimeout(() => {
      serviceHideTimer = null;
      closeServicePopover();
    }, delay);
  }
  function closeServicePopover() {
    clearServiceTimers();
    if (servicePopoverEl) {
      servicePopoverEl.remove();
      servicePopoverEl = null;
    }
    const btn = $("#serviceBtn");
    if (btn) {
      btn.classList.remove("is-open");
      btn.setAttribute("aria-expanded", "false");
    }
  }
  function openServicePopover() {
    clearServiceTimers();
    if (servicePopoverEl) return;
    UI.closeMenus();
    const s = D.serviceStatus;
    const el = document.createElement("div");
    el.className = "service-popover";
    el.id = "servicePopover";
    el.setAttribute("role", "dialog");
    el.setAttribute("aria-label", "服务组件状态");
    el.innerHTML = `
      <div class="sp-head">
        <span class="dot ${s.overall === "ready" ? "ok" : (s.overall === "warning" ? "warn" : "fail")}"></span>
        <span class="sp-title">${esc(s.label)}</span>
        <span class="sp-time">更新于 ${esc(s.checkedAt)}</span>
      </div>
      <div class="sp-list">
        ${s.components.map(c => `
          <div class="sp-row">
            <span class="dot ${c.state === "ok" ? "ok" : (c.state === "warn" ? "warn" : "fail")}"></span>
            <div class="sp-row-main">
              <div class="sp-row-top">
                <span class="sp-name">${esc(c.name)}</span>
                <span class="sp-state ${c.state === "fail" ? "fail" : ""}">${esc(c.statusText)}</span>
              </div>
              <div class="sp-metrics">${esc(c.metrics)}</div>
            </div>
          </div>
        `).join("")}
      </div>
      <div class="sp-foot">
        <button type="button" class="sp-detail-btn" id="spGoService">
          <span>在设置中查看详情</span>
          ${icon("chev")}
        </button>
      </div>
    `;
    el.onmouseenter = () => clearServiceTimers();
    el.onmouseleave = () => scheduleHideServicePopover(200);
    el.addEventListener("focusin", () => clearServiceTimers());
    el.addEventListener("focusout", e => {
      if (!el.contains(e.relatedTarget) && e.relatedTarget !== $("#serviceBtn")) {
        scheduleHideServicePopover(200);
      }
    });
    $("#window").appendChild(el);
    servicePopoverEl = el;
    const btn = $("#serviceBtn");
    if (btn) {
      btn.classList.add("is-open");
      btn.setAttribute("aria-expanded", "true");
    }
    $("#spGoService", el).onclick = () => {
      closeServicePopover();
      go("settings", { tab: "service" });
    };
  }

  document.addEventListener("pointerdown", e => {
    if (servicePopoverEl && !e.target.closest("#servicePopover") && !e.target.closest("#serviceBtn")) {
      closeServicePopover();
    }
  });

  /* ---------- 侧栏 ---------- */
  function renderNav() {
    const ext = registeredPages.get(state.page);
    const hideSidebar = !!(ext && ext.hideSidebar);
    $("#window").classList.toggle("no-sidebar", hideSidebar);

    const items = [["overview", "总览"], ["common", "通用记忆"], ["profile", "用户画像"]];
    $("#navItems").innerHTML = items.map(([id, label]) => {
      const on = state.page === id;
      return `<button class="nav-item ${on ? "on" : ""}" data-nav="${id}"><span class="ico">${icon(id, on)}</span><span class="label">${label}</span></button>`;
    }).join("");
    $("#navProjects").innerHTML = D.projects.map(p => {
      const on = state.page === "project" && state.project === p.id, n = unreadCount(p.id);
      return `<button class="nav-item ${on ? "on" : ""}" data-nav="project" data-project="${p.id}" title="${esc(p.path)}"><span class="ico">${icon("folder", on)}</span><span class="label">${esc(p.name)}</span>${n ? `<span class="badge" data-unread="${p.id}" title="${n} 条未读记忆，点击查看">${n}</span>` : ""}</button>`;
    }).join("");

    const settingsBtn = $("#navSettings");
    const settingsOn = state.page === "settings";
    settingsBtn.classList.toggle("on", settingsOn);
    $("#settingsIco").innerHTML = icon("settings", settingsOn);

    $$(".nav-item").forEach(b => b.onclick = e => {
      const nav = b.dataset.nav;
      if (e.target.dataset.unread) {
        state.unreadOnly = true; state.cat = ""; state.recall = null;
        return go("project", { project: e.target.dataset.unread, tab: 0, force: true });
      }
      if (nav === "overview") go("overview");
      else if (nav === "profile") go("profile");
      else if (nav === "settings") go("settings");
      else if (nav === "project") {
        state.unreadOnly = false; state.recall = null; state.cat = "";
        go("project", { project: b.dataset.project, tab: 0 });
      } else if (nav === "common") {
        toast("本次 V4 范围不含通用记忆");
      }
    });
    $("#allProjects").onclick = $("#addProject").onclick = () => toast("项目列表共 8 个（原型展示前 4 个）");

    const ind = $("#navIndicator"), on = $(".nav .nav-item.on");
    ind.style.setProperty("--o", on ? 1 : 0);
    if (on) ind.style.setProperty("--y", on.offsetTop + "px");

    // 底部服务浮层（悬停 300ms / 移出 200ms / 聚焦显示 / 单击直达设置·服务）& 外观快捷切换
    const serviceBtn = $("#serviceBtn");
    if (serviceBtn) {
      serviceBtn.onpointerdown = () => { servicePointerDown = true; };
      serviceBtn.onmouseenter = () => scheduleShowServicePopover(300);
      serviceBtn.onmouseleave = () => scheduleHideServicePopover(200);
      serviceBtn.onfocus = () => {
        if (servicePointerDown) { servicePointerDown = false; return; }
        clearServiceTimers();
        openServicePopover();
      };
      serviceBtn.onblur = e => {
        if (!servicePopoverEl || !servicePopoverEl.contains(e.relatedTarget)) {
          scheduleHideServicePopover(200);
        }
      };
      serviceBtn.onclick = () => {
        servicePointerDown = false;
        closeServicePopover();
        go("settings", { tab: "service" });
      };
    }

    const themeBtn = $("#themeToggle");
    if (themeBtn) {
      updateThemeToggleIcon();
      themeBtn.onclick = () => setTheme(getResolvedTheme() === "dark" ? "light" : "dark");
      themeBtn.oncontextmenu = e => {
        e.preventDefault();
        UI.openContextMenu(e.clientX, e.clientY, [
          { v: "system", label: "跟随系统", on: themeChoice === "system" },
          { v: "light", label: "浅色", on: themeChoice === "light" },
          { v: "dark", label: "深色", on: themeChoice === "dark" },
        ], v => setTheme(v));
      };
    }
  }

  function resolveTabIndex(tab) {
    if (typeof tab === "number") return Math.max(0, Math.min(PROJECT_TABS.length - 1, tab));
    const idx = PROJECT_TABS.findIndex(t => t.id === tab);
    return idx >= 0 ? idx : 0;
  }

  function go(page, projectOrOpts, forceArg) {
    let opts = {};
    if (typeof projectOrOpts === "string") opts = { project: projectOrOpts, force: !!forceArg };
    else if (projectOrOpts && typeof projectOrOpts === "object") opts = projectOrOpts;

    const prevPage = state.page;
    const nextProject = opts.project || state.project;
    const nextTab = opts.tab != null ? (page === "project" ? resolveTabIndex(opts.tab) : state.tab) : (page === "project" && page !== prevPage ? 0 : state.tab);

    if (page === "settings" && opts.tab) state.settingsTab = opts.tab;
    if (page === "search") {
      if (opts.query != null) state.searchQuery = opts.query;
      if (opts.scope != null) state.searchScope = opts.scope;
      if (opts.projectOnly != null) state.searchProjectOnly = !!opts.projectOnly;
      if (opts.selectedId != null) state.searchSelectedId = opts.selectedId;
    }
    if (page === "project" && opts.memoryId) {
      state.selected[nextProject] = opts.memoryId;
    }

    const sameTarget = !opts.force && page === prevPage && nextProject === state.project && nextTab === state.tab && !(page === "settings" && opts.tab) && page !== "search";
    if (sameTarget) return;

    const prevExt = registeredPages.get(activePageId);
    if (prevExt && prevExt.onLeave && prevPage !== page) {
      try { prevExt.onLeave(state, shellCtx); } catch (_) {}
    }

    state.page = page;
    state.project = nextProject;
    state.tab = nextTab;
    if (page !== prevPage) state.day = "";
    activePageId = page;

    const nextExt = registeredPages.get(page);
    if (nextExt && nextExt.onEnter && prevPage !== page) {
      try { nextExt.onEnter(state, shellCtx); } catch (_) {}
    }

    render(page !== prevPage || !!opts.force);
  }

  function render(enter) {
    UI.closeMenus();
    closeServicePopover();
    renderNav();
    const main = $("#main");
    const ext = registeredPages.get(state.page);

    if (ext && typeof ext.render === "function") {
      const out = ext.render(main, state, shellCtx);
      if (typeof out === "string") main.innerHTML = out;
      if (enter) {
        const pg = $(".page", main);
        if (pg) pg.classList.add("page-enter");
      }
      bindSearchTrigger(main);
      bindScrollBorder(main);
      if (typeof ext.bind === "function") ext.bind(main, state, shellCtx);
      syncProto();
      return;
    }

    if (state.page === "project") {
      main.innerHTML = projectHTML();
    } else if (state.page === "profile") {
      main.innerHTML = profileHTML();
    } else if (state.page === "overview") {
      main.innerHTML = overviewHTML();
    } else {
      main.innerHTML = `<div class="titlebar" id="titlebar"><h1>${esc(state.page)}</h1><span class="spacer"></span>${searchBoxHTML()}</div><div class="page">${emptyHTML("该页面模块尚未加载")}</div>`;
    }

    if (enter) {
      const pg = $(".page", main);
      if (pg) pg.classList.add("page-enter");
    }
    bind();
    syncProto();
  }

  function bindSearchTrigger(scope = document) {
    const gs = $("#gsearch", scope);
    if (gs) gs.onclick = () => MemlanePages.openCommandPalette({ projectOnly: state.page === "project" });
  }

  function bindScrollBorder(scope = document) {
    const tb = $("#titlebar", scope);
    if (!tb) return;
    for (const el of $$(".list, .scroll, .items, .profile-sources", scope)) {
      el.addEventListener("scroll", () => { tb.classList.toggle("scrolled", el.scrollTop > 2); });
    }
  }

  /* ---------- 总览 ---------- */
  const level = n => n <= 0 ? 0 : n < 3 ? 1 : n < 6 ? 2 : n < 10 ? 3 : 4;
  const dayLabel = k => { const [, m, d] = k.split("-"); return `${+m}月${+d}日`; };
  function overviewHTML() {
    const today = new Date(); today.setHours(0, 0, 0, 0);
    const tk = D.key(today);
    const start = new Date(today); start.setDate(start.getDate() - 364);
    const g = new Date(start); g.setDate(g.getDate() - g.getDay());
    let cells = "", w = 0;
    for (const d = new Date(g); d <= today; w++) {
      let label = "";
      for (let i = 0; i < 7; i++) { const x = new Date(d); x.setDate(x.getDate() + i); if (x.getDate() === 1 && x <= today && x >= start) label = `${x.getMonth() + 1}月`; }
      if (w === 0) { const nx = new Date(start); nx.setDate(nx.getDate() + 21); label = nx.getMonth() === start.getMonth() ? `${start.getMonth() + 1}月` : ""; }
      cells += `<span class="mo">${label}</span>`;
      for (let i = 0; i < 7; i++) {
        const k = D.key(d), vis = d >= start && d <= today;
        const n = (D.heat[k] || 0) + (k === tk ? state.todayExtra : 0);
        cells += vis ? `<button class="cell l${level(n)} ${state.day === k ? "sel" : ""}" style="--w:${w}" data-day="${k}" title="${dayLabel(k)} · 新增 ${n} 条记忆"></button>` : `<span class="cell blank" style="--w:${w}"></span>`;
        d.setDate(d.getDate() + 1);
      }
    }
    const rows = recentRows();
    return `
      <div class="titlebar" id="titlebar"><h1>总览</h1><span class="spacer"></span>${searchBoxHTML()}</div>
      <div class="page"><div class="ov">
        <section class="dist">
          <div class="section-head">
            <h2>记忆分布</h2><span class="meta">${start.getFullYear()}.${String(start.getMonth() + 1).padStart(2, "0")} — ${today.getFullYear()}.${String(today.getMonth() + 1).padStart(2, "0")}</span>
            <div class="stats">
              <div class="stat"><b>${D.stats.days}</b><span>天 已使用</span></div>
              <div class="stat"><b id="statMem">${D.stats.memories + state.todayExtra}</b><span>条 记忆</span></div>
              <div class="stat"><b>${D.stats.raw}</b><span>条 原始记录</span></div>
            </div>
          </div>
          <div class="heat-wrap">
            <div class="heat ${state.growHeat ? "grow" : ""}"><span class="wd" style="top:31px">一</span><span class="wd" style="top:59px">三</span><span class="wd" style="top:87px">五</span>${cells}</div>
            <div class="heat-side"><span class="foot">今天新增 <b id="todayN">${(D.heat[tk] || 0) + state.todayExtra}</b> 条</span><div class="legend">少 <span class="cell l1"></span><span class="cell l2"></span><span class="cell l3"></span><span class="cell l4"></span> 多</div></div>
          </div>
        </section>
        <div class="pair">
          <section class="col recent">
            <div class="section-head"><h2>${state.day ? `${dayLabel(state.day)} · ${rows.length} 条记忆` : "最近记忆"}</h2><span class="spacer"></span>${state.day ? `<button class="link" id="clearDay">返回最近记忆</button>` : ""}</div>
            <div class="list stagger" id="recentList">${rows.length ? rows.map(rowHTML).join("") : emptyHTML("这一天没有记忆")}</div>
          </section>
          <section class="col act">
            <div class="section-head"><h2>近期动态</h2><span class="spacer"></span>${segHTML("actSeg", ["动态", "最近搜索"], state.actTab)}</div>
            <div class="list stagger" id="actList">${actHTML()}</div>
          </section>
        </div>
      </div></div>`;
  }
  function recentRows() {
    if (!state.day) return state.memories.slice(0, 12);
    const t = new Date(); t.setHours(0, 0, 0, 0); const y = new Date(t); y.setDate(y.getDate() - 1);
    if (state.day === D.key(t)) return state.memories.filter(m => m.date === "今天" || m.date === "刚刚");
    if (state.day === D.key(y)) return state.memories.filter(m => m.date === "昨天");
    return [];
  }
  const rowHTML = (m, i) => `<button class="row" style="--i:${i}" data-open="${m.id}">${logo(m.agent, 22)}
      <span class="body"><span class="t">${fmt(m.body)}</span>
      <span class="m">${m.unread ? '<span class="unread" title="未读"></span>' : ""}<span>${esc(m.project)}</span>${recallTag(m.recall)}<span class="chip">${esc(m.cat)}</span><span>${esc(m.date)}</span></span></span>
      <span class="chev">${icon("chev")}</span></button>`;
  function actHTML() {
    const list = state.actTab ? D.searches : state.activity;
    return list.map((e, i) => `<div class="ev ${e.hits ? "click" : ""}" style="--i:${i}" ${e.hits ? `data-recall="${i}" title="查看本次召回的记忆"` : ""}>
      <span class="rail"><span class="dot ${state.actTab ? "" : e.state}"></span></span>
      <span style="flex:1;min-width:0"><span class="t">${esc(e.title)}${!state.actTab && e.state === "run" ? stageHTML(false) : ""}</span>
        <span class="m">${e.meta.split(" · ").map((s, j) => `<span class="${!state.actTab && e.state === "fail" && j === 1 ? "fail" : ""}">${esc(s)}</span>`).join("<span>·</span>")}</span>
        ${e.more ? `<button class="more">更多 ›</button>` : ""}</span>
      <span class="time">${esc(e.time)}</span></div>`).join("");
  }
  const segHTML = (id, labels, sel) => `<div class="seg" id="${id}"><span class="thumb"></span>${labels.map((l, i) => `<button data-i="${i}" class="${i === sel ? "on" : ""}">${esc(l)}</button>`).join("")}</div>`;

  /* ---------- 项目页 ---------- */
  function filtered() {
    let list = state.memories.filter(m => m.project === state.project);
    if (state.recall) list = list.filter(m => state.recall.includes(m.id));
    if (state.cat) list = list.filter(m => m.cat === state.cat);
    if (state.unreadOnly) list = list.filter(m => m.unread);
    if (state.sort === "recall") list = list.slice().sort((a, b) => b.recall - a.recall);
    return list;
  }
  function toolbarHTML(compact) {
    const sortLabel = state.sort === "time" ? "按时间" : "按召回次数";
    return `<div class="toolbar">
      <div class="dd"><button class="dd-btn ${state.cat ? "active" : ""}" id="catBtn"><span class="k">分类</span>${esc(state.cat || "全部")}${icon("down")}</button></div>
      <button class="switch ${state.unreadOnly ? "on" : ""}" id="unreadSw" role="switch" aria-checked="${state.unreadOnly}"><span class="track"><span class="knob"></span></span>仅未读</button>
      <span class="spacer"></span>
      ${compact ? "" : `<span class="meta">${filtered().length} 条</span>`}
      <div class="dd"><button class="dd-btn" id="sortBtn">${sortLabel}${icon("down")}</button></div>
    </div>${state.recall ? `<div class="recall-bar">${icon("recall").replace("<svg", '<svg style="width:14px;height:14px;color:var(--accent)"')}<span>本次召回 · <b>${state.recall.length}</b> 条</span><span class="spacer"></span><button class="link" id="clearRecall">显示全部记忆</button><button class="link" id="backAct">返回动态</button></div>` : ""}`;
  }
  const tabsHTML = () => `<div class="tabs" id="tabs"><span class="bar"></span>${PROJECT_TABS.map((t, i) => `<button data-tab="${i}" data-tab-id="${t.id}" class="${state.tab === i ? "on" : ""}">${t.label}</button>`).join("")}</div>`;
  function projectHTML() {
    const p = D.projects.find(x => x.id === state.project) || D.projects[0];
    // 视觉规范 V4 §4.1：项目顶栏不重复放「项目设置」，项目级配置统一收口到「管理」标签
    const title = `<div class="titlebar" id="titlebar"><span class="ico">${icon("folder")}</span><h1>${esc(p.name)}</h1><span class="meta">${esc(p.path)}</span><span class="spacer"></span>${searchBoxHTML()}</div>`;
    const curTab = PROJECT_TABS[state.tab] || PROJECT_TABS[0];
    const regTab = registeredProjectTabs.get(curTab.id);

    // 注册的全宽标签（如「管理」Q5）保持 tabs 与三栏同基准不跳动，下方为单栏可滚内容
    if (curTab.id === "manage" || (regTab && regTab.fullWidth)) {
      return `${title}<div class="page"><div class="proj-full">
        <div class="proj-full-head">${tabsHTML()}</div>
        <div class="proj-tab-body scroll" id="projectTabBody">${regTab ? "" : emptyHTML("「管理」标签模块尚未加载")}</div>
      </div></div>`;
    }

    const list = filtered();
    const selId = list.find(m => m.id === state.selected[p.id]) ? state.selected[p.id] : list[0] && list[0].id;
    state.selected[p.id] = selId;
    const sel = list.find(m => m.id === selId);
    return `${title}<div class="page"><div class="three">
      <section class="listcol">
        <div class="head">${tabsHTML()}</div>
        ${state.tab ? (regTab ? `<div id="projectTabBody" style="flex:1;min-height:0;display:flex;flex-direction:column;"></div>` : emptyHTML(`原型暂未展开「${curTab.label}」标签`)) : `
        <div class="tools">${toolbarHTML(true)}</div>
        <div class="items stagger" id="items"><span class="sel-ind" id="selInd"></span>${list.map((m, i) => itemHTML(m, i, m.id === selId)).join("")}${list.length ? "" : emptyHTML("没有符合筛选的记忆")}</div>
        <div class="listfoot">已载入 ${list.length} 项<span class="spacer"></span>1 / 1</div>`}
      </section>
      <section class="reader">
        <div class="doc scroll" id="doc">${sel && !state.tab ? `<div class="doc-enter">${detailHTML(sel)}</div>` : emptyHTML(state.tab ? `切换「记忆」或「管理」查看内容` : "选择一条记忆")}</div>
        <div class="readfoot">Cognee 提供 · 只读<span class="spacer"></span><button class="btn" data-copy><span class="ico">${icon("copy")}</span>复制正文</button></div>
      </section>
    </div></div>`;
  }
  const itemHTML = (m, i, on) => `<button class="item ${on ? "on" : ""}" style="--i:${i}" data-sel="${m.id}">
      <span class="s">${fmt(m.body)}</span>
      <span class="m">${m.unread ? '<span class="unread"></span>' : ""}${logo(m.agent, 16)}${recallTag(m.recall)}<span class="chip">${esc(m.cat)}</span><span class="r">${esc(m.date)}</span></span></button>`;
  function detailHTML(m) {
    const a = D.agents[m.agent];
    return `<article class="detail">
      <div class="dm">${logo(m.agent, 18)}<span>${esc(a.name)}</span><span>·</span><span>${esc(m.project)}</span><span>·</span><span>${esc(m.date)}</span>${recallTag(m.recall)}<span class="chip">${esc(m.cat)}</span></div>
      <div class="bodytext">${fmt(m.body)}</div>
      <section class="sec"><h3><span class="ico">${icon("info")}</span>记忆点</h3><p>${esc(m.rule)}</p><div class="miss">学习情境、接受理由未记录</div></section>
      <section class="sec src"><span class="ico" style="width:16px;height:16px;color:var(--text-3)">${icon("doc")}</span><div style="flex:1"><h3>出处 · 来源会话</h3><p style="margin-top:2px">${esc(m.source)}</p></div><button class="link">查看来源会话 ›</button></section>
    </article>`;
  }

  /* ---------- 用户画像页（Q4） ---------- */
  function parseProfileBody(body) {
    const sections = [];
    let current = { heading: "", items: [] };
    const lines = String(body || "").split("\n");
    for (const raw of lines) {
      const line = raw.trim();
      if (!line) continue;
      if (line.startsWith("## ")) {
        if (current.heading || current.items.length) sections.push(current);
        current = { heading: line.slice(3).trim(), items: [] };
        continue;
      }
      const m = line.match(/^(?:[-*]\s+)?\[(\d{4}-\d{2}-\d{2})\]\s*[-–—]\s*(.+)$/);
      if (m) {
        current.items.push({ date: m[1], text: m[2].trim() });
      } else {
        current.items.push({ date: "", text: line.replace(/^[-*]\s+/, "") });
      }
    }
    if (current.heading || current.items.length) sections.push(current);
    return sections;
  }

  function profileEntryCount(p) {
    if (!p || !p.body) return 0;
    return parseProfileBody(p.body).reduce((acc, s) => acc + s.items.length, 0);
  }

  function profileStatusMeta(p) {
    if (p.status === "generating") return { text: `正在导出 · ${p.progress ? p.progress.elapsed : 6} 秒`, fail: false, dot: "run" };
    if (p.status === "failed") return { text: "上次导入未完成", fail: true, dot: "fail" };
    if (p.status === "ready") {
      const n = profileEntryCount(p);
      const tag = p.method === "manual" ? `手动导入 · ${n} 条` : `${n} 条`;
      return { text: `${p.updated || "今天"} · ${tag}`, fail: false, dot: "ok" };
    }
    return { text: p.canGenerate ? "尚未导入" : "待手动导入", fail: false, dot: "" };
  }

  function profileHTML() {
    const sel = state.profiles.find(p => p.id === state.selectedProfile) || state.profiles[0];
    if (sel) state.selectedProfile = sel.id;
    let prevGroup = "";
    const srcHTML = state.profiles.map(p => {
      let grp = "";
      if (p.group !== prevGroup) {
        prevGroup = p.group;
        grp = `<div class="profile-group-label">${esc(p.group)}</div>`;
      }
      const meta = profileStatusMeta(p);
      const on = sel && sel.id === p.id;
      return `${grp}<button type="button" class="profile-src-row${on ? " on" : ""}" data-profile="${p.id}">
        ${logo(p.agent, 22)}
        <span class="profile-src-info">
          <span class="profile-src-top">
            <span class="profile-src-name">${esc(p.name)}</span>
            ${meta.dot ? `<span class="dot ${meta.dot}"></span>` : ""}
          </span>
          <span class="profile-src-meta${meta.fail ? " fail" : ""}">${esc(meta.text)}</span>
        </span>
      </button>`;
    }).join("");

    return `
      <div class="titlebar" id="titlebar"><span class="ico">${icon("profile")}</span><h1>用户画像</h1><span class="meta">按来源独立保存，不自动合并为通用记忆</span><span class="spacer"></span>${searchBoxHTML()}</div>
      <div class="page">
        <div class="profile-layout">
          <aside class="profile-col">
            <div class="profile-col-head">
              <span>来源</span>
              <button type="button" class="icon-btn" id="profileRefresh" title="刷新画像列表">${icon("refresh")}</button>
            </div>
            <div class="profile-sources" id="profileSources">
              <span class="profile-ind" id="profileInd"></span>
              ${srcHTML}
            </div>
          </aside>
          <section class="profile-reader">
            <div class="doc scroll" id="profileDoc">${sel ? profileReaderHTML(sel) : emptyHTML("选择一个 AI，查看它对你的了解")}</div>
            <div class="readfoot" id="profileFoot">${sel ? profileFootHTML(sel) : ""}</div>
          </section>
        </div>
      </div>`;
  }

  function profileReaderHTML(p) {
    const meta = profileStatusMeta(p);
    const sections = parseProfileBody(p.body);
    const methodLabel = p.status === "ready" ? (p.method === "manual" ? "手动导入" : "自动生成") : meta.text;
    let bodyBlock = "";
    if (p.status === "generating") {
      bodyBlock = `<div class="profile-progress">${stageHTML(false)}<span>正在向 ${esc(p.name)} 导出用户画像，完成后自动写入 Cognee…</span></div>
        <div style="margin-top:var(--s5)">${UI.skeletonGroupHTML({ count: 4, lines: 2, right: false })}</div>`;
    } else if (p.status === "failed") {
      bodyBlock = `<div class="profile-alert">导入失败：${esc(p.error || "Agent 未返回完整画像正文")}</div>
        ${emptyHTML("可点击下方「重新导入」重试，或使用「手动导入」粘贴正文")}`;
    } else if (!sections.length) {
      bodyBlock = emptyHTML(p.canGenerate ? `尚未从 ${p.name} 导入画像，可点击底部「重新导入」或「手动导入」` : `该来源支持手动导入：复制提示词后粘贴画像正文`);
    } else {
      bodyBlock = `<div class="profile-sections">${sections.map(sec => `
        <section class="profile-sec">
          ${sec.heading ? `<h3>${esc(sec.heading)}</h3>` : ""}
          <div>${sec.items.map(it => it.date
            ? `<div class="profile-entry"><span class="profile-entry-date">${esc(it.date)}</span><div class="profile-entry-text">${fmt(it.text)}</div></div>`
            : `<p class="profile-plain">${fmt(it.text)}</p>`
          ).join("")}</div>
        </section>`).join("")}</div>`;
    }

    const count = sections.reduce((acc, s) => acc + s.items.length, 0);
    return `<article class="detail profile-doc-enter">
      <div class="profile-meta-bar">
        ${logo(p.agent, 22)}
        <span class="profile-meta-name">${esc(p.name)}</span>
        <span>·</span>
        <span>${esc(p.group)}</span>
        <span>·</span>
        <span class="chip">${esc(methodLabel)}</span>
        ${p.updated ? `<span>更新于 ${esc(p.updated)}</span>` : ""}
        ${count ? `<span>·</span><span>${count} 条记录</span>` : ""}
      </div>
      ${bodyBlock}
    </article>`;
  }

  function profileFootHTML(p) {
    const working = p.status === "generating";
    const autoLabel = working ? "停止导入" : ((p.body || p.status === "failed") ? "重新导入" : "自动导入");
    return `
      <span class="profile-foot-hint">独立来源 · 仅作个人参考</span>
      ${p.canGenerate ? UI.buttonHTML({ id: "profileAutoBtn", label: autoLabel, variant: working ? "danger-outline" : "default", icon: icon("refresh") }) : ""}
      ${UI.buttonHTML({ id: "profileManualBtn", label: "手动导入", icon: icon("upload"), disabled: working })}
      ${UI.buttonHTML({ id: "profileCopyBtn", label: "复制正文", icon: icon("copy"), disabled: !p.body })}
    `;
  }

  function placeProfileInd() {
    const ind = $("#profileInd");
    const on = $(".profile-src-row.on");
    if (!ind) return;
    ind.style.opacity = on ? 1 : 0;
    if (on) {
      ind.style.setProperty("--y", on.offsetTop + "px");
      ind.style.setProperty("--h", on.offsetHeight + "px");
    }
  }

  function selectProfile(id) {
    const p = state.profiles.find(x => x.id === id);
    if (!p) return;
    state.selectedProfile = id;
    $$(".profile-src-row").forEach(el => el.classList.toggle("on", el.dataset.profile === id));
    placeProfileInd();
    const doc = $("#profileDoc");
    const foot = $("#profileFoot");
    if (doc) {
      doc.innerHTML = profileReaderHTML(p);
      doc.scrollTop = 0;
    }
    if (foot) {
      foot.innerHTML = profileFootHTML(p);
      bindProfileFooter(p);
    }
  }

  function openProfileImportDialog(p) {
    const draft = state.profileDrafts[p.id] ?? p.body ?? "";
    UI.openDialog({
      title: `手动导入 ${p.name} 用户画像`,
      size: 640,
      leading: logo(p.agent, 22),
      cancelText: "取消",
      confirmText: "保存画像",
      confirmDisabled: !draft.trim(),
      hint: "支持 ## 二级标题与「- [YYYY-MM-DD] - 内容」格式",
      body: `
        <div class="profile-import-bar">
          <span class="profile-import-tip">先将提示词发给 ${esc(p.name)}，再把返回的 Markdown 粘贴到下方：</span>
          ${UI.buttonHTML({ id: "copyProfilePrompt", label: "复制提示词", size: "sm", icon: icon("copy") })}
        </div>
        <textarea class="v4-textarea" id="profileImportArea" placeholder="粘贴 ${esc(p.name)} 返回的用户画像正文…">${esc(draft)}</textarea>
      `,
      onMount(panel, api) {
        const ta = $("#profileImportArea", panel);
        const copyBtn = $("#copyProfilePrompt", panel);
        if (copyBtn) {
          copyBtn.onclick = () => {
            if (navigator.clipboard && navigator.clipboard.writeText) navigator.clipboard.writeText(D.profilePrompt).catch(() => {});
            toast("已复制画像导出提示词");
          };
        }
        if (ta) {
          ta.oninput = () => {
            state.profileDrafts[p.id] = ta.value;
            api.setConfirmDisabled(!ta.value.trim());
          };
          setTimeout(() => ta.focus(), 20);
        }
      },
      onConfirm(api) {
        const ta = $("#profileImportArea", api.el);
        const text = ta ? ta.value.trim() : "";
        if (!text) return false;
        const parsed = parseProfileBody(text);
        const count = parsed.reduce((acc, s) => acc + s.items.length, 0);
        p.body = text;
        p.status = "ready";
        p.method = "manual";
        p.updated = "2026-11-20";
        p.count = count || 1;
        p.error = "";
        delete state.profileDrafts[p.id];
        render(false);
        toast(`已保存 ${p.name} 用户画像`);
      },
    });
  }

  let profileGenTimer = null;
  function bindProfileFooter(p) {
    const autoBtn = $("#profileAutoBtn");
    if (autoBtn) {
      autoBtn.onclick = () => {
        if (p.status === "generating") {
          clearTimeout(profileGenTimer);
          p.status = p.body ? "ready" : "empty";
          render(false);
          toast(`已停止从 ${p.name} 导入`);
          return;
        }
        p.status = "generating";
        p.progress = { elapsed: 1, idle: 0 };
        render(false);
        profileGenTimer = setTimeout(() => {
          p.status = "ready";
          p.method = "auto";
          p.updated = "2026-11-20";
          p.error = "";
          if (!p.body) {
            p.body = `## 工程习惯\n- [2026-11-20] - 从 ${p.name} 导出的最新偏好记录，已按日期列格式化展示。`;
            p.count = 1;
          }
          if (state.page === "profile") render(false);
          toast(`${p.name} 用户画像已更新`);
        }, reduce.checked ? 400 : 1400);
      };
    }
    const manualBtn = $("#profileManualBtn");
    if (manualBtn) manualBtn.onclick = () => openProfileImportDialog(p);
    const copyBtn = $("#profileCopyBtn");
    if (copyBtn) {
      copyBtn.onclick = () => {
        if (p.body && navigator.clipboard && navigator.clipboard.writeText) navigator.clipboard.writeText(p.body).catch(() => {});
        toast("已复制画像正文");
      };
    }
  }

  function markRead(m) {
    if (!m.unread) return;
    m.unread = false;
    $$(`[data-sel="${m.id}"] .unread, [data-open="${m.id}"] .unread`).forEach(x => x.remove());
    renderNav();
  }

  /* ---------- 绑定 ---------- */
  function rerenderProject() { render(false); restagger($("#items")); }
  function bind() {
    const main = $("#main");
    $$(".seg", main).forEach(seg => {
      placeThumb(seg);
      $$("button", seg).forEach(b => b.onclick = () => {
        $$("button", seg).forEach(x => x.classList.toggle("on", x === b));
        placeThumb(seg);
        state.actTab = +b.dataset.i;
        const l = $("#actList");
        if (l) { l.innerHTML = actHTML(); restagger(l); bindAct(); }
      });
    });

    const tabs = $("#tabs", main);
    if (tabs) {
      placeBar(tabs);
      $$("[data-tab]", tabs).forEach(b => b.onclick = () => {
        state.tab = +b.dataset.tab;
        render(false);
      });
    }

    // 挂载已注册的项目子标签（如「管理」）
    if (state.page === "project") {
      const curTab = PROJECT_TABS[state.tab];
      const regTab = curTab && registeredProjectTabs.get(curTab.id);
      const container = $("#projectTabBody", main);
      if (regTab && container) {
        const p = D.projects.find(x => x.id === state.project) || D.projects[0];
        const out = regTab.render(container, p, state, shellCtx);
        if (typeof out === "string") container.innerHTML = out;
        if (typeof regTab.bind === "function") regTab.bind(container, p, state, shellCtx);
      }
    }

    // 用户画像绑定
    if (state.page === "profile") {
      $$(".profile-src-row", main).forEach(row => row.onclick = () => selectProfile(row.dataset.profile));
      const refBtn = $("#profileRefresh", main);
      if (refBtn) refBtn.onclick = () => toast("已刷新画像来源列表");
      const sel = state.profiles.find(x => x.id === state.selectedProfile);
      if (sel) bindProfileFooter(sel);
      placeProfileInd();
    }

    $$("[data-day]", main).forEach(c => c.onclick = () => {
      const k = c.dataset.day; state.day = state.day === k ? "" : k; render(false);
      const cell = $(`[data-day="${k}"]`); if (cell) cell.classList.add("tap"); restagger($("#recentList"));
    });
    const cd = $("#clearDay", main); if (cd) cd.onclick = () => { state.day = ""; render(false); restagger($("#recentList")); };
    $$("[data-open]", main).forEach(r => r.onclick = () => openFromOverview(r.dataset.open));
    bindAct();

    const catBtn = $("#catBtn", main);
    if (catBtn) {
      const base = state.memories.filter(m => m.project === state.project && (!state.recall || state.recall.includes(m.id)) && (!state.unreadOnly || m.unread));
      catBtn.onclick = () => UI.openMenu(catBtn, [
        { v: "", label: "全部", n: base.length, on: !state.cat },
        "-",
        ...D.categories.map(c => ({ v: c, label: c, n: base.filter(m => m.cat === c).length, on: state.cat === c })),
      ], v => { state.cat = v; rerenderProject(); }, { minWidth: 204 });
      $("#sortBtn", main).onclick = () => UI.openMenu($("#sortBtn", main), [
        { v: "time", label: "按时间", on: state.sort === "time" },
        { v: "recall", label: "按召回次数", on: state.sort === "recall" },
      ], v => { state.sort = v; rerenderProject(); }, { align: "right", minWidth: 164 });
      $("#unreadSw", main).onclick = () => {
        state.unreadOnly = !state.unreadOnly;
        $("#unreadSw", main).classList.toggle("on", state.unreadOnly);
        setTimeout(rerenderProject, reduce.checked ? 0 : 160);
      };
    }

    const cr = $("#clearRecall", main); if (cr) cr.onclick = () => { state.recall = null; rerenderProject(); };
    const ba = $("#backAct", main); if (ba) ba.onclick = () => { state.recall = null; go("overview"); };
    $$("[data-sel]", main).forEach(it => it.onclick = () => select(it.dataset.sel));
    placeSel();
    $$(".readfoot [data-copy]", main).forEach(b => b.onclick = () => toast("已复制正文"));
    bindSearchTrigger(main);
    bindScrollBorder(main);

    const empty = $("#empty", main);
    if (empty && empty.parentElement) {
      empty.parentElement.onpointermove = e => {
        const r = empty.getBoundingClientRect(), b = $(".blocks", empty);
        if (!b || !r.width || !r.height) return;
        b.style.setProperty("--px", `${(-(e.clientX - r.left - r.width / 2) / r.width * 10).toFixed(1)}px`);
        b.style.setProperty("--py", `${(-(e.clientY - r.top - r.height / 2) / r.height * 8).toFixed(1)}px`);
      };
    }
  }

  function bindAct() {
    $$("[data-recall]").forEach(ev => ev.onclick = e => {
      if (e.target.closest(".more")) return;
      const a = state.activity[+ev.dataset.recall];
      state.recall = a.hits; state.cat = ""; state.unreadOnly = false;
      go("project", { project: a.project, tab: 0, force: true });
    });
    $$(".ev .more").forEach(b => b.onclick = () => {
      UI.openDialog({
        title: "任务失败详情",
        size: 480,
        cancelText: "关闭",
        confirmText: "复制诊断信息",
        body: `<p>最近 3 次整理任务均返回 <code>SubprocessTransportError</code>：子进程在输出完整 JSON 前关闭。</p><p style="font-family:var(--font-mono);font-size:var(--fs-caption);padding:var(--s2) var(--s3);border-radius:var(--radius);background:var(--fill-1);color:var(--text-2);white-space:pre-wrap">${esc(D.serviceStatus.diagnosticText)}</p>`,
        onConfirm() { toast("已复制诊断信息"); },
      });
    });
  }

  function openFromOverview(id) {
    const m = state.memories.find(x => x.id === id);
    if (!m) return;
    state.selected[m.project] = id;
    markRead(m);
    state.recall = null; state.cat = ""; state.unreadOnly = false;
    go("project", { project: m.project, tab: 0, force: true });
  }

  const restagger = el => { if (!el) return; el.classList.remove("stagger"); void el.offsetWidth; el.classList.add("stagger"); };
  function placeThumb(seg) {
    if (!seg) return;
    const on = $("button.on", seg), th = $(".thumb", seg);
    if (!on || !th) return;
    th.style.width = on.offsetWidth + "px";
    th.style.transform = `translateX(${on.offsetLeft}px)`;
  }
  function placeBar(tabs) {
    if (!tabs) return;
    const on = $("button.on", tabs), bar = $(".bar", tabs);
    if (!on || !bar) return;
    bar.style.width = on.offsetWidth + "px";
    bar.style.transform = `translateX(${on.offsetLeft}px)`;
  }
  function select(id) {
    const m = state.memories.find(x => x.id === id);
    if (!m) return;
    state.selected[m.project] = id;
    $$("[data-sel]").forEach(it => it.classList.toggle("on", it.dataset.sel === id));
    markRead(m); placeSel();
    const doc = $("#doc");
    if (doc) { doc.innerHTML = `<div class="doc-enter">${detailHTML(m)}</div>`; doc.scrollTop = 0; }
  }
  function placeSel() {
    const ind = $("#selInd"), on = $(".item.on");
    if (!ind) return;
    ind.style.opacity = on ? 1 : 0;
    if (on) {
      ind.style.setProperty("--y", on.offsetTop + "px");
      ind.style.setProperty("--h", on.offsetHeight + "px");
    }
  }

  /* ---------- 全局键盘快捷键：⌘K、Esc 关浮层、项目列表与画像来源栏 ↑↓ ---------- */
  document.addEventListener("keydown", e => {
    if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
      e.preventDefault();
      if (MemlanePages.isCommandPaletteOpen()) MemlanePages.closeCommandPalette();
      else MemlanePages.openCommandPalette({ projectOnly: state.page === "project" });
      return;
    }
    if (e.key === "Escape" && servicePopoverEl) {
      e.preventDefault();
      closeServicePopover();
      return;
    }
    // 若弹窗或 ⌘K 或菜单打开、或焦点在输入框中，不抢 ↑↓
    if (document.querySelector(".v4-dialog-scrim, .v4-menu") || MemlanePages.isCommandPaletteOpen()) return;
    const tag = (e.target && e.target.tagName || "").toLowerCase();
    if (tag === "input" || tag === "textarea") return;

    const ext = registeredPages.get(state.page);
    if (ext && ext.hooks && typeof ext.hooks.onKeydown === "function") {
      if (ext.hooks.onKeydown(e, state, shellCtx) === true) return;
    }

    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      const dir = e.key === "ArrowDown" ? 1 : -1;
      if (state.page === "profile" && state.profiles.length) {
        e.preventDefault();
        const idx = state.profiles.findIndex(p => p.id === state.selectedProfile);
        const next = state.profiles[(idx + dir + state.profiles.length) % state.profiles.length];
        selectProfile(next.id);
        const row = $(`.profile-src-row[data-profile="${next.id}"]`);
        if (row) row.scrollIntoView({ block: "nearest" });
      } else if (state.page === "project" && state.tab === 0) {
        const list = filtered();
        if (!list.length) return;
        e.preventDefault();
        const curId = state.selected[state.project];
        const idx = list.findIndex(m => m.id === curId);
        const next = list[(idx + dir + list.length) % list.length];
        select(next.id);
        const item = $(`[data-sel="${next.id}"]`);
        if (item) item.scrollIntoView({ block: "nearest" });
      }
    }
  });

  /* ---------- 模拟新记忆入库：动画在「服务回执」之后才开始 ---------- */
  const incoming = [
    ["memlane", "gemini", "约束条件", "`internal/simpledesktop/overview_style.go` 的色值迁移到设计令牌后，页面不得再直接写 `ui.Hex`；新增颜色先进令牌表，再在组件中引用。"],
    ["memlane", "codex", "踩坑经验", "`ui.Segmented` 在主题字号临时改小时会继承到下一个兄弟节点；改用组件级 `FontSize` 而不是 `c.SetTheme` 切换。"],
    ["memlane", "claude", "代码位置与关联", "弹簧参数集中在 `internal/simpledesktop/motion_tokens.go`，`Smooth`／`Snappy`／`Bouncy` 三档供选中块、弹窗与入库动画复用。"],
  ];
  let inc = 0, busy = false;
  $("#simulate").onclick = () => {
    if (busy) return; busy = true;
    const [project, agent, cat, body] = incoming[inc++ % incoming.length];
    const m = { id: "n" + Date.now(), project, agent, cat, body, recall: 0, date: "刚刚", unread: true, source: "来源会话 · 刚刚", rule: "新入库的记忆。" };
    state.activity.unshift({ state: "run", title: "正在整理", meta: `${project} · 整理中`, time: "现在" });
    if (state.page === "overview" && !state.actTab) { $("#actList").innerHTML = actHTML(); bindAct(); }
    setTimeout(() => {
      state.activity[0] = { state: "ok", title: "整理完成", meta: `${project} · 1 条记忆`, time: "现在" };
      state.memories.unshift(m); state.todayExtra++;
      renderNav();
      const badge = $(`[data-unread="${project}"]`); if (badge) badge.classList.add("bump");
      if (state.page === "overview") {
        if (!state.actTab) { $("#actList").innerHTML = actHTML(); bindAct(); }
        if (!state.day) {
          const list = $("#recentList"); list.classList.remove("stagger");
          const slot = document.createElement("div"); slot.className = "drop-slot"; slot.innerHTML = rowHTML(m, 0);
          list.prepend(slot); list.scrollTop = 0;
          requestAnimationFrame(() => requestAnimationFrame(() => slot.classList.add("open")));
          slot.querySelector("[data-open]").onclick = () => openFromOverview(m.id);
        }
        $("#statMem").textContent = D.stats.memories + state.todayExtra;
        const t = new Date(); t.setHours(0, 0, 0, 0); const k = D.key(t);
        $("#todayN").textContent = (D.heat[k] || 0) + state.todayExtra;
        const cell = $(`[data-day="${k}"]`); if (cell) { cell.className = `cell l${level((D.heat[k] || 0) + state.todayExtra)}`; void cell.offsetWidth; cell.classList.add("pulse"); }
      } else if (state.page === "project" && state.project === project && !state.tab && filtered().includes(m)) {
        const items = $("#items"); items.classList.remove("stagger");
        const slot = document.createElement("div"); slot.className = "drop-slot"; slot.innerHTML = itemHTML(m, 0, false);
        items.insertBefore(slot, $("#selInd").nextSibling); items.scrollTop = 0;
        requestAnimationFrame(() => requestAnimationFrame(() => slot.classList.add("open")));
        slot.querySelector("[data-sel]").onclick = () => select(m.id);
        [60, 200, 450].forEach(t => setTimeout(placeSel, t));
      } else toast(`新记忆已保存到 ${project}`);
      busy = false;
    }, reduce.checked ? 500 : 1800);
  };
  $("#replay").onclick = () => { state.growHeat = true; render(true); state.growHeat = false; };

  let tt;
  function toast(text) {
    let t = $(".toast"); if (!t) { t = document.createElement("div"); t.className = "toast"; }
    $("#window").appendChild(t); t.textContent = text; void t.offsetWidth; t.classList.add("show");
    clearTimeout(tt); tt = setTimeout(() => t.classList.remove("show"), 1600);
  }

  /* ---------- 暴露给扩展页面的上下文（window.MemlaneShell） ---------- */
  const shellCtx = {
    state,
    DATA: D,
    UI,
    go,
    openMemory: openFromOverview,
    render,
    toast,
    setTheme,
    getThemeChoice: () => themeChoice,
    getResolvedTheme,
    onThemeChange: fn => { themeListeners.add(fn); return () => themeListeners.delete(fn); },
    isReducedMotion: () => reduce.checked,
    setReducedMotion: v => { reduce.checked = !!v; applyReduce(); },
    springEasing,
    icon,
    logo,
    esc,
    fmt,
    plain,
    recallTag,
    searchBoxHTML,
    bindSearchTrigger,
    bindScrollBorder,
    placeBar,
    placeThumb,
    emptyHTML,
    stageHTML,
    openServicePopover,
    closeServicePopover,
    openProfileImportDialog: () => {
      const sel = state.profiles.find(x => x.id === state.selectedProfile) || state.profiles[0];
      if (sel) openProfileImportDialog(sel);
    },
    windowEl: () => $("#window"),
  };
  window.MemlaneShell = shellCtx;
  window.__proto = { state, render, go, shell: shellCtx };

  /* ---------- 延迟到 DOMContentLoaded 启动，确保后续 pages-*.js 已完成同步注册 ---------- */
  function boot() {
    if (booted) return;
    booted = true;
    const q = new URLSearchParams(location.search);
    if (q.get("theme")) themeChoice = q.get("theme");
    if (q.get("reduce") === "1" || q.get("reduce") === "true") {
      reduce.checked = true;
      applyReduce();
    }
    if (q.get("project")) state.project = q.get("project");
    if (q.get("page")) state.page = q.get("page");
    if (q.get("tab")) {
      if (state.page === "project") state.tab = resolveTabIndex(q.get("tab"));
      else if (state.page === "settings") state.settingsTab = q.get("tab");
    }
    if (q.get("q")) state.searchQuery = q.get("q");
    if (q.get("scope")) state.searchScope = q.get("scope");
    if (q.get("profile")) state.selectedProfile = q.get("profile");
    if (q.get("step")) state.setupStep = Number(q.get("step")) || 0;

    applyTheme();
    activePageId = state.page;
    state.growHeat = true;
    render(true);
    state.growHeat = false;

    if (q.get("popover") === "service") openServicePopover();
    if (q.get("cmdk") === "1") setTimeout(() => MemlanePages.openCommandPalette({ query: q.get("q") || "" }), 60);
    if (q.get("dialog") === "import" && state.page === "profile") {
      setTimeout(() => shellCtx.openProfileImportDialog(), 60);
    }

    if (document.fonts && document.fonts.ready) {
      document.fonts.ready.then(() => {
        $$(".seg").forEach(placeThumb);
        $$(".v4-seg").forEach(UI.layoutSegmented);
        const t = $("#tabs"); if (t) placeBar(t);
        renderNav(); placeSel(); placeProfileInd();
      });
    }
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", boot, { once: true });
  } else {
    queueMicrotask(boot);
  }
})();
