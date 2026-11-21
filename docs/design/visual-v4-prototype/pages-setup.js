// 首次启动向导（Q7）：左步骤栏弹簧选中块+完成勾、右侧依赖项分组列表、方块舞台动效、底部固定上一步/继续、确认安装与环境详情用 v4Dialog
(() => {
  const Pages = window.MemlanePages;
  const UI = window.MemlaneUI;
  const D = window.DATA || window.MEMLANE_DATA;
  if (!Pages || !UI || !D) return;

  const $ = (s, el = document) => el.querySelector(s);
  const $$ = (s, el = document) => [...el.querySelectorAll(s)];

  /* ---------- 遵循 initial_setup_layout.go 的 3 步流程契约 ---------- */
  const STEPS = [
    {
      id: "env",
      title: "准备环境",
      sub: "复用已有依赖",
      heading: "准备好你的本地记忆环境",
      lead: "记忆留在你的电脑上，已有的兼容组件直接复用。",
    },
    {
      id: "agents",
      title: "连接 Agent",
      sub: "自动采集与召回",
      heading: "连接你正在使用的 Agent",
      lead: "接入后，Agent 可以采集经验，并在后续任务中找回记忆。",
    },
    {
      id: "processing",
      title: "配置记忆提炼",
      sub: "Agent 或模型 API",
      heading: "选择如何提炼记忆",
      lead: "连接 Agent 与提炼记忆分别配置，使用已有账号或自配模型。",
    },
  ];

  const setupState = {
    installing: false,
    installStage: "",
    envReady: false,
    startingApp: false,
    completedSteps: new Set(),
    sourceMode: "cli",
    selectedCli: "gemini",
    selectedProvider: "openai-compat",
    deps: [
      { group: "运行基础", groupDesc: "Python 与运行依赖", id: "uv", name: "uv", version: "0.7.8", status: "ok", size: "14 MiB", source: "已检测到本机环境", dest: "~/.local/bin/uv", purpose: "受管 Python 依赖解析与隔离安装" },
      { group: "运行基础", groupDesc: "Python 与运行依赖", id: "python", name: "Python 3.12", version: "3.12.9", status: "ok", size: "42 MiB", source: "已检测到本机环境", dest: "~/.local/share/uv/python/cpython-3.12.9", purpose: "运行官方记忆引擎（Cognee）与接入网关" },
      { group: "运行基础", groupDesc: "Python 与运行依赖", id: "runtime", name: "运行虚拟环境", version: "venv · 3.12", status: "ok", size: "180 MiB", source: "受管隔离环境", dest: ".local/daily/venv", purpose: "保持与系统 Python 隔离" },
      { group: "本地记忆引擎", groupDesc: "提炼与检索", id: "cognee", name: "Cognee 源码", version: "1.6.7 (ec6f408)", status: "missing", size: "下载 18.4 MiB · 占用 62.0 MiB", source: "官方固定版本归档（启动前 SHA256 校验）", dest: ".local/daily/engine-components/cognee-1.6.7", purpose: "本地知识正文、LanceDB 向量与 Ladybug 图谱唯一数据源" },
      { group: "本地记忆引擎", groupDesc: "提炼与检索", id: "embedding", name: "向量模型", version: "BAAI/bge-small-zh-v1.5", status: "missing", size: "下载 91.2 MiB · 占用 96.0 MiB", source: "HuggingFace 镜像缓存", dest: ".local/daily/model-cache/bge-small-zh-v1.5", purpose: "中文记忆本地语义检索向量化" },
      { group: "Agent 接入", groupDesc: "采集与召回", id: "plugin", name: "受管 Hook 插件", version: "memlane-hook 1.6.7", status: "ok", size: "2.1 MiB", source: "内置受管插件包", dest: ".local/daily/plugins", purpose: "为 Coding Agents 提供非侵入式采集与召回接口" },
    ],
    agents: [
      { id: "gemini", name: "Antigravity", version: "0.9.4", connected: true, note: "已检测到配置目录 ~/.gemini/antigravity-cli" },
      { id: "codex", name: "Codex", version: "0.1.2504", connected: true, note: "已检测到配置目录 ~/.codex" },
      { id: "claude", name: "Claude Code", version: "1.0.18", connected: false, note: "已检测到本机安装，点击连接写入受管凭据" },
      { id: "cursor", name: "Cursor", version: "0.49.2", connected: false, note: "已检测到本机安装，连接后可按项目配置权限" },
    ],
  };

  // URL 参数支持演示安装中方块舞台（?page=setup&installing=1）
  if (new URLSearchParams(location.search).get("installing") === "1") {
    setupState.installing = true;
    setupState.installStage = "正在安装 Cognee 1.6.7 与向量模型…";
    setupState.deps.forEach(d => { if (d.status === "missing") d.status = "installing"; });
  }

  let installTimers = [];

  function clearInstallTimers() {
    while (installTimers.length) clearTimeout(installTimers.pop());
  }

  function missingDeps() {
    return setupState.deps.filter(d => d.status !== "ok");
  }

  function statusLabel(st) {
    if (st === "ok") return { text: "可用", cls: "is-ok", dot: "ok" };
    if (st === "installing") return { text: "正在安装", cls: "", dot: "run" };
    return { text: "需要安装", cls: "is-warn", dot: "warn" };
  }

  function startInstallFlow(ctx) {
    clearInstallTimers();
    setupState.installing = true;
    setupState.installStage = "正在校验并安装 Cognee 1.6.7 与向量模型…";
    setupState.deps.forEach(d => {
      if (d.status !== "ok") d.status = "installing";
    });
    ctx.render(false);

    const t1 = ctx.isReducedMotion() ? 240 : 1100;
    const t2 = ctx.isReducedMotion() ? 450 : 2100;
    installTimers.push(setTimeout(() => {
      if (!setupState.installing) return;
      setupState.installStage = "正在启动本地记忆引擎（Cognee）与接入网关…";
      const sub = $("#setupStageTitle");
      if (sub) sub.textContent = setupState.installStage;
    }, t1));

    installTimers.push(setTimeout(() => {
      if (!setupState.installing) return;
      clearInstallTimers();
      setupState.installing = false;
      setupState.installStage = "";
      setupState.envReady = true;
      setupState.deps.forEach(d => { d.status = "ok"; });
      setupState.completedSteps.add(0);
      if (ctx.state.page === "setup") ctx.render(false);
      ctx.toast("本地记忆环境已准备就绪");
    }, t2));
  }

  function stopInstallFlow(ctx) {
    clearInstallTimers();
    setupState.installing = false;
    setupState.installStage = "";
    setupState.deps.forEach(d => {
      if (d.status === "installing") d.status = "missing";
    });
    ctx.render(false);
    ctx.toast("已停止安装，已完成的准备会保留");
  }

  /* ---------- v4Dialog：确认安装缺少的依赖（640） ---------- */
  function openInstallConfirmDialog(ctx) {
    const missing = missingDeps();
    if (!missing.length) {
      ctx.toast("所有环境依赖均已就绪");
      return;
    }
    UI.openDialog({
      title: "确认安装缺少的依赖",
      size: 640,
      cancelText: "暂不安装",
      confirmText: "确认安装",
      hint: "已有环境和目录保持原样，不修改系统配置",
      body: `
        <p style="font-size:var(--fs-footnote);color:var(--text-2)">仅安装以下 ${missing.length} 个缺失组件至 Memlane 受管目录：</p>
        <div>
          ${missing.map(it => `
            <div class="setup-plan-item">
              <div class="setup-plan-head">
                <span>${ctx.esc(it.name)} · ${ctx.esc(it.version)}</span>
                <span class="setup-plan-meta">${ctx.esc(it.size)}</span>
              </div>
              <div class="setup-plan-desc">${ctx.esc(it.purpose)}</div>
              <div class="setup-plan-meta">来源：${ctx.esc(it.source)}</div>
              <div class="setup-plan-meta">位置：${ctx.esc(it.dest)}</div>
            </div>
          `).join("")}
        </div>
      `,
      onConfirm() {
        startInstallFlow(ctx);
      },
      onCancel() {
        ctx.toast("暂不安装，已有环境和准备进度会保留");
      },
    });
  }

  /* ---------- v4Dialog：环境详情（640） ---------- */
  function openEnvDetailsDialog(ctx) {
    UI.openDialog({
      title: "环境详情",
      size: 640,
      cancelText: "关闭",
      confirmText: "复制诊断材料",
      hint: "路径与校验结果已脱敏，不含任何密钥或聊天内容",
      body: `
        ${UI.groupHTML({
          title: "已检测组件路径",
          rows: setupState.deps.map(d => {
            const st = statusLabel(d.status);
            return UI.rowHTML({
              leading: `<span class="dot ${st.dot}"></span>`,
              title: `${ctx.esc(d.name)} <span class="set-ver">${ctx.esc(d.version)}</span>`,
              desc: d.dest,
              descMono: true,
              value: st.text,
            });
          }),
        })}
        <label class="set-field" style="margin-top:var(--s2)">
          <span class="set-field-label">指定已有 Python / 模型缓存目录（可选）</span>
          <input class="v4-input" id="setupManualPath" placeholder="留空表示使用默认检测路径" />
        </label>
      `,
      onConfirm() {
        if (navigator.clipboard && navigator.clipboard.writeText) {
          navigator.clipboard.writeText(D.serviceStatus.diagnosticText).catch(() => {});
        }
        ctx.toast("脱敏环境诊断材料已复制");
      },
    });
  }

  /* ---------- 渲染各步骤右侧内容 ---------- */
  function renderStep0Env(ctx) {
    const missing = missingDeps();
    const groups = [];
    const seen = new Map();
    for (const d of setupState.deps) {
      if (!seen.has(d.group)) {
        const g = { title: d.group, desc: d.groupDesc, items: [] };
        seen.set(d.group, g);
        groups.push(g);
      }
      seen.get(d.group).items.push(d);
    }

    const stageBanner = setupState.installing ? `
      <div class="setup-stage-banner">
        ${ctx.stageHTML(true)}
        <div class="setup-stage-info">
          <div class="setup-stage-title" id="setupStageTitle">${ctx.esc(setupState.installStage || "正在准备依赖组件…")}</div>
          <div class="setup-stage-sub">请稍候，完成后将自动校验上游 SHA256 并拉起本地回环服务。</div>
        </div>
        ${UI.buttonHTML({ id: "btnStopInstall", label: "停止安装", size: "sm" })}
      </div>` : `
      <div class="setup-summary-row">
        <span class="setup-summary-stat">${missing.length ? `尚缺 ${missing.length} 个组件` : "环境已就绪"}</span>
        <span class="setup-summary-note">${missing.length ? "先检测已有环境，仅安装你确认的缺失组件。" : "全部 6 项核心依赖已通过校验，可继续连接 Agent。"}</span>
        ${UI.buttonHTML({ id: "btnRedetectEnv", label: "重新检测", size: "sm", icon: ctx.icon("refresh") })}
        ${missing.length
          ? UI.buttonHTML({ id: "btnInstallMissing", label: "安装缺少的依赖", variant: "primary", size: "sm" })
          : ""}
      </div>`;

    return `
      ${stageBanner}
      ${groups.map(g => UI.groupHTML({
        title: g.title,
        desc: g.desc,
        rows: g.items.map(d => {
          const st = statusLabel(d.status);
          return UI.rowHTML({
            leading: `<span class="dot ${st.dot}"></span>`,
            title: ctx.esc(d.name),
            desc: d.version,
            descMono: true,
            control: `<span class="set-state-text ${st.cls}">${st.text}</span>`,
          });
        }),
      })).join("")}
    `;
  }

  function renderStep1Agents(ctx) {
    const connectedCount = setupState.agents.filter(a => a.connected).length;
    return UI.groupHTML({
      title: "本机 Coding Agents",
      desc: `已连接 ${connectedCount} / ${setupState.agents.length}`,
      rows: setupState.agents.map(a => UI.rowHTML({
        leading: ctx.logo(a.id, 22),
        title: `${ctx.esc(a.name)} <span class="set-ver">${ctx.esc(a.version)}</span>`,
        desc: a.note,
        control: a.connected
          ? `<span class="set-badge set-badge--ok">已连接</span>`
          : UI.buttonHTML({ label: "连接", variant: "primary", size: "sm", attrs: `data-setup-connect="${ctx.esc(a.id)}"` }),
      })),
    });
  }

  function renderStep2Processing(ctx) {
    const mode = setupState.sourceMode;
    const rows = [
      UI.rowHTML({
        title: "提炼来源",
        desc: mode === "cli"
          ? "让 Codex、Gemini 等已连接的 Agent 用它自己的模型和额度提炼记忆，无需填写 API Key"
          : "填写 OpenAI 兼容或 Ollama 等接口，由 Memlane 直接调用",
        control: UI.segmentedHTML({
          id: "setupSourceSeg",
          options: [
            { value: "cli", label: "用已连接的 Agent 提炼" },
            { value: "provider", label: "直接调用模型 API" },
          ],
          value: mode,
        }),
      }),
    ];

    if (mode === "cli") {
      for (const a of setupState.agents) {
        const picked = setupState.selectedCli === a.id;
        rows.push(UI.rowHTML({
          leading: ctx.logo(a.id, 20),
          title: ctx.esc(a.name),
          desc: a.connected ? "已绑定 CLI · 可立即用于提炼记忆" : "尚未连接，请先在上一步连接",
          control: picked
            ? `<span class="set-badge set-badge--ok">已选择</span>`
            : (a.connected ? UI.buttonHTML({ label: "选择", size: "sm", attrs: `data-pick-cli="${ctx.esc(a.id)}"` }) : `<span class="set-state-text">未连接</span>`),
        }));
      }
    } else {
      for (const p of D.settings.providers) {
        const picked = setupState.selectedProvider === p.id;
        rows.push(UI.rowHTML({
          title: `${ctx.esc(p.name)} <span class="set-ver">${ctx.esc(p.model)}</span>`,
          desc: p.baseUrl,
          descMono: true,
          control: picked
            ? `<span class="set-badge set-badge--ok">已选择</span>`
            : UI.buttonHTML({ label: "选择", size: "sm", attrs: `data-pick-prov="${ctx.esc(p.id)}"` }),
        }));
      }
    }

    const startingBanner = setupState.startingApp ? `
      <div class="setup-stage-banner">
        ${ctx.stageHTML(true)}
        <div class="setup-stage-info">
          <div class="setup-stage-title">正在启用记忆提炼配置并验证日常服务…</div>
          <div class="setup-stage-sub">等待服务回执确认后自动进入总览页。</div>
        </div>
      </div>` : "";

    return `
      ${startingBanner}
      ${UI.groupHTML({ title: "记忆提炼方式", desc: "以后可随时在「设置 › 记忆提炼」切换", rows })}
    `;
  }

  function placeStepInd(mainEl) {
    const ind = $("#setupStepInd", mainEl);
    const on = $(".setup-step-btn.on", mainEl);
    if (!ind || !on) return;
    ind.style.setProperty("--y", on.offsetTop + "px");
    ind.style.setProperty("--h", on.offsetHeight + "px");
  }

  Pages.register({
    id: "setup",
    navLabel: "首次启动",
    hideSidebar: true,
    render(mainEl, state, ctx) {
      const stepIdx = Math.max(0, Math.min(STEPS.length - 1, Number(state.setupStep) || 0));
      state.setupStep = stepIdx;
      const cur = STEPS[stepIdx];
      const missing = missingDeps();

      // 步骤 1 或 2 时默认标记前序步骤已完成，便于评审直接通过 ?page=setup&step=1 看到完成勾
      if (stepIdx >= 1) setupState.completedSteps.add(0);
      if (stepIdx >= 2) setupState.completedSteps.add(1);

      let stepBody = "";
      if (stepIdx === 0) stepBody = renderStep0Env(ctx);
      else if (stepIdx === 1) stepBody = renderStep1Agents(ctx);
      else stepBody = renderStep2Processing(ctx);

      const primaryLabel = stepIdx === 0
        ? (missing.length ? "安装并继续" : "继续")
        : (stepIdx === 1 ? "继续" : "启用并完成");

      mainEl.innerHTML = `
        <div class="setup-shell">
          <aside class="setup-sidebar">
            <div class="traffic"><i></i><i></i><i></i></div>
            <div class="brand">
              <svg viewBox="0 0 20 20" width="18" height="18" aria-hidden="true"><rect x="2" y="2" width="7" height="7" rx="2" fill="var(--accent)"/><rect x="11" y="2" width="7" height="7" rx="2" fill="var(--accent)" opacity=".55"/><rect x="2" y="11" width="7" height="7" rx="2" fill="var(--accent)" opacity=".55"/><rect x="11" y="11" width="7" height="7" rx="2" fill="var(--accent)" opacity=".25"/></svg>
              Memlane
            </div>
            <nav class="setup-steps" id="setupSteps">
              <span class="setup-step-ind" id="setupStepInd"></span>
              ${STEPS.map((st, idx) => {
                const isOn = idx === stepIdx;
                const isDone = setupState.completedSteps.has(idx) && !isOn;
                return `<button type="button" class="setup-step-btn${isOn ? " on" : ""}${isDone ? " is-done" : ""}" data-setup-step="${idx}">
                  <span class="setup-step-num">${isDone ? ctx.icon("check") : (idx + 1)}</span>
                  <span class="setup-step-text">
                    <span class="setup-step-title">${ctx.esc(st.title)}</span>
                    <span class="setup-step-sub">${ctx.esc(st.sub)}</span>
                  </span>
                </button>`;
              }).join("")}
            </nav>
            <div class="setup-sidebar-foot">以后也能在设置中随时调整所有选项。</div>
          </aside>

          <section class="setup-main">
            <div class="setup-topbar" id="titlebar">
              ${UI.buttonHTML({ id: "btnSetupPreviewStage", label: setupState.installing ? "停止安装演示" : "演示安装动效", size: "sm" })}
              ${UI.buttonHTML({ id: "btnSetupExit", label: "返回主界面", variant: "ghost", size: "sm" })}
            </div>
            <div class="setup-body scroll" id="setupBody">
              <div class="v4-group-wrap settings-pane-enter">
                <header class="setup-header">
                  <span class="setup-eyebrow">开始使用 / 0${stepIdx + 1}</span>
                  <h1 class="setup-heading">${ctx.esc(cur.heading)}</h1>
                  <p class="setup-lead">${ctx.esc(cur.lead)}</p>
                </header>
                ${stepBody}
              </div>
            </div>
            <footer class="setup-foot">
              ${UI.buttonHTML({ id: "btnSetupEnvDetails", label: "环境详情", size: "sm" })}
              <span class="setup-foot-hint">${stepIdx === 0 ? "只安装你确认的组件" : "可随时返回上一步核对"}</span>
              <span class="spacer"></span>
              ${UI.buttonHTML({ id: "btnSetupPrev", label: "上一步", disabled: stepIdx === 0 || setupState.installing || setupState.startingApp })}
              ${UI.buttonHTML({ id: "btnSetupNext", label: primaryLabel, variant: "primary", disabled: setupState.installing || setupState.startingApp })}
            </footer>
          </section>
        </div>
      `;
    },
    bind(mainEl, state, ctx) {
      placeStepInd(mainEl);
      requestAnimationFrame(() => placeStepInd(mainEl));

      $$("[data-setup-step]", mainEl).forEach(b => {
        b.onclick = () => {
          if (setupState.installing || setupState.startingApp) return;
          state.setupStep = Number(b.dataset.setupStep);
          ctx.render(false);
        };
      });

      const btnExit = $("#btnSetupExit", mainEl);
      if (btnExit) btnExit.onclick = () => ctx.go("overview");

      const btnPreviewStage = $("#btnSetupPreviewStage", mainEl);
      if (btnPreviewStage) {
        btnPreviewStage.onclick = () => {
          state.setupStep = 0;
          if (setupState.installing) {
            stopInstallFlow(ctx);
          } else {
            setupState.deps.forEach(d => {
              if (d.id === "cognee" || d.id === "embedding") d.status = "missing";
            });
            startInstallFlow(ctx);
          }
        };
      }

      const btnEnvDetails = $("#btnSetupEnvDetails", mainEl);
      if (btnEnvDetails) btnEnvDetails.onclick = () => openEnvDetailsDialog(ctx);

      const btnInstallMissing = $("#btnInstallMissing", mainEl);
      if (btnInstallMissing) btnInstallMissing.onclick = () => openInstallConfirmDialog(ctx);

      const btnStopInstall = $("#btnStopInstall", mainEl);
      if (btnStopInstall) btnStopInstall.onclick = () => stopInstallFlow(ctx);

      const btnRedetect = $("#btnRedetectEnv", mainEl);
      if (btnRedetect) btnRedetect.onclick = () => ctx.toast("已完成本地依赖重新检测");

      $$("[data-setup-connect]", mainEl).forEach(b => {
        b.onclick = () => {
          const ag = setupState.agents.find(x => x.id === b.dataset.setupConnect);
          if (!ag) return;
          ag.connected = true;
          ag.note = "已写入受管接入配置，新建会话后生效";
          ctx.render(false);
          ctx.toast(`已连接 ${ag.name}`);
        };
      });

      const sourceSeg = $("#setupSourceSeg", mainEl);
      if (sourceSeg) {
        UI.bindSegmented(sourceSeg, val => {
          setupState.sourceMode = val;
          ctx.render(false);
        });
      }
      $$("[data-pick-cli]", mainEl).forEach(b => {
        b.onclick = () => {
          setupState.selectedCli = b.dataset.pickCli;
          ctx.render(false);
        };
      });
      $$("[data-pick-prov]", mainEl).forEach(b => {
        b.onclick = () => {
          setupState.selectedProvider = b.dataset.pickProv;
          ctx.render(false);
        };
      });

      const btnPrev = $("#btnSetupPrev", mainEl);
      if (btnPrev) {
        btnPrev.onclick = () => {
          if (state.setupStep > 0) {
            state.setupStep -= 1;
            ctx.render(false);
          }
        };
      }

      const btnNext = $("#btnSetupNext", mainEl);
      if (btnNext) {
        btnNext.onclick = () => {
          const step = state.setupStep;
          if (step === 0) {
            if (missingDeps().length) {
              openInstallConfirmDialog(ctx);
              return;
            }
            setupState.completedSteps.add(0);
            state.setupStep = 1;
            ctx.render(false);
          } else if (step === 1) {
            setupState.completedSteps.add(1);
            state.setupStep = 2;
            ctx.render(false);
          } else {
            setupState.startingApp = true;
            setupState.completedSteps.add(2);
            ctx.render(false);
            setTimeout(() => {
              setupState.startingApp = false;
              ctx.go("overview", { force: true });
              ctx.toast("日常服务已就绪，欢迎使用 Memlane");
            }, ctx.isReducedMotion() ? 200 : 1000);
          }
        };
      }
    },
  });
})();
