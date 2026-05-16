import { Canvas } from "./dag";
import { TaskSummary } from "../api/client";

// Live structural validation for the graph editor. Runs on every keystroke /
// edit and tells the UI whether Save can fire. Distinct from `safeCompile`:
// compile is the authoritative "can we produce a YAML"; this layer is
// friendlier — it surfaces problems by node id so the canvas can paint
// them, and gives the user multiple diagnostics at once instead of bailing
// on the first.

export type ValidationSeverity = "error" | "warning";

export interface ValidationIssue {
  severity: ValidationSeverity;
  message: string;
  nodeIds?: string[]; // nodes implicated in this issue, for visual highlight
}

export interface ValidationResult {
  issues: ValidationIssue[];
  /** node ids the canvas should paint as having a problem */
  problemNodes: Set<string>;
  /** node ids that have warnings only (not errors) */
  warnNodes: Set<string>;
  /** true if any error-severity issues; Save must be disabled. */
  hasErrors: boolean;
}

export function validateCanvas(
  canvas: Canvas,
  knownTasks: TaskSummary[],
): ValidationResult {
  const issues: ValidationIssue[] = [];
  const problemNodes = new Set<string>();
  const warnNodes = new Set<string>();

  if (canvas.nodes.length === 0) {
    return { issues, problemNodes, warnNodes, hasErrors: false };
  }

  // ---- degrees ----------------------------------------------------------
  const inDeg = new Map<string, number>();
  const outDeg = new Map<string, number>();
  for (const n of canvas.nodes) {
    inDeg.set(n.id, 0);
    outDeg.set(n.id, 0);
  }
  for (const e of canvas.edges) {
    if (!inDeg.has(e.target) || !outDeg.has(e.source)) continue;
    inDeg.set(e.target, (inDeg.get(e.target) ?? 0) + 1);
    outDeg.set(e.source, (outDeg.get(e.source) ?? 0) + 1);
  }

  // ---- multi-start / multi-end ------------------------------------------
  const starts = canvas.nodes.filter((n) => (inDeg.get(n.id) ?? 0) === 0);
  const ends = canvas.nodes.filter((n) => (outDeg.get(n.id) ?? 0) === 0);

  if (starts.length === 0) {
    issues.push({
      severity: "error",
      message: "Flow has no entry node — every node has an incoming edge (cycle?)",
    });
  } else if (starts.length > 1) {
    const ids = starts.map((n) => n.id);
    issues.push({
      severity: "error",
      message: `Flow has ${starts.length} entry nodes (${starts.map((n) => `"${n.taskName}"`).join(", ")}). A flow must have exactly one start.`,
      nodeIds: ids,
    });
    ids.forEach((id) => problemNodes.add(id));
  }

  if (ends.length === 0) {
    issues.push({
      severity: "error",
      message: "Flow has no terminal node — every node has an outgoing edge (cycle?)",
    });
  } else if (ends.length > 1) {
    const ids = ends.map((n) => n.id);
    issues.push({
      severity: "error",
      message: `Flow has ${ends.length} end nodes (${ends.map((n) => `"${n.taskName}"`).join(", ")}). All branches must converge on a single terminal task.`,
      nodeIds: ids,
    });
    ids.forEach((id) => problemNodes.add(id));
  }

  // ---- cycles -----------------------------------------------------------
  if (hasCycle(canvas)) {
    issues.push({
      severity: "error",
      message: "Graph has a cycle — Jigsaw flows must be acyclic.",
    });
  }

  // ---- reachability -----------------------------------------------------
  if (starts.length === 1) {
    const reachable = reachableFrom(canvas, starts[0].id);
    const unreachable = canvas.nodes.filter((n) => !reachable.has(n.id));
    if (unreachable.length > 0) {
      const ids = unreachable.map((n) => n.id);
      issues.push({
        severity: "error",
        message: `${unreachable.length} unreachable node(s): ${unreachable.map((n) => `"${n.taskName}"`).join(", ")}. Every node must be reachable from the start.`,
        nodeIds: ids,
      });
      ids.forEach((id) => problemNodes.add(id));
    }
  }

  // ---- unknown task names ----------------------------------------------
  const known = new Set(knownTasks.map((t) => t.name));
  for (const n of canvas.nodes) {
    if (n.taskName.startsWith("·")) continue; // virtual fork/join
    if (!known.has(n.taskName)) {
      issues.push({
        severity: "error",
        message: `Task "${n.taskName}" is not defined under tasks/. Add it before saving the flow.`,
        nodeIds: [n.id],
      });
      problemNodes.add(n.id);
    }
  }

  // ---- unimplemented logic (warning) -----------------------------------
  const taskByName = new Map(knownTasks.map((t) => [t.name, t]));
  for (const n of canvas.nodes) {
    if (n.taskName.startsWith("·")) continue;
    const t = taskByName.get(n.taskName);
    if (t && !t.logic_implemented && t.logic) {
      warnNodes.add(n.id);
      issues.push({
        severity: "warning",
        message: `Task "${n.taskName}" references logic "${t.logic}" which is not registered. The flow will fail at runtime until that handler is implemented.`,
        nodeIds: [n.id],
      });
    }
  }

  // ---- duplicate task placements (warning) -----------------------------
  const seen = new Map<string, string[]>();
  for (const n of canvas.nodes) {
    if (n.taskName.startsWith("·")) continue;
    if (!seen.has(n.taskName)) seen.set(n.taskName, []);
    seen.get(n.taskName)!.push(n.id);
  }
  for (const [name, ids] of seen) {
    if (ids.length > 1) {
      issues.push({
        severity: "warning",
        message: `Task "${name}" appears ${ids.length} times in this flow. Labels become ambiguous when the same task is placed multiple times.`,
        nodeIds: ids,
      });
      ids.forEach((id) => warnNodes.add(id));
    }
  }

  const hasErrors = issues.some((i) => i.severity === "error");
  return { issues, problemNodes, warnNodes, hasErrors };
}

function hasCycle(canvas: Canvas): boolean {
  const out = new Map<string, string[]>();
  for (const n of canvas.nodes) out.set(n.id, []);
  for (const e of canvas.edges) out.get(e.source)?.push(e.target);
  const WHITE = 0, GRAY = 1, BLACK = 2;
  const color = new Map<string, number>();
  for (const n of canvas.nodes) color.set(n.id, WHITE);
  function dfs(id: string): boolean {
    color.set(id, GRAY);
    for (const next of out.get(id) ?? []) {
      const c = color.get(next);
      if (c === GRAY) return true;
      if (c === WHITE && dfs(next)) return true;
    }
    color.set(id, BLACK);
    return false;
  }
  for (const n of canvas.nodes) if (color.get(n.id) === WHITE && dfs(n.id)) return true;
  return false;
}

function reachableFrom(canvas: Canvas, source: string): Set<string> {
  const out = new Map<string, string[]>();
  for (const n of canvas.nodes) out.set(n.id, []);
  for (const e of canvas.edges) out.get(e.source)?.push(e.target);
  const seen = new Set<string>();
  const stack = [source];
  while (stack.length) {
    const cur = stack.pop()!;
    if (seen.has(cur)) continue;
    seen.add(cur);
    for (const n of out.get(cur) ?? []) stack.push(n);
  }
  return seen;
}
