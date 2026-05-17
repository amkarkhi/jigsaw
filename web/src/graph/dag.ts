// DAG model + compile/decompile for the graph editor.
//
// The user edits a free-form DAG (nodes placed wherever, edges drawn manually).
// Jigsaw's YAML model is a sequence of TaskRefs, where parallelism is an
// explicit `parallel: {branches: [...]}` block. So we need:
//
//   YAML  ──decompile──▶  Canvas DAG  ──edit──▶  Canvas DAG  ──compile──▶  YAML
//
// Constraints the compiler enforces:
//
//   - Exactly one source (in-degree 0). Multiple sources => error.
//   - Acyclic. Cycles => error.
//   - Every fork (out-degree > 1) must have a matching join (in-degree > 1)
//     that's reachable from all forked paths. Otherwise we can't represent
//     the structure as `parallel: {branches}` and we reject.
//   - All nodes reachable from the source.
//
// Forks and joins are expressed by the topology of real task nodes — a node
// with out-degree > 1 is a fork point, and a node with in-degree > 1 is a
// join point. There are no synthetic fork/join marker nodes.
//
// Positions are persisted in a sidecar (.jigsaw/layouts/<flow>.json), kept
// out of the flow YAML so mouse-driven layout changes don't churn the config.

import { Flow, ParallelBlock, TaskRef } from "./types";

export interface CanvasNode {
  id: string;
  taskName: string;
  // Per-placement label (TaskRef.label). Carries through decompile/compile
  // so the same task can appear multiple times in a flow with distinct
  // identities.
  label?: string;
  // Full chain of enclosing parallel-branch labels, outermost first. A node
  // inside `parallel:[branch_1].parallel:[branch_3]` ends up with
  // branchPath = ["branch_1", "branch_3"]. Compile reads the index that
  // matches the current fork depth, so nested parallels round-trip with
  // their user-authored labels preserved at every level.
  branchPath?: string[];
  position: { x: number; y: number };
}

export interface CanvasEdge {
  id: string;
  source: string;
  target: string;
}

export interface Canvas {
  nodes: CanvasNode[];
  edges: CanvasEdge[];
}

export interface CompileError {
  message: string;
}

// ---------------------------------------------------------------------------
// Adjacency helpers
// ---------------------------------------------------------------------------

interface Adj {
  out: Map<string, string[]>;
  in: Map<string, string[]>;
}

function adjacency(c: Canvas): Adj {
  const out = new Map<string, string[]>();
  const inc = new Map<string, string[]>();
  for (const n of c.nodes) {
    out.set(n.id, []);
    inc.set(n.id, []);
  }
  for (const e of c.edges) {
    if (!out.has(e.source) || !inc.has(e.target)) continue;
    out.get(e.source)!.push(e.target);
    inc.get(e.target)!.push(e.source);
  }
  return { out, in: inc };
}

function findSource(c: Canvas, adj: Adj): string | null {
  const sources = c.nodes.filter((n) => (adj.in.get(n.id) ?? []).length === 0);
  if (sources.length !== 1) return null;
  return sources[0].id;
}

function detectCycle(c: Canvas, adj: Adj): boolean {
  const WHITE = 0, GRAY = 1, BLACK = 2;
  const color = new Map<string, number>();
  for (const n of c.nodes) color.set(n.id, WHITE);
  const stack: string[] = [];
  for (const n of c.nodes) {
    if (color.get(n.id) !== WHITE) continue;
    stack.push(n.id);
    while (stack.length > 0) {
      const top = stack[stack.length - 1];
      if (color.get(top) === WHITE) {
        color.set(top, GRAY);
      }
      let pushed = false;
      for (const next of adj.out.get(top) ?? []) {
        const co = color.get(next);
        if (co === GRAY) return true;
        if (co === WHITE) {
          stack.push(next);
          pushed = true;
          break;
        }
      }
      if (!pushed) {
        color.set(top, BLACK);
        stack.pop();
      }
    }
  }
  return false;
}

