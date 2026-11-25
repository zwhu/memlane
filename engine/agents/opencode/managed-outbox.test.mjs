import test from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, realpathSync, rmSync, mkdirSync, statSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
import { spawn } from "node:child_process";
import { once } from "node:events";

const compiled = pathToFileURL(process.env.MEMLANE_OPENCODE_TEST_RUNTIME);
const { Outbox, hash } = await import(compiled.href);
const fixture = () => {
  const root = mkdtempSync(join(tmpdir(), "memlane-managed-outbox-"));
  return {root, queue: new Outbox("same-installation-and-project", root),
          close: () => rmSync(root, {recursive: true, force: true})};
};

const {ManagedProjectControl} = await import(new URL("managed-control.js", compiled));
const {CogneeHttpClient} = await import(new URL("client.js", compiled));
const {ManagedDelivery} = await import(new URL("managed-delivery.js", compiled));
const {ManagedBacklog} = await import(new URL("managed-backlog.js", compiled));
const {CaptureWindow} = await import(new URL("managed-capture-window.js", compiled));
const {captureManagedConversation, recordManagedPrompt} = await import(new URL("managed-hooks.js", compiled));
const project = "11111111-1111-4111-8111-111111111111";
const dataset = "22222222-2222-4222-8222-222222222222";

async function captureFixture(t) {
  const f = fixture(), root = realpathSync(f.root), native = "long-session";
  t.after(f.close);
  const tokenFile = join(root, "agent-token"), managementTokenFile = join(root, "manager-token");
  writeFileSync(tokenFile, "a".repeat(40), {mode: 0o600});
  writeFileSync(managementTokenFile, "m".repeat(40), {mode: 0o600});
  const origin = "http://127.0.0.1:19001", stateDir = join(root, "control-state");
  const options = {instanceId: "local", directory: root, stateDir, origin, tokenFile};
  const control = new ManagedProjectControl(options), client = new CogneeHttpClient(origin + "/cognee", "child-key");
  const commands = {project_id: project, dataset_id: dataset, host_session_key: control.hostKey(native)};
  const session = `opencode_${hash("local").slice(0,16)}_${hash(native).slice(0,24)}`;
  const writes = [], requests = [];
  const state = {enabled: true, ready: true, version: 1, updatedAt: "", offline: false, pauseNextAutomatic: false, process: false};
  t.mock.method(globalThis, "fetch", async (url, init) => {
    const path = new URL(url).pathname;
    requests.push(path);
    if (path.startsWith("/v1/") && state.offline) throw new Error("offline");
    const capture_policy = {enabled: state.enabled, project_enabled: state.enabled, version: state.version,
      scope: state.enabled ? "ok" : "project", updated_at: state.updatedAt};
    const common = {ready: true, readable: false};
    const policy = {permissions: ["read", "write"], can_capture: state.enabled, can_auto_process: state.process};
    if (path === "/v1/project/resolve" || path === "/v1/project/policy" || path === "/v1/project/capture-policy") {
      return Response.json({...commands, dataset_name: "project", ready: state.ready, revision: state.version,
        capture_policy, common, policy, can_capture: state.enabled});
    }
    if (path === "/v1/projects") {
      return Response.json({projects: [{id: project, dataset_id: dataset}], agents: [{id: "local"}],
        bindings: [{...commands, agent_id: "local"}]});
    }
    if (path === "/cognee/api/v1/remember/entry") {
      const mode = new Headers(init.headers).get("X-Memlane-Capture-Mode");
      if (mode === "automatic" && (state.pauseNextAutomatic || !state.enabled)) {
        state.pauseNextAutomatic = false;
        return Response.json({error: "capture_paused", permission_denied: false, capture_policy_version: state.version + 1,
          capture_scope: "project"}, {status: 403});
      }
      writes.push({mode, ...JSON.parse(init.body)});
      return Response.json({status: "saved"});
    }
    assert.fail("unexpected route " + path);
  });
  const delivery = new ManagedDelivery(control, client, f.queue);
  const backlog = new ManagedBacklog(control, client, f.queue, {improve: async () => ({status: "accepted"})}, {
    origin, instanceId: options.instanceId, managementTokenFile,
  });
  const activate = () => control.current(native);
  return {...f, root, native, state, stateDir, control, options, delivery, backlog, commands, session, writes, requests, activate};
}

