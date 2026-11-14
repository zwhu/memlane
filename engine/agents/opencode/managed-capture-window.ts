/** Metadata-only activation boundary for the official OpenCode collector. */
export class CaptureWindow {
  private readonly turns = new Map<string, Map<string, boolean>>();
  private readonly finished = new Map<string, Set<string>>();
  private readonly answers = new Map<string, Map<string, string>>();
  constructor(private readonly activatedAt = Date.now()) {}
  prompt(native: string, id: string, created: number, allowed: boolean) {
    const turns = this.turns.get(native) ?? new Map<string, boolean>();
    if (!turns.has(id)) turns.set(id, allowed && Number.isFinite(created) && created >= this.activatedAt);
    while (turns.size > 64) turns.delete(turns.keys().next().value!);
    this.turns.set(native, turns);
  }
  completed(native: string, id: string, parent: string) {
    if (this.turns.get(native)?.get(parent) !== true || this.finished.get(native)?.has(id)) return;
    const answers = this.answers.get(native) ?? new Map<string, string>();
    answers.set(id, parent);
    while (answers.size > 64) answers.delete(answers.keys().next().value!);
    this.answers.set(native, answers);
  }
  candidates(native: string): [string, string][] {
    return [...(this.answers.get(native) ?? [])].filter(([, parent]) => this.turns.get(native)?.get(parent) === true);
  }
  allowed(native: string, id: string) { return this.turns.get(native)?.get(id) === true; }
  finish(native: string, id: string) {
    this.answers.get(native)?.delete(id);
    const finished = this.finished.get(native) ?? new Set<string>();
    finished.add(id); this.finished.set(native, finished);
    if (finished.size > 4096) { this.disable(native); finished.clear(); }
  }
  disable(native: string) {
    for (const id of this.turns.get(native)?.keys() ?? []) this.turns.get(native)!.set(id, false);
    this.answers.delete(native);
  }
  clear(native: string) { this.turns.delete(native); this.answers.delete(native); this.finished.delete(native); }
}
