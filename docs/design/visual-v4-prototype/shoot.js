// 原型自查截图（建议输出至过程目录）：
// 默认模式：node shoot.js ../../process/visual-v4-prototype-shots [--port 18790]
// Round2 模式：node shoot.js --round2 [--port 18792] [outDir]
const fs = require("fs");
const path = require("path");
const assert = require("assert");
const pw = require(path.resolve(__dirname, "../../../web/official-graph/node_modules/.pnpm/playwright-core@1.58.2/node_modules/playwright-core"));

function parseArgs(argv) {
  let round2 = false;
  let port = null;
  let baseUrl = process.env.BASE_URL || "";
  const positional = [];
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a === "--round2") {
      round2 = true;
    } else if (a === "--port" && argv[i + 1]) {
      port = argv[++i];
    } else if (a.startsWith("--port=")) {
      port = a.slice("--port=".length);
    } else if (a === "--base-url" && argv[i + 1]) {
      baseUrl = argv[++i];
    } else if (a.startsWith("--base-url=")) {
      baseUrl = a.slice("--base-url=".length);
    } else {
      positional.push(a);
    }
  }
  if (!baseUrl) {
    baseUrl = `http://127.0.0.1:${port || (round2 ? "18792" : "18790")}`;
  }
  baseUrl = baseUrl.replace(/\/+$/, "");
  const out = positional[0] || (round2 ? path.resolve(__dirname, "../../process/visual-v4-prototype-shots/round2") : "");
  return { round2, baseUrl, out };
}

async function waitSettled(page, ms = 700) {
  await page.evaluate(() => (document.fonts ? document.fonts.ready : Promise.resolve()));
  await page.waitForTimeout(ms);
}

async function runDefault(baseUrl, out) {
  if (out) fs.mkdirSync(out, { recursive: true });
  const browser = await pw.chromium.launch({ channel: "chrome" });
  const page = await browser.newPage({ viewport: { width: 1280, height: 920 }, deviceScaleFactor: 2 });
  const errors = [];
  page.on("pageerror", e => errors.push(String(e)));
  page.on("console", m => m.type() === "error" && errors.push(m.text()));
  const win = name => page.locator("#window").screenshot({ path: `${out}/${name}.png` });
  const open = async q => { await page.goto(`${baseUrl}/?${q}`); await page.waitForTimeout(1500); };
  await open("theme=light&page=overview"); await win("overview-light");
  await open("theme=dark&page=overview"); await win("overview-dark");
  await open("theme=light&page=project"); await win("three-light");
  await page.click("#catBtn"); await page.waitForTimeout(500); await win("three-menu"); await page.mouse.click(5, 5);
  await page.click("#unreadSw"); await page.waitForTimeout(900); await win("three-unread");
  await open("theme=dark&page=project"); await win("three-dark");
  await open("theme=light&page=overview");
  await page.click("[data-recall]"); await page.waitForTimeout(1000); await win("three-recall");
  console.log("errors:", JSON.stringify(errors));
  await browser.close();
}

const ROUND2_SHOTS = [
  { name: "manage-alerts-light", query: "theme=light&page=project&project=memlane&tab=manage", target: "#window" },
  { name: "manage-alerts-dark", query: "theme=dark&page=project&project=memlane&tab=manage", target: "#window" },
  { name: "manage-healthy-light", query: "theme=light&page=project&project=daily-cli&tab=manage", target: "#window", scrollBottom: true },
  { name: "manage-msg-dialog-light", query: "theme=light&page=project&project=memlane&tab=manage&dialog=msg", target: "#window" },
  { name: "manage-msg-dialog-dark", query: "theme=dark&page=project&project=memlane&tab=manage&dialog=msg", target: "#window" },
  { name: "menubar-flyout-light", query: "theme=light&page=overview&menubar=1", target: ".desk", hideProtoBar: true },
  { name: "menubar-flyout-dark", query: "theme=dark&page=overview&menubar=1", target: ".desk", hideProtoBar: true },
  { name: "profile-cn-light", query: "theme=light&page=profile&profile=prof-antigravity", target: "#window" },
  { name: "profile-cn-dark", query: "theme=dark&page=profile&profile=prof-antigravity", target: "#window" },
  { name: "profile-codex-light", query: "theme=light&page=profile&profile=prof-codex", target: "#window" },
  { name: "profile-chatgpt-light", query: "theme=light&page=profile&profile=prof-chatgpt", target: "#window" },
  { name: "common-reader-light", query: "theme=light&page=common", target: "#window" },
  { name: "common-reader-dark", query: "theme=dark&page=common", target: "#window" },
  { name: "common-editor-light", query: "theme=light&page=common&editor=1", target: "#window" },
  { name: "graph-entity-light", query: "theme=light&page=project&project=memlane&tab=graph", target: "#window" },
  { name: "graph-entity-dark", query: "theme=dark&page=project&project=memlane&tab=graph", target: "#window" },
  { name: "graph-code-empty-light", query: "theme=light&page=project&project=memlane&tab=graph&graphMode=code", target: "#window" },
  { name: "cmdk-resume-light", query: "theme=light&page=overview&cmdk=1&q=AnimationFrame", target: "#window" },
  { name: "cmdk-resume-dark", query: "theme=dark&page=overview&cmdk=1&q=AnimationFrame", target: "#window" },
];

