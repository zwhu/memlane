"""Strict overlays for the pinned official OpenCode plugin, managed installs only."""


def managed_http_client(text):
    def method(start, end, replacement):
        nonlocal text
        if text.count(start) != 1 or text.count(end) != 1:
            raise ValueError('fixed OpenCode HTTP overlay anchor mismatch')
        first, last = text.index(start), text.index(end)
        if last <= first:
            raise ValueError('fixed OpenCode HTTP overlay method order mismatch')
        text = text[:first] + replacement + text[last:]

    def replace(old, new, count=1):
        nonlocal text
        if text.count(old) != count:
            raise ValueError('fixed OpenCode HTTP overlay anchor mismatch')
        text = text.replace(old, new)

    if 'const user = this.username || "default_user@example.com";' not in text:
        raise ValueError('fixed OpenCode HTTP overlay input mismatch')
    method('  async login(): Promise<void> {\n', '  async ensureAuth(): Promise<void> {\n', '''  async login(): Promise<void> {
    throw new Error("Managed OpenCode does not use owner login");
  }

''')
    method('  async ensureAuth(): Promise<void> {\n', '  private buildHeaders(): Record<string, string> {\n', '''  async ensureAuth(): Promise<void> {
    if (this.mode !== "local" || !this.apiKey) {
      throw new Error("Managed OpenCode requires its installation API key");
    }
  }

''')
    method('  private buildHeaders(): Record<string, string> {\n', '  async fetchAPI<T>(\n', '''  private buildHeaders(): Record<string, string> {
    return { "X-Api-Key": this.apiKey! };
  }
  private managedHeaders(init: RequestInit): Headers {
    const headers = new Headers(init.headers);
    headers.delete("Authorization");
    headers.set("X-Api-Key", this.buildHeaders()["X-Api-Key"]);
    return headers;
  }

''')
    replace('retries = init.method === "GET" ? MAX_RETRIES : 0,', 'retries = 0,')
    replace('headers: { ...this.buildHeaders(), ...(init.headers as Record<string, string>) },',
            'headers: this.managedHeaders(init),\n          redirect: "error",', 2)
    replace('throw new Error(`Cognee request failed (${retryResponse.status}): ${errorText}`);',
            'throw new Error(`Cognee request failed (${retryResponse.status})`);')
    replace('throw new Error(`Cognee request failed (${response.status}): ${errorText}`);',
            'throw new Error(`Cognee request failed (${response.status})`);')
    replace('datasetId: response.dataset_id ?? params.datasetId ?? "",',
            'datasetId: response.dataset_id ?? "",')
    replace('body: JSON.stringify({ dataset_name: dataset, session_id: session, entry }),',
            'body: JSON.stringify({ dataset_id: dataset, session_id: session, entry }),')
    replace('dataset_names: [dataset]', 'dataset_ids: [dataset]')
    replace('    formData.append("datasetName", params.datasetName);',
            '    formData.append("datasetName", params.datasetName);\n    formData.append("run_in_background", "true");')
    replace('    searchPrompt?: string;\n', '    searchPrompt?: string;\n    onlyContext?: boolean;\n    nodeNames?: string[];\n')
    replace('        ...(params.searchPrompt ? { system_prompt: params.searchPrompt } : {}),',
            '        ...(params.searchPrompt ? { system_prompt: params.searchPrompt } : {}),\n'
            '        ...(params.onlyContext !== undefined ? { only_context: params.onlyContext } : {}),\n'
            '        ...(params.nodeNames ? { node_name: params.nodeNames } : {}),')
    method('  async health(): Promise<{ status: string }> {\n', '  async remember(params: {\n', '''  async health(): Promise<{ status: string }> {
    return this.fetchAPI<{ status: string }>("/health", { method: "GET" });
  }

''')
    return text


