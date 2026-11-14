import test from "node:test";
import assert from "node:assert/strict";
import http from "node:http";
import { pathToFileURL } from "node:url";

const { CogneeHttpClient } = await import(pathToFileURL(process.env.MEMLANE_OPENCODE_TEST_CLIENT).href);
async function server(handler) {
  const requests = [];
  const instance = http.createServer((req, res) => { requests.push({method: req.method, path: req.url, headers: req.headers}); handler(req, res); });
  await new Promise(resolve => instance.listen(0, "127.0.0.1", resolve));
  return {requests, url: `http://127.0.0.1:${instance.address().port}`,
          close: async () => { instance.closeAllConnections(); await new Promise(resolve => instance.close(resolve)); }};
}
const respond = (_req, res) => { res.setHeader("Content-Type", "application/json"); res.end('{"ok":true}'); };

test("missing child credential and explicit login never request owner authentication", async () => {
  const s = await server(respond);
  try {
    const client = new CogneeHttpClient(s.url, "", undefined, undefined, 500);
    await assert.rejects(client.fetchAPI("/data", {method: "GET"}));
    await assert.rejects(client.login());
    await assert.rejects(client.health());
    const cloud = new CogneeHttpClient(s.url, "synthetic-child-key", undefined, undefined, 500, 500, "cloud");
    await assert.rejects(cloud.fetchAPI("/data", {method: "GET"}));
    assert.equal(s.requests.length, 0);
  } finally { await s.close(); }
});

test("installation key is the sole credential even for health and overridden init headers", async () => {
  const s = await server(respond);
  try {
    const client = new CogneeHttpClient(s.url, "synthetic-child-key", undefined, undefined, 500);
    await client.fetchAPI("/data", {method: "GET", headers: {"X-Api-Key": "wrong", "Authorization": "Bearer owner-must-not-leave", "X-Test": "preserve"}});
    await client.health();
    assert.equal(s.requests.length, 2);
    for (const req of s.requests) {
      assert.equal(req.headers["x-api-key"], "synthetic-child-key");
      assert.equal(req.headers.authorization, undefined);
    }
    assert.equal(s.requests[0].headers["x-test"], "preserve");
  } finally { await s.close(); }
});

test("401 and 403 do not trigger login or request retries", async () => {
  for (const status of [401, 403]) {
    const s = await server((_req, res) => { res.statusCode = status; res.end("denied"); });
    try {
      const client = new CogneeHttpClient(s.url, "synthetic-child-key", undefined, undefined, 500);
      await assert.rejects(client.fetchAPI("/write", {method: "POST", body: "synthetic"}), new RegExp(`failed \\(${status}\\)`));
      assert.equal(s.requests.length, 1);
      assert.equal(s.requests[0].path, "/write");
    } finally { await s.close(); }
  }
});

test("307 and 308 never follow a redirect with credentials or a write", async () => {
  const sink = await server(respond);
  try {
    for (const status of [307, 308]) for (const method of ["GET", "POST"]) {
      const source = await server((_req, res) => { res.writeHead(status, {Location: sink.url + "/sink"}); res.end(); });
      try {
        const client = new CogneeHttpClient(source.url, "synthetic-child-key", undefined, undefined, 500);
        await assert.rejects(client.fetchAPI("/redirect", {method, redirect: "follow", ...(method === "POST" ? {body: "synthetic"} : {})}));
        assert.equal(source.requests.length, 1);
        assert.equal(sink.requests.length, 0);
      } finally { await source.close(); }
    }
  } finally { await sink.close(); }
});

test("HTTP errors report status without echoing the response body", async () => {
  const s = await server((_req, res) => { res.statusCode = 403; res.end("server_echo_secret"); });
  try {
    const client = new CogneeHttpClient(s.url, "synthetic-child-key", undefined, undefined, 500);
    await assert.rejects(client.fetchAPI("/write", {method: "POST"}), error => {
      assert.match(error.message, /failed \(403\)/);
      assert.ok(!error.message.includes("server_echo_secret"));
      return true;
    });
  } finally { await s.close(); }
});

test("transport timeouts do not automatically retry reads or writes", async () => {
  for (const method of ["GET", "POST"]) {
    const s = await server(() => {});
    try {
      const client = new CogneeHttpClient(s.url, "synthetic-child-key", undefined, undefined, 200);
      await assert.rejects(client.fetchAPI("/timeout", {method}));
      assert.equal(s.requests.length, 1);
    } finally { await s.close(); }
  }
});

test("unknown trace confirmation uses the original target and trace marker field only", async () => {
  const dataset = "11111111-1111-4111-8111-111111111111", session = "original-session";
  const entry = {type: "trace", method_params: {cognee_capture_id: "original-trace-marker"}};
  let detail;
  const s = await server((_req, res) => { res.setHeader("Content-Type", "application/json"); res.end(JSON.stringify(detail)); });
  try {
    const client = new CogneeHttpClient(s.url, "synthetic-child-key", undefined, undefined, 500);
    const valid = {session_id: session, dataset_id: dataset, traces: [{method_params: {cognee_capture_id: "original-trace-marker"}}]};
    for (const value of [
      {...valid, dataset_id: undefined}, {...valid, session_id: "another-session"},
      {...valid, traces: [{method_return_value: "original-trace-marker", method_params: {cognee_capture_id: "another-marker"}}]},
      {...valid, traces: [], qas: [{context: "original-trace-marker"}]},
    ]) {
      detail = value; assert.equal(await client.hasEntry(session, entry, dataset), false);
    }
    detail = valid; assert.equal(await client.hasEntry(session, entry, dataset), true);
    const before = s.requests.length;
    assert.equal(await client.hasEntry(session, {type: "trace"}, dataset), false);
    assert.equal(await client.hasEntry(session, entry, "invalid-dataset"), false);
    assert.equal(s.requests.length, before);
    assert.ok(s.requests.every(request => request.method === "GET"));
  } finally { await s.close(); }
});
