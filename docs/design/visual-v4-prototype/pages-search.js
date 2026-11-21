// ⌘K Raycast 式单栏全局搜索面板（含最近搜索上下文记忆与通用记忆检索）
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

  const KIND_ORDER = { memory: 0, common: 1, raw: 2 };

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

    const commons = projectOnly
      ? []
      : (state.commonMemories || D.commonMemories || []).map(c => ({
          uid: "cm:" + c.id,
          kind: "common",
          id: c.id,
          agent: c.agent || "gemini",
          category: c.category || "preference",
          catLabel: c.category === "experience" ? "通用经验" : "个人偏好",
          title: c.title || "",
          body: c.body || "",
          applicability: c.applicability || "",
          source: c.source || "",
          recall: c.recall || 0,
          date: c.date || "今天",
          raw: c,
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
      if (includeMem) {
        list.push(...mems.slice(0, 4));
        list.push(...commons.slice(0, 2));
      }
      if (includeRaw) list.push(...raws.slice(0, 3));
      return { items: list, keywordCount: 0, semanticCount: 0 };
    }

    const matched = [];
    const pool = [
      ...(includeMem ? mems : []),
      ...(includeMem ? commons : []),
      ...(includeRaw ? raws : []),
    ];
    for (const item of pool) {
      let hay = "";
      if (item.kind === "memory") {
        hay = `${item.body} ${item.cat} ${item.project} ${item.source}`;
      } else if (item.kind === "common") {
        hay = `${item.title} ${item.body} ${item.applicability} ${item.source} ${item.category} ${item.catLabel}`;
      } else {
        hay = `${item.title} ${item.summary} ${item.project} ${(item.messages || []).map(x => x.text).join(" ")}`;
      }
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

    // 项目记忆、通用记忆排前，原始记录排后，组内按相关度排序
    matched.sort((a, b) => {
      if (a.kind !== b.kind) return (KIND_ORDER[a.kind] ?? 9) - (KIND_ORDER[b.kind] ?? 9);
      return (b.score || 0) - (a.score || 0);
    });

    const keywordCount = matched.filter(x => x.matchType === "关键词匹配").length;
    const semanticCount = matched.length - keywordCount;
    return { items: matched, keywordCount, semanticCount };
  }

  /* ==========================================================================
   *  全局 ⌘K Raycast 式单栏面板（持久化上次搜索上下文）
   * ========================================================================== */
  let overlayEl = null;
  let hasUsedCmdk = false;
  let lastCmdk = {
    query: "",
    scope: "all",
    projectOnly: false,
    activeIdx: 0,
  };
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

  function saveLastCmdk() {
    hasUsedCmdk = true;
    lastCmdk = {
      query: cmdkState.query,
      scope: cmdkState.scope,
      projectOnly: cmdkState.projectOnly,
      activeIdx: cmdkState.activeIdx,
    };
  }

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
      saveLastCmdk();
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
    saveLastCmdk();
    const rows = $$(".cmdk-row", overlayEl);
    rows.forEach((r, i) => r.classList.toggle("on", i === cmdkState.activeIdx));
    placeCmdkIndicator();
    if (scrollIntoView && rows[cmdkState.activeIdx]) {
      rows[cmdkState.activeIdx].scrollIntoView({ block: "nearest" });
    }
  }

  function openSelectedItem(ctx, item) {
    if (!item) return;
    saveLastCmdk();
    close();
    if (item.kind === "memory") {
      ctx.openMemory(item.id);
    } else if (item.kind === "common") {
      ctx.go("common", { force: true });
      requestAnimationFrame(() => {
        const el = $(`[data-cm-id="${item.id}"]`);
        if (el) {
          el.click();
          el.scrollIntoView({ block: "nearest" });
        }
      });
    } else if (item.kind === "raw") {
      ctx.go("project", { project: item.project, tab: 1, force: true });
      ctx.toast("已定位到原始记录：" + item.title);
    }
  }

  function renderCmdkList(ctx, { scrollActiveIntoView = false } = {}) {
    if (!overlayEl) return;
    const listEl = $("#cmdkList", overlayEl);
    const statEl = $("#cmdkMatchStat", overlayEl);
    const footStatEl = $("#cmdkFootStat", overlayEl);
    if (!listEl) return;

    if (cmdkState.loading) {
      if (statEl) statEl.textContent = "搜索中…";
      if (footStatEl) footStatEl.textContent = "";
      listEl.innerHTML = UI.skeletonGroupHTML({ count: 4, lines: 2, avatar: true, right: false });
      return;
    }

    const q = cmdkState.query.trim();
    if (statEl) {
      statEl.textContent = q
        ? `关键词匹配 ${cmdkState.keywordCount} · 语义相关 ${cmdkState.semanticCount}`
        : `最近 ${cmdkState.items.length} 项`;
    }
    if (footStatEl) {
      footStatEl.textContent = cmdkState.items.length ? `共 ${cmdkState.items.length} 项` : "";
    }

    if (!cmdkState.items.length) {
      cmdkState.activeIdx = 0;
      listEl.innerHTML = ctx.emptyHTML(q ? `未找到与“${q}”相关的结果` : "暂无最近记忆或原始记录");
      return;
    }

    if (cmdkState.activeIdx < 0 || cmdkState.activeIdx >= cmdkState.items.length) {
      cmdkState.activeIdx = 0;
    }

    let prevGroup = "";
    let html = '<span class="cmdk-sel-ind" id="cmdkSelInd"></span>';
    cmdkState.items.forEach((it, idx) => {
      let grpLabel = "";
      if (it.kind === "memory") grpLabel = q ? "记忆" : "最近记忆";
      else if (it.kind === "common") grpLabel = "通用记忆";
      else grpLabel = q ? "原始记录" : "最近原始记录";

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
      } else if (it.kind === "common") {
        html += `<button type="button" class="cmdk-row${on ? " on" : ""}" data-cmdk-idx="${idx}">
          ${ctx.logo(it.agent, 20)}
          <span class="cmdk-row-body">
            <span class="cmdk-row-text">${it.title ? `<strong class="raw-title">${fmtHighlight(ctx, it.title, q)}</strong> ` : ""}<span class="cmdk-row-sub">${fmtHighlight(ctx, it.body, q)}</span></span>
            <span class="cmdk-row-meta">
              <span>通用记忆</span>
              ${ctx.recallTag(it.recall)}
              <span class="chip">${ctx.esc(it.catLabel)}</span>
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
    requestAnimationFrame(() => {
      if (scrollActiveIntoView && overlayEl) {
        const activeRow = $(".cmdk-row.on", overlayEl);
        if (activeRow) activeRow.scrollIntoView({ block: "nearest" });
      }
      placeCmdkIndicator();
    });
  }

  function runCmdkSearch(ctx, immediate = false, { preserveActiveIdx = false, scrollActiveIntoView = false } = {}) {
    clearTimeout(searchTimer);
    const delay = immediate || ctx.isReducedMotion() ? 0 : 220;
    const finish = () => {
      cmdkState.loading = false;
      const res = queryItems(ctx.state, cmdkState);
      cmdkState.items = res.items;
      cmdkState.keywordCount = res.keywordCount;
      cmdkState.semanticCount = res.semanticCount;
      if (!preserveActiveIdx) {
        cmdkState.activeIdx = 0;
      } else if (!res.items.length) {
        cmdkState.activeIdx = 0;
      } else {
        cmdkState.activeIdx = Math.max(0, Math.min(cmdkState.activeIdx, res.items.length - 1));
      }
      renderCmdkList(ctx, { scrollActiveIntoView });
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
    const explicitQuery = opts.query != null;
    const nextQuery = explicitQuery ? String(opts.query) : lastCmdk.query;
    const nextScope = opts.scope != null ? opts.scope : (explicitQuery ? "all" : lastCmdk.scope);
    const nextProjectOnly = explicitQuery
      ? !!opts.projectOnly
      : (hasUsedCmdk ? lastCmdk.projectOnly : !!opts.projectOnly);
    const nextActiveIdx = explicitQuery ? 0 : lastCmdk.activeIdx;

    cmdkState = {
      query: nextQuery,
      scope: nextScope,
      projectOnly: nextProjectOnly,
      activeIdx: nextActiveIdx,
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
          <span class="cmdk-hint"><kbd>Esc</kbd> 关闭</span>
          <span class="spacer"></span>
          <span class="cmdk-foot-stat" id="cmdkFootStat"></span>
        </div>
      </div>
    `;

    ctx.windowEl().appendChild(scrim);
    overlayEl = scrim;

    const inputEl = $("#cmdkInput", scrim);
    const clearBtn = $("#cmdkClear", scrim);
    const projChip = $("#cmdkProjChip", scrim);

    UI.bindSegmented($("#cmdkScopeSeg", scrim), val => {
      cmdkState.scope = val;
      cmdkState.activeIdx = 0;
      saveLastCmdk();
      runCmdkSearch(ctx, true);
    });

    projChip.onclick = () => {
      cmdkState.projectOnly = !cmdkState.projectOnly;
      cmdkState.activeIdx = 0;
      projChip.classList.toggle("on", cmdkState.projectOnly);
      projChip.setAttribute("aria-pressed", cmdkState.projectOnly ? "true" : "false");
      projChip.querySelector(".ico").innerHTML = ctx.icon("folder", cmdkState.projectOnly);
      saveLastCmdk();
      runCmdkSearch(ctx, true);
    };

    inputEl.oninput = () => {
      cmdkState.query = inputEl.value;
      cmdkState.activeIdx = 0;
      clearBtn.hidden = !cmdkState.query;
      saveLastCmdk();
      runCmdkSearch(ctx, false);
    };

    clearBtn.onclick = () => {
      cmdkState.query = "";
      cmdkState.activeIdx = 0;
      lastCmdk.query = "";
      lastCmdk.activeIdx = 0;
      saveLastCmdk();
      inputEl.value = "";
      clearBtn.hidden = true;
      inputEl.focus();
      runCmdkSearch(ctx, true);
    };

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
      } else if (e.key === "Enter") {
        e.preventDefault();
        e.stopPropagation();
        if (cmdkState.items.length) openSelectedItem(ctx, cmdkState.items[cmdkState.activeIdx]);
      }
    };
    document.addEventListener("keydown", keydownHandler, true);

    if (cmdkState.loading) {
      renderCmdkList(ctx);
    } else {
      runCmdkSearch(ctx, true, { preserveActiveIdx: !explicitQuery, scrollActiveIntoView: !explicitQuery });
    }

    setTimeout(() => {
      if (inputEl && document.body.contains(inputEl)) {
        inputEl.focus();
        if (cmdkState.query) inputEl.select();
      }
    }, 20);
  }

  Pages.registerCommandPalette({ open, close, isOpen });
})();
