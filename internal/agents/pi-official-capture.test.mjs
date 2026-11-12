import test from 'node:test';
import assert from 'node:assert/strict';
import { PiOfficialCaptureProjection } from './pi-official-capture.ts';

const binding = { key: 'grant-generation-1', workspace: '/synthetic/pi', enabled: true, purpose: 'daily' };
function fixture() {
  const entries = new Map();
  const reads = [];
  let leaf = 'historical-boundary', session = 'synthetic-session';
  const manager = {
    getSessionId: () => session, getLeafId: () => leaf,
    getEntry: id => { reads.push(id); return entries.get(id); },
  };
  return { manager, reads, entries,
    add(id, message, type = 'message') { entries.set(id, { id, parentId: leaf, type, message }); leaf = id; },
    setLeaf(id) { leaf = id; }, setSession(id) { session = id; },
  };
}
const user = content => ({ role: 'user', content });
const assistant = (content, stopReason = 'stop') => ({ role: 'assistant', content, stopReason });
const call = (id = 'call-1', name = 'bash', args = { command: 'printf synthetic' }) => ({ type: 'toolCall', id, name, arguments: args });
const result = (id = 'call-1', isError = false) => ({ role: 'toolResult', toolCallId: id, toolName: 'bash', isError, content: [{ type: 'text', text: 'synthetic output' }] });
function start(f = fixture()) {
  const capture = new PiOfficialCaptureProjection();
  assert.equal(capture.activate(f.manager, binding), true);
  assert.deepEqual(f.reads, []);
  return { ...f, capture, collect: (workspace = binding.workspace, idle = true) => capture.collectSettled(f.manager, workspace, idle) };
}

test('new branch only, typed QA and verified tool trace, no thinking or historical reads', () => {
  const f = start();
  f.add('user-1', user('Run the synthetic check.'));
  f.add('assistant-call', assistant([call(), { type: 'thinking', thinking: 'private reasoning' }], 'toolUse'));
  f.add('result-1', result());
  f.add('answer-1', assistant([{ type: 'thinking', thinking: 'hidden' }, { type: 'text', text: 'The check passed.' }]));
  const { batch } = f.collect();
  assert.deepEqual(f.reads, ['answer-1', 'result-1', 'assistant-call', 'user-1']);
  assert.equal(batch.entries.length, 2);
  const [trace, qa] = batch.entries.map(x => x.entry);
  assert.equal(trace.type, 'trace'); assert.equal(trace.status, 'success');
  assert.deepEqual(trace.method_params, { command: 'printf synthetic' });
  assert.equal(trace.method_return_value, 'synthetic output');
  assert.equal(trace.generate_feedback_with_llm, false);
  assert.equal(qa.question, 'Run the synthetic check.'); assert.equal(qa.answer, 'The check passed.');
  assert.deepEqual(JSON.parse(qa.context).question_entries, ['user-1']);
  assert.equal(JSON.parse(qa.context).answer_entry, 'answer-1');
  assert.equal(JSON.stringify(batch).includes('private reasoning'), false);
  assert.equal(JSON.stringify(batch).includes('hidden'), false);
  assert.equal(f.capture.acknowledge(batch.id), true); assert.deepEqual(f.collect(), {});
});

test('unresolved batch is immutable and does not advance to newer host messages', () => {
  const f = start(); f.add('u1', user('first')); f.add('a1', assistant('one'));
  const first = f.collect().batch, original = structuredClone(first);
  first.entries[0].entry.answer = 'caller mutation';
  f.add('u2', user('second')); f.add('a2', assistant('two'));
  const readCount = f.reads.length;
  assert.deepEqual(f.collect().batch, original); assert.equal(f.reads.length, readCount);
  assert.equal(f.capture.acknowledge('wrong-batch'), false);
  assert.equal(f.capture.acknowledge(original.id), true);
  const second = f.collect().batch;
  assert.equal(second.entries[0].entry.question, 'second');
  assert.notEqual(second.id, original.id);
});

test('scope change invalidates pending acknowledgement and collection', () => {
  for (const change of ['workspace', 'session']) {
    const f = start(); f.add('u', user('q')); f.add('a', assistant('a'));
    const pending = f.collect().batch;
    if (change === 'session') f.setSession('other-session');
    assert.equal(f.collect(change === 'workspace' ? '/other' : binding.workspace).issue, 'capture_scope_changed');
    assert.equal(f.capture.acknowledge(pending.id), false);
    assert.equal(f.collect().issue, 'capture_disabled');
  }
});

test('processing and disabled capture read no messages, unsettled agent reads none', () => {
  for (const extra of [{ purpose: 'processing' }, { enabled: false }, { key: 'bad key' }, { workspace: 'relative' }]) {
    const f = fixture(), capture = new PiOfficialCaptureProjection();
    assert.equal(capture.activate(f.manager, { ...binding, ...extra }), false);
    assert.equal(capture.collectSettled(f.manager, binding.workspace, true).issue, 'capture_disabled');
    assert.deepEqual(f.reads, []);
  }
  const f = start(); f.add('u', user('q'));
  assert.equal(f.collect(binding.workspace, false).issue, 'agent_not_settled'); assert.deepEqual(f.reads, []);
});

