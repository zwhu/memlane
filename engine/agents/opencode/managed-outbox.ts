/** Managed outbox state transitions, target snapshotting, and operator review helpers. */
import { createHash } from "node:crypto";
import { CapturePaused } from "./managed-http.js";

export type ProjectTarget = { native: string; project_id: string; dataset_id: string };
export type OutboxEntry = { type: "qa" | "trace"; [key: string]: unknown };
export type ManagedPending = {
  id: string;
  session: string;
  entry: OutboxEntry;
  uncertain?: boolean;
  blocked?: boolean;
  blocked_reason?: "local_policy_denied" | "official_auth_denied" | "official_request_rejected";
  retry_after?: number;
  retry_count?: number;
  target?: ProjectTarget;
};
export type SessionRateLimit = { retry_after: number; retry_count: number };
export type ManagedState = {
  pending: ManagedPending[];
  seen: string[];
  discarded?: string[];
  savedTargets?: Record<string, ProjectTarget>;
  sessionRateLimits?: Record<string, SessionRateLimit>;
};

export interface OutboxStore {
  lock<T>(run: () => T): T;
  read(): ManagedState;
  write(state: ManagedState): void;
}

export class LocalPolicyDenied extends Error {
  constructor() {
    super("local_policy_denied");
    this.name = "LocalPolicyDenied";
  }
}

export class SendCancelled extends Error {}

const UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const sha256 = (value: string) => createHash("sha256").update(value).digest("hex");

function sessionCooldownKey(session: string, target?: ProjectTarget): string {
  return target
    ? JSON.stringify([session, target.project_id, target.dataset_id, target.native])
    : JSON.stringify([session]);
}

function activeSessionCooldown(
  state: ManagedState,
  session: string,
  target?: ProjectTarget,
  now = Date.now(),
): number {
  const limit = state.sessionRateLimits?.[sessionCooldownKey(session, target)];
  return limit && typeof limit.retry_after === "number" && limit.retry_after > now ? limit.retry_after : 0;
}

function pruneExpiredRateLimits(state: ManagedState, now = Date.now()): void {
  if (!state.sessionRateLimits) return;
  for (const [key, limit] of Object.entries(state.sessionRateLimits)) {
    if (!limit || typeof limit.retry_after !== "number" || limit.retry_after <= now) {
      delete state.sessionRateLimits[key];
    }
  }
  if (Object.keys(state.sessionRateLimits).length === 0) {
    delete state.sessionRateLimits;
  }
}

export function snapshotTarget(target: ProjectTarget): ProjectTarget {
  const uuid = (value: unknown) => typeof value === "string" && UUID_PATTERN.test(value);
  if (!target || typeof target.native !== "string" || !target.native || target.native.length > 256 ||
      /[\x00-\x1f]/.test(target.native) || !uuid(target.project_id) || !uuid(target.dataset_id)) {
    throw new Error("original_project_target_required");
  }
  return { native: target.native, project_id: target.project_id, dataset_id: target.dataset_id };
}

export function outboxStatus(state: ManagedState): { pending: number; uncertain: number; saved: number; blocked: number } {
  return {
    pending: state.pending.length,
    uncertain: state.pending.filter((item) => item.uncertain).length,
    saved: state.seen.length,
    blocked: state.pending.filter((item) => item.blocked).length,
  };
}

export function listBlockedIds(state: ManagedState): string[] {
  return state.pending.filter((item) => item.blocked && !item.uncertain).map((item) => item.id);
}

export function retryBlockedEntries(store: OutboxStore, running: boolean, ids: string[]): number {
  if (running) throw new Error("Outbox flush is running");
  if (!Array.isArray(ids) || ids.length === 0 || ids.length > 100 || ids.some((id) => typeof id !== "string" || !id)) {
    throw new Error("Exact blocked entry IDs required");
  }
  const selected = new Set(ids);
  return store.lock(() => {
    const state = store.read();
    for (const id of selected) {
      if (!state.pending.some((item) => item.id === id && item.blocked && !item.uncertain)) {
        throw new Error("Selected entry is not a definite blocked outcome");
      }
    }
    for (const item of state.pending) {
      if (selected.has(item.id)) {
        item.blocked = false;
        delete item.blocked_reason;
        delete item.retry_after;
        delete item.retry_count;
      }
    }
    store.write(state);
    return selected.size;
  });
}

