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
    return text