test('aborted, error and length answers are omitted; authorized retry can complete same question', () => {
  for (const reason of ['aborted', 'error', 'length']) {
    const f = start(); f.add('u', user('question')); f.add('partial', assistant('partial text', reason));
    const batch = f.collect().batch;
    assert.deepEqual(batch.entries, []); assert.ok(batch.diagnostics.includes('incomplete_answer_omitted'));
    assert.equal(f.capture.acknowledge(batch.id), true);
    f.add('retry', assistant('completed answer'));
    assert.equal(f.collect().batch.entries[0].entry.question, 'question');
  }
});

test('triple tool-call collision and missing boolean status cannot produce fabricated traces', () => {
  const f = start(); f.add('u', user('q'));
  for (let i = 0; i < 3; i++) f.add(`call-${i}`, assistant([call()], 'toolUse'));
  f.add('result', result()); f.add('unknown-result', { ...result('unknown'), isError: undefined });
  f.add('a', assistant('done'));
  const batch = f.collect().batch;
  assert.deepEqual(batch.entries.map(x => x.entry.type), ['qa']);
  assert.ok(batch.diagnostics.includes('ambiguous_tool_call')); assert.ok(batch.diagnostics.includes('unverified_tool_omitted'));
});

test('tool errors retain real status and only post-activation matching calls', () => {
  const f = start(); f.add('orphan', result('historical-call'));
  f.add('call', assistant([call()], 'toolUse')); f.add('result', result('call-1', true));
  const entries = f.collect().batch.entries;
  assert.equal(entries.length, 1); assert.equal(entries[0].entry.status, 'error');
  assert.equal(entries[0].entry.error_message, 'synthetic output');
});

test('unknown branch, cycle and excessive range fail closed without reading beyond activation', () => {
  const missing = start(); missing.setLeaf('missing'); assert.equal(missing.collect().issue, 'capture_branch_changed');
  const cycle = start(); cycle.entries.set('loop', { id: 'loop', parentId: 'loop', type: 'message' }); cycle.setLeaf('loop');
  assert.equal(cycle.collect().issue, 'capture_branch_changed'); assert.deepEqual(cycle.reads, ['loop']);
  const long = start(); for (let i = 0; i < 257; i++) long.add(`e${i}`, {}, 'custom');
  assert.equal(long.collect().issue, 'capture_range_exceeded'); assert.equal(long.reads.length, 256);
  assert.equal(long.reads.includes('historical-boundary'), false);
});

test('full policy input survives until redaction; nontext is excluded and oversized ranges fail closed', () => {
  const f = start(); f.add('u', user([{ type: 'text', text: '蓝'.repeat(3000) }, { type: 'image', data: 'not-text' }]));
  f.add('a', assistant('色'.repeat(6000)));
  const qa = f.collect().batch.entries[0].entry, context = JSON.parse(qa.context);
  assert.equal(qa.question, '蓝'.repeat(3000)); assert.equal(qa.answer, '色'.repeat(6000));
  assert.equal(context.question_truncated, false); assert.equal(context.answer_truncated, false);
  assert.equal(qa.question.includes('\ufffd'), false); assert.equal(qa.answer.includes('\ufffd'), false);
  assert.equal(qa.question.includes('not-text'), false);
  const huge = start(); huge.add('huge-user', user('x'.repeat(1024 * 1024 + 1))); huge.add('huge-answer', assistant('a'));
  assert.equal(huge.collect().issue, 'capture_range_exceeded');
});

test('stable operation identity is scoped to binding generation and host session', () => {
  function project(grant = binding) {
    const f = fixture(), capture = new PiOfficialCaptureProjection(); capture.activate(f.manager, grant);
    f.add('u', user('q')); f.add('a', assistant('a'));
    return capture.collectSettled(f.manager, grant.workspace, true).batch;
  }
  assert.deepEqual(project(), project());
  const changed = project({ ...binding, key: 'grant-generation-2' });
  assert.notEqual(project().entries[0].operation, changed.entries[0].operation);
  assert.notEqual(project().entries[0].session, changed.entries[0].session);
});

test('closing discards pending batch and activation baseline excludes previous messages', () => {
  const f = start(); f.add('u', user('old')); f.add('a', assistant('old answer'));
  const pending = f.collect().batch; f.capture.close(); assert.equal(f.capture.acknowledge(pending.id), false);
  assert.equal(f.capture.activate(f.manager, binding), true); assert.deepEqual(f.collect(), {});
  f.add('new-u', user('new')); f.add('new-a', assistant('new answer'));
  assert.equal(f.collect().batch.entries[0].entry.question, 'new');
});
