// drafts.ts — local-only "save as draft" persistence. Lives in localStorage,
// never hits the server. Used for demoing, scratch experimentation, or
// stashing in-progress edits without polluting the on-disk config.
//
// Storage shape:
//   key: "jigsaw:draft:<scope>:<targetName>"
//   value: JSON array of DraftEntry, newest first.

export interface DraftEntry {
  id: string;             // unique within (scope, targetName)
  label: string;          // user-chosen name
  yaml: string;           // full YAML doc — what we'd otherwise have sent to /api/files
  savedAt: number;        // unix ms
}

const PREFIX = "jigsaw:draft:";

function key(scope: string, targetName: string): string {
  return `${PREFIX}${scope}:${targetName}`;
}

export function listDrafts(scope: string, targetName: string): DraftEntry[] {
  try {
    const raw = localStorage.getItem(key(scope, targetName));
    if (!raw) return [];
    const arr = JSON.parse(raw);
    if (!Array.isArray(arr)) return [];
    return arr as DraftEntry[];
  } catch {
    return [];
  }
}

export function saveDraft(
  scope: string,
  targetName: string,
  label: string,
  yaml: string,
): DraftEntry {
  const entry: DraftEntry = {
    id: `d_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 7)}`,
    label: label.trim() || `draft @ ${new Date().toLocaleString()}`,
    yaml,
    savedAt: Date.now(),
  };
  const current = listDrafts(scope, targetName);
  current.unshift(entry);
  localStorage.setItem(key(scope, targetName), JSON.stringify(current));
  return entry;
}

export function deleteDraft(scope: string, targetName: string, id: string): void {
  const filtered = listDrafts(scope, targetName).filter((d) => d.id !== id);
  if (filtered.length === 0) {
    localStorage.removeItem(key(scope, targetName));
  } else {
    localStorage.setItem(key(scope, targetName), JSON.stringify(filtered));
  }
}

export function renameDraft(
  scope: string,
  targetName: string,
  id: string,
  newLabel: string,
): void {
  const drafts = listDrafts(scope, targetName);
  const idx = drafts.findIndex((d) => d.id === id);
  if (idx < 0) return;
  drafts[idx] = { ...drafts[idx], label: newLabel.trim() || drafts[idx].label };
  localStorage.setItem(key(scope, targetName), JSON.stringify(drafts));
}
