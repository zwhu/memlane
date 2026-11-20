(() => {
  const D = window.MEMLANE_DATA;
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
  };
  const icon = (name, filled = false) => { const [p, can] = ICONS[name]; return `<svg viewBox="0 0 20 20" width="100%" height="100%" fill="${filled && can ? "currentColor" : "none"}" stroke="currentColor" stroke-width="1.5">${p}</svg>`; };

  /* ---------- 状态 ---------- */
  const state = {
    page: "overview", project: "memlane", tab: 0,
    cat: "", unreadOnly: false, sort: "time", recall: null,
    selected: {}, day: "", actTab: 0, growHeat: false, todayExtra: 0,
    memories: D.memories.map(m => ({ ...m })), activity: D.activity.slice(),
  };
  const unreadCount = p => state.memories.filter(m => m.project === p && m.unread).length;

  /* ---------- 主题 / 减少动态 ---------- */
  const mq = matchMedia("(prefers-color-scheme: dark)");
  let themeChoice = "light";
  const applyTheme = () => { root.dataset.theme = themeChoice === "system" ? (mq.matches ? "dark" : "light") : themeChoice; syncProto(); };
  mq.addEventListener("change", applyTheme);
  $$("[data-theme-choice]").forEach(b => b.onclick = () => { themeChoice = b.dataset.themeChoice; applyTheme(); });
  const reduce = $("#reduce");
  reduce.checked = matchMedia("(prefers-reduced-motion: reduce)").matches;
  const applyReduce = () => root.classList.toggle("reduce", reduce.checked);
  reduce.onchange = applyReduce; applyReduce();
  function syncProto() {
    $$("[data-theme-choice]").forEach(b => b.classList.toggle("on", b.dataset.themeChoice === themeChoice));
    $$("[data-page-choice]").forEach(b => b.classList.toggle("on", b.dataset.pageChoice === state.page));
  }
  $$("[data-page-choice]").forEach(b => b.onclick = () => go(b.dataset.pageChoice));

  /* ---------- 文本 ---------- */
  const esc = s => s.replace(/&/g, "&amp;").replace(/</g, "&lt;");
  const fmt = s => esc(s).replace(/`([^`]+)`/g, (_, c) => `<code>${c.replace(/([/_.])/g, "$1<wbr>")}</code>`);
  const plain = s => s.replace(/`/g, "");
  function logo(agent, size = 20) {
    const a = D.agents[agent];
    return `<span class="logo" title="${a.name}" style="width:${size}px;height:${size}px;font-size:${Math.round(size * .55)}px;border-radius:${Math.round(size * .28)}px;background:${a.color};color:${a.fg}">${a.mark}</span>`;
  }
  const recallTag = n => `<span class="recall ${n ? "" : "zero"}" title="被 Agent 召回 ${n} 次">${n ? icon("recall") : ""}召回 ${n} 次</span>`;

  /* ---------- 侧栏 ---------- */
  function renderNav() {
    const items = [["overview", "总览"], ["common", "通用记忆"], ["profile", "用户画像"]];
    $("#navItems").innerHTML = items.map(([id, label]) => { const on = state.page === id; return `<button class="nav-item ${on ? "on" : ""}" data-nav="${id}"><span class="ico">${icon(id, on)}</span><span class="label">${label}</span></button>`; }).join("");
    $("#navProjects").innerHTML = D.projects.map(p => {
      const on = state.page === "project" && state.project === p.id, n = unreadCount(p.id);
      return `<button class="nav-item ${on ? "on" : ""}" data-nav="project" data-project="${p.id}" title="${p.path}"><span class="ico">${icon("folder", on)}</span><span class="label">${p.name}</span>${n ? `<span class="badge" data-unread="${p.id}" title="${n} 条未读记忆，点击查看">${n}</span>` : ""}</button>`;
    }).join("");
    $("#settingsIco").innerHTML = icon("settings");
    $$(".nav-item").forEach(b => b.onclick = e => {
      const nav = b.dataset.nav;
      if (e.target.dataset.unread) { state.unreadOnly = true; state.cat = ""; state.recall = null; return go("project", e.target.dataset.unread, true); }
      if (nav === "overview") go("overview");
      else if (nav === "project") { state.unreadOnly = false; state.recall = null; state.cat = ""; go("project", b.dataset.project); }
      else toast("原型只包含总览与项目页");
    });
    $("#allProjects").onclick = $("#addProject").onclick = () => toast("原型只包含总览与项目页");
    const ind = $("#navIndicator"), on = $(".nav .nav-item.on");
    ind.style.setProperty("--o", on ? 1 : 0);
    if (on) ind.style.setProperty("--y", on.offsetTop + "px");
  }

  function go(page, project, force) {
    if (!force && page === state.page && (!project || project === state.project)) return;
    state.page = page; if (project) state.project = project;
    state.day = ""; state.tab = 0;
    render(true);
  }

  function render(enter) {
    closeMenus();
    renderNav();
    const main = $("#main");
    main.innerHTML = state.page === "project" ? projectHTML() : overviewHTML();
    if (enter) $(".page", main).classList.add("page-enter");
    bind(); syncProto();
  }
  const searchBox = () => `<div class="search" id="gsearch"><span class="ico">${icon("search")}</span>搜索记忆与原始记录<span class="kbd">⌘K</span></div>`;

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
      <div class="titlebar" id="titlebar"><h1>总览</h1><span class="spacer"></span>${searchBox()}</div>
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
      <span class="m">${m.unread ? '<span class="unread" title="未读"></span>' : ""}<span>${m.project}</span>${recallTag(m.recall)}<span class="chip">${m.cat}</span><span>${m.date}</span></span></span>
      <span class="chev">${icon("chev")}</span></button>`;
  const emptyHTML = t => `<div class="empty" id="empty"><div class="blocks"><i></i><i></i><i></i></div><div>${t}</div></div>`;
  function actHTML() {
    const list = state.actTab ? D.searches : state.activity;
    return list.map((e, i) => `<div class="ev ${e.hits ? "click" : ""}" style="--i:${i}" ${e.hits ? `data-recall="${i}" title="查看本次召回的记忆"` : ""}>
      <span class="rail"><span class="dot ${e.state}"></span></span>
      <span style="flex:1;min-width:0"><span class="t">${e.title}${e.state === "run" ? `<span class="stage">${[0, 1, 2, 3, 4, 5].map(k => `<i style="--k:${k}"></i>`).join("")}</span>` : ""}</span>
        <span class="m">${e.meta.split(" · ").map((s, j) => `<span class="${e.state === "fail" && j === 1 ? "fail" : ""}">${s}</span>`).join("<span>·</span>")}</span>
        ${e.more ? `<button class="more">更多 ›</button>` : ""}</span>
      <span class="time">${e.time}</span></div>`).join("");
  }
  const segHTML = (id, labels, sel) => `<div class="seg" id="${id}"><span class="thumb"></span>${labels.map((l, i) => `<button data-i="${i}" class="${i === sel ? "on" : ""}">${l}</button>`).join("")}</div>`;

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
      <div class="dd"><button class="dd-btn ${state.cat ? "active" : ""}" id="catBtn"><span class="k">分类</span>${state.cat || "全部"}${icon("down")}</button></div>
      <button class="switch ${state.unreadOnly ? "on" : ""}" id="unreadSw" role="switch" aria-checked="${state.unreadOnly}"><span class="track"><span class="knob"></span></span>仅未读</button>
      <span class="spacer"></span>
      ${compact ? "" : `<span class="meta">${filtered().length} 条</span>`}
      <div class="dd"><button class="dd-btn" id="sortBtn">${sortLabel}${icon("down")}</button></div>
    </div>${state.recall ? `<div class="recall-bar">${icon("recall").replace("<svg", '<svg style="width:14px;height:14px;color:var(--accent)"')}<span>本次召回 · <b>${state.recall.length}</b> 条</span><span class="spacer"></span><button class="link" id="clearRecall">显示全部记忆</button><button class="link" id="backAct">返回动态</button></div>` : ""}`;
  }
  const tabsHTML = () => `<div class="tabs" id="tabs"><span class="bar"></span>${["记忆", "原始记录", "图谱"].map((t, i) => `<button data-tab="${i}" class="${state.tab === i ? "on" : ""}">${t}</button>`).join("")}</div>`;
  function projectHTML() {
    const p = D.projects.find(x => x.id === state.project);
    const title = `<div class="titlebar" id="titlebar"><span class="ico">${icon("folder")}</span><h1>${p.name}</h1><span class="meta">${p.path}</span><span class="spacer"></span><button class="ghost" id="projSettings"><span class="ico">${icon("settings")}</span>项目设置</button>${searchBox()}</div>`;
    const list = filtered();
    const selId = list.find(m => m.id === state.selected[p.id]) ? state.selected[p.id] : list[0] && list[0].id;
    state.selected[p.id] = selId;
    const sel = list.find(m => m.id === selId);
    return `${title}<div class="page"><div class="three">
      <section class="listcol">
        <div class="head">${tabsHTML()}</div>
        ${state.tab ? emptyHTML("原型只演示「记忆」标签") : `
        <div class="tools">${toolbarHTML(true)}</div>
        <div class="items stagger" id="items"><span class="sel-ind" id="selInd"></span>${list.map((m, i) => itemHTML(m, i, m.id === selId)).join("")}${list.length ? "" : emptyHTML("没有符合筛选的记忆")}</div>
        <div class="listfoot">已载入 ${list.length} 项<span class="spacer"></span>1 / 1</div>`}
      </section>
      <section class="reader">
        <div class="doc scroll" id="doc">${sel && !state.tab ? `<div class="doc-enter">${detailHTML(sel)}</div>` : emptyHTML("选择一条记忆")}</div>
        <div class="readfoot">Cognee 提供 · 只读<span class="spacer"></span><button class="btn" data-copy><span class="ico">${icon("copy")}</span>复制正文</button></div>
      </section>
    </div></div>`;
  }
  const itemHTML = (m, i, on) => `<button class="item ${on ? "on" : ""}" style="--i:${i}" data-sel="${m.id}">
      <span class="s">${fmt(m.body)}</span>
      <span class="m">${m.unread ? '<span class="unread"></span>' : ""}${logo(m.agent, 16)}${recallTag(m.recall)}<span class="chip">${m.cat}</span><span class="r">${m.date}</span></span></button>`;
  function detailHTML(m) {
    const a = D.agents[m.agent];
    return `<article class="detail">
      <div class="dm">${logo(m.agent, 18)}<span>${a.name}</span><span>·</span><span>${m.project}</span><span>·</span><span>${m.date}</span>${recallTag(m.recall)}<span class="chip">${m.cat}</span></div>
      <div class="bodytext">${fmt(m.body)}</div>
      <section class="sec"><h3><span class="ico">${icon("info")}</span>记忆点</h3><p>${m.rule}</p><div class="miss">学习情境、接受理由未记录</div></section>
      <section class="sec src"><span class="ico" style="width:16px;height:16px;color:var(--text-3)">${icon("doc")}</span><div style="flex:1"><h3>出处 · 来源会话</h3><p style="margin-top:2px">${m.source}</p></div><button class="link">查看来源会话 ›</button></section>
    </article>`;
  }

  function markRead(m) {
    if (!m.unread) return;
    m.unread = false;
    $$(`[data-sel="${m.id}"] .unread, [data-open="${m.id}"] .unread`).forEach(x => x.remove());
    renderNav();
  }

  /* ---------- 菜单 ---------- */
  function closeMenus() { $$(".menu").forEach(m => m.remove()); }
  function menu(anchor, items, onPick, right) {
    const was = anchor.parentElement.querySelector(".menu");
    closeMenus(); if (was) return;
    const el = document.createElement("div");
    el.className = "menu" + (right ? " right" : "");
    el.innerHTML = items.map(it => it === "-" ? "<hr>" : `<button data-v="${it.v}"><span class="chk">${it.on ? icon("check") : ""}</span>${it.label}${it.n != null ? `<span class="n">${it.n}</span>` : ""}</button>`).join("");
    anchor.parentElement.appendChild(el);
    el.onclick = e => { const b = e.target.closest("button"); if (!b) return; closeMenus(); onPick(b.dataset.v); };
  }
  document.addEventListener("pointerdown", e => { if (!e.target.closest(".dd")) closeMenus(); });

  /* ---------- 绑定 ---------- */
  function rerenderProject() { render(false); restagger($("#items")); }
  function bind() {
    $$(".seg").forEach(seg => { placeThumb(seg); $$("button", seg).forEach(b => b.onclick = () => {
      $$("button", seg).forEach(x => x.classList.toggle("on", x === b)); placeThumb(seg);
      state.actTab = +b.dataset.i; const l = $("#actList"); l.innerHTML = actHTML(); restagger(l); bindAct();
    }); });
    const tabs = $("#tabs");
    if (tabs) { placeBar(tabs); $$("[data-tab]", tabs).forEach(b => b.onclick = () => { state.tab = +b.dataset.tab; render(false); }); }
    $$("[data-day]").forEach(c => c.onclick = () => {
      const k = c.dataset.day; state.day = state.day === k ? "" : k; render(false);
      const cell = $(`[data-day="${k}"]`); cell.classList.add("tap"); restagger($("#recentList"));
    });
    const cd = $("#clearDay"); if (cd) cd.onclick = () => { state.day = ""; render(false); restagger($("#recentList")); };
    $$("[data-open]").forEach(r => r.onclick = () => openFromOverview(r.dataset.open));
    bindAct();
    const catBtn = $("#catBtn");
    if (catBtn) {
      const base = state.memories.filter(m => m.project === state.project && (!state.recall || state.recall.includes(m.id)) && (!state.unreadOnly || m.unread));
      catBtn.onclick = () => menu(catBtn, [{ v: "", label: "全部", n: base.length, on: !state.cat }, "-", ...D.categories.map(c => ({ v: c, label: c, n: base.filter(m => m.cat === c).length, on: state.cat === c }))], v => { state.cat = v; rerenderProject(); });
      $("#sortBtn").onclick = () => menu($("#sortBtn"), [{ v: "time", label: "按时间", on: state.sort === "time" }, { v: "recall", label: "按召回次数", on: state.sort === "recall" }], v => { state.sort = v; rerenderProject(); }, true);
      $("#unreadSw").onclick = () => { state.unreadOnly = !state.unreadOnly; $("#unreadSw").classList.toggle("on", state.unreadOnly); setTimeout(rerenderProject, reduce.checked ? 0 : 160); };
    }
    const cr = $("#clearRecall"); if (cr) cr.onclick = () => { state.recall = null; rerenderProject(); };
    const ba = $("#backAct"); if (ba) ba.onclick = () => { state.recall = null; go("overview"); };
    $$("[data-sel]").forEach(it => it.onclick = () => select(it.dataset.sel));
    placeSel();
    $$(".readfoot [data-copy]").forEach(b => b.onclick = () => toast("已复制正文"));
    const ps = $("#projSettings"); if (ps) ps.onclick = () => toast("原型未包含项目设置");
    const gs = $("#gsearch"); if (gs) gs.onclick = () => toast("⌘K 全局搜索：可限定当前项目");
    for (const el of $$(".list, .scroll, .items")) el.addEventListener("scroll", () => { const t = $("#titlebar"); if (t) t.classList.toggle("scrolled", el.scrollTop > 2); });
    const empty = $("#empty");
    if (empty) empty.parentElement.onpointermove = e => { const r = empty.getBoundingClientRect(), b = $(".blocks", empty); b.style.setProperty("--px", `${(-(e.clientX - r.left - r.width / 2) / r.width * 10).toFixed(1)}px`); b.style.setProperty("--py", `${(-(e.clientY - r.top - r.height / 2) / r.height * 8).toFixed(1)}px`); };
  }
  function bindAct() {
    $$("[data-recall]").forEach(ev => ev.onclick = e => {
      if (e.target.closest(".more")) return;
      const a = state.activity[+ev.dataset.recall];
      state.recall = a.hits; state.cat = ""; state.unreadOnly = false; go("project", a.project, true);
    });
    $$(".ev .more").forEach(b => b.onclick = () => toast("失败详情：查看与复制诊断信息"));
  }
  function openFromOverview(id) {
    const m = state.memories.find(x => x.id === id);
    state.selected[m.project] = id; markRead(m); state.recall = null; state.cat = ""; state.unreadOnly = false; go("project", m.project, true);
  }
  const restagger = el => { if (!el) return; el.classList.remove("stagger"); void el.offsetWidth; el.classList.add("stagger"); };
  function placeThumb(seg) { const on = $("button.on", seg), th = $(".thumb", seg); th.style.width = on.offsetWidth + "px"; th.style.transform = `translateX(${on.offsetLeft}px)`; }
  function placeBar(tabs) { const on = $("button.on", tabs), bar = $(".bar", tabs); bar.style.width = on.offsetWidth + "px"; bar.style.transform = `translateX(${on.offsetLeft}px)`; }
  function select(id) {
    const m = state.memories.find(x => x.id === id);
    state.selected[m.project] = id;
    $$("[data-sel]").forEach(it => it.classList.toggle("on", it.dataset.sel === id));
    markRead(m); placeSel();
    $("#doc").innerHTML = `<div class="doc-enter">${detailHTML(m)}</div>`; $("#doc").scrollTop = 0;
  }
  function placeSel() { const ind = $("#selInd"), on = $(".item.on"); if (!ind) return; ind.style.opacity = on ? 1 : 0; if (on) { ind.style.setProperty("--y", on.offsetTop + "px"); ind.style.setProperty("--h", on.offsetHeight + "px"); } }

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

  const q = new URLSearchParams(location.search);
  if (q.get("theme")) themeChoice = q.get("theme");
  if (q.get("page")) state.page = q.get("page");
  applyTheme();
  state.growHeat = true; render(true); state.growHeat = false;
  document.fonts && document.fonts.ready.then(() => { $$(".seg").forEach(placeThumb); const t = $("#tabs"); if (t) placeBar(t); renderNav(); placeSel(); });
  window.__proto = { state, render };
})();