export function pendingForTarget(state: ManagedState, target: ProjectTarget, session: string): ManagedPending[] {
  const original = snapshotTarget(target);
  if (typeof session !== "string" || !session || session.length > 256) {
    throw new Error("original_pending_session_required");
  }
  return state.pending.filter((item) => {
    if (!item.target) throw new Error("original_project_target_required");
    const current = snapshotTarget(item.target);
    if (current.native !== original.native) return false;
    if (current.project_id !== original.project_id || current.dataset_id !== original.dataset_id || item.session !== session) {
      throw new Error("original_project_target_changed");
    }
    return true;
  });
}

export function listPendingSessions(store: OutboxStore): { target: ProjectTarget; session: string }[] {
  return store.lock(() => {
    const unique = new Map<string, { target: ProjectTarget; session: string }>();
    for (const item of store.read().pending) {
      if (!item.target || typeof item.session !== "string" || !item.session || item.session.length > 256) {
        throw new Error("original_project_target_required");
      }
      const value = { target: snapshotTarget(item.target), session: item.session };
      unique.set(JSON.stringify(value), value);
    }
    return [...unique.values()];
  });
}

export function buildPendingReview(store: OutboxStore, target: ProjectTarget, session: string) {
  return store.lock(() => {
    const items = pendingForTarget(store.read(), target, session);
    return {
      pending: items.length,
      unknown_outcomes: items.filter((item) => item.uncertain).length,
      signature: sha256(JSON.stringify(items)),
      session_id: session,
      ...(items.some((item) => item.blocked) ? { blocked: { reason: "operator_review_required" } } : {}),
    };
  });
}

export function buildBacklogReview(store: OutboxStore, target: ProjectTarget, session: string) {
  return store.lock(() => {
    const items = pendingForTarget(store.read(), target, session);
    return {
      session_id: session,
      tokens: items
        .filter((item) => !item.uncertain && !(item.blocked && item.blocked_reason === "official_request_rejected"))
        .map((item) => item.id),
      unknown: items.filter((item) => item.uncertain).length,
    };
  });
}

export function inspectBacklogEntry(
  store: OutboxStore,
  target: ProjectTarget,
  session: string,
  id: string,
): "saved" | "uncertain" | "rejected" | "auth_denied" | "deferred" | "pending" {
  return store.lock(() => {
    const state = store.read();
    const items = pendingForTarget(state, target, session);
    const item = items.find((entry) => entry.id === id);
    if (!item) return "saved";
    if (item.uncertain) return "uncertain";
    if (item.blocked) {
      return item.blocked_reason === "official_request_rejected" ? "rejected" : "auth_denied";
    }
    const now = Date.now();
    if ((typeof item.retry_after === "number" && item.retry_after > now) ||
        activeSessionCooldown(state, session, target, now) > now) {
      return "deferred";
    }
    return "pending";
  });
}

export function releaseBacklogItem(
  store: OutboxStore,
  senderHeld: boolean,
  running: boolean,
  target: ProjectTarget,
  session: string,
  id: string,
): "released" | "handled" | "isolated" | "deferred" {
  if (!senderHeld || running) throw new Error("sender_lease_required");
  if (!/^[a-f0-9]{64}$/.test(id)) throw new Error("invalid_pending_action");
  return store.lock(() => {
    const state = store.read();
    const items = pendingForTarget(state, target, session);
    const item = items.find((entry) => entry.id === id);
    if (!item) {
      if (state.pending.some((entry) => entry.id === id)) throw new Error("original_project_target_changed");
      return "handled";
    }
    if (item.uncertain || (item.blocked && item.blocked_reason === "official_request_rejected")) return "isolated";
    const now = Date.now();
    if ((typeof item.retry_after === "number" && item.retry_after > now) ||
        activeSessionCooldown(state, session, target, now) > now) {
      return "deferred";
    }
    item.blocked = false;
    delete item.blocked_reason;
    delete item.retry_after;
    delete item.retry_count;
    pruneExpiredRateLimits(state, now);
    store.write(state);
    return "released";
  });
}

export function listPendingIds(store: OutboxStore, target: ProjectTarget, session: string): string[] {
  return store.lock(() => pendingForTarget(store.read(), target, session).map((item) => item.id));
}

