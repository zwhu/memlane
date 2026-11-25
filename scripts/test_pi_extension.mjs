/** Exercise the installed pi loader against a loopback Go gateway contract. */
import assert from 'node:assert/strict';
import { cp, mkdir, mkdtemp, readFile, realpath, rm, writeFile } from 'node:fs/promises';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const repository = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const packageRoot = process.argv[2];
if (!packageRoot) throw new Error('usage: node test_pi_extension.mjs <installed-pi-package>');
const root = await realpath(await mkdtemp(join(tmpdir(), 'memlane-pi-extension-')));
const calls = [];
let denied = false;
const server = createServer(async (req, res) => {
  let raw = '';
  for await (const chunk of req) raw += chunk;
  calls.push({ path: req.url, token: req.headers['x-api-key'], ...JSON.parse(raw) });
  res.setHeader('Content-Type', 'application/json');
  if (req.url === '/recall') {
    res.statusCode = denied ? 403 : 200;
    res.end(JSON.stringify({ hits: [{ title: 'Project', snippet: 'synthetic lesson' }] }));
  } else if (req.url === '/ingest') {
    res.statusCode = 202;
    res.end(JSON.stringify({ status: 'queued' }));
  } else {
    res.end(JSON.stringify({ jsonrpc: '2.0', id: 1, result: { structuredContent: { ok: true } } }));
  }
});
server.listen(0, '127.0.0.1');
try {
  await once(server, 'listening');
  for (const key of Object.keys(process.env)) {
    if (/^(COGNEE_|MEMLANE_|PI_|LLM_|EMBEDDING_|OPENAI_|ANTHROPIC_|GEMINI_)/.test(key)) delete process.env[key];
  }
  process.env.HOME = join(root, 'home');
  process.env.XDG_CACHE_HOME = join(root, 'cache');
  await mkdir(process.env.HOME);
  await cp(join(repository, 'engine/agents/pi'), join(root, 'extension'), { recursive: true });
  const tokenFile = join(root, 'agent.key');
  await writeFile(tokenFile, 'synthetic-key\n', { mode: 0o600 });
  await writeFile(join(root, 'host-binding.json'), JSON.stringify({
    gateway_url: `http://127.0.0.1:${server.address().port}`, token_file: tokenFile,
  }));
  for (const file of ['index.ts', 'tools.ts']) {
    assert.doesNotMatch(await readFile(join(root, 'extension', file), 'utf8'), /child_process|\bspawn\b|agent_memory_adapter\.py/);
  }
  const { loadExtensions } = await import(pathToFileURL(join(packageRoot, 'dist/core/extensions/loader.js')).href);
  const loaded = await loadExtensions([join(root, 'extension/index.ts')], process.env.HOME);
  assert.deepEqual(loaded.errors, []);
  assert.equal(loaded.extensions.length, 1);
  const extension = loaded.extensions[0];
  const declaredTypes = await readFile(join(packageRoot, 'dist/core/extensions/types.d.ts'), 'utf8');
  for (const event of extension.handlers.keys()) assert.ok(declaredTypes.includes(`on(event: "${event}"`), `undeclared pi event: ${event}`);
  assert.deepEqual([...extension.tools.keys()].sort(), ['cognee_forget', 'cognee_health', 'cognee_recall', 'cognee_remember']);
  const ctx = { cwd: root, sessionManager: { getSessionId: () => 'synthetic-session' } };
  const emit = async (name, value = {}) => {
    let result;
    for (const handler of extension.handlers.get(name) ?? []) result = await handler({ type: name, ...value }, ctx);
    return result;
  };
  await emit('session_start');
  await emit('before_agent_start', { prompt: 'synthetic question' });
  const stale = { role: 'custom', customType: 'memlane-cognee-context', content: 'stale memory', timestamp: 1, display: false };
  let context = await emit('context', { messages: [stale] });
  assert.equal(context.messages.length, 1);
  assert.equal(context.messages[0].content, '- Project: synthetic lesson');
  denied = true;
  context = await emit('context', { messages: context.messages });
  assert.deepEqual(context.messages, []);
  await emit('tool_execution_start', { toolCallId: 'tool-1', toolName: 'bash', args: { command: 'synthetic' } });
  await emit('tool_execution_end', { toolCallId: 'tool-1', toolName: 'bash', result: { text: 'failed' }, isError: true });
  await emit('message_end', { message: { role: 'assistant', stopReason: 'toolUse', content: [{ type: 'text', text: 'intermediate tool commentary' }] } });
  await emit('message_end', { message: { role: 'assistant', stopReason: 'length', content: [{ type: 'text', text: 'truncated answer' }] } });
  await emit('message_end', { message: { role: 'assistant', stopReason: 'stop', content: [{ type: 'text', text: 'synthetic answer' }] } });
  await emit('tool_execution_end', { toolCallId: 'own-tool', toolName: 'cognee_recall', result: { text: 'memory' }, isError: false });
  await emit('agent_end');
  await emit('session_shutdown');
  const traces = calls.filter(c => c.path === '/ingest' && c.kind === 'capture_trace');
  assert.equal(traces.length, 1);
  assert.equal(traces[0].message_id, 'tool:tool-1');
  assert.equal(traces[0].content.status, 'error');
  assert.deepEqual(traces[0].content.method_params, { command: 'synthetic' });
  const qa = calls.filter(c => c.path === '/ingest' && c.kind === 'capture_qa');
  assert.equal(qa.length, 1);
  assert.equal(qa[0].content.question, 'synthetic question');
  assert.equal(qa[0].content.answer, 'synthetic answer');
  assert.ok(qa[0].content.prompt_time);
  const recall = calls.filter(c => c.path === '/recall');
  assert.equal(recall.length, 2);
  assert.equal(recall[0].query, 'synthetic question');
  assert.ok(calls.every(c => c.session_id === 'synthetic-session' && c.workspace === root));
  for (const [name, args] of Object.entries({ health: {}, remember: { data: 'note' }, recall: { query: 'note' }, forget: { data_id: 'item' } })) {
    const entry = extension.tools.get('cognee_' + name);
    const result = await (entry.definition ?? entry).execute('call', args, undefined, undefined, ctx);
    assert.equal(result.details.structuredContent.ok, true);
    assert.equal(calls.at(-1).path, '/mcp');
    assert.equal(calls.at(-1).method, 'tools/call');
    assert.equal(calls.at(-1).params.name, name);
    assert.equal(calls.at(-1).params.arguments.project_path, root);
    if (name === 'forget') assert.equal(calls.at(-1).params.arguments.item_id, 'item');
  }
  assert.ok(calls.every(c => c.token === 'synthetic-key'));
  const requests = calls.length;
  process.env.MEMLANE_PROCESSING_SESSION = '1';
  await emit('before_agent_start', { prompt: 'processing' });
  assert.deepEqual((await emit('context', { messages: [stale] })).messages, []);
  await emit('tool_execution_end', { toolCallId: 'internal', toolName: 'bash' });
  assert.equal(calls.length, requests);
  console.log(JSON.stringify({ status: 'passed', boundary: 'real_pi_loader_loopback_gateway', tools: 4, trace: 1, qa: 1 }));
} finally {
  await new Promise(resolve => server.close(resolve));
  await rm(root, { recursive: true, force: true });
}
