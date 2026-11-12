import test from 'node:test';
import assert from 'node:assert/strict';
import { PiOfficialCaptureHost, registerPiOfficialCaptureHost } from './pi-official-capture-host.ts';

const grant = { key: 'synthetic-grant-1', workspace: '/synthetic/pi', enabled: true, purpose: 'daily' };
const deferred = () => { let resolve; const promise = new Promise(r => { resolve = r; }); return { promise, resolve }; };
function fixture(overrides = {}) {
  const entries = new Map(), reads = [], writes = [], inspections = [];
  let leaf = 'before-activation';
  const ctx = { cwd: grant.workspace, isIdle: () => true, sessionManager: {
    getSessionId: () => 'synthetic-session', getLeafId: () => leaf,
    getEntry: id => { reads.push(id); return entries.get(id); },
  } };
  const receipt = batch => ({ batch: batch.id, operations: batch.entries.map(e => ({ operation: e.operation, state: 'saved' })) });
  const broker = {
    authorize: async () => grant,
    submit: async batch => { writes.push(batch); return receipt(batch); },
    inspect: async batch => { inspections.push(batch); return receipt(batch); },
    ...overrides,
  };
  const host = new PiOfficialCaptureHost(broker);
  return { host, ctx, broker, receipt, reads, writes, inspections,
    turn(suffix = '1') {
      const user = 'u' + suffix, answer = 'a' + suffix;
      entries.set(user, { id: user, parentId: leaf, type: 'message', message: { role: 'user', content: 'synthetic question ' + suffix } });
      entries.set(answer, { id: answer, parentId: user, type: 'message', message: { role: 'assistant', content: 'synthetic answer ' + suffix, stopReason: 'stop' } });
      leaf = answer;
    },
  };
}

test('authorize before traversal, successful receipt advances, no duplicate submit', async () => {
  const f = fixture(); f.turn('past');
  await f.host.start(f.ctx); assert.deepEqual(f.reads, []);
  f.turn(); await f.host.settled(f.ctx); await f.host.settled(f.ctx);
  assert.deepEqual(f.reads, ['a1', 'u1']); assert.equal(f.writes.length, 1);
  assert.equal(f.host.status(), 'capture_ready');
});

test('unknown and thrown write outcomes only reconcile, never resubmit', async () => {
  for (const throws of [false, true]) {
    let submits = 0;
    const f = fixture({ submit: async () => { submits++; if (throws) throw new Error('private transport error'); } });
    await f.host.start(f.ctx); f.turn(); await f.host.settled(f.ctx);
    assert.equal(f.host.status(), 'capture_receipt_unknown');
    f.turn('2'); await f.host.settled(f.ctx);
    assert.equal(submits, 1); assert.equal(f.inspections.length, 1);
    assert.equal(f.host.status(), 'capture_ready');
    await f.host.settled(f.ctx); assert.equal(submits, 2);
  }
});

test('only authoritative not-dispatched receipt permits another submission', async () => {
  let submits = 0;
  const f = fixture({
    submit: async batch => { submits++; if (submits === 1) throw new Error('before daemon accepted'); return f.receipt(batch); },
    inspect: async batch => ({ batch: batch.id, operations: batch.entries.map(entry => ({ operation: entry.operation, state: 'not_dispatched' })) }),
  });
  await f.host.start(f.ctx); f.turn(); await f.host.settled(f.ctx);
  assert.equal(f.host.status(), 'capture_receipt_unknown');
  await f.host.settled(f.ctx);
  assert.equal(f.host.status(), 'capture_retry_ready'); assert.equal(submits, 1);
  await f.host.settled(f.ctx);
  assert.equal(submits, 2); assert.equal(f.host.status(), 'capture_ready');
});

test('an unknown or foreign-batch lookup never proves no dispatch', async () => {
  for (const invalid of ['unknown', 'foreign-batch']) {
    let submits = 0;
    const f = fixture({
      submit: async () => { submits++; },
      inspect: async batch => ({ batch: invalid === 'foreign-batch' ? 'other' : batch.id, operations: batch.entries.map(entry => ({ operation: entry.operation, state: invalid === 'unknown' ? 'unknown' : 'not_dispatched' })) }),
    });
    await f.host.start(f.ctx); f.turn();
    for (let i = 0; i < 4; i++) await f.host.settled(f.ctx);
    assert.equal(submits, 1); assert.equal(f.host.status(), 'capture_receipt_unknown');
  }
});

