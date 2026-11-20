/** Managed HTTP authentication, header isolation, and session-entry verification. */

const UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export function rejectManagedLogin(): never {
  throw new Error("Managed OpenCode does not use owner login");
}

export function ensureManagedAuth(mode: string, apiKey?: string): void {
  if (mode !== "local" || !apiKey) {
    throw new Error("Managed OpenCode requires its installation API key");
  }
}

export function buildManagedHeaders(apiKey?: string): Record<string, string> {
  return { "X-Api-Key": apiKey! };
}

export function mergeManagedHeaders(apiKey: string | undefined, init: RequestInit): Headers {
  const headers = new Headers(init.headers);
  headers.delete("Authorization");
  headers.set("X-Api-Key", buildManagedHeaders(apiKey)["X-Api-Key"]);
  return headers;
}

export async function hasManagedSessionEntry(
  fetchSession: (path: string) => Promise<Record<string, unknown>>,
  session: string,
  entry: Record<string, unknown>,
  dataset: string,
): Promise<boolean> {
  if (!UUID_PATTERN.test(dataset)) return false;
  const marker = entry.type === "qa" ? entry.context : entry.type === "trace"
    ? (entry.method_params as Record<string, unknown> | undefined)?.cognee_capture_id : undefined;
  if (typeof marker !== "string" || !marker) return false;
  try {
    const data = await fetchSession("/api/v1/sessions/" + encodeURIComponent(session));
    if (data.session_id !== session || data.dataset_id !== dataset) return false;
    const entries = entry.type === "qa" ? data.qas : data.traces;
    return Array.isArray(entries) && entries.some((value) => {
      if (!value || typeof value !== "object") return false;
      const row = value as Record<string, unknown>;
      return (entry.type === "qa" ? row.context :
        (row.method_params as Record<string, unknown> | undefined)?.cognee_capture_id) === marker;
    });
  } catch {
    return false; // Unavailable or nonmatching evidence remains unknown.
  }
}
