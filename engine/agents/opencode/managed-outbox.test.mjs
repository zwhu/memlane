import test from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

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
