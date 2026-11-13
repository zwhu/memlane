/** Managed pi lifecycle adapter; no SDK, MCP server or transcript scanning. */
import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
import { spawn } from "node:child_process";
import { createHash, randomUUID } from "node:crypto";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { registerTools, type MemoryCall } from "./tools.js";

const customType = "memlane-cognee-context";
const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const binding = JSON.parse(readFileSync(resolve(root, "host-binding.json"), "utf8")) as { python: string };
const text = (content: unknown): string => typeof content === "string" ? content :
  Array.isArray(content) ? content.filter(b => b?.type === "text").map(b => b.text).join("\n") : "";
const hash = (value: unknown) => createHash("sha256").update(JSON.stringify(value)).digest("hex");

export default function (pi: ExtensionAPI) {
  let prompt = "", turn = "", promptTime = "";
  const inputs = new Map<string, unknown>();
  const call: MemoryCall = (ctx, operation, payload = {}, signal) => new Promise((done) => {
    if (process.env.MEMLANE_PROCESSING_SESSION === "1") {
      done({ status: "skipped_processing_session" }); return;
    }
    const child = spawn(binding.python, [resolve(root, "scripts/agent_memory_adapter.py")], {
      stdio: ["pipe", "pipe", "ignore"],
    });
    let output = "", finished = false;
    const finish = (value: Record<string, unknown>) => {
      if (finished) return;
      finished = true; clearTimeout(timer); signal?.removeEventListener("abort", abort); done(value);
    };
    const abort = () => { child.kill("SIGTERM"); finish({ status: "result_unconfirmed" }); };
    const timer = setTimeout(abort, 20000);
    signal?.addEventListener("abort", abort, { once: true });
    child.on("error", () => finish({ status: "unavailable" }));
    child.stdin.on("error", () => finish({ status: "result_unconfirmed" }));
    child.stdout.on("data", chunk => {
      output += chunk.toString();
      if (output.length > 128 * 1024) abort();
    });
    child.on("close", code => {
      try { finish(code === 0 ? JSON.parse(output) : { status: "result_unconfirmed" }); }
      catch { finish({ status: "result_unconfirmed" }); }
    });
    child.stdin.end(JSON.stringify({ ...payload, operation, session_id: ctx.sessionManager.getSessionId(), cwd: ctx.cwd }));
    if (signal?.aborted) abort();
  });
  const reset = async (ctx: ExtensionContext) => {
    prompt = ""; turn = ""; inputs.clear(); await call(ctx, "resolve");
  };
  pi.on("session_start", async (_event, ctx) => reset(ctx));
  pi.on("before_agent_start", async (event, ctx) => {
    prompt = event.prompt; turn = randomUUID(); promptTime = new Date().toISOString();
    await call(ctx, "resolve");
  });
  pi.on("context", async (event, ctx) => {
    // Remove persisted blocks from this extension before every provider call,
    // then insert only a fresh, authorized block (including common knowledge).
    const messages = event.messages.filter(m => !(m.role === "custom" && m.customType === customType));
    const result = await call(ctx, "recall", { query: prompt });
    if (typeof result.context === "string" && result.context) messages.push({
      role: "custom", customType, content: result.context, display: false, timestamp: Date.now(),
    });
    return { messages };
  });
  pi.on("message_end", async (event, ctx) => {
    if (event.message.role !== "assistant" || !turn) return;
    const answer = text(event.message.content);
    if (!answer) return;
    const entry = { type: "qa", question: prompt, answer, prompt_time: promptTime, time: new Date().toISOString() };
    await call(ctx, "capture", { event_id: turn + ":answer:" + hash(event.message), entry });
  });
  pi.on("tool_execution_start", async event => { inputs.set(event.toolCallId, event.args); });
  pi.on("tool_execution_end", async (event, ctx) => {
    const args = inputs.get(event.toolCallId); inputs.delete(event.toolCallId);
    // Do not capture this extension's own memory output into itself.
    if (event.toolName.startsWith("cognee_")) return;
    await call(ctx, "capture", { event_id: "tool:" + event.toolCallId, entry: {
      type: "trace", origin_function: event.toolName, method_params: args ?? {},
      method_return_value: event.result, status: event.isError ? "error" : "success", time: new Date().toISOString(),
    } });
  });
  pi.on("agent_end", async (_event, ctx) => { await call(ctx, "flush"); });
  pi.on("session_shutdown", async (_event, ctx) => { await call(ctx, "flush", { final: true }); });
  registerTools(pi, call);
}
