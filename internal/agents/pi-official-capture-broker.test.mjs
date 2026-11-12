import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, writeFile, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { PiCaptureCLIBroker } from './pi-official-capture-broker.ts';

const policy = 'aa49445f3d78880d5784c0b47195994aabc88dc32d0a2824a8232d77ad4719b0';
const connection = '42fd1c55-ea63-4c5e-8c7f-f6cc6b6d417a';
const project = '40556711-2cc4-4f6d-b09a-d079491ea2ac';
const library = 'b383db98-f08d-4f94-b2f2-5ce04b52e80a';
const key = 'a'.repeat(64);
const batch = { id: 'b'.repeat(64), entries: [{ operation: 'c'.repeat(64), session: 'pi-' + 'd'.repeat(48), entry: { type: 'qa', question: 'synthetic raw question', answer: 'synthetic raw answer' } }], diagnostics: [] };

async function fixture(t, mode = 'normal') {
  const root = await mkdtemp(join(tmpdir(), 'memlane-pi-broker-test-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  const bridge = join(root, 'synthetic-bridge.mjs'), observed = join(root, 'observed.jsonl');
  const grant = { enabled: true, key, library, policy_digest: policy, binding: { connection_id: connection, generation: 1, project, material_submission: true } };
  const program = `#!${process.execPath}
import { appendFileSync } from 'node:fs';
const args=process.argv.slice(2), action=args[1];
let input=''; for await (const part of process.stdin) input+=part;
const body=JSON.parse(input);
appendFileSync(${JSON.stringify(observed)},JSON.stringify({args,bodyKeys:Object.keys(body),rawQuestion:body.entries?.[0]?.entry.question,hasAmbientCredential:!!process.env.OPENAI_API_KEY})+'\\n');
const mode=${JSON.stringify(mode)};
if(mode==='abort') { setInterval(()=>{},1000); }
else if(mode==='malformed') { process.stdout.write('synthetic-sensitive-invalid-output'); }
else if(mode==='overflow') { process.stdout.write('x'.repeat(300000)); }
else if(mode==='denied') { process.stdout.write(JSON.stringify({protocol_version:1,ok:false,error:{code:'scope_denied',message:'synthetic-sensitive-diagnostic'}})); process.exitCode=1; }
else {
 const result=action==='authorize'?${JSON.stringify(grant)}:{batch:mode==='wrong_batch'?'other':body.batch,operations:body.entries.map(entry=>({operation:entry.operation,state:action==='inspect'?'unknown':'saved',unexpected:'synthetic-sensitive-field'})),unexpected:'synthetic-sensitive-field'};
 process.stdout.write(JSON.stringify({protocol_version:1,ok:true,result}));
}
`;
  await writeFile(bridge, program, { mode: 0o700 });
  const broker = new PiCaptureCLIBroker({ root, bridge, connection_id: connection, generation: 1, policy_digest: policy });
  return { broker, root, observed };
}

test('CLI broker uses stdin, explicit authority and reduced child environment', async t => {
  const prior = process.env.OPENAI_API_KEY; process.env.OPENAI_API_KEY = 'synthetic-ambient-secret';
  t.after(() => { if (prior === undefined) delete process.env.OPENAI_API_KEY; else process.env.OPENAI_API_KEY = prior; });
  const f = await fixture(t), signal = new AbortController().signal;
  const binding = await f.broker.authorize('/synthetic/workspace', 'synthetic-session', signal);
  assert.equal(binding.key, key); assert.equal(binding.workspace, '/synthetic/workspace');
  const saved = await f.broker.submit(batch, binding, signal), unknown = await f.broker.inspect(batch, binding, signal);
  assert.deepEqual(saved, { batch: batch.id, operations: [{ operation: batch.entries[0].operation, state: 'saved' }] });
  assert.equal(unknown.operations[0].state, 'unknown');
  const calls = (await readFile(f.observed, 'utf8')).trim().split('\n').map(JSON.parse);
  assert.deepEqual(calls.map(x => x.args[1]), ['authorize', 'submit', 'inspect']);
  assert.equal(calls[0].args.includes('--project'), false);
  assert.ok(calls[1].args.includes(project));
  assert.equal(calls[1].rawQuestion, 'synthetic raw question');
  for (const call of calls) { assert.equal(call.hasAmbientCredential, false); assert.equal(JSON.stringify(call.args).includes('synthetic raw question'), false); }
  assert.equal(JSON.stringify(saved).includes('sensitive'), false);
});

test('isolated channels authorize nothing and spawn no child', async t => {
  const f = await fixture(t), previous = process.env.MEMLANE_USAGE_CHANNEL;
  process.env.MEMLANE_USAGE_CHANNEL = 'processing';
  t.after(() => { if (previous === undefined) delete process.env.MEMLANE_USAGE_CHANNEL; else process.env.MEMLANE_USAGE_CHANNEL = previous; });
  assert.equal(await f.broker.authorize('/synthetic/workspace', 'session', new AbortController().signal), undefined);
  await assert.rejects(readFile(f.observed));
});

test('explicit no-consent response disables collection without exposing diagnostics', async t => {
  const f = await fixture(t, 'denied');
  assert.equal(await f.broker.authorize('/synthetic/workspace', 'session', new AbortController().signal), undefined);
});

test('foreign workspace/session binding cannot borrow cached authorization', async t => {
  const f = await fixture(t), signal = new AbortController().signal;
  const binding = await f.broker.authorize('/synthetic/one', 'session-one', signal);
  await assert.rejects(f.broker.submit(batch, { ...binding, workspace: '/synthetic/two' }, signal));
  await f.broker.authorize('/synthetic/one', 'session-two', signal);
  await assert.rejects(f.broker.submit(batch, binding, signal));
});

test('malformed and over-budget outputs never echo raw diagnostics', async t => {
  for (const mode of ['malformed', 'overflow']) {
    const f = await fixture(t, mode);
    await assert.rejects(f.broker.authorize('/synthetic/workspace', 'session', new AbortController().signal), error => !error.message.includes('sensitive') && !error.message.includes('xxxxx'));
  }
});

test('cancellation waits for child close and rejects safe fixed error', async t => {
  const f = await fixture(t, 'abort'), controller = new AbortController();
  const pending = f.broker.authorize('/synthetic/workspace', 'session', controller.signal);
  const timer = setTimeout(() => controller.abort(), 100);
  t.after(() => clearTimeout(timer));
  await assert.rejects(pending, /did not confirm/);
});

test('foreign batch receipt is not adopted', async t => {
  const f = await fixture(t, 'wrong_batch'), signal = new AbortController().signal;
  const binding = await f.broker.authorize('/synthetic/workspace', 'session', signal);
  assert.equal(await f.broker.submit(batch, binding, signal), undefined);
});
