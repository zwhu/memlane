"""Strict overlays for the pinned official OpenCode plugin, managed installs only.

Runtime logic lives in standalone TypeScript modules under engine/agents/opencode/
(managed-http.ts, managed-outbox.ts, managed-hooks.ts, etc.); this file performs
only exact upstream anchor checks and minimal delegations.
"""


def _replace_once(text, old, new, label, count=1):
    if text.count(old) != count:
        raise ValueError(f'fixed OpenCode {label} overlay anchor mismatch')
    return text.replace(old, new)


def _replace_section(text, start, end, replacement, label, *, require_inside=()):
    if text.count(start) != 1 or text.count(end) != 1:
        raise ValueError(f'fixed OpenCode {label} overlay anchor mismatch')
    first, last = text.index(start), text.index(end)
    if last <= first:
        raise ValueError(f'fixed OpenCode {label} overlay method order mismatch')
    original = text[first:last]
    for needle in require_inside:
        if needle not in original:
            raise ValueError(f'fixed OpenCode {label} overlay input mismatch')
    return text[:first] + replacement + text[last:]


def managed_http_client(text):
    if 'const user = this.username || "default_user@example.com";' not in text:
        raise ValueError('fixed OpenCode HTTP overlay input mismatch')
    text = _replace_once(
        text,
        'import { hash } from "./runtime.js";\n',
        'import { hash } from "./runtime.js";\n'
        'import { buildManagedHeaders, ensureManagedAuth, hasManagedSessionEntry, managedRequestError, mergeManagedHeaders, rejectManagedLogin } from "./managed-http.js";\n',
        'HTTP',
    )
    text = _replace_section(
        text, '  async login(): Promise<void> {\n', '  async ensureAuth(): Promise<void> {\n',
        '  async login(): Promise<void> {\n    rejectManagedLogin();\n  }\n\n', 'HTTP',
    )
    text = _replace_section(
        text, '  async ensureAuth(): Promise<void> {\n', '  private buildHeaders(): Record<string, string> {\n',
        '  async ensureAuth(): Promise<void> {\n    ensureManagedAuth(this.mode, this.apiKey);\n  }\n\n', 'HTTP',
    )
    text = _replace_section(
        text, '  private buildHeaders(): Record<string, string> {\n', '  async fetchAPI<T>(\n',
        '  private buildHeaders(): Record<string, string> {\n'
        '    return buildManagedHeaders(this.apiKey);\n'
        '  }\n'
        '  private managedHeaders(init: RequestInit): Headers {\n'
        '    return mergeManagedHeaders(this.apiKey, init);\n'
        '  }\n\n', 'HTTP',
    )
    for old, new, count in (
        ('retries = init.method === "GET" ? MAX_RETRIES : 0,', 'retries = 0,', 1),
        ('headers: { ...this.buildHeaders(), ...(init.headers as Record<string, string>) },',
         'headers: this.managedHeaders(init),\n          redirect: "error",', 2),
        ('throw new Error(`Cognee request failed (${retryResponse.status}): ${errorText}`);',
         'throw managedRequestError(retryResponse.status, errorText);', 1),
        ('throw new Error(`Cognee request failed (${response.status}): ${errorText}`);',
         'throw managedRequestError(response.status, errorText);', 1),
        ('datasetId: response.dataset_id ?? params.datasetId ?? "",', 'datasetId: response.dataset_id ?? "",', 1),
        ('async entry(dataset: string, session: string, entry: Record<string, unknown>):',
         'async entry(dataset: string, session: string, entry: Record<string, unknown>, captureMode: "automatic" | "manual" = "automatic"):', 1),
        ('method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ dataset_name: dataset, session_id: session, entry }),',
         'method: "POST", headers: { "Content-Type": "application/json", "X-Memlane-Capture-Mode": captureMode }, body: JSON.stringify({ dataset_id: dataset, session_id: session, entry }),', 1),
        ('dataset_names: [dataset]', 'dataset_ids: [dataset]', 1),
        ('    formData.append("datasetName", params.datasetName);',
         '    formData.append("datasetName", params.datasetName);\n    formData.append("run_in_background", "true");', 1),
        ('    searchPrompt?: string;\n',
         '    searchPrompt?: string;\n    onlyContext?: boolean;\n    nodeNames?: string[];\n', 1),
        ('        ...(params.searchPrompt ? { system_prompt: params.searchPrompt } : {}),',
         '        ...(params.searchPrompt ? { system_prompt: params.searchPrompt } : {}),\n'
         '        ...(params.onlyContext !== undefined ? { only_context: params.onlyContext } : {}),\n'
         '        ...(params.nodeNames ? { node_name: params.nodeNames } : {}),', 1),
    ):
        text = _replace_once(text, old, new, 'HTTP', count=count)
    text = _replace_section(
        text, '  async health(): Promise<{ status: string }> {\n', '  async remember(params: {\n',
        '  async health(): Promise<{ status: string }> {\n'
        '    return this.fetchAPI<{ status: string }>("/health", { method: "GET" });\n'
        '  }\n\n', 'HTTP',
    )
    text = _replace_section(
        text, '  async hasEntry(', '  async listDatasets():',
        '  async hasEntry(session: string, entry: Record<string, unknown>, dataset: string): Promise<boolean> {\n'
        '    return hasManagedSessionEntry((path) => this.fetchAPI<Record<string, unknown>>(path, { method: "GET" }, 2500, undefined, 0), session, entry, dataset);\n'
        '  }\n\n', 'HTTP',
    )
    return text