def managed_outbox(text):
    def replace(old, new):
        nonlocal text
        if text.count(old) != 1:
            raise ValueError('fixed OpenCode outbox overlay anchor mismatch')
        text = text.replace(old, new, 1)

    replace('type Pending = { id: string; session: string; entry: Entry; uncertain?: boolean };', '''export type ProjectTarget = { native: string; project_id: string; dataset_id: string };
export class LocalPolicyDenied extends Error {
  constructor() { super("local_policy_denied"); this.name = "LocalPolicyDenied"; }
}
function snapshotTarget(target: ProjectTarget): ProjectTarget {
  const uuid = (value: unknown) => typeof value === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(value);
  if (!target || typeof target.native !== "string" || !target.native || target.native.length > 256 ||
      /[\\x00-\\x1f]/.test(target.native) || !uuid(target.project_id) || !uuid(target.dataset_id)) {
    throw new Error("original_project_target_required");
  }
  return {native: target.native, project_id: target.project_id, dataset_id: target.dataset_id};
}
type Pending = { id: string; session: string; entry: Entry; uncertain?: boolean; blocked?: boolean; blocked_reason?: "local_policy_denied" | "official_auth_denied"; target?: ProjectTarget };''')
    replace('  enqueue(id: string, session: string, entry: Entry): void {',
            '  enqueue(id: string, session: string, entry: Entry, target?: ProjectTarget): void {\n    const original = target === undefined ? undefined : snapshotTarget(target);')
    replace('state.pending.push({ id, session, entry: scrub(entry) as Entry });',
            'state.pending.push({ id, session, entry: scrub(entry) as Entry, ...(original ? {target: original} : {}) });')
    replace('if (/(?:request|login) failed \\((?:400|401|403|404|422|429)\\)/.test(String(error))) {',
            'if (error instanceof LocalPolicyDenied || /(?:request|login) failed \\((?:400|401|403|404|422|429)\\)/.test(String(error))) {')
    replace('''  status(): { pending: number; uncertain: number; saved: number } {
    const state = this.read();
    return { pending: state.pending.length, uncertain: state.pending.filter((item) => item.uncertain).length, saved: state.seen.length };
  }
''', '''  status(): { pending: number; uncertain: number; saved: number; blocked: number } {
    const state = this.read();
    return { pending: state.pending.length, uncertain: state.pending.filter((item) => item.uncertain).length, saved: state.seen.length, blocked: state.pending.filter((item) => item.blocked).length };
  }
  blockedIds(): string[] {
    return this.read().pending.filter((item) => item.blocked && !item.uncertain).map((item) => item.id);
  }
  // The managed caller must recheck current write policy and journal an
  // explicit operator action before releasing exact selected native IDs.
  retryBlocked(ids: string[]): number {
    if (this.running) throw new Error("Outbox flush is running");
    if (!Array.isArray(ids) || ids.length === 0 || ids.length > 100 || ids.some((id) => typeof id !== "string" || !id)) {
      throw new Error("Exact blocked entry IDs required");
    }
    const selected = new Set(ids);
    return this.lock(() => {
      const state = this.read();
      for (const id of selected) {
        if (!state.pending.some((item) => item.id === id && item.blocked && !item.uncertain)) {
          throw new Error("Selected entry is not a definite blocked outcome");
        }
      }
      for (const item of state.pending) {
        if (selected.has(item.id)) { item.blocked = false; delete item.blocked_reason; }
      }
      this.write(state);
      return selected.size;
    });
  }
''')
    replace('const next = state.pending[0]; if (!next) return undefined;',
            'const next = state.pending.find((entry) => !entry.blocked); if (!next) return undefined;')
    replace('if (pending) pending.uncertain = false;', '''if (pending) {
                pending.uncertain = false;
                if (error instanceof LocalPolicyDenied) {
                  pending.blocked = true; pending.blocked_reason = "local_policy_denied";
                } else if (/(?:request|login) failed \\((?:401|403)\\)/.test(String(error))) {
                  pending.blocked = true; pending.blocked_reason = "official_auth_denied";
                }
              }''')
    replace('flush(send: (item: Pending) => Promise<void>, committed: (item: Pending) => Promise<boolean>): Promise<void> {',
            'flush(send: (item: Pending) => Promise<void>, committed: (item: Pending) => Promise<boolean>, authorize?: (item: Pending) => Promise<boolean>): Promise<void> {')
    replace('''        const item = this.lock(() => { const state = this.read(); const next = state.pending.find((entry) => !entry.blocked); if (!next) return undefined; const copy = { ...next }; next.uncertain = true; this.write(state); return copy; });
        if (!item) return;
''', '''        const candidate = this.lock(() => {
          const next = this.read().pending.find((entry) => !entry.blocked);
          return next ? JSON.parse(JSON.stringify(next)) as Pending : undefined;
        });
        if (!candidate) return;
        // Policy lookup is read-only and occurs before marking an unsent write
        // uncertain. A control outage cannot manufacture an ambiguous write.
        if (authorize && !await authorize(candidate)) {
          if (candidate.uncertain) return; // Original outcome still needs reconciliation.
          this.lock(() => {
            const state = this.read();
            const next = state.pending.find((entry) => entry.id === candidate.id);
            if (next && !next.uncertain && JSON.stringify(next) === JSON.stringify(candidate)) {
              next.blocked = true; next.blocked_reason = "local_policy_denied"; this.write(state);
            }
          });
          continue;
        }
        const item = this.lock(() => {
          const state = this.read();
          const next = state.pending.find((entry) => entry.id === candidate.id && !entry.blocked);
          if (!next || JSON.stringify(next) !== JSON.stringify(candidate)) return undefined;
          const copy = JSON.parse(JSON.stringify(next)) as Pending;
          next.uncertain = true; this.write(state); return copy;
        });
        if (!item) continue;
''')
    replace('type State = { pending: Pending[]; seen: string[] };',
            'type State = { pending: Pending[]; seen: string[]; savedTargets?: Record<string, ProjectTarget> };')
    replace('  blockedIds(): string[] {', '''  completedTarget(native: string): {target: ProjectTarget; watermark: string} | undefined {
    const state = this.read();
    if (state.pending.some(item => !item.target || item.target.native === native)) return undefined;
    const rows = Object.entries(state.savedTargets ?? {}).filter(([id, target]) => state.seen.includes(id) && target.native === native).sort(([a], [b]) => a.localeCompare(b));
    if (!rows.length) return undefined;
    const target = snapshotTarget(rows[0][1]);
    if (rows.some(([, other]) => other.project_id !== target.project_id || other.dataset_id !== target.dataset_id)) throw new Error("original_project_target_changed");
    return {target, watermark: hash(JSON.stringify(rows.map(([id]) => id)))};
  }
  blockedIds(): string[] {''')
    replace('if (!state.seen.includes(item.id)) state.seen.push(item.id); this.write(state);',
            'if (!state.seen.includes(item.id)) state.seen.push(item.id); if (item.target) { state.savedTargets ??= {}; state.savedTargets[item.id] = snapshotTarget(item.target); } this.write(state);')
    return text


