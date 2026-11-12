// Lifecycle adapter only. Installation and the authenticated daemon broker are
// separate: without an explicit broker grant no host messages are traversed.
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { PiOfficialCaptureProjection } from "./pi-official-capture.ts";
import type { PiCaptureBatch, PiCaptureBinding, PiCaptureSession } from "./pi-official-capture.ts";

export type CaptureHostContext = {
  cwd: string;
  sessionManager: PiCaptureSession;
  isIdle(): boolean;
};
export type CaptureReceipt = {
  batch: string;
  // Each operation must be accounted for exactly once. An uncertain write is
  // deliberately absent; an HTTP success by itself is not a cache receipt.
  operations: { operation: string; state: "saved" | "policy_filtered" | "unknown" | "not_dispatched" }[];
};
export interface PiCaptureBroker {
  authorize(workspace: string, session: string, signal: AbortSignal): Promise<PiCaptureBinding | undefined>;
  // submit must enforce fresh authorization and the pinned official policy.
  submit(batch: PiCaptureBatch, binding: PiCaptureBinding, signal: AbortSignal): Promise<CaptureReceipt | undefined>;
  // Read-only reconciliation: must never resend an operation of unknown outcome.
  inspect(batch: PiCaptureBatch, binding: PiCaptureBinding, signal: AbortSignal): Promise<CaptureReceipt | undefined>;
}

function completeReceipt(batch: PiCaptureBatch, receipt: CaptureReceipt | undefined): boolean {
  if (!receipt || receipt.batch !== batch.id || !Array.isArray(receipt.operations) || receipt.operations.length !== batch.entries.length) return false;
  const expected = new Set(batch.entries.map(entry => entry.operation));
  for (const operation of receipt.operations) {
    if (!operation || !expected.delete(operation.operation) || !["saved", "policy_filtered"].includes(operation.state)) return false;
  }
  return expected.size === 0;
}

function safeToRetryReceipt(batch: PiCaptureBatch, receipt: CaptureReceipt | undefined): boolean {
  if (!receipt || receipt.batch !== batch.id || !Array.isArray(receipt.operations) || receipt.operations.length !== batch.entries.length) return false;
  const expected = new Set(batch.entries.map(entry => entry.operation));
  let unsent = false;
  for (const operation of receipt.operations) {
    if (!operation || !expected.delete(operation.operation) || !["saved", "policy_filtered", "not_dispatched"].includes(operation.state)) return false;
    unsent ||= operation.state === "not_dispatched";
  }
  return expected.size === 0 && unsent;
}

export class PiOfficialCaptureHost {
  private projection = new PiOfficialCaptureProjection();
  private grant?: PiCaptureBinding;
  private session = "";
  private epoch = 0;
  private controller = new AbortController();
  private running?: Promise<void>;
  private submitted?: string;
  private issue = "capture_disabled";

  private broker: PiCaptureBroker;
  constructor(broker: PiCaptureBroker) { this.broker = broker; }
  status(): string { return this.issue; }

  close(): void {
    this.epoch++; this.controller.abort(); this.controller = new AbortController();
    this.projection.close(); this.grant = undefined; this.session = "";
    this.submitted = undefined; this.running = undefined; this.issue = "capture_disabled";
  }

  async start(ctx: CaptureHostContext): Promise<void> {
    this.close();
    const epoch = this.epoch, signal = this.controller.signal;
    const session = ctx.sessionManager.getSessionId(), workspace = ctx.cwd;
    try {
      const grant = await this.broker.authorize(workspace, session, signal);
      if (epoch !== this.epoch || signal.aborted) return;
      if (ctx.cwd !== workspace || ctx.sessionManager.getSessionId() !== session || !grant || grant.workspace !== workspace || !this.projection.activate(ctx.sessionManager, grant)) {
        this.issue = "capture_not_authorized"; return;
      }
      this.grant = { ...grant }; this.session = session; this.issue = "capture_ready";
    } catch {
      if (epoch === this.epoch) this.issue = "capture_authorization_unavailable";
    }
  }

  settled(ctx: CaptureHostContext): Promise<void> {
    if (this.running) return this.running;
    const work = this.collect(ctx);
    this.running = work;
    void work.finally(() => { if (this.running === work) this.running = undefined; });
    return work;
  }

  private async collect(ctx: CaptureHostContext): Promise<void> {
    const grant = this.grant;
    if (!grant) return;
    if (ctx.cwd !== grant.workspace || ctx.sessionManager.getSessionId() !== this.session) { this.close(); this.issue = "capture_scope_changed"; return; }
    if (!ctx.isIdle()) { this.issue = "agent_not_settled"; return; }
    const epoch = this.epoch, signal = this.controller.signal;
    try {
      const current = await this.broker.authorize(ctx.cwd, this.session, signal);
      if (epoch !== this.epoch || signal.aborted) return;
      if (!current || current.key !== grant.key || current.workspace !== grant.workspace || !current.enabled || current.purpose !== "daily") {
        this.close(); this.issue = "capture_not_authorized"; return;
      }
      const result = this.projection.collectSettled(ctx.sessionManager, ctx.cwd, ctx.isIdle());
      if (result.issue) { this.issue = result.issue; return; }
      const batch = result.batch;
      if (!batch) { this.issue = "capture_ready"; return; }
      if (batch.entries.length === 0) { this.projection.acknowledge(batch.id); this.issue = "capture_ready"; return; }
      let receipt: CaptureReceipt | undefined;
      const reconciling = this.submitted === batch.id;
      if (reconciling) receipt = await this.broker.inspect(batch, grant, signal);
      else {
        this.submitted = batch.id; // Set before dispatch: a thrown/aborted write may have arrived.
        receipt = await this.broker.submit(batch, grant, signal);
      }
      if (epoch !== this.epoch || signal.aborted) return;
      // Only a current authoritative daemon ledger can prove no dispatch
      // occurred. Unknown cache lookups alone never permit another submit.
      if (reconciling && safeToRetryReceipt(batch, receipt)) {
        this.submitted = undefined; this.issue = "capture_retry_ready"; return;
      }
      if (!completeReceipt(batch, receipt)) { this.issue = "capture_receipt_unknown"; return; }
      if (!this.projection.acknowledge(batch.id)) { this.issue = "capture_receipt_stale"; return; }
      this.submitted = undefined; this.issue = "capture_ready";
    } catch {
      if (epoch === this.epoch) this.issue = this.submitted ? "capture_receipt_unknown" : "capture_authorization_unavailable";
    }
  }
}

export function registerPiOfficialCaptureHost(pi: Pick<ExtensionAPI, "on">, broker: PiCaptureBroker): PiOfficialCaptureHost {
  const host = new PiOfficialCaptureHost(broker);
  pi.on("session_start", async (_event, ctx) => { await host.start(ctx); });
  pi.on("session_shutdown", async () => { host.close(); });
  // Tree navigation is not consent to inspect old branches. Establish a new
  // baseline after navigation, without traversing the selected past messages.
  pi.on("session_tree", async (_event, ctx) => { await host.start(ctx); });
  pi.on("agent_settled", async (_event, ctx) => { await host.settled(ctx); });
  return host;
}
