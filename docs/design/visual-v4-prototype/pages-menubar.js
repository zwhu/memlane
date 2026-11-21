// Memlane V4 模拟 macOS 顶部菜单栏 + 300px 状态栏弹出面板（window.MemlaneMenubar）
(() => {
  const APPLE_SVG = '<svg viewBox="0 0 16 16" width="100%" height="100%" fill="currentColor" aria-hidden="true"><path d="M11.18 8.37c-.01-1.36 1.11-2.01 1.16-2.04-.63-.93-1.62-1.05-1.97-1.07-.83-.09-1.63.49-2.05.49-.43 0-1.08-.48-1.78-.47-.91.01-1.76.53-2.23 1.35-.96 1.66-.24 4.11.68 5.45.45.66.99 1.4 1.7 1.37.68-.03.94-.44 1.77-.44.82 0 1.06.44 1.78.42.74-.01 1.2-.67 1.65-1.33.52-.76.73-1.5.74-1.54-.02-.01-1.44-.55-1.45-2.19zM9.82 4.35c.38-.46.63-1.09.56-1.73-.54.02-1.2.36-1.59.82-.34.4-.65 1.04-.57 1.66.6.05 1.22-.3 1.6-.75z"/></svg>';
  const TRAY_MARK_SVG = '<svg viewBox="0 0 20 20" width="100%" height="100%" aria-hidden="true"><rect x="2" y="2" width="7" height="7" rx="2" fill="currentColor"/><rect x="11" y="2" width="7" height="7" rx="2" fill="currentColor" opacity=".62"/><rect x="2" y="11" width="7" height="7" rx="2" fill="currentColor" opacity=".62"/><rect x="11" y="11" width="7" height="7" rx="2" fill="currentColor" opacity=".32"/></svg>';
  const BRAND_MINI_SVG = '<svg class="mb-brand-mark" viewBox="0 0 20 20" width="16" height="16" aria-hidden="true"><rect x="2" y="2" width="7" height="7" rx="2" fill="var(--accent)"/><rect x="11" y="2" width="7" height="7" rx="2" fill="var(--accent)" opacity=".55"/><rect x="2" y="11" width="7" height="7" rx="2" fill="var(--accent)" opacity=".55"/><rect x="11" y="11" width="7" height="7" rx="2" fill="var(--accent)" opacity=".25"/></svg>';
  const WIFI_SVG = '<svg viewBox="0 0 20 20" width="100%" height="100%" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" aria-hidden="true"><path d="M3.2 8.2a10.2 10.2 0 0 1 13.6 0M5.8 11.1a6.4 6.4 0 0 1 8.4 0M8.5 13.9a2.4 2.4 0 0 1 3 0"/><circle cx="10" cy="16" r="1" fill="currentColor" stroke="none"/></svg>';
  const CONTROL_SVG = '<svg viewBox="0 0 20 20" width="100%" height="100%" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><rect x="3" y="4.5" width="14" height="4.5" rx="2.25"/><circle cx="13.2" cy="6.75" r="1.4" fill="currentColor" stroke="none"/><rect x="3" y="11" width="14" height="4.5" rx="2.25"/><circle cx="6.8" cy="13.25" r="1.4" fill="currentColor" stroke="none"/></svg>';
  const WINDOW_SVG = '<svg viewBox="0 0 20 20" width="100%" height="100%" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><rect x="3" y="4" width="14" height="12" rx="2"/><path d="M3 8h14" stroke-linecap="round"/></svg>';
  const CHEV_SVG = '<svg class="mb-chev" viewBox="0 0 20 20" width="12" height="12" fill="none" stroke="currentColor" stroke-width="1.6" aria-hidden="true"><path d="M8 5l5 5-5 5" stroke-linecap="round" stroke-linejoin="round"/></svg>';

  const DEFAULT_TRAY = {
    keepAliveOnQuit: true,
    openAtLogin: false,
    isDevBundle: true,
    loginHint: "当前为开发直接运行模式（非 .app 安装包），系统登录项受 macOS 限制时将保持关闭并如实提示。",
  };

  let barEl = null;
  let flyoutEl = null;
  let openState = false;
  let themeSegCtrl = null;
  let listenersBound = false;
  let selfNotifying = false;

  const getShell = () => window.MemlaneShell || null;
  const getUI = () => (getShell() && getShell().UI) || window.MemlaneUI;
  const getData = () => (getShell() && getShell().DATA) || window.MEMLANE_DATA || window.DATA || {};
  const esc = s => {
    const sh = getShell();
    if (sh && typeof sh.esc === "function") return sh.esc(s);
    return String(s ?? "").replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/"/g, "&quot;");
  };
  const getIcon = name => {
    const sh = getShell();
    if (sh && typeof sh.icon === "function") return sh.icon(name);
    return "";
  };

  function getTraySettings() {
    const sh = getShell();
    const D = getData();
    if (sh && sh.state) {
      if (!sh.state.traySettings) {
        sh.state.traySettings = { ...(D.traySettings || DEFAULT_TRAY) };
      }
      return sh.state.traySettings;
    }
    return { ...(D.traySettings || DEFAULT_TRAY) };
  }

  function getAlerts() {
    const sh = getShell();
    if (sh && typeof sh.getAllProjectAlerts === "function") {
      try { return sh.getAllProjectAlerts() || []; } catch (_) { return []; }
    }
    return [];
  }

  function getServiceStatus() {
    const D = getData();
    return D.serviceStatus || { overall: "ready", label: "服务 · 已连接", components: [] };
  }

  function getTodayCount() {
    const sh = getShell();
    const D = getData();
    const extra = (sh && sh.state && Number(sh.state.todayExtra)) || 0;
    if (D.heat && typeof D.key === "function") {
      const today = new Date();
      today.setHours(0, 0, 0, 0);
      return (D.heat[D.key(today)] || 0) + extra;
    }
    return extra;
  }

  function overallDotClass(overall) {
    if (overall === "ready") return "ok";
    if (overall === "warning") return "warn";
    return "fail";
  }

  function overallFlyoutLabel(svc) {
    if (svc.overall === "ready") return "服务 · 运行中";
    if (svc.overall === "warning") return "服务 · 需关注";
    if (svc.overall === "degraded") return "服务 · 异常";
    if (svc.overall === "exited") return "服务 · 已停止";
    return svc.label || "服务 · 运行中";
  }

  function trayIndicator() {
    const svc = getServiceStatus();
    const alerts = getAlerts();
    const serviceDegraded = svc.overall && svc.overall !== "ready";
    const serviceDanger = svc.overall === "degraded" || svc.overall === "exited" || svc.overall === "error";
    const hasErrorAlert = alerts.some(a => (a.errorCount || 0) > 0);
    if (!serviceDegraded && alerts.length === 0) return null;
    return { danger: serviceDanger || hasErrorAlert };
  }

  function compactComponentMetric(c) {
    if (!c) return "";
    const status = c.statusText || "";
    const m = String(c.metrics || "").match(/(\d+\s*ms)/i);
    if (m) return status ? `${status} · ${m[1]}` : m[1];
    return status || "运行中";
  }

  function getCoreComponents() {
    const svc = getServiceStatus();
    const list = Array.isArray(svc.components) ? svc.components : [];
    const wanted = ["cognee", "control", "agent-channel"];
    const picked = wanted.map(id => list.find(c => c.id === id)).filter(Boolean);
    if (picked.length) return picked;
    return list.slice(0, 3);
  }

  function hasOpenDialog() {
    return !!document.querySelector(".v4-dialog-scrim");
  }

  function mountMenubar() {
    const desk = document.querySelector(".desk");
    if (!desk) return;
    if (!barEl || !barEl.isConnected) {
      barEl = document.createElement("div");
      barEl.className = "macos-menubar";
      barEl.setAttribute("role", "banner");
      barEl.setAttribute("aria-label", "模拟 macOS 顶部菜单栏");
      desk.insertBefore(barEl, desk.firstChild);
    }
    renderBar();
  }

  function renderBar() {
    if (!barEl) return;
    const ind = trayIndicator();
    barEl.innerHTML = `
      <div class="mb-left">
        <span class="mb-apple">${APPLE_SVG}</span>
        <span class="mb-app-name">Memlane</span>
        <span class="mb-menu-item">文件</span>
        <span class="mb-menu-item">编辑</span>
        <span class="mb-menu-item">窗口</span>
      </div>
      <div class="mb-right">
        <span class="mb-sys-icon" aria-hidden="true">${WIFI_SVG}</span>
        <span class="mb-sys-icon" aria-hidden="true">${CONTROL_SVG}</span>
        <button type="button" id="menubarTrayBtn" class="mb-tray-btn${openState ? " is-open" : ""}" aria-haspopup="dialog" aria-expanded="${openState ? "true" : "false"}" aria-label="Memlane 状态栏面板" title="Memlane 状态栏">
          ${TRAY_MARK_SVG}
          ${ind ? `<span class="mb-tray-dot${ind.danger ? " is-danger" : ""}" aria-hidden="true"></span>` : ""}
        </button>
        <span class="mb-clock">11:32</span>
      </div>
    `;
    const btn = barEl.querySelector("#menubarTrayBtn");
    if (btn) {
      btn.onclick = e => {
        e.stopPropagation();
        toggle();
      };
    }
  }

  function positionFlyout() {
    if (!flyoutEl || !barEl) return;
    const desk = document.querySelector(".desk");
    const btn = barEl.querySelector("#menubarTrayBtn");
    if (!desk || !btn) return;
    const dr = desk.getBoundingClientRect();
    const br = btn.getBoundingClientRect();
    const fw = 300;
    const pad = 12;
    let left = br.right - dr.left - fw;
    if (left + fw > dr.width - pad) left = dr.width - fw - pad;
    if (left < pad) left = pad;
    flyoutEl.style.left = Math.round(left) + "px";
    flyoutEl.style.right = "auto";
    flyoutEl.style.top = "34px";
  }

  function renderFlyout() {
    if (!openState || !flyoutEl) return;
    const sh = getShell();
    const UI = getUI();
    const svc = getServiceStatus();
    const comps = getCoreComponents();
    const todayN = getTodayCount();
    const alerts = getAlerts();
    const tray = getTraySettings();
    const themeChoice = sh && typeof sh.getThemeChoice === "function" ? sh.getThemeChoice() : "light";

    const compsHTML = comps.map(c => {
      const dotCls = c.state === "ok" ? "ok" : (c.state === "warn" ? "warn" : "fail");
      return `<div class="mb-comp-row">
        <span class="dot ${dotCls}"></span>
        <span class="mb-comp-name">${esc(c.name)}</span>
        <span class="mb-comp-val${dotCls === "fail" ? " is-fail" : ""}">${esc(compactComponentMetric(c))}</span>
      </div>`;
    }).join("");

    const alertsHTML = alerts.length
      ? `<div class="mb-sec-label">项目待处理提醒</div>${alerts.map(a => {
          const isDanger = (a.errorCount || 0) > 0;
          return `<button type="button" class="mb-alert-row" data-mb-project="${esc(a.projectId)}">
            <span class="dot ${isDanger ? "fail" : "warn"}"></span>
            <span class="mb-alert-name">${esc(a.projectName || a.projectId)}</span>
            <span class="mb-alert-badge${isDanger ? " is-danger" : ""}">${esc(a.total)} 项待处理</span>
            ${CHEV_SVG}
          </button>`;
        }).join("")}`
      : `<div class="mb-quiet-row"><span class="dot ok"></span><span>所有项目消息状态正常</span></div>`;

    const segHTML = UI && typeof UI.segmentedHTML === "function"
      ? UI.segmentedHTML({
          id: "mbThemeSeg",
          size: "sm",
          value: themeChoice,
          options: [
            { value: "system", label: "跟随系统" },
            { value: "light", label: "浅色" },
            { value: "dark", label: "深色" },
          ],
        })
      : "";

    const keepAliveSwitch = UI && typeof UI.switchHTML === "function"
      ? UI.switchHTML({ id: "mbKeepAliveSwitch", checked: !!tray.keepAliveOnQuit })
      : "";

    const openAtLoginSwitch = UI && typeof UI.switchHTML === "function"
      ? UI.switchHTML({ id: "mbOpenAtLoginSwitch", checked: !!tray.openAtLogin })
      : "";

    const quitLabel = tray.keepAliveOnQuit ? "退出界面（后台保活）" : "退出并停止服务";

    flyoutEl.innerHTML = `
      <div class="mb-head">
        ${BRAND_MINI_SVG}
        <div class="mb-head-status">
          <span class="dot ${overallDotClass(svc.overall)}"></span>
          <span class="mb-head-label">${esc(overallFlyoutLabel(svc))}</span>
        </div>
        <div class="mb-head-actions">
          <button type="button" class="mb-ico-btn" id="mbOpenWindowBtn" title="打开主窗口" aria-label="打开主窗口">${WINDOW_SVG}</button>
          <button type="button" class="mb-ico-btn" id="mbCmdKBtn" title="⌘K 搜索" aria-label="⌘K 搜索">${getIcon("search")}</button>
        </div>
      </div>

      <div class="mb-sec">
        ${compsHTML}
        <div class="mb-today-row">
          <span>今日新增记忆</span>
          <span class="mb-today-count">${todayN} 条</span>
        </div>
      </div>

      <div class="mb-sec">
        ${alertsHTML}
      </div>

      <div class="mb-sec">
        <div class="mb-setting-row">
          <span class="mb-setting-label">外观</span>
          ${segHTML}
        </div>
        <div class="mb-setting-row">
          <span class="mb-setting-label">退出时保持后台运行</span>
          ${keepAliveSwitch}
        </div>
        <div class="mb-setting-row">
          <span class="mb-setting-label">登录时静默启动</span>
          ${openAtLoginSwitch}
        </div>
        <div id="mbLoginHintSlot">${tray.openAtLogin && tray.isDevBundle && tray.loginHint ? `<div class="mb-login-hint">${esc(tray.loginHint)}</div>` : ""}</div>
        <div class="mb-links">
          <button type="button" class="mb-link-row" id="mbGoProcessingBtn">
            <span>记忆提炼设置</span>
            ${CHEV_SVG}
          </button>
          <button type="button" class="mb-link-row" id="mbGoServiceBtn">
            <span>服务与组件详情</span>
            ${CHEV_SVG}
          </button>
        </div>
      </div>

      <div class="mb-foot" id="mbFoot">
        <button type="button" class="mb-quit-btn" id="mbQuitPrimaryBtn">${esc(quitLabel)}</button>
        ${tray.keepAliveOnQuit ? '<button type="button" class="mb-quit-hard-btn" id="mbQuitHardBtn">彻底停止并退出</button>' : ""}
      </div>
    `;

    bindFlyoutEvents();
    positionFlyout();
  }

  function bindFlyoutEvents() {
    if (!flyoutEl) return;
    const sh = getShell();
    const UI = getUI();
    const tray = getTraySettings();

    const openWinBtn = flyoutEl.querySelector("#mbOpenWindowBtn");
    if (openWinBtn) {
      openWinBtn.onclick = () => {
        close();
        if (sh && sh.state && sh.state.page === "setup" && typeof sh.go === "function") {
          sh.go("overview");
        }
        if (sh && typeof sh.toast === "function") sh.toast("已聚焦 Memlane 主窗口");
      };
    }

    const cmdKBtn = flyoutEl.querySelector("#mbCmdKBtn");
    if (cmdKBtn) {
      cmdKBtn.onclick = () => {
        close();
        if (sh && typeof sh.openCommandPalette === "function") sh.openCommandPalette();
        else if (window.MemlanePages && typeof window.MemlanePages.openCommandPalette === "function") {
          window.MemlanePages.openCommandPalette();
        }
      };
    }

    flyoutEl.querySelectorAll("[data-mb-project]").forEach(btn => {
      btn.onclick = () => {
        const pid = btn.dataset.mbProject;
        close();
        if (sh && typeof sh.openProjectMessages === "function") {
          sh.openProjectMessages(pid);
        } else if (sh && typeof sh.go === "function") {
          sh.go("project", { project: pid, tab: "manage" });
        }
      };
    });

    const segEl = flyoutEl.querySelector("#mbThemeSeg");
    if (segEl && UI && typeof UI.bindSegmented === "function") {
      themeSegCtrl = UI.bindSegmented(segEl, val => {
        if (sh && typeof sh.setTheme === "function") sh.setTheme(val);
      });
      requestAnimationFrame(() => UI.layoutSegmented(segEl));
    }

    const notifyShell = () => {
      if (!sh || typeof sh.notifyExternalStateChange !== "function") return;
      selfNotifying = true;
      try { sh.notifyExternalStateChange(); } finally { selfNotifying = false; }
    };

    const bindFooterEvents = () => {
      const quitPrimary = flyoutEl.querySelector("#mbQuitPrimaryBtn");
      if (quitPrimary) {
        quitPrimary.onclick = () => {
          close();
          const msg = tray.keepAliveOnQuit
            ? "已退出桌面界面，后台记忆服务保持运行（原型模拟）"
            : "已停止本地服务并退出 Memlane（原型模拟）";
          if (sh && typeof sh.toast === "function") sh.toast(msg);
        };
      }

      const quitHard = flyoutEl.querySelector("#mbQuitHardBtn");
      if (quitHard) {
        quitHard.onclick = () => {
          close();
          if (UI && typeof UI.openDialog === "function") {
            UI.openDialog({
              title: "彻底停止并退出 Memlane？",
              size: 480,
              danger: true,
              confirmText: "彻底停止并退出",
              cancelText: "取消",
              hint: "危险操作不响应回车，请手动点击确认",
              body: `<p>将停止本地记忆引擎（Cognee）、接入网关与 Agent 模型代理，正在进行的后台提炼任务会被中断。</p>
                <p style="font-size:var(--fs-footnote);color:var(--text-3)">已采集的记忆与原始记录保留在本地存储中，下次打开应用时可重新启动服务。</p>`,
              onConfirm: () => {
                if (sh && typeof sh.toast === "function") {
                  sh.toast("已彻底停止全部后台服务并退出 Memlane（原型模拟）");
                }
              },
            });
          }
        };
      }
    };

    const keepSw = flyoutEl.querySelector("#mbKeepAliveSwitch");
    if (keepSw && UI && typeof UI.bindSwitch === "function") {
      UI.bindSwitch(keepSw, next => {
        tray.keepAliveOnQuit = next;
        const foot = flyoutEl.querySelector("#mbFoot");
        if (foot) {
          const nextLabel = next ? "退出界面（后台保活）" : "退出并停止服务";
          foot.innerHTML = `<button type="button" class="mb-quit-btn" id="mbQuitPrimaryBtn">${esc(nextLabel)}</button>${next ? '<button type="button" class="mb-quit-hard-btn" id="mbQuitHardBtn">彻底停止并退出</button>' : ""}`;
          bindFooterEvents();
        }
        notifyShell();
      });
    }

    const loginSw = flyoutEl.querySelector("#mbOpenAtLoginSwitch");
    if (loginSw && UI && typeof UI.bindSwitch === "function") {
      UI.bindSwitch(loginSw, next => {
        tray.openAtLogin = next;
        const slot = flyoutEl.querySelector("#mbLoginHintSlot");
        if (slot) {
          slot.innerHTML = next && tray.isDevBundle && tray.loginHint
            ? `<div class="mb-login-hint">${esc(tray.loginHint)}</div>`
            : "";
        }
        notifyShell();
      });
    }

    const goProcBtn = flyoutEl.querySelector("#mbGoProcessingBtn");
    if (goProcBtn) {
      goProcBtn.onclick = () => {
        close();
        if (sh && typeof sh.go === "function") sh.go("settings", { tab: "processing" });
      };
    }

    const goSvcBtn = flyoutEl.querySelector("#mbGoServiceBtn");
    if (goSvcBtn) {
      goSvcBtn.onclick = () => {
        close();
        if (sh && typeof sh.go === "function") sh.go("settings", { tab: "service" });
      };
    }

    bindFooterEvents();
  }

  function open() {
    mountMenubar();
    if (openState && flyoutEl && flyoutEl.isConnected) {
      renderFlyout();
      return;
    }
    const sh = getShell();
    const UI = getUI();
    if (UI && typeof UI.closeMenus === "function") UI.closeMenus();
    if (sh && typeof sh.closeServicePopover === "function") sh.closeServicePopover();

    openState = true;
    renderBar();

    const desk = document.querySelector(".desk");
    if (!desk) return;
    if (flyoutEl) flyoutEl.remove();
    flyoutEl = document.createElement("div");
    flyoutEl.className = "menubar-flyout";
    flyoutEl.setAttribute("role", "dialog");
    flyoutEl.setAttribute("aria-label", "Memlane 状态栏面板");
    desk.appendChild(flyoutEl);
    renderFlyout();
  }

  function close() {
    if (!openState) return;
    openState = false;
    themeSegCtrl = null;
    if (flyoutEl) {
      flyoutEl.remove();
      flyoutEl = null;
    }
    renderBar();
  }

  function toggle() {
    if (openState) close();
    else open();
  }

  function isOpen() {
    return openState;
  }

  function render() {
    mountMenubar();
    if (openState) renderFlyout();
  }

  function bindGlobalListeners() {
    if (listenersBound) return;
    listenersBound = true;

    document.addEventListener("pointerdown", e => {
      if (!openState || hasOpenDialog()) return;
      if (flyoutEl && flyoutEl.contains(e.target)) return;
      if (e.target.closest("#menubarTrayBtn") || e.target.closest("#toggleMenubar")) return;
      close();
    });

    document.addEventListener("keydown", e => {
      if (e.key === "Escape" && openState && !hasOpenDialog()) {
        e.preventDefault();
        e.stopPropagation();
        close();
      }
    });

    window.addEventListener("resize", () => {
      if (openState) positionFlyout();
    });

    const sh = getShell();
    if (sh && typeof sh.onThemeChange === "function") {
      sh.onThemeChange(choice => {
        if (openState && themeSegCtrl) themeSegCtrl.setValue(choice);
      });
    }
    if (sh && typeof sh.onStateChange === "function") {
      sh.onStateChange(() => {
        renderBar();
        if (openState && !selfNotifying) renderFlyout();
      });
    }
  }

  function init() {
    mountMenubar();
    bindGlobalListeners();
    const q = new URLSearchParams(location.search);
    if (q.get("menubar") === "1") {
      requestAnimationFrame(() => open());
    }
  }

  window.MemlaneMenubar = { open, close, toggle, isOpen, render };

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init, { once: true });
  } else {
    queueMicrotask(init);
  }
})();
