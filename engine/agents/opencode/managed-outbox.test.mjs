import test from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, rmSync, mkdirSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
import { spawn } from "node:child_process";
import { once } from "node:events";

const { Outbox } = await import(pathToFileURL(process.env.MEMLANE_OPENCODE_TEST_RUNTIME).href);
const fixture = () => {
  const root = mkdtempSync(join(tmpdir(), "memlane-managed-outbox-"));
  return {root, queue: new Outbox("same-installation-and-project", root),
          close: () => rmSync(root, {recursive: true, force: true})};
};

test("401 and 403 survive restart and automatic pumps without resubmission", async () => {
  for (const status of [401, 403]) {
    const f = fixture();
    try {
      f.queue.enqueue("denied", "native-session", {type: "qa", question: "q", answer: "a"});
      let attempts = 0;
      await assert.rejects(f.queue.flush(async () => {
        attempts++; throw new Error(`Cognee request failed (${status}): denied`);
      }, async () => false));
      const restarted = new Outbox("same-installation-and-project", f.root);
      for (let i = 0; i < 3; i++) {
        await restarted.flush(async () => { attempts++; }, async () => {
          assert.fail("definite denial must not use uncertain reconciliation");
        });
      }
      assert.equal(attempts, 1, "permission restoration or a timer retried the denial");
      assert.equal(restarted.status().blocked, 1);
      assert.equal(restarted.status().pending, 1);
      assert.equal(restarted.status().uncertain, 0);
      assert.deepEqual(restarted.blockedIds(), ["denied"]);
      assert.equal(restarted.retryBlocked(["denied"]), 1);
      await restarted.flush(async item => {
        assert.equal(item.id, "denied"); attempts++;
      }, async () => false);
      assert.equal(attempts, 2);
      assert.deepEqual(restarted.status(), {pending: 0, uncertain: 0, saved: 1, blocked: 0});
      restarted.enqueue("denied", "native-session", {type: "qa"});
      await restarted.flush(async () => assert.fail("duplicate native id"), async () => false);
    } finally { f.close(); }
  }
});

test("a blocked item does not prevent a fresh authorized item", async () => {
  const f = fixture();
  try {
    f.queue.enqueue("blocked", "old-turn", {type: "qa"});
    await assert.rejects(f.queue.flush(async () => { throw new Error("Cognee request failed (403)"); }, async () => false));
    f.queue.enqueue("fresh", "new-turn", {type: "qa"});
    const sent = [];
    await f.queue.flush(async item => sent.push(item.id), async () => false);
    assert.deepEqual(sent, ["fresh"]);
    assert.deepEqual(f.queue.blockedIds(), ["blocked"]);
    assert.equal(f.queue.status().saved, 1);
  } finally { f.close(); }
});

test("explicit release is all-or-nothing and cannot release uncertain outcomes", async () => {
  const f = fixture();
  try {
    f.queue.enqueue("blocked", "turn", {type: "trace"});
    await assert.rejects(f.queue.flush(async () => { throw new Error("Cognee request failed (403)"); }, async () => false));
    f.queue.enqueue("unknown", "turn", {type: "qa"});
    await assert.rejects(f.queue.flush(async () => { throw new Error("response lost after commit"); }, async () => false));
    const before = readFileSync(f.queue.file);
    for (const ids of [[], ["blocked", "unknown"], ["missing"], ["unknown"]]) {
      assert.throws(() => f.queue.retryBlocked(ids));
      assert.deepEqual(readFileSync(f.queue.file), before);
    }
    const sent = [];
    await f.queue.flush(async item => sent.push(item.id), async () => false);
    assert.deepEqual(sent, []);
    assert.equal(f.queue.status().uncertain, 1);
    assert.equal(f.queue.status().blocked, 1);
    await f.queue.flush(async () => assert.fail("uncertain record resent"), async item => item.id === "unknown");
    assert.equal(f.queue.status().saved, 1);
    assert.equal(f.queue.retryBlocked(["blocked"]), 1);
  } finally { f.close(); }
});

