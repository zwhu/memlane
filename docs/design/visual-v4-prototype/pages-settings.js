// 设置页（Q2 / Q6）与项目「管理」标签（Q5）
(() => {
  const Pages = window.MemlanePages;
  const UI = window.MemlaneUI;
  const D = window.DATA || window.MEMLANE_DATA;
  if (!Pages || !UI || !D) return;

  const $ = (s, el = document) => el.querySelector(s);
  const $$ = (s, el = document) => [...el.querySelectorAll(s)];

  /* ---------- 本地可变原型状态 ---------- */
  const cfg = {
    sourceMode: D.settings.sourceMode || "cli", // "cli" | "provider"
    activeMode: D.settings.sourceMode || "cli",
    activeCliAgent: D.settings.activeCliAgent || "gemini",
    activeProvider: D.settings.activeProvider || "openai-compat",
    autoOrganize: !!D.settings.autoOrganize,
    autoRecall: !!D.settings.autoRecall,
    toolGraphGlobal: false,
    sessionDateLock: !!D.settings.sessionDateLock,
    cliAgents: D.settings.cliAgents.map((a, idx) => ({
      ...a,
      version: ["0.9.4", "0.1.2504", "1.0.18", "0.49.2", "0.2.14"][idx] || "1.0.0",
      profileDir: [
        "~/.gemini/antigravity-cli",
        "~/.codex",
        "~/.claude",
        "~/.cursor",
        "~/.config/opencode",
      ][idx] || "~/.config/" + a.id,
      pluginVer: a.connected ? "memlane-hook 1.6.7" : "未安装",
    })),
    providers: D.settings.providers.map(p => ({ ...p, protocol: p.id === "ollama" ? "Ollama" : "OpenAI 兼容", maxTokens: p.id === "ollama" ? "" : "16384", validated: !!p.configured })),
    expandedAgentDirs: {},
    agentsDetecting: false,
    // 服务页状态（Q6）
    serviceDetecting: false,
    diagOpen: false,
    diagRaw: false,
    // 项目管理状态（Q5）
    access: JSON.parse(JSON.stringify(D.projectAccess || {})),
    accessAttemptCount: 0,
    projectToolGraph: {
      memlane: "inherit", // "inherit" | "on" | "off"
      "item-fe": "on",
      "daily-cli": "inherit",
      "dcim-fe": "off",
    },
    projectNotices: {
      memlane: [
        { id: "n-hook", title: "Antigravity Hook 重试已恢复", tag: "已恢复", level: "warn", desc: "19:11 写入回执超时后自动重试成功，原会话消息完整保留。", ack: false },
        { id: "n-old", title: "历史会话前缀兼容归档", tag: "已知晓", level: "normal", desc: "codex_antigravity- 前缀的历史记录已按旧规则归档，不影响新会话。", ack: true },
      ],
    },
    noticeHistoryOpen: {},
    queuedDocs: {
      memlane: [
        { id: "q1", title: "V4 设置页与分组列表交互评审会话", date: "今天 19:28", status: "已保存 · 排队提炼" },
        { id: "q2", title: "supervisor 日志截尾单测补充", date: "今天 18:54", status: "已保存 · 排队提炼" },
      ],
    },
  };

  // URL 参数支持演示「检测中」骨架行（?page=settings&tab=service&detecting=1）
  const initParams = new URLSearchParams(location.search);
  if (initParams.get("detecting") === "1") cfg.serviceDetecting = true;
  if (initParams.get("diag") === "1") cfg.diagOpen = true;

  const SETTINGS_TABS = [
    { id: "processing", label: "记忆处理" },
    { id: "agents", label: "Agent 与模型" },
    { id: "service", label: "服务" },
  ];

  const ENV_ITEMS = [
    { id: "uv", name: "uv", version: "0.7.8", path: "~/.local/bin/uv", state: "ok", statusText: "可用" },
    { id: "python", name: "Python", version: "3.12.9", path: "~/.local/share/uv/python/cpython-3.12.9", state: "ok", statusText: "可用" },
    { id: "cognee", name: "Cognee 源码", version: "1.6.7 (ec6f408) · SHA256 已校验", path: ".local/daily/engine-components/cognee-1.6.7", state: "ok", statusText: "可用" },
    { id: "embedding", name: "向量模型", version: "BAAI/bge-small-zh-v1.5", path: ".local/daily/model-cache/bge-small-zh-v1.5", state: "ok", statusText: "可用" },
    { id: "plugin", name: "Agent 插件", version: "memlane-hook 1.6.7", path: ".local/daily/plugins", state: "ok", statusText: "可用" },
  ];

  const AGENT_ORDER = ["gemini", "codex", "claude", "cursor", "opencode", "pi"];
  const ACCESS_DESC = {
    rw: "读取、写入并维护该项目记忆（含删除）",
    ro: "仅允许检索与读取已有记忆，不可写入",
    none: "禁止访问此项目的记忆与原始记录",
  };

  function currentActiveSummary() {
    if (cfg.activeMode === "cli") {
      const a = cfg.cliAgents.find(x => x.id === cfg.activeCliAgent) || cfg.cliAgents[0];
      return `当前使用：已连接的 Agent · ${a.name}`;
    }
    const p = cfg.providers.find(x => x.id === cfg.activeProvider) || cfg.providers[0];
    return `当前使用：模型 API · ${p ? `${p.name} (${p.model})` : "未配置"}`;
  }

  /* ==========================================================================
   *  1. 全局设置页（Q2 / Q6）
   * ========================================================================== */
  function renderProcessingTab(ctx) {
    const mode = cfg.sourceMode;
    const candidateRows = [];

    // 来源选择行（保留 Segmented 契约）
    candidateRows.push(UI.rowHTML({
      title: "处理来源",
      desc: mode === "cli"
        ? "让 Codex、Gemini 等已连接的 Agent 用它自己的模型和额度整理记忆，无需填写 API Key"
        : "填写 OpenAI 兼容或 Ollama 等接口，由 Memlane 直接调用",
      control: UI.segmentedHTML({
        id: "setSourceSeg",
        options: [
          { value: "cli", label: "用已连接的 Agent 整理" },
          { value: "provider", label: "直接调用模型 API" },
        ],
        value: mode,
      }),
    }));

    if (mode === "cli") {
      for (const a of cfg.cliAgents) {
        const isActive = cfg.activeMode === "cli" && cfg.activeCliAgent === a.id;
        let ctrl = "";
        if (isActive) {
          ctrl = `<span class="set-badge set-badge--ok">使用中</span>`;
        } else if (!a.connected) {
          ctrl = `<span class="set-state-text">CLI 未绑定</span>`;
        } else {
          ctrl = UI.buttonHTML({ label: "切换", size: "sm", attrs: `data-handoff-cli="${ctx.esc(a.id)}"` });
        }
        candidateRows.push(UI.rowHTML({
          leading: ctx.logo(a.id, 20),
          title: `${ctx.esc(a.name)}`,
          desc: a.model,
          control: ctrl,
        }));
      }
    } else {
      for (const p of cfg.providers) {
        const isActive = cfg.activeMode === "provider" && cfg.activeProvider === p.id;
        let ctrl = "";
        if (isActive) {
          ctrl = `<span class="set-badge set-badge--ok">使用中</span>`;
        } else if (!p.validated) {
          ctrl = `<span class="set-state-text">未验证</span>${UI.buttonHTML({ label: "去配置", size: "sm", attrs: `data-goto-agents="1"` })}`;
        } else {
          ctrl = UI.buttonHTML({ label: "切换", size: "sm", attrs: `data-handoff-provider="${ctx.esc(p.id)}"` });
        }
        candidateRows.push(UI.rowHTML({
          title: `${ctx.esc(p.name)} <span class="set-ver">${ctx.esc(p.model)}</span>`,
          desc: p.baseUrl,
          descMono: true,
          control: ctrl,
        }));
      }
    }

    candidateRows.push(UI.rowHTML({
      id: "setGoAgentsRow",
      title: "管理 Agent 与模型",
      desc: "连接或检查本机 Coding Agents、添加与编辑模型 Provider",
      clickable: true,
      chevron: true,
    }));

    const behaviorGroup = UI.groupHTML({
      title: "整理与记忆策略",
      rows: [
        UI.rowHTML({
          title: "自动整理新会话",
          desc: "收到新材料后自动提炼记忆；关闭不取消已经运行的整理任务",
          control: UI.switchHTML({ id: "swAutoOrganize", checked: cfg.autoOrganize }),
        }),
        UI.rowHTML({
          title: "自动召回记忆",
          desc: "Agent 对话时由外置网关自动合并项目记忆与通用记忆",
          control: UI.switchHTML({ id: "swAutoRecall", checked: cfg.autoRecall }),
        }),
        UI.rowHTML({
          title: "自动构建关系图谱",
          desc: "开启后消耗模型额度；关闭不影响消息保存与记忆提炼，项目可单独覆盖",
          control: UI.switchHTML({ id: "swToolGraphGlobal", checked: cfg.toolGraphGlobal }),
        }),
        UI.rowHTML({
          title: "锁定最早会话日期",
          desc: "优先按外置 provenance_store 锁定日期，防止重复提炼导致时间漂移",
          control: UI.switchHTML({ id: "swDateLock", checked: cfg.sessionDateLock }),
        }),
        UI.rowHTML({
          title: "记忆语言",
          desc: "中文提炼提示词仅作用于受管副本，不修改上游 Cognee 源码",
          value: "简体中文",
        }),
      ],
    });

    const appearanceGroup = UI.groupHTML({
      title: "外观",
      rows: [
        UI.rowHTML({
          title: "外观模式",
          desc: "可与侧栏右下角外观按钮实时同步切换",
          control: UI.segmentedHTML({
            id: "setAppearanceSeg",
            options: [
              { value: "system", label: "跟随系统" },
              { value: "light", label: "浅色" },
              { value: "dark", label: "深色" },
            ],
            value: ctx.getThemeChoice(),
          }),
        }),
      ],
    });

    return `<div class="v4-group-wrap settings-pane-enter">
      ${UI.groupHTML({ title: "处理方式", desc: currentActiveSummary(), rows: candidateRows })}
      ${behaviorGroup}
      ${appearanceGroup}
    </div>`;
  }

  function renderAgentsTab(ctx) {
    const agentRowsHTML = cfg.agentsDetecting
      ? UI.skeletonGroupHTML({ count: 4, lines: 2, avatar: true, right: true })
      : cfg.cliAgents.map(a => {
        const expanded = !!cfg.expandedAgentDirs[a.id];
        const statusClass = a.connected ? "is-ok" : "is-fail";
        const statusText = a.connected ? "已连接" : "未接入";
        const row = UI.rowHTML({
          leading: ctx.logo(a.id, 22),
          title: `${ctx.esc(a.name)} <span class="set-ver">${ctx.esc(a.version)}</span>`,
          desc: `<span class="set-dot-text"><span class="dot ${a.hookState}"></span><span>${ctx.esc(a.hookNote)}</span></span>`,
          control: `
            <span class="set-state-text ${statusClass}">${statusText}</span>
            ${UI.buttonHTML({ label: expanded ? "收起目录" : "配置目录", size: "sm", attrs: `data-toggle-dir="${ctx.esc(a.id)}"` })}
            ${a.connected
              ? UI.buttonHTML({ label: "核对", size: "sm", attrs: `data-verify-agent="${ctx.esc(a.id)}"` })
              : UI.buttonHTML({ label: "连接", variant: "primary", size: "sm", attrs: `data-connect-agent="${ctx.esc(a.id)}"` })
            }
          `,
        });
        const expand = expanded ? `
          <div class="set-expand-box">
            <div class="set-expand-row">
              <span>配置目录：<span class="set-mono">${ctx.esc(a.profileDir)}</span></span>
              ${UI.buttonHTML({ label: "更换目录", size: "sm", attrs: `data-change-dir="${ctx.esc(a.id)}"` })}
            </div>
            <div class="set-expand-row">
              <span>插件状态：${ctx.esc(a.pluginVer)} · 仅使用独立受管 X-Api-Key</span>
            </div>
          </div>` : "";
        return row + expand;
      }).join("");

    const providerRows = cfg.providers.map(p => {
      const isActive = cfg.activeMode === "provider" && cfg.activeProvider === p.id;
      const stateText = isActive ? "当前使用" : (p.validated ? "已验证" : "未验证");
      const stateCls = (isActive || p.validated) ? "is-ok" : "";
      return UI.rowHTML({
        title: `${ctx.esc(p.name)} <span class="set-ver">${ctx.esc(p.model)}</span>`,
        desc: `${p.protocol} · ${p.baseUrl}${p.temperature !== "" ? ` · temp=${p.temperature}` : ""}`,
        descMono: true,
        control: `
          <span class="set-state-text ${stateCls}">${stateText}</span>
          ${UI.buttonHTML({ label: "配置", size: "sm", attrs: `data-edit-provider="${ctx.esc(p.id)}"` })}
          ${UI.buttonHTML({
            label: "删除",
            variant: "danger-outline",
            size: "sm",
            disabled: isActive,
            attrs: `data-del-provider="${ctx.esc(p.id)}" title="${isActive ? "当前正在使用，切换模型后再删除" : "删除此 Provider 配置"}"`,
          })}
        `,
      });
    });

    return `<div class="v4-group-wrap settings-pane-enter">
      <section class="v4-group">
        <div class="v4-group-head">
          <h3 class="v4-group-title">Agent 连接</h3>
          <div class="set-group-actions">
            ${UI.buttonHTML({ id: "btnDetectAgents", label: cfg.agentsDetecting ? "检测中…" : "重新检测", size: "sm", icon: ctx.icon("refresh"), disabled: cfg.agentsDetecting })}
          </div>
        </div>
        <div class="v4-group-list">${agentRowsHTML}</div>
      </section>

      <section class="v4-group">
        <div class="v4-group-head">
          <h3 class="v4-group-title">模型 Provider 配置</h3>
          <div class="set-group-actions">
            ${UI.buttonHTML({ id: "btnAddProvider", label: "添加配置", size: "sm", icon: ctx.icon("plus") })}
          </div>
        </div>
        <div class="v4-group-list">
          ${providerRows.length ? providerRows.join("") : UI.rowHTML({ title: "尚未添加模型 Provider", desc: "点击右上角「添加配置」接入 OpenAI 兼容或本地 Ollama 服务" })}
        </div>
      </section>
    </div>`;
  }

  function renderServiceTab(ctx) {
    const s = D.serviceStatus;
    const compRowsHTML = cfg.serviceDetecting
      ? UI.skeletonGroupHTML({ count: 4, lines: 2, right: true })
      : s.components.map(c => UI.rowHTML({
        leading: `<span class="dot ${c.state === "ok" ? "ok" : (c.state === "warn" ? "warn" : "fail")}"></span>`,
        title: ctx.esc(c.name),
        desc: c.metrics,
        descMono: true,
        value: c.statusText,
        valueMono: true,
      })).join("");

    const envRows = ENV_ITEMS.map(it => UI.rowHTML({
      leading: `<span class="dot ${it.state}"></span>`,
      title: ctx.esc(it.name),
      desc: it.version,
      descMono: true,
      value: it.statusText,
    }));
    envRows.push(UI.rowHTML({
      id: "btnEnvPathsRow",
      title: "查看组件路径与校验清单",
      desc: "核对受管目录与上游 Cognee 1.6.7 SHA256 指纹",
      clickable: true,
      chevron: true,
    }));

    const diagLogs = s.logs || [];
    const diagBody = cfg.diagOpen ? `
      <div class="svc-diag-panel" id="svcDiagPanel">
        <div class="svc-diag-bar">
          <span>${cfg.diagRaw ? "最近服务原始日志（已脱敏，最多 20 MiB 活 inode 截尾）" : "最近服务日志摘要（已脱敏）"}</span>
          <div class="set-group-actions">
            ${UI.buttonHTML({ id: "btnToggleRawLog", label: cfg.diagRaw ? "查看可读摘要" : "查看原始日志", size: "sm" })}
          </div>
        </div>
        <div class="svc-log-box">
          ${diagLogs.map(l => {
            const cls = l.level === "error" ? "is-error" : (l.level === "warning" ? "is-warning" : "");
            const txt = cfg.diagRaw ? `2026-11-20T${l.text} pid=48210 host=127.0.0.1` : l.text;
            return `<div class="svc-log-line ${cls}">${ctx.esc(txt)}</div>`;
          }).join("")}
        </div>
      </div>` : "";

    return `<div class="v4-group-wrap settings-pane-enter">
      <section class="v4-group">
        <div class="v4-group-head">
          <h3 class="v4-group-title">组件状态</h3>
          <span class="meta">更新于 ${ctx.esc(s.checkedAt)}</span>
          <div class="set-group-actions">
            ${UI.buttonHTML({ id: "btnSimDetect", label: cfg.serviceDetecting ? "结束检测" : "检测中骨架态", size: "sm" })}
            ${UI.buttonHTML({ id: "btnRecheckService", label: cfg.serviceDetecting ? "检测中…" : "重新检查", size: "sm", icon: ctx.icon("refresh"), disabled: cfg.serviceDetecting })}
          </div>
        </div>
        <div class="v4-group-list">${compRowsHTML}</div>
      </section>

      <section class="v4-group">
        <div class="v4-group-head">
          <h3 class="v4-group-title">诊断</h3>
          <span class="meta">日志默认收起，已脱敏</span>
          <div class="set-group-actions">
            ${UI.buttonHTML({ id: "btnCopyDiagRow", label: "复制诊断", size: "sm", icon: ctx.icon("copy") })}
          </div>
        </div>
        <div class="v4-group-list">
          ${UI.rowHTML({
            id: "btnToggleDiag",
            title: "服务日志与诊断信息",
            desc: `最近 ${diagLogs.length} 条记录 · 点击${cfg.diagOpen ? "收起" : "展开"}详情`,
            clickable: true,
            chevron: false,
            control: `<span class="svc-diag-chev${cfg.diagOpen ? " is-open" : ""}">${ctx.icon("chev")}</span>`,
          })}
          ${diagBody}
        </div>
      </section>

      ${UI.groupHTML({ title: "运行环境", desc: "已就绪 · 5/5 项", rows: envRows })}
    </div>`;
  }

  /* ---------- 弹窗：切换处理方式确认 / Provider 编辑 / Provider 删除（danger 不绑 ↵） / 环境详情 ---------- */
  function openHandoffDialog(ctx, targetMode, targetId, displayLabel) {
    UI.openDialog({
      title: "切换处理方式？",
      size: 480,
      cancelText: "取消",
      confirmText: "确认启用",
      hint: "仅确认后的服务回执会更新唯一 active",
      body: `
        <p>即将把记忆整理切换为：<strong>${ctx.esc(displayLabel)}</strong></p>
        <p style="font-size:var(--fs-footnote);color:var(--text-3)">当前正在处理 1 个会话，切换后将在收到新模型回执后接续处理，未完成记录不会丢失。</p>
      `,
      onConfirm() {
        cfg.activeMode = targetMode;
        if (targetMode === "cli") cfg.activeCliAgent = targetId;
        else cfg.activeProvider = targetId;
        ctx.render(false);
        ctx.toast(`已切换至 ${displayLabel}`);
      },
    });
  }

  function openProviderEditorDialog(ctx, provider) {
    const isNew = !provider;
    const draft = provider
      ? { ...provider }
      : { id: "prov-" + Date.now(), name: "自定义 Provider", protocol: "OpenAI 兼容", baseUrl: "", model: "", temperature: "", maxTokens: "", validated: false };

    let validationState = draft.validated ? "passed" : "";
    const protocols = ["OpenAI", "Anthropic", "Gemini", "OpenAI 兼容", "Ollama"];

    UI.openDialog({
      title: isNew ? "添加模型 Provider" : `配置 ${draft.name}`,
      size: 640,
      cancelText: "取消",
      confirmText: "保存配置",
      hint: "保存配置不会自动切换当前使用的模型",
      body: `
        <div class="set-form">
          <div class="set-field-row">
            <label class="set-field">
              <span class="set-field-label">配置名称</span>
              <input class="v4-input" id="provName" value="${ctx.esc(draft.name)}" placeholder="例如 OpenAI Compatible" />
            </label>
            <div class="set-field">
              <span class="set-field-label">供应商协议</span>
              <div id="provProtocolSlot">${UI.selectHTML({ id: "provProtocolSel", options: protocols.map(x => ({ value: x, label: x })), value: draft.protocol, width: 0 })}</div>
            </div>
          </div>
          <label class="set-field">
            <span class="set-field-label">API 地址</span>
            <input class="v4-input" id="provUrl" value="${ctx.esc(draft.baseUrl)}" placeholder="https://api.example.com/v1" />
          </label>
          <label class="set-field">
            <span class="set-field-label">模型 ID</span>
            <input class="v4-input" id="provModel" value="${ctx.esc(draft.model)}" placeholder="例如 gpt-4.1-mini" />
          </label>
          <div class="set-field-row">
            <label class="set-field">
              <span class="set-field-label">采样温度（可选）</span>
              <input class="v4-input" id="provTemp" value="${ctx.esc(draft.temperature)}" placeholder="留空表示省略；填写 0 将保留" />
            </label>
            <label class="set-field">
              <span class="set-field-label">最大输出 Token（可选）</span>
              <input class="v4-input" id="provMaxTok" value="${ctx.esc(draft.maxTokens)}" placeholder="例如 16384，留空则省略" />
            </label>
          </div>
          <label class="set-field">
            <span class="set-field-label">API Key<span class="set-field-hint">${isNew ? "Ollama 无需填写" : "已保存密钥；留空保留原密钥，不会回填"}</span></span>
            <input class="v4-input" id="provKey" type="password" value="" placeholder="${isNew ? "sk-…" : "已保存时留空保留"}" />
          </label>
          <div style="display:flex;align-items:center;gap:var(--s2)">
            ${UI.buttonHTML({ id: "btnValidateProv", label: "验证（会消耗额度）", size: "sm" })}
            <span class="set-field-hint" id="provValHint">${draft.validated ? "当前配置已通过结构化输出验证" : "未配置的选项将保持省略，不自动补默认值"}</span>
          </div>
        </div>
      `,
      onMount(panel) {
        UI.bindSelect($("#provProtocolSel", panel), {
          options: protocols.map(x => ({ value: x, label: x })),
          value: draft.protocol,
          align: "left",
          onSelect(next, _prev, ctrl) {
            draft.protocol = next;
            ctrl.commit(next);
          },
        });
        const valBtn = $("#btnValidateProv", panel);
        const valHint = $("#provValHint", panel);
        if (valBtn) {
          valBtn.onclick = () => {
            valBtn.disabled = true;
            valBtn.textContent = "正在验证…";
            setTimeout(() => {
              validationState = "passed";
              draft.validated = true;
              valBtn.disabled = false;
              valBtn.textContent = "验证通过";
              if (valHint) {
                valHint.textContent = "本次结构化请求：Schema 校验通过";
                valHint.style.color = "var(--ok)";
              }
            }, ctx.isReducedMotion() ? 100 : 700);
          };
        }
      },
      onConfirm(api) {
        const p = api.el;
        const name = ($("#provName", p).value || "").trim();
        const baseUrl = ($("#provUrl", p).value || "").trim();
        const model = ($("#provModel", p).value || "").trim();
        // 严格保留用户显式填写的 "0"，空串则保持省略
        const temperature = $("#provTemp", p).value.trim();
        const maxTokens = $("#provMaxTok", p).value.trim();
        if (!name || !model) {
          ctx.toast("请填写配置名称与模型 ID");
          return false;
        }
        const nextObj = {
          ...draft,
          name,
          baseUrl: baseUrl || "http://127.0.0.1:11434",
          model,
          temperature,
          maxTokens,
          validated: validationState === "passed" || draft.validated,
        };
        if (isNew) cfg.providers.push(nextObj);
        else {
          const idx = cfg.providers.findIndex(x => x.id === draft.id);
          if (idx >= 0) cfg.providers[idx] = nextObj;
        }
        ctx.render(false);
        ctx.toast(`已保存模型配置：${name}`);
      },
    });
  }

  function openDeleteProviderDialog(ctx, provider) {
    // Q8：删除类弹窗 danger: true -> danger 底色按钮、写明动作、不绑回车确认
    UI.openDialog({
      title: `删除模型配置「${provider.name}」？`,
      size: 480,
      danger: true,
      cancelText: "取消",
      confirmText: "删除此配置",
      hint: "此操作不可撤销（删除确认不绑定回车键）",
      body: `
        <p>将移除 <code>${ctx.esc(provider.model)}</code>（<code>${ctx.esc(provider.baseUrl)}</code>）及其本地密钥引用。</p>
        <p style="font-size:var(--fs-footnote);color:var(--text-3)">已保存在 Cognee 中的历史记忆与原始记录不受影响。</p>
      `,
      onConfirm() {
        cfg.providers = cfg.providers.filter(x => x.id !== provider.id);
        ctx.render(false);
        ctx.toast(`已删除 ${provider.name}`);
      },
    });
  }

  function openEnvPathsDialog(ctx) {
    UI.openDialog({
      title: "运行环境路径与校验清单",
      size: 640,
      hideConfirm: true,
      cancelText: "关闭",
      hint: "所有组件与日常库隔离存放，启动前校验上游 SHA256",
      body: UI.groupHTML({
        rows: ENV_ITEMS.map(it => UI.rowHTML({
          leading: `<span class="dot ${it.state}"></span>`,
          title: `${ctx.esc(it.name)} <span class="set-ver">${ctx.esc(it.version)}</span>`,
          desc: it.path,
          descMono: true,
          value: it.statusText,
        })),
      }),
    });
  }

  /* ---------- 注册「设置」页 ---------- */
  let unsubTheme = null;
  Pages.register({
    id: "settings",
    navLabel: "设置",
    render(mainEl, state, ctx) {
      const tab = SETTINGS_TABS.some(t => t.id === state.settingsTab) ? state.settingsTab : "processing";
      state.settingsTab = tab;

      let pane = "";
      if (tab === "processing") pane = renderProcessingTab(ctx);
      else if (tab === "agents") pane = renderAgentsTab(ctx);
      else pane = renderServiceTab(ctx);

      mainEl.innerHTML = `
        <div class="titlebar" id="titlebar">
          <span class="ico">${ctx.icon("settings")}</span>
          <h1>设置</h1>
          <span class="spacer"></span>
          ${ctx.searchBoxHTML()}
        </div>
        <div class="page">
          <div class="settings-tabs-bar">
            <div class="tabs" id="settingsTabs">
              <span class="bar"></span>
              ${SETTINGS_TABS.map(t => `<button type="button" data-stab="${t.id}" class="${tab === t.id ? "on" : ""}">${t.label}</button>`).join("")}
            </div>
          </div>
          <div class="settings-scroll scroll" id="settingsScroll">${pane}</div>
        </div>
      `;
    },
    bind(mainEl, state, ctx) {
      const tabsEl = $("#settingsTabs", mainEl);
      if (tabsEl) {
        ctx.placeBar(tabsEl);
        requestAnimationFrame(() => ctx.placeBar(tabsEl));
        $$("[data-stab]", tabsEl).forEach(b => {
          b.onclick = () => {
            if (state.settingsTab === b.dataset.stab) return;
            state.settingsTab = b.dataset.stab;
            ctx.render(false);
          };
        });
      }

      // 1) 记忆处理 tab 绑定
      const sourceSeg = $("#setSourceSeg", mainEl);
      if (sourceSeg) {
        UI.bindSegmented(sourceSeg, val => {
          cfg.sourceMode = val;
          ctx.render(false);
        });
      }
      UI.bindSwitch($("#swAutoOrganize", mainEl), v => { cfg.autoOrganize = v; ctx.toast(v ? "已开启自动整理新会话" : "已关闭自动整理（运行中任务保留）"); });
      UI.bindSwitch($("#swAutoRecall", mainEl), v => { cfg.autoRecall = v; ctx.toast(v ? "已开启自动召回" : "已关闭自动召回"); });
      UI.bindSwitch($("#swToolGraphGlobal", mainEl), v => { cfg.toolGraphGlobal = v; ctx.toast(v ? "已开启全局关系图谱构建" : "已关闭全局关系图谱构建"); });
      UI.bindSwitch($("#swDateLock", mainEl), v => { cfg.sessionDateLock = v; ctx.toast(v ? "已启用最早会话日期锁定" : "已关闭日期锁定"); });

      const appSegEl = $("#setAppearanceSeg", mainEl);
      if (unsubTheme) { unsubTheme(); unsubTheme = null; }
      if (appSegEl) {
        const segApi = UI.bindSegmented(appSegEl, val => ctx.setTheme(val));
        unsubTheme = ctx.onThemeChange(choice => {
          if (segApi && document.body.contains(appSegEl)) segApi.setValue(choice);
        });
      }

      $$("[data-handoff-cli]", mainEl).forEach(b => {
        b.onclick = () => {
          const a = cfg.cliAgents.find(x => x.id === b.dataset.handoffCli);
          if (a) openHandoffDialog(ctx, "cli", a.id, `已连接的 Agent · ${a.name}`);
        };
      });
      $$("[data-handoff-provider]", mainEl).forEach(b => {
        b.onclick = () => {
          const p = cfg.providers.find(x => x.id === b.dataset.handoffProvider);
          if (p) openHandoffDialog(ctx, "provider", p.id, `模型 API · ${p.name} (${p.model})`);
        };
      });
      const goAgentsRow = $("#setGoAgentsRow", mainEl);
      if (goAgentsRow) goAgentsRow.onclick = () => { state.settingsTab = "agents"; ctx.render(false); };
      $$("[data-goto-agents]", mainEl).forEach(b => {
        b.onclick = () => { state.settingsTab = "agents"; ctx.render(false); };
      });

      // 2) Agent 与模型 tab 绑定
      const btnDetectAgents = $("#btnDetectAgents", mainEl);
      if (btnDetectAgents) {
        btnDetectAgents.onclick = () => {
          cfg.agentsDetecting = true;
          ctx.render(false);
          setTimeout(() => {
            cfg.agentsDetecting = false;
            if (state.page === "settings" && state.settingsTab === "agents") ctx.render(false);
            ctx.toast("已完成本机 Agent 连接检测");
          }, ctx.isReducedMotion() ? 180 : 900);
        };
      }
      $$("[data-toggle-dir]", mainEl).forEach(b => {
        b.onclick = () => {
          const id = b.dataset.toggleDir;
          cfg.expandedAgentDirs[id] = !cfg.expandedAgentDirs[id];
          ctx.render(false);
        };
      });
      $$("[data-verify-agent]", mainEl).forEach(b => {
        b.onclick = () => {
          const a = cfg.cliAgents.find(x => x.id === b.dataset.verifyAgent);
          if (a) ctx.toast(`${a.name} Hook 回执核对正常`);
        };
      });
      $$("[data-connect-agent]", mainEl).forEach(b => {
        b.onclick = () => {
          const a = cfg.cliAgents.find(x => x.id === b.dataset.connectAgent);
          if (!a) return;
          a.connected = true;
          a.hookState = "ok";
          a.model = "默认模型（由宿主决定）";
          a.hookNote = "Hook 已接入 · 等待新建会话确认";
          a.pluginVer = "memlane-hook 1.6.7";
          ctx.render(false);
          ctx.toast(`已为 ${a.name} 写入受管接入配置`);
        };
      });
      $$("[data-change-dir]", mainEl).forEach(b => {
        b.onclick = () => ctx.toast("原型演示：已保留当前受管配置目录");
      });
      const btnAddProvider = $("#btnAddProvider", mainEl);
      if (btnAddProvider) btnAddProvider.onclick = () => openProviderEditorDialog(ctx, null);
      $$("[data-edit-provider]", mainEl).forEach(b => {
        b.onclick = () => {
          const p = cfg.providers.find(x => x.id === b.dataset.editProvider);
          if (p) openProviderEditorDialog(ctx, p);
        };
      });
      $$("[data-del-provider]", mainEl).forEach(b => {
        b.onclick = () => {
          const p = cfg.providers.find(x => x.id === b.dataset.delProvider);
          if (p) openDeleteProviderDialog(ctx, p);
        };
      });

      // 3) 服务 tab 绑定（Q6）
      const btnRecheck = $("#btnRecheckService", mainEl);
      if (btnRecheck) {
        btnRecheck.onclick = () => {
          cfg.serviceDetecting = true;
          ctx.render(false);
          setTimeout(() => {
            cfg.serviceDetecting = false;
            D.serviceStatus.checkedAt = "刚刚";
            if (state.page === "settings" && state.settingsTab === "service") ctx.render(false);
            ctx.toast("服务组件状态已刷新");
          }, ctx.isReducedMotion() ? 200 : 1100);
        };
      }
      const btnSimDetect = $("#btnSimDetect", mainEl);
      if (btnSimDetect) {
        btnSimDetect.onclick = () => {
          cfg.serviceDetecting = !cfg.serviceDetecting;
          ctx.render(false);
        };
      }
      const btnEnvPaths = $("#btnEnvPathsRow", mainEl);
      if (btnEnvPaths) btnEnvPaths.onclick = () => openEnvPathsDialog(ctx);

      const copyDiag = e => {
        if (e) e.stopPropagation();
        if (navigator.clipboard && navigator.clipboard.writeText) {
          navigator.clipboard.writeText(D.serviceStatus.diagnosticText).catch(() => {});
        }
        ctx.toast("已复制脱敏服务诊断信息");
      };
      const btnCopyDiagRow = $("#btnCopyDiagRow", mainEl);
      if (btnCopyDiagRow) btnCopyDiagRow.onclick = copyDiag;

      const btnToggleDiag = $("#btnToggleDiag", mainEl);
      if (btnToggleDiag) {
        btnToggleDiag.onclick = e => {
          if (e.target.closest("#btnCopyDiagRow")) return;
          cfg.diagOpen = !cfg.diagOpen;
          ctx.render(false);
        };
      }
      const btnToggleRawLog = $("#btnToggleRawLog", mainEl);
      if (btnToggleRawLog) {
        btnToggleRawLog.onclick = () => {
          cfg.diagRaw = !cfg.diagRaw;
          ctx.render(false);
        };
      }
    },
    onLeave() {
      if (unsubTheme) { unsubTheme(); unsubTheme = null; }
    },
  });

  /* ==========================================================================
   *  2. 项目「管理」标签（Q5）
   * ========================================================================== */
  function ensureProjectAccess(projId) {
    if (!cfg.access[projId]) {
      cfg.access[projId] = { gemini: "rw", codex: "ro", claude: "ro", cursor: "none", opencode: "none", pi: "none" };
    }
    return cfg.access[projId];
  }

  Pages.registerProjectTab("manage", {
    fullWidth: true,
    render(containerEl, project, state, ctx) {
      const accessMap = ensureProjectAccess(project.id);
      const tgMode = cfg.projectToolGraph[project.id] || "inherit";
      const tgEffective = tgMode === "on" || (tgMode === "inherit" && cfg.toolGraphGlobal);
      const notices = cfg.projectNotices[project.id] || [];
      const activeNotices = notices.filter(n => !n.ack);
      const historyNotices = notices.filter(n => n.ack);
      const historyOpen = !!cfg.noticeHistoryOpen[project.id];
      const queued = cfg.queuedDocs[project.id] || [];

      const infoGroup = UI.groupHTML({
        title: "项目信息",
        rows: [
          UI.rowHTML({
            title: "项目名称",
            value: ctx.esc(project.name),
            control: UI.buttonHTML({ id: "btnEditProjName", label: "编辑名称", size: "sm" }),
          }),
          UI.rowHTML({
            title: "项目路径",
            desc: project.path,
            descMono: true,
            control: UI.buttonHTML({ id: "btnCopyProjPath", label: "复制路径", size: "sm", icon: ctx.icon("copy") }),
          }),
        ],
      });

      const permRows = AGENT_ORDER.map(agentId => {
        const ag = D.agents[agentId];
        if (!ag) return "";
        const val = accessMap[agentId] || "none";
        return UI.rowHTML({
          id: `permRow-${agentId}`,
          leading: ctx.logo(agentId, 20),
          title: ctx.esc(ag.name),
          desc: ACCESS_DESC[val] || ACCESS_DESC.none,
          control: UI.triSelectHTML({
            id: `permSel-${agentId}`,
            value: val,
            attrs: `data-perm-agent="${ctx.esc(agentId)}"`,
          }),
        });
      });

      const permGroup = UI.groupHTML({
        title: "Agent 访问权限",
        desc: "三档权限立即保存并核对（第 2 次变更将演示失败回滚）",
        rows: permRows,
      });

      const toolGraphGroup = UI.groupHTML({
        title: "工具记录关系图谱",
        rows: [
          UI.rowHTML({
            title: "自动构建策略",
            desc: `当前生效：${tgEffective ? "开启" : "关闭"} · 更改仅影响后续任务，已有图谱保留`,
            control: UI.segmentedHTML({
              id: "projToolGraphSeg",
              options: [
                { value: "inherit", label: "跟随全局" },
                { value: "on", label: "开启" },
                { value: "off", label: "关闭" },
              ],
              value: tgMode,
            }),
          }),
          UI.rowHTML({
            title: "整理历史工具记录",
            desc: "待处理：6 个会话 · 42 条工具记录（使用当前模型，会消耗额度）",
            control: UI.buttonHTML({ id: "btnBuildToolGraph", label: "创建工具图谱", size: "sm" }),
          }),
        ],
      });

      const statusRows = [];
      statusRows.push(UI.rowHTML({
        title: "记忆整理队列",
        desc: queued.length
          ? `${queued.length} 份会话文档已保存，等待完成记忆提炼`
          : "当前没有积压的待整理文档",
        control: `
          <span class="manage-notice-tag${queued.length ? " is-accent" : ""}">${queued.length ? `${queued.length} 份排队中` : "已清空"}</span>
          ${queued.length ? UI.buttonHTML({ id: "btnViewQueue", label: "查看文档", size: "sm" }) : ""}
        `,
      }));

      for (const n of activeNotices) {
        statusRows.push(UI.rowHTML({
          title: `${ctx.esc(n.title)} <span class="manage-notice-tag${n.level === "warn" ? " is-warn" : ""}">${ctx.esc(n.tag)}</span>`,
          desc: n.desc,
          control: UI.buttonHTML({ label: "知道了", size: "sm", attrs: `data-ack-notice="${ctx.esc(n.id)}"` }),
        }));
      }

      if (!activeNotices.length) {
        statusRows.push(UI.rowHTML({
          title: "状态与异常提示",
          desc: "当前没有需要你处理的事项或未确认异常",
          value: "正常",
        }));
      }

      let historyBlock = "";
      if (historyNotices.length) {
        statusRows.push(UI.rowHTML({
          id: "btnToggleNoticeHistory",
          title: `已知晓的历史提示（${historyNotices.length}）`,
          desc: historyOpen ? "点击收起历史提示" : "点击查看已确认不再提醒的历史记录",
          clickable: true,
          chevron: false,
          control: `<span class="svc-diag-chev${historyOpen ? " is-open" : ""}">${ctx.icon("chev")}</span>`,
        }));
        if (historyOpen) {
          historyBlock = `<div class="manage-history-list">${historyNotices.map(h => `
            <div class="manage-history-item">
              <span>${ctx.esc(h.title)} · ${ctx.esc(h.desc)}</span>
              ${UI.buttonHTML({ label: "恢复提醒", size: "sm", attrs: `data-restore-notice="${ctx.esc(h.id)}"` })}
            </div>`).join("")}</div>`;
        }
      }

      containerEl.innerHTML = `<div class="v4-group-wrap settings-pane-enter">
        ${infoGroup}
        ${permGroup}
        ${toolGraphGroup}
        <section class="v4-group">
          <div class="v4-group-head">
            <h3 class="v4-group-title">消息与处理状态</h3>
            <span class="meta">自动保存已开启</span>
            <div class="set-group-actions">
              ${UI.buttonHTML({ id: "btnRefreshQueue", label: "刷新状态", size: "sm", icon: ctx.icon("refresh") })}
            </div>
          </div>
          <div class="v4-group-list">${statusRows.join("")}${historyBlock}</div>
        </section>
      </div>`;
    },
    bind(containerEl, project, state, ctx) {
      const accessMap = ensureProjectAccess(project.id);

      // 编辑项目名称（v4Dialog 480）
      const btnEditName = $("#btnEditProjName", containerEl);
      if (btnEditName) {
        btnEditName.onclick = () => {
          UI.openDialog({
            title: "编辑项目名称",
            size: 480,
            cancelText: "取消",
            confirmText: "保存名称",
            body: `
              <label class="set-field">
                <span class="set-field-label">项目名称</span>
                <input class="v4-input" id="editProjNameInput" value="${ctx.esc(project.name)}" placeholder="输入项目名称" />
                <span class="set-field-hint">仅修改客户端展示名称，不改动项目文件夹路径与 Cognee 数据集标识。</span>
              </label>
            `,
            onMount(panel) {
              const inp = $("#editProjNameInput", panel);
              if (inp) setTimeout(() => { inp.focus(); inp.select(); }, 20);
            },
            onConfirm(api) {
              const val = ($("#editProjNameInput", api.el).value || "").trim();
              if (!val) return false;
              project.name = val;
              ctx.render(false);
              ctx.toast(`项目名称已更新为 ${val}`);
            },
          });
        };
      }

      // 复制路径
      const btnCopyPath = $("#btnCopyProjPath", containerEl);
      if (btnCopyPath) {
        btnCopyPath.onclick = () => {
          if (navigator.clipboard && navigator.clipboard.writeText) {
            navigator.clipboard.writeText(project.path).catch(() => {});
          }
          ctx.toast(`已复制路径：${project.path}`);
        };
      }

      // 三档权限下拉（Q5：立即保存 + 保存中转圈 + 模拟一次失败回滚）
      $$("[data-perm-agent]", containerEl).forEach(selEl => {
        const agentId = selEl.dataset.permAgent;
        const rowEl = $(`#permRow-${agentId}`, containerEl);
        const descEl = rowEl ? $(".v4-row-desc", rowEl) : null;

        UI.bindSelect(selEl, {
          value: accessMap[agentId] || "none",
          align: "right",
          onSelect(nextVal, prevVal, ctrl) {
            cfg.accessAttemptCount += 1;
            const shouldFail = cfg.accessAttemptCount === 2;
            ctrl.setSaving(true, "保存中");
            setTimeout(() => {
              if (shouldFail) {
                const prevLabel = (UI.TRI_ACCESS_OPTIONS.find(o => o.value === prevVal) || { label: prevVal }).label;
                ctrl.fail(`保存失败：控制面校验超时，已回滚为「${prevLabel}」`, prevVal);
                return;
              }
              accessMap[agentId] = nextVal;
              ctrl.commit(nextVal);
              if (descEl) descEl.textContent = ACCESS_DESC[nextVal] || ACCESS_DESC.none;
              const agName = (D.agents[agentId] && D.agents[agentId].name) || agentId;
              const nextLabel = (UI.TRI_ACCESS_OPTIONS.find(o => o.value === nextVal) || { label: nextVal }).label;
              ctx.toast(`${agName} 权限已保存为「${nextLabel}」`);
            }, ctx.isReducedMotion() ? 180 : 680);
          },
        });
      });

      // 工具记录关系图谱三态
      const tgSeg = $("#projToolGraphSeg", containerEl);
      if (tgSeg) {
        UI.bindSegmented(tgSeg, val => {
          cfg.projectToolGraph[project.id] = val;
          ctx.render(false);
          const label = val === "inherit" ? "跟随全局" : (val === "on" ? "开启" : "关闭");
          ctx.toast(`关系图谱策略已保存为「${label}」`);
        });
      }

      // 创建工具图谱确认弹窗（v4Dialog 480）
      const btnBuild = $("#btnBuildToolGraph", containerEl);
      if (btnBuild) {
        btnBuild.onclick = () => {
          UI.openDialog({
            title: "创建工具图谱",
            size: 480,
            cancelText: "取消",
            confirmText: "开始创建",
            hint: "使用当前模型，会消耗额度",
            body: `
              <p>将已有工具记录整理成关系图谱，已处理的会话会自动跳过。</p>
              <div class="set-validation-box">
                <span>待处理：<strong>6 个会话 · 42 条工具记录</strong></span>
              </div>
            `,
            onConfirm() {
              ctx.toast("已提交关系图谱构建任务");
            },
          });
        };
      }

      // 队列文档查看
      const btnViewQueue = $("#btnViewQueue", containerEl);
      if (btnViewQueue) {
        btnViewQueue.onclick = () => {
          const docs = cfg.queuedDocs[project.id] || [];
          UI.openDialog({
            title: `待整理文档（${docs.length}）`,
            size: 480,
            hideConfirm: true,
            cancelText: "关闭",
            body: UI.groupHTML({
              rows: docs.map(d => UI.rowHTML({
                title: ctx.esc(d.title),
                desc: d.date,
                value: d.status,
              })),
            }),
          });
        };
      }

      // 刷新消息状态
      const btnRefresh = $("#btnRefreshQueue", containerEl);
      if (btnRefresh) btnRefresh.onclick = () => ctx.toast("已刷新消息与处理状态");

      // 确认 / 恢复历史提醒
      $$("[data-ack-notice]", containerEl).forEach(b => {
        b.onclick = () => {
          const list = cfg.projectNotices[project.id] || [];
          const hit = list.find(n => n.id === b.dataset.ackNotice);
          if (hit) hit.ack = true;
          ctx.render(false);
          ctx.toast("已不再提醒该条提示");
        };
      });
      $$("[data-restore-notice]", containerEl).forEach(b => {
        b.onclick = () => {
          const list = cfg.projectNotices[project.id] || [];
          const hit = list.find(n => n.id === b.dataset.restoreNotice);
          if (hit) hit.ack = false;
          ctx.render(false);
          ctx.toast("已恢复提示显示");
        };
      });
      const btnHist = $("#btnToggleNoticeHistory", containerEl);
      if (btnHist) {
        btnHist.onclick = () => {
          cfg.noticeHistoryOpen[project.id] = !cfg.noticeHistoryOpen[project.id];
          ctx.render(false);
        };
      }
    },
  });
})();