def managed_capture_callbacks(text):
    """Capture overlay only; config/recall/tool overlays gate production activation."""
    def replace(old, new):
        nonlocal text
        if text.count(old) != 1:
            raise ValueError('fixed OpenCode capture overlay anchor mismatch')
        text = text.replace(old, new, 1)

    replace('import type { CogneePluginConfig } from "./types.js";', '''import type { CogneePluginConfig } from "./types.js";
import { ManagedProjectControl, type ControlOptions } from "./managed-control.js";
import { ManagedDelivery } from "./managed-delivery.js";
import { CaptureWindow } from "./managed-capture-window.js";''')
    replace('  const active = new Set<string>();', '''  const binding = options?.memlane as ControlOptions;
  if (!binding) throw new Error("managed_installation_required");
  const control = new ManagedProjectControl({...binding, directory: ctx.directory});
  const delivery = new ManagedDelivery(control, client, outbox);
  const window = new CaptureWindow();
  const active = new Set<string>();''')
    replace('''  const pump = () => cfg.autoCapture ? outbox.flush(
    (item) => client.entry(cfg.datasetName, item.session, item.entry),
    (item) => client.hasEntry(item.session, item.entry),
  ) : Promise.resolve();''', '''  const pump = () => cfg.autoCapture ? delivery.flush() : Promise.resolve();''')
    first = text.index('  async function captureConversation(native: string): Promise<void> {')
    last = text.index('  return {\n    "chat.message":', first)
    original = text[first:last]
    if 'ctx.client.session.messages(' not in original or original.count('outbox.enqueue(') != 1:
        raise ValueError('fixed OpenCode capture method mismatch')
    text = text[:first] + '''  async function captureConversation(native: string): Promise<void> {
    if (!cfg.autoCapture) { window.disable(native); return; }
    if (window.candidates(native).length === 0) return;
    if (!await control.permits(native, "capture")) { window.disable(native); return; }
    // Read only IDs observed after activation, never list a session's history.
    for (const [messageID, parentID] of window.candidates(native)) {
      if (!await control.permits(native, "capture")) { window.disable(native); return; }
      const response = await ctx.client.session.message({path: {id: native, messageID}, query: {directory: ctx.directory}});
      if (response.error || !response.data) throw new Error("Cannot read observed completed message");
      const message = response.data;
      if (message.info.id !== messageID || message.info.sessionID !== native || message.info.role !== "assistant" ||
          message.info.parentID !== parentID || !message.info.time.completed || message.info.error) { window.finish(native, messageID); continue; }
      if (!await control.permits(native, "capture")) { window.disable(native); return; }
      const questionResponse = await ctx.client.session.message({path: {id: native, messageID: parentID}, query: {directory: ctx.directory}});
      if (questionResponse.error || !questionResponse.data) throw new Error("Cannot read observed parent message");
      const parent = questionResponse.data;
      if (parent.info.id !== parentID || parent.info.sessionID !== native || parent.info.role !== "user") { window.finish(native, messageID); continue; }
      const content = (parts: typeof message.parts) => parts.filter((part) => part.type === "text" && !part.synthetic).map((part) => part.type === "text" ? part.text : "").join("\\n");
      const question = safeText(content(parent.parts)); const answer = safeText(content(message.parts));
      if (question && answer) {
        const id = hash(sid(native) + ":qa:" + message.info.id);
        await delivery.enqueue(native, id, sid(native), {type: "qa", question, answer, context: `opencode_capture:${id}`});
      }
      window.finish(native, messageID);
    }
  }
''' + text[last:]
    replace('''      prompts.set(input.sessionID, {id: output.message.id, text});''', '''      let capture = false;
      try { capture = cfg.autoCapture && await control.permits(input.sessionID, "capture"); } catch { /* Unconfirmed turns are never backfilled. */ }
      window.prompt(input.sessionID, output.message.id, output.message.time?.created, capture);
      prompts.set(input.sessionID, {id: output.message.id, text});''')
    replace('''      if (!cfg.autoCapture || input.tool.startsWith("cognee_") || !allowedTool(input.tool, input.args, cfg.captureTools)) return;''', '''      if (!cfg.autoCapture || input.tool.startsWith("cognee_") || !allowedTool(input.tool, input.args, cfg.captureTools)) return;
      const prompt = prompts.get(input.sessionID);
      if (!prompt || !window.allowed(input.sessionID, prompt.id) || !await control.permits(input.sessionID, "capture")) { window.disable(input.sessionID); return; }''')
    replace('try { outbox.enqueue(id, sid(input.sessionID), entry); background(pump()); }',
            'try { await delivery.enqueue(input.sessionID, id, sid(input.sessionID), entry); background(pump()); }')
    replace('''    event: async ({event}) => {
''', '''    event: async ({event}) => {
      if (event.type === "message.updated") {
        const info = event.properties.info;
        if (info.role === "assistant" && info.time.completed && !info.error) window.completed(info.sessionID, info.id, info.parentID);
      }
''')
    replace('''        active.delete(native); prompts.delete(native); recalls.delete(native);''',
            '''        window.clear(native);
        active.delete(native); prompts.delete(native); recalls.delete(native);''')
    return text


