/** Managed pi lifecycle adapter. */
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { randomUUID } from "node:crypto";
import { existsSync, readFileSync } from "node:fs";
import { registerTools, type MemoryCall } from "./tools.js";

const customType = "memlane-cognee-context";
const processing = () => process.env.MEMLANE_PROCESSING_SESSION === "1";
const text = (content: unknown): string => typeof content === "string" ? content :
  Array.isArray(content) ? content.filter(b => b?.type === "text").map(b => b.text).join("\n") : "";

const call: MemoryCall = async (path, payload, signal) => {
  if (processing()) return { status: "skipped_processing_session" };
  try {
    let binding: { gateway_url?: string; token_file?: string } = {};
    const { MEMLANE_GATEWAY_URL, MEMLANE_AGENT_KEY_FILE } = process.env;
    if (!MEMLANE_GATEWAY_URL || !MEMLANE_AGENT_KEY_FILE) {
      const adjacentBinding = new URL("./host-binding.json", import.meta.url);
      const bindingPath = existsSync(adjacentBinding) ? adjacentBinding : new URL("../host-binding.json", import.meta.url);
      binding = JSON.parse(readFileSync(bindingPath, "utf8"));
    }
    const url = MEMLANE_GATEWAY_URL || binding.gateway_url || "";
    const tokenFile = MEMLANE_AGENT_KEY_FILE || binding.token_file;
    const port = /^http:\/\/(?:127\.0\.0\.1|localhost):([0-9]{1,5})\/?$/.exec(url)?.[1];
    if (!port || +port < 1 || +port > 65535 || !tokenFile) return { status: "unavailable" };
    const token = readFileSync(tokenFile, "utf8").trim();
    if (!token) return { status: "unavailable" };
    const timeout = AbortSignal.timeout(5000);
    const res = await fetch(`http://127.0.0.1:${port}${path}`, {
      method: "POST", redirect: "error", signal: signal ? AbortSignal.any([signal, timeout]) : timeout,
      headers: { "Content-Type": "application/json", "X-Api-Key": token }, body: JSON.stringify(payload),
    });
    if (res.ok) return await res.json();
    await res.body?.cancel();
  } catch {
    return { status: "unavailable" };
  }
  return { status: "unavailable" };
};

export default function (pi: ExtensionAPI) {
  let prompt = "", turn = "", promptTime = "";
  const inputs = new Map<string, unknown>();
  const reset = () => { prompt = ""; turn = ""; promptTime = ""; inputs.clear(); };
  pi.on("session_start", async () => reset());
  pi.on("session_shutdown", async () => reset());
  pi.on("agent_end", async () => reset());
  pi.on("before_agent_start", async event => {
    if (processing()) return;
    prompt = event.prompt.trim(); turn = randomUUID(); promptTime = new Date().toISOString();
  });
  pi.on("context", async (event, ctx) => {
    const messages = event.messages.filter(m => !(m.role === "custom" && m.customType === customType));
    if (!prompt || processing()) return { messages };
    const session = ctx.sessionManager.getSessionId(), activeTurn = turn;
    const { hits } = await call("/recall", {
      workspace: ctx.cwd, session_id: session, query: [...prompt].slice(0, 16000).join(""), channel: "automatic", limit: 10,
    });
    const content = Array.isArray(hits) && hits.map(hit => [hit?.title, hit?.snippet]
      .filter(part => typeof part === "string" && part.trim()).map(part => part.trim()).join(": "))
      .filter(Boolean).map(line => "- " + line).join("\n");
    if (content && turn === activeTurn && session === ctx.sessionManager.getSessionId()) {
      messages.push({ role: "custom", customType, content, display: false, timestamp: Date.now() });
    }
    return { messages };
  });
  pi.on("message_end", async ({ message }, ctx) => {
    if (processing() || message.role !== "assistant" || message.stopReason !== "stop" || !turn || !prompt) return;
    const answer = text(message.content).trim();
    if (!answer) return;
    await call("/ingest", { kind: "capture_qa", workspace: ctx.cwd, session_id: ctx.sessionManager.getSessionId(),
      message_id: turn, content: { question: prompt, answer, prompt_time: promptTime, time: new Date().toISOString() } });
  });
  pi.on("tool_execution_start", async event => {
    if (!processing()) inputs.set(event.toolCallId, event.args);
  });
  pi.on("tool_execution_end", async (event, ctx) => {
    const args = inputs.get(event.toolCallId);
    inputs.delete(event.toolCallId);
    if (processing() || /^(cognee|memlane)_/.test(event.toolName)) return;
    await call("/ingest", { kind: "capture_trace", workspace: ctx.cwd, session_id: ctx.sessionManager.getSessionId(),
      message_id: "tool:" + event.toolCallId, content: { origin_function: event.toolName, method_params: args && typeof args === "object" && !Array.isArray(args) ? args : args === undefined ? {} : { input: args },
        method_return_value: event.result, status: event.isError ? "error" : "success", time: new Date().toISOString() } });
  });
  registerTools(pi, call);
}
