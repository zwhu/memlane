/** Connect the fixed official outbox to original-target project policy. */
import { Outbox, type Entry, type ProjectTarget } from "./runtime.js";
import { ManagedProjectControl, type ProjectPolicy } from "./managed-control.js";
import { CogneeHttpClient } from "./client.js";
import { CapturePaused } from "./managed-http.js";
import { SendCancelled } from "./managed-outbox.js";

type Item = {id: string; session: string; entry: Entry; uncertain?: boolean; target?: ProjectTarget};

export class ManagedDelivery {
  constructor(private readonly control: ManagedProjectControl,
              private readonly client: CogneeHttpClient,
              readonly outbox: Outbox) {}

  async enqueue(native: string, id: string, session: string, entry: Entry, promptCreated?: number): Promise<boolean> {
    const policy = await this.control.current(native);
    if (!policy.policy.permissions.includes("write") || !policy.policy.can_capture) return false;
    const updatedAt = Date.parse(policy.capture_policy?.updated_at ?? "");
    if (promptCreated !== undefined && Number.isFinite(updatedAt) && promptCreated < updatedAt) return false;
    this.outbox.enqueue(id, session, entry,
      {native, project_id: policy.project_id, dataset_id: policy.dataset_id});
    return true;
  }

  async flush(ids?: readonly string[], senderToken?: object, automatic = true, stopped?: () => boolean): Promise<void> {
    const checked = new Map<string, ProjectPolicy>();
    await this.outbox.flush(async (item: Item) => {
      const policy = checked.get(item.id);
      if (!policy || !item.target || policy.dataset_id !== item.target.dataset_id ||
          policy.project_id !== item.target.project_id) throw new Error("original_project_target_unconfirmed");
      if (stopped?.()) throw new SendCancelled();
      try {
        await this.client.entry(policy.dataset_id, item.session, item.entry, automatic ? "automatic" : "manual");
      } catch (error) {
        if (error instanceof CapturePaused) {
          try { this.control.noteCapturePause(policy.project_id, error.version, error.scope); }
          catch { /* A cache lock cannot turn a definite pause into an unknown write. */ }
        }
        throw error;
      }
    }, async (item: Item) => {
      // The official marker lookup reconciles the original operation only.
      if (!checked.has(item.id)) throw new Error("original_project_target_unconfirmed");
      return this.client.hasEntry(item.session, item.entry, checked.get(item.id)!.dataset_id);
    }, async (item: Item) => {
      if (!item.target) throw new Error("original_project_target_required");
      if (automatic && !item.uncertain) {
        this.control.confirmTarget(item.target);
        const capture = await this.control.captureStatus(item.target.native);
        if (!capture.enabled && capture.scope !== "ok") return "paused";
      }
      const policy = await this.control.currentTarget(item.target);
      checked.set(item.id, policy);
      if (item.uncertain) return policy.policy.permissions.includes("read");
      if (automatic && !policy.policy.can_capture && policy.capture_policy?.scope !== "ok") return "paused";
      return policy.policy.permissions.includes("write") && (!automatic || policy.policy.can_capture);
    }, ids === undefined ? undefined : new Set(ids), senderToken);
  }
}