test('partial, duplicate, wrong-batch and invalid-state receipts do not advance', async () => {
  for (const malformed of [
    batch => ({ batch: batch.id, operations: [] }),
    batch => ({ batch: 'wrong', operations: [{ operation: batch.entries[0].operation, state: 'saved' }] }),
    batch => ({ batch: batch.id, operations: [{ operation: batch.entries[0].operation, state: 'unknown' }] }),
    batch => ({ batch: batch.id, operations: [{ operation: 'another-operation', state: 'saved' }] }),
  ]) {
    let sent;
    const f = fixture({ submit: async batch => { sent = batch; return malformed(batch); }, inspect: async batch => malformed(batch) });
    await f.host.start(f.ctx); f.turn(); await f.host.settled(f.ctx); f.turn('2'); await f.host.settled(f.ctx);
    assert.equal(f.host.status(), 'capture_receipt_unknown');
    assert.deepEqual(f.reads, ['a1', 'u1']); assert.equal(sent.entries[0].entry.question, 'synthetic question 1');
  }
});

test('authorization revocation reads no new messages and sends no batch', async () => {
  const f = fixture(); await f.host.start(f.ctx); f.turn();
  f.broker.authorize = async () => ({ ...grant, key: 'new-generation' });
  await f.host.settled(f.ctx);
  assert.equal(f.host.status(), 'capture_not_authorized'); assert.deepEqual(f.reads, []); assert.deepEqual(f.writes, []);
});

test('late authorization and write receipts cannot revive a closed host', async () => {
  const authorization = deferred(), f = fixture({ authorize: async () => authorization.promise });
  const starting = f.host.start(f.ctx); f.host.close(); authorization.resolve(grant); await starting;
  assert.equal(f.host.status(), 'capture_disabled'); f.turn(); await f.host.settled(f.ctx); assert.deepEqual(f.reads, []);
  const write = deferred(), sent = deferred();
  const g = fixture({ submit: async batch => { sent.resolve(batch); return write.promise; } });
  await g.host.start(g.ctx); g.turn(); const settling = g.host.settled(g.ctx);
  const batch = await sent.promise; g.host.close(); write.resolve(g.receipt(batch)); await settling;
  assert.equal(g.host.status(), 'capture_disabled');
  await g.host.start(g.ctx); await g.host.settled(g.ctx); assert.equal(g.writes.length, 0);
});

test('concurrent settled events share one write; complete filtered receipt also advances', async () => {
  const pending = deferred(), arrived = deferred(); let writes = 0;
  const f = fixture({ submit: async batch => { writes++; arrived.resolve(batch); return pending.promise; } });
  await f.host.start(f.ctx); f.turn();
  const first = f.host.settled(f.ctx), second = f.host.settled(f.ctx);
  assert.equal(first, second);
  const batch = await arrived.promise;
  pending.resolve({ batch: batch.id, operations: batch.entries.map(e => ({ operation: e.operation, state: 'policy_filtered' })) });
  await first; await f.host.settled(f.ctx); assert.equal(writes, 1); assert.equal(f.host.status(), 'capture_ready');
});

test('host subscribes to stable completion, resets baseline on tree navigation, closes on shutdown', async () => {
  const handlers = new Map(), f = fixture();
  const host = registerPiOfficialCaptureHost({ on(name, handler) { handlers.set(name, handler); } }, f.broker);
  assert.deepEqual([...handlers.keys()], ['session_start', 'session_shutdown', 'session_tree', 'agent_settled']);
  assert.equal(handlers.has('agent_end'), false);
  await handlers.get('session_start')({}, f.ctx); f.turn('old-branch');
  await handlers.get('session_tree')({}, f.ctx); f.turn('new-branch');
  await handlers.get('agent_settled')({}, f.ctx);
  assert.deepEqual(f.reads, ['anew-branch', 'unew-branch']);
  await handlers.get('session_shutdown')({}, f.ctx); assert.equal(host.status(), 'capture_disabled');
});