async function runRound2(baseUrl, out) {
  fs.mkdirSync(out, { recursive: true });
  const browser = await pw.chromium.launch({ channel: "chrome" });
  const page = await browser.newPage({ viewport: { width: 1280, height: 960 }, deviceScaleFactor: 2 });
  const errors = [];
  page.on("pageerror", e => errors.push(`pageerror: ${e.stack || e}`));
  page.on("console", m => {
    if (m.type() === "error") errors.push(`console.error: ${m.text()}`);
  });

  const open = async (q, settleMs = 700) => {
    await page.goto(`${baseUrl}/?${q}`, { waitUntil: "load" });
    await waitSettled(page, settleMs);
  };

  // Remove deprecated profile-legacy-compat-light.png if present
  const deprecatedShot = path.join(out, "profile-legacy-compat-light.png");
  if (fs.existsSync(deprecatedShot)) fs.unlinkSync(deprecatedShot);

  // 1. Capture all 19 screenshots
  const captured = [];
  for (const shot of ROUND2_SHOTS) {
    await open(shot.query, 700);
    if (shot.hideProtoBar) {
      await page.evaluate(() => {
        const bar = document.querySelector(".proto-bar");
        if (bar) bar.style.display = "none";
        window.scrollTo(0, 0);
      });
      await page.waitForTimeout(150);
    }
    if (shot.scrollBottom) {
      await page.evaluate(() => {
        const scrollers = [
          document.querySelector("#projectTabBody"),
          document.querySelector(".manage-wrap"),
          document.querySelector(".v4-settings-scroll"),
          document.querySelector("#main"),
        ].filter(Boolean);
        for (const el of scrollers) {
          if (el.scrollHeight > el.clientHeight) {
            el.scrollTop = el.scrollHeight;
          }
        }
      });
      await page.waitForTimeout(200);
    }
    const outPath = path.join(out, `${shot.name}.png`);
    await page.locator(shot.target).screenshot({ path: outPath });
    captured.push(outPath);
    console.log(`[shot] ${shot.name}.png (${shot.target})`);
  }

  // 2. Interactive smoke assertions
  const results = [];
  const check = async (name, fn) => {
    try {
      await fn();
      results.push({ name, ok: true });
      console.log(`[smoke PASS] ${name}`);
    } catch (err) {
      results.push({ name, ok: false, error: err && err.stack ? err.stack : String(err) });
      console.error(`[smoke FAIL] ${name}:`, err);
      throw err;
    }
  };

  // Smoke 1 & 2: Sidebar .proj-alert-dot jump + acknowledge all active alerts on memlane
  await check("sidebar-alert-dot-jump-and-ack", async () => {
    await open("theme=light&page=overview", 500);
    const dot = page.locator('.proj-alert-dot[data-alert-project="memlane"]');
    assert.strictEqual(await dot.count(), 1, "Expected .proj-alert-dot on memlane");
    await dot.click();
    await waitSettled(page, 400);

    // Should be on memlane project -> 管理 tab with messages section visible
    const msgGroup = page.locator(".manage-msg-group, #projectMessagesGroup, .proj-msg-group").first();
    assert.strictEqual(await msgGroup.isVisible(), true, "Expected topped 管理 messages section to be visible");

    // Acknowledge all active alerts on memlane
    let guard = 0;
    while (guard < 20) {
      const ackBtn = page.locator("[data-ack-notice]").first();
      if ((await ackBtn.count()) === 0) break;
      await ackBtn.click();
      await page.waitForTimeout(150);
      guard++;
    }
    assert.ok(guard > 0, "Expected at least one active alert to acknowledge on memlane");
    await page.waitForTimeout(250);

    // Sidebar dot for memlane must be cleared
    assert.strictEqual(
      await page.locator('.proj-alert-dot[data-alert-project="memlane"]').count(),
      0,
      "Expected .proj-alert-dot for memlane to be cleared after acking all alerts"
    );

    // Open menubar flyout and verify memlane is removed from alerts list
    await page.click("#menubarTrayBtn");
    await page.waitForTimeout(300);
    assert.strictEqual(
      await page.locator('.mb-alert-row[data-mb-project="memlane"]').count(),
      0,
      "Expected memlane to be removed from menubar flyout alerts list"
    );
  });

  // Smoke 3: Menubar flyout theme + keepAlive/openAtLogin bidirectional sync with 设置 › 服务
  await check("menubar-flyout-bidirectional-sync", async () => {
    await open("theme=light&page=settings&menubar=1", 500);
    // Go to 服务 tab via menubar link or directly
    await page.click("#mbGoServiceBtn");
    await waitSettled(page, 400);

    // Re-open menubar flyout
    await page.click("#menubarTrayBtn");
    await page.waitForTimeout(300);

    // Switch theme to dark via menubar segmented control
    await page.locator('#mbThemeSeg button[data-v="dark"]').click();
    await page.waitForTimeout(200);
    assert.strictEqual(
      await page.locator("html").getAttribute("data-theme"),
      "dark",
      "Expected html[data-theme='dark'] after switching theme in menubar"
    );

    // Switch theme back to light
    await page.locator('#mbThemeSeg button[data-v="light"]').click();
    await page.waitForTimeout(200);
    assert.strictEqual(
      await page.locator("html").getAttribute("data-theme"),
      "light",
      "Expected html[data-theme='light'] after switching theme in menubar"
    );

    // Check initial states of keepAlive & openAtLogin in menubar vs settings
    const getChecked = async sel => (await page.locator(sel).getAttribute("aria-checked")) === "true";
    const initKeep = await getChecked("#mbKeepAliveSwitch");
    const initLogin = await getChecked("#mbOpenAtLoginSwitch");
    assert.strictEqual(await getChecked("#swTrayKeepAlive"), initKeep, "Initial keepAlive mismatch");
    assert.strictEqual(await getChecked("#swTrayOpenAtLogin"), initLogin, "Initial openAtLogin mismatch");

    // Toggle in menubar -> verify settings updates
    await page.click("#mbKeepAliveSwitch");
    await page.click("#mbOpenAtLoginSwitch");
    await page.waitForTimeout(250);
    assert.strictEqual(await getChecked("#swTrayKeepAlive"), !initKeep, "Settings #swTrayKeepAlive did not sync from menubar");
    assert.strictEqual(await getChecked("#swTrayOpenAtLogin"), !initLogin, "Settings #swTrayOpenAtLogin did not sync from menubar");

    // Close menubar, toggle in settings -> re-open menubar and verify sync back
    await page.click("#menubarTrayBtn");
    await page.waitForTimeout(200);
    await page.click("#swTrayKeepAlive");
    await page.click("#swTrayOpenAtLogin");
    await page.waitForTimeout(250);
    await page.click("#menubarTrayBtn");
    await page.waitForTimeout(300);
    assert.strictEqual(await getChecked("#mbKeepAliveSwitch"), initKeep, "Menubar #mbKeepAliveSwitch did not sync from settings");
    assert.strictEqual(await getChecked("#mbOpenAtLoginSwitch"), initLogin, "Menubar #mbOpenAtLoginSwitch did not sync from settings");
  });

  // Smoke 4: 通用记忆 edit/save + delete 480 danger dialog Enter does NOT delete
  await check("common-memory-edit-save-and-danger-enter", async () => {
    await open("theme=light&page=common", 500);
    const initialCount = await page.locator(".cm-item").count();
    assert.ok(initialCount > 0, "Expected common memory items");

    // Click edit, modify title, save
    await page.click("#commonEditBtn");
    await page.waitForTimeout(250);
    const newTitle = "中文简洁汇报与结论前置（QA已更新）";
    await page.fill("#cmEditTitle", newTitle);
    await page.click("#cmSaveBtn");
    await page.waitForTimeout(300);
    const titleText = await page.locator(".cm-title").textContent();
    assert.strictEqual(titleText.trim(), newTitle, "Expected saved common memory title to update");

    // Open delete 480 danger dialog and press Enter -> must NOT delete
    await page.click("#commonDeleteBtn");
    await page.waitForTimeout(250);
    assert.strictEqual(await page.locator(".v4-dialog-scrim").isVisible(), true, "Expected delete danger dialog to open");
    await page.keyboard.press("Enter");
    await page.waitForTimeout(300);
    assert.strictEqual(
      await page.locator(".cm-item").count(),
      initialCount,
      "Pressing Enter in danger delete dialog must NOT delete the item"
    );
  });

  // Smoke 5: 项目 › 图谱: switch to 代码图谱 -> empty state -> enable -> dark canvas -> click entity node -> inspector + 查看来源记忆
  await check("project-graph-code-enable-and-entity-inspector", async () => {
    await open("theme=light&page=project&project=memlane&tab=graph", 500);

    // Switch to 代码图谱
    await page.locator('#pgModeSeg button[data-v="code"]').click();
    await page.waitForTimeout(350);
    const enableBtn = page.locator("#pgEnableCodeBtn");
    assert.strictEqual(await enableBtn.isVisible(), true, "Expected 开启代码图谱 button in empty state");

    // Click 开启代码图谱 -> dark graph canvas appears
    await enableBtn.click();
    await page.waitForTimeout(400);
    assert.strictEqual(await page.locator("#pgCanvasWrap").isVisible(), true, "Expected dark graph canvas after enabling code graph");

    // Switch back to 实体关系 and click an entity node
    await page.locator('#pgModeSeg button[data-v="entity"]').click();
    await page.waitForTimeout(350);
    const firstNode = page.locator("#pgSvg [data-node-id]").first();
    assert.strictEqual(await firstNode.count(), 1, "Expected entity graph nodes");
    await firstNode.click();
    await page.waitForTimeout(300);

    // Node inspector & 查看来源记忆 should be visible
    assert.strictEqual(await page.locator("#pgNodePanel").isVisible(), true, "Expected node inspector panel (#pgNodePanel)");
    assert.strictEqual(await page.locator("#pgJumpSourceBtn").isVisible(), true, "Expected 查看来源记忆 button (#pgJumpSourceBtn)");
  });

  // Smoke 6: ?reduce=1&page=overview&menubar=1 runs with zero errors
  await check("reduce-motion-overview-menubar", async () => {
    await open("reduce=1&page=overview&menubar=1", 500);
    assert.strictEqual(await page.locator(".macos-menubar").isVisible(), true, "Expected macOS menubar");
  });

  // Smoke 7: Standalone 检索结果 page removed + ?page=search redirects cleanly to overview
  await check("search-page-removed", async () => {
    await open("theme=light&page=search", 400);
    assert.strictEqual(
      await page.locator('[data-page-choice="search"]').count(),
      0,
      'Expected [data-page-choice="search"] to NOT exist'
    );
    assert.strictEqual(
      await page.locator("#cmdkOpenAll").count(),
      0,
      "Expected #cmdkOpenAll to NOT exist"
    );
    assert.strictEqual(
      await page.locator('.nav-item.on[data-nav="overview"]').count(),
      1,
      "Expected ?page=search to redirect cleanly to overview"
    );
  });

  // Smoke 8: ⌘K search, reopen context resume, clear, and commonMemories navigation
  await check("cmdk-search-reopen-resume-and-common", async () => {
    await open("theme=light&page=overview", 400);
    await page.click("#gsearch");
    await page.waitForTimeout(250);
    assert.strictEqual(await page.locator("#cmdkOpenAll").count(), 0, "Expected #cmdkOpenAll absent in ⌘K");

    await page.fill("#cmdkInput", "AnimationFrame");
    await page.waitForTimeout(350);
    await page.keyboard.press("ArrowDown");
    await page.waitForTimeout(150);

    const activeIndexBefore = await page.evaluate(() => {
      const items = [...document.querySelectorAll("#cmdkList button, #cmdkList [role='option']")];
      return items.findIndex(el => el.classList.contains("on") || el.classList.contains("is-active") || el.getAttribute("aria-selected") === "true");
    });
    assert.ok(activeIndexBefore >= 0, "Expected an active highlighted item in #cmdkList");

    await page.keyboard.press("Enter");
    await waitSettled(page, 350);

    // Reopen ⌘K via Meta+k and verify last query & highlighted selection are preserved
    await page.keyboard.press("Meta+k");
    await page.waitForTimeout(250);
    assert.strictEqual(
      await page.locator("#cmdkInput").inputValue(),
      "AnimationFrame",
      "Expected ⌘K to preserve last query on reopen"
    );
    const activeIndexAfter = await page.evaluate(() => {
      const items = [...document.querySelectorAll("#cmdkList button, #cmdkList [role='option']")];
      return items.findIndex(el => el.classList.contains("on") || el.classList.contains("is-active") || el.getAttribute("aria-selected") === "true");
    });
    assert.strictEqual(activeIndexAfter, activeIndexBefore, "Expected ⌘K to preserve highlighted selection on reopen");

    // Click #cmdkClear -> verify query clears and recent list returns
    await page.click("#cmdkClear");
    await page.waitForTimeout(200);
    assert.strictEqual(await page.locator("#cmdkInput").inputValue(), "", "Expected #cmdkClear to clear query");
    assert.ok(
      (await page.locator("#cmdkList button, #cmdkList [role='option']").count()) > 0,
      "Expected recent list to return after clearing query"
    );

    // Search a commonMemories keyword, select the matching 通用记忆 result, and hit Enter -> lands on 通用记忆 with that item selected
    await page.fill("#cmdkInput", "中文技术沟通先给结论");
    await page.waitForTimeout(350);
    for (let step = 0; step < 10; step++) {
      const activeText = await page.evaluate(() => {
        const el = document.querySelector("#cmdkList .on, #cmdkList .is-active, #cmdkList [aria-selected='true']");
        return el ? el.textContent || "" : "";
      });
      if (activeText.includes("中文技术沟通先给结论")) break;
      await page.keyboard.press("ArrowDown");
      await page.waitForTimeout(80);
    }
    await page.keyboard.press("Enter");
    await waitSettled(page, 400);
    assert.strictEqual(
      await page.locator('.nav-item.on[data-nav="common"]').count(),
      1,
      "Expected Enter on commonMemories result to navigate to 通用记忆"
    );
    const activeCmText = await page.locator(".cm-item.on").textContent();
    assert.ok(activeCmText && activeCmText.includes("中文技术沟通先给结论"), "Expected matching common memory item to be selected");
  });

  // Smoke 9: Unified User Profile Markdown across all 4 ready sources
  await check("profile-unified-markdown", async () => {
    for (const profId of ["prof-antigravity", "prof-codex", "prof-claude", "prof-chatgpt"]) {
      await open(`theme=light&page=profile&profile=${profId}`, 350);
      assert.strictEqual(
        await page.locator(".profile-md").isVisible(),
        true,
        `Expected .profile-md to render for ${profId}`
      );
      assert.strictEqual(
        await page.locator(".profile-entry-date").count(),
        0,
        `Expected ZERO .profile-entry-date elements for ${profId}`
      );
      const mainText = (await page.locator("#main").textContent()) || "";
      assert.strictEqual(
        mainText.includes("条记录"),
        false,
        `Expected ZERO '条记录' text in profile column/meta for ${profId}`
      );
      assert.strictEqual(
        await page.locator("#profileCopyBtn").isEnabled(),
        true,
        `Expected #profileCopyBtn to be enabled for ${profId}`
      );
    }
  });

  await browser.close();

  if (errors.length > 0) {
    console.error("Runtime errors detected:", JSON.stringify(errors, null, 2));
    process.exit(1);
  }
  console.log(`ALL OK: ${captured.length} screenshots captured, ${results.length} smoke suites passed, 0 runtime errors.`);
}

(async () => {
  const { round2, baseUrl, out } = parseArgs(process.argv.slice(2));
  if (round2) {
    await runRound2(baseUrl, out);
  } else {
    await runDefault(baseUrl, out);
  }
})().catch(err => {
  console.error(err);
  process.exit(1);
});
