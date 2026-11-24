// Actual browser/official Canvas with synthetic graph data; no backend/model.
import { chromium } from 'playwright-core';
import { createServer } from 'node:http';
import { readFile, mkdir, writeFile } from 'node:fs/promises';
import path from 'node:path';
import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';

process.chdir(path.dirname(fileURLToPath(import.meta.url)));

const [executablePath, output] = process.argv.slice(2);
if (!executablePath || !output) throw Error('usage: node verify.mjs <installed-browser> <private-output>');
await mkdir(output, {recursive: true, mode: 0o700});
const root = path.resolve('dist');
const server = createServer(async (req, res) => {
  try {
    const name = new URL(req.url, 'http://localhost').pathname;
    const file = path.resolve(root, '.' + (name === '/' ? '/index.html' : name));
    if (!file.startsWith(root + path.sep)) { res.writeHead(403).end(); return; }
    const mime = file.endsWith('.js') ? 'text/javascript' : file.endsWith('.css') ? 'text/css' : 'text/html';
    const content = await readFile(file);
    res.writeHead(200, {'Content-Type': mime});
    res.end(content);
  } catch {
    if (!res.headersSent) res.writeHead(404);
    res.end();
  }
});
await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
const origin = `http://127.0.0.1:${server.address().port}`;
const errors = [], external = [], selections = [];
let browser;
try {
  browser = await chromium.launch({executablePath, headless: true});
  const page = await browser.newPage({viewport: {width: 1100, height: 720}, reducedMotion: 'reduce'});
  page.on('pageerror', (error) => errors.push(error.message));
  page.on('request', (request) => {if (!request.url().startsWith(origin + '/')) external.push(request.url());});
  await page.exposeFunction('recordHostEvent', (name, value) => selections.push({name, value}));
  await page.addInitScript(() => {window.mygo = {call: async (name, value) => window.recordHostEvent(name, value)};});
  await page.goto(origin);
  await page.getByText('等待当前项目的官方图谱').waitFor();
  const frame = {epoch: 1, project: 'synthetic-project', instance: 'synthetic-instance', dataset: 'synthetic-dataset', payload: {
    nodes: [
      {id:'doc', name:'Synthetic coding lesson', type:'TextDocument', stage:'document', belongs_to_set:['session_learnings']},
      {id:'chunk',name:'Synthetic chunk',type:'DocumentChunk',stage:'chunk',belongs_to_set:['session_learnings']},
      {id:'a',name:'PaymentService.charge',type:'CodeSymbol',stage:'other',source_node_set:'synthetic-repo'},
      {id:'b',name:'validateAmount',type:'CodeSymbol',stage:'other',source_node_set:'synthetic-repo'},
    ], links: [
      {source:'doc',target:'chunk',relation:'has_chunk',edge_class:'structural'},
      {source:'chunk',target:'a',relation:'mentions',edge_class:'semantic'},
      {source:'a',target:'b',relation:'calls',edge_class:'semantic'},
    ], color_maps: {node_set:{'synthetic-repo':'#43D9E8','session_learnings':'#56DB7D'}},
  }};
  assert.equal(await page.evaluate((value) => window.memlaneOfficialGraph.apply(value), frame), true);
  await page.locator('canvas').waitFor();
  await page.getByRole('button', {name:'适应窗口'}).click();
  await page.waitForTimeout(800);
  const canvas = await page.locator('canvas').evaluate((element) => {
    const context = element.getContext('2d');
    const bytes = context.getImageData(0,0,element.width,element.height).data;
    let drawn = 0;
    for (let i=0; i<bytes.length; i+=4) if (bytes[i+3] && (bytes[i]>45 || bytes[i+1]>55 || bytes[i+2]>70)) drawn++;
    return {width:element.width,height:element.height,drawn};
  });
  assert(canvas.width > 500 && canvas.height > 300 && canvas.drawn > 100, 'official canvas did not draw');
  await page.screenshot({path:path.join(output,'official-canvas.png')});
  // Locate the two synthetic CodeSymbol squares from the actual painted pixels,
  // then exercise the renderer's own hit testing and selection callbacks.
  const centers = await page.locator('canvas').evaluate((element) => {
    const {width,height} = element;
    const data = element.getContext('2d').getImageData(0,0,width,height).data;
    const columns = new Map();
    for (let y=Math.floor(height*.38); y<height*.58; y++) for (let x=Math.floor(width*.3);x<width*.7;x++) {
      const i=(y*width+x)*4;
      if (data[i+3]>240 && Math.abs(data[i]-67)<3 && Math.abs(data[i+1]-217)<3 && Math.abs(data[i+2]-232)<3) {
        const list=columns.get(x)??[]; list.push(y); columns.set(x,list);
      }
    }
    const groups=[];
    for (const x of [...columns.keys()].sort((a,b)=>a-b)) {
      let group=groups.at(-1);
      if (!group || x>group.at(-1)+1) {group=[];groups.push(group);}
      group.push(x);
    }
    return groups.filter((group)=>group.length>=5).map((group)=>{
      const x=group[Math.floor(group.length/2)], ys=columns.get(x);
      return {x,y:ys[Math.floor(ys.length/2)]};
    });
  });
  assert.equal(centers.length,2,'code symbols were not visible for pointer verification');
  await page.mouse.click(centers[0].x,centers[0].y);
  try {
    await page.getByText(/used in 0 answers/i).waitFor({timeout:5000});
  } catch (error) {
    await page.screenshot({path:path.join(output,'selection-failure.png')});
    await writeFile(path.join(output,'selection-failure.json'),JSON.stringify({text:await page.locator('body').innerText(),selections,errors,centers},null,2)+'\n');
    throw error;
  }
  assert(selections.some((event)=>event.name==='OfficialGraph.Select'
    && event.value?.epoch===1 && event.value?.project==='synthetic-project'
    && event.value?.instance==='synthetic-instance' && event.value?.dataset==='synthetic-dataset'
    && ['a','b'].includes(event.value?.node)));
  await page.keyboard.down('Shift');
  await page.mouse.click(centers[1].x,centers[1].y);
  await page.keyboard.up('Shift');
  await page.getByText(/path to .* — 1 hop/).waitFor();
  await page.screenshot({path:path.join(output,'official-selection.png')});
  await page.getByRole('button',{name:'查看节点',exact:true}).click();
  await page.waitForTimeout(300);
  await page.screenshot({path:path.join(output,'official-records.png')});
  assert.equal(await page.evaluate(() => window.memlaneOfficialGraph.clear(2)), true);
  await page.getByText('等待当前项目的官方图谱').waitFor();
  assert.equal(await page.locator('canvas').count(),0);
  assert.equal(await page.evaluate((value) => window.memlaneOfficialGraph.apply(value), frame),false);
  assert.equal(await page.evaluate((value) => window.memlaneOfficialGraph.apply({...value,epoch:2}), frame),false);
  assert.equal(await page.evaluate((value) => window.memlaneOfficialGraph.apply({...value,epoch:3,dataset:'other-authorized-dataset'}), frame),true);
  await page.setViewportSize({width:760,height:540});
  await page.locator('canvas').waitFor();
  await page.waitForTimeout(300);
  const resized = await page.locator('canvas').evaluate((el) => ({width: el.width, height: el.height}));
  assert(resized.width > 0 && resized.width < canvas.width && resized.height > 0 && resized.height < canvas.height, 'canvas did not resize');
  await page.screenshot({path:path.join(output,'official-resized.png')});
  assert.deepEqual(external,[]); assert.deepEqual(errors,[]);
  await writeFile(path.join(output,'browser.json'),JSON.stringify({scope:'Actual Chromium and unchanged official Canvas; synthetic data; not Native or real SDK graph acceptance',canvas,resized,errors,external,ready:selections.some((x)=>x.name==='OfficialGraph.Ready'),pointer_selection:true,shortest_path:true,clear_and_stale_rejection:true,resize:true},null,2)+'\n');
} finally {
  if (browser) await browser.close();
  await new Promise((resolve) => server.close(resolve));
}
