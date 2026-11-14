/** Connect the fixed official outbox to original-target project policy. */
import { Outbox, type Entry, type ProjectTarget } from "./runtime.js";
import { ManagedProjectControl, type ProjectPolicy } from "./managed-control.js";
import { CogneeHttpClient } from "./client.js";

type Item = {id: string; session: string; entry: Entry; uncertain?: boolean; target?: ProjectTarget};

export class ManagedDelivery {
  constructor(private readonly control: ManagedProjectControl,
              private readonly client: CogneeHttpClient,
              readonly outbox: Outbox) {}

  async enqueue(native: string, id: string, session: string, entry: Entry): Promise<boolean> {
    const policy = await this.control.current(native);
    if (!policy.policy.permissions.includes("write") || !policy.policy.can_capture) return false;
    this.outbox.enqueue(id, session, entry,
      {native, project_id: policy.project_id, dataset_id: policy.dataset_id});
    return true;
  }

  async flush(): Promise<void> {
    const checked = new Map<string, ProjectPolicy>();
    await this.outbox.flush(async (item: Item) => {
      const policy = checked.get(item.id);
      if (!policy || !item.target || policy.dataset_id !== item.target.dataset_id ||
          policy.project_id !== item.target.project_id) throw new Error("original_project_target_unconfirmed");
      await this.client.entry(policy.dataset_id, item.session, item.entry);
    }, async (item: Item) => {
      // The official marker lookup reconciles the original operation only.
      if (!checked.has(item.id)) throw new Error("original_project_target_unconfirmed");
      return this.client.hasEntry(item.session, item.entry);
    }, async (item: Item) => {
      if (!item.target) throw new Error("original_project_target_required");
      const policy = await this.control.currentTarget(item.target);
      checked.set(item.id, policy);
      return item.uncertain ? policy.policy.permissions.includes("read") :
        policy.policy.permissions.includes("write") && policy.policy.can_capture;
    });
  }
}
