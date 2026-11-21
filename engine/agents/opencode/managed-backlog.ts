/** Manager-only continuous saving of a frozen set of original outbox IDs. */
import {existsSync} from "node:fs";
import {isAbsolute} from "node:path";
import {ManagedPending} from "./managed-pending.js";
import {ManagedDelivery} from "./managed-delivery.js";
import {hash} from "./runtime.js";

type BacklogCommand = {action: "backlog_review" | "backlog_save" | "backlog_process"; project_id: string;
  dataset_id: string; host_session_key: string; session_id?: string; tokens?: string[]; cancel_file?: string; native_session?: string};
type Progress = {status: "running" | "waiting" | "completed" | "paused" | "stopped";
  reason: string; saved: number; handled: number; isolated: number; remaining: number};
const uuid = (s: unknown) => typeof s === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(s);

export class ManagedBacklog extends ManagedPending {
  async runBacklog(input: BacklogCommand, emit: (value: Progress) => void) {
    const command = {...input, tokens: input.tokens ? [...input.tokens] : undefined};
    if (Object.keys(input).some(k => !["action", "project_id", "dataset_id", "host_session_key", "session_id", "tokens", "cancel_file", "native_session"].includes(k)) ||
        !["backlog_review", "backlog_save", "backlog_process"].includes(command.action) || !uuid(command.project_id) || !uuid(command.dataset_id) ||
        typeof command.host_session_key !== "string" || !/^opencode-[0-9a-f]{64}$/.test(command.host_session_key)) throw new Error("invalid_pending_action");
    const identity = {action: "show" as const, project_id: command.project_id,
      dataset_id: command.dataset_id, host_session_key: command.host_session_key};
    await this.authorize(identity);
    if (command.action === "backlog_review") {
      if (input.tokens !== undefined || input.session_id !== undefined || input.cancel_file !== undefined || input.native_session !== undefined) throw new Error("invalid_pending_action");
      const original = this.original(identity);
      return original ? {...this.queue.reviewBacklog(original.target, original.session), native: original.target.native} : {session_id: "", native: "", tokens: [], unknown: 0};
    }
    if (typeof command.session_id !== "string" || !/^opencode_[a-f0-9]{16}_[a-f0-9]{24}$/.test(command.session_id) ||
        typeof command.cancel_file !== "string" || !isAbsolute(command.cancel_file) ||
        typeof command.native_session !== "string" || !command.native_session || command.native_session.length > 256 ||
        this.control.hostKey(command.native_session) !== command.host_session_key ||
        command.session_id !== `opencode_${hash(this.options.instanceId).slice(0,16)}_${hash(command.native_session).slice(0,24)}`) throw new Error("invalid_pending_action");
    const target = {native: command.native_session, project_id: command.project_id, dataset_id: command.dataset_id};
    this.control.confirmTarget(target);
    if (command.action === "backlog_process") {
      if (input.tokens !== undefined) throw new Error("invalid_pending_action");
      if (existsSync(command.cancel_file)) return {status: "deferred"};
      const policy = await this.control.currentTarget(target);
      if (!policy.policy.can_auto_process) return {status: "disabled"};
      const completed = this.queue.completedTarget(target.native);
      if (!completed) return {status: "deferred"};
      const result = await this.actions.improve(target.native, command.session_id, completed.watermark);
      return {status: result.status === "accepted" ? "submitted" : result.status === "unknown" ? "unknown" : "failed"};
    }
    if (!Array.isArray(command.tokens) || command.tokens.length > 200000 ||
        command.tokens.some(t => typeof t !== "string" || !/^[a-f0-9]{64}$/.test(t)) ||
        new Set(command.tokens).size !== command.tokens.length) throw new Error("invalid_pending_action");
    const tokens = command.tokens;
    const progress: Progress = {status: "running", reason: "", saved: 0, handled: 0, isolated: 0, remaining: tokens.length};
    let signalled = false;
    const signal = () => {signalled = true;};
    const stopped = () => signalled || existsSync(command.cancel_file!);
    const send = (status: Progress["status"], reason = "") => {
      progress.status = status; progress.reason = reason; progress.remaining = tokens.length;
      emit({...progress}); return {...progress};
    };
    process.on("SIGTERM", signal); process.on("SIGINT", signal);
    const heartbeat = setInterval(() => emit({...progress, remaining: tokens.length}), 5000);
    try {
      for (;;) {
        if (stopped()) return send("stopped", "user_stopped");
        try {
          return await this.queue.withSender(async lease => {
            while (tokens.length) {
              if (stopped()) return send("stopped", "user_stopped");
              this.original(identity); // Reject changed queued targets even after consent.
              try {this.writable(await this.control.currentTarget(target));}
              catch (error) {
                const denied = error instanceof Error && ["project_write_denied", "project_capture_disabled"].includes(error.message);
                return send("paused", denied ? "write_denied" : "policy_unconfirmed");
              }
              const id = tokens[0], decision = this.queue.releaseBacklogEntry(target, command.session_id!, id);
              if (decision === "handled") {progress.handled++; tokens.shift(); send("running"); continue;}
              if (decision === "isolated") {progress.isolated++; tokens.shift(); send("running"); continue;}
              if (decision === "deferred") {return send("paused", "rate_limited");}
              let flushError: unknown = undefined;
              try {await new ManagedDelivery(this.control, this.client, this.queue).flush([id], lease);}
              catch (error) {flushError = error;}
              const pending = this.queue.pendingIds(target, command.session_id!);
              if (!pending.includes(id)) {progress.saved++; tokens.shift(); send("running"); continue;}
              const entryState = this.queue.entryState(target, command.session_id!, id);
              const errText = flushError instanceof Error ? flushError.message : String(flushError ?? "");
              const isAuthDenied = entryState === "auth_denied" ||
                /(?:request|login) failed \((?:401|403)\)/.test(errText) ||
                ["local_policy_denied", "project_write_denied", "project_capture_disabled"].includes(errText);
              if (isAuthDenied) {
                return send("paused", "write_denied");
              }
              if (entryState === "deferred") {
                return send("paused", "rate_limited");
              }
              if (entryState === "rejected" || entryState === "uncertain") {
                progress.isolated++; tokens.shift(); send("running"); continue;
              }
              const current = this.queue.reviewBacklog(target, command.session_id!);
              if (!current.tokens.includes(id)) {
                progress.isolated++; tokens.shift(); send("running"); continue;
              }
              if (flushError !== undefined) {
                progress.isolated++; tokens.shift(); send("running"); continue;
              }
              return send("paused", "worker_state_unconfirmed");
            }
            return send("completed");
          });
        } catch (error) {
          if (!(error instanceof Error) || error.message !== "Outbox busy") throw error;
          send("waiting", "sender_busy");
          await new Promise(resolve => setTimeout(resolve, 350));
        }
      }
    } finally {
      clearInterval(heartbeat); process.off("SIGTERM", signal); process.off("SIGINT", signal);
    }
  }
}
