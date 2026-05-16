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
// Positions are persisted in flow.metadata.layout, which the engine ignores.

import { Flow, ParallelBlock, TaskRef } from "./types";

export interface CanvasNode {
  id: string;
  taskName: string;
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
  entryId: string | null;
  exitId: string | null;
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
  let prev: string | null = null;
  for (const ref of flow.tasks) {
    const r = emitRef(canvas, ref);
    if (prev && r.entryId) canvas.edges.push({ id: newId("e"), source: prev, target: r.entryId });
    if (r.exitId) prev = r.exitId;
  }
  // Always start from a deterministic auto-layout so missing layout entries
  // still look sensible, then override with persisted positions where we have them.
  autoLayout(canvas);
  if (layout) {
    for (const n of canvas.nodes) {
      const p = layout[n.taskName];
      if (p) n.position = p;
    }
  }
  return canvas;
}

function emitRef(canvas: Canvas, ref: TaskRef): DecompiledChain {
  if (ref.name) {
    const id = newId("t");
    canvas.nodes.push({ id, taskName: ref.name, position: { x: 0, y: 0 } });
    return { entryId: id, exitId: id };
  }
  if (ref.parallel) {
    // Decompile branches and join with a virtual fan-in pattern: we create
    // no synthetic nodes. The "fork" is the previous task; we just emit
    // every branch's tasks and remember each branch tail. The caller will
    // connect the fork's predecessor to each branch entry, and each branch
    // tail to whatever comes next.
    //
    // Since this function operates inside a parent sequential context, the
    // way to express a parallel block here is: emit each branch as a
    // separate path from the fork, all converging on the next task.
    //
    // We can't truly model that without a synthetic fork/join node, so for
    // decompile we add invisible "fork" and "join" nodes. These get
    // suppressed in compile if the user hasn't broken the round-trip.
    const forkId = newId("fork");
    const joinId = newId("join");
    canvas.nodes.push({ id: forkId, taskName: "·fork", position: { x: 0, y: 0 } });
    canvas.nodes.push({ id: joinId, taskName: "·join", position: { x: 0, y: 0 } });
    for (const branch of ref.parallel.branches) {
      let bprev: string | null = forkId;
      for (const bref of branch.tasks) {
        const r = emitRef(canvas, bref);
        if (bprev && r.entryId) canvas.edges.push({ id: newId("e"), source: bprev, target: r.entryId });
        if (r.exitId) bprev = r.exitId;
      }
      if (bprev) canvas.edges.push({ id: newId("e"), source: bprev, target: joinId });
    }
    return { entryId: forkId, exitId: joinId };
  }
  return { entryId: null, exitId: null };
}

// Topological-ish auto-layout: each node sits at row = longest path from
// source, column = horizontal slot within row.
export function autoLayout(canvas: Canvas) {
  if (canvas.nodes.length === 0) return;
  const adj = adjacency(canvas);
  // Compute depth (longest path from any source).
  const depth = new Map<string, number>();
  for (const n of canvas.nodes) depth.set(n.id, 0);
  const order = topoOrder(canvas, adj);
  for (const id of order) {
    const d = depth.get(id) ?? 0;
    for (const next of adj.out.get(id) ?? []) {
      depth.set(next, Math.max(depth.get(next) ?? 0, d + 1));
    }
  }
  // Bucket by depth, spread horizontally.
  const buckets = new Map<number, string[]>();
  for (const [id, d] of depth) {
    if (!buckets.has(d)) buckets.set(d, []);
    buckets.get(d)!.push(id);
  }
  const ROW = 110;
  const COL = 220;
  for (const [d, ids] of buckets) {
    const totalWidth = (ids.length - 1) * COL;
    ids.forEach((id, i) => {
      const node = canvas.nodes.find((n) => n.id === id);
      if (node) node.position = { x: i * COL - totalWidth / 2, y: d * ROW };
    });
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
    const next: string | null = walk(cur, null, adj, topo, canvas, tasks);
    cur = next;
  }
  const layout: Record<string, { x: number; y: number }> = {};
  for (const n of canvas.nodes) {
    if (n.taskName.startsWith("·")) continue; // skip virtual fork/join
    layout[n.taskName] = n.position;
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
): string | null {
  if (cur === stop) return null;
  const node = canvas.nodes.find((n) => n.id === cur);
  if (!node) return null;
  const succ = adj.out.get(cur) ?? [];

  // Suppress synthetic fork/join nodes from the decompile path.
  const isVirtual = node.taskName.startsWith("·");

  if (succ.length === 0) {
    if (!isVirtual) out.push({ name: node.taskName });
    return null;
  }
  if (succ.length === 1) {
    if (!isVirtual) out.push({ name: node.taskName });
    return succ[0] === stop ? null : succ[0];
  }
  // Fork. Emit the current task, then a parallel block.
  if (!isVirtual) out.push({ name: node.taskName });
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
      cursor = walk(cursor, join, adj, topo, canvas, branchTasks);
    }
    block.branches.push({ label: `branch_${i + 1}`, tasks: branchTasks });
  });
  out.push({ parallel: block });
  return join === stop ? null : join;
}

class CompileException extends Error {}

// Public wrapper that converts the exception path into a CompileResult.
export function safeCompile(canvas: Canvas): CompileResult {
  try {
    return compile(canvas);
  } catch (e) {
    if (e instanceof CompileException) return { ok: false, error: e.message };
    return { ok: false, error: (e as Error).message };
  }
}
