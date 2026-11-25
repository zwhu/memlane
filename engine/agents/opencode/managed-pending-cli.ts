/** Verified local worker entry. Management credentials arrive only from desktop input. */
import {createHash} from "node:crypto";
import {lstatSync, readFileSync, readdirSync, realpathSync} from "node:fs";
import {isAbsolute, join, relative} from "node:path";
import {pathToFileURL} from "node:url";
const sha = (value: Uint8Array) => createHash("sha256").update(value).digest("hex");
const fail = (): never => {throw new Error("pending_worker_unconfirmed");};
function file(path: string, limit = 65536, privateOnly = false): Buffer {
  if (typeof path !== "string" || !isAbsolute(path) || realpathSync(path) !== path) fail();
  const stat = lstatSync(path);
  if (!stat.isFile() || stat.isSymbolicLink() || stat.size > limit || (privateOnly && (stat.mode & 0o077))) fail();
  return readFileSync(path);
}
function keys(value: any, allowed: string[]) {
  if (!value || typeof value !== "object" || Array.isArray(value) || Object.keys(value).some(key => !allowed.includes(key))) fail();
}
try {
  const chunks: Buffer[] = []; let size = 0;
  for await (const chunk of process.stdin) {size += chunk.length; if (size > 16 * 1024 * 1024) fail(); chunks.push(chunk);}
  const input = JSON.parse(Buffer.concat(chunks).toString("utf8"));
  keys(input, ["installation", "agent_id", "operator", "command"]);
  keys(input.installation, ["plugin_dir", "manifest_sha256", "binding_file", "binding_sha256", "node_executable", "node_sha256"]);
  keys(input.operator, ["token_file", "control_url", "service_url", "bound_service_url"]);
  const expected = input.installation;
  if (!/^[a-f0-9]{64}$/.test(expected.manifest_sha256) || !/^[a-f0-9]{64}$/.test(expected.binding_sha256) ||
      !/^[a-f0-9]{64}$/.test(expected.node_sha256)) fail();
  if (realpathSync(expected.plugin_dir) !== expected.plugin_dir ||
      realpathSync(process.argv[1]) !== join(expected.plugin_dir, "dist/src/managed-pending-cli.js") ||
      realpathSync(process.execPath) !== expected.node_executable ||
      sha(file(expected.node_executable, 512 * 1024 * 1024)) !== expected.node_sha256) fail();
  const manifestPath = join(expected.plugin_dir, ".memlane-opencode-artifact.json");
  const raw = file(manifestPath, 8 * 1024 * 1024);
  if (sha(raw) !== expected.manifest_sha256) fail();
  const manifest = JSON.parse(raw.toString("utf8")), found: Record<string, string> = {};
  function visit(root: string) {
    for (const name of readdirSync(root)) {
      const path = join(root, name), stat = lstatSync(path);
      if (stat.isSymbolicLink()) fail();
      if (stat.isDirectory()) visit(path);
      else if (path !== manifestPath) found[relative(expected.plugin_dir, path).split("\\").join("/")] = sha(file(path, 64 * 1024 * 1024));
    }
  }
  visit(expected.plugin_dir);
  if (manifest.protocol !== 1 || !manifest.files || Object.keys(found).length !== Object.keys(manifest.files).length ||
      Object.entries(found).some(([name, hash]) => manifest.files[name] !== hash) || !found["dist/src/managed-pending.js"]) fail();
  const bindingRaw = file(expected.binding_file, 65536, true);
  if (sha(bindingRaw) !== expected.binding_sha256) fail();
  const binding = JSON.parse(bindingRaw.toString("utf8"));
  if (binding.version !== 1 || binding.plugin_root !== expected.plugin_dir || binding.instance_id !== input.agent_id ||
      typeof input.operator.control_url !== "string" || typeof input.operator.service_url !== "string" ||
      binding.memlane?.origin !== input.operator.control_url.replace(/\/$/, "") ||
      input.operator.service_url !== binding.memlane.origin + "/cognee" ||
      binding.cognee?.baseUrl !== (input.operator.bound_service_url ?? input.operator.service_url)) fail();
  if (!Array.isArray(binding.secrets) || binding.secrets.length !== 2) fail();
  for (const secret of binding.secrets) if (sha(file(secret.path, 65536, true)) !== secret.sha256) fail();
  // No unverified component code is imported before the complete tree check.
  const module = (name: string) => import(pathToFileURL(join(expected.plugin_dir, "dist/src", name + ".js")).href);
  const [{ManagedPending}, {ManagedProjectControl}, {resolveManagedConfig}, {CogneeHttpClient}, {Outbox}, {ManagedActions}] =
    await Promise.all([module("managed-pending"), module("managed-control"), module("managed-config"), module("client"), module("runtime"), module("managed-actions")]);
  const cfg = resolveManagedConfig(binding.cognee);
  // Queued targets must already have a private original binding; this directory
  // is never used to discover a replacement project for a queued session.
  const control = new ManagedProjectControl({...binding.memlane, directory: binding.profile});
  const client = new CogneeHttpClient(input.operator.service_url, cfg.apiKey, undefined, undefined,
    cfg.requestTimeoutMs, cfg.ingestionTimeoutMs, cfg.mode);
  const queue = new Outbox([cfg.baseUrl, binding.memlane.instanceId].join("|"), cfg.stateDir);
  const actions = new ManagedActions(control, client, join(cfg.stateDir, "actions"), binding.memlane.instanceId);
  const worker = new ManagedPending(control, client, queue, actions,
    {origin: binding.memlane.origin, instanceId: binding.instance_id, managementTokenFile: input.operator.token_file});
  if (["backlog_review", "backlog_save", "backlog_process"].includes(input.command?.action)) {
    if (!found["dist/src/managed-backlog.js"]) fail();
    const {ManagedBacklog} = await module("managed-backlog");
    const backlog = new ManagedBacklog(control, client, queue, actions,
      {origin: binding.memlane.origin, instanceId: binding.instance_id, managementTokenFile: input.operator.token_file});
    const result = await backlog.runBacklog(input.command, (value: unknown) => process.stdout.write(JSON.stringify(value) + "\n"));
    if (input.command.action !== "backlog_save") process.stdout.write(JSON.stringify(result) + "\n");
  } else {
    const result = await worker.run(input.command);
    process.stdout.write(JSON.stringify(result) + "\n"); if (result.error) process.exitCode = 1;
  }
} catch (error) {
  let code = error instanceof Error ? error.message : "";
  if (code === "Outbox busy") code = "buffer_send_in_progress";
  if (!/^(pending_[a-z_]+|original_[a-z_]+|project_[a-z_]+|invalid_pending_action|sender_lease_required|buffer_send_in_progress)$/.test(code)) code = "pending_worker_unconfirmed";
  process.stdout.write(JSON.stringify({error: code}) + "\n"); process.exitCode = 1;
}