export async function inspectUnknownEntries(
  store: OutboxStore,
  senderHeld: boolean,
  running: boolean,
  target: ProjectTarget,
  session: string,
  signature: string,
  probe: (session: string, marker: OutboxEntry, dataset: string) => Promise<boolean>,
): Promise<string[]> {
  if (!senderHeld || running) throw new Error("sender_lease_required");
  const items = store.lock(() => {
    const selected = pendingForTarget(store.read(), target, session);
    if (signature !== sha256(JSON.stringify(selected))) throw new Error("pending_materials_changed_review_again");
    return JSON.parse(JSON.stringify(selected.filter((item) => item.uncertain))) as ManagedPending[];
  });
  const matched: string[] = [];
  for (const item of items) {
    const value = item.entry.type === "qa" ? item.entry.context :
      (item.entry.method_params as Record<string, unknown> | undefined)?.cognee_capture_id;
    if (typeof value !== "string" || !value || value.length > 160) continue;
    const marker: OutboxEntry = item.entry.type === "qa"
      ? { type: "qa", context: value }
      : { type: "trace", method_params: { cognee_capture_id: value } };
    if (await probe(item.session, marker, item.target!.dataset_id)) matched.push(item.id);
  }
  return matched;
}

export function applyPendingDecision(
  store: OutboxStore,
  senderHeld: boolean,
  running: boolean,
  target: ProjectTarget,
  session: string,
  signature: string,
  action: "replay" | "discard" | "acknowledge",
  matchedUnknown: string[] = [],
) {
  if (!senderHeld || running) throw new Error("sender_lease_required");
  if (!["replay", "discard", "acknowledge"].includes(action) || !Array.isArray(matchedUnknown)) {
    throw new Error("invalid_pending_action");
  }
  return store.lock(() => {
    const state = store.read();
    const items = pendingForTarget(state, target, session);
    if (typeof signature !== "string" || signature !== sha256(JSON.stringify(items))) {
      throw new Error("pending_materials_changed_review_again");
    }
    const matched = new Set(matchedUnknown);
    for (const id of matched) {
      if (!items.some((item) => item.id === id && item.uncertain)) {
        throw new Error("original_pending_marker_required");
      }
    }
    if (action === "replay" && items.some((item) => item.uncertain && !matched.has(item.id))) {
      throw new Error("original_write_result_still_unknown");
    }
    const removed = new Set<string>();
    const released: string[] = [];
    for (const item of items) {
      if (action === "discard") {
        state.discarded ??= [];
        if (!state.discarded.includes(item.id)) state.discarded.push(item.id);
        removed.add(item.id);
      } else if (matched.has(item.id)) {
        if (!state.seen.includes(item.id)) state.seen.push(item.id);
        state.savedTargets ??= {};
        state.savedTargets[item.id] = snapshotTarget(item.target!);
        removed.add(item.id);
      } else if (action === "replay") {
        item.blocked = false;
        delete item.blocked_reason;
        released.push(item.id);
      }
    }
    state.pending = state.pending.filter((item) => !removed.has(item.id));
    store.write(state);
    return {
      released_ids: released,
      matched_unknown: action === "discard" ? 0 : matched.size,
      discarded: action === "discard" ? removed.size : 0,
    };
  });
}

export function findCompletedTarget(
  store: OutboxStore,
  native: string,
): { target: ProjectTarget; watermark: string } | undefined {
  const state = store.read();
  if (state.pending.some((item) => !item.target || item.target.native === native)) return undefined;
  const rows = Object.entries(state.savedTargets ?? {})
    .filter(([id, target]) => state.seen.includes(id) && target.native === native)
    .sort(([a], [b]) => a.localeCompare(b));
  if (!rows.length) return undefined;
  const target = snapshotTarget(rows[0][1]);
  if (rows.some(([, other]) => other.project_id !== target.project_id || other.dataset_id !== target.dataset_id)) {
    throw new Error("original_project_target_changed");
  }
  return { target, watermark: sha256(JSON.stringify(rows.map(([id]) => id))) };
}