test("offline capture distinguishes a known pause from an unknown policy and resumes within the same session", async t => {
  const f = await captureFixture(t);
  const messages = new Map(), bodyReads = [];
  const window = new CaptureWindow(1);
  const deps = {autoCapture: true, directory: f.root, control: f.control, delivery: f.delivery,
    window, sid: () => f.session, safeText: String, hash, sessionClient: {
      message: async ({path}) => { bodyReads.push(path.messageID); return {data: messages.get(path.messageID)}; },
    }};
  async function turn(id, created, control = f.control) {
    const prompt = "prompt-" + id, answer = "answer-" + id;
    await recordManagedPrompt(window, control, true, f.native, prompt, created);
    messages.set(prompt, {info: {id: prompt, sessionID: f.native, role: "user"}, parts: [{type: "text", text: prompt}]});
    messages.set(answer, {info: {id: answer, sessionID: f.native, role: "assistant", parentID: prompt,
      time: {completed: created + 1}}, parts: [{type: "text", text: answer}]});
    window.completed(f.native, answer, prompt);
  }
  await f.activate();
  f.state.ready = false;
  await assert.rejects(f.control.captureStatus(f.native), /project_policy_unconfirmed/);
  f.state.ready = true;
  f.state.offline = true;
  await turn("unconfirmed", 100);
  await assert.rejects(captureManagedConversation(deps, f.native), /project_policy_unconfirmed/);
  assert.deepEqual(bodyReads, []);
  assert.equal(window.candidates(f.native).length, 1, "outage must keep the unread turn");
  f.state.offline = false;
  await captureManagedConversation(deps, f.native);
  assert.equal(f.queue.status().pending, 1);
  f.state.enabled = false;
  f.state.version = 2;
  f.state.updatedAt = new Date(200).toISOString();
  await turn("closed-online", 220);
  f.state.offline = true;
  const restarted = new ManagedProjectControl(f.options);
  await turn("closed-offline", 240, restarted);
  assert.deepEqual(window.candidates(f.native), []);
  const cachePath = join(f.stateDir, "capture-policies", project + ".json");
  const cached = JSON.parse(readFileSync(cachePath));
  assert.equal(statSync(cachePath).mode & 0o777, 0o600);
  assert.deepEqual([cached.project_id, cached.enabled, cached.project_enabled, cached.version, cached.scope],
    [project, false, false, 2, "project"]);
  assert.ok(cached.checked_at && cached.updated_at);
  f.state.offline = false;
  f.state.enabled = true;
  f.state.version = 3;
  f.state.updatedAt = new Date(300).toISOString();
  await turn("resumed", 350);
  await captureManagedConversation(deps, f.native);
  assert.deepEqual(bodyReads, ["answer-unconfirmed", "prompt-unconfirmed", "answer-resumed", "prompt-resumed"]);
  assert.equal(f.queue.status().pending, 2);
  await f.delivery.flush();
  assert.equal(f.queue.status().saved, 2);
  assert.ok(f.writes.every(w => w.mode === "automatic"));
});

test("a raced capture pause preserves the original pending entry for manual replay", async t => {
  const f = await captureFixture(t);
  const id = hash("original-number");
  assert.equal(await f.delivery.enqueue(f.native, id, f.session, {type: "qa", question: "original"}), true);
  f.state.pauseNextAutomatic = true;
  await f.delivery.flush();
  assert.deepEqual(f.queue.status(), {pending: 1, uncertain: 0, saved: 0, blocked: 0});
  assert.deepEqual(f.writes, []);
  f.state.enabled = false;
  f.state.version = 2;
  await f.delivery.flush();
  assert.deepEqual(f.queue.status(), {pending: 1, uncertain: 0, saved: 0, blocked: 0});
  const review = await f.backlog.run({...f.commands, action: "show"});
  const replay = await f.backlog.run({...f.commands, action: "replay", signature: review.signature});
  assert.equal(replay.drained, 1);
  assert.equal(f.writes[0].mode, "manual");
  assert.equal(f.writes[0].session_id, f.session);
  assert.equal(f.writes[0].entry.question, "original");
});

