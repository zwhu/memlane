/** Exercise the real installed pi loader and lifecycle with a synthetic helper.
 * This proves loader/protocol compatibility, not model adoption or Cognee E2E.
 */
import assert from 'node:assert/strict';
import { cp, mkdir, mkdtemp, readFile, realpath, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const repository = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const packageRoot = process.argv[2];
const python = process.argv[3];
if (!packageRoot || !python) throw new Error('usage: node test_pi_extension.mjs <installed-pi-package> <python>');
const root = await realpath(await mkdtemp(join(tmpdir(), 'memlane-pi-extension-')));
try {
  // Imports and helper children run with isolated logs/cache/home and no keys.
  for (const key of Object.keys(process.env)) {
    if (/^(COGNEE_|MEMLANE_|PI_|LLM_|EMBEDDING_|OPENAI_|ANTHROPIC_|GEMINI_)/.test(key)) delete process.env[key];
  }
  process.env.HOME = join(root, 'home');
  process.env.XDG_CACHE_HOME = join(root, 'cache');
  await mkdir(process.env.HOME);
  await cp(join(repository, 'engine/agents/pi'), join(root, 'extension'), { recursive: true });
  await mkdir(join(root, 'scripts'));
  await writeFile(join(root, 'host-binding.json'), JSON.stringify({ python }));
  await writeFile(join(root, 'scripts/agent_memory_adapter.py'), `
import json, pathlib, sys
root = pathlib.Path(__file__).resolve().parents[1]
request = json.load(sys.stdin)
with (root / 'calls.jsonl').open('a') as stream: stream.write(json.dumps(request) + '\\n')
print(json.dumps({'status':'complete', 'context':'' if (root / 'denied').exists() else 'synthetic project lesson'}))
`);
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
  assert.equal(context.messages[0].content, 'synthetic project lesson');
  await writeFile(join(root, 'denied'), '');
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
  const calls = (await readFile(join(root, 'calls.jsonl'), 'utf8')).trim().split('\n').map(JSON.parse);
  const traces = calls.filter(c => c.operation === 'capture' && c.entry.type === 'trace');
  assert.equal(traces.length, 1);
  assert.equal(traces[0].entry.status, 'error');
  assert.deepEqual(traces[0].entry.method_params, { command: 'synthetic' });
  const qa = calls.find(c => c.operation === 'capture' && c.entry.type === 'qa');
  assert.equal(qa.entry.question, 'synthetic question');
  assert.equal(qa.entry.answer, 'synthetic answer');
  const prompt = calls.find(c => c.operation === 'resolve' && c.event_id);
  assert.equal(qa.turn_event_id, prompt.event_id);
  assert.equal(traces[0].turn_event_id, prompt.event_id);
  assert.equal(calls.filter(c => c.operation === 'flush').length, 2);
  assert.ok(calls.every(c => c.session_id === 'synthetic-session' && c.cwd === root));
  console.log(JSON.stringify({ status: 'passed', boundary: 'real_pi_loader_synthetic_memory_helper', tools: 4, trace: 1, qa: 1, revocation_removes_context: true }));
} finally {
  await rm(root, { recursive: true, force: true });
}
