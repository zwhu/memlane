/** Installation-scoped metadata and existing project_control protocol only. */
import { createHash, randomUUID } from "node:crypto";
import { closeSync, existsSync, fsyncSync, lstatSync, mkdirSync, openSync, readFileSync, realpathSync, renameSync, rmSync, writeFileSync } from "node:fs";
import { isAbsolute, join, resolve } from "node:path";

type Permission = "read" | "write" | "delete";
export type Operation = Permission | "recall" | "capture" | "process";
export type ProjectPolicy = {
  project_id: string; dataset_id: string; dataset_name: string; revision: number; ready: true;
  policy: {permissions: Permission[]; can_capture: boolean; can_auto_process: boolean};
  common: {ready: boolean; readable: boolean; dataset_id?: string};
};
type Binding = {state: "unknown" | "bound"; workspace: string; project_id?: string; dataset_id?: string | null};
export type ControlOptions = {instanceId: string; directory: string; stateDir: string; origin: string; tokenFile: string; timeoutMs?: number};
const uuid = (value: unknown): value is string => typeof value === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(value);
const hash = (value: string) => createHash("sha256").update(value).digest("hex");
const unconfirmed = () => new Error("project_policy_unconfirmed");

function privateFile(path: string): string {
  if (!isAbsolute(path) || realpathSync(path) !== path) throw unconfirmed();
  const stat = lstatSync(path);
  if (!stat.isFile() || stat.isSymbolicLink() || (stat.mode & 0o077)) throw unconfirmed();
  if (stat.size > 64 * 1024) throw unconfirmed();
  return readFileSync(path, "utf8");
}
function store(path: string, value: Binding) {
  const temporary = path + "." + randomUUID();
  const fd = openSync(temporary, "wx", 0o600);
  try { writeFileSync(fd, JSON.stringify(value)); fsyncSync(fd); }
  finally { closeSync(fd); }
  try { renameSync(temporary, path); }
  finally { if (existsSync(temporary)) rmSync(temporary); }
}
function locked<T>(path: string, run: () => T): T {
  const lock = path + ".lock";
  try { mkdirSync(lock, {mode: 0o700}); }
  catch (error) {
    if ((error as NodeJS.ErrnoException).code !== "EEXIST") throw error;
    const recovery = lock + ".recovery";
    mkdirSync(recovery, {mode: 0o700});
    try {
      const pid = Number(privateFile(join(lock, "pid")));
      if (!Number.isSafeInteger(pid) || pid <= 0) throw unconfirmed();
      try { process.kill(pid, 0); throw unconfirmed(); }
      catch (probe) { if ((probe as NodeJS.ErrnoException).code !== "ESRCH") throw probe; }
      rmSync(lock, {recursive: true}); mkdirSync(lock, {mode: 0o700});
    } finally { rmSync(recovery, {recursive: true}); }
  }
  writeFileSync(join(lock, "pid"), String(process.pid), {mode: 0o600});
  try { return run(); } finally { rmSync(lock, {recursive: true}); }
}