test("release during a running flush is rejected; persisted credentials stay scrubbed", async () => {
  const f = fixture();
  try {
    f.queue.enqueue("active", "turn", {type: "qa", answer: "api_key=private-value"});
    assert.ok(!readFileSync(f.queue.file, "utf8").includes("private-value"));
    let done;
    const flush = f.queue.flush(async () => await new Promise(resolve => { done = resolve; }), async () => false);
    assert.throws(() => f.queue.retryBlocked(["active"]), /Outbox flush is running/);
    done(); await flush;
    assert.equal(f.queue.status().saved, 1);
  } finally { f.close(); }
});

const target = () => ({native: "native-original", project_id: "11111111-1111-4111-8111-111111111111", dataset_id: "22222222-2222-4222-8222-222222222222"});

test("pending target is a private snapshot of original project and native session after restart", async () => {
  const f = fixture();
  try {
    const original = target();
    f.queue.enqueue("original-target", "official-session", {type: "qa", answer: "answer"}, original);
    original.native = "different-current-session";
    original.dataset_id = "33333333-3333-4333-8333-333333333333";
    const restarted = new Outbox("same-installation-and-project", f.root);
    await restarted.flush(async item => {
      assert.deepEqual(item.target, target());
      assert.equal(item.session, "official-session");
    }, async () => assert.fail("fresh item used unknown reconciliation"));
    assert.equal(restarted.status().saved, 1);
  } finally { f.close(); }
});

test("invalid original targets never alter existing pending entries", () => {
  const f = fixture();
  try {
    f.queue.enqueue("valid", "turn", {type: "qa"}, target());
    const before = readFileSync(f.queue.file);
    for (const invalid of [null, {}, {...target(), native: ""}, {...target(), native: "x".repeat(257)},
      {...target(), native: "bad\nkey"}, {...target(), dataset_id: "another-dataset"},
      {...target(), project_id: "../project"}]) {
      assert.throws(() => f.queue.enqueue("invalid", "turn", {type: "qa"}, invalid), /original_project_target_required/);
      assert.deepEqual(readFileSync(f.queue.file), before);
    }
  } finally { f.close(); }
});

test("local policy denial is durable and distinct from an actual official HTTP denial", async () => {
  const { LocalPolicyDenied } = await import(pathToFileURL(process.env.MEMLANE_OPENCODE_TEST_RUNTIME).href);
  const f = fixture();
  try {
    f.queue.enqueue("local", "turn", {type: "qa"}, target());
    let remoteWrites = 0;
    await assert.rejects(f.queue.flush(async () => { throw new LocalPolicyDenied(); }, async () => false), /local_policy_denied/);
    let state = JSON.parse(readFileSync(f.queue.file, "utf8"));
    assert.equal(state.pending[0].blocked_reason, "local_policy_denied");
    assert.equal(state.pending[0].uncertain, false);
    assert.deepEqual(state.pending[0].target, target());
    const restarted = new Outbox("same-installation-and-project", f.root);
    await restarted.flush(async () => { remoteWrites++; }, async () => assert.fail("local denial was never submitted"));
    assert.equal(remoteWrites, 0);
    restarted.enqueue("official", "turn", {type: "qa"}, target());
    await assert.rejects(restarted.flush(async () => { remoteWrites++; throw new Error("Cognee request failed (403)"); }, async () => false));
    state = JSON.parse(readFileSync(f.queue.file, "utf8"));
    assert.equal(state.pending.find(p => p.id === "official").blocked_reason, "official_auth_denied");
    assert.equal(state.pending.find(p => p.id === "local").blocked_reason, "local_policy_denied");
    restarted.retryBlocked(["local"]);
    await restarted.flush(async item => { assert.equal(item.id, "local"); assert.deepEqual(item.target, target()); remoteWrites++; }, async () => false);
    assert.equal(remoteWrites, 2);
    assert.equal(restarted.status().blocked, 1);
  } finally { f.close(); }
});

test("preflight outage leaves unsent state untouched and denial skips to a fresh item", async () => {
  const f = fixture();
  try {
    f.queue.enqueue("preflight", "turn", {type: "qa"}, target());
    const before = readFileSync(f.queue.file);
    await assert.rejects(f.queue.flush(async () => assert.fail("control outage sent a write"), async () => false,
      async () => { throw new Error("control_unavailable"); }));
    assert.deepEqual(readFileSync(f.queue.file), before);
    f.queue.enqueue("allowed", "turn", {type: "qa"}, target());
    const sent = [];
    await f.queue.flush(async item => sent.push(item.id), async () => false, async item => item.id === "allowed");
    assert.deepEqual(sent, ["allowed"]);
    assert.equal(f.queue.status().blocked, 1);
    assert.equal(f.queue.status().uncertain, 0);
  } finally { f.close(); }
});