test("OpenCode backlog pauses on a raced gateway denial and permits an explicit manual save while closed", async t => {
  const f = await captureFixture(t);
  const ids = [hash("first"), hash("second")];
  for (const id of ids) await f.delivery.enqueue(f.native, id, f.session, {type: "qa", context: id});
  const command = {...f.commands, action: "backlog_save", session_id: f.session, tokens: ids,
    native_session: f.native, cancel_file: join(f.root, "job.stop")};
  f.state.pauseNextAutomatic = true;
  const automatic = await f.backlog.runBacklog({...command, automatic: true}, () => {});
  assert.deepEqual([automatic.status, automatic.reason, automatic.remaining], ["paused", "project_auto_capture_disabled", 2]);
  assert.equal(f.queue.status().blocked, 0);
  assert.equal(f.queue.status().uncertain, 0);
  f.state.enabled = false;
  f.state.version = 2;
  const stopped = await f.backlog.runBacklog({...command, automatic: true}, () => {});
  assert.deepEqual([stopped.status, stopped.reason, stopped.remaining], ["paused", "project_auto_capture_disabled", 2]);
  const manual = await f.backlog.runBacklog({...command, automatic: false}, () => {});
  assert.deepEqual([manual.status, manual.saved], ["completed", 2]);
  assert.deepEqual(f.writes.map(w => w.mode), ["manual", "manual"]);
  f.state.process = true;
  const {tokens, ...processCommand} = {...command, action: "backlog_process"};
  writeFileSync(command.cancel_file, "project_auto_capture_disabled", {mode: 0o600});
  assert.deepEqual(await f.backlog.runBacklog(processCommand, () => {}), {status: "submitted"});
  writeFileSync(command.cancel_file, "stop", {mode: 0o600});
  assert.deepEqual(await f.backlog.runBacklog(processCommand, () => {}), {status: "deferred"});
});

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

test("unmatched unknown stays isolated while later same-session and new-session records save after restart", async () => {
  const f = fixture();
  try {
    f.queue.enqueue("unknown", "old-turn", {type: "qa", answer: "original"}, target());
    await assert.rejects(f.queue.flush(async () => { throw new Error("lost response"); }, async () => false));
    const original = JSON.parse(readFileSync(f.queue.file)).pending[0];
    f.queue.enqueue("same-session", "old-turn", {type: "qa"}, target());
    f.queue.enqueue("new-session", "new-turn", {type: "qa"}, {...target(), native: "another-native"});
    const restarted = new Outbox("same-installation-and-project", f.root), sent = [], checked = [];
    for (let i = 0; i < 2; i++) await restarted.flush(async item => sent.push(item.id), async item => {checked.push(item.id); return false;});
    assert.deepEqual(sent, ["same-session", "new-session"]);
    assert.deepEqual(checked, ["unknown", "unknown"], "one exact original lookup per pump, never a resend or spin");
    assert.deepEqual(JSON.parse(readFileSync(f.queue.file)).pending, [original]);
  } finally { f.close(); }
});

test("unreadable original unknown does not block a separately authorized fresh target", async () => {
  const f = fixture();
  try {
    f.queue.enqueue("unknown", "old-turn", {type: "qa"}, target());
    await assert.rejects(f.queue.flush(async () => { throw new Error("lost response"); }, async () => false));
    const original = JSON.parse(readFileSync(f.queue.file)).pending[0];
    f.queue.enqueue("fresh", "new-turn", {type: "qa"}, {...target(), native: "authorized-native"});
    const sent = [];
    await f.queue.flush(async item => sent.push(item.id), async () => assert.fail("denied original must not be read"), async item => item.id === "fresh");
    assert.deepEqual(sent, ["fresh"]);
    assert.deepEqual(JSON.parse(readFileSync(f.queue.file)).pending, [original]);
  } finally { f.close(); }
});

test("a newly unknown write still interrupts its current pump; later pump never resends it", async () => {
  const f = fixture();
  try {
    f.queue.enqueue("first", "turn", {type: "qa"}, target());
    f.queue.enqueue("later", "turn", {type: "qa"}, target());
    const sent = [];
    await assert.rejects(f.queue.flush(async item => {sent.push(item.id); throw new Error("lost response");}, async () => false));
    assert.deepEqual(sent, ["first"]);
    await f.queue.flush(async item => sent.push(item.id), async item => {assert.equal(item.id, "first"); return false;});
    assert.deepEqual(sent, ["first", "later"]);
    assert.equal(f.queue.status().uncertain, 1);
  } finally { f.close(); }
});

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

