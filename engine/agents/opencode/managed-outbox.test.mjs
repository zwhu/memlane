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
