import { spawn } from "node:child_process";
import { isAbsolute } from "node:path";
import type { PiCaptureBatch, PiCaptureBinding } from "./pi-official-capture.ts";
import type { CaptureReceipt, PiCaptureBroker } from "./pi-official-capture-host.ts";

export type PiCaptureCLIConfig = { root: string; bridge: string; connection_id: string; generation: number; policy_digest: string };
const hash = /^[0-9a-f]{64}$/;
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const opaque = /^[!-~]{1,128}$/;
type HostBinding = PiCaptureBinding & { host_session: string; project: string };
class CaptureBridgeFailure extends Error {
  code: string;
  constructor(code: string) { super("Pi capture request was not authorized"); this.code = code; }
}

export class PiCaptureCLIBroker implements PiCaptureBroker {
  private config: PiCaptureCLIConfig;
  private grants = new Map<string, HostBinding>();
  constructor(config: PiCaptureCLIConfig) {
    if (!config || !isAbsolute(config.root) || !isAbsolute(config.bridge) || !uuid.test(config.connection_id) || !Number.isSafeInteger(config.generation) || config.generation < 1 || !hash.test(config.policy_digest)) throw new Error("Invalid pi capture host configuration");
    this.config = { ...config };
  }

  private daily(): boolean {
    return !process.env.MEMLANE_TOOL_MODE && (!process.env.MEMLANE_USAGE_CHANNEL || process.env.MEMLANE_USAGE_CHANNEL === "daily");
  }

  private call(mode: "authorize" | "submit" | "inspect", workspace: string, project: string | undefined, payload: unknown, signal: AbortSignal): Promise<any> {
    if (!this.daily() || !isAbsolute(workspace) || signal.aborted) return Promise.reject(new Error("Pi capture is isolated"));
    const body = JSON.stringify(payload);
    if (Buffer.byteLength(body, "utf8") > 950 * 1024) return Promise.reject(new Error("Pi capture input exceeds transport budget"));
    const c = this.config;
    const argv = ["pi-capture", mode, "--root", c.root, "--connection", c.connection_id, "--generation", String(c.generation), "--workspace", workspace, ...(project ? ["--project", project] : [])];
    return new Promise((resolve, reject) => {
      const child = spawn(c.bridge, argv, { stdio: ["pipe", "pipe", "ignore"], env: { PATH: "/usr/bin:/bin", MEMLANE_TOOL_MODE: process.env.MEMLANE_TOOL_MODE ?? "", MEMLANE_USAGE_CHANNEL: process.env.MEMLANE_USAGE_CHANNEL ?? "daily" } });
      let stdout = "", bytes = 0, interrupted = false, spawnFailed = false;
      let killTimer: ReturnType<typeof setTimeout> | undefined;
      const abort = () => {
        if (interrupted) return;
        interrupted = true; child.kill("SIGTERM");
        killTimer = setTimeout(() => child.kill("SIGKILL"), 1000); killTimer.unref();
      };
      const timer = setTimeout(abort, 120_000); timer.unref();
      signal.addEventListener("abort", abort, { once: true });
      if (signal.aborted) abort();
      child.stdout.setEncoding("utf8");
      child.stdout.on("data", part => { bytes += Buffer.byteLength(part, "utf8"); if (bytes > 256 * 1024) { abort(); return; } stdout += part; });
      child.on("error", () => { spawnFailed = true; });
      child.on("close", code => {
        clearTimeout(timer); if (killTimer) clearTimeout(killTimer); signal.removeEventListener("abort", abort);
        if (spawnFailed || interrupted) { reject(new Error("Pi capture bridge did not confirm the request")); return; }
        try {
          const response = JSON.parse(stdout);
          if (response?.protocol_version === 1 && response.ok === false && typeof response.error?.code === "string" && /^[a-z_]{1,64}$/.test(response.error.code)) {
            reject(new CaptureBridgeFailure(response.error.code)); return;
          }
          if (code !== 0 || response?.protocol_version !== 1 || response.ok !== true || response.result === null || typeof response.result !== "object") throw new Error();
          resolve(response.result);
        } catch { reject(new Error("Pi capture bridge returned an invalid receipt")); }
      });
      child.stdin.on("error", () => {});
      child.stdin.end(body);
    });
  }

  async authorize(workspace: string, session: string, signal: AbortSignal): Promise<PiCaptureBinding | undefined> {
    this.grants.clear();
    if (!this.daily() || typeof session !== "string" || !opaque.test(session)) return undefined;
    let receipt;
    try { receipt = await this.call("authorize", workspace, undefined, {}, signal); }
    catch (error) {
      if (error instanceof CaptureBridgeFailure && ["scope_denied", "scope_changed", "connection_not_found", "reload_required", "restore_reconnect_required"].includes(error.code)) return undefined;
      throw error;
    }
    const b = receipt.binding;
    if (receipt.enabled !== true || !hash.test(receipt.key) || !uuid.test(receipt.library) || receipt.policy_digest !== this.config.policy_digest || !b || b.connection_id !== this.config.connection_id || b.generation !== this.config.generation || b.material_submission !== true || !uuid.test(b.project)) return undefined;
    const binding: HostBinding = { key: receipt.key, workspace, enabled: true, purpose: "daily", host_session: session, project: b.project };
    // One active host context per broker. Its closure cannot later borrow a
    // different session's/project's authorization from a changed receipt.
    this.grants.set(binding.key, { ...binding });
    return { ...binding };
  }

  private async send(mode: "submit" | "inspect", batch: PiCaptureBatch, binding: PiCaptureBinding, signal: AbortSignal): Promise<CaptureReceipt | undefined> {
    const known = this.grants.get(binding.key), actual = binding as HostBinding;
    if (!known || known.workspace !== binding.workspace || known.host_session !== actual.host_session || known.project !== actual.project || !binding.enabled || binding.purpose !== "daily" || !hash.test(batch.id)) throw new Error("Pi capture authorization changed");
    const receipt = await this.call(mode, binding.workspace, known.project, { batch: batch.id, grant: binding.key, host_session: known.host_session, entries: batch.entries }, signal);
    if (receipt.batch !== batch.id || !Array.isArray(receipt.operations) || receipt.operations.length !== batch.entries.length) return undefined;
    const expected = new Set(batch.entries.map(entry => entry.operation));
    const operations: CaptureReceipt["operations"] = [];
    for (const operation of receipt.operations) {
      if (!operation || !expected.delete(operation.operation) || !["saved", "policy_filtered", "unknown", "not_dispatched"].includes(operation.state)) return undefined;
      operations.push({ operation: operation.operation, state: operation.state });
    }
    return expected.size === 0 ? { batch: receipt.batch, operations } : undefined;
  }

  submit(batch: PiCaptureBatch, binding: PiCaptureBinding, signal: AbortSignal): Promise<CaptureReceipt | undefined> { return this.send("submit", batch, binding, signal); }
  inspect(batch: PiCaptureBatch, binding: PiCaptureBinding, signal: AbortSignal): Promise<CaptureReceipt | undefined> { return this.send("inspect", batch, binding, signal); }
}