def managed_config_recall(text):
    """Apply after capture overlay; remaining lifecycle/tool overlays still required."""
    def replace(old, new):
        nonlocal text
        if text.count(old) != 1:
            raise ValueError('fixed OpenCode config/recall overlay anchor mismatch')
        text = text.replace(old, new, 1)

    replace('import { CaptureWindow } from "./managed-capture-window.js";', '''import { CaptureWindow } from "./managed-capture-window.js";
import { resolveManagedConfig } from "./managed-config.js";
import { ManagedRecall } from "./managed-recall.js";''')
    first = text.index('const bool = ')
    last = text.index('export const CogneeOpenCodePlugin: Plugin', first)
    if 'process.env.COGNEE_API_KEY' not in text[first:last]:
        raise ValueError('fixed OpenCode config overlay input mismatch')
    text = text[:first] + 'export const resolveConfig = resolveManagedConfig;\n\n' + text[last:]
    replace('  const cfg = resolveConfig((options?.cognee ?? options ?? {}) as CogneePluginConfig);', '''  if (process.env.MEMLANE_PROCESSING_SESSION === "1") return {};
  const cfg = resolveConfig(options?.cognee as CogneePluginConfig);''')
    replace('  const window = new CaptureWindow();', '''  const window = new CaptureWindow();
  const recallClient = new ManagedRecall(control, client, cfg);
  const memoryMarker = `<memlane_opencode_memory instance="${hash(binding.instanceId)}">`;
  const removeOwnedMemory = (values: string[]) => { for (let i = values.length - 1; i >= 0; i--) if (values[i].startsWith(memoryMarker)) values.splice(i, 1); };
  const wrapMemory = (value: string) => memoryMarker + value + "</memlane_opencode_memory>";''')
    first = text.index('  async function recall(native: string, query: string, explicit = false): Promise<string> {')
    last = text.index('  async function captureConversation', first)
    if 'client.listDatasets()' not in text[first:last]:
        raise ValueError('fixed OpenCode recall overlay input mismatch')
    text = text[:first] + '''  async function recall(native: string, query: string, explicit = false): Promise<string> {
    if ((!cfg.autoRecall && !explicit) || !query.trim()) return "";
    await recallClient.query(native, query, sid(native));
    return recallClient.context(native);
  }
''' + text[last:]
    replace('''      if (!input.sessionID) return;
      const memory = recalls.get(input.sessionID);
      if (memory?.context && memory.id === prompts.get(input.sessionID)?.id && !output.system.includes(memory.context)) output.system.push(memory.context);''', '''      removeOwnedMemory(output.system);
      if (!input.sessionID) return;
      const memory = recalls.get(input.sessionID);
      if (memory?.id === prompts.get(input.sessionID)?.id) {
        const context = await recallClient.context(input.sessionID);
        if (context) output.system.push(wrapMemory(context));
      }''')
    replace('''      const prompt = prompts.get(input.sessionID);
      const context = prompt ? await recall(input.sessionID, prompt.text) : "";
      if (context && !output.context.includes(context)) output.context.push(context);''', '''      removeOwnedMemory(output.context);
      const prompt = prompts.get(input.sessionID);
      const context = prompt ? await recall(input.sessionID, prompt.text) : "";
      if (context) output.context.push(wrapMemory(context));''')
    replace('''        window.clear(native);''', '''        recallClient.clear(native);
        window.clear(native);''')
    return text


