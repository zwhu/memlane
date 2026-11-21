// ⌘K Raycast 式搜索面板与「检索结果」三栏页（Q3）
(() => {
  const Pages = window.MemlanePages;
  const UI = window.MemlaneUI;
  const D = window.DATA || window.MEMLANE_DATA;
  if (!Pages || !UI || !D) return;

  const $ = (s, el = document) => el.querySelector(s);
  const $$ = (s, el = document) => [...el.querySelectorAll(s)];

  /* ---------- 检索与关键词高亮 ---------- */
  function escapeRegExp(s) {
    return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  }

  function tokenize(q) {
    return String(q || "").trim().split(/\s+/).filter(Boolean);
  }

  // 先用 ctx.fmt 渲染行内代码与转义，再对文本节点（非 HTML 标签）做关键词 <mark> 高亮
  function fmtHighlight(ctx, rawText, query) {
    const html = ctx.fmt(rawText || "");
    const tokens = tokenize(query);
    if (!tokens.length) return html;
    const re = new RegExp(`(${tokens.map(escapeRegExp).join("|")})`, "gi");
    return html.replace(/(<[^>]+>)|([^<]+)/g, (m, tag, text) => {
      if (tag) return tag;
      return text.replace(re, '<mark class="v4-mark">$1</mark>');
    });
  }

  function scoreText(text, tokens) {
    const low = String(text || "").toLowerCase();
    let hits = 0;
    for (const tok of tokens) {
      if (low.includes(tok.toLowerCase())) hits++;
    }
    return hits;
  }

  function queryItems(state, { query = "", scope = "all", projectOnly = false } = {}) {
    const q = String(query || "").trim();
    const tokens = tokenize(q);
    const curProj = state.project;

    const mems = (state.memories || D.memories || [])
      .filter(m => !projectOnly || m.project === curProj)
      .map(m => ({
        uid: "mem:" + m.id,
        kind: "memory",
        id: m.id,
        project: m.project,
        agent: m.agent,
        cat: m.cat,
        recall: m.recall,
        date: m.date,
        unread: !!m.unread,
        body: m.body,
        rule: m.rule,
        source: m.source,
        raw: m,
      }));

    const raws = (D.rawSessions || [])
      .filter(r => !projectOnly || r.project === curProj)
      .map(r => ({
        uid: "raw:" + r.id,
        kind: "raw",
        id: r.id,
        project: r.project,
        agent: r.agent,
        rawKind: r.kind,
        title: r.title,
        summary: r.summary,
        turns: r.turns,
        date: r.date,
        messages: r.messages || [],
        raw: r,
      }));

    const includeMem = scope === "all" || scope === "memory";
    const includeRaw = scope === "all" || scope === "raw";

    if (!q) {
      const list = [];
      if (includeMem) list.push(...mems.slice(0, 4));
      if (includeRaw) list.push(...raws.slice(0, 3));
      return { items: list, keywordCount: 0, semanticCount: 0 };
    }

    const matched = [];
    const pool = [...(includeMem ? mems : []), ...(includeRaw ? raws : [])];
    for (const item of pool) {
      const hay = item.kind === "memory"
        ? `${item.body} ${item.cat} ${item.project} ${item.source}`
        : `${item.title} ${item.summary} ${item.project} ${(item.messages || []).map(x => x.text).join(" ")}`;
      const exactHits = scoreText(hay, tokens);
      if (exactHits > 0) {
        matched.push({ ...item, matchType: "关键词匹配", score: exactHits * 10 + (item.recall || 0) * 0.05 });
      }
    }

    // 若关键词命中较少，补 1-2 条「语义相关」合成结果以体现双路召回形态
    if (matched.length < 3) {
      for (const item of pool) {
        if (matched.some(x => x.uid === item.uid)) continue;
        if (q.length >= 2) {
          matched.push({ ...item, matchType: "语义相关", score: 1 });
          if (matched.length >= Math.min(4, pool.length)) break;
        }
      }
    }

    // 记忆排前、原始记录排后，组内按相关度排序
    matched.sort((a, b) => {
      if (a.kind !== b.kind) return a.kind === "memory" ? -1 : 1;
      return (b.score || 0) - (a.score || 0);
    });

    const keywordCount = matched.filter(x => x.matchType === "关键词匹配").length;
    const semanticCount = matched.length - keywordCount;
    return { items: matched, keywordCount, semanticCount };
  }

  /* ==========================================================================
   *  1. 全局 ⌘K Raycast 式面板（Q3）
   * ========================================================================== */
  let overlayEl = null;
  let cmdkState = {
    query: "",
    scope: "all",
    projectOnly: false,
    activeIdx: 0,
    loading: false,
    items: [],
    keywordCount: 0,
    semanticCount: 0,
  };
  let searchTimer = null;
  let keydownHandler = null;

  function isOpen() {
    return !!overlayEl;
  }

  function close() {
    clearTimeout(searchTimer);
    if (keydownHandler) {
      document.removeEventListener("keydown", keydownHandler, true);
      keydownHandler = null;
    }
    if (overlayEl) {
      overlayEl.remove();
      overlayEl = null;
    }
  }

  function placeCmdkIndicator() {
    if (!overlayEl) return;
    const ind = $("#cmdkSelInd", overlayEl);
    const on = $(".cmdk-row.on", overlayEl);
    if (!ind) return;
    if (!on) {
      ind.style.setProperty("--o", 0);
      return;
    }
    ind.style.setProperty("--o", 1);
    ind.style.setProperty("--y", on.offsetTop + "px");
    ind.style.setProperty("--h", on.offsetHeight + "px");
  }

  function setCmdkActive(idx, scrollIntoView = false) {
    if (!overlayEl || !cmdkState.items.length) return;
    const len = cmdkState.items.length;
    cmdkState.activeIdx = ((idx % len) + len) % len;
    const rows = $$(".cmdk-row", overlayEl);
    rows.forEach((r, i) => r.classList.toggle("on", i === cmdkState.activeIdx));
    placeCmdkIndicator();
    if (scrollIntoView && rows[cmdkState.activeIdx]) {
      rows[cmdkState.activeIdx].scrollIntoView({ block: "nearest" });
    }
  }

  function openSelectedItem(ctx, item) {
    if (!item) return;
    const q = cmdkState.query;
    const scope = cmdkState.scope;
    const projectOnly = cmdkState.projectOnly;
    close();
    if (item.kind === "memory") {
      ctx.openMemory(item.id);
    } else {
      ctx.go("search", { query: q, scope, projectOnly, selectedId: item.uid, force: true });
    }
  }

  function openResultsPage(ctx) {
    const selected = cmdkState.items[cmdkState.activeIdx];
    const opts = {
      query: cmdkState.query,
      scope: cmdkState.scope,
      projectOnly: cmdkState.projectOnly,
      selectedId: selected ? selected.uid : "",
      force: true,
    };
    ctx.state._searchClearedOnce = !String(cmdkState.query || "").trim();
    close();
    ctx.go("search", opts);
  }

  function renderCmdkList(ctx) {
    if (!overlayEl) return;
    const listEl = $("#cmdkList", overlayEl);
    const statEl = $("#cmdkMatchStat", overlayEl);
    if (!listEl) return;

    if (cmdkState.loading) {
      if (statEl) statEl.textContent = "搜索中…";
      listEl.innerHTML = UI.skeletonGroupHTML({ count: 4, lines: 2, avatar: true, right: false });
      return;
    }

    const q = cmdkState.query.trim();
    if (statEl) {
      statEl.textContent = q
        ? `关键词匹配 ${cmdkState.keywordCount} · 语义相关 ${cmdkState.semanticCount}`
        : `最近 ${cmdkState.items.length} 项`;
    }

    if (!cmdkState.items.length) {
      listEl.innerHTML = ctx.emptyHTML(q ? `未找到与“${q}”相关的结果` : "暂无最近记忆或原始记录");
      return;
    }

    if (cmdkState.activeIdx >= cmdkState.items.length) cmdkState.activeIdx = 0;

    let prevGroup = "";
    let html = '<span class="cmdk-sel-ind" id="cmdkSelInd"></span>';
    cmdkState.items.forEach((it, idx) => {
      const grpLabel = it.kind === "memory"
        ? (q ? "记忆" : "最近记忆")
        : (q ? "原始记录" : "最近原始记录");
      if (grpLabel !== prevGroup) {
        prevGroup = grpLabel;
        html += `<div class="cmdk-group-title">${ctx.esc(grpLabel)}</div>`;
      }
      const on = idx === cmdkState.activeIdx;
      if (it.kind === "memory") {
        html += `<button type="button" class="cmdk-row${on ? " on" : ""}" data-cmdk-idx="${idx}">
          ${ctx.logo(it.agent, 20)}
          <span class="cmdk-row-body">
            <span class="cmdk-row-text">${fmtHighlight(ctx, it.body, q)}</span>
            <span class="cmdk-row-meta">
              ${it.unread ? '<span class="unread" title="未读"></span>' : ""}
              <span>${ctx.esc(it.project)}</span>
              ${ctx.recallTag(it.recall)}
              <span class="chip">${ctx.esc(it.cat)}</span>
              ${it.matchType ? `<span class="cmdk-match-tag">${ctx.esc(it.matchType)}</span>` : ""}
              <span class="r">${ctx.esc(it.date)}</span>
            </span>
          </span>
        </button>`;
      } else {
        const typeLabel = it.rawKind === "doc" ? "文档" : "会话";
        html += `<button type="button" class="cmdk-row${on ? " on" : ""}" data-cmdk-idx="${idx}">
          ${ctx.logo(it.agent, 20)}
          <span class="cmdk-row-body">
            <span class="cmdk-row-text"><strong class="raw-title">${fmtHighlight(ctx, it.title, q)}</strong> <span class="cmdk-row-sub">${fmtHighlight(ctx, it.summary, q)}</span></span>
            <span class="cmdk-row-meta">
              <span>${ctx.esc(it.project)}</span>
              <span class="chip">${typeLabel}</span>
              <span>${it.turns} 轮问答</span>
              ${it.matchType ? `<span class="cmdk-match-tag">${ctx.esc(it.matchType)}</span>` : ""}
              <span class="r">${ctx.esc(it.date)}</span>
            </span>
          </span>
        </button>`;
      }
    });

    listEl.innerHTML = html;
    $$(".cmdk-row", listEl).forEach(r => {
      const idx = Number(r.dataset.cmdkIdx);
      r.onmouseenter = () => setCmdkActive(idx, false);
      r.onclick = () => openSelectedItem(ctx, cmdkState.items[idx]);
    });
    requestAnimationFrame(() => placeCmdkIndicator());
  }

  function runCmdkSearch(ctx, immediate = false) {
    clearTimeout(searchTimer);
    const delay = immediate || ctx.isReducedMotion() ? 0 : 220;
    const finish = () => {
      cmdkState.loading = false;
      const res = queryItems(ctx.state, cmdkState);
      cmdkState.items = res.items;
      cmdkState.keywordCount = res.keywordCount;
      cmdkState.semanticCount = res.semanticCount;
      cmdkState.activeIdx = 0;
      renderCmdkList(ctx);
    };
    if (delay === 0) {
      finish();
    } else {
      cmdkState.loading = true;
      renderCmdkList(ctx);
      searchTimer = setTimeout(finish, delay);
    }
  }

  function open(opts = {}) {
    const ctx = window.MemlaneShell;
    if (!ctx) return;
    close();

    const urlLoading = new URLSearchParams(location.search).get("loading") === "1";
    cmdkState = {
      query: opts.query != null ? String(opts.query) : "",
      scope: opts.scope || "all",
      projectOnly: !!opts.projectOnly,
      activeIdx: 0,
      loading: !!opts.loading || urlLoading,
      items: [],
      keywordCount: 0,
      semanticCount: 0,
    };

    const curProjObj = (D.projects || []).find(p => p.id === ctx.state.project);
    const curProjName = curProjObj ? curProjObj.name : ctx.state.project;

    const scrim = document.createElement("div");
    scrim.className = "cmdk-scrim";
    scrim.id = "cmdkScrim";
    scrim.innerHTML = `
      <div class="cmdk-panel" role="dialog" aria-modal="true" aria-label="全局搜索">
        <div class="cmdk-input-bar">
          <span class="cmdk-search-ico">${ctx.icon("search")}</span>
          <input type="text" class="cmdk-input" id="cmdkInput" placeholder="搜索记忆与原始记录…" value="${ctx.esc(cmdkState.query)}" autocomplete="off" />
          <button type="button" class="cmdk-clear-btn" id="cmdkClear" title="清空关键词" ${cmdkState.query ? "" : "hidden"}>${ctx.icon("close")}</button>
        </div>
        <div class="cmdk-filters">
          ${UI.segmentedHTML({
            id: "cmdkScopeSeg",
            size: "sm",
            options: [
              { value: "all", label: "全部" },
              { value: "memory", label: "记忆" },
              { value: "raw", label: "原始记录" },
            ],
            value: cmdkState.scope,
          })}
          <button type="button" class="cmdk-scope-chip${cmdkState.projectOnly ? " on" : ""}" id="cmdkProjChip" aria-pressed="${cmdkState.projectOnly ? "true" : "false"}">
            <span class="ico">${ctx.icon("folder", cmdkState.projectOnly)}</span>
            <span>仅当前项目 · ${ctx.esc(curProjName)}</span>
          </button>
          <span class="cmdk-match-stat" id="cmdkMatchStat"></span>
        </div>
        <div class="cmdk-list" id="cmdkList"></div>
        <div class="cmdk-foot">
          <span class="cmdk-hint"><kbd>↑↓</kbd> 选择</span>
          <span class="cmdk-hint"><kbd>↵</kbd> 打开</span>
          <span class="cmdk-hint"><kbd>⌘↵</kbd> 检索结果页</span>
          <span class="cmdk-hint"><kbd>Esc</kbd> 关闭</span>
          <span class="spacer"></span>
          ${UI.buttonHTML({ id: "cmdkOpenAll", label: "查看全部结果", size: "sm" })}
        </div>
      </div>
    `;

    ctx.windowEl().appendChild(scrim);
    overlayEl = scrim;

    const inputEl = $("#cmdkInput", scrim);
    const clearBtn = $("#cmdkClear", scrim);
    const projChip = $("#cmdkProjChip", scrim);
    const openAllBtn = $("#cmdkOpenAll", scrim);

    UI.bindSegmented($("#cmdkScopeSeg", scrim), val => {
      cmdkState.scope = val;
      runCmdkSearch(ctx, true);
    });

    projChip.onclick = () => {
      cmdkState.projectOnly = !cmdkState.projectOnly;
      projChip.classList.toggle("on", cmdkState.projectOnly);
      projChip.setAttribute("aria-pressed", cmdkState.projectOnly ? "true" : "false");
      projChip.querySelector(".ico").innerHTML = ctx.icon("folder", cmdkState.projectOnly);
      runCmdkSearch(ctx, true);
    };

    inputEl.oninput = () => {
      cmdkState.query = inputEl.value;
      clearBtn.hidden = !cmdkState.query;
      runCmdkSearch(ctx, false);
    };

    clearBtn.onclick = () => {
      cmdkState.query = "";
      inputEl.value = "";
      clearBtn.hidden = true;
      inputEl.focus();
      runCmdkSearch(ctx, true);
    };

    openAllBtn.onclick = () => openResultsPage(ctx);
    scrim.addEventListener("pointerdown", e => {
      if (e.target === scrim) close();
    });

    keydownHandler = e => {
      if (!overlayEl || e.isComposing || e.keyCode === 229) return;
      if (e.key === "Escape") {
        e.preventDefault();
        e.stopPropagation();
        close();
      } else if (e.key === "ArrowDown") {
        e.preventDefault();
        e.stopPropagation();
        setCmdkActive(cmdkState.activeIdx + 1, true);
      } else if (e.key === "ArrowUp") {
        e.preventDefault();
        e.stopPropagation();
        setCmdkActive(cmdkState.activeIdx - 1, true);
      } else if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
        e.preventDefault();
        e.stopPropagation();
        openResultsPage(ctx);
      } else if (e.key === "Enter") {
        e.preventDefault();
        e.stopPropagation();
        if (cmdkState.items.length) openSelectedItem(ctx, cmdkState.items[cmdkState.activeIdx]);
      }
    };
    document.addEventListener("keydown", keydownHandler, true);

    if (cmdkState.loading) renderCmdkList(ctx);
    else runCmdkSearch(ctx, true);

    setTimeout(() => { if (inputEl && document.body.contains(inputEl)) inputEl.focus(); }, 20);
  }

  Pages.registerCommandPalette({ open, close, isOpen });

  /* ==========================================================================
   *  2. 「检索结果」三栏页（Q3：360 列表 + 阅读栏 + 关键词高亮）
   * ========================================================================== */
  function renderSearchDetail(ctx, item, query) {
    if (!item) return ctx.emptyHTML("选择一条检索结果查看详情");
    const a = D.agents[item.agent] || { name: item.agent };

    if (item.kind === "memory") {
      return `<article class="detail">
        <div class="dm">
          ${ctx.logo(item.agent, 18)}
          <span>${ctx.esc(a.name)}</span>
          <span>·</span>
          <span>${ctx.esc(item.project)}</span>
          <span>·</span>
          <span>${ctx.esc(item.date)}</span>
          ${ctx.recallTag(item.recall)}
          <span class="chip">${ctx.esc(item.cat)}</span>
          ${item.matchType ? `<span class="cmdk-match-tag">${ctx.esc(item.matchType)}</span>` : ""}
        </div>
        <div class="bodytext">${fmtHighlight(ctx, item.body, query)}</div>
        <section class="sec">
          <h3><span class="ico">${ctx.icon("info")}</span>记忆点</h3>
          <p>${fmtHighlight(ctx, item.rule, query)}</p>
          <div class="miss">学习情境、接受理由未记录</div>
        </section>
        <section class="sec src">
          <span class="ico" style="width:16px;height:16px;color:var(--text-3)">${ctx.icon("doc")}</span>
          <div style="flex:1">
            <h3>出处 · 来源会话</h3>
            <p style="margin-top:2px">${ctx.esc(item.source)}</p>
          </div>
          ${UI.buttonHTML({ id: "srJumpProjectBtn", label: "在项目中打开", size: "sm" })}
        </section>
      </article>`;
    }

    const typeLabel = item.rawKind === "doc" ? "文档" : "会话";
    return `<article class="detail">
      <div class="dm">
        ${ctx.logo(item.agent, 18)}
        <span>${ctx.esc(a.name)}</span>
        <span>·</span>
        <span>${ctx.esc(item.project)}</span>
        <span>·</span>
        <span class="chip">${typeLabel}</span>
        <span>${item.turns} 轮问答</span>
        <span>·</span>
        <span>${ctx.esc(item.date)}</span>
        ${item.matchType ? `<span class="cmdk-match-tag">${ctx.esc(item.matchType)}</span>` : ""}
      </div>
      <h2 class="sr-session-title">${fmtHighlight(ctx, item.title, query)}</h2>
      <p class="sr-session-summary">${fmtHighlight(ctx, item.summary, query)}</p>
      <div class="sr-msg-list">
        ${(item.messages || []).map(msg => `
          <div class="sr-msg">
            <div class="sr-msg-role">${msg.role === "user" ? "用户" : ctx.esc(a.name)}</div>
            <div class="sr-msg-text">${fmtHighlight(ctx, msg.text, query)}</div>
            ${msg.tools && msg.tools.length ? `<div class="sr-tool-list">${msg.tools.map(t => `
              <div class="sr-tool-row">
                <span class="dot ${t.ok ? "ok" : "fail"}"></span>
                <span class="sr-tool-name">${ctx.esc(t.name)}</span>
                <span>${fmtHighlight(ctx, t.summary, query)}</span>
              </div>`).join("")}</div>` : ""}
          </div>
        `).join("")}
      </div>
    </article>`;
  }

  function renderSearchItem(ctx, item, i, on, query) {
    if (item.kind === "memory") {
      return `<button type="button" class="item${on ? " on" : ""}" style="--i:${i}" data-sr-uid="${ctx.esc(item.uid)}">
        <span class="s">${fmtHighlight(ctx, item.body, query)}</span>
        <span class="m">
          ${item.unread ? '<span class="unread"></span>' : ""}
          ${ctx.logo(item.agent, 16)}
          <span>${ctx.esc(item.project)}</span>
          ${ctx.recallTag(item.recall)}
          <span class="chip">${ctx.esc(item.cat)}</span>
          <span class="r">${ctx.esc(item.date)}</span>
        </span>
      </button>`;
    }
    const typeLabel = item.rawKind === "doc" ? "文档" : "会话";
    return `<button type="button" class="item${on ? " on" : ""}" style="--i:${i}" data-sr-uid="${ctx.esc(item.uid)}">
      <span class="sr-item-title">${fmtHighlight(ctx, item.title, query)}</span>
      <span class="m">
        ${ctx.logo(item.agent, 16)}
        <span>${ctx.esc(item.project)}</span>
        <span class="chip">${typeLabel}</span>
        <span>${item.turns} 轮</span>
        <span class="r">${ctx.esc(item.date)}</span>
      </span>
    </button>`;
  }

  function placeSrSel(mainEl) {
    const ind = $("#srSelInd", mainEl);
    const on = $(".item.on", mainEl);
    if (!ind) return;
    ind.style.opacity = on ? 1 : 0;
    if (on) {
      ind.style.setProperty("--y", on.offsetTop + "px");
      ind.style.setProperty("--h", on.offsetHeight + "px");
    }
  }

  let latestSearchResults = [];

  Pages.register({
    id: "search",
    navLabel: "检索结果",
    render(mainEl, state, ctx) {
      // 若从顶部控制条直接点「检索结果」且无 query，给一个演示默认词方便直观看高亮
      if (!state.searchQuery && !state._searchClearedOnce) {
        state.searchQuery = "AnimationFrame";
      }
      const q = state.searchQuery || "";
      const scope = state.searchScope || "all";
      const projectOnly = !!state.searchProjectOnly;
      const res = queryItems(state, { query: q, scope, projectOnly });
      latestSearchResults = res.items;

      const sel = res.items.find(x => x.uid === state.searchSelectedId) || res.items[0] || null;
      state.searchSelectedId = sel ? sel.uid : "";

      const curProjObj = (D.projects || []).find(p => p.id === state.project);
      const curProjName = curProjObj ? curProjObj.name : state.project;

      mainEl.innerHTML = `
        <div class="titlebar" id="titlebar">
          <span class="ico">${ctx.icon("search")}</span>
          <h1>检索结果</h1>
          <span class="meta">${q ? `“${ctx.esc(q)}” · 关键词 ${res.keywordCount} / 语义 ${res.semanticCount}` : `最近 ${res.items.length} 条`}</span>
          <span class="spacer"></span>
          ${ctx.searchBoxHTML()}
        </div>
        <div class="page">
          <div class="three">
            <section class="listcol">
              <div class="sr-tools">
                <div class="sr-input-wrap">
                  <span class="ico">${ctx.icon("search")}</span>
                  <input type="text" class="sr-input" id="srQueryInput" value="${ctx.esc(q)}" placeholder="输入关键词过滤并高亮…" />
                </div>
                <div class="sr-filter-row">
                  ${UI.segmentedHTML({
                    id: "srScopeSeg",
                    size: "sm",
                    options: [
                      { value: "all", label: "全部" },
                      { value: "memory", label: "记忆" },
                      { value: "raw", label: "原始记录" },
                    ],
                    value: scope,
                  })}
                  <button type="button" class="cmdk-scope-chip${projectOnly ? " on" : ""}" id="srProjChip" style="margin-left:auto" title="仅限定项目 ${ctx.esc(curProjName)}">
                    <span>仅 ${ctx.esc(curProjName)}</span>
                  </button>
                </div>
              </div>
              <div class="items stagger" id="srItems">
                <span class="sel-ind" id="srSelInd"></span>
                ${res.items.length
                  ? res.items.map((it, i) => renderSearchItem(ctx, it, i, sel && it.uid === sel.uid, q)).join("")
                  : ctx.emptyHTML("没有匹配的记忆或原始记录")}
              </div>
              <div class="listfoot">共 ${res.items.length} 项<span class="spacer"></span>↑↓ 切换</div>
            </section>
            <section class="reader">
              <div class="doc scroll" id="srDoc">
                <div class="doc-enter">${renderSearchDetail(ctx, sel, q)}</div>
              </div>
              <div class="readfoot" id="srFoot">
                <span>${sel ? (sel.kind === "memory" ? "记忆 · 只读" : "原始记录 · 会话主题") : "检索结果"}</span>
                <span class="spacer"></span>
                ${sel && sel.kind === "memory" ? UI.buttonHTML({ id: "srOpenProjFoot", label: "在项目中打开", size: "sm" }) : ""}
                ${UI.buttonHTML({ id: "srCopyFoot", label: "复制正文", icon: ctx.icon("copy"), disabled: !sel })}
              </div>
            </section>
          </div>
        </div>
      `;
    },
    bind(mainEl, state, ctx) {
      const selectUid = uid => {
        const hit = latestSearchResults.find(x => x.uid === uid);
        if (!hit) return;
        state.searchSelectedId = uid;
        $$("[data-sr-uid]", mainEl).forEach(el => el.classList.toggle("on", el.dataset.srUid === uid));
        placeSrSel(mainEl);
        const doc = $("#srDoc", mainEl);
        if (doc) {
          doc.innerHTML = `<div class="doc-enter">${renderSearchDetail(ctx, hit, state.searchQuery)}</div>`;
          doc.scrollTop = 0;
          const jumpBtn = $("#srJumpProjectBtn", doc);
          if (jumpBtn && hit.kind === "memory") jumpBtn.onclick = () => ctx.openMemory(hit.id);
        }
        const foot = $("#srFoot", mainEl);
        if (foot) {
          foot.innerHTML = `
            <span>${hit.kind === "memory" ? "记忆 · 只读" : "原始记录 · 会话主题"}</span>
            <span class="spacer"></span>
            ${hit.kind === "memory" ? UI.buttonHTML({ id: "srOpenProjFoot", label: "在项目中打开", size: "sm" }) : ""}
            ${UI.buttonHTML({ id: "srCopyFoot", label: "复制正文", icon: ctx.icon("copy") })}
          `;
          bindFoot(hit);
        }
      };

      const bindFoot = hit => {
        const openBtn = $("#srOpenProjFoot", mainEl);
        if (openBtn && hit && hit.kind === "memory") openBtn.onclick = () => ctx.openMemory(hit.id);
        const copyBtn = $("#srCopyFoot", mainEl);
        if (copyBtn && hit) {
          copyBtn.onclick = () => {
            const text = hit.kind === "memory" ? ctx.plain(hit.body) : `${hit.title}\n${ctx.plain(hit.summary)}`;
            if (navigator.clipboard && navigator.clipboard.writeText) navigator.clipboard.writeText(text).catch(() => {});
            ctx.toast("已复制正文");
          };
        }
        const jumpBtn = $("#srJumpProjectBtn", mainEl);
        if (jumpBtn && hit && hit.kind === "memory") jumpBtn.onclick = () => ctx.openMemory(hit.id);
      };

      UI.bindSegmented($("#srScopeSeg", mainEl), val => {
        state.searchScope = val;
        ctx.render(false);
      });

      const projChip = $("#srProjChip", mainEl);
      if (projChip) {
        projChip.onclick = () => {
          state.searchProjectOnly = !state.searchProjectOnly;
          ctx.render(false);
        };
      }

      const qInput = $("#srQueryInput", mainEl);
      if (qInput) {
        let debounce = null;
        qInput.oninput = () => {
          state.searchQuery = qInput.value;
          state._searchClearedOnce = !qInput.value;
          clearTimeout(debounce);
          debounce = setTimeout(() => {
            const pos = qInput.selectionStart;
            ctx.render(false);
            const nextInput = $("#srQueryInput");
            if (nextInput) {
              nextInput.focus();
              nextInput.setSelectionRange(pos, pos);
            }
          }, 160);
        };
      }

      $$("[data-sr-uid]", mainEl).forEach(el => {
        el.onclick = () => selectUid(el.dataset.srUid);
      });

      const cur = latestSearchResults.find(x => x.uid === state.searchSelectedId);
      bindFoot(cur);
      placeSrSel(mainEl);
      requestAnimationFrame(() => placeSrSel(mainEl));
    },
    hooks: {
      onKeydown(e, state, ctx) {
        if ((e.key === "ArrowDown" || e.key === "ArrowUp") && latestSearchResults.length) {
          e.preventDefault();
          const dir = e.key === "ArrowDown" ? 1 : -1;
          const idx = latestSearchResults.findIndex(x => x.uid === state.searchSelectedId);
          const next = latestSearchResults[(idx + dir + latestSearchResults.length) % latestSearchResults.length];
          const el = $(`[data-sr-uid="${next.uid}"]`);
          if (el) {
            el.click();
            el.scrollIntoView({ block: "nearest" });
          }
          return true;
        }
        return false;
      },
    },
  });
})();
