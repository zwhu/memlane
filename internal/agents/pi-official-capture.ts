// pi 0.84.1 host-message projection for Cognee's typed QA/trace API.
// This module does not install hooks, authorize collection, send HTTP, persist
// transcripts or run improve. The broker must apply the pinned official capture
// policy and verify current consent/workspace before accepting these entries.
// Text/arguments stay whole inside a 1 MiB volatile range so upstream filtering
// sees the original input. Storage truncation belongs AFTER that policy stage.
import { createHash } from "node:crypto";

type HostEntry = {
  id: string;
  parentId: string | null;
  type: string;
  message?: Record<string, any>;
};
export interface PiCaptureSession {
  getSessionId(): string;
  getLeafId(): string | null;
  getEntry(id: string): HostEntry | undefined;
}
export type PiCaptureBinding = {
  // Public identity fingerprint, never a credential. Only a broker may issue it.
  key: string;
  workspace: string;
  enabled: boolean;
  purpose: "daily" | "processing";
};
export type PiCapturedEntry = {
  operation: string;
  session: string;
  entry: Record<string, unknown>;
};
export type PiCaptureBatch = {
  id: string;
  entries: PiCapturedEntry[];
  diagnostics: string[];
};
type Question = { ids: string[]; text: string; truncated: boolean; hadAssistant: boolean };
type Call = { entry: string; name: string; args: unknown };
type Projection = { question?: Question; calls: Map<string, Call> };
type Pending = { batch: PiCaptureBatch; cursor: string | null; projection: Projection };

const fingerprint = (value: unknown) => createHash("sha256").update(JSON.stringify(value)).digest("hex");
const opaqueID = (value: unknown): value is string => typeof value === "string" && value.length > 0 && value.length <= 128 && !/[\s\x00-\x1f]/.test(value);
function withinBudget(value: unknown): boolean {
  try { return Buffer.byteLength(JSON.stringify(value), "utf8") <= 1024 * 1024; } catch { return false; }
}
function textContent(content: unknown): string {
  if (typeof content === "string") return content;
  if (!Array.isArray(content)) return "";
  return content.filter(x => x?.type === "text" && typeof x.text === "string").map(x => x.text).join("\n");
}

// Cursor is set at activation, before reading any message. Traversal follows
// only new parents up to that cursor; it never scans getEntries/getBranch or a
// transcript file. A changed branch or missing boundary fails closed.
export class PiOfficialCaptureProjection {
  private binding?: PiCaptureBinding;
  private hostSession = "";
  private cursor: string | null = null;
  private projection: Projection = { calls: new Map() };
  private pending?: Pending;

  activate(manager: PiCaptureSession, binding: PiCaptureBinding): boolean {
    this.close();
    if (!binding.enabled || binding.purpose !== "daily" || !opaqueID(binding.key) || typeof binding.workspace !== "string" || !binding.workspace.startsWith("/") || binding.workspace.length > 4096 || /[\x00-\x1f]/.test(binding.workspace)) return false;
    const session = manager.getSessionId(), leaf = manager.getLeafId();
    if (!opaqueID(session) || leaf !== null && !opaqueID(leaf)) return false;
    this.binding = { ...binding }; this.hostSession = session; this.cursor = leaf;
    return true;
  }