def managed_action_callbacks(text):
    def replace(old, new):
        nonlocal text
        if text.count(old) != 1:
            raise ValueError('fixed OpenCode action overlay anchor mismatch')
        text = text.replace(old, new, 1)

    replace('import { ManagedRecall } from "./managed-recall.js";', '''import { ManagedRecall } from "./managed-recall.js";
import { ManagedActions } from "./managed-actions.js";
import { join } from "node:path";''')
    replace('  const outbox = new Outbox([cfg.baseUrl, hash(cfg.apiKey || cfg.username || "default"), cfg.datasetName, ctx.directory].join("|"), cfg.stateDir);\n  const binding = options?.memlane as ControlOptions;\n  if (!binding) throw new Error("managed_installation_required");', '''  const binding = options?.memlane as ControlOptions;
  if (!binding) throw new Error("managed_installation_required");
  const outbox = new Outbox([cfg.baseUrl, binding.instanceId].join("|"), cfg.stateDir);''')
    replace('  const window = new CaptureWindow();', '''  const activatedAt = Date.now();
  const window = new CaptureWindow(activatedAt);
  let disposed = false;
  let discovering = false;
  // Some native versions publish the first session event during bootstrap.
  // Reconcile only post-activation metadata in this exact host directory.
  const discoverSessions = async () => {
    if (disposed || discovering || !cfg.autoCapture || !ctx.client.session?.list) return;
    discovering = true;
    try {
      const result = await ctx.client.session.list({query: {directory: ctx.directory}, signal: AbortSignal.timeout(2500)});
      if (disposed || result.error || !Array.isArray(result.data)) return;
      for (const info of result.data) {
        if (typeof info.id === "string" && info.id && info.id.length <= 256 && info.directory === ctx.directory &&
            Number.isFinite(info.time?.created) && info.time.created >= activatedAt && !active.has(info.id)) background(heartbeat(info.id));
      }
    } finally { discovering = false; }
  };
  const actions = new ManagedActions(control, client, join(cfg.stateDir, "actions"), binding.instanceId);''')
    replace('  const sid = (id: string) => sessionId(ctx.directory, id);',
            '  const sid = (id: string) => `opencode_${hash(binding.instanceId).slice(0, 16)}_${hash(id).slice(0, 24)}`;')
    replace('''    try { await client.lifecycle(sid(native), cfg.datasetName); } catch { log("lifecycle registration failed; check credentials and backend status"); }''',
            '''    if (!cfg.autoCapture) return;
    try { await actions.lifecycle(native, sid(native)); } catch { log("lifecycle status unconfirmed"); }''')
    replace('''          if (cfg.improveOnSessionEnd !== false && cfg.autoCapture && outbox.status().pending === 0) await client.improve({datasetName: cfg.datasetName, sessionIds: [sid(native)]});''',
            '''          const completed = outbox.completedTarget(native);
          if (cfg.improveOnSessionEnd && cfg.autoCapture && completed) {
            await control.currentTarget(completed.target);
            await actions.improve(native, sid(native), completed.watermark);
          }''')
    replace('background(client.lifecycle(sid(native), cfg.datasetName, true));',
            'background(actions.lifecycle(native, sid(native), true));')
    replace('''      await Promise.allSettled([...active].map((native) => client.lifecycle(sid(native), cfg.datasetName, true)));''',
            '''      await Promise.allSettled([...active].map((native) => actions.lifecycle(native, sid(native), true)));''')
    replace('''args: {fact: tool.schema.string()}, async execute(args) {
        await client.remember({data: args.fact, datasetName: cfg.datasetName}); return "Saved to Cognee memory.";''', '''args: {fact: tool.schema.string()}, async execute(args, context) {
        const receipt = await actions.remember(context.sessionID, context.messageID, sid(context.sessionID), safeText(args.fact));
        if (receipt.status === "unknown") return "Original remember result is unknown. Do not resubmit; inspect the original receipt.";
        if (receipt.status !== "accepted") return "Remember was rejected; no automatic retry. Inspect the original receipt.";
        return "Remember request accepted. Knowledge processing is not yet confirmed.";''')
    replace('''  const timer = setInterval(() => { for (const id of active) background(heartbeat(id)); background(pump()); }, 30000);
  timer.unref();''', '''  const startupDiscovery = setTimeout(() => background(discoverSessions()), 250);
  startupDiscovery.unref();
  const timer = setInterval(() => { for (const id of active) background(heartbeat(id)); background(discoverSessions()); background(pump()); }, 30000);
  timer.unref();''')
    replace('''      clearInterval(timer);''', '''      disposed = true; clearTimeout(startupDiscovery); clearInterval(timer);''')
    return text