// Reachable set from a starting node, exclusive of `stopAt` if provided.
function reachable(adj: Adj, from: string): Set<string> {
  const seen = new Set<string>();
  const stack = [from];
  while (stack.length) {
    const cur = stack.pop()!;
    if (seen.has(cur)) continue;
    seen.add(cur);
    for (const next of adj.out.get(cur) ?? []) stack.push(next);
  }
  return seen;
}

// Find the join point for a fork: the earliest node (by topological order)
// reachable from ALL of `branches`. Returns null if none exists.
function findJoin(adj: Adj, topo: string[], branches: string[]): string | null {
  const reach = branches.map((b) => reachable(adj, b));
  for (const id of topo) {
    if (reach.every((r) => r.has(id))) return id;
  }
  return null;
}

function topoOrder(c: Canvas, adj: Adj): string[] {
  const inDeg = new Map<string, number>();
  for (const n of c.nodes) inDeg.set(n.id, (adj.in.get(n.id) ?? []).length);
  const out: string[] = [];
  const queue: string[] = [];
  for (const [id, d] of inDeg) if (d === 0) queue.push(id);
  while (queue.length) {
    const cur = queue.shift()!;
    out.push(cur);
    for (const next of adj.out.get(cur) ?? []) {
      const d = (inDeg.get(next) ?? 0) - 1;
      inDeg.set(next, d);
      if (d === 0) queue.push(next);
    }
  }
  return out;
}

// ---------------------------------------------------------------------------
// Decompile: Flow → Canvas
// ---------------------------------------------------------------------------

interface DecompiledChain {
  entryIds: string[];
  exitIds: string[];
}

let __idCounter = 0;
function newId(prefix = "n"): string {
  __idCounter += 1;
  return `${prefix}_${Date.now().toString(36)}_${__idCounter}`;
}

// Decompile a Flow into a Canvas. Positions come from the optional `layout`
// argument; nodes whose task name is missing from the layout get auto-placed.
export function decompile(flow: Flow, layout?: Record<string, { x: number; y: number }>): Canvas {
  __idCounter = 0;
  const canvas: Canvas = { nodes: [], edges: [] };
  let prevExits: string[] = [];
  for (const ref of flow.tasks) {
    const r = emitRef(canvas, ref, []);
    for (const px of prevExits) {
      for (const ce of r.entryIds) {
        canvas.edges.push({ id: newId("e"), source: px, target: ce });
      }
    }
    prevExits = r.exitIds;
  }
  // Always start from a deterministic auto-layout so missing layout entries
  // still look sensible, then override with persisted positions where we have them.
  autoLayout(canvas);
  if (layout) {
    // We key layout by canvas node identity (taskName + label) so the same
    // task placed multiple times in a flow gets distinct positions. Fall back
    // to the bare taskName for entries written by the old single-key format
    // so existing sidecars keep working.
    for (const n of canvas.nodes) {
      const p = layout[layoutKey(n)] ?? layout[n.taskName];
      if (p) n.position = p;
    }
  }
  return canvas;
}

// layoutKey returns the stable identity of a canvas node for layout
// persistence. Labels distinguish multiple placements of the same task;
// without a label the task name alone is the key.
export function layoutKey(n: { taskName: string; label?: string }): string {
  return n.label ? `${n.taskName}@${n.label}` : n.taskName;
}

function emitRef(canvas: Canvas, ref: TaskRef, path: string[]): DecompiledChain {
  if (ref.name) {
    const id = newId("t");
    canvas.nodes.push({
      id,
      taskName: ref.name,
      label: ref.label || undefined,
      branchPath: path.length > 0 ? [...path] : undefined,
      position: { x: 0, y: 0 },
    });
    return { entryIds: [id], exitIds: [id] };
  }
  if (ref.parallel) {
    // Parallel blocks are represented purely by topology — the preceding
    // task's edges fan out directly to each branch head, and each branch
    // tail's edges fan in directly to whatever comes next. compile()
    // recovers the parallel block from that shape. Nested parallels work
    // the same way: a branch whose first task is itself a parallel just
    // recurses, producing multiple entryIds for that branch.
    const entryIds: string[] = [];
    const exitIds: string[] = [];
    ref.parallel.branches.forEach((branch, i) => {
      const branchLabel = branch.label || `branch_${i + 1}`;
      const childPath = [...path, branchLabel];
      let bprev: string[] = [];
      let branchHead: string[] | null = null;
      for (const bref of branch.tasks) {
        const r = emitRef(canvas, bref, childPath);
        if (branchHead === null) branchHead = r.entryIds;
        for (const px of bprev) {
          for (const ce of r.entryIds) {
            canvas.edges.push({ id: newId("e"), source: px, target: ce });
          }
        }
        bprev = r.exitIds;
      }
      if (branchHead) entryIds.push(...branchHead);
      exitIds.push(...bprev);
    });
    return { entryIds, exitIds };
  }
  return { entryIds: [], exitIds: [] };
}

