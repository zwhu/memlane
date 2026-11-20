/** Managed hook, conversation capture, memory tagging, and session discovery helpers. */
import type { CaptureWindow } from "./managed-capture-window.js";
import type { ManagedProjectControl } from "./managed-control.js";
import type { ManagedDelivery } from "./managed-delivery.js";
import type { ManagedActions } from "./managed-actions.js";
import type { Outbox } from "./runtime.js";

type TextPart = { type: string; text?: string; synthetic?: boolean };
type MessagePayload = {
  info: {
    id: string;
    sessionID: string;
    role: string;
    parentID?: string;
    time?: { completed?: number; created?: number };
    error?: unknown;
  };
  parts: TextPart[];
};
type SessionClient = {
  message?: (args: { path: { id: string; messageID: string }; query: { directory: string } }) =>
    Promise<{ error?: unknown; data?: MessagePayload }>;
  list?: (args: { query: { directory: string }; signal: AbortSignal }) =>
    Promise<{ error?: unknown; data?: Array<{ id?: unknown; directory?: unknown; time?: { created?: number } }> }>;
};

const extractText = (parts: TextPart[]): string =>
  parts.filter((part) => part.type === "text" && !part.synthetic)
    .map((part) => (part.type === "text" ? (part.text ?? "") : ""))
    .join("\n");

export async function captureManagedConversation(
  deps: {
    autoCapture: boolean;
    sessionClient?: SessionClient;
    directory: string;
    control: ManagedProjectControl;
    delivery: ManagedDelivery;
    window: CaptureWindow;
    sid: (native: string) => string;
    safeText: (value: unknown) => string;
    hash: (value: string) => string;
  },
  native: string,
): Promise<void> {
  if (!deps.autoCapture) {
    deps.window.disable(native);
    return;
  }
  if (deps.window.candidates(native).length === 0) return;
  if (!await deps.control.permits(native, "capture")) {
    deps.window.disable(native);
    return;
  }
  // Read only IDs observed after activation, never list a session's history.
  for (const [messageID, parentID] of deps.window.candidates(native)) {
    if (!await deps.control.permits(native, "capture")) {
      deps.window.disable(native);
      return;
    }
    const response = await deps.sessionClient!.message!({ path: { id: native, messageID }, query: { directory: deps.directory } });
    if (response.error || !response.data) throw new Error("Cannot read observed completed message");
    const message = response.data;
    if (message.info.id !== messageID || message.info.sessionID !== native || message.info.role !== "assistant" ||
        message.info.parentID !== parentID || !message.info.time?.completed || message.info.error) {
      deps.window.finish(native, messageID);
      continue;
    }
    if (!await deps.control.permits(native, "capture")) {
      deps.window.disable(native);
      return;
    }
    const questionResponse = await deps.sessionClient!.message!({ path: { id: native, messageID: parentID }, query: { directory: deps.directory } });
    if (questionResponse.error || !questionResponse.data) throw new Error("Cannot read observed parent message");
    const parent = questionResponse.data;
    if (parent.info.id !== parentID || parent.info.sessionID !== native || parent.info.role !== "user") {
      deps.window.finish(native, messageID);
      continue;
    }
    const question = deps.safeText(extractText(parent.parts));
    const answer = deps.safeText(extractText(message.parts));
    if (question && answer) {
      const id = deps.hash(deps.sid(native) + ":qa:" + message.info.id);
      await deps.delivery.enqueue(native, id, deps.sid(native), { type: "qa", question, answer, context: `opencode_capture:${id}` });
    }
    deps.window.finish(native, messageID);
  }
}

export async function recordManagedPrompt(
  window: CaptureWindow,
  control: ManagedProjectControl,
  autoCapture: boolean,
  sessionID: string,
  messageID: string,
  created: number,
): Promise<void> {
  let capture = false;
  try {
    capture = autoCapture && await control.permits(sessionID, "capture");
  } catch {
    /* Unconfirmed turns are never backfilled. */
  }
  window.prompt(sessionID, messageID, created, capture);
}

export async function allowManagedToolCapture(
  window: CaptureWindow,
  control: ManagedProjectControl,
  promptID: string | undefined,
  sessionID: string,
): Promise<boolean> {
  if (!promptID || !window.allowed(sessionID, promptID) || !await control.permits(sessionID, "capture")) {
    window.disable(sessionID);
    return false;
  }
  return true;
}

export function observeManagedMessageEvent(
  window: CaptureWindow,
  event: { type?: string; properties?: unknown },
): void {
  if (event.type !== "message.updated" || !event.properties || typeof event.properties !== "object") return;
  const info = (event.properties as { info?: { role?: string; time?: { completed?: number }; error?: unknown; sessionID?: string; id?: string; parentID?: string } }).info;
  if (info?.role === "assistant" && info.time?.completed && !info.error && info.sessionID && info.id && info.parentID) {
    window.completed(info.sessionID, info.id, info.parentID);
  }
}

export function createOwnedMemoryHelpers(instanceHash: string) {
  const memoryMarker = `<memlane_opencode_memory instance="${instanceHash}">`;
  const removeOwnedMemory = (values: string[]) => {
    for (let i = values.length - 1; i >= 0; i--) {
      if (values[i].startsWith(memoryMarker)) values.splice(i, 1);
    }
  };
  const wrapMemory = (value: string) => memoryMarker + value + "</memlane_opencode_memory>";
  return { removeOwnedMemory, wrapMemory };
}

export function createSessionDiscoverer(deps: {
  isDisposed: () => boolean;
  autoCapture: boolean;
  sessionClient?: SessionClient;
  directory: string;
  activatedAt: number;
  hasActive: (id: string) => boolean;
  onSession: (id: string) => void;
}) {
  let discovering = false;
  // Some native versions publish the first session event during bootstrap.
  // Reconcile only post-activation metadata in this exact host directory.
  return async () => {
    if (deps.isDisposed() || discovering || !deps.autoCapture || !deps.sessionClient?.list) return;
    discovering = true;
    try {
      const result = await deps.sessionClient.list({ query: { directory: deps.directory }, signal: AbortSignal.timeout(2500) });
      if (deps.isDisposed() || result.error || !Array.isArray(result.data)) return;
      for (const info of result.data) {
        if (typeof info.id === "string" && info.id && info.id.length <= 256 && info.directory === deps.directory &&
            Number.isFinite(info.time?.created) && info.time!.created! >= deps.activatedAt && !deps.hasActive(info.id)) {
          deps.onSession(info.id);
        }
      }
    } finally {
      discovering = false;
    }
  };
}

export async function maybeImproveCompletedSession(
  outbox: Outbox,
  control: ManagedProjectControl,
  actions: ManagedActions,
  enabled: boolean,
  native: string,
  session: string,
): Promise<void> {
  const completed = outbox.completedTarget(native);
  if (enabled && completed) {
    await control.currentTarget(completed.target);
    await actions.improve(native, session, completed.watermark);
  }
}

export async function executeManagedRemember(
  actions: ManagedActions,
  context: { sessionID: string; messageID: string },
  sid: (native: string) => string,
  safeText: (value: unknown) => string,
  fact: string,
): Promise<string> {
  const receipt = await actions.remember(context.sessionID, context.messageID, sid(context.sessionID), safeText(fact));
  if (receipt.status === "unknown") return "Original remember result is unknown. Do not resubmit; inspect the original receipt.";
  if (receipt.status !== "accepted") return "Remember was rejected; no automatic retry. Inspect the original receipt.";
  return "Remember request accepted. Knowledge processing is not yet confirmed.";
}
