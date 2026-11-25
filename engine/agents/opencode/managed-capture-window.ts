/** Metadata-only activation boundary for the official OpenCode collector. */
export class CaptureWindow {
  private readonly turns = new Map<string, Map<string, {created: number; capture: boolean | undefined}>>();
  private readonly finished = new Map<string, Set<string>>();
  private readonly answers = new Map<string, Map<string, string>>();
  constructor(private readonly activatedAt = Date.now()) {}
  prompt(native: string, id: string, created: number, capture: boolean | undefined) {
    const turns = this.turns.get(native) ?? new Map();
    if (!turns.has(id)) turns.set(id, {created, capture: Number.isFinite(created) && created >= this.activatedAt ? capture : false});
    while (turns.size > 64) turns.delete(turns.keys().next().value!);
    this.turns.set(native, turns);
  }
  completed(native: string, id: string, parent: string) {
    if (!this.allowed(native, parent) || this.finished.get(native)?.has(id)) return;
    const answers = this.answers.get(native) ?? new Map<string, string>();
    answers.set(id, parent);
    while (answers.size > 64) answers.delete(answers.keys().next().value!);
    this.answers.set(native, answers);
  }
  candidates(native: string): [string, string][] {
    return [...(this.answers.get(native) ?? [])].filter(([, parent]) => this.allowed(native, parent));
  }
  allowed(native: string, id: string) {
    const turn = this.turns.get(native)?.get(id);
    return Boolean(turn && turn.capture !== false);
  }
  promptCreated(native: string, id: string) { return this.turns.get(native)?.get(id)?.created; }
  finish(native: string, id: string) {
    this.answers.get(native)?.delete(id);
    const finished = this.finished.get(native) ?? new Set<string>();
    finished.add(id); this.finished.set(native, finished);
    if (finished.size > 4096) { this.disable(native); finished.clear(); }
  }
  disable(native: string) {
    for (const turn of this.turns.get(native)?.values() ?? []) turn.capture = false;
    this.answers.delete(native);
  }
  resume(native: string, updatedAt: string) {
    const since = Date.parse(updatedAt);
    if (!Number.isFinite(since)) return;
    for (const turn of this.turns.get(native)?.values() ?? []) {
      if (turn.created < since) turn.capture = false;
    }
    const answers = this.answers.get(native);
    for (const [id, parent] of answers ?? []) {
      if (!this.allowed(native, parent)) answers!.delete(id);
    }
  }
  clear(native: string) { this.turns.delete(native); this.answers.delete(native); this.finished.delete(native); }
}
