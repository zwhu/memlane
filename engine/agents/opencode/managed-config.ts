/** Installation configuration only; never inherit ambient Cognee credentials. */
import { lstatSync, mkdirSync, readFileSync, realpathSync } from "node:fs";
import { isAbsolute, resolve } from "node:path";
import type { CogneePluginConfig } from "./types.js";

type InstalledConfig = CogneePluginConfig & {apiKeyFile?: string};
const bounded = (value: number | undefined, fallback: number, max: number) =>
  typeof value === "number" && Number.isFinite(value) && value > 0 ? Math.min(value, max) : fallback;
export function resolveManagedConfig(options: InstalledConfig = {}) {
  let url: URL;
  try { url = new URL(options.baseUrl ?? ""); } catch { throw new Error("managed_local_service_required"); }
  if (options.mode && options.mode !== "local") throw new Error("managed_local_service_required");
  const normalizedPath = url.pathname.replace(/\/+$/, "");
  if (url.protocol !== "http:" || url.hostname !== "127.0.0.1" || !url.port || url.username || url.password ||
      (normalizedPath !== "" && normalizedPath !== "/cognee") || url.search || url.hash) throw new Error("managed_local_service_required");
  const keyFile = options.apiKeyFile;
  if (!keyFile || !isAbsolute(keyFile) || realpathSync(keyFile) !== keyFile) throw new Error("managed_credential_required");
  const stat = lstatSync(keyFile);
  if (!stat.isFile() || stat.isSymbolicLink() || stat.mode & 0o077 || stat.size > 65536) throw new Error("managed_credential_required");
  const apiKey = readFileSync(keyFile, "utf8").trim();
  if (apiKey.length < 32 || /[\r\n]/.test(apiKey)) throw new Error("managed_credential_required");
  const stateDir = options.stateDir;
  if (!stateDir || !isAbsolute(stateDir)) throw new Error("managed_state_directory_required");
  mkdirSync(stateDir, {recursive: true, mode: 0o700});
  if (realpathSync(stateDir) !== resolve(stateDir) || (lstatSync(stateDir).mode & 0o077)) throw new Error("managed_state_directory_required");
  if (options.captureTools && (!Array.isArray(options.captureTools) || options.captureTools.some(v => typeof v !== "string"))) throw new Error("managed_capture_filter_invalid");
  return {
    mode: "local" as const, baseUrl: url.origin + normalizedPath, apiKey, username: undefined, password: undefined,
    datasetName: "managed_target_only", readScopes: {}, stateDir,
    autoCapture: options.autoCapture !== false && options.enableSessions !== false,
    autoRecall: options.autoRecall !== false, captureTools: [...(options.captureTools ?? [])],
    maxCaptureChars: bounded(options.maxCaptureChars, 8000, 100000),
    // Include policy and retrieval latency in a bounded per-turn budget.
    recallTimeoutMs: bounded(options.recallTimeoutMs, 10000, 10000),
    requestTimeoutMs: bounded(options.requestTimeoutMs, 5000, 30000),
    ingestionTimeoutMs: bounded(options.ingestionTimeoutMs, 15000, 60000),
    maxResults: bounded(options.maxResults, 8, 100), searchType: options.searchType ?? "CHUNKS",
    minScore: options.minScore ?? 0.1, improveOnSessionEnd: options.improveOnSessionEnd !== false,
  };
}