  collectSettled(manager: PiCaptureSession, workspace: string, idle: boolean): { batch?: PiCaptureBatch; issue?: string } {
    const binding = this.binding;
    if (!binding) return { issue: "capture_disabled" };
    if (manager.getSessionId() !== this.hostSession || workspace !== binding.workspace) {
      this.close();
      return { issue: "capture_scope_changed" };
    }
    if (!idle) return { issue: "agent_not_settled" };
    // No new batch can replace an unresolved write. The sender must inspect
    // receipts; collecting again is not permission to resend these entries.
    if (this.pending) return { batch: structuredClone(this.pending.batch) };
    const leaf = manager.getLeafId();
    if (leaf === this.cursor) return {};
    const entries: HostEntry[] = [], seen = new Set<string>();
    let next = leaf;
    while (next !== this.cursor) {
      if (!opaqueID(next) || seen.has(next)) return { issue: "capture_branch_changed" };
      if (entries.length >= 256) return { issue: "capture_range_exceeded" };
      const entry = manager.getEntry(next);
      if (!entry || entry.id !== next || entry.parentId !== null && !opaqueID(entry.parentId)) return { issue: "capture_branch_changed" };
      seen.add(next); entries.push(entry); next = entry.parentId;
    }
    const projection: Projection = { question: this.projection.question ? { ...this.projection.question, ids: [...this.projection.question.ids] } : undefined, calls: new Map(this.projection.calls) };
    const output: PiCapturedEntry[] = [], diagnostics: string[] = [];
    const seenCalls = new Set(projection.calls.keys());
    const note = (code: string) => { if (!diagnostics.includes(code)) diagnostics.push(code); };
    const session = "pi-" + fingerprint([binding.key, this.hostSession]).slice(0, 48);
    const source = (fields: Record<string, unknown>) => JSON.stringify({ origin: "memlane_pi_capture_v1", host_session: this.hostSession, ...fields });
    let answer: { id: string; text: string; truncated: boolean } | undefined;
    const flush = () => {
      if (projection.question && answer) {
        const q = projection.question;
        output.push({ operation: fingerprint([binding.key, this.hostSession, "qa", q.ids, answer.id]), session,
          entry: { type: "qa", question: q.text, answer: answer.text, context: source({ question_entries: q.ids, answer_entry: answer.id, question_truncated: q.truncated, answer_truncated: answer.truncated }) } });
        projection.question = undefined;
      }
      answer = undefined;
    };
    for (const entry of entries.reverse()) {
      if (entry.type !== "message" || !entry.message) continue;
      const message = entry.message;
      if (message.role === "user") {
        flush();
        // Keep full text until the pinned official policy has inspected and
        // redacted it. The broker applies UTF-8 storage limits afterwards.
        const text = { value: textContent(message.content), truncated: false };
        if (!text.value.trim()) { note("empty_user_omitted"); continue; }
        if (projection.question?.hadAssistant) { note("unanswered_user_omitted"); projection.question = undefined; }
        if (projection.question) {
          if (projection.question.ids.length >= 16) return { issue: "capture_range_exceeded" };
          const joined = { value: projection.question.text + "\n\n" + text.value, truncated: false };
          projection.question = { ...projection.question, ids: [...projection.question.ids, entry.id], text: joined.value, truncated: projection.question.truncated || text.truncated || joined.truncated };
        } else projection.question = { ids: [entry.id], text: text.value, truncated: text.truncated, hadAssistant: false };
      } else if (message.role === "assistant") {
        if (projection.question) projection.question.hadAssistant = true;
        for (const block of Array.isArray(message.content) ? message.content : []) {
          if (block?.type !== "toolCall" || !opaqueID(block.id) || typeof block.name !== "string") continue;
          if (seenCalls.has(block.id)) { note("ambiguous_tool_call"); projection.calls.delete(block.id); continue; }
          seenCalls.add(block.id);
          if (!withinBudget(block.arguments) || !block.arguments || typeof block.arguments !== "object" || Array.isArray(block.arguments)) return { issue: "capture_range_exceeded" };
          projection.calls.set(block.id, { entry: entry.id, name: block.name, args: structuredClone(block.arguments) });
        }
        if (projection.calls.size > 256) return { issue: "capture_range_exceeded" };
        if (message.stopReason === "stop") {
          const text = { value: textContent(message.content), truncated: false };
          if (text.value.trim()) answer = { id: entry.id, text: text.value, truncated: text.truncated };
        } else if (message.stopReason === "error" || message.stopReason === "aborted" || message.stopReason === "length") {
          answer = undefined; note("incomplete_answer_omitted");
        }
      } else if (message.role === "toolResult") {
        const call = typeof message.toolCallId === "string" ? projection.calls.get(message.toolCallId) : undefined;
        if (!call || call.name !== message.toolName || typeof message.isError !== "boolean") { note("unverified_tool_omitted"); continue; }
        projection.calls.delete(message.toolCallId);
        const params = { value: call.args, truncated: false }, result = { value: textContent(message.content), truncated: false };
        output.push({ operation: fingerprint([binding.key, this.hostSession, "trace", call.entry, entry.id]), session,
          entry: { type: "trace", origin_function: call.name, status: message.isError ? "error" : "success", method_params: params.value, method_return_value: result.value,
            error_message: message.isError ? result.value : "", generate_feedback_with_llm: false,
            memory_context: source({ call_entry: call.entry, result_entry: entry.id, tool_call: message.toolCallId, params_truncated: params.truncated, result_truncated: result.truncated }) } });
      }
      if (!withinBudget({ output, question: projection.question, calls: [...projection.calls.values()] })) return { issue: "capture_range_exceeded" };
    }
    flush();
    if (!withinBudget({ output, question: projection.question, calls: [...projection.calls.values()] })) return { issue: "capture_range_exceeded" };
    const batch: PiCaptureBatch = { id: fingerprint([binding.key, this.hostSession, this.cursor, leaf]), entries: output, diagnostics };
    this.pending = { batch, cursor: leaf, projection };
    return { batch: structuredClone(batch) };
  }

  // Call only after each entry has a known official-cache receipt or an
  // explicit policy-filtered receipt. Unknown results must not advance it.
  acknowledge(batchID: string): boolean {
    if (!this.binding || !this.pending || this.pending.batch.id !== batchID) return false;
    this.cursor = this.pending.cursor; this.projection = this.pending.projection; this.pending = undefined;
    return true;
  }

  close(): void {
    this.binding = undefined; this.hostSession = ""; this.cursor = null;
    this.projection = { calls: new Map() }; this.pending = undefined;
  }
}
