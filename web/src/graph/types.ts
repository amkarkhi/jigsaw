// Domain models for the flow graph editor. These map 1:1 onto Jigsaw's
// YAML shapes, deliberately not onto ReactFlow's internal types — that
// keeps the graph view and the serialization logic decoupled.

export interface Bind {
  in?: Record<string, string>;
  out?: Record<string, string>;
}

export interface TaskRef {
  name?: string;
  // Per-placement label. The same task can appear multiple times in a flow
  // as long as each placement has a distinct label.
  label?: string;
  parallel?: ParallelBlock;
  overrides?: unknown[];
  bind?: Bind;
}

export interface ParallelBlock {
  on_branch_failure?: string;
  branches: Branch[];
}

export interface Branch {
  label: string;
  tasks: TaskRef[];
}

export interface Flow {
  name: string;
  description?: string;
  version?: string;
  inherits?: string;
  tasks: TaskRef[];
  metadata?: Record<string, unknown>;
}

export interface FlowFile {
  flows: Flow[];
}

// Each visual node carries a path into the flow so mutations know what
// to change. The path is a list of indices: [3] is "the 4th top-level
// task"; [3, "branches", 0, "tasks", 1] is "the 2nd task of the 1st
// branch of the parallel block at top-level index 3".
export type NodePath = (number | string)[];

export type NodeKind = "task" | "parallel-header" | "branch-header";

export interface GraphNode {
  id: string;
  kind: NodeKind;
  label: string;
  sub?: string; // secondary line (logic handler, branch count, etc.)
  path: NodePath;
  // For parallel headers: identifies the wrapping container so the layout
  // can position branches and join them visually.
  parallelId?: string;
}

export interface GraphEdge {
  id: string;
  source: string;
  target: string;
}
