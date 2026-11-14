/** Desktop-only management of the original official outbox. Never an Agent tool. */
import { readPrivateMetadata, ManagedProjectControl, type ProjectPolicy } from "./managed-control.js";
import { Outbox, hash, type ProjectTarget } from "./runtime.js";
import { CogneeHttpClient } from "./client.js";
import { ManagedDelivery } from "./managed-delivery.js";
import { ManagedActions } from "./managed-actions.js";

type Command = {action: "show" | "check" | "replay" | "discard"; project_id: string;
  dataset_id: string; host_session_key: string; signature?: string};
type Options = {origin: string; instanceId: string; managementTokenFile: string};
const uuid = (value: unknown) => typeof value === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(value);
const failure = (code: string): never => { throw new Error(code); };

export class ManagedPending {
  protected readonly options: Readonly<Options>;
  constructor(protected readonly control: ManagedProjectControl, protected readonly client: CogneeHttpClient,
              protected readonly queue: Outbox, protected readonly actions: ManagedActions, options: Options) {
    const url = new URL(options.origin);
    if (url.protocol !== "http:" || url.hostname !== "127.0.0.1" || !url.port || url.username || url.password ||
        url.pathname !== "/" || url.search || url.hash || !/^[a-zA-Z0-9_-]{1,64}$/.test(options.instanceId)) failure("pending_manager_unconfirmed");
    this.options = Object.freeze({...options, origin: url.origin});
  }
  protected async authorize(command: Readonly<Command>): Promise<void> {
    let token: string;
    try { token = readPrivateMetadata(this.options.managementTokenFile).trim(); }
    catch { failure("pending_manager_auth_required"); }
    if (token!.length < 32 || /[\r\n]/.test(token!)) failure("pending_manager_auth_required");
    let directory: any;
    try {
      const response = await fetch(this.options.origin + "/v1/projects", {redirect: "error", signal: AbortSignal.timeout(5000),
        headers: {Authorization: "Bearer " + token!}});
      if (!response.ok) {
        await response.body?.cancel();
        failure([401, 403].includes(response.status) ? "pending_manager_auth_required" : "pending_manager_unavailable");
      }
      if (!response.body) failure("pending_manager_unavailable");
      const reader = response.body!.getReader(), chunks: Uint8Array[] = [];
      let size = 0;
      try {
        for (;;) {
          const part = await reader.read(); if (part.done) break;
          size += part.value.byteLength; if (size > 256 * 1024) failure("pending_manager_unavailable");
          chunks.push(part.value);
        }
      } finally { await reader.cancel().catch(() => {}); }
      directory = JSON.parse(Buffer.concat(chunks).toString("utf8"));
    } catch (error) {
      if (error instanceof Error && /^(pending_manager_auth_required|pending_manager_unavailable)$/.test(error.message)) throw error;
      failure("pending_manager_unavailable");
    }
    if (!directory || !Array.isArray(directory.projects) || !Array.isArray(directory.agents) || !Array.isArray(directory.bindings)) failure("pending_manager_unavailable");
    const project = directory.projects.find((row: any) => row.id === command.project_id);
    if (!project || project.dataset_id !== command.dataset_id ||
        !directory.agents.some((row: any) => row.id === this.options.instanceId) ||
        !directory.bindings.some((row: any) => row.agent_id === this.options.instanceId &&
          row.project_id === command.project_id && row.host_session_key === command.host_session_key)) failure("original_project_target_unconfirmed");
  }
  protected original(command: Readonly<Command>): {target: ProjectTarget; session: string} | undefined {
    const sessions = this.queue.pendingSessions().filter(row => this.control.hostKey(row.target.native) === command.host_session_key);
    if (!sessions.length) return undefined;
    if (sessions.length !== 1) failure("original_pending_session_unconfirmed");
    const original = sessions[0];
    const session = `opencode_${hash(this.options.instanceId).slice(0, 16)}_${hash(original.target.native).slice(0, 24)}`;
    if (original.target.project_id !== command.project_id || original.target.dataset_id !== command.dataset_id ||
        original.session !== session) failure("original_project_target_changed");
    this.control.confirmTarget(original.target);
    return original;
  }
  protected writable(policy: ProjectPolicy): void {
    if (!policy.policy.permissions.includes("write")) failure("project_write_denied");
    if (!policy.policy.can_capture) failure("project_capture_disabled");
  }
  async run(input: Command) {
    const command = Object.freeze({...input});
    if (Object.keys(command).some(key => !["action", "project_id", "dataset_id", "host_session_key", "signature"].includes(key)) ||
        !["show", "check", "replay", "discard"].includes(command.action) || !uuid(command.project_id) || !uuid(command.dataset_id) ||
        typeof command.host_session_key !== "string" || !/^opencode-[0-9a-f]{64}$/.test(command.host_session_key)) failure("invalid_pending_action");
    // Agent control tokens must fail here, before any queue mutation or lease.
    await this.authorize(command);
    const empty = {pending: 0, unknown_outcomes: 0, signature: hash("[]"), session_id: ""};
    if (command.action === "show") {
      const original = this.original(command);
      return original ? this.queue.reviewPending(original.target, original.session) : empty;
    }
    return this.queue.withSender(async token => {
      const original = this.original(command);
      const view = original ? this.queue.reviewPending(original.target, original.session) : empty;
      if (["replay", "discard"].includes(command.action) && command.signature !== view.signature) failure("pending_materials_changed_review_again");
      if (!original) return {...empty, checked: command.action === "check", drained: 0, discarded: 0, matched_unknown: 0};
      const {target, session} = original;
      if (command.action === "discard") {
        const result = this.queue.decidePending(target, session, view.signature, "discard");
        return {...this.queue.reviewPending(target, session), discarded: result.discarded};
      }
      const policy = await this.control.currentTarget(target);
      if (command.action === "replay") this.writable(policy);
      if (view.unknown_outcomes && !policy.policy.permissions.includes("read")) failure("project_read_denied");
      const matches = await this.queue.inspectUnknown(target, session, view.signature,
        (sid, marker, dataset) => this.client.hasEntry(sid, marker, dataset));
      if (command.action === "check") {
        const result = this.queue.decidePending(target, session, view.signature, "acknowledge", matches);
        return {...this.queue.reviewPending(target, session), checked: true, matched_unknown: result.matched_unknown};
      }
      this.writable(await this.control.currentTarget(target));
      const decision = this.queue.decidePending(target, session, view.signature, "replay", matches);
      const delivery = new ManagedDelivery(this.control, this.client, this.queue);
      let error: string | undefined;
      try { await delivery.flush(decision.released_ids, token); }
      catch { error = "pending_delivery_unconfirmed"; }
      const remaining = new Set(this.queue.pendingIds(target, session));
      const drained = decision.matched_unknown + decision.released_ids.filter(id => !remaining.has(id)).length;
      let processing_status = "not_requested";
      if (drained) {
        const completed = this.queue.completedTarget(target.native);
        if (!completed) processing_status = "waiting_for_remaining";
        else {
          try {
            const latest = await this.control.currentTarget(target);
            if (!latest.policy.can_auto_process) processing_status = "disabled";
            else {
              const result = await this.actions.improve(target.native, session, completed.watermark);
              processing_status = result.status === "accepted" ? result.result_status ?? "accepted" : result.status;
            }
          } catch { processing_status = "unconfirmed"; }
        }
      }
      return {...this.queue.reviewPending(target, session), drained, matched_unknown: decision.matched_unknown,
        processing_status, ...(error ? {error} : {})};
    });
  }
}