// Layered DAG auto-layout: each node sits at row = longest path from source,
// and its column is the barycenter of its predecessors' columns. This keeps a
// branch in its own vertical lane instead of collapsing every row to the
// canvas center. Within each row we resolve overlaps left-to-right and then
// re-center the row so the whole graph stays balanced.
export function autoLayout(canvas: Canvas) {
  if (canvas.nodes.length === 0) return;
  const adj = adjacency(canvas);
  const depth = new Map<string, number>();
  for (const n of canvas.nodes) depth.set(n.id, 0);
  const order = topoOrder(canvas, adj);
  for (const id of order) {
    const d = depth.get(id) ?? 0;
    for (const next of adj.out.get(id) ?? []) {
      depth.set(next, Math.max(depth.get(next) ?? 0, d + 1));
    }
  }
  const buckets = new Map<number, string[]>();
  for (const [id, d] of depth) {
    if (!buckets.has(d)) buckets.set(d, []);
    buckets.get(d)!.push(id);
  }

  const ROW = 110;
  const COL = 220;
  const pos = new Map<string, { x: number; y: number }>();
  const depths = [...buckets.keys()].sort((a, b) => a - b);

  for (const d of depths) {
    const ids = buckets.get(d)!;
    const desired = ids.map((id) => {
      const preds = adj.in.get(id) ?? [];
      if (preds.length === 0) return { id, x: 0 };
      const xs = preds.map((p) => pos.get(p)?.x ?? 0);
      return { id, x: xs.reduce((a, b) => a + b, 0) / xs.length };
    });
    // Sort by desired x to minimize crossings, then enforce min spacing.
    desired.sort((a, b) => a.x - b.x);
    let lastX = -Infinity;
    for (const it of desired) {
      let x = it.x;
      if (x < lastX + COL) x = lastX + COL;
      pos.set(it.id, { x, y: d * ROW });
      lastX = x;
    }
    // Re-center this row around 0 so the whole graph stays balanced —
    // single-item rows already sit at their predecessor's column.
    if (desired.length > 1) {
      const xs = desired.map((it) => pos.get(it.id)!.x);
      const offset = -(Math.min(...xs) + Math.max(...xs)) / 2;
      for (const it of desired) {
        const p = pos.get(it.id)!;
        pos.set(it.id, { x: p.x + offset, y: p.y });
      }
    }
  }
  for (const n of canvas.nodes) {
    const p = pos.get(n.id);
    if (p) n.position = p;
  }
}

// ---------------------------------------------------------------------------
// Compile: Canvas → Flow
// ---------------------------------------------------------------------------

export type CompileResult =
  | { ok: true; tasks: TaskRef[]; layout: Record<string, { x: number; y: number }> }
  | { ok: false; error: string };

