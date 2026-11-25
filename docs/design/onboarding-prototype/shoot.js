// 截图自查：node shoot.js [outDir]（需先在仓库根目录启动 http.server 18795；默认输出至本地忽略的过程目录）
const path = require("path");
const fs = require("fs");
const pw = require(path.resolve(__dirname, "../../../web/official-graph/node_modules/.pnpm/playwright-core@1.58.2/node_modules/playwright-core"));
const BASE = process.env.BASE_URL || "http://127.0.0.1:18795/docs/design/onboarding-prototype/index.html";
const out = process.argv[2] || path.resolve(__dirname, "../../process/onboarding-prototype-shots");
fs.mkdirSync(out, { recursive: true });

const shots = [
  ["01-welcome", "stage=welcome", async p => p.waitForTimeout(1300)],
  ["01b-welcome-agent-install", "stage=welcome", async p => { await p.waitForTimeout(1300); await p.click("[data-act=agentInstall]"); await p.waitForTimeout(500); }],
  ["02-agents", "stage=agents", null],
  ["02b-agents-support-list", "stage=agents", async p => { await p.click("[data-act=supportList]"); await p.waitForTimeout(500); }],
  ["02c-agents-none", "stage=agents&hosts=none", null],
  ["03-env-missing", "stage=env", null],
  ["04-env-installing", "stage=env", async p => { await p.click("[data-act=envPrimary]"); await p.waitForTimeout(2600); }],
  ["04b-env-log", "stage=env", async p => { await p.click("[data-act=envPrimary]"); await p.waitForTimeout(2600); await p.click("[data-disc=log]"); await p.waitForTimeout(400); }],
  ["05-env-fail", "stage=env&env=fail", async p => { await p.click("[data-act=envPrimary]"); await p.waitForTimeout(4200); }],
  ["06-connect", "stage=connect", null],
  ["07-connect-written", "stage=connect", async p => { await p.click("[data-act=connectAll]"); await p.waitForTimeout(2400); }],
  ["08-process-agent", "stage=process", null],
  ["09-process-verifying", "stage=process", async p => { await p.click("[data-act=verify]"); await p.waitForTimeout(1500); }],
  ["10-process-api-fail", "stage=process&verify=fail", async p => { await p.click("[data-mode=api]"); await p.fill("[data-api=key]", "sk-test"); await p.click("[data-act=verify]"); await p.waitForTimeout(3800); }],
  ["11-process-no-cli", "stage=process&cli=none", null],
  ["12-done", "stage=done", async p => p.waitForTimeout(900)],
];

(async () => {
  const browser = await pw.chromium.launch({ channel: "chrome" });
  const themes = (process.env.THEMES || "light,dark").split(",");
  for (const theme of themes) {
    const page = await browser.newPage({ viewport: { width: 1300, height: 920 }, deviceScaleFactor: 2 });
    for (const [name, q, act] of shots.filter(s => !process.env.ONLY || s[0].startsWith(process.env.ONLY))) {
      await page.goto(`${BASE}?theme=${theme}&${q}`);
      await page.evaluate(() => document.fonts.ready);
      await page.waitForTimeout(500);
      if (act) await act(page);
      await page.waitForTimeout(450);
      await page.locator("#win").screenshot({ path: path.join(out, `${theme}-${name}.png`) });
    }
    await page.close();
  }
  await browser.close();
  console.log("ok", out);
})().catch(e => { console.error(e); process.exit(1); });