test("sender lease excludes a second queue during asynchronous policy preflight", async () => {
  const f = fixture();
  let releaseGate, working;
  try {
    f.queue.enqueue("concurrent", "turn", {type: "qa"}, target());
    const second = new Outbox("same-installation-and-project", f.root);
    let checks = 0, admitted;
    const gate = new Promise(resolve => { releaseGate = resolve; });
    const started = new Promise(resolve => { admitted = resolve; });
    const authorize = async () => { checks++; admitted(); await gate; return true; };
    let sends = 0;
    const send = async () => { sends++; };
    working = f.queue.flush(send, async () => false, authorize);
    await started;
    await assert.rejects(second.flush(send, async () => false, authorize), /Outbox busy/);
    assert.equal(checks, 1); assert.equal(sends, 0);
    releaseGate(); await working;
    assert.equal(sends, 1); assert.equal(f.queue.status().saved, 1);
  } finally { releaseGate?.(); await working?.catch(() => {}); f.close(); }
});

test("saved target watermark survives restart and excludes other session pending items", async () => {
  const f = fixture();
  try {
    f.queue.enqueue("saved-original", "turn", {type: "qa"}, target());
    assert.equal(f.queue.completedTarget(target().native), undefined);
    await f.queue.flush(async () => {}, async () => false);
    const completed = f.queue.completedTarget(target().native);
    assert.deepEqual(completed.target, target());
    f.queue.enqueue("other-pending", "other-turn", {type: "qa"}, {...target(), native: "other-native"});
    const restarted = new Outbox("same-installation-and-project", f.root);
    assert.deepEqual(restarted.completedTarget(target().native), completed);
    assert.equal(restarted.completedTarget("other-native"), undefined);
  } finally { f.close(); }
});


test("host sender lease excludes another process until the original request finishes", {timeout: 10000}, async () => {
  const f = fixture();
  f.queue.enqueue("inflight", "original-session", {type: "qa"}, target());
  const code = `import {pathToFileURL} from "node:url";
    const {Outbox}=await import(pathToFileURL(process.argv[1]).href);
    const queue=new Outbox("same-installation-and-project",process.argv[2]);
    await queue.flush(async()=>{process.stdout.write("sending\\n");await new Promise(resolve=>{process.stdin.once("end",resolve);process.stdin.resume();});},async()=>false);`;
  const child = spawn(process.execPath, ["--input-type=module", "-e", code, process.env.MEMLANE_OPENCODE_TEST_RUNTIME, f.root], {stdio: ["pipe", "pipe", "pipe"]});
  const exit = once(child, "exit");
  let timer;
  try {
    await Promise.race([once(child.stdout, "data"), once(child, "error").then(([error]) => {throw error;}),
      new Promise((_, reject) => { timer = setTimeout(() => reject(new Error("child sender did not start")), 3000); })]);
    clearTimeout(timer);
    const before = readFileSync(f.queue.file);
    let callbacks = 0;
    await assert.rejects(f.queue.flush(async () => { callbacks++; }, async () => { callbacks++; return false; }), /Outbox busy/);
    assert.equal(callbacks, 0); assert.deepEqual(readFileSync(f.queue.file), before);
    child.stdin.end(); const [status] = await exit; assert.equal(status, 0);
    assert.equal(f.queue.status().saved, 1);
    // A dead owner is reclaimed using its recorded process, not a timed lease.
    mkdirSync(f.queue.file + ".sender", {mode: 0o700});
    writeFileSync(f.queue.file + ".sender/pid", String(child.pid), {mode: 0o600});
    f.queue.enqueue("after-owner-exit", "original-session", {type: "qa"}, target());
    await f.queue.flush(async () => { callbacks++; }, async () => false);
    assert.equal(callbacks, 1); assert.equal(f.queue.status().saved, 2);
  } finally {
    clearTimeout(timer);
    if (child.exitCode === null && child.signalCode === null) { child.kill("SIGTERM"); await exit; }
    f.close();
  }
});

