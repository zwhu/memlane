// Memlane V4 共用组件库（window.MemlaneUI）：分组列表、v4Switch、v4Segmented、三档下拉、骨架行、按钮、v4Dialog、V4 下拉/右键菜单。
window.MemlaneUI = (() => {
  const esc = s => String(s ?? "").replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/"/g, "&quot;");
  const getWindowEl = () => document.getElementById("window") || document.body;

  const CHECK_SVG = '<svg viewBox="0 0 20 20" width="100%" height="100%" fill="none" stroke="currentColor" stroke-width="1.75"><path d="M4.5 10.5l3.5 3.5 7.5-8" stroke-linecap="round" stroke-linejoin="round"/></svg>';
  const CHEV_SVG = '<svg viewBox="0 0 20 20" width="100%" height="100%" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M8 5l5 5-5 5" stroke-linecap="round" stroke-linejoin="round"/></svg>';
  const DOWN_SVG = '<svg viewBox="0 0 20 20" width="100%" height="100%" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M5 8l5 5 5-5" stroke-linecap="round" stroke-linejoin="round"/></svg>';
  const CLOSE_SVG = '<svg viewBox="0 0 20 20" width="100%" height="100%" fill="none" stroke="currentColor" stroke-width="1.6"><path d="M5 5l10 10M15 5L5 15" stroke-linecap="round"/></svg>';

  /* ---------- 1. 分组列表（Q2） ---------- */
  function rowHTML(opts = {}) {
    const {
      id = "", title = "", desc = "", descMono = false, leading = "",
      control = "", value = "", valueMono = false, error = "",
      clickable = false, chevron = clickable, double = !!desc, attrs = "",
    } = opts;
    const tag = clickable ? "button" : "div";
    const cls = ["v4-row", double ? "v4-row--double" : "", clickable ? "v4-row--click" : ""].filter(Boolean).join(" ");
    const idAttr = id ? ` id="${esc(id)}"` : "";
    const typeAttr = clickable ? ' type="button"' : "";
    return `<${tag}${typeAttr}${idAttr} class="${cls}" ${attrs}>
      ${leading ? `<span class="v4-row-lead">${leading}</span>` : ""}
      <span class="v4-row-main">
        <span class="v4-row-title">${title}</span>
        ${desc ? `<span class="v4-row-desc${descMono ? " mono" : ""}">${desc}</span>` : ""}
        ${error ? `<span class="v4-row-error">${esc(error)}</span>` : ""}
      </span>
      <span class="v4-row-ctrl">
        ${value ? `<span class="v4-row-val${valueMono ? " mono" : ""}">${value}</span>` : ""}
        ${control}
        ${chevron ? `<span class="v4-row-chev">${CHEV_SVG}</span>` : ""}
      </span>
    </${tag}>`;
  }

  function groupHTML({ id = "", title = "", desc = "", rows = [] } = {}) {
    const body = Array.isArray(rows) ? rows.map(r => (typeof r === "string" ? r : rowHTML(r))).join("") : String(rows || "");
    return `<section${id ? ` id="${esc(id)}"` : ""} class="v4-group">
      ${(title || desc) ? `<div class="v4-group-head">${title ? `<h3 class="v4-group-title">${esc(title)}</h3>` : ""}${desc ? `<span class="v4-group-desc">${esc(desc)}</span>` : ""}</div>` : ""}
      <div class="v4-group-list">${body}</div>
    </section>`;
  }

  /* ---------- 2. 按钮 ---------- */
  function buttonHTML({ id = "", label = "", variant = "default", size = "md", icon = "", disabled = false, attrs = "" } = {}) {
    const cls = [
      "v4-btn",
      variant !== "default" ? `v4-btn--${variant}` : "",
      size === "sm" ? "v4-btn--sm" : "",
    ].filter(Boolean).join(" ");
    return `<button type="button"${id ? ` id="${esc(id)}"` : ""} class="${cls}"${disabled ? " disabled" : ""} ${attrs}>${icon ? `<span class="ico">${icon}</span>` : ""}${esc(label)}</button>`;
  }

  /* ---------- 3. v4Switch ---------- */
  function switchHTML({ id = "", checked = false, label = "", disabled = false, attrs = "" } = {}) {
    return `<button type="button"${id ? ` id="${esc(id)}"` : ""} class="v4-switch${checked ? " on" : ""}" role="switch" aria-checked="${checked ? "true" : "false"}" data-v4-switch${disabled ? " disabled" : ""} ${attrs}><span class="v4-switch-track"><span class="v4-switch-knob"></span></span>${label ? `<span>${esc(label)}</span>` : ""}</button>`;
  }

  function bindSwitch(el, onChange) {
    if (!el) return;
    el.onclick = () => {
      if (el.disabled) return;
      const next = !el.classList.contains("on");
      el.classList.toggle("on", next);
      el.setAttribute("aria-checked", next ? "true" : "false");
      if (onChange) onChange(next, el);
    };
  }

  /* ---------- 4. v4Segmented（弹簧滑块） ---------- */
  function normalizeOptions(options) {
    return (options || []).map((o, i) => (typeof o === "string" ? { value: String(i), label: o } : { value: String(o.value), label: o.label }));
  }

  function segmentedHTML({ id = "", options = [], value = null, size = "md", fullWidth = false, attrs = "" } = {}) {
    const opts = normalizeOptions(options);
    const activeVal = value == null ? (opts[0] ? opts[0].value : "") : String(value);
    const cls = ["v4-seg", size === "sm" ? "v4-seg--sm" : "", fullWidth ? "v4-seg--full" : ""].filter(Boolean).join(" ");
    return `<div${id ? ` id="${esc(id)}"` : ""} class="${cls}" role="tablist" data-v4-seg ${attrs}><span class="v4-seg-thumb"></span>${opts.map((o, i) => `<button type="button" role="tab" data-v="${esc(o.value)}" data-i="${i}" class="${o.value === activeVal ? "on" : ""}" aria-selected="${o.value === activeVal ? "true" : "false"}">${esc(o.label)}</button>`).join("")}</div>`;
  }

  function layoutSegmented(segEl) {
    if (!segEl) return;
    const on = segEl.querySelector("button.on") || segEl.querySelector("button");
    const thumb = segEl.querySelector(".v4-seg-thumb");
    if (!on || !thumb || !on.offsetWidth) return;
    thumb.style.width = on.offsetWidth + "px";
    thumb.style.transform = `translateX(${on.offsetLeft}px)`;
  }

  function bindSegmented(segEl, onChange) {
    if (!segEl) return;
    const btns = [...segEl.querySelectorAll("button")];
    const setActive = (btn, emit) => {
      btns.forEach(b => {
        const isOn = b === btn;
        b.classList.toggle("on", isOn);
        b.setAttribute("aria-selected", isOn ? "true" : "false");
      });
      layoutSegmented(segEl);
      if (emit && onChange) onChange(btn.dataset.v, Number(btn.dataset.i), segEl);
    };
    btns.forEach(btn => {
      btn.onclick = () => {
        if (btn.classList.contains("on")) return;
        setActive(btn, true);
      };
    });
    layoutSegmented(segEl);
    requestAnimationFrame(() => layoutSegmented(segEl));
    return {
      setValue(v) {
        const target = btns.find(b => b.dataset.v === String(v));
        if (target) setActive(target, false);
      },
      layout: () => layoutSegmented(segEl),
    };
  }

  /* ---------- 5. 骨架行 ---------- */
  function skeletonRowHTML({ lines = 1, avatar = false, right = true, double = lines > 1 } = {}) {
    return `<div class="v4-skel-row${double ? " v4-skel-row--double" : ""}" aria-hidden="true">
      ${avatar ? '<span class="v4-skel-bar v4-skel-avatar"></span>' : ""}
      <span class="v4-skel-col">
        <span class="v4-skel-bar" style="width:46%"></span>
        ${lines > 1 ? '<span class="v4-skel-bar v4-skel-bar--sm" style="width:74%"></span>' : ""}
      </span>
      ${right ? '<span class="v4-skel-bar v4-skel-bar--pill"></span>' : ""}
    </div>`;
  }

  function skeletonGroupHTML({ count = 3, lines = 1, avatar = false, right = true } = {}) {
    return Array.from({ length: count }, () => skeletonRowHTML({ lines, avatar, right })).join("");
  }

  /* ---------- 6. V4 下拉 / 右键菜单（Q8） ---------- */
  let activeMenu = null;

  function closeMenus() {
    if (activeMenu) {
      if (activeMenu.cleanup) activeMenu.cleanup();
      activeMenu.el.remove();
      activeMenu = null;
    }
    document.querySelectorAll(".v4-menu, .menu").forEach(m => m.remove());
  }

  function renderMenuEl(items, onPick) {
    const el = document.createElement("div");
    el.className = "v4-menu";
    el.setAttribute("role", "menu");
    el.innerHTML = items.map((it, idx) => {
      if (it === "-" || it.type === "divider") return "<hr>";
      const cls = ["v4-menu-item", it.danger ? "is-danger" : "", it.disabled ? "is-disabled" : ""].filter(Boolean).join(" ");
      return `<button type="button" role="menuitem" class="${cls}" data-idx="${idx}" data-v="${esc(it.v ?? it.value ?? "")}"${it.disabled ? " disabled" : ""}>
        <span class="v4-menu-chk">${it.on || it.checked ? CHECK_SVG : ""}</span>
        <span class="v4-menu-text">
          <span>${esc(it.label)}</span>
          ${it.desc ? `<span class="v4-menu-sub">${esc(it.desc)}</span>` : ""}
        </span>
        ${it.n != null ? `<span class="v4-menu-trail">${esc(it.n)}</span>` : (it.shortcut ? `<span class="v4-menu-trail">${esc(it.shortcut)}</span>` : "")}
      </button>`;
    }).join("");

    const pick = btn => {
      if (!btn || btn.disabled) return;
      const item = items[Number(btn.dataset.idx)];
      closeMenus();
      if (onPick) onPick(btn.dataset.v, item);
    };

    el.onclick = e => {
      const btn = e.target.closest(".v4-menu-item");
      if (btn) pick(btn);
    };

    // 键盘 ↑↓ / Enter / Esc
    let focusIdx = -1;
    const enabledBtns = () => [...el.querySelectorAll(".v4-menu-item:not(:disabled)")];
    const setFocus = i => {
      const list = enabledBtns();
      if (!list.length) return;
      focusIdx = (i + list.length) % list.length;
      list.forEach((b, k) => b.classList.toggle("is-active", k === focusIdx));
    };
    const onKey = e => {
      if (e.key === "Escape") { e.preventDefault(); e.stopPropagation(); closeMenus(); }
      else if (e.key === "ArrowDown") { e.preventDefault(); e.stopPropagation(); setFocus(focusIdx + 1); }
      else if (e.key === "ArrowUp") { e.preventDefault(); e.stopPropagation(); setFocus(focusIdx <= 0 ? enabledBtns().length - 1 : focusIdx - 1); }
      else if (e.key === "Enter" && focusIdx >= 0) { e.preventDefault(); e.stopPropagation(); pick(enabledBtns()[focusIdx]); }
    };
    document.addEventListener("keydown", onKey, true);
    return { el, cleanup: () => document.removeEventListener("keydown", onKey, true) };
  }

  function clampMenuInWindow(el, x, y, opts = {}) {
    const win = getWindowEl();
    const wr = win.getBoundingClientRect();
    win.appendChild(el);
    const mw = el.offsetWidth;
    const mh = el.offsetHeight;
    let left = x;
    let top = y;
    if (opts.align === "right") {
      left = x - mw;
      el.classList.add("from-right");
    }
    if (opts.placement === "top") {
      top = y - mh;
      el.classList.add("from-bottom");
    }
    if (left + mw > wr.width - 8) {
      left = Math.max(8, wr.width - mw - 8);
      el.classList.add("from-right");
    }
    if (left < 8) left = 8;
    if (top + mh > wr.height - 8) {
      top = Math.max(8, (opts.anchorTop != null ? opts.anchorTop - mh - 4 : wr.height - mh - 8));
      el.classList.add("from-bottom");
    }
    if (top < 8) top = 8;
    el.style.left = Math.round(left) + "px";
    el.style.top = Math.round(top) + "px";
  }

  function openMenu(anchorEl, items, onPick, opts = {}) {
    const wasSame = activeMenu && activeMenu.anchor === anchorEl;
    closeMenus();
    if (wasSame) return null;
    const win = getWindowEl();
    const wr = win.getBoundingClientRect();
    const ar = anchorEl.getBoundingClientRect();
    const menuObj = renderMenuEl(items, onPick);
    if (opts.minWidth) menuObj.el.style.minWidth = opts.minWidth + "px";
    const x = opts.align === "right" ? (ar.right - wr.left) : (ar.left - wr.left);
    const y = opts.placement === "top" ? (ar.top - wr.top - 4) : (ar.bottom - wr.top + 4);
    clampMenuInWindow(menuObj.el, x, y, { ...opts, anchorTop: ar.top - wr.top });
    anchorEl.classList.add("is-open");
    const prevCleanup = menuObj.cleanup;
    menuObj.anchor = anchorEl;
    menuObj.cleanup = () => {
      anchorEl.classList.remove("is-open");
      prevCleanup();
    };
    activeMenu = menuObj;
    return menuObj;
  }

  function openContextMenu(clientX, clientY, items, onPick) {
    closeMenus();
    const win = getWindowEl();
    const wr = win.getBoundingClientRect();
    const menuObj = renderMenuEl(items, onPick);
    clampMenuInWindow(menuObj.el, clientX - wr.left, clientY - wr.top);
    activeMenu = menuObj;
    return menuObj;
  }

  document.addEventListener("pointerdown", e => {
    if (!e.target.closest(".v4-menu") && !e.target.closest(".v4-select-btn") && !e.target.closest(".dd")) {
      closeMenus();
    }
  });

  /* ---------- 7. 三档下拉 / V4 Select（Q5：保存中转圈 + 失败回滚 danger 文字） ---------- */
  const TRI_ACCESS_OPTIONS = [
    { value: "none", label: "不可访问" },
    { value: "ro", label: "只读" },
    { value: "rw", label: "读写" },
  ];

  function selectHTML({ id = "", options = [], value = "", disabled = false, width = 104, attrs = "" } = {}) {
    const opts = normalizeOptions(options);
    const cur = opts.find(o => o.value === String(value)) || opts[0] || { value: "", label: "" };
    return `<div${id ? ` id="${esc(id)}"` : ""} class="v4-select" data-v4-select data-value="${esc(cur.value)}" ${attrs}>
      <div class="v4-select-wrap">
        <span class="v4-select-state" hidden></span>
        <button type="button" class="v4-select-btn" style="min-width:${width}px"${disabled ? " disabled" : ""}>
          <span class="v4-select-label">${esc(cur.label)}</span>
          <span class="v4-select-chev">${DOWN_SVG}</span>
        </button>
      </div>
      <div class="v4-select-err" hidden></div>
    </div>`;
  }

  function triSelectHTML({ id = "", value = "ro", disabled = false, attrs = "" } = {}) {
    return selectHTML({ id, options: TRI_ACCESS_OPTIONS, value, disabled, width: 100, attrs });
  }

  function bindSelect(rootEl, { options = TRI_ACCESS_OPTIONS, value, align = "right", onSelect } = {}) {
    if (!rootEl) return null;
    const opts = normalizeOptions(options);
    const btn = rootEl.querySelector(".v4-select-btn");
    const labelEl = rootEl.querySelector(".v4-select-label");
    const stateEl = rootEl.querySelector(".v4-select-state");
    const errEl = rootEl.querySelector(".v4-select-err");
    let current = value != null ? String(value) : (rootEl.dataset.value || (opts[0] ? opts[0].value : ""));

    const setDisplayed = v => {
      current = String(v);
      rootEl.dataset.value = current;
      const found = opts.find(o => o.value === current);
      if (labelEl) labelEl.textContent = found ? found.label : current;
    };

    const ctrl = {
      getValue: () => current,
      setValue: v => setDisplayed(v),
      setSaving(on, text = "保存中") {
        btn.disabled = !!on;
        if (on) {
          btn.classList.remove("is-error");
          errEl.hidden = true;
          errEl.textContent = "";
          stateEl.hidden = false;
          stateEl.innerHTML = `<span class="v4-spinner"></span><span>${esc(text)}</span>`;
        } else {
          stateEl.hidden = true;
          stateEl.innerHTML = "";
        }
      },
      commit(nextVal) {
        ctrl.setSaving(false);
        setDisplayed(nextVal);
        btn.classList.remove("is-error");
        errEl.hidden = true;
        errEl.textContent = "";
      },
      fail(message, rollbackVal) {
        ctrl.setSaving(false);
        if (rollbackVal != null) setDisplayed(rollbackVal);
        btn.classList.add("is-error");
        errEl.textContent = message || "保存失败，已恢复原权限";
        errEl.hidden = false;
      },
    };

    btn.onclick = () => {
      if (btn.disabled) return;
      const menuItems = opts.map(o => ({ v: o.value, label: o.label, on: o.value === current }));
      openMenu(btn, menuItems, nextVal => {
        if (nextVal === current) return;
        const prevVal = current;
        if (onSelect) onSelect(nextVal, prevVal, ctrl);
        else ctrl.commit(nextVal);
      }, { align, minWidth: Math.max(128, btn.offsetWidth) });
    };

    setDisplayed(current);
    return ctrl;
  }

  /* ---------- 8. v4Dialog（Q8：480/640、bg-elevated、圆角 8、snappy 0.96→1、Esc 取消、删除类不绑 Enter） ---------- */
  let activeDialog = null;

  function closeDialog() {
    if (!activeDialog) return;
    const d = activeDialog;
    activeDialog = null;
    document.removeEventListener("keydown", d.onKey, true);
    d.scrim.remove();
    if (d.prevFocus && d.prevFocus.isConnected && typeof d.prevFocus.focus === "function") {
      try { d.prevFocus.focus({ preventScroll: true }); } catch (_) {}
    }
  }

  function openDialog(opts = {}) {
    closeMenus();
    closeDialog();
    const prevFocus = document.activeElement;
    const {
      title = "",
      size = 480, // 480 | 640 | "sm" | "lg"
      leading = "",
      body = "",
      cancelText = "取消",
      confirmText = "确定",
      hideCancel = false,
      hideConfirm = false,
      danger = false,
      confirmDisabled = false,
      closable = true,
      hint = "",
      onMount,
      onConfirm,
      onCancel,
    } = opts;

    const isLarge = size === 640 || size === "640" || size === "lg";
    const scrim = document.createElement("div");
    scrim.className = "v4-dialog-scrim";
    scrim.setAttribute("role", "presentation");

    const panel = document.createElement("div");
    panel.className = "v4-dialog" + (isLarge ? " v4-dialog--640" : "");
    panel.setAttribute("role", "dialog");
    panel.setAttribute("aria-modal", "true");
    panel.setAttribute("tabindex", "-1");
    if (title) panel.setAttribute("aria-label", title);

    panel.innerHTML = `
      <div class="v4-dialog-head">
        ${leading ? `<span class="v4-row-lead">${leading}</span>` : ""}
        <h2 class="v4-dialog-title">${esc(title)}</h2>
        ${closable ? `<button type="button" class="v4-dialog-close" aria-label="关闭">${CLOSE_SVG}</button>` : ""}
      </div>
      <div class="v4-dialog-body"></div>
      <div class="v4-dialog-foot">
        ${hint ? `<span class="v4-dialog-hint">${esc(hint)}</span>` : ""}
        ${hideCancel ? "" : `<button type="button" class="v4-btn" data-dlg-cancel>${esc(cancelText)}</button>`}
        ${hideConfirm ? "" : `<button type="button" class="v4-btn ${danger ? "v4-btn--danger" : "v4-btn--primary"}" data-dlg-confirm${confirmDisabled ? " disabled" : ""}>${esc(confirmText)}</button>`}
      </div>
    `;

    const bodyEl = panel.querySelector(".v4-dialog-body");
    if (typeof body === "string") bodyEl.innerHTML = body;
    else if (body instanceof Node) bodyEl.appendChild(body);

    scrim.appendChild(panel);
    getWindowEl().appendChild(scrim);

    const confirmBtn = panel.querySelector("[data-dlg-confirm]");
    const cancelBtn = panel.querySelector("[data-dlg-cancel]");
    const closeBtn = panel.querySelector(".v4-dialog-close");

    const api = {
      el: panel,
      bodyEl,
      scrim,
      close() { closeDialog(); },
      setConfirmDisabled(v) { if (confirmBtn) confirmBtn.disabled = !!v; },
      setConfirmText(t) { if (confirmBtn) confirmBtn.textContent = t; },
    };

    const handleCancel = () => {
      if (!closable) return;
      closeDialog();
      if (onCancel) onCancel();
    };
    const handleConfirm = () => {
      if (!confirmBtn || confirmBtn.disabled) return;
      if (onConfirm) {
        const res = onConfirm(api);
        if (res === false) return;
      }
      closeDialog();
    };

    if (cancelBtn) cancelBtn.onclick = handleCancel;
    if (closeBtn) closeBtn.onclick = handleCancel;
    if (confirmBtn) confirmBtn.onclick = handleConfirm;
    scrim.addEventListener("pointerdown", e => {
      if (e.target === scrim && closable) handleCancel();
    });

    const onKey = e => {
      if (e.key === "Escape" && closable) {
        e.preventDefault();
        e.stopPropagation();
        handleCancel();
        return;
      }
      if (e.key === "Enter") {
        const active = document.activeElement;
        const inPanel = panel.contains(active);
        // Q8：删除类弹窗（danger: true）明确不绑定回车确认，同时拦截防止击穿背景按钮
        if (danger) {
          if (inPanel && (active === cancelBtn || active === closeBtn)) return;
          e.preventDefault();
          e.stopPropagation();
          return;
        }
        if (hideConfirm) {
          if (!inPanel) { e.preventDefault(); e.stopPropagation(); }
          return;
        }
        const tag = (active && active.tagName || "").toLowerCase();
        if (inPanel && tag === "textarea" && !(e.metaKey || e.ctrlKey)) return;
        if (inPanel && tag === "button" && active !== confirmBtn) return;
        e.preventDefault();
        e.stopPropagation();
        handleConfirm();
      }
    };
    document.addEventListener("keydown", onKey, true);
    activeDialog = { scrim, panel, onKey, prevFocus };

    if (onMount) onMount(panel, api);
    // 若 onMount 未把焦点移入弹窗内部控件，则把焦点移到弹窗本身，避免停留在背景触发按钮上
    if (!panel.contains(document.activeElement)) {
      panel.focus({ preventScroll: true });
    }
    return api;
  }

  return {
    TRI_ACCESS_OPTIONS,
    rowHTML,
    groupHTML,
    buttonHTML,
    switchHTML,
    bindSwitch,
    segmentedHTML,
    layoutSegmented,
    bindSegmented,
    skeletonRowHTML,
    skeletonGroupHTML,
    selectHTML,
    triSelectHTML,
    bindSelect,
    openMenu,
    openContextMenu,
    closeMenus,
    openDialog,
    closeDialog,
  };
})();
