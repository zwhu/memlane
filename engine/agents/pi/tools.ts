/** Adapted from @kerryhatcher/pi-cognee 0.1.0, src/tools.ts.
 * Upstream commit: 5807e9e5e8231db8b04c0d1d7fb4c5dd3a28dd9b.
 * MIT, Copyright (c) 2026 Kerry Hatcher; see LICENSE.pi-cognee.
 * Memlane: retain three tool names (delete is desktop-only), constrain targets, replace backend.
 */
import { createHash } from "node:crypto";
import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
import { Type } from "typebox";

export type MemoryCall = (path: string, payload: Record<string, unknown>, signal?: AbortSignal) =>
  Promise<Record<string, unknown>>;

function deterministicRememberKey(cwd: string, data: string, explicit?: string): string {
  const trimmed = typeof explicit === "string" ? explicit.trim() : "";
  if (trimmed) return trimmed;
  return "pi:" + createHash("sha256").update(JSON.stringify([cwd, data])).digest("hex");
}

export function registerTools(pi: ExtensionAPI, call: MemoryCall) {
  const tool = async (ctx: ExtensionContext, name: string, args: Record<string, unknown>, signal?: AbortSignal) => {
    const reply = await call("/mcp", { jsonrpc: "2.0", id: 1, method: "tools/call",
      params: { name, arguments: { ...args, project_path: ctx.cwd } } }, signal);
    const result = reply.result ?? reply.error ?? reply;
    return { content: [{ type: "text" as const, text: JSON.stringify(result) }], details: result };
  };
  pi.registerTool({
    name: "cognee_health", label: "Cognee Health",
    description: "Check this managed connection and current project permissions.",
    parameters: Type.Object({}),
    async execute(_id, _params, signal, _update, ctx) { return tool(ctx, "health", {}, signal); },
  });
  pi.registerTool({
    name: "cognee_remember", label: "Remember (Store Memory)",
    description: "Store text in the current authorized project. Submission and knowledge completion are separate.",
    parameters: Type.Object({
      data: Type.String({ maxLength: 32000, description: "Text content to store." }),
      idempotency_key: Type.Optional(Type.String({ maxLength: 256, description: "Optional idempotency key for safe retries." })),
    }),
    async execute(_id, params, signal, _update, ctx) {
      const idempotency_key = deterministicRememberKey(ctx.cwd, params.data, params.idempotency_key);
      return tool(ctx, "remember", { content: params.data, idempotency_key }, signal);
    },
  });
  pi.registerTool({
    name: "cognee_recall", label: "Recall Memory",
    description: "Recall authorized current project and common knowledge; project conventions take priority.",
    parameters: Type.Object({ query: Type.String({ maxLength: 16000, description: "Natural-language query." }) }),
    async execute(_id, params, signal, _update, ctx) { return tool(ctx, "recall", { query: params.query }, signal); },
  });
}