export class ManagedProjectControl {
  private readonly origin: string;
  private readonly directory: string;
  private readonly timeout: number;
  private readonly options: Readonly<ControlOptions>;
  private readonly pending = new Map<string, Promise<Binding>>();
  constructor(options: ControlOptions) {
    this.options = Object.freeze({...options});
    const url = new URL(options.origin);
    if (url.protocol !== "http:" || url.hostname !== "127.0.0.1" || !url.port ||
        url.username || url.password || url.search || url.hash || url.pathname !== "/" ||
        !/^[a-zA-Z0-9_-]{1,64}$/.test(options.instanceId)) throw unconfirmed();
    this.origin = url.origin;
    this.directory = realpathSync(options.directory);
    if (!isAbsolute(options.directory) || !lstatSync(this.directory).isDirectory() ||
        !isAbsolute(options.stateDir)) throw unconfirmed();
    mkdirSync(options.stateDir, {recursive: true, mode: 0o700});
    if (realpathSync(options.stateDir) !== resolve(options.stateDir) ||
        (lstatSync(options.stateDir).mode & 0o077)) throw unconfirmed();
    this.timeout = options.timeoutMs ?? 5000;
    if (!Number.isFinite(this.timeout) || this.timeout <= 0 || this.timeout > 10000) throw unconfirmed();
    this.token();
  }
  private token() {
    const value = privateFile(this.options.tokenFile).trim();
    if (value.length < 32 || /[\r\n]/.test(value)) throw unconfirmed();
    return value;
  }
  hostKey(native: string): string {
    if (typeof native !== "string" || !native || native.length > 256) throw unconfirmed();
    return "opencode-" + hash(this.options.instanceId + "\0" + native);
  }
  private path(native: string) { return join(this.options.stateDir, this.hostKey(native) + ".json"); }
  private read(path: string): Binding {
    const value = JSON.parse(privateFile(path));
    if (!value || !["unknown", "bound"].includes(value.state) || !isAbsolute(value.workspace)) throw unconfirmed();
    if (value.state === "bound" && (!uuid(value.project_id) || (value.dataset_id !== null && !uuid(value.dataset_id)))) throw unconfirmed();
    return value;
  }
  private async request(path: string, payload?: object): Promise<any> {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), this.timeout);
    try {
      const response = await fetch(this.origin + path, {
        method: payload === undefined ? "GET" : "POST", redirect: "error",
        headers: {Authorization: "Bearer " + this.token(), "Content-Type": "application/json"},
        body: payload === undefined ? undefined : JSON.stringify(payload), signal: controller.signal,
      });
      if (!response.ok || !response.body) { await response.body?.cancel(); throw unconfirmed(); }
      const reader = response.body.getReader();
      const chunks: Uint8Array[] = [];
      let size = 0;
      try {
        for (;;) {
          const {done, value} = await reader.read();
          if (done) break;
          size += value.byteLength;
          if (size > 64 * 1024) throw unconfirmed();
          chunks.push(value);
        }
      } finally { await reader.cancel().catch(() => {}); }
      const result = JSON.parse(Buffer.concat(chunks).toString("utf8"));
      if (!result || typeof result !== "object" || Array.isArray(result)) throw unconfirmed();
      return result;
    } catch { throw unconfirmed(); }
    finally { clearTimeout(timer); }
  }
  private async bindOnce(native: string): Promise<Binding> {
    const path = this.path(native);
    const first = locked(path, () => {
      if (existsSync(path)) return false;
      store(path, {state: "unknown", workspace: this.directory}); return true;
    });
    let binding = this.read(path);
    if (binding.state === "bound") return binding;
    // Claim before POST. Every uncertain repeat uses the subject-scoped GET,
    // including after process restart; no project discovery is replayed.
    const result = await this.request(first ? "/v1/project/resolve" :
      "/v1/project/binding?host_session_key=" + encodeURIComponent(this.hostKey(native)),
      first ? {workspace: binding.workspace, host_session_key: this.hostKey(native)} : undefined);
    if (!uuid(result.project_id) || (result.dataset_id !== null && !uuid(result.dataset_id))) throw unconfirmed();
    return locked(path, () => {
      binding = this.read(path);
      if (binding.state === "bound" && (binding.project_id !== result.project_id || binding.dataset_id !== result.dataset_id)) throw unconfirmed();
      const confirmed: Binding = {...binding, state: "bound", project_id: result.project_id, dataset_id: result.dataset_id};
      store(path, confirmed); return confirmed;
    });
  }
  async bind(native: string): Promise<Binding> {
    let work = this.pending.get(native);
    if (!work) {
      work = this.bindOnce(native); this.pending.set(native, work);
      void work.finally(() => { if (this.pending.get(native) === work) this.pending.delete(native); }).catch(() => {});
    }
    return work;
  }
  async current(native: string): Promise<ProjectPolicy> {
    const binding = await this.bind(native);
    const result = await this.request("/v1/project/policy?project_id=" + encodeURIComponent(binding.project_id!));
    if (result.ready !== true || result.project_id !== binding.project_id || !uuid(result.dataset_id) ||
        (binding.dataset_id && result.dataset_id !== binding.dataset_id) ||
        typeof result.dataset_name !== "string" || !result.dataset_name ||
        !Number.isSafeInteger(result.revision) || result.revision < 0 ||
        !result.policy || !Array.isArray(result.policy.permissions) ||
        result.policy.permissions.some((p: unknown) => !["read", "write", "delete"].includes(String(p))) ||
        typeof result.policy.can_capture !== "boolean" || typeof result.policy.can_auto_process !== "boolean" ||
        !result.common || typeof result.common.ready !== "boolean" || typeof result.common.readable !== "boolean" ||
        (result.common.ready && result.common.readable && !uuid(result.common.dataset_id))) throw unconfirmed();
    if (binding.dataset_id === null) locked(this.path(native), () => {
      const current = this.read(this.path(native));
      if (current.project_id !== result.project_id || (current.dataset_id && current.dataset_id !== result.dataset_id)) throw unconfirmed();
      store(this.path(native), {...current, dataset_id: result.dataset_id});
    });
    return result as ProjectPolicy;
  }
  confirmTarget(target: {native: string; project_id: string; dataset_id: string}): void {
    if (!target || !uuid(target.project_id) || !uuid(target.dataset_id)) throw unconfirmed();
    target = {...target};
    // Queued material must already have a confirmed original binding. Never
    // discover a project from a new process cwd or an unrecognized queue item.
    const binding = this.read(this.path(target.native));
    if (binding.state !== "bound" || binding.project_id !== target.project_id ||
        binding.dataset_id !== target.dataset_id) throw unconfirmed();
  }
  async currentTarget(target: {native: string; project_id: string; dataset_id: string}): Promise<ProjectPolicy> {
    target = {...target}; this.confirmTarget(target);
    const current = await this.current(target.native);
    if (current.project_id !== target.project_id || current.dataset_id !== target.dataset_id) throw unconfirmed();
    return current;
  }
  async permits(native: string, operation: Operation, automatic = true): Promise<boolean> {
    const policy = await this.current(native);
    const read = policy.policy.permissions.includes("read");
    const write = policy.policy.permissions.includes("write");
    switch (operation) {
      case "read": return read;
      case "recall": return read || (policy.common.ready && policy.common.readable);
      case "write": return write;
      case "delete": return policy.policy.permissions.includes("delete");
      case "capture": return write && policy.policy.can_capture;
      case "process": return write && (!automatic || policy.policy.can_auto_process);
      default: throw unconfirmed();
    }
  }
}

// Shared metadata helpers; callers hold only brief local locks, never network I/O.
export { privateFile as readPrivateMetadata, locked as withMetadataLock };