test("HTTP 400/404/409/413/422 isolates single entry without halting healthy tail", async () => {
  for (const status of [400, 404, 409, 413, 422]) {
    const f = fixture(), a = target(), b = {...a, native: "other-session"};
    const badId = "1".repeat(64), healthySameId = "2".repeat(64), healthyOtherId = "3".repeat(64);
    try {
      f.queue.enqueue(badId, "session-a", {type: "qa", question: "too big", answer: "payload"}, a);
      f.queue.enqueue(healthySameId, "session-a", {type: "qa", question: "good same", answer: "ok"}, a);
      f.queue.enqueue(healthyOtherId, "session-b", {type: "qa", question: "good other", answer: "ok"}, b);
      const sent = [];
      await f.queue.flush(async item => {
        sent.push(item.id);
        if (item.id === badId) throw new Error(`Cognee request failed (${status}): rejected`);
      }, async () => assert.fail("definite 4xx rejection must not probe uncertain"));
      assert.deepEqual(sent, [badId, healthySameId, healthyOtherId]);
      assert.equal(f.queue.status().saved, 2);
      assert.equal(f.queue.status().blocked, 1);
      assert.equal(f.queue.status().uncertain, 0);
      const state = JSON.parse(readFileSync(f.queue.file, "utf8"));
      assert.equal(state.pending.length, 1);
      assert.equal(state.pending[0].blocked, true);
      assert.equal(state.pending[0].blocked_reason, "official_request_rejected");
      assert.equal(state.pending[0].uncertain, false);
      assert.deepEqual(f.queue.reviewBacklog(a, "session-a"), {session_id: "session-a", tokens: [], unknown: 0});
      assert.equal(f.queue.entryState(a, "session-a", badId), "rejected");
      await f.queue.withSender(async () => {
        assert.equal(f.queue.releaseBacklogEntry(a, "session-a", badId), "isolated");
      });
    } finally { f.close(); }
  }
});

test("HTTP 429 persists session cooldown across flushes and releaseBacklogEntry without blocking other sessions", async () => {
  const f = fixture(), a = target(), b = {...a, native: "other-session"};
  const s1First = "4".repeat(64), s1Second = "5".repeat(64), s2First = "6".repeat(64), s2Second = "7".repeat(64);
  try {
    f.queue.enqueue(s1First, "session-1", {type: "qa", question: "q1", answer: "a1"}, a);
    f.queue.enqueue(s1Second, "session-1", {type: "qa", question: "q2", answer: "a2"}, a);
    f.queue.enqueue(s2First, "session-2", {type: "qa", question: "q3", answer: "a3"}, b);
    const sent = [];
    await f.queue.flush(async item => {
      sent.push(item.id);
      if (item.id === s1First) throw new Error("Cognee request failed (429): rate limited");
    }, async () => assert.fail("429 backoff must not probe uncertain"));
    assert.deepEqual(sent, [s1First, s2First]);
    assert.equal(f.queue.status().saved, 1);
    assert.equal(f.queue.status().uncertain, 0);
    assert.equal(f.queue.status().blocked, 0);
    assert.equal(f.queue.entryState(a, "session-1", s1First), "deferred");
    assert.equal(f.queue.entryState(a, "session-1", s1Second), "deferred");
    const state = JSON.parse(readFileSync(f.queue.file, "utf8"));
    const deferred = state.pending.find(item => item.id === s1First);
    assert.equal(deferred.retry_count, 1);
    assert.ok(typeof deferred.retry_after === "number" && deferred.retry_after > Date.now());
    assert.ok(state.sessionRateLimits && Object.keys(state.sessionRateLimits).length === 1);
    // Restarting Outbox preserves persisted session cooldown: neither s1First nor s1Second sends,
    // while another session drains immediately and releaseBacklogEntry keeps unexpired cooldown intact.
    const restarted = new Outbox("same-installation-and-project", f.root);
    restarted.enqueue(s2Second, "session-2", {type: "qa", question: "q4", answer: "a4"}, b);
    await restarted.withSender(async () => {
      assert.equal(restarted.releaseBacklogEntry(a, "session-1", s1First), "deferred");
      assert.equal(restarted.releaseBacklogEntry(a, "session-1", s1Second), "deferred");
    });
    const secondPass = [];
    await restarted.flush(async item => secondPass.push(item.id), async () => false);
    assert.deepEqual(secondPass, [s2Second]);
    // Once cooldown expires, releaseBacklogEntry releases and both session-1 items drain, clearing sessionRateLimits.
    const expired = JSON.parse(readFileSync(restarted.file, "utf8"));
    for (const item of expired.pending) { if (item.retry_after) item.retry_after = Date.now() - 1000; }
    for (const key of Object.keys(expired.sessionRateLimits ?? {})) expired.sessionRateLimits[key].retry_after = Date.now() - 1000;
    writeFileSync(restarted.file, JSON.stringify(expired));
    await restarted.withSender(async () => {
      assert.equal(restarted.releaseBacklogEntry(a, "session-1", s1First), "released");
      assert.equal(restarted.releaseBacklogEntry(a, "session-1", s1Second), "released");
    });
    const thirdPass = [];
    await restarted.flush(async item => thirdPass.push(item.id), async () => false);
    assert.deepEqual(thirdPass, [s1First, s1Second]);
    const finalState = JSON.parse(readFileSync(restarted.file, "utf8"));
    assert.equal(finalState.sessionRateLimits, undefined);
  } finally { f.close(); }
});
