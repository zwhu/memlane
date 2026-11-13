/** Adapted from @kerryhatcher/pi-cognee 0.1.0, src/tools.ts.
 * Upstream commit: 5807e9e5e8231db8b04c0d1d7fb4c5dd3a28dd9b.
 * MIT, Copyright (c) 2026 Kerry Hatcher; see LICENSE.pi-cognee.
 * Memlane: retain four tool names, constrain targets, replace backend.
 */
import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
import { Type } from "typebox";

export type MemoryCall = (ctx: ExtensionContext, operation: string, payload?: Record<string, unknown>, signal?: AbortSignal) => Promise<Record<string, unknown>>;

export function registerTools(pi: ExtensionAPI, call: MemoryCall) {
  const output = (result: Record<string, unknown>) => ({
    content: [{ type: "text" as const, text: JSON.stringify(result) }], details: result,
  });
  pi.registerTool({
    name: "cognee_health", label: "Cognee Health",
    description: "Check this managed connection and current project permissions.",
    parameters: Type.Object({}),
    async execute(_id, _params, signal, _update, ctx) { return output(await call(ctx, "status", {}, signal)); },
  });
  pi.registerTool({
    name: "cognee_remember", label: "Remember (Store Memory)",
    description: "Store text in the current authorized project. Submission and knowledge completion are separate.",
    parameters: Type.Object({ data: Type.String({ maxLength: 32000, description: "Text content to store." }) }),
    async execute(_id, params, signal, _update, ctx) { return output(await call(ctx, "remember", { content: params.data }, signal)); },
  });
  pi.registerTool({
    name: "cognee_recall", label: "Recall Memory",
    description: "Recall authorized current project and common knowledge; project conventions take priority.",
    parameters: Type.Object({ query: Type.String({ maxLength: 16000, description: "Natural-language query." }) }),
    async execute(_id, params, signal, _update, ctx) { return output(await call(ctx, "recall", { query: params.query }, signal)); },
  });
  pi.registerTool({
    name: "cognee_forget", label: "Forget (Delete Memory)",
    description: "Delete one explicit official DataID in the current project. Requires delete permission.",
    parameters: Type.Object({ data_id: Type.String({ description: "Explicit DataID (UUID)." }) }),
    async execute(_id, params, signal, _update, ctx) { return output(await call(ctx, "forget", { data_id: params.data_id }, signal)); },
  });
}
