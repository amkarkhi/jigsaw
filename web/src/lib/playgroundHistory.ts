// playgroundHistory.ts — localStorage-backed history of recent Playground
// runs. Scoped per (mode, target) so jumping between endpoints/flows/tasks
// doesn't mix up "the last set of headers I used here".
//
// Each entry captures the editable shape of a run: inputs JSON, headers list
// (with enabled flags), optional params JSON, and sub. Pretty much what we
// need to re-populate the form.

export interface HistoryHeader {
  key: string;
  value: string;
  enabled: boolean;
}

export interface PlaygroundHistoryEntry {
  id: string;
  savedAt: number;
  label?: string; // optional user label; auto-runs leave it blank
  inputs: string; // raw JSON text — preserve user formatting
  headers: HistoryHeader[];
  params?: string;
  sub?: number;
}

const PREFIX = "jigsaw:playground:history:";
const MAX_ENTRIES = 20;

function key(scope: string): string {
  return `${PREFIX}${scope}`;
}

export function listHistory(scope: string): PlaygroundHistoryEntry[] {
  try {
    const raw = localStorage.getItem(key(scope));
    if (!raw) return [];
    const arr = JSON.parse(raw);
    if (!Array.isArray(arr)) return [];
    return arr as PlaygroundHistoryEntry[];
  } catch {
    return [];
  }
}

export function pushHistory(
  scope: string,
  entry: Omit<PlaygroundHistoryEntry, "id" | "savedAt">,
): PlaygroundHistoryEntry {
  const full: PlaygroundHistoryEntry = {
    ...entry,
    id: `h_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 7)}`,
    savedAt: Date.now(),
  };
  const current = listHistory(scope);
  // De-duplicate: if the most recent entry has identical content, skip.
  if (current.length > 0 && sameShape(current[0], full)) {
    return current[0];
  }
  current.unshift(full);
  while (current.length > MAX_ENTRIES) current.pop();
  localStorage.setItem(key(scope), JSON.stringify(current));
  return full;
}

export function deleteHistory(scope: string, id: string): void {
  const filtered = listHistory(scope).filter((e) => e.id !== id);
  if (filtered.length === 0) {
    localStorage.removeItem(key(scope));
  } else {
    localStorage.setItem(key(scope), JSON.stringify(filtered));
  }
}

export function renameHistory(
  scope: string,
  id: string,
  label: string,
): void {
  const entries = listHistory(scope);
  const idx = entries.findIndex((e) => e.id === id);
  if (idx < 0) return;
  entries[idx] = { ...entries[idx], label: label.trim() || undefined };
  localStorage.setItem(key(scope), JSON.stringify(entries));
}

function sameShape(
  a: PlaygroundHistoryEntry,
  b: Pick<PlaygroundHistoryEntry, "inputs" | "headers" | "params" | "sub">,
): boolean {
  if (a.inputs !== b.inputs) return false;
  if ((a.params ?? "") !== (b.params ?? "")) return false;
  if ((a.sub ?? 0) !== (b.sub ?? 0)) return false;
  if (a.headers.length !== b.headers.length) return false;
  for (let i = 0; i < a.headers.length; i++) {
    const x = a.headers[i];
    const y = b.headers[i];
    if (x.key !== y.key || x.value !== y.value || x.enabled !== y.enabled) return false;
  }
  return true;
}