test("review consent is scoped to exact original entries and selected replay excludes other sessions", async () => {
  const f = fixture(), a = target(), b = {...a, native: "another-native"};
  try {
    f.queue.enqueue("reviewed", "official-a", {type: "qa", answer: "PRIVATE_BODY"}, a);
    await assert.rejects(f.queue.flush(async () => {throw new Error("Cognee request failed (403)");}, async () => false));
    const snapshot = f.queue.reviewPending(a, "official-a");
    assert.equal(snapshot.pending, 1); assert.equal(snapshot.unknown_outcomes, 0);
    assert.ok(!JSON.stringify(snapshot).includes("PRIVATE_BODY"));
    f.queue.enqueue("unselected", "official-b", {type: "qa"}, b);
    assert.equal(f.queue.reviewPending(a, "official-a").signature, snapshot.signature);
    assert.throws(() => f.queue.decidePending(a, "official-a", snapshot.signature, "replay"), /sender_lease_required/);
    const sent = [];
    const result = await f.queue.withSender(async token => {
      const decision = f.queue.decidePending(a, "official-a", snapshot.signature, "replay");
      await f.queue.flush(async item => sent.push(item.id), async () => false, undefined, new Set(decision.released_ids), token);
      return decision;
    });
    assert.deepEqual(result.released_ids, ["reviewed"]);
    assert.deepEqual(sent, ["reviewed"]); assert.equal(f.queue.status().pending, 1);
    assert.equal(f.queue.completedTarget(a.native).target.dataset_id, a.dataset_id);
    assert.equal(f.queue.completedTarget(b.native), undefined);
    assert.throws(() => f.queue.flush(async () => assert.fail("forged lease sent a write"), async () => false, undefined, undefined, {}), /sender_lease_required/);
  } finally { f.close(); }
});

test("changed same-session snapshots cannot discard and discarded IDs never re-enter capture", async () => {
  const f = fixture(), a = target();
  try {
    f.queue.enqueue("old", "official-a", {type: "qa", answer: "old body"}, a);
    const snapshot = f.queue.reviewPending(a, "official-a");
    f.queue.enqueue("new", "official-a", {type: "qa", answer: "new body"}, a);
    const before = readFileSync(f.queue.file);
    await assert.rejects(f.queue.withSender(async () => f.queue.decidePending(a, "official-a", snapshot.signature, "discard")), /pending_materials_changed_review_again/);
    assert.deepEqual(readFileSync(f.queue.file), before);
    const fresh = f.queue.reviewPending(a, "official-a");
    const result = await f.queue.withSender(async () => f.queue.decidePending(a, "official-a", fresh.signature, "discard"));
    assert.equal(result.discarded, 2); assert.equal(f.queue.status().saved, 0);
    const restarted = new Outbox("same-installation-and-project", f.root);
    restarted.enqueue("old", "official-a", {type: "qa", answer: "old body"}, a);
    assert.equal(restarted.status().pending, 0); assert.equal(restarted.completedTarget(a.native), undefined);
    assert.ok(!readFileSync(f.queue.file, "utf8").includes("old body"));
  } finally { f.close(); }
});

test("unknown replay needs matched originals and acknowledgement never sends unmatched entries", async () => {
  const f = fixture(), a = target();
  try {
    f.queue.enqueue("unknown", "official-a", {type: "qa", context: "original-marker", answer: "PRIVATE_UNKNOWN_BODY"}, a);
    await assert.rejects(f.queue.flush(async () => {throw new Error("controlled transport failure");}, async () => false));
    const review = f.queue.reviewPending(a, "official-a"), before = readFileSync(f.queue.file);
    await assert.rejects(f.queue.withSender(async () => f.queue.decidePending(a, "official-a", review.signature, "replay")), /original_write_result_still_unknown/);
    await assert.rejects(f.queue.withSender(async () => f.queue.decidePending(a, "official-a", review.signature, "acknowledge", ["other-id"])), /original_pending_marker_required/);
    assert.deepEqual(readFileSync(f.queue.file), before);
    const result = await f.queue.withSender(async () => {
      const matches = await f.queue.inspectUnknown(a, "official-a", review.signature, async (session, marker, dataset) => {
        assert.equal(session, "official-a"); assert.equal(dataset, a.dataset_id);
        assert.ok(!JSON.stringify(marker).includes("PRIVATE_UNKNOWN_BODY"));
        assert.deepEqual(marker, {type: "qa", context: "original-marker"}); return true;
      });
      return f.queue.decidePending(a, "official-a", review.signature, "acknowledge", matches);
    });
    assert.equal(result.matched_unknown, 1); assert.equal(result.released_ids.length, 0);
    assert.equal(f.queue.status().saved, 1); assert.equal(f.queue.status().pending, 0);
    assert.deepEqual(f.queue.completedTarget(a.native).target, a);
  } finally { f.close(); }
});


