import test from "node:test";
import assert from "node:assert/strict";
import { chmodSync, mkdirSync, readFileSync, readdirSync, realpathSync, symlinkSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import http from "node:http";
import { pathToFileURL } from "node:url";

const { ManagedProjectControl } = await import(pathToFileURL(process.env.MEMLANE_OPENCODE_TEST_CONTROL).href);
const root = realpathSync(process.env.MEMLANE_CONTROL_FIXTURE_ROOT);
const origin = process.env.MEMLANE_CONTROL_FIXTURE_ORIGIN;
const tokenFile = join(root, "agent-token");
const manager = readFileSync(join(root, "manager-token"), "utf8");
const options = (name, extra = {}) => ({instanceId: "opencode-fixture", directory: join(root, "repo-a"),
  stateDir: join(root, "metadata-" + name), origin, tokenFile, ...extra});
async function admin(path, method = "GET", body) {
  const res = await fetch(origin + path, {method, headers: {Authorization: "Bearer " + manager, "Content-Type": "application/json"},
    body: body === undefined ? undefined : JSON.stringify(body)});
  assert.equal(res.status, 200);
  return res.json();
}
async function access(project, fields) {
  const {revision} = await admin("/v1/projects");
  await admin(`/v1/projects/${project}/overrides`, "PUT", {expected_revision: revision, agent_id: "a", fields});
}
async function listener(handler) {
  const requests = [];
  const server = http.createServer(async (req, res) => {
    requests.push({method: req.method, path: req.url, headers: req.headers});
    try { await handler(req, res); } catch { res.destroy(); }
  });
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  return {requests, url: `http://127.0.0.1:${server.address().port}`,
    close: async () => { server.closeAllConnections(); await new Promise(resolve => server.close(resolve)); }};
}
async function forward(req) {
  const chunks = [];
  for await (const chunk of req) chunks.push(chunk);
  const body = Buffer.concat(chunks);
  return fetch(origin + req.url, {method: req.method, headers: {
    Authorization: req.headers.authorization, "Content-Type": "application/json"},
    ...(body.length ? {body} : {}), redirect: "error"});
}

test("actual registry binds two projects, pins original sessions and survives client restart", async () => {
  const a = new ManagedProjectControl(options("binding"));
  const first = await a.current("native-a");
  const b = new ManagedProjectControl(options("binding", {directory: join(root, "repo-b")}));
  const second = await b.current("native-b");
  assert.notEqual(first.project_id, second.project_id);
  assert.notEqual(first.dataset_id, second.dataset_id);
  assert.equal((await b.current("native-a")).dataset_id, first.dataset_id);
  const restarted = new ManagedProjectControl(options("binding"));
  assert.equal((await restarted.current("native-b")).dataset_id, second.dataset_id);
  const state = readdirSync(options("binding").stateDir).filter(name => name.endsWith(".json"));
  assert.equal(state.length, 2);
  for (const name of state) assert.ok(!readFileSync(join(options("binding").stateDir, name), "utf8").includes("a".repeat(40)));
});

test("actual effective policy is reread for maintain, read, none and explicit operations", async () => {
  const c = new ManagedProjectControl(options("policy"));
  const p = await c.current("policy-native");
  try {
    assert.equal(await c.permits("policy-native", "capture"), true);
    assert.equal(await c.permits("policy-native", "process"), true);
    await access(p.project_id, {access: "read"});
    assert.equal(await c.permits("policy-native", "read"), true);
    assert.equal(await c.permits("policy-native", "write"), false);
    assert.equal(await c.permits("policy-native", "capture"), false);
    assert.equal(await c.permits("policy-native", "delete"), false);
    await access(p.project_id, {access: "none"});
    assert.equal(await c.permits("policy-native", "read"), false);
    assert.equal(await c.permits("policy-native", "recall"), true, "common read must remain independent");
    await access(p.project_id, {access: "maintain", capture: false, auto_process: false});
    assert.equal(await c.permits("policy-native", "write"), true);
    assert.equal(await c.permits("policy-native", "capture"), false);
    assert.equal(await c.permits("policy-native", "process"), false);
    assert.equal(await c.permits("policy-native", "process", false), true);
  } finally { await access(p.project_id, {access: "maintain", capture: true, auto_process: true}); }
});

test("committed resolve with a lost response recovers only through original binding GET", async () => {
  const proxy = await listener(async (req, res) => {
    const response = await forward(req);
    if (req.method === "POST") { await response.arrayBuffer(); res.destroy(); return; }
    res.writeHead(response.status, {"Content-Type": "application/json"}); res.end(await response.text());
  });
  try {
    const cfg = options("unknown-committed", {origin: proxy.url});
    await assert.rejects(new ManagedProjectControl(cfg).current("unknown-native"));
    const restarted = new ManagedProjectControl(cfg);
    assert.equal((await restarted.current("unknown-native")).ready, true);
    assert.equal(proxy.requests.filter(row => row.method === "POST").length, 1);
    assert.ok(proxy.requests.some(row => row.path.startsWith("/v1/project/binding?")));
    for (const row of proxy.requests) {
      assert.equal(row.headers.authorization, "Bearer " + "a".repeat(40));
      assert.equal(row.headers["x-api-key"], undefined);
    }
  } finally { await proxy.close(); }
});

test("uncommitted unknown resolve stays unknown without replay or project creation", async () => {
  const before = await admin("/v1/projects");
  const proxy = await listener(async (req, res) => {
    if (req.method === "POST") { res.destroy(); return; }
    const response = await forward(req);
    res.writeHead(response.status, {"Content-Type": "application/json"}); res.end(await response.text());
  });
  try {
    const cfg = options("unknown-absent", {origin: proxy.url});
    for (let i = 0; i < 3; i++) await assert.rejects(new ManagedProjectControl(cfg).current("never-committed"));
    assert.equal(proxy.requests.filter(row => row.method === "POST").length, 1);
    const after = await admin("/v1/projects");
    assert.deepEqual(after.projects, before.projects);
  } finally { await proxy.close(); }
});

test("private control token is required; ambient exports cannot nominate credentials", async () => {
  const bad = join(root, "bad-token"); writeFileSync(bad, "b".repeat(40), {mode: 0o644});
  chmodSync(bad, 0o644);
  assert.throws(() => new ManagedProjectControl(options("bad", {tokenFile: bad})));
  const alias = join(root, "token-alias"); symlinkSync(tokenFile, alias);
  assert.throws(() => new ManagedProjectControl(options("alias", {tokenFile: alias})));
  for (const url of ["https://127.0.0.1:12345", "http://localhost:12345", "http://127.0.0.1:12345/other", "http://owner@127.0.0.1:12345"]) {
    assert.throws(() => new ManagedProjectControl(options("bad-url", {origin: url})));
  }
  process.env.MEMLANE_PROJECT_CONTROL_TOKEN = "must-not-use-ambient-token";
  assert.equal((await new ManagedProjectControl(options("ambient")).current("ambient-native")).ready, true);
});

test("control redirects, oversized bodies and malformed policy cannot deliver cached permission", async () => {
  const trap = await listener((_req, res) => res.end("{}"));
  const redirect = await listener((_req, res) => { res.writeHead(307, {Location: trap.url}); res.end(); });
  try {
    await assert.rejects(new ManagedProjectControl(options("redirect", {origin: redirect.url})).current("redirect-native"));
    assert.equal(trap.requests.length, 0);
  } finally { await redirect.close(); await trap.close(); }
  const huge = await listener((_req, res) => res.end("x".repeat(65537)));
  try { await assert.rejects(new ManagedProjectControl(options("huge", {origin: huge.url})).current("huge-native")); }
  finally { await huge.close(); }
  const malformed = await listener(async (req, res) => {
    const response = await forward(req); const body = await response.json();
    if (req.url.startsWith("/v1/project/policy")) body.ready = "true";
    res.setHeader("Content-Type", "application/json"); res.end(JSON.stringify(body));
  });
  try { await assert.rejects(new ManagedProjectControl(options("malformed", {origin: malformed.url})).permits("malformed-native", "write")); }
  finally { await malformed.close(); }
});

test("a changed project, dataset, permission vocabulary or common target is rejected", async () => {
  const wrong = "00000000-0000-0000-0000-000000000001";
  const mutations = [body => {body.project_id = wrong;}, body => {body.dataset_id = wrong;},
    body => {body.policy.permissions = ["share"];}, body => {body.common = {ready: true, readable: true, dataset_id: "bad"};}];
  for (let index = 0; index < mutations.length; index++) {
    const proxy = await listener(async (req, res) => {
      const response = await forward(req); const body = await response.json();
      if (req.url.startsWith("/v1/project/policy")) mutations[index](body);
      res.setHeader("Content-Type", "application/json"); res.end(JSON.stringify(body));
    });
    try {
      const cfg = options("drift-" + index, {origin: proxy.url});
      await assert.rejects(new ManagedProjectControl(cfg).permits("drift-native-" + index, "write"));
      const records = readdirSync(cfg.stateDir).filter(name => name.endsWith(".json"));
      assert.equal(records.length, 1);
      const saved = JSON.parse(readFileSync(join(cfg.stateDir, records[0]), "utf8"));
      assert.notEqual(saved.project_id, wrong);
      assert.notEqual(saved.dataset_id, wrong);
    } finally { await proxy.close(); }
  }
});

test("native identifiers and installation namespaces do not alias or become paths", async () => {
  const cfg = options("ids");
  const a = new ManagedProjectControl(cfg);
  const original = a.hostKey("native");
  cfg.instanceId = "caller-mutated";
  cfg.stateDir = join(root, "caller-mutated-state");
  cfg.tokenFile = join(root, "nonexistent-caller-token");
  assert.equal(a.hostKey("native"), original, "caller changed the installation snapshot");
  const b = new ManagedProjectControl(options("ids-other", {instanceId: "opencode-other"}));
  assert.notEqual(a.hostKey("native"), b.hostKey("native"));
  assert.match(a.hostKey("../native"), /^opencode-[a-f0-9]{64}$/);
  for (const native of ["", "x".repeat(257), null]) await assert.rejects(a.bind(native));
  assert.deepEqual(readdirSync(options("ids").stateDir), []);
  assert.equal((await a.current("snapshot-native")).ready, true);
});


test("queued original target survives cwd changes and cannot discover an unbound native", async () => {
  const c = new ManagedProjectControl(options("queued-target"));
  const original = await c.current("queued-original");
  const target = {native: "queued-original", project_id: original.project_id, dataset_id: original.dataset_id};
  const changed = new ManagedProjectControl(options("queued-target", {directory: join(root, "repo-b")}));
  assert.equal((await changed.currentTarget(target)).dataset_id, original.dataset_id);
  const before = readdirSync(options("queued-target").stateDir).sort();
  const directory = await admin("/v1/projects");
  await assert.rejects(changed.currentTarget({...target, native: "never-bound"}));
  await assert.rejects(changed.currentTarget({...target, project_id: "33333333-3333-4333-8333-333333333333"}));
  await assert.rejects(changed.currentTarget({...target, dataset_id: "33333333-3333-4333-8333-333333333333"}));
  assert.deepEqual(readdirSync(options("queued-target").stateDir).sort(), before);
  assert.equal((await admin("/v1/projects")).revision, directory.revision);
  try {
    await access(original.project_id, {access: "read"});
    assert.deepEqual((await changed.currentTarget(target)).policy.permissions, ["read"]);
  } finally { await access(original.project_id, {access: "maintain", capture: true, auto_process: true}); }
});

const compiled = new URL("./", pathToFileURL(process.env.MEMLANE_OPENCODE_TEST_CONTROL));
const { ManagedDelivery } = await import(new URL("managed-delivery.js", compiled));
const { Outbox } = await import(new URL("runtime.js", compiled));
const { CogneeHttpClient } = await import(new URL("client.js", compiled));
async function officialServer({loseFirstResponse = false, credential = "synthetic-child-key", loseLifecycleResponse = false, loseImproveResponse = false, loseRememberResponse = false, sessionDetail, afterEntry} = {}) {
  const writes = [], recalls = [], lifecycles = [], improvements = [], remembers = [];
  let lookups = 0;
  const server = await listener(async (req, res) => {
    assert.equal(req.headers["x-api-key"], credential);
    assert.equal(req.headers.authorization, undefined);
    res.setHeader("Content-Type", "application/json");
    if (req.method === "POST" && req.url === "/api/v1/remember/entry") {
      const chunks = [];
      for await (const chunk of req) chunks.push(chunk);
      writes.push(JSON.parse(Buffer.concat(chunks).toString()));
      if (afterEntry) await afterEntry(writes.length);
      if (loseFirstResponse && writes.length === 1) { res.destroy(); return; }
      res.end('{"status":"saved"}'); return;
    }
    if (req.method === "GET" && req.url.startsWith("/api/v1/sessions/")) {
      lookups++;
      const session = decodeURIComponent(req.url.split("/").at(-1));
      const items = writes.filter(item => item.session_id === session);
      const detail = {session_id: session, dataset_id: items[0]?.dataset_id,
        qas: items.filter(item => item.entry.type === "qa").map(item => item.entry),
        traces: items.filter(item => item.entry.type === "trace").map(item => item.entry)};
      res.end(JSON.stringify(sessionDetail ? sessionDetail(detail) : detail)); return;
    }
    if (req.method === "POST" && req.url === "/api/v1/recall") {
      const chunks = []; for await (const chunk of req) chunks.push(chunk);
      const body = JSON.parse(Buffer.concat(chunks).toString()); recalls.push(body);
      res.end(JSON.stringify([{id: "hit", text: "knowledge " + body.dataset_ids[0], score: 1}])); return;
    }
    if (req.method === "POST" && /\/agents\/(?:un)?register$/.test(req.url)) {
      const chunks = []; for await (const chunk of req) chunks.push(chunk);
      lifecycles.push(JSON.parse(Buffer.concat(chunks).toString()));
      if (loseLifecycleResponse) { res.destroy(); return; }
      res.end('{"status":"ok"}'); return;
    }
    if (req.method === "POST" && req.url === "/api/v1/improve") {
      const chunks = []; for await (const chunk of req) chunks.push(chunk);
      improvements.push(JSON.parse(Buffer.concat(chunks).toString()));
      if (loseImproveResponse) { res.destroy(); return; }
      res.end('{"status":"running"}'); return;
    }
    if (req.method === "POST" && req.url === "/api/v1/remember") {
      const chunks = []; for await (const chunk of req) chunks.push(chunk);
      const raw = Buffer.concat(chunks).toString();
      const field = name => new RegExp('name="' + name + '"\\r\\n\\r\\n([^\\r]+)').exec(raw)?.[1];
      const target = {dataset_id: field("datasetId"), dataset_name: field("datasetName")};
      remembers.push({...target, session_id: field("session_id"), node_set: field("node_set"), run_in_background: field("run_in_background")});
      if (loseRememberResponse) { res.destroy(); return; }
      res.end(JSON.stringify({...target, status: "running"})); return;
    }
    assert.fail("unexpected official route " + req.url);
  });
  return {...server, writes, recalls, lifecycles, improvements, remembers, lookups: () => lookups,
    client: new CogneeHttpClient(server.url, credential, undefined, undefined, 500)};
}

test("delivery gates original-target official writes and keeps denied items blocked", async () => {
  const c = new ManagedProjectControl(options("delivery-policy"));
  const p = await c.current("delivery-native");
  const s = await officialServer();
  const outbox = new Outbox("delivery", join(root, "delivery-outbox"));
  const delivery = new ManagedDelivery(c, s.client, outbox);
  try {
    assert.equal(await delivery.enqueue("delivery-native", "queued-before-revoke", "original-official-session", {type: "qa", context: "marker-original"}), true);
    await access(p.project_id, {access: "read"});
    await delivery.flush();
    assert.equal(s.writes.length, 0);
    assert.equal(outbox.status().blocked, 1);
    assert.equal(await delivery.enqueue("delivery-native", "disabled-entry", "turn", {type: "qa"}), false);
    await access(p.project_id, {access: "maintain", capture: true, auto_process: true});
    await delivery.flush();
    assert.equal(s.writes.length, 0, "restoration must not release a denial");
    // Primitive release only: production management authorization is still separate.
    outbox.retryBlocked(["queued-before-revoke"]);
    await delivery.flush();
    assert.equal(s.writes.length, 1);
    assert.equal(s.writes[0].dataset_id, p.dataset_id);
    assert.equal(s.writes[0].session_id, "original-official-session");
  } finally {
    await access(p.project_id, {access: "maintain", capture: true, auto_process: true});
    await s.close();
  }
});

test("delivery unknown response reconciles original marker after restart in another cwd", async () => {
  const c = new ManagedProjectControl(options("delivery-unknown"));
  const p = await c.current("unknown-delivery-native");
  const s = await officialServer({loseFirstResponse: true});
  const queueRoot = join(root, "unknown-delivery-outbox");
  const outbox = new Outbox("same-installation", queueRoot);
  try {
    const delivery = new ManagedDelivery(c, s.client, outbox);
    await delivery.enqueue("unknown-delivery-native", "lost-result", "original-session", {type: "qa", context: "opencode_capture:lost-result"});
    await assert.rejects(delivery.flush());
    assert.equal(outbox.status().uncertain, 1);
    await access(p.project_id, {access: "none"});
    await delivery.flush();
    assert.equal(outbox.status().uncertain, 1);
    assert.equal(s.lookups(), 0, "withdrawal must prevent official session reads");
    await access(p.project_id, {access: "read"});
    const restarted = new ManagedDelivery(new ManagedProjectControl(options("delivery-unknown", {directory: join(root, "repo-b")})),
      s.client, new Outbox("same-installation", queueRoot));
    await restarted.flush();
    assert.equal(s.writes.length, 1);
    assert.equal(s.lookups(), 1);
    assert.equal(restarted.outbox.status().saved, 1);
  } finally {
    await access(p.project_id, {access: "maintain", capture: true, auto_process: true});
    await s.close();
  }
});

test("unknown receipt requires original official session, dataset and exact QA marker field", async () => {
  const c = new ManagedProjectControl(options("unknown-receipt-target")), native = "unknown-receipt-native";
  await c.current(native);
  const variants = [
    detail => ({...detail, dataset_id: "33333333-3333-4333-8333-333333333333"}),
    detail => ({...detail, dataset_id: undefined}),
    detail => ({...detail, session_id: "different-official-session"}),
    detail => ({...detail, qas: detail.qas.map(qa => ({...qa, answer: qa.context, context: "other-context"}))}),
  ];
  for (const [index, sessionDetail] of variants.entries()) {
    const s = await officialServer({loseFirstResponse: true, sessionDetail});
    const queueRoot = join(root, "unknown-target-outbox-" + index);
    const queue = new Outbox("original", queueRoot);
    try {
      const delivery = new ManagedDelivery(c, s.client, queue);
      await delivery.enqueue(native, "original-marker-" + index, "original-session", {type: "qa", context: "opencode_capture:original-" + index});
      await assert.rejects(delivery.flush());
      await new ManagedDelivery(c, s.client, new Outbox("original", queueRoot)).flush();
      assert.equal(queue.status().uncertain, 1, "nonmatching official evidence consumed the original unknown entry");
      assert.equal(queue.status().saved, 0);
      assert.equal(s.writes.length, 1, "unknown entry was resent");
    } finally { await s.close(); }
  }
});

test("unconfirmed original target never manufactures an uncertain write or calls official service", async () => {
  const c = new ManagedProjectControl(options("delivery-unconfirmed"));
  const p = await c.current("unconfirmed-native");
  const s = await officialServer();
  const outbox = new Outbox("unconfirmed", join(root, "unconfirmed-outbox"));
  try {
    outbox.enqueue("unbound-native", "turn", {type: "qa"},
      {native: "unrecognized-native", project_id: p.project_id, dataset_id: p.dataset_id});
    const delivery = new ManagedDelivery(c, s.client, outbox);
    for (let i = 0; i < 2; i++) await assert.rejects(delivery.flush());
    assert.equal(outbox.status().uncertain, 0);
    assert.equal(outbox.status().pending, 1);
    assert.equal(s.requests.length, 0);
    assert.equal(readdirSync(options("delivery-unconfirmed").stateDir).filter(n => n.endsWith(".json")).length, 1);
  } finally { await s.close(); }
});


test("official capture callbacks read only observed post-activation messages and never backfill disabled turns", async () => {
  const { CogneeOpenCodePlugin } = await import(new URL("plugin.js", compiled));
  const s = await officialServer({credential: "c".repeat(40)});
  const apiKeyFile = join(root, "callback-official-key");
  writeFileSync(apiKeyFile, "c".repeat(40), {mode: 0o600});
  const reads = [];
  const native = "callback-native";
  const cfg = options("host-capture");
  const messages = new Map();
  let wholeSessionReads = 0;
  const hooks = await CogneeOpenCodePlugin({directory: join(root, "repo-a"), client: {session: {
    messages: async () => { wholeSessionReads++; throw new Error("history reads forbidden"); },
    message: async ({path}) => { reads.push(path.messageID); return {data: messages.get(path.messageID)}; },
  }}}, {memlane: cfg, cognee: {baseUrl: s.url, apiKeyFile, stateDir: join(root, "host-capture-outbox"),
    autoRecall: false, improveOnSessionEnd: false}});
  const prompt = async (id, created = Date.now()) => {
    messages.set(id, {info: {id, sessionID: native, role: "user", time: {created}}, parts: [{type: "text", text: "Question " + id}]});
    await hooks["chat.message"]({sessionID: native}, {message: messages.get(id).info, parts: messages.get(id).parts});
  };
  const complete = async (id, parent) => {
    const info = {id, parentID: parent, sessionID: native, role: "assistant", time: {created: Date.now(), completed: Date.now()}};
    messages.set(id, {info, parts: [{type: "text", text: "Answer " + id}, {type: "text", synthetic: true, text: "NEVER_CAPTURE_SYNTHETIC"}]});
    await hooks.event({event: {type: "message.updated", properties: {info}}});
  };
  const idle = () => hooks.event({event: {type: "session.idle", properties: {sessionID: native}}});
  let p;
  try {
    await prompt("pre-activation", 1);
    await complete("old-answer", "pre-activation");
    await idle();
    assert.deepEqual(reads, []);
    await prompt("new-prompt");
    await complete("new-answer", "new-prompt");
    await idle();
    assert.deepEqual(reads, ["new-answer", "new-prompt"]);
    assert.equal(s.writes.length, 1);
    assert.equal(s.writes[0].entry.question, "Question new-prompt");
    assert.equal(s.writes[0].entry.answer, "Answer new-answer");
    await complete("new-answer", "new-prompt"); await idle();
    assert.equal(reads.length, 2, "duplicate completion reread captured message");
    p = await new ManagedProjectControl(cfg).current(native);
    assert.equal(s.writes[0].dataset_id, p.dataset_id);
    await access(p.project_id, {access: "read"});
    await prompt("disabled-prompt");
    await access(p.project_id, {access: "maintain", capture: true, auto_process: true});
    await complete("disabled-answer", "disabled-prompt"); await idle();
    await hooks["tool.execute.after"]({sessionID: native, callID: "disabled-tool", tool: "edit", args: {filePath: "src/main.ts"}}, {output: "skip disabled turn"});
    assert.equal(s.writes.length, 1);
    assert.equal(reads.length, 2);
    await prompt("withdrawn-prompt");
    await complete("withdrawn-answer", "withdrawn-prompt");
    await access(p.project_id, {access: "read"}); await idle();
    await access(p.project_id, {access: "maintain", capture: true, auto_process: true});
    await complete("withdrawn-answer", "withdrawn-prompt"); await idle();
    assert.equal(reads.length, 2, "restoration backfilled a withdrawn turn");
    await prompt("fresh-tool-prompt");
    await hooks["tool.execute.after"]({sessionID: native, callID: "fresh-tool", tool: "edit", args: {filePath: "src/main.ts"}}, {output: "updated password=do-not-store"});
    await idle();
    assert.equal(s.writes.length, 2);
    assert.equal(s.writes[1].dataset_id, p.dataset_id);
    assert.equal(s.writes[1].entry.type, "trace");
    assert.ok(!JSON.stringify(s.writes).includes("do-not-store"));
    await hooks["tool.execute.after"]({sessionID: native, callID: "private-tool", tool: "read", args: {filePath: ".env"}}, {output: "never-store-private-file"});
    await idle();
    assert.equal(wholeSessionReads, 0);
    assert.equal(s.writes.length, 2);
  } finally {
    if (p) await access(p.project_id, {access: "maintain", capture: true, auto_process: true});
    await hooks.dispose(); await s.close();
  }
});


const { resolveManagedConfig } = await import(new URL("managed-config.js", compiled));
const { ManagedRecall } = await import(new URL("managed-recall.js", compiled));
test("managed config reads only its private child key and ignores ambient owner/cloud options", async () => {
  const apiKeyFile = join(root, "managed-config-key"); writeFileSync(apiKeyFile, "c".repeat(40), {mode: 0o600});
  const config = {baseUrl: "http://127.0.0.1:19876", apiKeyFile, stateDir: join(root, "managed-config-state")};
  const overrides = {COGNEE_API_KEY: "ambient-owner", COGNEE_SERVICE_URL: "https://wrong.invalid", COGNEE_MODE: "cloud", COGNEE_CAPTURE: "false", COGNEE_PLUGIN_DATASET: "wrong-dataset"};
  const before = Object.fromEntries(Object.keys(overrides).map(key => [key, process.env[key]]));
  try {
    Object.assign(process.env, overrides);
    const actual = resolveManagedConfig({...config, apiKey: "caller-owner", username: "owner", password: "secret", readScopes: {extra: "other-dataset"}});
    assert.equal(actual.apiKey, "c".repeat(40)); assert.equal(actual.baseUrl, config.baseUrl);
    assert.equal(actual.mode, "local"); assert.equal(actual.autoCapture, true);
    assert.equal(actual.searchType, "CHUNKS");
    assert.equal(actual.username, undefined); assert.equal(actual.password, undefined);
    assert.deepEqual(actual.readScopes, {});
    // Official graph recall can legitimately take longer than the old 2.5s
    // deadline. Exercise the real per-turn query/context path with that latency.
    const policy = {common: {ready: false}, policy: {permissions: ["read"]}, dataset_id: "slow-project"};
    const recall = new ManagedRecall({current: async () => policy}, {
      recall: async () => {
        await new Promise(resolve => setTimeout(resolve, 2700));
        return [{id: "slow-result", text: "zero disables batching", score: 1}];
      },
    }, actual);
    await recall.query("slow-native", "batch convention", "slow-session");
    assert.ok((await recall.context("slow-native")).includes("zero disables batching"));
    for (const bad of [{...config, mode: "cloud"}, {...config, baseUrl: "https://remote.invalid"}, {...config, apiKeyFile: undefined}, {...config, stateDir: "relative"}]) assert.throws(() => resolveManagedConfig(bad));
    const alias = join(root, "managed-config-alias"); symlinkSync(apiKeyFile, alias);
    assert.throws(() => resolveManagedConfig({...config, apiKeyFile: alias}));
    chmodSync(apiKeyFile, 0o644); assert.throws(() => resolveManagedConfig(config));
    chmodSync(apiKeyFile, 0o600);
  } finally { for (const [key, value] of Object.entries(before)) { if (value === undefined) delete process.env[key]; else process.env[key] = value; } }
});

test("managed recall targets live dataset IDs and permanently drops withdrawn fragments", async () => {
  let unavailable = false;
  const proxy = await listener(async (req, res) => {
    if (unavailable) { res.writeHead(503); res.end(); return; }
    const response = await forward(req); res.writeHead(response.status, {"Content-Type": "application/json"}); res.end(await response.text());
  });
  const c = new ManagedProjectControl(options("recall", {origin: proxy.url}));
  const native = "recall-native", p = await c.current(native), s = await officialServer();
  const recall = new ManagedRecall(c, s.client, {maxResults: 3, minScore: 0.1, recallTimeoutMs: 1000, searchType: "GRAPH_COMPLETION"});
  try {
    await recall.query(native, "query", "official-recall-session");
    let context = await recall.context(native);
    assert.ok(context.includes(p.dataset_id)); assert.ok(context.includes(p.common.dataset_id));
    assert.equal(s.recalls.length, 2);
    assert.deepEqual(s.recalls.map(call => call.dataset_ids[0]), [p.common.dataset_id, p.dataset_id]);
    for (const call of s.recalls) {
      assert.equal(call.session_id, undefined, "automatic recall must not complete from conversation history");
      assert.equal(call.only_context, true, "automatic recall must return source context, not an LLM answer");
    }
    assert.equal(s.recalls[0].node_name, undefined);
    assert.deepEqual(s.recalls[1].node_name, ["session_learnings", "memory"]);
    await access(p.project_id, {access: "none"});
    context = await recall.context(native);
    assert.ok(!context.includes(p.dataset_id)); assert.ok(context.includes(p.common.dataset_id));
    await access(p.project_id, {access: "maintain", capture: true, auto_process: true});
    assert.ok(!(await recall.context(native)).includes(p.dataset_id), "permission restoration resurrected a revoked fragment");
    await recall.query(native, "fresh query", "official-recall-session");
    assert.ok((await recall.context(native)).includes(p.dataset_id));
    unavailable = true; assert.equal(await recall.context(native), "");
    unavailable = false; assert.equal(await recall.context(native), "", "outage fallback resurrected cached body");
    assert.equal(s.requests.filter(req => req.path.endsWith("/datasets")).length, 0);
  } finally { unavailable = false; await access(p.project_id, {access: "maintain", capture: true, auto_process: true}); await proxy.close(); await s.close(); }
});

test("official system and compacting callbacks remove only owned stale context and isolate processing sessions", async () => {
  const { CogneeOpenCodePlugin } = await import(new URL("plugin.js", compiled));
  const s = await officialServer({credential: "c".repeat(40)});
  const apiKeyFile = join(root, "injection-official-key"); writeFileSync(apiKeyFile, "c".repeat(40), {mode: 0o600});
  const cfg = options("injection"), native = "injection-native";
  const hooks = await CogneeOpenCodePlugin({directory: join(root, "repo-a"), client: {}}, {memlane: cfg,
    cognee: {baseUrl: s.url, apiKeyFile, stateDir: join(root, "injection-outbox"), autoCapture: false, improveOnSessionEnd: false}});
  const p = await new ManagedProjectControl(cfg).current(native);
  const system = {system: ["OTHER_PLUGIN_CONTEXT"]};
  try {
    const prompt = id => hooks["chat.message"]({sessionID: native}, {message: {id, time: {created: Date.now()}}, parts: [{type: "text", text: "coding question"}]});
    await prompt("inject-one");
    await hooks["experimental.chat.system.transform"]({sessionID: native}, system);
    assert.equal(system.system.length, 2); assert.ok(system.system[1].includes(p.dataset_id));
    await access(p.project_id, {access: "none"});
    await hooks["experimental.chat.system.transform"]({sessionID: native}, system);
    assert.equal(system.system[0], "OTHER_PLUGIN_CONTEXT"); assert.equal(system.system.length, 2);
    assert.ok(!system.system[1].includes(p.dataset_id)); assert.ok(system.system[1].includes(p.common.dataset_id));
    await hooks["experimental.chat.system.transform"]({}, system);
    assert.deepEqual(system.system, ["OTHER_PLUGIN_CONTEXT"]);
    await access(p.project_id, {access: "maintain", capture: true, auto_process: true});
    await prompt("inject-two");
    const compact = {context: ["OTHER_COMPACTION_CONTEXT"]};
    await hooks["experimental.session.compacting"]({sessionID: native}, compact);
    assert.equal(compact.context[0], "OTHER_COMPACTION_CONTEXT"); assert.ok(compact.context[1].includes(p.dataset_id));
    const old = process.env.MEMLANE_PROCESSING_SESSION;
    try { process.env.MEMLANE_PROCESSING_SESSION = "1"; assert.deepEqual(await CogneeOpenCodePlugin({}, {}), {}); }
    finally { if (old === undefined) delete process.env.MEMLANE_PROCESSING_SESSION; else process.env.MEMLANE_PROCESSING_SESSION = old; }
  } finally { await access(p.project_id, {access: "maintain", capture: true, auto_process: true}); await hooks.dispose(); await s.close(); }
});


test("late context checks cannot resurrect a previous turn or overwrite a newer recall", async () => {
  const c = new ManagedProjectControl(options("recall-race"));
  const p = await c.current("race-native");
  let block = false, release, started;
  const controlPort = {current: async () => { if (block) { started(); await new Promise(resolve => { release = resolve; }); } return p; }};
  const clientPort = {recall: async ({queryText}) => [{id: "race", text: queryText, score: 1}]};
  const recall = new ManagedRecall(controlPort, clientPort, {maxResults: 3, minScore: 0.1, recallTimeoutMs: 1000, searchType: "GRAPH_COMPLETION"});
  await recall.query("race-native", "PREVIOUS_TURN", "session");
  block = true;
  const observed = new Promise(resolve => { started = resolve; });
  const oldContext = recall.context("race-native"); await observed;
  block = false;
  await recall.query("race-native", "CURRENT_TURN", "session");
  release();
  assert.equal(await oldContext, "");
  assert.ok((await recall.context("race-native")).includes("CURRENT_TURN"));
  assert.ok(!(await recall.context("race-native")).includes("PREVIOUS_TURN"));
  block = true;
  const clearing = new Promise(resolve => { started = resolve; });
  const stale = recall.context("race-native"); await clearing;
  recall.clear("race-native"); block = false; release();
  assert.equal(await stale, ""); assert.equal(await recall.context("race-native"), "");
});

const { ManagedActions } = await import(new URL("managed-actions.js", compiled));
const { hash } = await import(new URL("runtime.js", compiled));
test("lifecycle receipts preserve accepted history and unknown heartbeat never advances by time", async () => {
  const c = new ManagedProjectControl(options("actions-lifecycle")), native = "actions-lifecycle-native";
  const p = await c.current(native), s = await officialServer(), lost = await officialServer({loseLifecycleResponse: true});
  let clock = 60000;
  const receiptRoot = join(root, "lifecycle-actions");
  const actions = new ManagedActions(c, s.client, receiptRoot, "opencode-fixture", () => clock);
  try {
    assert.equal((await actions.lifecycle(native, "original-session")).status, "accepted");
    await actions.lifecycle(native, "original-session"); assert.equal(s.lifecycles.length, 1);
    clock += 30000; await actions.lifecycle(native, "original-session"); assert.equal(s.lifecycles.length, 2);
    assert.deepEqual(s.lifecycles[0].dataset_ids, [p.dataset_id]);
    assert.ok(readdirSync(receiptRoot).some(name => name.includes(".g1.json")));
    const unknownRoot = join(root, "unknown-lifecycle-actions");
    const unknown = new ManagedActions(c, lost.client, unknownRoot, "opencode-fixture", () => clock);
    assert.equal((await unknown.lifecycle(native, "original-session")).status, "unknown");
    clock += 60000;
    assert.equal((await new ManagedActions(c, lost.client, unknownRoot, "opencode-fixture", () => clock).lifecycle(native, "original-session")).status, "unknown");
    assert.equal(lost.lifecycles.length, 1);
  } finally { await s.close(); await lost.close(); }
});

test("processing uses original dataset and watermark; an unknown request blocks newer watermarks", async () => {
  const c = new ManagedProjectControl(options("actions-improve")), native = "actions-improve-native";
  const p = await c.current(native), s = await officialServer(), lost = await officialServer({loseImproveResponse: true});
  const actions = new ManagedActions(c, s.client, join(root, "process-actions"), "opencode-fixture");
  try {
    const first = await actions.improve(native, "original-process-session", hash("saved-first"));
    assert.equal(first.status, "accepted"); assert.equal(first.result_status, "running");
    await actions.improve(native, "original-process-session", hash("saved-first"));
    assert.equal(s.improvements.length, 1);
    assert.equal(s.improvements[0].dataset_id, p.dataset_id); assert.equal(s.improvements[0].dataset_name, undefined);
    await actions.improve(native, "original-process-session", hash("saved-next")); assert.equal(s.improvements.length, 2);
    await access(p.project_id, {access: "maintain", capture: true, auto_process: false});
    assert.equal((await actions.improve(native, "original-process-session", hash("disabled"))).status, "rejected");
    assert.equal(s.improvements.length, 2);
    await access(p.project_id, {access: "maintain", capture: true, auto_process: true});
    await actions.improve(native, "original-process-session", hash("disabled")); assert.equal(s.improvements.length, 2);
    const lostRoot = join(root, "unknown-process-actions");
    const unknown = new ManagedActions(c, lost.client, lostRoot, "opencode-fixture");
    assert.equal((await unknown.improve(native, "original-process-session", hash("lost-first"))).status, "unknown");
    const restarted = new ManagedActions(c, lost.client, lostRoot, "opencode-fixture");
    assert.equal((await restarted.improve(native, "original-process-session", hash("new-material"))).status, "unknown");
    assert.equal(lost.improvements.length, 1);
  } finally { await access(p.project_id, {access: "maintain", capture: true, auto_process: true}); await s.close(); await lost.close(); }
});

test("explicit remember ignores auto-capture flag, journals only metadata and never replays unknown", async () => {
  const c = new ManagedProjectControl(options("actions-remember")), native = "actions-remember-native";
  const p = await c.current(native), s = await officialServer(), lost = await officialServer({loseRememberResponse: true});
  const receiptRoot = join(root, "remember-actions");
  const actions = new ManagedActions(c, s.client, receiptRoot, "opencode-fixture");
  try {
    await access(p.project_id, {access: "maintain", capture: false, auto_process: false});
    const first = await actions.remember(native, "explicit-message", "original-session", "SYNTHETIC_FACT_DO_NOT_STORE_IN_RECEIPT");
    assert.equal(first.status, "accepted"); assert.equal(s.remembers[0].dataset_id, p.dataset_id);
    assert.equal(s.remembers[0].session_id, undefined, "file upload must use permanent ingestion; session cache drops upload placeholders");
    assert.equal(s.remembers[0].node_set, "memory");
    assert.equal(s.remembers[0].run_in_background, "true");
    await actions.remember(native, "explicit-message", "original-session", "SYNTHETIC_FACT_DO_NOT_STORE_IN_RECEIPT"); assert.equal(s.remembers.length, 1);
    for (const file of readdirSync(receiptRoot).filter(name => name.endsWith(".json"))) assert.ok(!readFileSync(join(receiptRoot, file), "utf8").includes("SYNTHETIC_FACT"));
    await access(p.project_id, {access: "read"});
    assert.equal((await actions.remember(native, "read-message", "original-session", "rejected fact")).status, "rejected"); assert.equal(s.remembers.length, 1);
    await access(p.project_id, {access: "maintain", capture: true, auto_process: true});
    const lostRoot = join(root, "unknown-remember-actions");
    const unknown = new ManagedActions(c, lost.client, lostRoot, "opencode-fixture");
    assert.equal((await unknown.remember(native, "lost-message", "original-session", "lost fact")).status, "unknown");
    assert.equal((await new ManagedActions(c, lost.client, lostRoot, "opencode-fixture").remember(native, "new-message-same-unknown-fact", "original-session", "lost fact")).status, "unknown");
    assert.equal(lost.remembers.length, 1);
  } finally { await access(p.project_id, {access: "maintain", capture: true, auto_process: true}); await s.close(); await lost.close(); }
});

test("official action callbacks process each saved watermark once and report explicit request status", async () => {
  const { CogneeOpenCodePlugin } = await import(new URL("plugin.js", compiled));
  const s = await officialServer({credential: "c".repeat(40)}), cfg = options("action-hooks"), native = "action-hooks-native";
  const apiKeyFile = join(root, "action-hooks-key"); writeFileSync(apiKeyFile, "c".repeat(40), {mode: 0o600});
  const hooks = await CogneeOpenCodePlugin({directory: join(root, "repo-a"), client: {}}, {memlane: cfg,
    cognee: {baseUrl: s.url, apiKeyFile, stateDir: join(root, "action-hooks-outbox"), autoRecall: false}});
  const p = await new ManagedProjectControl(cfg).current(native);
  try {
    await hooks["chat.message"]({sessionID: native}, {message: {id: "hook-message", time: {created: Date.now()}}, parts: [{type: "text", text: "new coding turn"}]});
    await hooks["tool.execute.after"]({sessionID: native, callID: "hook-edit", tool: "edit", args: {filePath: "src/main.ts"}}, {output: "changed"});
    const idle = () => hooks.event({event: {type: "session.idle", properties: {sessionID: native}}});
    await idle(); await idle();
    assert.equal(s.improvements.length, 1); assert.equal(s.improvements[0].dataset_id, p.dataset_id);
    assert.equal(s.lifecycles[0].dataset_ids[0], p.dataset_id);
    const accepted = await hooks.tool.cognee_remember.execute({fact: "synthetic explicit fact"}, {sessionID: native, messageID: "explicit-hook-message"});
    assert.match(accepted, /accepted/); assert.match(accepted, /not yet confirmed/);
    await access(p.project_id, {access: "read"});
    const denied = await hooks.tool.cognee_remember.execute({fact: "not allowed"}, {sessionID: native, messageID: "denied-hook-message"});
    assert.match(denied, /rejected/); assert.equal(s.remembers.length, 1);
  } finally { await access(p.project_id, {access: "maintain", capture: true, auto_process: true}); await hooks.dispose(); await s.close(); }
});


test("two action instances claim the same explicit fact once even with different host message IDs", async () => {
  const c = new ManagedProjectControl(options("actions-concurrent")), native = "concurrent-action-native";
  const s = await officialServer(), receiptRoot = join(root, "concurrent-actions");
  try {
    const a = new ManagedActions(c, s.client, receiptRoot, "opencode-fixture");
    const b = new ManagedActions(c, s.client, receiptRoot, "opencode-fixture");
    const results = await Promise.all([a.remember(native, "message-a", "same-session", "same explicit fact"), b.remember(native, "message-b", "same-session", "same explicit fact")]);
    assert.ok(results.some(receipt => receipt.status === "accepted"));
    assert.equal(s.remembers.length, 1);
    assert.equal((await b.remember(native, "message-c", "same-session", "same explicit fact")).status, "accepted");
    assert.equal(s.remembers.length, 1);
  } finally { await s.close(); }
});


test("startup metadata discovery binds only new sessions in this exact directory without reading messages", async () => {
  const { CogneeOpenCodePlugin } = await import(new URL("plugin.js", compiled));
  const s = await officialServer({credential: "c".repeat(40)}), cfg = options("startup-discovery");
  const apiKeyFile = join(root, "discovery-key"); writeFileSync(apiKeyFile, "c".repeat(40), {mode: 0o600});
  let rows = [], lists = 0;
  const hooks = await CogneeOpenCodePlugin({directory: join(root, "repo-a"), client: {session: {
    list: async () => { lists++; return {data: rows}; },
    messages: async () => assert.fail("metadata discovery read history"), message: async () => assert.fail("metadata discovery read body"),
  }}}, {memlane: cfg, cognee: {baseUrl: s.url, apiKeyFile, stateDir: join(root, "startup-discovery-outbox"), autoRecall: false, improveOnSessionEnd: false}});
  rows = [{id: "new-metadata-native", directory: join(root, "repo-a"), time: {created: Date.now()}},
    {id: "old-metadata-native", directory: join(root, "repo-a"), time: {created: 1}},
    {id: "wrong-directory-native", directory: join(root, "repo-b"), time: {created: Date.now()}}];
  try {
    for (let n = 0; n < 30 && s.lifecycles.length === 0; n++) await new Promise(resolve => setTimeout(resolve, 20));
    assert.equal(lists, 1); assert.equal(s.lifecycles.length, 1);
    assert.equal(readdirSync(cfg.stateDir).filter(name => name.endsWith(".json")).length, 1);
    assert.equal(s.writes.length, 0); assert.equal(s.recalls.length, 0);
  } finally { await hooks.dispose(); await s.close(); }
});

const { ManagedPending } = await import(new URL("managed-pending.js", compiled));
async function pendingFixture(name, serverOptions = {}) {
  const cfg = options("pending-" + name, {instanceId: "a"}), native = "pending-" + name;
  const control = new ManagedProjectControl(cfg), policy = await control.current(native);
  const server = await officialServer(serverOptions), queue = new Outbox("pending-" + name, join(root, "pending-queue-" + name));
  const actions = new ManagedActions(control, server.client, join(root, "pending-actions-" + name), "a");
  const session = `opencode_${hash("a").slice(0, 16)}_${hash(native).slice(0, 24)}`;
  const command = {project_id: policy.project_id, dataset_id: policy.dataset_id, host_session_key: control.hostKey(native)};
  const operator = managementTokenFile => new ManagedPending(control, server.client, queue, actions,
    {origin, instanceId: "a", managementTokenFile});
  return {cfg, native, policy, control, server, queue, session, command, operator,
    target: {native, project_id: policy.project_id, dataset_id: policy.dataset_id},
    manager: operator(join(root, "manager-token")),
    close: async () => {await access(policy.project_id, {access: "maintain", capture: true, auto_process: true}); await server.close();}};
}

test("pending management authenticates the operator and sends only the reviewed original session", async () => {
  const f = await pendingFixture("auth");
  try {
    f.queue.enqueue("selected-auth", f.session, {type: "qa", context: "opencode_capture:selected", answer: "PRIVATE_OPERATOR_BODY"}, f.target);
    await assert.rejects(f.queue.flush(async () => {throw new Error("Cognee request failed (403)");}, async () => false));
    const before = readFileSync(f.queue.file);
    await assert.rejects(f.operator(tokenFile).run({...f.command, action: "show"}), /pending_manager_auth_required/);
    await assert.rejects(f.manager.run({...f.command, project_id: "33333333-3333-4333-8333-333333333333", action: "show"}), /original_project_target_unconfirmed/);
    assert.deepEqual(readFileSync(f.queue.file), before); assert.equal(f.server.requests.length, 0);
    const review = await f.manager.run({...f.command, action: "show"});
    assert.equal(review.pending, 1); assert.ok(!JSON.stringify(review).includes("PRIVATE_OPERATOR_BODY"));
    const otherNative = "pending-auth-other", otherPolicy = await f.control.current(otherNative);
    const otherSession = `opencode_${hash("a").slice(0,16)}_${hash(otherNative).slice(0,24)}`;
    f.queue.enqueue("unselected-auth", otherSession, {type: "qa", context: "opencode_capture:other"},
      {native: otherNative, project_id: otherPolicy.project_id, dataset_id: otherPolicy.dataset_id});
    const result = await f.manager.run({...f.command, action: "replay", signature: review.signature});
    assert.equal(result.drained, 1); assert.equal(result.pending, 0);
    assert.equal(f.queue.status().pending, 1); assert.equal(f.server.writes.length, 1);
    assert.equal(f.server.writes[0].session_id, f.session); assert.equal(f.server.writes[0].dataset_id, f.policy.dataset_id);
    assert.equal(f.server.improvements.length, 1); assert.equal(result.processing_status, "running");
  } finally {await f.close();}
});

test("authenticated unknown review acknowledges exact originals, refuses missing proof and discards only local copies", async () => {
  for (const shadow of [false, true]) {
    const f = await pendingFixture("unknown-" + shadow, {loseFirstResponse: true,
      ...(shadow ? {sessionDetail: detail => ({...detail, dataset_id: "33333333-3333-4333-8333-333333333333"})} : {})});
    try {
      const delivery = new ManagedDelivery(f.control, f.server.client, f.queue);
      await delivery.enqueue(f.native, "unknown-operator", f.session, {type: "qa", context: "opencode_capture:operator-unknown", answer: "PRIVATE_UNKNOWN_OPERATOR_BODY"});
      await assert.rejects(delivery.flush());
      const review = await f.manager.run({...f.command, action: "show"}); assert.equal(review.unknown_outcomes, 1);
      const checked = await f.manager.run({...f.command, action: "check"});
      assert.equal(checked.pending, shadow ? 1 : 0); assert.equal(checked.matched_unknown, shadow ? 0 : 1);
      assert.equal(f.server.writes.length, 1); assert.equal(f.server.improvements.length, 0);
      if (shadow) {
        await assert.rejects(f.manager.run({...f.command, action: "replay", signature: checked.signature}), /original_write_result_still_unknown/);
        assert.equal(f.server.writes.length, 1);
        await access(f.policy.project_id, {access: "none"});
        const dropped = await f.manager.run({...f.command, action: "discard", signature: checked.signature});
        assert.equal(dropped.discarded, 1); assert.equal(f.queue.status().saved, 0);
        assert.equal(f.server.writes.length, 1); assert.equal(f.server.improvements.length, 0);
        assert.ok(!readFileSync(f.queue.file, "utf8").includes("PRIVATE_UNKNOWN_OPERATOR_BODY"));
      } else assert.equal(f.queue.status().saved, 1);
    } finally {await f.close();}
  }
});

test("pending commands reject stale consent and withdrawn write policy without altering original material", async () => {
  const f = await pendingFixture("consent");
  try {
    f.queue.enqueue("reviewed-old", f.session, {type: "qa", context: "opencode_capture:old"}, f.target);
    const review = await f.manager.run({...f.command, action: "show"});
    f.queue.enqueue("added-later", f.session, {type: "qa", context: "opencode_capture:new"}, f.target);
    const before = readFileSync(f.queue.file);
    await assert.rejects(f.manager.run({...f.command, action: "discard", signature: review.signature}), /pending_materials_changed_review_again/);
    assert.deepEqual(readFileSync(f.queue.file), before);
    const fresh = await f.manager.run({...f.command, action: "show"});
    await access(f.policy.project_id, {access: "read"});
    await assert.rejects(f.manager.run({...f.command, action: "replay", signature: fresh.signature}), /project_write_denied/);
    assert.deepEqual(readFileSync(f.queue.file), before); assert.equal(f.server.requests.length, 0);
    const dropped = await f.manager.run({...f.command, action: "discard", signature: fresh.signature});
    assert.equal(dropped.discarded, 2); assert.equal(f.queue.status().saved, 0);
  } finally {await f.close();}
});


const {ManagedBacklog} = await import(new URL("managed-backlog.js", compiled));
const backlogManager = (f, credential = join(root, "manager-token")) => new ManagedBacklog(f.control, f.server.client, f.queue,
  new ManagedActions(f.control, f.server.client, join(root, f.native + "-backlog-actions"), "a"),
  {origin, instanceId: "a", managementTokenFile: credential});
const backlogSave = (f, snapshot) => ({...f.command, action: "backlog_save", tokens: snapshot.tokens,
  session_id: snapshot.session_id, native_session: snapshot.native, cancel_file: join(root, f.native + ".stop")});

test("backlog requires manager auth, freezes known IDs and leaves unknown and new arrivals untouched", async () => {
  const f = await pendingFixture("backlog-auth"), manager = backlogManager(f);
  try {
    const unknown = hash("backlog unknown"), known = hash("backlog reviewed"), later = hash("backlog new arrival");
    f.queue.enqueue(unknown, f.session, {type: "qa", context: "unknown-marker", answer: "PRIVATE_UNKNOWN"}, f.target);
    await assert.rejects(f.queue.flush(async () => {throw new Error("lost response");}, async () => false));
    f.queue.enqueue(known, f.session, {type: "qa", context: "reviewed-marker", answer: "PRIVATE_KNOWN"}, f.target);
    const before = readFileSync(f.queue.file);
    await assert.rejects(backlogManager(f, tokenFile).runBacklog({...f.command, action: "backlog_review"}, () => {}), /pending_manager_auth_required/);
    assert.deepEqual(readFileSync(f.queue.file), before);
    const snapshot = await manager.runBacklog({...f.command, action: "backlog_review"}, () => {});
    assert.deepEqual(snapshot.tokens, [known]); assert.equal(snapshot.unknown, 1); assert.ok(!JSON.stringify(snapshot).includes("PRIVATE_"));
    f.queue.enqueue(later, f.session, {type: "qa", context: "later-marker"}, f.target);
    const progress = [], saved = await manager.runBacklog(backlogSave(f, snapshot), p => progress.push(p));
    assert.equal(saved.status, "completed"); assert.equal(saved.saved, 1); assert.equal(saved.remaining, 0);
    assert.equal(f.server.writes.length, 1); assert.equal(f.server.writes[0].entry.context, "reviewed-marker");
    assert.equal(f.server.lookups(), 0); assert.equal(f.server.improvements.length, 0);
    assert.deepEqual(f.queue.reviewBacklog(f.target, f.session), {session_id: f.session, tokens: [later], unknown: 1});
    assert.ok(progress.every(p => p.saved + p.handled + p.isolated + p.remaining === 1));
    await assert.rejects(manager.runBacklog({...backlogSave(f, snapshot), native_session: "changed-native"}, () => {}), /invalid_pending_action/);
  } finally {await f.close();}
});

test("backlog waiting for another sender cooperatively stops without releasing or sending records", async () => {
  const f = await pendingFixture("backlog-wait"), manager = backlogManager(f);
  let release;
  try {
    f.queue.enqueue(hash("wait"), f.session, {type: "qa"}, f.target);
    const snapshot = await manager.runBacklog({...f.command, action: "backlog_review"}, () => {}), command = backlogSave(f, snapshot);
    const occupied = f.queue.withSender(async () => await new Promise(resolve => {release = resolve;}));
    const result = await manager.runBacklog(command, p => {if (p.status === "waiting") writeFileSync(command.cancel_file, "stop", {mode: 0o600});});
    assert.equal(result.status, "stopped"); assert.equal(result.remaining, 1); assert.equal(result.saved, 0);
    release(); release = undefined; await occupied;
    assert.equal(f.server.writes.length, 0); assert.equal(f.queue.status().pending, 1);
  } finally {if (release) release(); await f.close();}
});

test("backlog stop during a POST keeps its confirmed receipt and does not send the next frozen ID", async () => {
  let cancel;
  const f = await pendingFixture("backlog-inflight-stop", {afterEntry: async () => writeFileSync(cancel, "stop", {mode: 0o600})});
  try {
    const manager = backlogManager(f);
    for (const id of [hash("stop-first"), hash("stop-second")]) f.queue.enqueue(id, f.session, {type: "qa"}, f.target);
    const snapshot = await manager.runBacklog({...f.command, action: "backlog_review"}, () => {}), command = backlogSave(f, snapshot); cancel = command.cancel_file;
    const result = await manager.runBacklog(command, () => {});
    assert.equal(result.status, "stopped"); assert.equal(result.saved, 1); assert.equal(result.remaining, 1);
    assert.equal(f.server.writes.length, 1); assert.equal(f.queue.status().saved, 1); assert.equal(f.queue.status().pending, 1);
  } finally {await f.close();}
});

test("backlog new lost response is isolated and pauses before another POST, without probing or replay", async () => {
  const f = await pendingFixture("backlog-new-unknown", {loseFirstResponse: true});
  try {
    const manager = backlogManager(f);
    for (const id of [hash("unknown-first"), hash("unknown-second")]) f.queue.enqueue(id, f.session, {type: "qa"}, f.target);
    const snapshot = await manager.runBacklog({...f.command, action: "backlog_review"}, () => {});
    const result = await manager.runBacklog(backlogSave(f, snapshot), () => {});
    assert.equal(result.status, "paused"); assert.equal(result.reason, "request_result_unknown");
    assert.equal(result.saved, 0); assert.equal(result.isolated, 1); assert.equal(result.remaining, 1);
    assert.equal(f.server.writes.length, 1); assert.equal(f.server.lookups(), 0);
    const fresh = await manager.runBacklog({...f.command, action: "backlog_review"}, () => {});
    assert.equal(fresh.unknown, 1); assert.equal(fresh.tokens.length, 1); assert.equal(f.server.writes.length, 1);
  } finally {await f.close();}
});

test("backlog rereads withdrawn write policy before each frozen record", async () => {
  let project;
  const f = await pendingFixture("backlog-revoked", {afterEntry: async () => access(project, {access: "read"})});
  try {
    project = f.policy.project_id; const manager = backlogManager(f);
    for (const id of [hash("policy-first"), hash("policy-second")]) f.queue.enqueue(id, f.session, {type: "qa"}, f.target);
    const snapshot = await manager.runBacklog({...f.command, action: "backlog_review"}, () => {});
    const result = await manager.runBacklog(backlogSave(f, snapshot), () => {});
    assert.equal(result.status, "paused"); assert.equal(result.reason, "write_denied");
    assert.equal(result.saved, 1); assert.equal(result.remaining, 1); assert.equal(f.server.writes.length, 1);
  } finally {await f.close();}
});


test("backlog processing follows automatic-processing policy and preserves original watermark receipts across callers", async () => {
  for (const lost of [false, true]) {
    const f = await pendingFixture("backlog-process-" + lost, {loseImproveResponse: lost});
    try {
      const manager = backlogManager(f);
      f.queue.enqueue(hash("process"), f.session, {type: "qa"}, f.target);
      const snapshot = await manager.runBacklog({...f.command, action: "backlog_review"}, () => {});
      await manager.runBacklog(backlogSave(f, snapshot), () => {});
      const command = {...backlogSave(f, snapshot), action: "backlog_process"}; delete command.tokens;
      await access(f.policy.project_id, {auto_process: false});
      assert.equal((await manager.runBacklog(command, () => {})).status, "disabled"); assert.equal(f.server.improvements.length, 0);
      await access(f.policy.project_id, {auto_process: true});
      assert.equal((await manager.runBacklog(command, () => {})).status, lost ? "unknown" : "submitted");
      assert.equal((await backlogManager(f).runBacklog(command, () => {})).status, lost ? "unknown" : "submitted");
      assert.equal(f.server.improvements.length, 1);
    } finally {await f.close();}
  }
});
