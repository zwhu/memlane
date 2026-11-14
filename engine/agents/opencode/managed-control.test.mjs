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
async function officialServer({loseFirstResponse = false} = {}) {
  const writes = [];
  let lookups = 0;
  const server = await listener(async (req, res) => {
    assert.equal(req.headers["x-api-key"], "synthetic-child-key");
    assert.equal(req.headers.authorization, undefined);
    res.setHeader("Content-Type", "application/json");
    if (req.method === "POST" && req.url === "/api/v1/remember/entry") {
      const chunks = [];
      for await (const chunk of req) chunks.push(chunk);
      writes.push(JSON.parse(Buffer.concat(chunks).toString()));
      if (loseFirstResponse && writes.length === 1) { res.destroy(); return; }
      res.end('{"status":"saved"}'); return;
    }
    if (req.method === "GET" && req.url.startsWith("/api/v1/sessions/")) {
      lookups++; res.end(JSON.stringify({entries: writes.map(item => item.entry)})); return;
    }
    assert.fail("unexpected official route " + req.url);
  });
  return {...server, writes, lookups: () => lookups,
    client: new CogneeHttpClient(server.url, "synthetic-child-key", undefined, undefined, 500)};
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
    assert.equal(s.writes[0].dataset_name, p.dataset_name);
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
