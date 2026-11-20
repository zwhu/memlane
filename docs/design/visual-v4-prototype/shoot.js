// 原型自查截图：node shoot.js <outdir>
const path = require("path");
const pw = require(path.resolve(__dirname, "../../../web/official-graph/node_modules/.pnpm/playwright-core@1.58.2/node_modules/playwright-core"));
(async () => {
  const out = process.argv[2];
  const browser = await pw.chromium.launch({ channel: "chrome" });
  const page = await browser.newPage({ viewport: { width: 1280, height: 920 }, deviceScaleFactor: 2 });
  const errors = [];
  page.on("pageerror", e => errors.push(String(e)));
  page.on("console", m => m.type() === "error" && errors.push(m.text()));
  const win = name => page.locator("#window").screenshot({ path: `${out}/${name}.png` });
  const open = async q => { await page.goto(`http://127.0.0.1:18790/?${q}`); await page.waitForTimeout(1500); };
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
})();
