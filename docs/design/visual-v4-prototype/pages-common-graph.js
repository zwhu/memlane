// 通用记忆（Common Memory）V4 三栏页 & 项目「图谱」（Project Graph）Tab
(() => {
  const Pages = window.MemlanePages;
  const UI = window.MemlaneUI;
  const D = window.DATA || window.MEMLANE_DATA;
  if (!Pages || !UI) return;

  const $ = (s, el = document) => el.querySelector(s);
  const $$ = (s, el = document) => [...el.querySelectorAll(s)];

  /* ==========================================================================
   * 兜底合成数据（当壳层未注入 commonMemories / graphData 时自动补齐）
   * ========================================================================== */
  const FALLBACK_COMMON_MEMORIES = [
    {
      id: "cm-1",
      category: "preference",
      title: "中文简洁汇报与结论前置",
      body: "默认使用简洁中文交流，汇报结论先给可执行判断，再补关键文件路径与验证数据；严格区分「已验证事实」与「待核对假设」。",
      applicability: "所有跨项目协作与评审总结",
      source: "Antigravity · 2026-11-19 会话提炼",
      recall: 18,
      date: "今天",
      agent: "gemini",
      canonical: true,
    },
    {
      id: "cm-2",
      category: "preference",
      title: "界面克制去卡片化（Things 3 / Linear 风格）",
      body: "桌面端与设置页优先使用发丝分隔线与留白分区，避免多层带边框卡片嵌套；颜色只承载语义，分类标签一律中性灰。",
      applicability: "macOS Native 客户端与原型评审",
      source: "Claude Code · 2026-11-20 视觉 V4 讨论",
      recall: 12,
      date: "昨天",
      agent: "claude",
      canonical: true,
    },
    {
      id: "cm-3",
      category: "experience",
      title: "条件更新下推 SQL 避免库存/余额并发竞态",
      body: "并发场景禁止应用层先 `SELECT` 再 `UPDATE`；将下限条件直接写入 `UPDATE ... WHERE count >= :delta` 并校验受影响行数。",
      applicability: "所有关系型数据库并发扣减逻辑",
      source: "Codex · item-fe 压测复盘",
      recall: 9,
      date: "11月18日",
      agent: "codex",
      canonical: true,
    },
    {
      id: "cm-4",
      category: "experience",
      title: "自绘循环仅在确有运动时请求下一帧",
      body: "窗口被遮挡或弹簧已收敛时立即停止请求 `AnimationFrame()`；系统开启「减少动态效果」时直接落到终态，避免后台空转占 CPU。",
      applicability: "MyGo / 自绘 UI 动画实现",
      source: "Claude Code · memlane 性能排查",
      recall: 6,
      date: "11月16日",
      agent: "claude",
      canonical: true,
    },
    {
      id: "cm-5",
      category: "preference",
      title: "未配置模型参数保持省略，显式 0 必须保留",
      body: "启动、探针验证与热切换统一透传用户显式填写的参数；不得给未配置项补默认值，也不得将显式填写的 `0` 当作空值丢弃。",
      applicability: "LLM Provider / Ollama 配置与运行期切换",
      source: "Codex · 模型契约回归",
      recall: 14,
      date: "11月15日",
      agent: "codex",
      canonical: true,
    },
  ];

  const FALLBACK_GRAPH_DATA = {
    memlane: {
      codeEnabled: false,
      pendingHint: "有 2 份文档尚未提炼进图谱 · 可在「管理」查看",
      nodes: [
        { id: "n1", label: "Cognee Gateway", source: "架构契约", x: 430, y: 210, color: "#818CF8", docTitle: "外置网关 ACL 与召回合并", docId: "m6" },
        { id: "n2", label: "provenance_store", source: "存储层", x: 290, y: 140, color: "#38BDF8", docTitle: "记忆日期锁定与回执存储", docId: "m2" },
        { id: "n3", label: "supervisor", source: "守护进程", x: 580, y: 150, color: "#34D399", docTitle: "单实例 flock 锁与 Setsid 进程组", docId: "m5" },
        { id: "n4", label: "antigravity_plugin", source: "宿主接入", x: 270, y: 290, color: "#F472B6", docTitle: "0600 原子写入 last-hook-error.json", docId: "m1" },
        { id: "n5", label: "simpledesktop V4", source: "桌面端 UI", x: 590, y: 310, color: "#FBBF24", docTitle: "MyGo 自绘循环与 V4 动效", docId: "m9" },
        { id: "n6", label: "generation_config", source: "模型契约", x: 440, y: 350, color: "#A78BFA", docTitle: "native_llm_configuration 统一透传", docId: "m7" },
        { id: "n7", label: "official-store", source: "存储层", x: 430, y: 95, color: "#38BDF8", docTitle: "LanceDB 与 Ladybug 唯一真相", docId: "m6" },
        { id: "n8", label: "task.py 磁盘门禁", source: "工程约束", x: 690, y: 230, color: "#FB7185", docTitle: "50/55 GiB 磁盘水位保护", docId: "m8" },
      ],
      links: [
        ["n1", "n2"], ["n1", "n3"], ["n1", "n4"], ["n1", "n5"],
        ["n1", "n6"], ["n1", "n7"], ["n2", "n7"], ["n3", "n8"], ["n5", "n6"],
      ],
    },
    "item-fe": {
      codeEnabled: true,
      pendingHint: "",
      nodes: [
        { id: "i1", label: "StockService", source: "库存域", x: 410, y: 200, color: "#818CF8", docTitle: "商品扣减库存并发竞争条件修复", docId: "m12" },
        { id: "i2", label: "SQL 条件扣减", source: "数据库", x: 270, y: 270, color: "#34D399", docTitle: "WHERE quantity >= :count", docId: "m12" },
        { id: "i3", label: "pnpm-lock", source: "构建约束", x: 560, y: 250, color: "#FBBF24", docTitle: "CI --frozen-lockfile 约定", docId: "m13" },
      ],
      links: [["i1", "i2"], ["i1", "i3"]],
    },
  };

  function buildCodeGraphFallback(project) {
    const name = (project && project.name) || "project";
    return {
      nodes: [
        { id: "c1", label: `${name}/main`, source: "入口文件", x: 430, y: 130, color: "#60A5FA", docTitle: `cmd/${name}/main.go`, docId: "" },
        { id: "c2", label: "http_transport.go", source: "传输层", x: 290, y: 230, color: "#34D399", docTitle: "internal/simpledesktop/http_transport.go", docId: "" },
        { id: "c3", label: "project_control_server.py", source: "接入网关", x: 560, y: 220, color: "#818CF8", docTitle: "engine/python/project_control_server.py", docId: "m6" },
        { id: "c4", label: "cognee_extension_gateway.py", source: "外置网关", x: 430, y: 310, color: "#F472B6", docTitle: "engine/python/cognee_extension_gateway.py", docId: "m6" },
        { id: "c5", label: "provenance_store.py", source: "存储模块", x: 640, y: 325, color: "#38BDF8", docTitle: "engine/python/provenance_store.py", docId: "m2" },
        { id: "c6", label: "overview_ui.go", source: "视图控制器", x: 250, y: 340, color: "#FBBF24", docTitle: "internal/simpledesktop/overview_ui.go", docId: "m3" },
      ],
      links: [
        ["c1", "c2"], ["c1", "c6"], ["c2", "c3"], ["c3", "c4"], ["c4", "c5"], ["c2", "c6"],
      ],
    };
  }

  /* ==========================================================================
   * 通用辅助
   * ========================================================================== */
  const initParams = new URLSearchParams(location.search);

  function ensureCommonMemories(state, ctx) {
    if (!Array.isArray(state.commonMemories)) {
      const fromData = (ctx.DATA && ctx.DATA.commonMemories) || (D && D.commonMemories);
      state.commonMemories = Array.isArray(fromData)
        ? fromData.map(x => ({ ...x }))
        : FALLBACK_COMMON_MEMORIES.map(x => ({ ...x }));
    }
    return state.commonMemories;
  }

  function ensureGraphData(state, ctx, projectId) {
    if (!state.graphData || typeof state.graphData !== "object") {
      const fromData = (ctx.DATA && ctx.DATA.graphData) || (D && D.graphData);
      state.graphData = fromData
        ? JSON.parse(JSON.stringify(fromData))
        : JSON.parse(JSON.stringify(FALLBACK_GRAPH_DATA));
    }
    if (!state.graphData[projectId]) {
      state.graphData[projectId] = {
        codeEnabled: false,
        pendingHint: "",
        nodes: [
          { id: `${projectId}-n1`, label: `${projectId} 核心模块`, source: "项目结构", x: 380, y: 200, color: "#818CF8", docTitle: "项目基础记忆", docId: "" },
          { id: `${projectId}-n2`, label: "配置与约定", source: "项目约定", x: 540, y: 260, color: "#34D399", docTitle: "项目约定记录", docId: "" },
        ],
        links: [[`${projectId}-n1`, `${projectId}-n2`]],
      };
    }
    return state.graphData[projectId];
  }

  function catLabel(cat) {
    return cat === "experience" ? "通用经验" : "个人偏好";
  }

  function agentName(ctx, agentId) {
    const agents = (ctx.DATA && ctx.DATA.agents) || (D && D.agents) || {};
    if (agentId && agents[agentId]) return agents[agentId].name;
    return agentId ? String(agentId) : "手动录入";
  }

  function renderRichBody(ctx, body) {
    const blocks = String(body || "").trim().split(/\n{2,}/);
    return blocks.map(block => {
      const lines = block.split("\n").map(l => l.trim()).filter(Boolean);
      const isList = lines.length > 0 && lines.every(l => /^[-*]\s+/.test(l));
      if (isList) {
        return `<ul>${lines.map(l => `<li>${ctx.fmt(l.replace(/^[-*]\s+/, ""))}</li>`).join("")}</ul>`;
      }
      return `<p>${lines.map(l => ctx.fmt(l)).join("<br>")}</p>`;
    }).join("");
  }

  function bindEmptyParallax(scope) {
    const empty = $("#empty", scope);
    if (!empty || !empty.parentElement) return;
    empty.parentElement.onpointermove = e => {
      const r = empty.getBoundingClientRect();
      const b = $(".blocks", empty);
      if (!b || !r.width || !r.height) return;
      b.style.setProperty("--px", `${(-(e.clientX - r.left - r.width / 2) / r.width * 10).toFixed(1)}px`);
      b.style.setProperty("--py", `${(-(e.clientY - r.top - r.height / 2) / r.height * 8).toFixed(1)}px`);
    };
  }

  /* ==========================================================================
   * 1. 通用记忆（Common Memory）V4 三栏页
   * ========================================================================== */
  const cmState = {
    filter: "all", // "all" | "preference" | "experience"
    selectedId: "",
    editing: initParams.get("page") === "common" && initParams.get("editor") === "1",
    editMode: "create", // "create" | "edit"
    editId: "",
    extraOpen: false,
    draft: {
      category: "preference",
      title: "",
      body: "",
      applicability: "",
      source: "",
    },
  };

  function filteredCommon(state, ctx) {
    const all = ensureCommonMemories(state, ctx);
    if (cmState.filter === "all") return all;
    return all.filter(x => x.category === cmState.filter);
  }

  function cmItemHTML(item, idx, isOn, ctx) {
    const snippet = item.title
      ? `<span class="cm-item-title">${ctx.esc(item.title)}</span>${ctx.fmt(item.body)}`
      : ctx.fmt(item.body);
    return `<button type="button" class="item cm-item${isOn ? " on" : ""}" style="--i:${idx}" data-cm-id="${ctx.esc(item.id)}">
      <span class="s">${snippet}</span>
      <span class="m">
        ${ctx.logo(item.agent || "gemini", 16)}
        <span class="chip">${catLabel(item.category)}</span>
        ${ctx.recallTag(item.recall || 0)}
        <span class="r">${ctx.esc(item.date || "今天")}</span>
      </span>
    </button>`;
  }

  function cmReaderHTML(item, ctx) {
    if (!item) {
      return {
        doc: ctx.emptyHTML("当前分类没有通用记忆"),
        foot: `<span>跨项目参考 · 项目明确约定优先</span>`,
      };
    }
    const appSec = item.applicability
      ? `<section class="sec"><h3>适用条件</h3><p>${ctx.fmt(item.applicability)}</p></section>`
      : "";
    const srcSec = item.source
      ? `<section class="sec"><h3>原始来源</h3><p>${ctx.fmt(item.source)}</p></section>`
      : "";
    return {
      doc: `<article class="detail doc-enter cm-detail">
        <div class="cm-meta-bar">
          <div class="cm-meta-info">
            ${ctx.logo(item.agent || "gemini", 18)}
            <span>${ctx.esc(agentName(ctx, item.agent))}</span>
            <span>·</span>
            <span class="chip">${catLabel(item.category)}</span>
            ${ctx.recallTag(item.recall || 0)}
            <span>${ctx.esc(item.date || "今天")}</span>
          </div>
          <div class="cm-meta-actions">
            ${UI.buttonHTML({ id: "commonEditBtn", label: "编辑", size: "sm" })}
            ${UI.buttonHTML({ id: "commonDeleteBtn", label: "删除", variant: "danger-outline", size: "sm" })}
          </div>
        </div>
        ${item.title ? `<h2 class="cm-title">${ctx.esc(item.title)}</h2>` : ""}
        <div class="bodytext cm-body">${renderRichBody(ctx, item.body)}</div>
        ${appSec}
        ${srcSec}
      </article>`,
      foot: `<span>跨项目参考 · 项目明确约定优先</span><span class="spacer"></span>${UI.buttonHTML({ id: "commonCopyBtn", label: "复制正文", icon: ctx.icon("copy") })}`,
    };
  }

  function cmEditorHTML(ctx) {
    const isEdit = cmState.editMode === "edit";
    const titleText = isEdit ? "编辑通用记忆" : "新增通用记忆";
    const d = cmState.draft;
    const segHTML = UI.segmentedHTML({
      id: "cmEditCatSeg",
      size: "sm",
      value: d.category,
      options: [
        { value: "preference", label: "个人偏好" },
        { value: "experience", label: "通用经验" },
      ],
      attrs: isEdit ? 'aria-disabled="true" title="编辑已有记忆时不可切换分类"' : "",
    });
    return `
      <div class="cm-editor-head">
        <span class="cm-editor-title">${titleText}</span>
        <div class="${isEdit ? "cm-seg-disabled" : ""}" id="cmEditCatWrap">${segHTML}</div>
      </div>
      <div class="doc scroll">
        <div class="cm-editor-form doc-enter">
          <div class="cm-field">
            <label class="cm-field-label" for="cmEditTitle">
              <span>标题</span>
              <span class="cm-field-hint">一句话概括偏好或经验</span>
            </label>
            <input type="text" class="v4-input" id="cmEditTitle" value="${ctx.esc(d.title)}" placeholder="例如：默认使用简洁中文汇报并结论前置">
          </div>
          <div class="cm-field">
            <label class="cm-field-label" for="cmEditBody">
              <span>正文</span>
              <span class="cm-field-hint">支持 Markdown 与行内代码 \`code\`</span>
            </label>
            <textarea class="v4-textarea cm-edit-body" id="cmEditBody" placeholder="写下跨项目可复用的偏好、约定或踩坑经验…">${ctx.esc(d.body)}</textarea>
          </div>
          <div class="cm-extra-wrap">
            <button type="button" class="cm-extra-toggle${cmState.extraOpen ? " is-open" : ""}" id="cmToggleExtra" aria-expanded="${cmState.extraOpen ? "true" : "false"}">
              <span class="ico">${ctx.icon("chev")}</span>
              <span>补充适用条件与来源（可选）</span>
            </button>
            <div class="cm-extra-fields" id="cmExtraFields"${cmState.extraOpen ? "" : " hidden"}>
              <div class="cm-field">
                <label class="cm-field-label" for="cmEditApp">
                  <span>适用条件</span>
                  <span class="cm-field-hint">说明触发范围，项目约定优先</span>
                </label>
                <input type="text" class="v4-input" id="cmEditApp" value="${ctx.esc(d.applicability)}" placeholder="例如：所有 Go / TypeScript 项目或特定场景">
              </div>
              <div class="cm-field">
                <label class="cm-field-label" for="cmEditSource">
                  <span>原始来源</span>
                  <span class="cm-field-hint">记录来源会话或复盘文档</span>
                </label>
                <input type="text" class="v4-input" id="cmEditSource" value="${ctx.esc(d.source)}" placeholder="例如：Antigravity · 2026-11-21 会话">
              </div>
            </div>
          </div>
        </div>
      </div>
      <div class="readfoot">
        <span class="cm-editor-foot-hint">${isEdit ? "编辑已有记录 · 保存后立即生效" : "新建草稿 · 跨项目共享"}</span>
        ${UI.buttonHTML({ id: "cmDiscardBtn", label: "放弃草稿", variant: "ghost" })}
        ${UI.buttonHTML({ id: "cmBackReadBtn", label: "返回阅读" })}
        ${UI.buttonHTML({ id: "cmSaveBtn", label: "保存", variant: "primary" })}
      </div>
    `;
  }

  function placeCommonSelInd(scope) {
    const ind = $("#commonSelInd", scope);
    const on = $(".cm-item.on", scope);
    if (!ind) return;
    ind.style.opacity = on ? 1 : 0;
    if (on) {
      ind.style.setProperty("--y", on.offsetTop + "px");
      ind.style.setProperty("--h", on.offsetHeight + "px");
    }
  }

  function startCreateCommon() {
    cmState.editing = true;
    cmState.editMode = "create";
    cmState.editId = "";
    cmState.extraOpen = false;
    cmState.draft = {
      category: cmState.filter === "experience" ? "experience" : "preference",
      title: "",
      body: "",
      applicability: "",
      source: "",
    };
  }

  function startEditCommon(item) {
    if (!item) return;
    cmState.editing = true;
    cmState.editMode = "edit";
    cmState.editId = item.id;
    cmState.extraOpen = !!(item.applicability || item.source);
    cmState.draft = {
      category: item.category || "preference",
      title: item.title || "",
      body: item.body || "",
      applicability: item.applicability || "",
      source: item.source || "",
    };
  }

  function openDiscardDraftDialog(ctx) {
    UI.openDialog({
      title: "放弃当前草稿？",
      size: 480,
      danger: true,
      cancelText: "继续编辑",
      confirmText: "放弃草稿",
      body: "<p>未保存的标题、正文与补充信息将被丢弃，且无法恢复。</p>",
      onConfirm() {
        cmState.editing = false;
        ctx.render(false);
        ctx.toast("已放弃草稿");
      },
    });
  }

  function openDeleteCommonDialog(item, state, ctx) {
    if (!item) return;
    UI.openDialog({
      title: "删除这条通用记忆？",
      size: 480,
      danger: true,
      cancelText: "取消",
      confirmText: "删除记忆",
      body: `<p>即将永久删除「<strong>${ctx.esc(item.title || ctx.plain(item.body).slice(0, 28))}</strong>」，删除后不再参与跨项目召回。</p>`,
      onConfirm() {
        const list = ensureCommonMemories(state, ctx);
        const idx = list.findIndex(x => x.id === item.id);
        if (idx >= 0) list.splice(idx, 1);
        const nextList = filteredCommon(state, ctx);
        cmState.selectedId = nextList[Math.min(idx, nextList.length - 1)]?.id || "";
        ctx.render(false);
        ctx.toast("已删除通用记忆");
      },
    });
  }

  function bindCommonReaderActions(scope, item, state, ctx) {
    const editBtn = $("#commonEditBtn", scope);
    if (editBtn && item) {
      editBtn.onclick = () => {
        startEditCommon(item);
        ctx.render(false);
      };
    }
    const delBtn = $("#commonDeleteBtn", scope);
    if (delBtn && item) {
      delBtn.onclick = () => openDeleteCommonDialog(item, state, ctx);
    }
    const copyBtn = $("#commonCopyBtn", scope);
    if (copyBtn && item) {
      copyBtn.onclick = () => {
        const text = item.title ? `${item.title}\n\n${item.body}` : item.body;
        if (navigator.clipboard && navigator.clipboard.writeText) {
          navigator.clipboard.writeText(text).catch(() => {});
        }
        ctx.toast("已复制通用记忆正文");
      };
    }
  }

  function selectCommonItem(scope, id, state, ctx, scrollIntoView = false) {
    const list = filteredCommon(state, ctx);
    const item = list.find(x => x.id === id);
    if (!item) return;
    cmState.selectedId = id;
    if (cmState.editing) {
      cmState.editing = false;
      ctx.render(false);
      return;
    }
    $$(".cm-item", scope).forEach(el => el.classList.toggle("on", el.dataset.cmId === id));
    placeCommonSelInd(scope);
    if (scrollIntoView) {
      const activeEl = $(`.cm-item[data-cm-id="${id}"]`, scope);
      if (activeEl) activeEl.scrollIntoView({ block: "nearest" });
    }
    const doc = $("#commonDoc", scope);
    const foot = $("#commonFoot", scope);
    const view = cmReaderHTML(item, ctx);
    if (doc) {
      doc.innerHTML = view.doc;
      doc.scrollTop = 0;
    }
    if (foot) foot.innerHTML = view.foot;
    bindCommonReaderActions(scope, item, state, ctx);
  }

  Pages.register({
    id: "common",
    navLabel: "通用记忆",
    render(main, state, ctx) {
      const list = filteredCommon(state, ctx);
      const sel = list.find(x => x.id === cmState.selectedId) || list[0] || null;
      cmState.selectedId = sel ? sel.id : "";

      const filterSeg = UI.segmentedHTML({
        id: "commonFilterSeg",
        fullWidth: true,
        size: "sm",
        value: cmState.filter,
        options: [
          { value: "all", label: "全部" },
          { value: "preference", label: "个人偏好" },
          { value: "experience", label: "通用经验" },
        ],
      });

      const readerSection = cmState.editing
        ? cmEditorHTML(ctx)
        : (() => {
            const view = cmReaderHTML(sel, ctx);
            return `<div class="doc scroll" id="commonDoc">${view.doc}</div><div class="readfoot" id="commonFoot">${view.foot}</div>`;
          })();

      return `
        <div class="titlebar" id="titlebar">
          <span class="ico">${ctx.icon("common")}</span>
          <h1>通用记忆</h1>
          <span class="meta">跨项目共享偏好与经验</span>
          <span class="spacer"></span>
          ${ctx.searchBoxHTML()}
        </div>
        <div class="page">
          <div class="three">
            <section class="listcol cm-listcol">
              <div class="cm-list-head">
                <span class="cm-list-title">记忆库</span>
                <span class="spacer"></span>
                ${UI.buttonHTML({ id: "commonAddBtn", label: "新增", size: "sm", icon: ctx.icon("plus") })}
                <button type="button" class="cm-refresh-btn" id="commonRefreshBtn" title="刷新通用记忆">${ctx.icon("refresh")}</button>
              </div>
              <div class="cm-list-tools">${filterSeg}</div>
              <div class="items stagger cm-items" id="commonItems">
                <span class="sel-ind" id="commonSelInd"></span>
                ${list.length ? list.map((it, i) => cmItemHTML(it, i, sel && it.id === sel.id, ctx)).join("") : ctx.emptyHTML("当前分类没有通用记忆")}
              </div>
              <div class="listfoot">共 ${list.length} 条<span class="spacer"></span>${list.length ? "1 / 1" : "0 / 0"}</div>
            </section>
            <section class="reader" id="commonReader">
              ${readerSection}
            </section>
          </div>
        </div>
      `;
    },
    bind(main, state, ctx) {
      const list = filteredCommon(state, ctx);
      const sel = list.find(x => x.id === cmState.selectedId) || null;

      UI.bindSegmented($("#commonFilterSeg", main), val => {
        cmState.filter = val;
        const nextList = filteredCommon(state, ctx);
        if (!nextList.some(x => x.id === cmState.selectedId)) {
          cmState.selectedId = nextList[0] ? nextList[0].id : "";
        }
        ctx.render(false);
      });

      const addBtn = $("#commonAddBtn", main);
      if (addBtn) {
        addBtn.onclick = () => {
          startCreateCommon();
          ctx.render(false);
        };
      }

      const refBtn = $("#commonRefreshBtn", main);
      if (refBtn) {
        refBtn.onclick = () => ctx.toast("已刷新通用记忆库");
      }

      $$(".cm-item", main).forEach(btn => {
        btn.onclick = () => selectCommonItem(main, btn.dataset.cmId, state, ctx, false);
      });
      placeCommonSelInd(main);
      requestAnimationFrame(() => placeCommonSelInd(main));

      if (cmState.editing) {
        const catSeg = $("#cmEditCatSeg", main);
        if (catSeg) {
          UI.bindSegmented(catSeg, val => {
            if (cmState.editMode === "edit") return;
            cmState.draft.category = val;
          });
        }
        const titleEl = $("#cmEditTitle", main);
        const bodyEl = $("#cmEditBody", main);
        const appEl = $("#cmEditApp", main);
        const srcEl = $("#cmEditSource", main);
        if (titleEl) titleEl.oninput = () => { cmState.draft.title = titleEl.value; };
        if (bodyEl) bodyEl.oninput = () => { cmState.draft.body = bodyEl.value; };
        if (appEl) appEl.oninput = () => { cmState.draft.applicability = appEl.value; };
        if (srcEl) srcEl.oninput = () => { cmState.draft.source = srcEl.value; };

        const toggleExtra = $("#cmToggleExtra", main);
        const extraFields = $("#cmExtraFields", main);
        if (toggleExtra && extraFields) {
          toggleExtra.onclick = () => {
            cmState.extraOpen = !cmState.extraOpen;
            toggleExtra.classList.toggle("is-open", cmState.extraOpen);
            toggleExtra.setAttribute("aria-expanded", cmState.extraOpen ? "true" : "false");
            extraFields.hidden = !cmState.extraOpen;
          };
        }

        const discardBtn = $("#cmDiscardBtn", main);
        if (discardBtn) discardBtn.onclick = () => openDiscardDraftDialog(ctx);

        const backBtn = $("#cmBackReadBtn", main);
        if (backBtn) {
          backBtn.onclick = () => {
            cmState.editing = false;
            ctx.render(false);
          };
        }

        const saveBtn = $("#cmSaveBtn", main);
        if (saveBtn) {
          saveBtn.onclick = () => {
            const title = (cmState.draft.title || "").trim();
            const body = (cmState.draft.body || "").trim();
            if (!body) {
              ctx.toast("请填写通用记忆正文");
              if (bodyEl) bodyEl.focus();
              return;
            }
            const all = ensureCommonMemories(state, ctx);
            let savedId = "";
            if (cmState.editMode === "edit" && cmState.editId) {
              const target = all.find(x => x.id === cmState.editId);
              if (target) {
                target.title = title;
                target.body = body;
                target.applicability = (cmState.draft.applicability || "").trim();
                target.source = (cmState.draft.source || "").trim();
                target.date = "刚刚";
                savedId = target.id;
              }
            } else {
              const newItem = {
                id: "cm-" + Date.now(),
                category: cmState.draft.category || "preference",
                title: title || ctx.plain(body).slice(0, 22),
                body,
                applicability: (cmState.draft.applicability || "").trim(),
                source: (cmState.draft.source || "").trim() || "手动录入",
                recall: 0,
                date: "刚刚",
                agent: "gemini",
                canonical: true,
              };
              all.unshift(newItem);
              savedId = newItem.id;
              if (cmState.filter !== "all" && cmState.filter !== newItem.category) {
                cmState.filter = "all";
              }
            }
            cmState.editing = false;
            cmState.selectedId = savedId;
            ctx.render(false);
            ctx.toast("已保存通用记忆");
          };
        }

        if (titleEl && !titleEl.value) setTimeout(() => titleEl.focus(), 20);
      } else {
        bindCommonReaderActions(main, sel, state, ctx);
      }

      bindEmptyParallax(main);
    },
    hooks: {
      onKeydown(e, state, ctx) {
        if (state.page !== "common" || cmState.editing) return false;
        if (e.key !== "ArrowDown" && e.key !== "ArrowUp") return false;
        const list = filteredCommon(state, ctx);
        if (!list.length) return false;
        e.preventDefault();
        const dir = e.key === "ArrowDown" ? 1 : -1;
        const idx = list.findIndex(x => x.id === cmState.selectedId);
        const next = list[(idx + dir + list.length) % list.length];
        const main = $("#main");
        if (main && next) selectCommonItem(main, next.id, state, ctx, true);
        return true;
      },
    },
  });

  /* ==========================================================================
   * 2. 项目「图谱」（Project Graph）Tab
   * ========================================================================== */
  const pgState = {
    mode: initParams.get("graphMode") === "code" ? "code" : "entity", // "entity" | "code"
    selectedNodeId: "",
    sourceFilter: "",
    viewBox: "",
    lastProjectId: "",
  };

  function computeFitViewBox(nodes) {
    if (!nodes || !nodes.length) return "0 0 880 460";
    let minX = Infinity, maxX = -Infinity, minY = Infinity, maxY = -Infinity;
    for (const n of nodes) {
      if (n.x < minX) minX = n.x;
      if (n.x > maxX) maxX = n.x;
      if (n.y < minY) minY = n.y;
      if (n.y > maxY) maxY = n.y;
    }
    const padLeft = 250; // 为左侧 208px Sources 栏留出视觉空间
    const padRight = 110;
    const padTop = 76;   // 为左上浮层工具条留出空间
    const padBottom = 64;
    const w = Math.max(760, (maxX - minX) + padLeft + padRight);
    const h = Math.max(400, (maxY - minY) + padTop + padBottom);
    const vx = Math.round(minX - padLeft);
    const vy = Math.round(minY - padTop);
    return `${vx} ${vy} ${Math.round(w)} ${Math.round(h)}`;
  }

  function getActiveGraphNodesAndLinks(graph, project) {
    if (pgState.mode === "code") {
      if (Array.isArray(graph.codeNodes) && graph.codeNodes.length) {
        return { nodes: graph.codeNodes, links: graph.codeLinks || [] };
      }
      return buildCodeGraphFallback(project);
    }
    return {
      nodes: Array.isArray(graph.nodes) ? graph.nodes : [],
      links: Array.isArray(graph.links) ? graph.links : [],
    };
  }

  function groupSources(nodes) {
    const map = new Map();
    for (const n of nodes) {
      const key = n.source || "未分类";
      if (!map.has(key)) map.set(key, { name: key, color: n.color || "#818CF8", count: 0 });
      map.get(key).count += 1;
    }
    return [...map.values()];
  }

  function jumpToSourceMemory(project, node, state, ctx) {
    const mems = state.memories || [];
    const matched = node && node.docId ? mems.find(m => m.id === node.docId && m.project === project.id) : null;
    if (matched) {
      ctx.go("project", { project: project.id, tab: 0, memoryId: matched.id, force: true });
    } else {
      ctx.go("project", { project: project.id, tab: 0, force: true });
    }
    if (node && node.docTitle) {
      ctx.toast(`已定位至项目记忆 · ${node.docTitle}`);
    }
  }

  function renderGraphCanvasHTML(nodes, links, ctx) {
    const sources = groupSources(nodes);
    const selNode = nodes.find(n => n.id === pgState.selectedNodeId) || null;
    const nodeMap = new Map(nodes.map(n => [n.id, n]));

    const neighborIds = new Set();
    if (selNode) {
      neighborIds.add(selNode.id);
      for (const [a, b] of links) {
        if (a === selNode.id) neighborIds.add(b);
        if (b === selNode.id) neighborIds.add(a);
      }
    }

    const matchSource = n => !pgState.sourceFilter || (n.source || "未分类") === pgState.sourceFilter;
    const visibleCount = nodes.filter(matchSource).length;

    const edgesSVG = links.map(([aId, bId]) => {
      const a = nodeMap.get(aId);
      const b = nodeMap.get(bId);
      if (!a || !b) return "";
      const isHi = selNode && (aId === selNode.id || bId === selNode.id);
      const isDim = (pgState.sourceFilter && (!matchSource(a) && !matchSource(b))) || (selNode && !isHi);
      const cls = ["pg-edge", isHi ? "is-hi" : "", isDim ? "is-dim" : ""].filter(Boolean).join(" ");
      return `<line class="${cls}" x1="${a.x}" y1="${a.y}" x2="${b.x}" y2="${b.y}" />`;
    }).join("");

    const nodesSVG = nodes.map(n => {
      const isSel = selNode && selNode.id === n.id;
      const isDim = (pgState.sourceFilter && !matchSource(n)) || (selNode && !neighborIds.has(n.id));
      const cls = ["pg-node", isSel ? "is-selected" : "", isDim ? "is-dim" : ""].filter(Boolean).join(" ");
      return `<g class="${cls}" data-node-id="${ctx.esc(n.id)}" transform="translate(${n.x}, ${n.y})">
        <circle class="pg-node-ring" r="15" />
        <circle class="pg-node-dot" r="9" fill="${ctx.esc(n.color || "#818CF8")}" />
        <text class="pg-node-label" y="24">${ctx.esc(n.label)}</text>
      </g>`;
    }).join("");

    const panelHTML = selNode ? (() => {
      const neighbors = [...neighborIds].filter(id => id !== selNode.id).map(id => nodeMap.get(id)).filter(Boolean);
      return `<aside class="pg-node-panel" id="pgNodePanel" aria-label="节点详情">
        <div class="pg-np-head">
          <span class="pg-source-dot" style="background:${ctx.esc(selNode.color || "#818CF8")}"></span>
          <span class="pg-np-title" title="${ctx.esc(selNode.label)}">${ctx.esc(selNode.label)}</span>
          <button type="button" class="pg-np-close" id="pgClosePanel" aria-label="关闭节点面板">${ctx.icon("close")}</button>
        </div>
        <div class="pg-np-body">
          <div class="pg-np-row">
            <span class="pg-np-k">来源分组</span>
            <span class="pg-np-v">${ctx.esc(selNode.source || "未分类")}</span>
          </div>
          <div class="pg-np-row">
            <span class="pg-np-k">关联出处</span>
            <span class="pg-np-v">${ctx.esc(selNode.docTitle || "项目上下文子图")}</span>
          </div>
          <div class="pg-np-row">
            <span class="pg-np-k">关联节点（${neighbors.length}）</span>
            <div class="pg-np-neighbors">
              ${neighbors.length
                ? neighbors.map(nb => `<button type="button" class="pg-np-chip" data-jump-node="${ctx.esc(nb.id)}">${ctx.esc(nb.label)}</button>`).join("")
                : `<span class="pg-np-k">无直接相连节点</span>`}
            </div>
          </div>
        </div>
        <div class="pg-np-foot">
          <button type="button" class="pg-dark-btn pg-dark-btn--primary" id="pgPanelViewSource">在项目中查看出处</button>
        </div>
      </aside>`;
    })() : "";

    return `<div class="pg-canvas-wrap" id="pgCanvasWrap">
      <div class="pg-float-bar">
        <button type="button" class="pg-dark-btn" id="pgFitBtn">适应窗口</button>
        <button type="button" class="pg-dark-btn" id="pgFocusNodeBtn">查看节点</button>
        <span class="pg-float-pill">已加载子图 · ${visibleCount} 个节点</span>
      </div>
      <aside class="pg-sources-rail" aria-label="图谱来源筛选">
        <div class="pg-sources-head">
          <span>Sources</span>
          <span class="pg-sources-count">${sources.length}</span>
          ${pgState.sourceFilter ? `<button type="button" class="pg-sources-reset" id="pgResetSource">全部</button>` : ""}
        </div>
        <div class="pg-sources-list">
          ${sources.map(s => `<button type="button" class="pg-source-item${pgState.sourceFilter === s.name ? " on" : ""}" data-source-filter="${ctx.esc(s.name)}">
            <span class="pg-source-dot" style="background:${ctx.esc(s.color)}"></span>
            <span class="pg-source-name">${ctx.esc(s.name)}</span>
            <span class="pg-source-num">${s.count}</span>
          </button>`).join("")}
        </div>
      </aside>
      <svg class="pg-svg" id="pgSvg" viewBox="${pgState.viewBox || computeFitViewBox(nodes)}" preserveAspectRatio="xMidYMid meet">
        <g class="pg-edges">${edgesSVG}</g>
        <g class="pg-nodes">${nodesSVG}</g>
      </svg>
      ${panelHTML}
    </div>`;
  }

  Pages.registerProjectTab("graph", {
    fullWidth: true,
    render(container, project, state, ctx) {
      if (pgState.lastProjectId !== project.id) {
        pgState.lastProjectId = project.id;
        pgState.selectedNodeId = "";
        pgState.sourceFilter = "";
        pgState.viewBox = "";
      }

      const graph = ensureGraphData(state, ctx, project.id);
      const isCode = pgState.mode === "code";
      const showCodeEmpty = isCode && !graph.codeEnabled;
      const { nodes, links } = getActiveGraphNodesAndLinks(graph, project);
      const selNode = !showCodeEmpty ? (nodes.find(n => n.id === pgState.selectedNodeId) || null) : null;

      const statusText = showCodeEmpty
        ? "代码索引未开启"
        : (isCode ? `代码索引已开启 · ${nodes.length} 个节点` : `已加载 ${nodes.length} 个节点 · ${links.length} 条关系`);

      const rightControls = isCode
        ? (graph.codeEnabled
            ? `${UI.buttonHTML({ id: "pgDisableCodeBtn", label: "关闭代码索引", size: "sm" })}
               ${UI.buttonHTML({ id: "pgUpdateCodeBtn", label: "更新代码图谱", variant: "primary", size: "sm" })}
               <button type="button" class="pg-icon-btn" id="pgRefreshBtn" title="刷新图谱">${ctx.icon("refresh")}</button>`
            : `<button type="button" class="pg-icon-btn" id="pgRefreshBtn" title="刷新图谱">${ctx.icon("refresh")}</button>`)
        : `${UI.buttonHTML({ id: "pgCreateToolGraphBtn", label: "创建工具图谱", size: "sm" })}
           <button type="button" class="pg-icon-btn" id="pgRefreshBtn" title="刷新图谱">${ctx.icon("refresh")}</button>`;

      const pendingRow = (!isCode && graph.pendingHint)
        ? `<div class="pg-pending-row">
            <span class="pg-pending-dot"></span>
            <span>${ctx.esc(graph.pendingHint)}</span>
            <button type="button" class="pg-pending-link" id="pgGoManageBtn">前往管理 ›</button>
          </div>`
        : "";

      const bodyHTML = showCodeEmpty
        ? `<div class="pg-empty-wrap">
            <div class="empty" id="empty">
              <div class="blocks"><i></i><i></i><i></i></div>
              <div class="pg-empty-title">代码图谱尚未开启</div>
              <div class="pg-empty-desc">从文件、函数与调用关系了解这个项目的代码结构，开启后由你手动更新</div>
              <div class="pg-empty-cta">${UI.buttonHTML({ id: "pgEnableCodeBtn", label: "开启代码图谱", variant: "primary" })}</div>
            </div>
          </div>`
        : renderGraphCanvasHTML(nodes, links, ctx);

      const footLeft = showCodeEmpty
        ? `<span>代码图谱 · 按需手动更新</span>`
        : (selNode
            ? `<span class="pg-foot-node">${ctx.esc(selNode.label)}</span><span>·</span><span>${ctx.esc(selNode.docTitle || (isCode ? "代码结构图谱" : "实体关系图谱"))}</span>`
            : `<span>${isCode ? "代码结构图谱" : "实体关系图谱"} · Cognee 官方组件</span>`);

      const footRight = (!showCodeEmpty && selNode)
        ? UI.buttonHTML({ id: "pgJumpSourceBtn", label: "查看来源记忆 ›", size: "sm" })
        : "";

      return `<div class="pg-shell">
        <div class="pg-toolbar">
          ${UI.segmentedHTML({
            id: "pgModeSeg",
            size: "sm",
            value: pgState.mode,
            options: [
              { value: "entity", label: "实体关系" },
              { value: "code", label: "代码图谱" },
            ],
          })}
          <span class="pg-status">${ctx.esc(statusText)}</span>
          <div class="pg-toolbar-right">${rightControls}</div>
        </div>
        ${pendingRow}
        ${bodyHTML}
        <div class="readfoot pg-foot">
          ${footLeft}
          <span class="spacer"></span>
          ${footRight}
        </div>
      </div>`;
    },
    bind(container, project, state, ctx) {
      container.classList.add("pg-tab-host");
      const graph = ensureGraphData(state, ctx, project.id);
      const { nodes } = getActiveGraphNodesAndLinks(graph, project);
      const selNode = nodes.find(n => n.id === pgState.selectedNodeId) || null;

      UI.bindSegmented($("#pgModeSeg", container), val => {
        pgState.mode = val;
        pgState.selectedNodeId = "";
        pgState.sourceFilter = "";
        pgState.viewBox = "";
        ctx.render(false);
      });

      const refreshBtn = $("#pgRefreshBtn", container);
      if (refreshBtn) {
        refreshBtn.onclick = () => ctx.toast(pgState.mode === "code" ? "已刷新代码图谱状态" : "已刷新实体关系图谱");
      }

      const createToolBtn = $("#pgCreateToolGraphBtn", container);
      if (createToolBtn) {
        createToolBtn.onclick = () => {
          UI.openDialog({
            title: "创建工具记录关系图谱？",
            size: 480,
            cancelText: "取消",
            confirmText: "开始创建",
            body: `<p>将为项目 <strong>${ctx.esc(project.name)}</strong> 的工具调用与会话产出建立关系图谱。</p><p>该操作会调用当前激活的模型配置并消耗模型额度，构建任务在后台异步执行，不影响正常阅读与召回。</p>`,
            onConfirm() {
              ctx.toast(`已提交「${project.name}」工具图谱构建任务`);
            },
          });
        };
      }

      const goManageBtn = $("#pgGoManageBtn", container);
      if (goManageBtn) {
        goManageBtn.onclick = () => ctx.go("project", { project: project.id, tab: "manage" });
      }

      const enableCodeBtn = $("#pgEnableCodeBtn", container);
      if (enableCodeBtn) {
        enableCodeBtn.onclick = () => {
          graph.codeEnabled = true;
          pgState.selectedNodeId = "";
          pgState.sourceFilter = "";
          ctx.render(false);
          ctx.toast(`已为 ${project.name} 开启代码图谱`);
        };
      }

      const disableCodeBtn = $("#pgDisableCodeBtn", container);
      if (disableCodeBtn) {
        disableCodeBtn.onclick = () => {
          UI.openDialog({
            title: "关闭此项目的代码索引？",
            size: 480,
            danger: true,
            cancelText: "取消",
            confirmText: "关闭代码索引",
            body: `<p>关闭后将隐藏「${ctx.esc(project.name)}」的代码图谱视图，不影响已有实体关系记忆；随时可重新开启。</p>`,
            onConfirm() {
              graph.codeEnabled = false;
              pgState.selectedNodeId = "";
              pgState.sourceFilter = "";
              ctx.render(false);
              ctx.toast("已关闭代码索引");
            },
          });
        };
      }

      const updateCodeBtn = $("#pgUpdateCodeBtn", container);
      if (updateCodeBtn) {
        updateCodeBtn.onclick = () => {
          ctx.toast(`正在更新「${project.name}」代码图谱索引…`);
        };
      }

      // 暗色画布交互
      const svg = $("#pgSvg", container);
      if (svg) {
        svg.onclick = e => {
          const nodeEl = e.target.closest("[data-node-id]");
          if (nodeEl) {
            const id = nodeEl.dataset.nodeId;
            pgState.selectedNodeId = pgState.selectedNodeId === id ? "" : id;
            ctx.render(false);
          } else if (pgState.selectedNodeId) {
            pgState.selectedNodeId = "";
            ctx.render(false);
          }
        };
      }

      $$("[data-source-filter]", container).forEach(btn => {
        btn.onclick = () => {
          const src = btn.dataset.sourceFilter;
          pgState.sourceFilter = pgState.sourceFilter === src ? "" : src;
          ctx.render(false);
        };
      });

      const resetSrcBtn = $("#pgResetSource", container);
      if (resetSrcBtn) {
        resetSrcBtn.onclick = () => {
          pgState.sourceFilter = "";
          ctx.render(false);
        };
      }

      const fitBtn = $("#pgFitBtn", container);
      if (fitBtn) {
        fitBtn.onclick = () => {
          pgState.viewBox = "";
          pgState.sourceFilter = "";
          ctx.render(false);
          ctx.toast("已适应窗口范围");
        };
      }

      const focusBtn = $("#pgFocusNodeBtn", container);
      if (focusBtn) {
        focusBtn.onclick = () => {
          const target = selNode || nodes[0];
          if (!target) return;
          pgState.selectedNodeId = target.id;
          pgState.viewBox = `${Math.round(target.x - 260)} ${Math.round(target.y - 140)} 520 280`;
          ctx.render(false);
        };
      }

      const closePanel = $("#pgClosePanel", container);
      if (closePanel) {
        closePanel.onclick = () => {
          pgState.selectedNodeId = "";
          ctx.render(false);
        };
      }

      $$("[data-jump-node]", container).forEach(chip => {
        chip.onclick = () => {
          pgState.selectedNodeId = chip.dataset.jumpNode;
          ctx.render(false);
        };
      });

      const panelViewSource = $("#pgPanelViewSource", container);
      if (panelViewSource && selNode) {
        panelViewSource.onclick = () => jumpToSourceMemory(project, selNode, state, ctx);
      }

      const jumpSourceBtn = $("#pgJumpSourceBtn", container);
      if (jumpSourceBtn && selNode) {
        jumpSourceBtn.onclick = () => jumpToSourceMemory(project, selNode, state, ctx);
      }

      bindEmptyParallax(container);
    },
  });
})();