export async function runManagedOutboxFlush(
  store: OutboxStore,
  send: (item: ManagedPending) => Promise<void>,
  committed: (item: ManagedPending) => Promise<boolean>,
  authorize?: (item: ManagedPending) => Promise<boolean | "paused">,
  selected?: ReadonlySet<string>,
): Promise<void> {
  const unresolved = new Set<string>();
  const pausedProjects = new Set<string>();
  const rateLimitedSessions = new Set<string>();
  const deferPaused = (item: ManagedPending) => {
    if (item.target) pausedProjects.add(item.target.project_id);
    else unresolved.add(item.id);
  };
  for (;;) {
    const candidate = store.lock(() => {
      const now = Date.now();
      const state = store.read();
      const next = state.pending.find(
        (entry) =>
          !entry.blocked &&
          !unresolved.has(entry.id) &&
          (entry.uncertain || !entry.target || !pausedProjects.has(entry.target.project_id)) &&
          !rateLimitedSessions.has(sessionCooldownKey(entry.session, entry.target)) &&
          !(typeof entry.retry_after === "number" && entry.retry_after > now) &&
          !(activeSessionCooldown(state, entry.session, entry.target, now) > now) &&
          (!selected || selected.has(entry.id)),
      );
      return next ? JSON.parse(JSON.stringify(next)) as ManagedPending : undefined;
    });
    if (!candidate) return;
    // Policy lookup is read-only and occurs before marking an unsent write
    // uncertain. A control outage cannot manufacture an ambiguous write.
    const authorized = authorize ? await authorize(candidate) : true;
    if (authorized === "paused") {
      if (selected) throw new CapturePaused();
      deferPaused(candidate);
      continue;
    }
    if (!authorized) {
      if (candidate.uncertain) {
        unresolved.add(candidate.id);
        continue;
      }
      store.lock(() => {
        const state = store.read();
        const next = state.pending.find((entry) => entry.id === candidate.id);
        if (next && !next.uncertain && JSON.stringify(next) === JSON.stringify(candidate)) {
          next.blocked = true;
          next.blocked_reason = "local_policy_denied";
          store.write(state);
        }
      });
      continue;
    }
    const item = store.lock(() => {
      const state = store.read();
      const next = state.pending.find((entry) => entry.id === candidate.id && !entry.blocked);
      if (!next || JSON.stringify(next) !== JSON.stringify(candidate)) return undefined;
      const copy = JSON.parse(JSON.stringify(next)) as ManagedPending;
      next.uncertain = true;
      store.write(state);
      return copy;
    });
    if (!item) continue;
    if (item.uncertain) {
      if (!await committed(item)) {
        unresolved.add(item.id);
        continue;
      }
    } else {
      try {
        await send(item);
      } catch (error) {
        const message = String(error);
        const isCapturePaused = error instanceof CapturePaused;
        const isCancelled = error instanceof SendCancelled;
        const isLocalDenied = error instanceof LocalPolicyDenied;
        const isAuthDenied = /(?:request|login) failed \((?:401|403)\)/.test(message);
        const isRequestRejected = /(?:request|login) failed \((?:400|404|409|413|422)\)/.test(message);
        const isRateLimited = /(?:request|login) failed \(429\)/.test(message);
        if (isCapturePaused || isCancelled || isLocalDenied || isAuthDenied || isRequestRejected || isRateLimited) {
          store.lock(() => {
            const state = store.read();
            const pending = state.pending.find((p) => p.id === item.id);
            if (pending) {
              pending.uncertain = false;
              if (isLocalDenied) {
                pending.blocked = true;
                pending.blocked_reason = "local_policy_denied";
              } else if (isAuthDenied) {
                pending.blocked = true;
                pending.blocked_reason = "official_auth_denied";
              } else if (isRequestRejected) {
                pending.blocked = true;
                pending.blocked_reason = "official_request_rejected";
              } else if (isRateLimited) {
                const key = sessionCooldownKey(item.session, item.target);
                state.sessionRateLimits ??= {};
                const prevCount = Math.max(pending.retry_count ?? 0, state.sessionRateLimits[key]?.retry_count ?? 0);
                const count = Math.min(prevCount + 1, 6);
                const retryAfter = Date.now() + Math.min(5000 * (2 ** (count - 1)), 60000);
                pending.retry_count = count;
                pending.retry_after = retryAfter;
                state.sessionRateLimits[key] = { retry_after: retryAfter, retry_count: count };
              }
            }
            store.write(state);
          });
          if (isCapturePaused && !selected) {
            deferPaused(item);
            continue;
          }
          if (isRequestRejected) {
            continue;
          }
          if (isRateLimited) {
            rateLimitedSessions.add(sessionCooldownKey(item.session, item.target));
            continue;
          }
        }
        throw error;
      }
    }
    store.lock(() => {
      const state = store.read();
      state.pending = state.pending.filter((p) => p.id !== item.id);
      if (!state.seen.includes(item.id)) state.seen.push(item.id);
      if (item.target) {
        state.savedTargets ??= {};
        state.savedTargets[item.id] = snapshotTarget(item.target);
      }
      if (state.sessionRateLimits) {
        delete state.sessionRateLimits[sessionCooldownKey(item.session, item.target)];
        pruneExpiredRateLimits(state);
      }
      store.write(state);
    });
  }
}