test("backlog snapshots exclude unknown bodies and cannot release without a shared sender lease", async () => {
  const f = fixture(), a = target(), known = "a".repeat(64), unknown = "b".repeat(64);
  try {
    f.queue.enqueue(known, "original", {type: "qa", answer: "PRIVATE_KNOWN_BODY"}, a);
    await assert.rejects(f.queue.flush(async () => {throw new Error("Cognee request failed (403)");}, async () => false));
    f.queue.enqueue(unknown, "original", {type: "qa", answer: "PRIVATE_UNKNOWN_BODY"}, a);
    await assert.rejects(f.queue.flush(async () => {throw new Error("response lost");}, async () => false));
    const before = readFileSync(f.queue.file), snapshot = f.queue.reviewBacklog(a, "original");
    assert.deepEqual(snapshot, {session_id: "original", tokens: [known], unknown: 1});
    assert.ok(!JSON.stringify(snapshot).includes("PRIVATE_"));
    assert.throws(() => f.queue.releaseBacklogEntry(a, "original", known), /sender_lease_required/);
    await f.queue.withSender(async () => {
      assert.equal(f.queue.releaseBacklogEntry(a, "original", unknown), "isolated");
      assert.equal(f.queue.releaseBacklogEntry(a, "original", "c".repeat(64)), "handled");
    });
    assert.deepEqual(readFileSync(f.queue.file), before);
  } finally { f.close(); }
});

test("frozen backlog IDs save only the reviewed known record, preserving unknown and new arrivals across restart", async () => {
  const f = fixture(), a = target(), known = "a".repeat(64), unknown = "b".repeat(64), later = "c".repeat(64);
  try {
    f.queue.enqueue(unknown, "original", {type: "qa", answer: "unknown preserved"}, a);
    await assert.rejects(f.queue.flush(async () => {throw new Error("response lost");}, async () => false));
    f.queue.enqueue(known, "original", {type: "qa", answer: "reviewed"}, a);
    const snapshot = f.queue.reviewBacklog(a, "original");
    f.queue.enqueue(later, "original", {type: "qa", answer: "new arrival"}, a);
    const sent = [];
    await f.queue.withSender(async token => {
      for (const id of snapshot.tokens) {
        assert.equal(f.queue.releaseBacklogEntry(a, "original", id), "released");
        await f.queue.flush(async item => sent.push(item.id), async () => assert.fail("unknown must not be probed or resent"), undefined, new Set([id]), token);
      }
    });
    assert.deepEqual(sent, [known]);
    const restarted = new Outbox("same-installation-and-project", f.root);
    assert.deepEqual(restarted.reviewBacklog(a, "original"), {session_id: "original", tokens: [later], unknown: 1});
    assert.equal(restarted.status().saved, 1);
  } finally { f.close(); }
});

test("backlog release distinguishes a removed record from a changed original target", async () => {
  const f = fixture(), a = target(), known = "a".repeat(64), moved = "b".repeat(64), other = {...a, native: "other-native"};
  try {
    f.queue.enqueue(known, "original", {type: "qa"}, a);
    await f.queue.withSender(async () => f.queue.decidePending(a, "original", f.queue.reviewPending(a, "original").signature, "discard"));
    f.queue.enqueue(moved, "other-session", {type: "qa"}, other);
    const before = readFileSync(f.queue.file);
    await f.queue.withSender(async () => {
      assert.equal(f.queue.releaseBacklogEntry(a, "original", known), "handled");
      assert.throws(() => f.queue.releaseBacklogEntry(a, "original", moved), /original_project_target_changed/);
      assert.throws(() => f.queue.releaseBacklogEntry(a, "original", "not-a-capture-id"), /invalid_pending_action/);
    });
    assert.deepEqual(readFileSync(f.queue.file), before);
  } finally { f.close(); }
});