export function compile(canvas: Canvas): CompileResult {
  if (canvas.nodes.length === 0) {
    return { ok: true, tasks: [], layout: {} };
  }
  const adj = adjacency(canvas);
  if (detectCycle(canvas, adj)) {
    return { ok: false, error: "graph has a cycle — Jigsaw flows must be acyclic" };
  }
  const source = findSource(canvas, adj);
  if (!source) {
    const sources = canvas.nodes.filter((n) => (adj.in.get(n.id) ?? []).length === 0);
    return {
      ok: false,
      error:
        sources.length === 0
          ? "no entry node — every node has an incoming edge (graph would have a cycle without sources)"
          : `multiple entry nodes (${sources
              .map((s) => s.taskName)
              .join(", ")}) — a flow must have exactly one starting task`,
    };
  }
  const topo = topoOrder(canvas, adj);
  // Confirm every node is reachable from source.
  const reach = reachable(adj, source);
  const unreachable = canvas.nodes.filter((n) => !reach.has(n.id));
  if (unreachable.length > 0) {
    return {
      ok: false,
      error: `${unreachable.length} unreachable node(s): ${unreachable
        .map((n) => n.taskName)
        .join(", ")}`,
    };
  }

  const tasks: TaskRef[] = [];
  let cur: string | null = source;
  while (cur) {
    const next: string | null = walk(cur, null, adj, topo, canvas, tasks, 0);
    cur = next;
  }
  const layout: Record<string, { x: number; y: number }> = {};
  for (const n of canvas.nodes) {
    layout[layoutKey(n)] = n.position;
  }
  return { ok: true, tasks, layout };
}

// walk advances from `cur` and emits TaskRefs onto `out`. Returns the next
// node to continue from (or null when the sink is reached). `stop` is the
// inclusive boundary — when we reach it, we return null instead of emitting it.
function walk(
  cur: string,
  stop: string | null,
  adj: Adj,
  topo: string[],
  canvas: Canvas,
  out: TaskRef[],
  depth: number,
): string | null {
  if (cur === stop) return null;
  const node = canvas.nodes.find((n) => n.id === cur);
  if (!node) return null;
  const succ = adj.out.get(cur) ?? [];

  if (succ.length === 0) {
    out.push(makeTaskRef(node));
    return null;
  }
  if (succ.length === 1) {
    out.push(makeTaskRef(node));
    return succ[0] === stop ? null : succ[0];
  }
  // Fork. The walker descends into each branch at depth+1 because each
  // branch entry is one level deeper than the current fork in the
  // user-authored branchPath chain. Emit the current task, then a parallel
  // block.
  out.push(makeTaskRef(node));
  const join = findJoin(adj, topo, succ);
  if (!join) {
    // Branches never converge: not representable.
    throw new CompileException(
      `fork at "${node.taskName}" has branches that don't rejoin — Jigsaw parallel blocks require all branches to converge on a single task`,
    );
  }
  const block: ParallelBlock = { branches: [] };
  succ.forEach((s, i) => {
    const branchTasks: TaskRef[] = [];
    let cursor: string | null = s;
    while (cursor) {
      cursor = walk(cursor, join, adj, topo, canvas, branchTasks, depth + 1);
    }
    // An empty branch (fork has a direct edge to join, skipping all
    // tasks) isn't representable in Jigsaw's `parallel:` shape — the
    // backend validator rejects it. Catch it here so the user sees the
    // problem inline without having to try Save.
    if (branchTasks.length === 0) {
      throw new CompileException(
        `parallel branch from "${node.taskName}" to "${
          canvas.nodes.find((n) => n.id === join)?.taskName ?? "?"
        }" has no tasks — every branch must contain at least one task`,
      );
    }
    // Preserve the user-authored branch label at this fork's depth.
    // branchPath holds the full chain from outermost to innermost; depth
    // is the index of the *current* fork's label within that chain.
    const firstNode = canvas.nodes.find((n) => n.id === s);
    const branchLabel = firstNode?.branchPath?.[depth] || `branch_${i + 1}`;
    block.branches.push({ label: branchLabel, tasks: branchTasks });
  });
  out.push({ parallel: block });
  return join === stop ? null : join;
}

class CompileException extends Error {}

// Build a TaskRef from a canvas node, attaching the per-placement label
// when set. The graph editor lets you label each placement independently
// so the same task can appear multiple times in one flow.
function makeTaskRef(node: CanvasNode): TaskRef {
  const ref: TaskRef = { name: node.taskName };
  if (node.label) ref.label = node.label;
  return ref;
}

// Public wrapper that converts the exception path into a CompileResult.
export function safeCompile(canvas: Canvas): CompileResult {
  try {
    return compile(canvas);
  } catch (e) {
    if (e instanceof CompileException) return { ok: false, error: e.message };
    return { ok: false, error: (e as Error).message };
  }
}
