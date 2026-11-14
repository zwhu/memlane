/** Metadata receipts for official mutations; unknown requests are never replayed. */
import { closeSync, existsSync, fsyncSync, lstatSync, mkdirSync, openSync, realpathSync, renameSync, writeFileSync } from "node:fs";
import { isAbsolute, join, resolve } from "node:path";
import { randomUUID } from "node:crypto";
import { CogneeHttpClient } from "./client.js";
import { hash, type ProjectTarget } from "./runtime.js";
import { ManagedProjectControl, readPrivateMetadata, withMetadataLock, type ProjectPolicy } from "./managed-control.js";

type Kind = "register" | "unregister" | "remember" | "improve";
type Status = "unknown" | "accepted" | "rejected" | "unsupported";
export type ActionReceipt = {version: 1; kind: Kind; native: string; signature: string; generation: number;
  target: ProjectTarget; session: string; status: Status; result_status?: string; http_status?: number};
function write(path: string, value: ActionReceipt) {
  const temporary = path + "." + randomUUID(), fd = openSync(temporary, "wx", 0o600);
  try { writeFileSync(fd, JSON.stringify(value)); fsyncSync(fd); } finally { closeSync(fd); }
  renameSync(temporary, path);
}
export class ManagedActions {
  private readonly root: string;
  private readonly lifePending = new Map<string, Promise<ActionReceipt>>();
  constructor(private readonly control: ManagedProjectControl, private readonly client: CogneeHttpClient,
              root: string, private readonly instance: string, private readonly now = () => Date.now()) {
    if (!isAbsolute(root) || !/^[a-zA-Z0-9_-]{1,64}$/.test(instance)) throw new Error("action_store_unconfirmed");
    mkdirSync(root, {recursive: true, mode: 0o700});
    if (realpathSync(root) !== resolve(root) || lstatSync(root).mode & 0o077) throw new Error("action_store_unconfirmed");
    this.root = root;
  }
  private read(path: string, kind: Kind, native: string): ActionReceipt | undefined {
    if (!existsSync(path)) return undefined;
    const value = JSON.parse(readPrivateMetadata(path));
    if (value.version !== 1 || value.kind !== kind || value.native !== native ||
        !["unknown", "accepted", "rejected", "unsupported"].includes(value.status) ||
        !Number.isSafeInteger(value.generation) || value.generation < 1 || typeof value.signature !== "string" ||
        !value.target || value.target.native !== native || typeof value.session !== "string") throw new Error("action_receipt_unconfirmed");
    return value;
  }
  private async invoke(kind: Kind, native: string, logical: string, signature: string, session: string,
                       allowed: (policy: ProjectPolicy) => boolean,
                       run: (policy: ProjectPolicy) => Promise<{status?: string; unsupported?: boolean}>): Promise<ActionReceipt> {
    this.control.hostKey(native); // Bound input validation before filesystem use.
    const path = join(this.root, hash(JSON.stringify([this.instance, kind, native, logical])) + ".json");
    const before = withMetadataLock(path, () => this.read(path, kind, native));
    if (before && (before.status === "unknown" || before.status === "unsupported" || before.signature === signature)) return before;
    const policy = before ? await this.control.currentTarget(before.target) : await this.control.current(native);
    const candidate: ActionReceipt = {version: 1, kind, native, signature, generation: (before?.generation ?? 0) + 1,
      target: {native, project_id: policy.project_id, dataset_id: policy.dataset_id}, session,
      status: allowed(policy) ? "unknown" : "rejected"};
    const claimed = withMetadataLock(path, () => {
      const current = this.read(path, kind, native);
      if (JSON.stringify(current) !== JSON.stringify(before)) return {owned: false, receipt: current!};
      if (before) {
        const history = path + ".g" + before.generation + ".json";
        if (existsSync(history)) {
          if (JSON.stringify(JSON.parse(readPrivateMetadata(history))) !== JSON.stringify(before)) throw new Error("action_history_changed");
        } else write(history, before);
      }
      write(path, candidate); return {owned: true, receipt: candidate};
    });
    if (!claimed.owned || candidate.status === "rejected") return claimed.receipt;
    // The durable unknown claim precedes the official mutation; no lock across await.
    try {
      const result = await run(policy);
      candidate.status = result.unsupported ? "unsupported" : "accepted";
      if (typeof result.status === "string" && ["completed", "errored", "skipped", "running", "received", "registered", "pending", "success"].includes(result.status)) candidate.result_status = result.status;
    } catch (error) {
      const code = /request failed \((400|401|403|404|422|429)\)/.exec(String(error));
      if (code) { candidate.status = "rejected"; candidate.http_status = Number(code[1]); }
      // Any other error retains unknown, including HTTP success with malformed body.
    }
    withMetadataLock(path, () => {
      const current = this.read(path, kind, native);
      if (current?.generation !== candidate.generation || current.status !== "unknown") throw new Error("action_receipt_changed");
      write(path, candidate);
    });
    return candidate;
  }
  lifecycle(native: string, session: string, remove = false): Promise<ActionReceipt> {
    const previous = this.lifePending.get(native);
    if (previous && !remove) return previous;
    const work = (async () => {
      if (previous) await previous;
      const kind = remove ? "unregister" : "register";
      return this.invoke(kind, native, "lifecycle", remove ? "remove" : String(Math.floor(this.now() / 30000)), session,
        policy => policy.policy.permissions.includes("write") && (remove || policy.policy.can_capture),
        async policy => ({unsupported: !await this.client.lifecycle(session, policy.dataset_name, remove), status: "registered"}));
    })();
    this.lifePending.set(native, work);
    void work.finally(() => { if (this.lifePending.get(native) === work) this.lifePending.delete(native); }).catch(() => {});
    return work;
  }
  remember(native: string, message: string, session: string, fact: string) {
    if (!message || message.length > 256 || !fact.trim()) throw new Error("explicit_operation_required");
    const digest = hash(fact), logical = digest;
    return this.invoke("remember", native, logical, digest, session, policy => policy.policy.permissions.includes("write"), async policy => {
      const result = await this.client.remember({data: fact, datasetId: policy.dataset_id, datasetName: policy.dataset_name, sessionId: session});
      if (result.datasetId !== policy.dataset_id || result.datasetName !== policy.dataset_name || result.status === "errored" || result.status === "error") throw new Error("remember_target_unconfirmed");
      return {status: result.status ?? "received"};
    });
  }
  improve(native: string, session: string, watermark: string) {
    if (!/^[0-9a-f]{64}$/.test(watermark)) throw new Error("saved_watermark_required");
    return this.invoke("improve", native, "process", watermark, session,
      policy => policy.policy.permissions.includes("write") && policy.policy.can_auto_process,
      async policy => {
        const result = await this.client.improve({datasetId: policy.dataset_id, sessionIds: [session]});
        if (!["running", "completed", "errored", "skipped"].includes(result.status ?? "")) throw new Error("process_result_unconfirmed");
        return result;
      });
  }
}