def managed_outbox(text):
    text = _replace_once(
        text,
        'type Pending = { id: string; session: string; entry: Entry; uncertain?: boolean };\n'
        'type State = { pending: Pending[]; seen: string[] };',
        'export type ProjectTarget = ManagedProjectTarget;\n'
        'import {\n'
        '  LocalPolicyDenied, type ManagedPending as Pending, type ProjectTarget as ManagedProjectTarget, type ManagedState as State,\n'
        '  type OutboxStore, applyPendingDecision, buildBacklogReview, buildPendingReview, findCompletedTarget, inspectBacklogEntry, inspectUnknownEntries,\n'
        '  listBlockedIds, listPendingIds, listPendingSessions, outboxStatus, releaseBacklogItem,\n'
        '  retryBlockedEntries, runManagedOutboxFlush, snapshotTarget,\n'
        '} from "./managed-outbox.js";\n'
        'export { LocalPolicyDenied };',
        'outbox',
    )
    text = _replace_once(
        text,
        '  private running?: Promise<void>;',
        '  private running?: Promise<void>;\n'
        '  private senderHeld = false;\n'
        '  private senderToken?: object;\n'
        '  private store(): OutboxStore { return { lock: (run) => this.lock(run), read: () => this.read(), write: (state) => this.write(state) }; }',
        'outbox',
    )
    text = _replace_once(
        text,
        '  private lock<T>(run: () => T): T {\n    const path = this.file + ".lock";\n',
        '  private acquire(path: string): () => void {\n',
        'outbox',
    )
    text = _replace_once(
        text,
        '    try { return run(); } finally { rmSync(path, { recursive: true }); }\n  }\n',
        '    return () => rmSync(path, { recursive: true });\n  }\n'
        '  private lock<T>(run: () => T): T {\n'
        '    const release = this.acquire(this.file + ".lock");\n'
        '    try { return run(); } finally { release(); }\n'
        '  }\n'
        '  async withSender<T>(run: (token: object) => Promise<T>): Promise<T> {\n'
        '    const release = this.acquire(this.file + ".sender");\n'
        '    this.senderHeld = true; this.senderToken = Object.freeze({});\n'
        '    try { return await run(this.senderToken); }\n'
        '    finally { this.senderHeld = false; this.senderToken = undefined; release(); }\n'
        '  }\n',
        'outbox',
    )
    text = _replace_once(
        text,
        '  enqueue(id: string, session: string, entry: Entry): void {',
        '  enqueue(id: string, session: string, entry: Entry, target?: ProjectTarget): void {\n'
        '    const original = target === undefined ? undefined : snapshotTarget(target);',
        'outbox',
    )
    text = _replace_once(
        text,
        'state.seen.includes(id) || state.pending.some((item) => item.id === id)',
        'state.seen.includes(id) || state.discarded?.includes(id) || state.pending.some((item) => item.id === id)',
        'outbox',
    )
    text = _replace_once(
        text,
        'state.pending.push({ id, session, entry: scrub(entry) as Entry });',
        'state.pending.push({ id, session, entry: scrub(entry) as Entry, ...(original ? { target: original } : {}) });',
        'outbox',
    )
    text = _replace_once(
        text,
        '  status(): { pending: number; uncertain: number; saved: number } {\n'
        '    const state = this.read();\n'
        '    return { pending: state.pending.length, uncertain: state.pending.filter((item) => item.uncertain).length, saved: state.seen.length };\n'
        '  }\n',
        '  status() { return outboxStatus(this.read()); }\n'
        '  blockedIds() { return listBlockedIds(this.read()); }\n'
        '  retryBlocked(ids: string[]) { return retryBlockedEntries(this.store(), Boolean(this.running), ids); }\n'
        '  pendingSessions() { return listPendingSessions(this.store()); }\n'
        '  reviewPending(target: ProjectTarget, session: string) { return buildPendingReview(this.store(), target, session); }\n'
        '  reviewBacklog(target: ProjectTarget, session: string) { return buildBacklogReview(this.store(), target, session); }\n'
        '  entryState(target: ProjectTarget, session: string, id: string) { return inspectBacklogEntry(this.store(), target, session, id); }\n'
        '  releaseBacklogEntry(target: ProjectTarget, session: string, id: string) { return releaseBacklogItem(this.store(), this.senderHeld, Boolean(this.running), target, session, id); }\n'
        '  pendingIds(target: ProjectTarget, session: string) { return listPendingIds(this.store(), target, session); }\n'
        '  inspectUnknown(target: ProjectTarget, session: string, signature: string, probe: (session: string, marker: Entry, dataset: string) => Promise<boolean>) {\n'
        '    return inspectUnknownEntries(this.store(), this.senderHeld, Boolean(this.running), target, session, signature, probe);\n'
        '  }\n'
        '  decidePending(target: ProjectTarget, session: string, signature: string, action: "replay" | "discard" | "acknowledge", matchedUnknown: string[] = []) {\n'
        '    return applyPendingDecision(this.store(), this.senderHeld, Boolean(this.running), target, session, signature, action, matchedUnknown);\n'
        '  }\n'
        '  completedTarget(native: string) { return findCompletedTarget(this.store(), native); }\n',
        'outbox',
    )
    flush_start = '  flush(send: (item: Pending) => Promise<void>, committed: (item: Pending) => Promise<boolean>): Promise<void> {\n'
    flush_end = '    return this.running;\n  }\n'
    text = _replace_section(
        text, flush_start, flush_end,
        '  flush(send: (item: Pending) => Promise<void>, committed: (item: Pending) => Promise<boolean>, '
        'authorize?: (item: Pending) => Promise<boolean | "paused">, selected?: ReadonlySet<string>, senderToken?: object): Promise<void> {\n'
        '    if (senderToken && (!this.senderHeld || senderToken !== this.senderToken)) throw new Error("sender_lease_required");\n'
        '    if (this.running) return this.running;\n'
        '    const run = () => runManagedOutboxFlush(this.store(), send, committed, authorize, selected);\n'
        '    this.running = (senderToken ? run() : this.withSender(run)).finally(() => { this.running = undefined; });\n',
        'outbox',
        require_inside=(
            'const next = state.pending[0];',
            'if (item.uncertain) { if (!await committed(item)) return; }',
            'if (/(?:request|login) failed \\((?:400|401|403|404|422|429)\\)/.test(String(error))) {',
            'if (!state.seen.includes(item.id)) state.seen.push(item.id); this.write(state);',
        ),
    )
    return text


def managed_capture_callbacks(text):
    """Capture overlay only; config/recall/tool overlays gate production activation."""
    text = _replace_once(
        text,
        'import type { CogneePluginConfig } from "./types.js";',
        'import type { CogneePluginConfig } from "./types.js";\n'
        'import { ManagedProjectControl, type ControlOptions } from "./managed-control.js";\n'
        'import { ManagedDelivery } from "./managed-delivery.js";\n'
        'import { CaptureWindow } from "./managed-capture-window.js";\n'
        'import { allowManagedToolCapture, captureManagedConversation, observeManagedMessageEvent, recordManagedPrompt } from "./managed-hooks.js";',
        'capture',
    )
    text = _replace_once(
        text,
        '  const active = new Set<string>();',
        '  const binding = options?.memlane as ControlOptions;\n'
        '  if (!binding) throw new Error("managed_installation_required");\n'
        '  const control = new ManagedProjectControl({...binding, directory: ctx.directory});\n'
        '  const delivery = new ManagedDelivery(control, client, outbox);\n'
        '  const window = new CaptureWindow();\n'
        '  const active = new Set<string>();',
        'capture',
    )
    text = _replace_once(
        text,
        '  const pump = () => cfg.autoCapture ? outbox.flush(\n'
        '    (item) => client.entry(cfg.datasetName, item.session, item.entry),\n'
        '    (item) => client.hasEntry(item.session, item.entry),\n'
        '  ) : Promise.resolve();',
        '  const pump = () => cfg.autoCapture ? delivery.flush() : Promise.resolve();',
        'capture',
    )
    text = _replace_section(
        text,
        '  async function captureConversation(native: string): Promise<void> {',
        '  return {\n    "chat.message":',
        '  const captureConversation = (native: string) => captureManagedConversation(\n'
        '    { autoCapture: cfg.autoCapture, sessionClient: ctx.client.session, directory: ctx.directory, control, delivery, window, sid, safeText, hash }, native);\n',
        'capture',
        require_inside=('ctx.client.session.messages(', 'outbox.enqueue('),
    )
    text = _replace_once(
        text,
        '      prompts.set(input.sessionID, {id: output.message.id, text});',
        '      await recordManagedPrompt(window, control, cfg.autoCapture, input.sessionID, output.message.id, output.message.time?.created);\n'
        '      prompts.set(input.sessionID, {id: output.message.id, text});',
        'capture',
    )
    text = _replace_once(
        text,
        '      if (!cfg.autoCapture || input.tool.startsWith("cognee_") || !allowedTool(input.tool, input.args, cfg.captureTools)) return;',
        '      if (!cfg.autoCapture || input.tool.startsWith("cognee_") || !allowedTool(input.tool, input.args, cfg.captureTools)) return;\n'
        '      const promptID = prompts.get(input.sessionID)?.id;\n'
        '      if (!promptID || !await allowManagedToolCapture(window, control, promptID, input.sessionID)) return;',
        'capture',
    )
    text = _replace_once(
        text,
        'try { outbox.enqueue(id, sid(input.sessionID), entry); background(pump()); }',
        'try { await delivery.enqueue(input.sessionID, id, sid(input.sessionID), entry, window.promptCreated(input.sessionID, promptID)); background(pump()); }',
        'capture',
    )
    text = _replace_once(
        text,
        '    event: async ({event}) => {\n',
        '    event: async ({event}) => {\n      observeManagedMessageEvent(window, event);\n',
        'capture',
    )
    text = _replace_once(
        text,
        '        active.delete(native); prompts.delete(native); recalls.delete(native);',
        '        window.clear(native);\n        active.delete(native); prompts.delete(native); recalls.delete(native);',
        'capture',
    )
    return text


def managed_config_recall(text):
    """Apply after capture overlay; remaining lifecycle/tool overlays still required."""
    text = _replace_once(
        text,
        'import { CaptureWindow } from "./managed-capture-window.js";',
        'import { CaptureWindow } from "./managed-capture-window.js";\n'
        'import { resolveManagedConfig } from "./managed-config.js";\n'
        'import { ManagedRecall } from "./managed-recall.js";\n'
        'import { createOwnedMemoryHelpers } from "./managed-hooks.js";',
        'config/recall',
    )
    text = _replace_section(
        text, 'const bool = ', 'export const CogneeOpenCodePlugin: Plugin',
        'export const resolveConfig = resolveManagedConfig;\n\n', 'config/recall',
        require_inside=('process.env.COGNEE_API_KEY',),
    )
    text = _replace_once(
        text,
        '  const cfg = resolveConfig((options?.cognee ?? options ?? {}) as CogneePluginConfig);',
        '  if (process.env.MEMLANE_PROCESSING_SESSION === "1") return {};\n'
        '  const cfg = resolveConfig(options?.cognee as CogneePluginConfig);',
        'config/recall',
    )
    text = _replace_once(
        text,
        '  const window = new CaptureWindow();',
        '  const window = new CaptureWindow();\n'
        '  const recallClient = new ManagedRecall(control, client, cfg);\n'
        '  const { removeOwnedMemory, wrapMemory } = createOwnedMemoryHelpers(hash(binding.instanceId));',
        'config/recall',
    )
    text = _replace_section(
        text,
        '  async function recall(native: string, query: string, explicit = false): Promise<string> {',
        '  const captureConversation =',
        '  async function recall(native: string, query: string, explicit = false): Promise<string> {\n'
        '    if ((!cfg.autoRecall && !explicit) || !query.trim()) return "";\n'
        '    await recallClient.query(native, query, sid(native));\n'
        '    return recallClient.context(native);\n'
        '  }\n',
        'config/recall',
        require_inside=('client.listDatasets()',),
    )
    text = _replace_once(
        text,
        '      if (!input.sessionID) return;\n'
        '      const memory = recalls.get(input.sessionID);\n'
        '      if (memory?.context && memory.id === prompts.get(input.sessionID)?.id && !output.system.includes(memory.context)) output.system.push(memory.context);',
        '      removeOwnedMemory(output.system);\n'
        '      if (!input.sessionID) return;\n'
        '      const memory = recalls.get(input.sessionID);\n'
        '      if (memory?.id === prompts.get(input.sessionID)?.id) {\n'
        '        const context = await recallClient.context(input.sessionID);\n'
        '        if (context) output.system.push(wrapMemory(context));\n'
        '      }',
        'config/recall',
    )
    text = _replace_once(
        text,
        '      const prompt = prompts.get(input.sessionID);\n'
        '      const context = prompt ? await recall(input.sessionID, prompt.text) : "";\n'
        '      if (context && !output.context.includes(context)) output.context.push(context);',
        '      removeOwnedMemory(output.context);\n'
        '      const prompt = prompts.get(input.sessionID);\n'
        '      const context = prompt ? await recall(input.sessionID, prompt.text) : "";\n'
        '      if (context) output.context.push(wrapMemory(context));',
        'config/recall',
    )
    text = _replace_once(
        text,
        '        window.clear(native);',
        '        recallClient.clear(native);\n        window.clear(native);',
        'config/recall',
    )
    return text


def managed_action_callbacks(text):
    text = _replace_once(
        text,
        'import { ManagedRecall } from "./managed-recall.js";',
        'import { ManagedRecall } from "./managed-recall.js";\n'
        'import { ManagedActions } from "./managed-actions.js";\n'
        'import { createSessionDiscoverer, executeManagedRemember, maybeImproveCompletedSession } from "./managed-hooks.js";\n'
        'import { join } from "node:path";',
        'action',
    )
    text = _replace_once(
        text,
        '  const outbox = new Outbox([cfg.baseUrl, hash(cfg.apiKey || cfg.username || "default"), cfg.datasetName, ctx.directory].join("|"), cfg.stateDir);\n'
        '  const binding = options?.memlane as ControlOptions;\n'
        '  if (!binding) throw new Error("managed_installation_required");',
        '  const binding = options?.memlane as ControlOptions;\n'
        '  if (!binding) throw new Error("managed_installation_required");\n'
        '  const outbox = new Outbox([cfg.baseUrl, binding.instanceId].join("|"), cfg.stateDir);',
        'action',
    )
    text = _replace_once(
        text,
        '  const window = new CaptureWindow();',
        '  const activatedAt = Date.now();\n'
        '  const window = new CaptureWindow(activatedAt);\n'
        '  let disposed = false;\n'
        '  const discoverSessions = createSessionDiscoverer({ isDisposed: () => disposed, autoCapture: cfg.autoCapture, sessionClient: ctx.client.session, directory: ctx.directory, activatedAt, hasActive: (id) => active.has(id), onSession: (id) => background(heartbeat(id)) });\n'
        '  const actions = new ManagedActions(control, client, join(cfg.stateDir, "actions"), binding.instanceId);',
        'action',
    )
    text = _replace_once(
        text,
        '  const sid = (id: string) => sessionId(ctx.directory, id);',
        '  const sid = (id: string) => `opencode_${hash(binding.instanceId).slice(0, 16)}_${hash(id).slice(0, 24)}`;',
        'action',
    )
    text = _replace_once(
        text,
        '    try { await client.lifecycle(sid(native), cfg.datasetName); } catch { log("lifecycle registration failed; check credentials and backend status"); }',
        '    if (!cfg.autoCapture) return;\n'
        '    try { await actions.lifecycle(native, sid(native)); } catch { log("lifecycle status unconfirmed"); }',
        'action',
    )
    text = _replace_once(
        text,
        '          if (cfg.improveOnSessionEnd !== false && cfg.autoCapture && outbox.status().pending === 0) await client.improve({datasetName: cfg.datasetName, sessionIds: [sid(native)]});',
        '          await maybeImproveCompletedSession(outbox, control, actions, Boolean(cfg.improveOnSessionEnd && cfg.autoCapture), native, sid(native));',
        'action',
    )
    text = _replace_once(
        text,
        'background(client.lifecycle(sid(native), cfg.datasetName, true));',
        'background(actions.lifecycle(native, sid(native), true));',
        'action',
    )
    text = _replace_once(
        text,
        '      await Promise.allSettled([...active].map((native) => client.lifecycle(sid(native), cfg.datasetName, true)));',
        '      await Promise.allSettled([...active].map((native) => actions.lifecycle(native, sid(native), true)));',
        'action',
    )
    text = _replace_once(
        text,
        'args: {fact: tool.schema.string()}, async execute(args) {\n'
        '        await client.remember({data: args.fact, datasetName: cfg.datasetName}); return "Saved to Cognee memory.";',
        'args: {fact: tool.schema.string()}, async execute(args, context) {\n'
        '        return executeManagedRemember(actions, context, sid, safeText, args.fact);',
        'action',
    )
    text = _replace_once(
        text,
        '  const timer = setInterval(() => { for (const id of active) background(heartbeat(id)); background(pump()); }, 30000);\n'
        '  timer.unref();',
        '  const startupDiscovery = setTimeout(() => background(discoverSessions()), 250);\n'
        '  startupDiscovery.unref();\n'
        '  const timer = setInterval(() => { for (const id of active) background(heartbeat(id)); background(discoverSessions()); background(pump()); }, 30000);\n'
        '  timer.unref();',
        'action',
    )
    text = _replace_once(text, '      clearInterval(timer);', '      disposed = true; clearTimeout(startupDiscovery); clearInterval(timer);', 'action')
    return text
