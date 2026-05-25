import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useParams, Link } from "react-router-dom";
import ReactFlow, {
  Background,
  Connection,
  Controls,
  Edge,
  Node,
  NodeChange,
  NodeProps,
  ReactFlowProvider,
  Handle,
  Position,
  useEdgesState,
  useNodesState,
  useReactFlow,
  addEdge,
} from "reactflow";
import "reactflow/dist/style.css";
import Editor from "@monaco-editor/react";
import { defineJigsawTheme, JIGSAW_THEME } from "../lib/monacoTheme";
import yaml from "js-yaml";
import { api, Diagnostic, FlowSummary, LogicHandler, TaskSummary, JSONSchema } from "../api/client";
import { autoLayout, Canvas, CanvasEdge, CanvasNode, computeBranchPaths, decompile, layoutKey, safeCompile } from "../graph/dag";
import { Flow, FlowFile, TaskRef, Bind } from "../graph/types";
import { validateCanvas, ValidationResult } from "../graph/validate";
import { useUnsavedGuard } from "../hooks/useUnsavedGuard";
import { ConfirmModal } from "../components/ConfirmModal";
import { DraftEntry, deleteDraft, listDrafts, renameDraft, saveDraft } from "../lib/drafts";
import { useConfirmDialog } from "../components/useDialog";

// Graph editor — DAG-native, with:
//   • lookup of the flow's file by name (filenames may differ from flow names)
//   • layout stored in a sidecar file, never in the YAML
//   • right-click context menus on nodes, edges, and the canvas
//   • visible start/end markers (the source node and sinks)
//   • editable inspector for TaskRef overrides + task info
//   • bidirectional YAML side panel

interface UndoSnapshot {
  nodes: CanvasNode[];
  edges: CanvasEdge[];
  // overrides per node id; preserved across edits.
  overrides: Record<string, TaskRef["overrides"]>;
  // bindings per node id; input/output wiring for each task
  bindings: Record<string, Bind | undefined>;
}

const NODE_TYPES = {
  task: TaskNode,
};

export default function FlowGraph() {
  return (
    <ReactFlowProvider>
      <FlowGraphInner />
    </ReactFlowProvider>
  );
}

interface NodeData {
  taskName: string;
  // Per-placement label (TaskRef.label). Distinct labels let the same
  // task be placed multiple times in one flow.
  label?: string;
  // Full chain of enclosing parallel-branch labels, outermost first.
  // Rendered as a single dotted path chip on the node.
  branchPath?: string[];
  // Inner logic this task wraps (taken from params.inner). When set, the
  // node renders as a container around a smaller chip naming the inner
  // logic. Read-only signal — editing happens in the side panel.
  wrapsLogic?: string;
  selected: boolean;
  isStart: boolean;
  isEnd: boolean;
  hasError?: boolean;
  hasWarning?: boolean;
  // Validation messages attached to this node, surfaced as a hover tooltip
  // on the error/warning chip in the node renderer.
  issues?: { severity: "error" | "warning"; message: string }[];
}

interface CtxMenu {
  x: number;
  y: number;
  target:
    | { kind: "node"; id: string }
    | { kind: "edge"; id: string }
    | { kind: "pane" };
}

function FlowGraphInner() {
  const { name } = useParams();
  const [flow, setFlow] = useState<Flow | null>(null);
  const [filePath, setFilePath] = useState<string>("");
  const [error, setError] = useState<string | null>(null);
  const [diags, setDiags] = useState<Diagnostic[]>([]);
  const [busy, setBusy] = useState(false);
  const [flash, setFlash] = useState<string | null>(null);
  const [palette, setPalette] = useState<TaskSummary[]>([]);
  const [logicHandlers, setLogicHandlers] = useState<LogicHandler[]>([]);
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [flowList, setFlowList] = useState<FlowSummary[]>([]);
  const [flowPickerOpen, setFlowPickerOpen] = useState(false);
  const [draftsOpen, setDraftsOpen] = useState(false);
  const [draftSaveOpen, setDraftSaveOpen] = useState(false);
  const [drafts, setDrafts] = useState<DraftEntry[]>([]);
  const refreshDrafts = useCallback(() => {
    if (name) setDrafts(listDrafts("flow", name));
  }, [name]);
  useEffect(() => { refreshDrafts(); }, [refreshDrafts]);
  const [yamlOpen, setYamlOpen] = useState(false);
  const [yamlDraft, setYamlDraft] = useState("");
  const [yamlError, setYamlError] = useState<string | null>(null);
  // YAML editor is read-only by default. Unlock to author YAML directly;
  // while unlocked, the graph → YAML auto-mirror is suspended so user keystrokes
  // aren't clobbered. Pressing "Apply" pushes YAML into the graph (and re-locks).
  const [yamlUnlocked, setYamlUnlocked] = useState(false);

  // Issues modal — show validation list on demand, not always in the banner.
  const [issuesOpen, setIssuesOpen] = useState(false);
  const [selectedNode, setSelectedNode] = useState<string | null>(null);
  const [selectedEdge, setSelectedEdge] = useState<string | null>(null);
  const [ctxMenu, setCtxMenu] = useState<CtxMenu | null>(null);

  // Per-node TaskRef overrides, keyed by canvas node id. Compiled into the
  // emitted YAML on save.
  const [overrides, setOverrides] = useState<Record<string, TaskRef["overrides"]>>({});
  
  // Per-node input/output bindings, keyed by canvas node id. Compiled into the
  // emitted YAML on save.
  const [bindings, setBindings] = useState<Record<string, Bind | undefined>>({});

  // Per-node TaskRef.params overrides, keyed by canvas node id. Currently used
  // read-only to render flow-level wrapper info (params.inner). Saving these
  // back into YAML is not yet wired; edits would need to be applied during
  // save and undo/redo snapshotting alongside `bindings`.
  const [refParams, setRefParams] = useState<Record<string, Record<string, unknown> | undefined>>({});

  // Per-parallel-block configuration, keyed by the branchPath prefix that
  // *contains* this fork. The outermost fork is keyed "", a fork nested
  // inside branch "alpha" is keyed "alpha", and so on. Round-trip happens via
  // collectParallelConfigs (load) and applyParallelConfigs (save / YAML mirror).
  const [parallelConfigs, setParallelConfigs] = useState<Record<string, ParallelConfig>>({});

  const [nodes, setNodes, onNodesChange] = useNodesState<NodeData>([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState([]);
  const { screenToFlowPosition } = useReactFlow();

  const [history, setHistory] = useState<UndoSnapshot[]>([]);
  const [future, setFuture] = useState<UndoSnapshot[]>([]);

  const dirty = history.length > 0;
  const blocker = useUnsavedGuard(dirty);
  // Discard-edits confirm for the YAML-relock case (separate from navigation).
  const [yamlDiscardConfirm, setYamlDiscardConfirm] = useState(false);

  // Width of the inspector/YAML side panel, in px. Persisted across sessions.
  const [rightWidth, setRightWidth] = useState<number>(() => {
    const v = Number(localStorage.getItem("flowRightWidth"));
    return Number.isFinite(v) && v >= 240 ? v : 400;
  });
  useEffect(() => {
    localStorage.setItem("flowRightWidth", String(rightWidth));
  }, [rightWidth]);
  const resizing = useRef(false);
  useEffect(() => {
    function onMove(e: MouseEvent) {
      if (!resizing.current) return;
      // Distance from the right edge of the viewport — the panel grows to the right.
      const next = window.innerWidth - e.clientX - 40; // account for .main padding
      const clamped = Math.min(900, Math.max(240, next));
      setRightWidth(clamped);
    }
    function onUp() {
      if (!resizing.current) return;
      resizing.current = false;
      document.body.style.cursor = "";
      document.body.style.userSelect = "";
    }
    window.addEventListener("mousemove", onMove);
    window.addEventListener("mouseup", onUp);
    return () => {
      window.removeEventListener("mousemove", onMove);
      window.removeEventListener("mouseup", onUp);
    };
  }, []);
  function startResize(e: React.MouseEvent) {
    e.preventDefault();
    resizing.current = true;
    document.body.style.cursor = "col-resize";
    document.body.style.userSelect = "none";
  }

  // -------- load -----------------------------------------------------------

  useEffect(() => {
    if (!name) return;
    setError(null);

    let cancelled = false;
    (async () => {
      try {
        const { path } = await api.flowLocation(name);
        if (cancelled) return;
        const raw = await api.file(path);
        if (cancelled) return;
        const doc = yaml.load(raw) as FlowFile;
        const f = doc?.flows?.find((x) => x.name === name);
        if (!f) throw new Error(`no flow named "${name}" in ${path}`);

        // Positions live in the .jigsaw/layouts/<flow>.json sidecar so the
        // flow YAML stays clean. Fall back to any legacy metadata.layout
        // embedded in the YAML (older saves) until the next save migrates it.
        const sidecar = await api.loadLayout(name).catch(() => ({}));
        const layout = Object.keys(sidecar).length > 0
          ? sidecar
          : readEmbeddedLayout(f);
        if (cancelled) return;
        const canvas = decompile(f, layout);
        const nodeOverrides = collectOverrides(canvas, f);
        const nodeBindings = collectBindings(canvas, f);
        const nodeRefParams = collectRefParams(canvas, f);

        setFilePath(path);
        setFlow(stripLayoutFromFlow(f)); // canonical state holds no layout; save() re-embeds it
        setOverrides(nodeOverrides);
        setBindings(nodeBindings);
        setRefParams(nodeRefParams);
        setParallelConfigs(collectParallelConfigs(f.tasks));
        setNodes(canvasToRFNodes(canvas));
        setEdges(canvasToRFEdges(canvas));
        setHistory([]);
        setFuture([]);
      } catch (e) {
        setError((e as Error).message);
      }
    })();

    api.tasks().then(setPalette).catch(() => {});
    api.logic().then((r) => setLogicHandlers(r.handlers)).catch(() => {});
    api.flows().then(setFlowList).catch(() => {});

    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [name]);

  // -------- derived: start/end markers + selection styling -----------------

  const startEnd = useMemo(() => {
    const inDeg = new Map<string, number>();
    const outDeg = new Map<string, number>();
    for (const n of nodes) {
      inDeg.set(n.id, 0);
      outDeg.set(n.id, 0);
    }
    for (const e of edges) {
      outDeg.set(e.source, (outDeg.get(e.source) ?? 0) + 1);
      inDeg.set(e.target, (inDeg.get(e.target) ?? 0) + 1);
    }
    const starts = new Set<string>();
    const ends = new Set<string>();
    for (const n of nodes) {
      if ((inDeg.get(n.id) ?? 0) === 0) starts.add(n.id);
      if ((outDeg.get(n.id) ?? 0) === 0) ends.add(n.id);
    }
    return { starts, ends };
  }, [nodes, edges]);

  // Live structural validation. Memoized on the canvas-derived inputs so it
  // runs whenever the user changes anything, but not on cosmetic re-renders.
  const validation: ValidationResult = useMemo(
    () => validateCanvas(rfToCanvas(nodes, edges), palette),
    [nodes, edges, palette],
  );

  // Build TaskSchemaMap from palette + logic handlers for scope computation.
  const taskSchemas: TaskSchemaMap = useMemo(() => {
    const map: TaskSchemaMap = {};
    for (const t of palette) {
      if (!t.logic) continue;
      const handler = logicHandlers.find((h) => h.name === t.logic);
      if (!handler) continue;
      map[t.name] = {
        logic: handler.name,
        inputs: flattenSchemaProps(handler.input_schema) ?? [],
        outputs: flattenSchemaProps(handler.output_schema) ?? [],
      };
    }
    return map;
  }, [palette, logicHandlers]);

  // Live task tree with current bindings applied — used for scope computation.
  const liveFlowTasks: TaskRef[] = useMemo(() => {
    if (!flow) return [];
    const canvas = rfToCanvas(nodes, edges);
    return applyBindings(flow.tasks, canvas, bindings);
  }, [flow, nodes, edges, bindings]);

  // Map of node id → validation issues, used both to paint the canvas and to
  // surface the messages in a tooltip when the user hovers the node's chip.
  const issuesByNode = useMemo(() => {
    const map: Record<string, { severity: "error" | "warning"; message: string }[]> = {};
    for (const issue of validation.issues) {
      if (!issue.nodeIds) continue;
      for (const id of issue.nodeIds) {
        if (!map[id]) map[id] = [];
        map[id].push({ severity: issue.severity, message: issue.message });
      }
    }
    return map;
  }, [validation]);

  // Look up wraps_logic by task name from the palette so the renderer can
  // draw wrapper tasks as containers around the inner logic chip.
  const wrapsByTaskName = useMemo(() => {
    const map: Record<string, string> = {};
    for (const t of palette) {
      if (t.wraps_logic) map[t.name] = t.wraps_logic;
    }
    return map;
  }, [palette]);

  // Reapply visual flags to nodes/edges every render.
  const styledNodes = useMemo(
    () =>
      nodes.map((n) => {
        const d = n.data as NodeData;
        return {
          ...n,
          data: {
            ...d,
            selected: n.id === selectedNode,
            isStart: startEnd.starts.has(n.id),
            isEnd: startEnd.ends.has(n.id),
            hasError: validation.problemNodes.has(n.id),
            hasWarning: validation.warnNodes.has(n.id) && !validation.problemNodes.has(n.id),
            issues: issuesByNode[n.id],
            wrapsLogic:
              (typeof refParams[n.id]?.inner === "string"
                ? (refParams[n.id]!.inner as string)
                : undefined) ?? wrapsByTaskName[d.taskName],
          },
        };
      }),
    [nodes, selectedNode, startEnd, validation, issuesByNode, wrapsByTaskName, refParams],
  );
  const styledEdges = useMemo(
    () =>
      edges.map((e) => ({
        ...e,
        style:
          e.id === selectedEdge
            ? { stroke: "#f08383", strokeWidth: 3 }
            : { stroke: "#7cf0c7", strokeWidth: 2 },
      })),
    [edges, selectedEdge],
  );

  // -------- undo / commit helpers ------------------------------------------

  const snapshot = useCallback((): UndoSnapshot => {
    return {
      nodes: nodes.map((n) => ({
        id: n.id,
        taskName: (n.data as NodeData).taskName,
        label: (n.data as NodeData).label,
        branchPath: (n.data as NodeData).branchPath,
        position: { ...n.position },
      })),
      edges: edges.map((e) => ({ id: e.id, source: e.source, target: e.target })),
      overrides: { ...overrides },
      bindings: { ...bindings },
    };
  }, [nodes, edges, overrides, bindings]);

  const commit = useCallback(
    (mutate: () => void) => {
      setHistory((h) => [...h, snapshot()]);
      setFuture([]);
      mutate();
    },
    [snapshot],
  );

  // Wrap React Flow's onNodesChange so a finished drag pushes an undo entry —
  // otherwise dragging nodes leaves the editor "clean" and Save stays disabled
  // even though the layout has visibly changed. Selection/dimension changes
  // don't count; only position changes whose `dragging` flag has gone false.
  const handleNodesChange = useCallback(
    (changes: NodeChange[]) => {
      const dragEnded = changes.some(
        (c) => c.type === "position" && c.dragging === false,
      );
      if (dragEnded) {
        setHistory((h) => [...h, snapshot()]);
        setFuture([]);
      }
      onNodesChange(changes);
    },
    [onNodesChange, snapshot],
  );

  // -------- YAML mirror ----------------------------------------------------
  // Auto-sync graph → YAML only when the YAML side is locked. While the user
  // has it unlocked, they're authoring there and we leave the buffer alone
  // until they click Apply (or re-lock without applying, which discards).

  useEffect(() => {
    if (!flow || yamlUnlocked) return;
    const canvas = rfToCanvas(nodes, edges);
    const compiled = safeCompile(canvas);
    if (!compiled.ok) return;
    const applied = applyOverrides(compiled.tasks, canvas, overrides);
    const withBindings = applyBindings(applied, canvas, bindings);
    const withParallel = applyParallelConfigs(withBindings, parallelConfigs);
    const merged: Flow = { ...flow, tasks: withParallel };
    setYamlDraft(yaml.dump({ flows: [merged] }, { lineWidth: 100, noRefs: true }));
  }, [nodes, edges, flow, yamlUnlocked, overrides, bindings, parallelConfigs]);

  // -------- operations -----------------------------------------------------

  function addNodeFromPalette(taskName: string, at?: { x: number; y: number }) {
    const id = `t_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 7)}`;
    const newNode: Node<NodeData> = {
      id,
      type: "task",
      position: at ?? dropPositionFor(nodes),
      data: { taskName, selected: false, isStart: false, isEnd: false },
    };
    commit(() => setNodes((cur) => [...cur, newNode]));
  }

  function onConnect(c: Connection) {
    if (!c.source || !c.target || c.source === c.target) return;
    // Prevent duplicate edges.
    if (edges.some((e) => e.source === c.source && e.target === c.target)) return;
    commit(() =>
      setEdges((cur) =>
        addEdge({ ...c, type: "smoothstep", style: { stroke: "#7cf0c7", strokeWidth: 2 } }, cur),
      ),
    );
  }

  function deleteNode(id: string) {
    commit(() => {
      setNodes((cur) => cur.filter((n) => n.id !== id));
      setEdges((cur) => cur.filter((e) => e.source !== id && e.target !== id));
      setOverrides((cur) => {
        const next = { ...cur };
        delete next[id];
        return next;
      });
      setBindings((cur) => {
        const next = { ...cur };
        delete next[id];
        return next;
      });
    });
    if (selectedNode === id) setSelectedNode(null);
  }

  function deleteEdge(id: string) {
    commit(() => setEdges((cur) => cur.filter((e) => e.id !== id)));
    if (selectedEdge === id) setSelectedEdge(null);
  }

  function deleteSelected() {
    if (selectedNode) deleteNode(selectedNode);
    else if (selectedEdge) deleteEdge(selectedEdge);
  }

  function autoArrange() {
    const canvas = rfToCanvas(nodes, edges);
    autoLayout(canvas);
    commit(() => setNodes(canvasToRFNodes(canvas)));
  }

  // Update each node's branch labels/tags in place — without recreating
  // nodes/edges or moving anything. Re-walks the current graph topology to
  // figure out which parallel branch each node belongs to, then patches only
  // `data.branchPath` on the existing nodes. Use when chips/tags drift after
  // structural edits (e.g. a node added inside a branch didn't inherit its
  // branchPath, or a rename needs to cascade).
  function refreshGraph() {
    const canvas = rfToCanvas(nodes, edges);
    const paths = computeBranchPaths(canvas);
    if (!paths) {
      setFlash("Can't refresh tags — graph has structural errors. Fix them first.");
      return;
    }
    setNodes((cur) =>
      cur.map((n) => {
        const path = paths.get(n.id) ?? [];
        const next = path.length > 0 ? path : undefined;
        const prev = (n.data as NodeData).branchPath;
        const same = JSON.stringify(prev ?? null) === JSON.stringify(next ?? null);
        if (same) return n;
        return { ...n, data: { ...(n.data as NodeData), branchPath: next } };
      }),
    );
    setFlash("Tags refreshed.");
  }

  function setNodeOverrides(nodeId: string, next: TaskRef["overrides"]) {
    commit(() =>
      setOverrides((cur) => ({
        ...cur,
        [nodeId]: next,
      })),
    );
  }

  function setNodeBindings(nodeId: string, next: Bind | undefined) {
    commit(() =>
      setBindings((cur) => ({
        ...cur,
        [nodeId]: next,
      })),
    );
  }

  // Update the per-placement label on a node. Empty string clears the label.
  function setNodeLabel(nodeId: string, label: string) {
    commit(() =>
      setNodes((cur) =>
        cur.map((n) =>
          n.id === nodeId
            ? {
                ...n,
                data: {
                  ...(n.data as NodeData),
                  label: label.trim() ? label.trim() : undefined,
                },
              }
            : n,
        ),
      ),
    );
  }

  // Rename one segment of a parallel-branch path across every node that
  // shares the exact same prefix up to (and including) that segment. We
  // match on the prefix rather than just the segment name so renaming
  // "branch_1" inside an outer "alpha" doesn't accidentally also rename a
  // sibling "branch_1" inside an outer "beta".
  function renameBranchPathSegment(prefix: string[], newLabel: string) {
    const next = newLabel.trim();
    if (!next || next === prefix[prefix.length - 1]) return;
    commit(() =>
      setNodes((cur) =>
        cur.map((n) => {
          const path = (n.data as NodeData).branchPath;
          if (!path || path.length < prefix.length) return n;
          for (let i = 0; i < prefix.length; i++) {
            if (path[i] !== prefix[i]) return n;
          }
          const renamed = [...path];
          renamed[prefix.length - 1] = next;
          return {
            ...n,
            data: { ...(n.data as NodeData), branchPath: renamed },
          };
        }),
      ),
    );
  }

  // Insert a "boilerplate" linear chain on the canvas.
  function insertBoilerplate(at: { x: number; y: number }) {
    if (palette.length < 2) {
      // fall back: just open the palette
      setPaletteOpen(true);
      return;
    }
    const a = palette[0].name;
    const b = palette[1].name;
    const idA = `t_${Date.now().toString(36)}_a`;
    const idB = `t_${Date.now().toString(36)}_b`;
    const nodeA: Node<NodeData> = {
      id: idA,
      type: "task",
      position: at,
      data: { taskName: a, selected: false, isStart: false, isEnd: false },
    };
    const nodeB: Node<NodeData> = {
      id: idB,
      type: "task",
      position: { x: at.x, y: at.y + 110 },
      data: { taskName: b, selected: false, isStart: false, isEnd: false },
    };
    commit(() => {
      setNodes((cur) => [...cur, nodeA, nodeB]);
      setEdges((cur) =>
        addEdge(
          {
            source: idA,
            target: idB,
            sourceHandle: null,
            targetHandle: null,
          },
          cur,
        ),
      );
    });
  }

  // Load another flow and splice its tasks into the current canvas at `at`
  // (graph coords). Every inserted node gets a fresh id so the template can be
  // dropped multiple times. Original layout is preserved relative to its top-left.
  async function insertFlowAsTemplate(srcName: string, at: { x: number; y: number }) {
    try {
      const { path } = await api.flowLocation(srcName);
      const raw = await api.file(path);
      const doc = yaml.load(raw) as FlowFile;
      const src = doc?.flows?.find((x) => x.name === srcName);
      if (!src) throw new Error(`flow "${srcName}" not found in ${path}`);

      const embeddedSrc = readEmbeddedLayout(src);
      const srcLayout = Object.keys(embeddedSrc).length > 0
        ? embeddedSrc
        : await api.loadLayout(srcName).catch(() => ({}));
      spliceFlowIntoCanvas(src, srcLayout, at);
    } catch (e) {
      setError(`insert template failed: ${(e as Error).message}`);
    }
  }

  // Insert a locally-saved template (Playground's "Save as template") at
  // the cursor. The template carries no layout, so we auto-layout it
  // before placement.
  function insertLocalTemplate(templateYAML: string, at: { x: number; y: number }) {
    try {
      const doc = yaml.load(templateYAML) as FlowFile;
      const src = doc?.flows?.[0];
      if (!src) throw new Error("template has no flows[] entry");
      spliceFlowIntoCanvas(src, {}, at);
    } catch (e) {
      setError(`insert template failed: ${(e as Error).message}`);
    }
  }

  // Shared template-splicing helper. Decompiles `src`, applies optional
  // layout, re-ids every node (so the same template can be dropped many
  // times), and offsets positions so the top-left lands at `at`.
  function spliceFlowIntoCanvas(
    src: Flow,
    srcLayout: Record<string, { x: number; y: number }>,
    at: { x: number; y: number },
  ) {
    const tmpl = decompile(src, srcLayout);
    if (tmpl.nodes.length === 0) return;
    if (Object.keys(srcLayout || {}).length === 0) autoLayout(tmpl);

    const minX = Math.min(...tmpl.nodes.map((n) => n.position.x));
    const minY = Math.min(...tmpl.nodes.map((n) => n.position.y));
    const idMap = new Map<string, string>();
    const stamp = Date.now().toString(36);
    tmpl.nodes.forEach((n, i) => {
      idMap.set(n.id, `t_${stamp}_${i}_${Math.random().toString(36).slice(2, 5)}`);
    });

    const newNodes: Node<NodeData>[] = tmpl.nodes.map((n) => ({
      id: idMap.get(n.id)!,
      type: "task",
      position: { x: at.x + (n.position.x - minX), y: at.y + (n.position.y - minY) },
      data: {
        taskName: n.taskName,
        label: n.label,
        branchPath: n.branchPath,
        selected: false,
        isStart: false,
        isEnd: false,
      },
    }));
    const newEdges: Edge[] = tmpl.edges.map((e) => ({
      id: `e_${stamp}_${idMap.get(e.source)}_${idMap.get(e.target)}`,
      source: idMap.get(e.source)!,
      target: idMap.get(e.target)!,
      type: "smoothstep",
      style: { stroke: "#7cf0c7", strokeWidth: 2 },
    }));

    commit(() => {
      setNodes((cur) => [...cur, ...newNodes]);
      setEdges((cur) => [...cur, ...newEdges]);
    });
  }

  // -------- keyboard -------------------------------------------------------

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement;
      if (t && (t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.closest(".monaco-editor"))) return;
      const meta = e.metaKey || e.ctrlKey;
      if (meta && e.key.toLowerCase() === "z" && !e.shiftKey) {
        e.preventDefault();
        undo();
      } else if (meta && ((e.key.toLowerCase() === "z" && e.shiftKey) || e.key.toLowerCase() === "y")) {
        e.preventDefault();
        redo();
      } else if (e.key === "Delete" || e.key === "Backspace") {
        if (selectedNode || selectedEdge) {
          e.preventDefault();
          deleteSelected();
        }
      } else if (e.key === "Escape") {
        setCtxMenu(null);
        setSelectedNode(null);
        setSelectedEdge(null);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedNode, selectedEdge, nodes, edges, overrides]);

  function undo() {
    if (history.length === 0) return;
    const prev = history[history.length - 1];
    setFuture((f) => [snapshot(), ...f]);
    setHistory((h) => h.slice(0, -1));
    applySnapshot(prev);
  }

  function redo() {
    if (future.length === 0) return;
    const next = future[0];
    setHistory((h) => [...h, snapshot()]);
    setFuture((f) => f.slice(1));
    applySnapshot(next);
  }

  function applySnapshot(snap: UndoSnapshot) {
    setNodes(
      snap.nodes.map((n) => ({
        id: n.id,
        type: "task",
        position: n.position,
        data: {
          taskName: n.taskName,
          label: n.label,
          branchPath: n.branchPath,
          selected: false,
          isStart: false,
          isEnd: false,
        },
      })),
    );
    setEdges(
      snap.edges.map((e) => ({
        id: e.id,
        source: e.source,
        target: e.target,
        type: "smoothstep",
        style: { stroke: "#7cf0c7", strokeWidth: 2 },
      })),
    );
    setOverrides(snap.overrides);
    setBindings(snap.bindings);
    setSelectedNode(null);
    setSelectedEdge(null);
  }

  // Serialize the current canvas (including embedded layout) to a YAML doc
  // identical to what save() would write — used by the draft system so a
  // reloaded draft restores the exact state.
  function currentDocYaml(): string | null {
    if (!flow) return null;
    const canvas = rfToCanvas(nodes, edges);
    const compiled = safeCompile(canvas);
    if (!compiled.ok) return null;
    const applied = applyOverrides(compiled.tasks, canvas, overrides);
    const withBindings = applyBindings(applied, canvas, bindings);
    const withParallel = applyParallelConfigs(withBindings, parallelConfigs);
    const layout: Record<string, { x: number; y: number }> = {};
    for (const n of canvas.nodes) {
      layout[layoutKey(n)] = n.position;
    }
    const merged: Flow = {
      ...flow,
      tasks: withParallel,
      metadata: { ...(flow.metadata ?? {}), layout },
    };
    return yaml.dump({ flows: [merged] }, { lineWidth: 100, noRefs: true });
  }

  function saveAsDraft(label: string) {
    if (!name) return;
    const text = currentDocYaml();
    if (text === null) {
      setDiags([{ Severity: "error", File: "", Message: "current graph can't be compiled — fix errors before saving a draft" }]);
      return;
    }
    saveDraft("flow", name, label, text);
    refreshDrafts();
    setFlash("Draft saved locally.");
  }

  function loadDraft(d: DraftEntry) {
    try {
      const doc = yaml.load(d.yaml) as FlowFile;
      const f = doc?.flows?.find((x) => x.name === flow?.name) ?? doc?.flows?.[0];
      if (!f) throw new Error("draft has no flows[] entry");
      commit(() => {
        const embedded = readEmbeddedLayout(f);
        const canvas = decompile(stripLayoutFromFlow(f), embedded);
        const nodeOverrides = collectOverrides(canvas, f);
        const nodeBindings = collectBindings(canvas, f);
        const nodeRefParams = collectRefParams(canvas, f);
        setFlow(stripLayoutFromFlow(f));
        setNodes(canvasToRFNodes(canvas));
        setEdges(canvasToRFEdges(canvas));
        setOverrides(nodeOverrides);
        setBindings(nodeBindings);
        setRefParams(nodeRefParams);
      });
      setFlash(`Loaded draft "${d.label}". Not saved to disk yet.`);
    } catch (e) {
      setDiags([{ Severity: "error", File: "", Message: `load draft: ${(e as Error).message}` }]);
    }
  }

  function applyYAML() {
    try {
      const doc = yaml.load(yamlDraft) as FlowFile;
      const f = doc?.flows?.find((x) => x.name === flow?.name) ?? doc?.flows?.[0];
      if (!f) throw new Error("YAML doesn't contain a flows[] entry");
      setYamlError(null);
      commit(() => {
        const canvas = decompile(stripLayoutFromFlow(f));
        const nodeOverrides = collectOverrides(canvas, f);
        const nodeBindings = collectBindings(canvas, f);
        const nodeRefParams = collectRefParams(canvas, f);
        setFlow(stripLayoutFromFlow(f));
        setNodes(canvasToRFNodes(canvas));
        setEdges(canvasToRFEdges(canvas));
        setOverrides(nodeOverrides);
        setBindings(nodeBindings);
        setRefParams(nodeRefParams);
      });
    } catch (e) {
      setYamlError((e as Error).message);
    }
  }

  async function save() {
    if (!flow) return;
    setBusy(true);
    setDiags([]);
    setFlash(null);
    try {
      const canvas = rfToCanvas(nodes, edges);
      const compiled = safeCompile(canvas);
      if (!compiled.ok) {
        setDiags([{ Severity: "error", File: filePath, Message: compiled.error }]);
        return;
      }
      const applied = applyOverrides(compiled.tasks, canvas, overrides);
      const withBindings = applyBindings(applied, canvas, bindings);
      const withParallel = applyParallelConfigs(withBindings, parallelConfigs);
      // Build the layout map once: keyed by taskName + label so a task
      // placed multiple times in the flow keeps distinct positions.
      const layout: Record<string, { x: number; y: number }> = {};
      for (const n of canvas.nodes) {
        layout[layoutKey(n)] = n.position;
      }
      // Layout lives in a sidecar (.jigsaw/layouts/<flow>.json), not the YAML.
      // Strip any layout that older saves embedded in metadata so the YAML
      // gets cleaned up the next time the user saves.
      const merged: Flow = { ...stripLayoutFromFlow(flow), tasks: withParallel };
      // Read the original file and replace just our flow entry, preserving
      // any other flows that may live in the same file.
      const raw = await api.file(filePath);
      const doc = (yaml.load(raw) as FlowFile) ?? { flows: [] };
      const idx = doc.flows.findIndex((x) => x.name === flow.name);
      if (idx >= 0) doc.flows[idx] = merged;
      else doc.flows.push(merged);
      const text = yaml.dump(doc, { lineWidth: 100, noRefs: true });
      const { status, data } = await api.saveFiles({ [filePath]: text });
      if (status !== 200 || !data.ok) {
        setDiags(data.diagnostics ?? [{ Severity: "error", File: filePath, Message: "save failed" }]);
        return;
      }
      // Persist positions in the sidecar — the engine never reads this file,
      // so node-drag churn stays out of the YAML history.
      await api.saveLayout(name!, layout).catch(() => {});

      setHistory([]);
      setFuture([]);
      setFlash("Saved.");
      if (data.diagnostics && data.diagnostics.length > 0) setDiags(data.diagnostics);
    } catch (e) {
      setDiags([{ Severity: "error", File: "", Message: (e as Error).message }]);
    } finally {
      setBusy(false);
    }
  }

  // -------- render ---------------------------------------------------------

  if (error) {
    return (
      <>
        <h1>Flow: {name}</h1>
        <div className="diag error">{error}</div>
        <p className="meta">
          The graph editor couldn't locate this flow. Check that a flow named
          "{name}" exists in some file under <code>flows/</code>, or use the{" "}
          <Link to="/editor">raw editor</Link> to inspect.
        </p>
      </>
    );
  }
  if (!flow) return <div className="loading">Loading…</div>;

  const selectedNodeData = selectedNode
    ? (nodes.find((n) => n.id === selectedNode)?.data as NodeData | undefined)
    : null;
  const selectedTask = selectedNodeData ? palette.find((t) => t.name === selectedNodeData.taskName) : undefined;
  const showRight = yamlOpen || !!selectedNode;

  return (
    <div className="flow-page">
      <div style={{ display: "flex", alignItems: "center", gap: 12, marginBottom: 16, flexWrap: "wrap" }}>
        <h1 style={{ margin: 0 }}>Flow: {flow.name}</h1>
        {dirty && <span className="badge warn">unsaved</span>}
        <span style={{ marginLeft: "auto", display: "flex", gap: 8, flexWrap: "wrap" }}>
          <button onClick={() => setPaletteOpen(true)} className="btn">+ Task</button>
          <button onClick={autoArrange} className="btn" title="Re-layout the graph">Auto-arrange</button>
          <button
            onClick={refreshGraph}
            className="btn"
            title="Re-derive the graph from current state — normalizes branch labels and refreshes derived data without moving nodes"
          >
            Refresh
          </button>
          <button onClick={deleteSelected} disabled={!selectedNode && !selectedEdge} className="btn">Delete</button>
          <button onClick={undo} disabled={history.length === 0} className="btn">Undo</button>
          <button onClick={redo} disabled={future.length === 0} className="btn">Redo</button>
          <IssuesBadge
            result={validation}
            onOpen={() => setIssuesOpen(true)}
          />
          <button onClick={() => setYamlOpen((v) => !v)} className={`btn ${yamlOpen ? "btn-primary" : ""}`}>
            {yamlOpen ? "Hide YAML" : "Show YAML"}
          </button>
          <button
            onClick={() => setDraftSaveOpen(true)}
            className="btn"
            disabled={validation.hasErrors}
            title={validation.hasErrors ? "Fix errors before drafting" : "Save the current graph locally (browser only)"}
          >
            Save draft
          </button>
          <button onClick={() => setDraftsOpen(true)} className="btn" title="Browse local drafts for this flow">
            Drafts{drafts.length > 0 ? ` (${drafts.length})` : ""}
          </button>
          <button
            onClick={save}
            disabled={!dirty || busy || validation.hasErrors}
            className="btn btn-primary"
            title={validation.hasErrors ? "Fix the errors below before saving" : undefined}
          >
            {busy ? "Saving…" : "Save"}
          </button>
        </span>
      </div>

      <div className="meta" style={{ marginBottom: 12 }}>
        Drag a node's bottom port (●) to another node's top port to connect them.
        Right-click anywhere for actions. Esc closes menus and clears selection.
      </div>

      {flash && <div className="diag" style={{ borderLeftColor: "var(--accent)" }}>{flash}</div>}
      {/* server-side diagnostics from the last Save still surface inline so
          the user sees them next to the Save button. They're rare and explicit. */}
      {diags.length > 0 && (
        <div style={{ marginBottom: 16 }}>
          {diags.map((d, i) => (
            <div key={i} className={`diag ${d.Severity}`}>
              <span className="badge" style={{ marginLeft: 0, marginRight: 8 }}>{d.Severity}</span>
              {d.Message}
            </div>
          ))}
        </div>
      )}

      <div
        className="flow-canvas-grid"
        style={{
          display: "grid",
          gridTemplateColumns: showRight ? `1fr 6px ${rightWidth}px` : "1fr",
          gap: showRight ? 0 : 16,
        }}
      >
        <div
          style={{
            border: "1px solid var(--border)",
            borderRadius: 6,
            background: "var(--panel)",
            position: "relative",
          }}
        >
          <ReactFlow
            nodes={styledNodes}
            edges={styledEdges}
            nodeTypes={NODE_TYPES}
            onNodesChange={handleNodesChange}
            onEdgesChange={onEdgesChange}
            onConnect={onConnect}
            onNodeClick={(_, n) => { setSelectedNode(n.id); setSelectedEdge(null); setCtxMenu(null); }}
            onEdgeClick={(_, e) => { setSelectedEdge(e.id); setSelectedNode(null); setCtxMenu(null); }}
            onPaneClick={() => { setSelectedNode(null); setSelectedEdge(null); setCtxMenu(null); }}
            onNodeContextMenu={(e, n) => {
              e.preventDefault();
              setCtxMenu({ x: e.clientX, y: e.clientY, target: { kind: "node", id: n.id } });
            }}
            onEdgeContextMenu={(e, edge) => {
              e.preventDefault();
              setCtxMenu({ x: e.clientX, y: e.clientY, target: { kind: "edge", id: edge.id } });
            }}
            onPaneContextMenu={(e) => {
              e.preventDefault();
              setCtxMenu({ x: e.clientX, y: e.clientY, target: { kind: "pane" } });
            }}
            fitView
            nodesDraggable
            nodesConnectable
            elementsSelectable
            deleteKeyCode={null}
            // Space defaults to pan-activation, which steals the key globally
            // — including from the YAML editor side-panel. Disable it; users
            // pan by drag-on-canvas anyway.
            panActivationKeyCode={null}
            selectionKeyCode={null}
            zoomActivationKeyCode={null}
            multiSelectionKeyCode={null}
            proOptions={{ hideAttribution: true }}
          >
            <Background gap={16} color="#1f2530" />
            <Controls showInteractive={false} />
          </ReactFlow>

          {ctxMenu && (
            <ContextMenu
              menu={ctxMenu}
              onClose={() => setCtxMenu(null)}
              onDeleteNode={(id) => { deleteNode(id); setCtxMenu(null); }}
              onDeleteEdge={(id) => { deleteEdge(id); setCtxMenu(null); }}
              onAddTask={(at) => { setPaletteOpen(true); setCtxMenu(null); _pendingPos.current = screenToFlowPosition(at); }}
              onInsertBoilerplate={(at) => { insertBoilerplate(screenToFlowPosition(at)); setCtxMenu(null); }}
              onInsertFlow={(at) => { setFlowPickerOpen(true); setCtxMenu(null); _pendingPos.current = screenToFlowPosition(at); }}
            />
          )}
        </div>

        {showRight && (
          <div
            onMouseDown={startResize}
            title="Drag to resize"
            style={{
              cursor: "col-resize",
              background: "transparent",
              position: "relative",
              userSelect: "none",
            }}
          >
            <div
              style={{
                position: "absolute",
                top: 0,
                bottom: 0,
                left: 2,
                width: 2,
                background: "var(--border)",
              }}
            />
          </div>
        )}

        {showRight && (
          <aside style={{
            border: "1px solid var(--border)",
            borderRadius: 6,
            background: "var(--panel)",
            padding: 12,
            display: "flex",
            flexDirection: "column",
            gap: 12,
            overflow: "auto",
          }}>
            {selectedNode && selectedNodeData && (
              <Inspector
                nodeId={selectedNode}
                data={selectedNodeData}
                taskInfo={selectedTask}
                overrides={overrides[selectedNode]}
                bindings={bindings[selectedNode]}
                allBranchPaths={nodes.map((n) => (n.data as NodeData).branchPath).filter(Boolean) as string[][]}
                parallelConfigs={parallelConfigs}
                onChangeParallelConfig={(key, next) =>
                  commit(() =>
                    setParallelConfigs((cur) => {
                      const out = { ...cur };
                      if (parallelConfigIsEmpty(next)) delete out[key];
                      else out[key] = next;
                      return out;
                    }),
                  )
                }
                onChangeOverrides={(next) => setNodeOverrides(selectedNode, next)}
                onChangeBindings={(next) => setNodeBindings(selectedNode, next)}
                onChangeLabel={(label) => setNodeLabel(selectedNode, label)}
                onRenameBranchSegment={(prefix, next) => renameBranchPathSegment(prefix, next)}
                onDelete={() => deleteNode(selectedNode)}
                taskSchemas={taskSchemas}
                flowTasks={liveFlowTasks}
                nodeOccurrence={(() => {
                  // Count preceding nodes with the same taskName + branchPath
                  let count = 0;
                  for (const n of nodes) {
                    if (n.id === selectedNode) break;
                    const d = n.data as NodeData;
                    if (
                      d.taskName === selectedNodeData.taskName &&
                      JSON.stringify(d.branchPath ?? null) === JSON.stringify(selectedNodeData.branchPath ?? null)
                    ) count++;
                  }
                  return count;
                })()}
              />
            )}

            {yamlOpen && (
              <>
                <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
                  <strong style={{ flex: 1 }}>YAML {yamlUnlocked ? <span className="badge warn">editing</span> : <span className="badge">locked</span>}</strong>
                  <button
                    className={`btn ${yamlUnlocked ? "" : "btn-primary"}`}
                    onClick={() => {
                      if (yamlUnlocked) {
                        if (!yamlMirrorsGraph(yamlDraft, flow, nodes, edges, overrides, bindings)) {
                          setYamlDiscardConfirm(true);
                          return;
                        }
                        setYamlUnlocked(false);
                        setYamlError(null);
                      } else {
                        setYamlUnlocked(true);
                      }
                    }}
                  >
                    {yamlUnlocked ? "Lock" : "Unlock"}
                  </button>
                  <button
                    className="btn btn-primary"
                    disabled={!yamlUnlocked}
                    onClick={() => { applyYAML(); setYamlUnlocked(false); }}
                    title={yamlUnlocked ? "Apply YAML to the graph" : "Unlock first"}
                  >
                    Apply
                  </button>
                </div>
                {yamlError && <div className="diag error">{yamlError}</div>}
                <div style={{ flex: 1, minHeight: 240, border: "1px solid var(--border)", borderRadius: 4, overflow: "hidden" }}>
                  <Editor
                    height="100%"
                    language="yaml"
                    theme={JIGSAW_THEME}
                    beforeMount={defineJigsawTheme}
                    value={yamlDraft}
                    onChange={(v) => yamlUnlocked && setYamlDraft(v ?? "")}
                    options={{
                      minimap: { enabled: false },
                      fontSize: 12,
                      tabSize: 2,
                      scrollBeyondLastLine: false,
                      automaticLayout: true,
                      readOnly: !yamlUnlocked,
                    }}
                  />
                </div>
                <div className="meta">
                  {yamlUnlocked
                    ? "Editing YAML. Click Apply to push your changes into the graph."
                    : "YAML mirrors the graph. Click Unlock to edit YAML by hand."}
                </div>
              </>
            )}
          </aside>
        )}
      </div>

      {paletteOpen && (
        <TaskPalette
          palette={palette}
          onPick={(taskName) => {
            const at = _pendingPos.current;
            _pendingPos.current = undefined;
            addNodeFromPalette(taskName, at);
            setPaletteOpen(false);
          }}
          onClose={() => { _pendingPos.current = undefined; setPaletteOpen(false); }}
        />
      )}

      {draftSaveOpen && (
        <SaveDraftModal
          flowName={flow.name}
          onSave={(label) => { saveAsDraft(label); setDraftSaveOpen(false); }}
          onClose={() => setDraftSaveOpen(false)}
        />
      )}

      {draftsOpen && (
        <DraftsModal
          drafts={drafts}
          onLoad={(d) => { loadDraft(d); setDraftsOpen(false); }}
          onDelete={(id) => {
            if (!name) return;
            deleteDraft("flow", name, id);
            refreshDrafts();
          }}
          onRename={(id, label) => {
            if (!name) return;
            renameDraft("flow", name, id, label);
            refreshDrafts();
          }}
          onClose={() => setDraftsOpen(false)}
        />
      )}

      {flowPickerOpen && (
        <FlowPicker
          flows={flowList.filter((f) => f.name !== flow.name)}
          localTemplates={listDrafts("playground-template", "default")}
          onPickFlow={(srcName) => {
            const at = _pendingPos.current ?? { x: 0, y: 0 };
            _pendingPos.current = undefined;
            setFlowPickerOpen(false);
            insertFlowAsTemplate(srcName, at);
          }}
          onPickLocal={(tpl) => {
            const at = _pendingPos.current ?? { x: 0, y: 0 };
            _pendingPos.current = undefined;
            setFlowPickerOpen(false);
            insertLocalTemplate(tpl.yaml, at);
          }}
          onClose={() => { _pendingPos.current = undefined; setFlowPickerOpen(false); }}
        />
      )}

      {issuesOpen && (
        <IssuesModal result={validation} onClose={() => setIssuesOpen(false)} />
      )}

      {/* Navigation blocker: user tried to leave with unsaved changes. */}
      {blocker.state === "blocked" && (
        <ConfirmModal
          title="Unsaved changes"
          message={
            <>You have unsaved changes to this flow. Leaving will discard them.</>
          }
          confirmLabel="Discard and leave"
          cancelLabel="Stay on page"
          danger
          onConfirm={() => blocker.proceed?.()}
          onCancel={() => blocker.reset?.()}
        />
      )}

      {/* YAML re-lock confirm: user has unapplied YAML edits. */}
      {yamlDiscardConfirm && (
        <ConfirmModal
          title="Discard YAML edits?"
          message="Re-locking will discard your in-flight YAML changes and re-mirror from the graph."
          confirmLabel="Discard"
          cancelLabel="Keep editing"
          danger
          onConfirm={() => {
            setYamlDiscardConfirm(false);
            setYamlUnlocked(false);
            setYamlError(null);
          }}
          onCancel={() => setYamlDiscardConfirm(false)}
        />
      )}
    </div>
  );
}

// Stash the right-click position so the palette knows where to drop.
// Module-scoped ref-like: transient, doesn't need to trigger renders.
const _pendingPos: { current: { x: number; y: number } | undefined } = { current: undefined };

// ---------------------------------------------------------------------------
// Inspector — TaskRef overrides + linked task info
// ---------------------------------------------------------------------------

function Inspector({
  nodeId,
  data,
  taskInfo,
  overrides,
  bindings,
  allBranchPaths,
  parallelConfigs,
  onChangeParallelConfig,
  onChangeOverrides,
  onChangeBindings,
  onChangeLabel,
  onRenameBranchSegment,
  onDelete,
  taskSchemas,
  flowTasks,
  nodeOccurrence,
}: {
  nodeId: string;
  data: NodeData;
  taskInfo: TaskSummary | undefined;
  overrides: TaskRef["overrides"];
  bindings: Bind | undefined;
  allBranchPaths: string[][];
  parallelConfigs: Record<string, ParallelConfig>;
  onChangeParallelConfig: (key: string, next: ParallelConfig) => void;
  onChangeOverrides: (next: TaskRef["overrides"]) => void;
  onChangeBindings: (next: Bind | undefined) => void;
  onChangeLabel: (label: string) => void;
  onRenameBranchSegment: (prefix: string[], next: string) => void;
  onDelete: () => void;
  taskSchemas: TaskSchemaMap;
  flowTasks: TaskRef[];
  nodeOccurrence: number;
}) {
  return (
    <div>
      <h3 style={{ marginTop: 0 }}>{data.taskName}</h3>
      <div className="meta" style={{ marginBottom: 12 }}>
        {data.isStart && <span className="badge ok">start</span>}{" "}
        {data.isEnd && <span className="badge ok">end</span>}{" "}
        {data.label && <span className="badge">label: {data.label}</span>}{" "}
        <span className="badge">id: {nodeId.slice(0, 10)}</span>
      </div>

      {data.branchPath && data.branchPath.length > 0 && (
        <BranchPathEditor
          path={data.branchPath}
          onRenameSegment={onRenameBranchSegment}
        />
      )}
      {data.branchPath && data.branchPath.length > 0 && (
        <ParallelBlockEditor
          path={data.branchPath}
          allBranchPaths={allBranchPaths}
          parallelConfigs={parallelConfigs}
          onChange={onChangeParallelConfig}
        />
      )}
      <LabelEditor
        label={data.label ?? ""}
        onChange={onChangeLabel}
      />

      {taskInfo ? (
        <TaskParamsEditor taskName={data.taskName} />
      ) : (
        <div className="diag error" style={{ marginBottom: 12 }}>
          No task definition for "{data.taskName}". The flow can't be saved
          until this task exists under <code>tasks/</code>.
        </div>
      )}

      <BindEditor
        taskName={data.taskName}
        bindings={bindings}
        onChange={onChangeBindings}
        taskSchemas={taskSchemas}
        flowTasks={flowTasks}
        targetBranchPath={data.branchPath}
        targetOccurrence={nodeOccurrence}
      />

      <OverridesEditor
        overrides={(overrides ?? []) as OverrideRow[]}
        onChange={(next) => onChangeOverrides(next as TaskRef["overrides"])}
      />

      <button
        className="btn"
        style={{ color: "var(--error)", borderColor: "var(--error)", marginTop: 12 }}
        onClick={onDelete}
      >
        Delete node
      </button>
    </div>
  );
}

// ---------------------------------------------------------------------------
// IssuesBadge — compact toolbar pill that opens a popup with the full list.
// Keeps the graph header clean instead of stacking diagnostics across the top.
// ---------------------------------------------------------------------------

function IssuesBadge({
  result,
  onOpen,
}: {
  result: ValidationResult;
  onOpen: () => void;
}) {
  const errors = result.issues.filter((i) => i.severity === "error").length;
  const warnings = result.issues.filter((i) => i.severity === "warning").length;
  if (errors === 0 && warnings === 0) {
    return <button className="btn" disabled title="No issues">✓ OK</button>;
  }
  const cls = errors > 0 ? "btn btn-issues-err" : "btn btn-issues-warn";
  return (
    <button className={cls} onClick={onOpen}>
      {errors > 0 && <span>⚠ {errors} error{errors === 1 ? "" : "s"}</span>}
      {errors > 0 && warnings > 0 && <span> · </span>}
      {warnings > 0 && <span>{warnings} warning{warnings === 1 ? "" : "s"}</span>}
    </button>
  );
}

function IssuesModal({
  result,
  onClose,
}: {
  result: ValidationResult;
  onClose: () => void;
}) {
  return (
    <div
      onClick={onClose}
      style={{
        position: "fixed", inset: 0, background: "#000a", zIndex: 200,
        display: "flex", alignItems: "flex-start", justifyContent: "center", paddingTop: 100,
      }}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        style={{
          background: "var(--panel)", border: "1px solid var(--border-strong)",
          borderRadius: 6, width: 600, maxHeight: "70vh",
          display: "flex", flexDirection: "column", overflow: "hidden",
        }}
      >
        <div style={{ padding: "12px 16px", borderBottom: "1px solid var(--border)", display: "flex", alignItems: "center" }}>
          <strong style={{ flex: 1 }}>Validation issues</strong>
          <button className="btn" onClick={onClose}>Close</button>
        </div>
        <div style={{ overflow: "auto", padding: 12 }}>
          {result.issues.length === 0 ? (
            <div className="empty">No issues. ✓</div>
          ) : (
            result.issues.map((iss, i) => (
              <div key={i} className={`diag ${iss.severity}`} style={{ marginBottom: 6 }}>
                <span className="badge" style={{ marginLeft: 0, marginRight: 8 }}>{iss.severity}</span>
                {iss.message}
              </div>
            ))
          )}
        </div>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// yamlMirrorsGraph — quick check: does the current YAML draft already match
// what the graph would produce? Used by the Lock toggle to decide whether to
// prompt before discarding YAML edits.
// ---------------------------------------------------------------------------

function yamlMirrorsGraph(
  draft: string,
  flow: Flow | null,
  nodes: Node<NodeData>[],
  edges: Edge[],
  overrides: Record<string, TaskRef["overrides"]>,
  bindings: Record<string, Bind | undefined>,
): boolean {
  if (!flow) return true;
  const canvas = rfToCanvas(nodes, edges);
  const compiled = safeCompile(canvas);
  if (!compiled.ok) return draft.trim() === "";
  const applied = applyOverrides(compiled.tasks, canvas, overrides);
  const withBindings = applyBindings(applied, canvas, bindings);
  const expected = yaml.dump({ flows: [{ ...flow, tasks: withBindings }] }, { lineWidth: 100, noRefs: true });
  return expected === draft;
}

// ---------------------------------------------------------------------------
// TaskParamsEditor — edit label / description / timeout / retry / version on
// the actual Task definition file. Writes through the same /api/files endpoint
// so all validation runs.
// ---------------------------------------------------------------------------

interface EditableTask {
  name: string;
  description: string;
  label: string;
  version: string;
  timeout: number | "";
  retry: number | "";
  logic: string;
  provider: string;
  inherits: string;
  // Catch-all for round-tripping fields we don't expose.
  _raw: Record<string, unknown>;
}

function TaskParamsEditor({ taskName }: { taskName: string }) {
  const [task, setTask] = useState<EditableTask | null>(null);
  const [filePath, setFilePath] = useState<string>("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [flash, setFlash] = useState<string | null>(null);
  const [dirty, setDirty] = useState(false);

  useEffect(() => {
    let cancelled = false;
    setError(null);
    setFlash(null);
    setTask(null);
    setDirty(false);

    (async () => {
      try {
        const loc = await api.taskLocation(taskName);
        if (cancelled) return;
        const raw = await api.file(loc.path);
        if (cancelled) return;
        const doc = yaml.load(raw) as { tasks?: Record<string, unknown>[] };
        const entry = (doc.tasks ?? []).find((t) => (t as { name?: string }).name === taskName);
        if (!entry) throw new Error(`task "${taskName}" not found in ${loc.path}`);
        const t = entry as Record<string, unknown>;
        setFilePath(loc.path);
        setTask({
          name: taskName,
          description: stringField(t.description),
          label: stringField(t.label),
          version: stringField(t.version),
          timeout: numberField(t.timeout),
          retry: numberField(t.retry),
          logic: stringField(t.logic),
          provider: stringField(t.provider),
          inherits: stringField(t.inherits),
          _raw: t,
        });
      } catch (e) {
        if (!cancelled) setError((e as Error).message);
      }
    })();
    return () => { cancelled = true; };
  }, [taskName]);

  function patch<K extends keyof EditableTask>(key: K, value: EditableTask[K]) {
    if (!task) return;
    setTask({ ...task, [key]: value });
    setDirty(true);
    setFlash(null);
  }

  async function save() {
    if (!task) return;
    setBusy(true);
    setError(null);
    setFlash(null);
    try {
      const raw = await api.file(filePath);
      const doc = (yaml.load(raw) as { tasks?: Record<string, unknown>[] }) ?? { tasks: [] };
      const tasks = doc.tasks ?? [];
      const idx = tasks.findIndex((t) => (t as { name?: string }).name === taskName);
      if (idx < 0) throw new Error("task vanished while editing");

      // Merge changes back into the original entry so we don't drop unknown fields.
      const merged = { ...(tasks[idx] as Record<string, unknown>) };
      setIfChanged(merged, "description", task.description);
      setIfChanged(merged, "label", task.label);
      setIfChanged(merged, "version", task.version);
      setIfChanged(merged, "logic", task.logic);
      setIfChanged(merged, "provider", task.provider);
      setIfChanged(merged, "inherits", task.inherits);
      // numeric fields: blank string => delete; number => set
      setNumberOrDelete(merged, "timeout", task.timeout);
      setNumberOrDelete(merged, "retry", task.retry);
      tasks[idx] = merged;
      doc.tasks = tasks;

      const text = yaml.dump(doc, { lineWidth: 100, noRefs: true });
      const { status, data } = await api.saveFiles({ [filePath]: text });
      if (status !== 200 || !data.ok) {
        setError((data.diagnostics ?? []).map((d) => d.Message).join("; ") || "save failed");
        return;
      }
      setDirty(false);
      setFlash("Task saved.");
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }

  if (error && !task) {
    return <div className="diag error">{error}</div>;
  }
  if (!task) return <div className="loading" style={{ padding: 0 }}>Loading task…</div>;

  return (
    <Section
      title="Task parameters"
      storageKey="task-params"
      hint={
        <InfoHint>
          Edits write to <code>{filePath || "tasks/…"}</code>. Inputs/outputs
          are not yet editable here — use the raw editor for those.
        </InfoHint>
      }
      right={
        <button
          className="btn btn-primary"
          onClick={save}
          disabled={!dirty || busy}
          style={{ fontSize: 11, padding: "2px 8px" }}
        >
          {busy ? "Saving…" : "Save"}
        </button>
      }
    >
      {flash && <div className="diag" style={{ borderLeftColor: "var(--accent)", marginBottom: 8 }}>{flash}</div>}
      {error && <div className="diag error" style={{ marginBottom: 8 }}>{error}</div>}

      <div className="form-grid">
        <label className="meta">label</label>
        <input className="input form-input" value={task.label} onChange={(e) => patch("label", e.target.value)} placeholder="(optional flow-local name)" />

        <label className="meta">description</label>
        <input className="input form-input" value={task.description} onChange={(e) => patch("description", e.target.value)} />

        <label className="meta">version</label>
        <input className="input form-input" value={task.version} onChange={(e) => patch("version", e.target.value)} placeholder="e.g. 1.0.0" />

        <label className="meta">timeout (ms)</label>
        <input
          className="input form-input" type="number" min={0}
          value={task.timeout === "" ? "" : task.timeout}
          onChange={(e) => patch("timeout", e.target.value === "" ? "" : Number(e.target.value))}
        />

        <label className="meta">retry</label>
        <input
          className="input form-input" type="number" min={0}
          value={task.retry === "" ? "" : task.retry}
          onChange={(e) => patch("retry", e.target.value === "" ? "" : Number(e.target.value))}
        />

        <label className="meta">logic</label>
        <input className="input form-input" value={task.logic} onChange={(e) => patch("logic", e.target.value)} />

        <label className="meta">provider</label>
        <input className="input form-input" value={task.provider} onChange={(e) => patch("provider", e.target.value)} placeholder="(optional)" />

        <label className="meta">inherits</label>
        <input className="input form-input" value={task.inherits} onChange={(e) => patch("inherits", e.target.value)} placeholder="(optional)" />
      </div>
    </Section>
  );
}

function stringField(v: unknown): string {
  return typeof v === "string" ? v : "";
}
function numberField(v: unknown): number | "" {
  return typeof v === "number" ? v : "";
}
function setIfChanged(target: Record<string, unknown>, key: string, value: string) {
  if (value === "") delete target[key];
  else target[key] = value;
}
function setNumberOrDelete(target: Record<string, unknown>, key: string, value: number | "") {
  if (value === "" || value === 0) delete target[key];
  else target[key] = value;
}

interface OverrideRow {
  condition?: Record<string, unknown>;
  action?: string;
  task?: string;
}

// ParallelBlockEditor — one Section per enclosing parallel fork. Each fork
// shows its sibling branch labels (so the user can see which branches share
// a join), highlights the branch this node belongs to, and lets the user
// edit `on_branch_failure` for that fork. The state round-trips through
// parallelConfigs keyed by the prefix path that contains the fork.
function ParallelBlockEditor({
  path,
  allBranchPaths,
  parallelConfigs,
  onChange,
}: {
  path: string[];
  allBranchPaths: string[][];
  parallelConfigs: Record<string, ParallelConfig>;
  onChange: (key: string, next: ParallelConfig) => void;
}) {
  // For each fork depth (0..path.length-1), the prefix is path.slice(0, depth);
  // the sibling branch labels are the distinct values of branchPath[depth] for
  // every other node whose branchPath starts with that same prefix.
  const forks = path.map((_, depth) => {
    const prefix = path.slice(0, depth);
    const siblingLabels = new Set<string>();
    for (const bp of allBranchPaths) {
      if (bp.length <= depth) continue;
      let matches = true;
      for (let i = 0; i < depth; i++) {
        if (bp[i] !== prefix[i]) {
          matches = false;
          break;
        }
      }
      if (matches) siblingLabels.add(bp[depth]);
    }
    return {
      depth,
      prefix,
      key: parallelKey(prefix),
      activeLabel: path[depth],
      siblings: Array.from(siblingLabels),
    };
  });

  return (
    <Section
      title={`Parallel block${forks.length > 1 ? `s (${forks.length} nested)` : ""}`}
      storageKey="parallel"
      defaultOpen
    >
      {forks.map((f) => {
        const cfg = parallelConfigs[f.key] ?? {};
        return (
          <div
            key={f.key + ":" + f.depth}
            style={{
              border: "1px solid var(--border)",
              borderRadius: 4,
              padding: 8,
              marginBottom: 8,
              background: "var(--bg)",
            }}
          >
            <div className="meta" style={{ marginBottom: 6, fontSize: 11 }}>
              <strong style={{ color: "var(--text)" }}>
                Fork {f.depth === 0 ? "(outermost)" : `at depth ${f.depth}`}
              </strong>
              {f.prefix.length > 0 && (
                <> · inside <code>{f.prefix.join("·")}</code></>
              )}
            </div>
            <div style={{ display: "flex", flexWrap: "wrap", gap: 4, marginBottom: 8 }}>
              {f.siblings.map((label) => (
                <span
                  key={label}
                  className="badge"
                  style={{
                    background: label === f.activeLabel ? "var(--accent, #4a9eff)" : undefined,
                    color: label === f.activeLabel ? "#fff" : undefined,
                    fontFamily: "var(--font-mono)",
                  }}
                  title={label === f.activeLabel ? "This node lives in this branch" : "Sibling branch"}
                >
                  {label}
                </span>
              ))}
            </div>
            <div className="form-grid">
              <label className="meta" title="Action when any branch fails: abort the parallel block or continue running other branches.">
                on_branch_failure
              </label>
              <select
                className="input form-input"
                value={cfg.on_branch_failure ?? ""}
                onChange={(e) => onChange(f.key, { ...cfg, on_branch_failure: e.target.value || undefined })}
              >
                <option value="">— default (abort) —</option>
                <option value="abort">abort</option>
                <option value="continue">continue</option>
              </select>

              <label
                className="meta"
                title="Number of branches that must succeed before the block returns. Remaining branches are canceled. Empty / 0 = wait for all."
              >
                min_success
              </label>
              <input
                className="input form-input"
                type="number"
                min={0}
                max={f.siblings.length}
                placeholder={`0 — all (${f.siblings.length})`}
                value={cfg.min_success ?? ""}
                onChange={(e) => {
                  const v = e.target.value === "" ? undefined : Math.max(0, parseInt(e.target.value, 10) || 0);
                  onChange(f.key, { ...cfg, min_success: v });
                }}
              />

              <label
                className="meta"
                title="Block-wide budget in milliseconds. When the budget elapses every in-flight branch is canceled. Empty / 0 = no timeout."
              >
                timeout (ms)
              </label>
              <input
                className="input form-input"
                type="number"
                min={0}
                placeholder="0 — no timeout"
                value={cfg.timeout ?? ""}
                onChange={(e) => {
                  const v = e.target.value === "" ? undefined : Math.max(0, parseInt(e.target.value, 10) || 0);
                  onChange(f.key, { ...cfg, timeout: v });
                }}
              />

              <label
                className="meta"
                title={`Per-branch budget in milliseconds for branch "${f.activeLabel}". When elapsed the branch is canceled and reports a timeout error. Empty / 0 = no timeout.`}
              >
                {`timeout: ${f.activeLabel} (ms)`}
              </label>
              <input
                className="input form-input"
                type="number"
                min={0}
                placeholder="0 — no timeout"
                value={cfg.branch_timeouts?.[f.activeLabel] ?? ""}
                onChange={(e) => {
                  const raw = e.target.value;
                  const next = { ...(cfg.branch_timeouts ?? {}) };
                  if (raw === "" || parseInt(raw, 10) <= 0 || isNaN(parseInt(raw, 10))) {
                    delete next[f.activeLabel];
                  } else {
                    next[f.activeLabel] = Math.max(0, parseInt(raw, 10));
                  }
                  onChange(f.key, {
                    ...cfg,
                    branch_timeouts: Object.keys(next).length > 0 ? next : undefined,
                  });
                }}
              />
            </div>
          </div>
        );
      })}
    </Section>
  );
}

// BranchPathEditor — shows the enclosing branch chain (outer → inner) and
// lets the user rename any segment. Each segment is editable in place;
// renaming propagates to every node that shares the same prefix.
function BranchPathEditor({
  path,
  onRenameSegment,
}: {
  path: string[];
  onRenameSegment: (prefix: string[], next: string) => void;
}) {
  const [editingIdx, setEditingIdx] = useState<number | null>(null);
  const [draft, setDraft] = useState("");

  function start(i: number) {
    setEditingIdx(i);
    setDraft(path[i]);
  }
  function commit() {
    if (editingIdx === null) return;
    const trimmed = draft.trim();
    if (trimmed && trimmed !== path[editingIdx]) {
      onRenameSegment(path.slice(0, editingIdx + 1), trimmed);
    }
    setEditingIdx(null);
  }

  return (
    <div style={{ marginBottom: 12 }}>
      <label className="meta" style={{ display: "block", marginBottom: 4 }}>
        Parallel branch path <span style={{ opacity: 0.6 }}>(outer → inner)</span>
      </label>
      <div style={{ display: "flex", flexWrap: "wrap", alignItems: "center", gap: 4 }}>
        {path.map((seg, i) => (
          <span key={i} style={{ display: "flex", alignItems: "center", gap: 4 }}>
            {editingIdx === i ? (
              <input
                autoFocus
                className="input"
                value={draft}
                onChange={(e) => setDraft(e.target.value)}
                onBlur={commit}
                onKeyDown={(e) => {
                  if (e.key === "Enter") e.currentTarget.blur();
                  if (e.key === "Escape") setEditingIdx(null);
                }}
                style={{ width: 140, padding: "2px 6px", fontSize: 12 }}
              />
            ) : (
              <span
                onClick={() => start(i)}
                className="chip branch"
                style={{ cursor: "pointer" }}
                title="Click to rename this branch"
              >
                {seg}
              </span>
            )}
            {i < path.length - 1 && <span style={{ color: "var(--text-dim)" }}>·</span>}
          </span>
        ))}
      </div>
    </div>
  );
}

// LabelEditor — sets the per-placement label (TaskRef.label). Local-state
// buffered so typing doesn't push a new history entry on every keystroke;
// commits when the input loses focus or the user presses Enter.
function LabelEditor({
  label,
  onChange,
}: {
  label: string;
  onChange: (next: string) => void;
}) {
  const [draft, setDraft] = useState(label);
  useEffect(() => { setDraft(label); }, [label]);
  function commitIfChanged() {
    if (draft !== label) onChange(draft);
  }
  return (
    <div style={{ marginBottom: 12 }}>
      <label className="meta" style={{ display: "block", marginBottom: 4 }}>
        Label <span style={{ opacity: 0.6 }}>(this placement only)</span>
      </label>
      <input
        className="input"
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onBlur={commitIfChanged}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            e.currentTarget.blur();
          }
        }}
        placeholder="(optional, but required if the same task appears twice)"
        style={{ width: "100%" }}
      />
    </div>
  );
}

function OverridesEditor({
  overrides,
  onChange,
}: {
  overrides: OverrideRow[];
  onChange: (next: OverrideRow[]) => void;
}) {
  function update(i: number, patch: Partial<OverrideRow>) {
    onChange(overrides.map((o, idx) => (idx === i ? { ...o, ...patch } : o)));
  }
  function add() {
    onChange([...overrides, { condition: {}, action: "skip" }]);
  }
  function remove(i: number) {
    onChange(overrides.filter((_, idx) => idx !== i));
  }

  return (
    <Section
      title={`Overrides${overrides.length > 0 ? ` (${overrides.length})` : ""}`}
      storageKey="overrides"
      defaultOpen={overrides.length > 0}
      right={<button className="btn" style={{ fontSize: 11, padding: "2px 8px" }} onClick={add}>+ Add</button>}
    >
      {overrides.length === 0 && <div className="meta" style={{ marginBottom: 8 }}>none</div>}
      {overrides.map((ov, i) => {
        return (
          <div key={i} style={{ background: "var(--bg)", border: "1px solid var(--border)", borderRadius: 4, padding: 8, marginBottom: 8 }}>
            <div style={{ display: "flex", gap: 6, marginBottom: 6, alignItems: "center" }}>
              <label className="meta">action</label>
              <select
                value={ov.action ?? "skip"}
                onChange={(e) => update(i, { action: e.target.value })}
                className="input"
                style={{ flex: 1 }}
              >
                <option value="skip">skip</option>
                <option value="replace">replace</option>
              </select>
              <button className="btn" onClick={() => remove(i)} style={{ color: "var(--error)" }}>×</button>
            </div>
            {ov.action === "replace" && (
              <div style={{ display: "flex", gap: 6, marginBottom: 6, alignItems: "center" }}>
                <label className="meta">task</label>
                <input
                  value={ov.task ?? ""}
                  onChange={(e) => update(i, { task: e.target.value })}
                  placeholder="replacement task name"
                  className="input"
                  style={{ flex: 1 }}
                />
              </div>
            )}
            <div className="meta" style={{ marginBottom: 4 }}>condition (key: value, one per line)</div>
            <textarea
              rows={2}
              value={kvToText(ov.condition ?? {})}
              onChange={(e) => update(i, { condition: textToKV(e.target.value) })}
              className="input"
              style={{ width: "100%", fontFamily: "var(--mono)", fontSize: 12 }}
            />
          </div>
        );
      })}
    </Section>
  );
}

function kvToText(obj: Record<string, unknown>): string {
  return Object.entries(obj).map(([k, v]) => `${k}: ${String(v)}`).join("\n");
}
function textToKV(text: string): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const line of text.split("\n")) {
    const t = line.trim();
    if (!t) continue;
    const idx = t.indexOf(":");
    if (idx < 0) continue;
    const k = t.slice(0, idx).trim();
    const v = t.slice(idx + 1).trim();
    out[k] = v;
  }
  return out;
}

// ---------------------------------------------------------------------------
// BindEditor — edit input/output bindings for a task
// ---------------------------------------------------------------------------

function BindEditor({
  taskName,
  bindings,
  onChange,
  taskSchemas,
  flowTasks,
  targetBranchPath,
  targetOccurrence,
}: {
  taskName: string;
  bindings: Bind | undefined;
  onChange: (next: Bind | undefined) => void;
  taskSchemas: TaskSchemaMap;
  flowTasks: TaskRef[];
  targetBranchPath: string[] | undefined;
  targetOccurrence: number;
}) {
  const [logicHandler, setLogicHandler] = useState<{
    name: string;
    input_schema: { name: string; type: string }[] | null;
    output_schema: { name: string; type: string }[] | null;
    skippable_inputs: string[];
  } | null>(null);
  const [loading, setLoading] = useState(false);
  // Fields in "custom text input" mode (user chose the Custom… option).
  // We also auto-enter custom mode if the current value isn't in the scope list.
  const [customFields, setCustomFields] = useState<Set<string>>(new Set());

  useEffect(() => {
    setLoading(true);
    api.tasks()
      .then((tasks) => {
        const task = tasks.find((t) => t.name === taskName);
        if (!task || !task.logic) {
          setLogicHandler(null);
          setLoading(false);
          return;
        }
        return api.logic().then((logicData) => {
          const handler = logicData.handlers.find((h) => h.name === task.logic);
          if (!handler) {
            setLogicHandler(null);
          } else {
            setLogicHandler({
              name: handler.name,
              input_schema: flattenSchemaProps(handler.input_schema),
              output_schema: flattenSchemaProps(handler.output_schema),
              skippable_inputs: handler.skippable_inputs ?? [],
            });
          }
          setLoading(false);
        });
      })
      .catch(() => {
        setLogicHandler(null);
        setLoading(false);
      });
  }, [taskName]);

  // Compute available scope variables at this node's position in the flow.
  const scope: ScopeVar[] = useMemo(
    () => computeScopeAtNode(flowTasks, taskName, targetBranchPath, targetOccurrence, taskSchemas),
    [flowTasks, taskName, targetBranchPath, targetOccurrence, taskSchemas],
  );

  // Quick lookup map: scope key → ScopeVar (for collision detection).
  const scopeMap = useMemo(() => {
    const m = new Map<string, ScopeVar>();
    for (const sv of scope) m.set(sv.name, sv);
    return m;
  }, [scope]);

  function updateIn(field: string, scopeKey: string) {
    const newIn = { ...(bindings?.in ?? {}) };
    if (scopeKey.trim() === "") {
      delete newIn[field];
    } else {
      newIn[field] = scopeKey;
    }
    // Picking a scope binding clears any "skip" entry for the same field.
    const curSkip = (bindings?.skip ?? []).filter((s) => s !== field);
    onChange({
      in: Object.keys(newIn).length > 0 ? newIn : undefined,
      out: bindings?.out,
      skip: curSkip.length > 0 ? curSkip : undefined,
    });
  }

  function updateOut(field: string, scopeKey: string) {
    const newOut = { ...(bindings?.out ?? {}) };
    if (scopeKey.trim() === "") {
      delete newOut[field];
    } else {
      newOut[field] = scopeKey;
    }
    onChange({
      in: bindings?.in,
      out: Object.keys(newOut).length > 0 ? newOut : undefined,
      skip: bindings?.skip,
    });
  }

  // Toggle a field's "skip" state. When turning on, also clear any bind.in
  // entry for the same field so the two modes don't coexist.
  function updateSkip(field: string, on: boolean) {
    const cur = new Set(bindings?.skip ?? []);
    if (on) cur.add(field);
    else cur.delete(field);
    const newSkip = Array.from(cur);
    const newIn = { ...(bindings?.in ?? {}) };
    if (on) delete newIn[field];
    onChange({
      in: Object.keys(newIn).length > 0 ? newIn : undefined,
      out: bindings?.out,
      skip: newSkip.length > 0 ? newSkip : undefined,
    });
  }

  // Returns true if a field should render as a text input rather than a dropdown.
  // This is the case when: (a) user explicitly toggled custom mode, or (b) the
  // current bound value is non-empty and not found in the scope list.
  function isCustomMode(fieldName: string, currentValue: string): boolean {
    if (customFields.has(fieldName)) return true;
    if (currentValue !== "" && !scope.some((sv) => sv.name === currentValue)) return true;
    return false;
  }

  function enterCustomMode(fieldName: string) {
    setCustomFields((prev) => new Set(prev).add(fieldName));
  }

  function exitCustomMode(fieldName: string) {
    setCustomFields((prev) => {
      const next = new Set(prev);
      next.delete(fieldName);
      return next;
    });
  }

  if (loading) {
    return (
      <Section title="Input/Output Bindings" storageKey="bindings">
        <div className="meta">Loading...</div>
      </Section>
    );
  }

  if (!logicHandler || (!logicHandler.input_schema && !logicHandler.output_schema)) {
    return (
      <Section title="Input/Output Bindings" storageKey="bindings">
        <div className="meta">No schema available for this task's logic handler.</div>
      </Section>
    );
  }

  const hasInputs = logicHandler.input_schema && logicHandler.input_schema.length > 0;
  const hasOutputs = logicHandler.output_schema && logicHandler.output_schema.length > 0;

  return (
    <Section
      title="Input/Output Bindings"
      storageKey="bindings"
      hint={
        <InfoHint>
          Map handler fields to scope variables. Leave blank to use the field
          name directly.
        </InfoHint>
      }
    >
      {scope.length === 0 && hasInputs && (
        <div className="meta" style={{ marginBottom: 8 }}>
          No upstream scope variables available at this position. Inputs will be read from request parameters by their handler-declared names.
        </div>
      )}

      {hasInputs && (
        <div style={{ marginBottom: 12 }}>
          <div className="meta" style={{ marginBottom: 4, fontWeight: 600 }}>Inputs (read from scope)</div>
          {logicHandler.input_schema!.map((field) => {
            const currentValue = bindings?.in?.[field.name] ?? "";
            const custom = isCustomMode(field.name, currentValue);
            const compatibleVars = scope.filter((sv) => typesCompatible(sv.type, field.type));
            // The dropdown shows ONLY compatible vars — picking an incompatible
            // one would publish an unusable binding, so we don't offer it. The
            // user can still type an arbitrary name via Custom mode; we warn
            // inline if that name happens to match an incompatible scope var.
            const boundVar = currentValue ? scope.find((sv) => sv.name === currentValue) : undefined;
            const typeMismatch = !!boundVar && !typesCompatible(boundVar.type, field.type);
            const isSkippable = (logicHandler.skippable_inputs ?? []).includes(field.name);
            const isSkipped = (bindings?.skip ?? []).includes(field.name);
            return (
              <div key={field.name} style={{ marginBottom: 8 }}>
                <div
                  style={{
                    display: "grid",
                    gridTemplateColumns: isSkippable ? "100px 1fr auto" : "100px 1fr",
                    gap: 8,
                    alignItems: "center",
                  }}
                >
                  <label className="meta" title={`Type: ${field.type}`}>{field.name}</label>
                  {isSkipped ? (
                    <div
                      className="meta"
                      style={{
                        fontSize: 12,
                        fontStyle: "italic",
                        color: "var(--text-dim)",
                        padding: "4px 8px",
                        border: "1px dashed var(--border)",
                        borderRadius: 4,
                      }}
                      title="Field omitted from the input map — logic receives the Go zero value."
                    >
                      skipped (logic sees zero value)
                    </div>
                  ) : custom ? (
                    <div style={{ display: "flex", gap: 4 }}>
                      <input
                        className="input"
                        placeholder={field.name}
                        value={currentValue}
                        onChange={(e) => updateIn(field.name, e.target.value)}
                        style={{ fontSize: 12, flex: 1, borderColor: typeMismatch ? "var(--error, #c84)" : undefined }}
                      />
                      <button
                        className="btn"
                        style={{ fontSize: 11, padding: "0 6px" }}
                        title="Back to dropdown"
                        onClick={() => {
                          exitCustomMode(field.name);
                          // Clear value so it doesn't auto-re-enter custom mode
                          if (currentValue !== "" && !scope.some((sv) => sv.name === currentValue)) {
                            updateIn(field.name, "");
                          }
                        }}
                      >
                        ↩
                      </button>
                    </div>
                  ) : (
                    <select
                      className="input"
                      value={currentValue}
                      onChange={(e) => {
                        if (e.target.value === "__custom__") {
                          enterCustomMode(field.name);
                        } else {
                          updateIn(field.name, e.target.value);
                        }
                      }}
                      style={{ fontSize: 12, borderColor: typeMismatch ? "var(--error, #c84)" : undefined }}
                    >
                      <option value="">— use "{field.name}" from scope —</option>
                      {compatibleVars.map((sv) => (
                        <option key={sv.name} value={sv.name}>
                          {sv.name} ({sv.type})
                        </option>
                      ))}
                      <option value="__custom__">Custom…</option>
                    </select>
                  )}
                  {isSkippable && (
                    <label
                      className="meta"
                      title="Omit this input — logic receives the Go zero value. Available because the logic marked the field jig:&quot;skippable&quot;."
                      style={{ display: "flex", alignItems: "center", gap: 4, fontSize: 11, cursor: "pointer" }}
                    >
                      <input
                        type="checkbox"
                        checked={isSkipped}
                        onChange={(e) => updateSkip(field.name, e.target.checked)}
                      />
                      skip
                    </label>
                  )}
                </div>
                {!isSkipped && typeMismatch && (
                  <div style={{ color: "var(--error, #c84)", fontSize: 11, marginTop: 2, marginLeft: 108, lineHeight: 1.4 }}>
                    ⚠ "{currentValue}" in scope is type <code>{boundVar!.type}</code>, but this input expects <code>{field.type}</code>.
                    Pick a compatible variable or rename the upstream output.
                  </div>
                )}
              </div>
            );
          })}
        </div>
      )}

      {hasOutputs && (
        <div style={{ marginBottom: 12 }}>
          <div className="meta" style={{ marginBottom: 4, fontWeight: 600 }}>Outputs (write to scope)</div>
          {logicHandler.output_schema!.map((field) => {
            const currentValue = bindings?.out?.[field.name] ?? "";
            const publishedName = currentValue.trim() !== "" ? currentValue.trim() : field.name;
            const existing = scopeMap.get(publishedName);
            const hasCollision = existing !== undefined && !typesCompatible(existing.type, field.type);
            return (
              <div key={field.name} style={{ marginBottom: 8 }}>
                <div style={{ display: "grid", gridTemplateColumns: "100px 1fr", gap: 8, alignItems: "center" }}>
                  <label className="meta" title={`Type: ${field.type}`}>{field.name}</label>
                  <input
                    className="input"
                    placeholder={field.name}
                    value={currentValue}
                    onChange={(e) => updateOut(field.name, e.target.value)}
                    style={{ fontSize: 12, borderColor: hasCollision ? "var(--warn)" : undefined }}
                  />
                </div>
                {hasCollision && (
                  <div style={{ color: "var(--warn)", fontSize: 11, marginTop: 2, marginLeft: 108, lineHeight: 1.4 }}>
                    ⚠ "{publishedName}" already in scope as {existing!.type}; this task emits {field.type}.
                    Rename to avoid silent overwrite with a different type.
                  </div>
                )}
              </div>
            );
          })}
        </div>
      )}
    </Section>
  );
}

// ---------------------------------------------------------------------------
// Context menu
// ---------------------------------------------------------------------------

function ContextMenu({
  menu,
  onClose,
  onDeleteNode,
  onDeleteEdge,
  onAddTask,
  onInsertBoilerplate,
  onInsertFlow,
}: {
  menu: CtxMenu;
  onClose: () => void;
  onDeleteNode: (id: string) => void;
  onDeleteEdge: (id: string) => void;
  onAddTask: (at: { x: number; y: number }) => void;
  onInsertBoilerplate: (at: { x: number; y: number }) => void;
  onInsertFlow: (at: { x: number; y: number }) => void;
}) {
  // The flow pane is positioned; menu uses fixed positioning relative to viewport.
  useEffect(() => {
    const onClick = () => onClose();
    window.addEventListener("click", onClick);
    return () => window.removeEventListener("click", onClick);
  }, [onClose]);

  const items: { label: string; action: () => void; danger?: boolean }[] = [];
  if (menu.target.kind === "node") {
    items.push({ label: "Delete node", action: () => onDeleteNode(menu.target.kind === "node" ? menu.target.id : ""), danger: true });
  } else if (menu.target.kind === "edge") {
    items.push({ label: "Delete edge", action: () => onDeleteEdge(menu.target.kind === "edge" ? menu.target.id : ""), danger: true });
  } else {
    // pane
    items.push({ label: "Add task here…", action: () => onAddTask({ x: menu.x, y: menu.y }) });
    items.push({ label: "Insert flow as template…", action: () => onInsertFlow({ x: menu.x, y: menu.y }) });
    items.push({ label: "Insert 2-step boilerplate", action: () => onInsertBoilerplate({ x: menu.x, y: menu.y }) });
  }
  return (
    <div
      onClick={(e) => e.stopPropagation()}
      style={{
        position: "fixed",
        left: menu.x,
        top: menu.y,
        background: "var(--panel)",
        border: "1px solid var(--border-strong)",
        borderRadius: 6,
        minWidth: 200,
        zIndex: 100,
        boxShadow: "0 4px 16px #000c",
      }}
    >
      {items.map((it, i) => (
        <div
          key={i}
          onClick={it.action}
          style={{
            padding: "8px 12px",
            cursor: "pointer",
            color: it.danger ? "var(--error)" : "var(--text)",
            borderBottom: i < items.length - 1 ? "1px solid var(--border)" : "none",
            fontSize: 12,
          }}
          onMouseEnter={(e) => (e.currentTarget.style.background = "var(--panel-2)")}
          onMouseLeave={(e) => (e.currentTarget.style.background = "transparent")}
        >
          {it.label}
        </div>
      ))}
    </div>
  );
}

// ---------------------------------------------------------------------------
// node renderer
// ---------------------------------------------------------------------------

function TaskNode({ data }: NodeProps<NodeData>) {
  const isWrapper = !!data.wrapsLogic;
  const cls = [
    "gnode",
    "task",
    isWrapper ? "wrapper" : "",
    data.selected ? "selected" : "",
    data.hasError ? "node-error" : "",
    data.hasWarning ? "node-warning" : "",
  ].filter(Boolean).join(" ");
  return (
    <div className={cls}>
      <Handle type="target" position={Position.Top} className="port port-top" />
      <div className="gnode-stripe">
        {data.isStart && <span className="chip start">▶ start</span>}
        {data.isEnd && <span className="chip end">■ end</span>}
        {data.branchPath && data.branchPath.length > 0 && (
          <span className="chip branch">⌥ {data.branchPath.join("·")}</span>
        )}
        {isWrapper && (
          <span className="chip wraps" title={`Dispatches logic "${data.wrapsLogic}" via Engine.Invoke`}>
            ↻ wraps
          </span>
        )}
        {(data.hasError || data.hasWarning) && (
          <IssueChip severity={data.hasError ? "error" : "warning"} issues={data.issues ?? []} />
        )}
      </div>
      <div className="gnode-title">{data.taskName}</div>
      {data.label && (
        <div className="gnode-sub" style={{ color: "var(--accent)" }}>
          @{data.label}
        </div>
      )}
      {isWrapper && (
        <div
          className="gnode-inner"
          title="Inner logic this wrapper invokes at runtime"
        >
          <span className="gnode-inner-arrow">↳</span>
          <span className="gnode-inner-name mono">{data.wrapsLogic}</span>
        </div>
      )}
      <Handle type="source" position={Position.Bottom} className="port port-bottom" />
    </div>
  );
}

// IssueChip — the `!` mark on a problem node. Hovering reveals the full list
// of error/warning messages so the user doesn't have to open the issues
// modal to find out what's wrong with this specific node.
function IssueChip({
  severity,
  issues,
}: {
  severity: "error" | "warning";
  issues: { severity: "error" | "warning"; message: string }[];
}) {
  const [open, setOpen] = useState(false);
  const [pos, setPos] = useState<{ x: number; y: number } | null>(null);
  const cls = severity === "error" ? "chip err" : "chip warn";
  const summary = issues.map((i) => i.message).join("\n");
  // Portal the popup to <body> with position:fixed so it escapes ReactFlow's
  // stacking context and never gets clipped by the canvas or node z-order.
  function show(e: React.MouseEvent) {
    const r = (e.currentTarget as HTMLElement).getBoundingClientRect();
    setPos({ x: r.left, y: r.bottom + 6 });
    setOpen(true);
  }
  return (
    <>
      <span
        className={cls}
        title={summary || undefined}
        onMouseEnter={show}
        onMouseLeave={() => setOpen(false)}
        style={{ cursor: "help" }}
      >
        !
      </span>
      {open && pos && issues.length > 0 && createPortal(
        <div
          className="nodrag nopan"
          style={{
            position: "fixed",
            top: pos.y,
            left: pos.x,
            zIndex: 10000,
            minWidth: 220,
            maxWidth: 360,
            padding: "8px 10px",
            background: "var(--panel-2, #2a2f3a)",
            border: `1px solid ${severity === "error" ? "var(--error, #c84)" : "var(--warn, #c93)"}`,
            borderRadius: 4,
            color: "var(--text)",
            fontSize: 11,
            lineHeight: 1.4,
            boxShadow: "0 4px 12px rgba(0,0,0,0.5)",
            pointerEvents: "none",
          }}
        >
          {issues.map((it, i) => (
            <div key={i} style={{ marginBottom: i < issues.length - 1 ? 6 : 0 }}>
              <span
                style={{
                  fontWeight: 600,
                  color: it.severity === "error" ? "var(--error, #c84)" : "var(--warn, #c93)",
                  marginRight: 4,
                }}
              >
                {it.severity === "error" ? "ERROR" : "WARN"}
              </span>
              {it.message}
            </div>
          ))}
        </div>,
        document.body,
      )}
    </>
  );
}

// ---------------------------------------------------------------------------
// helpers: Canvas ↔ React Flow, overrides, etc.
// ---------------------------------------------------------------------------

function canvasToRFNodes(c: Canvas): Node<NodeData>[] {
  return c.nodes.map((n) => ({
    id: n.id,
    type: "task",
    position: n.position,
    data: {
      taskName: n.taskName,
      label: n.label,
      branchPath: n.branchPath,
      selected: false,
      isStart: false,
      isEnd: false,
    },
  }));
}

function canvasToRFEdges(c: Canvas): Edge[] {
  return c.edges.map((e) => ({
    id: e.id,
    source: e.source,
    target: e.target,
    type: "smoothstep",
    style: { stroke: "#7cf0c7", strokeWidth: 2 },
  }));
}

function rfToCanvas(nodes: Node<NodeData>[], edges: Edge[]): Canvas {
  return {
    nodes: nodes.map((n) => ({
      id: n.id,
      taskName: n.data.taskName,
      label: n.data.label,
      branchPath: n.data.branchPath,
      position: { x: n.position.x, y: n.position.y },
    })),
    edges: edges.map((e) => ({ id: e.id, source: e.source, target: e.target })),
  };
}

function dropPositionFor(existing: Node<NodeData>[]): { x: number; y: number } {
  if (existing.length === 0) return { x: 0, y: 0 };
  const maxY = Math.max(...existing.map((n) => n.position.y));
  return { x: 0, y: maxY + 120 };
}

function stripLayoutFromFlow(f: Flow): Flow {
  if (!f.metadata) return f;
  const md = { ...f.metadata };
  delete (md as Record<string, unknown>)["layout"];
  return { ...f, metadata: Object.keys(md).length === 0 ? undefined : md };
}

// readEmbeddedLayout pulls the editor's persisted node positions out of
// flow.metadata.layout (the shape we write on save). Returns {} when the
// metadata isn't there or is shaped wrong, so callers can fall back to the
// server-side sidecar without special-casing.
function readEmbeddedLayout(f: Flow): Record<string, { x: number; y: number }> {
  const raw = (f.metadata as Record<string, unknown> | undefined)?.["layout"];
  if (!raw || typeof raw !== "object") return {};
  const out: Record<string, { x: number; y: number }> = {};
  for (const [k, v] of Object.entries(raw as Record<string, unknown>)) {
    if (v && typeof v === "object" && "x" in v && "y" in v) {
      const o = v as { x: unknown; y: unknown };
      if (typeof o.x === "number" && typeof o.y === "number") {
        out[k] = { x: o.x, y: o.y };
      }
    }
  }
  return out;
}

// collectOverrides walks the decompiled flow and pairs each TaskRef's
// overrides with the canvas node that represents it.
function collectOverrides(canvas: Canvas, flow: Flow): Record<string, TaskRef["overrides"]> {
  const byName: Record<string, TaskRef["overrides"][]> = {};
  walk(flow.tasks);
  function walk(refs: TaskRef[]) {
    for (const r of refs) {
      if (r.name) {
        if (r.overrides && r.overrides.length > 0) {
          (byName[r.name] ||= []).push(r.overrides);
        }
      } else if (r.parallel) {
        for (const b of r.parallel.branches) walk(b.tasks);
      }
    }
  }
  const out: Record<string, TaskRef["overrides"]> = {};
  for (const n of canvas.nodes) {
    const stack = byName[n.taskName];
    if (stack && stack.length > 0) {
      out[n.id] = stack.shift();
    }
  }
  return out;
}

// applyOverrides reattaches per-node overrides to the freshly compiled TaskRef
// tree by matching on task name + occurrence order.
function applyOverrides(
  tasks: TaskRef[],
  canvas: Canvas,
  overrides: Record<string, TaskRef["overrides"]>,
): TaskRef[] {
  // Build a queue per task name in the order nodes appear in the canvas.
  // We rely on the compiler emitting tasks in the same order it walks
  // canvas.nodes-by-id, which is the topological order.
  const queues: Record<string, TaskRef["overrides"][]> = {};
  for (const n of canvas.nodes) {
    const o = overrides[n.id];
    if (o && o.length > 0) (queues[n.taskName] ||= []).push(o);
  }
  function attach(refs: TaskRef[]): TaskRef[] {
    return refs.map((r) => {
      if (r.name) {
        const q = queues[r.name];
        if (q && q.length > 0) return { ...r, overrides: q.shift() };
        return r;
      }
      if (r.parallel) {
        return {
          ...r,
          parallel: {
            ...r.parallel,
            branches: r.parallel.branches.map((b) => ({ ...b, tasks: attach(b.tasks) })),
          },
        };
      }
      return r;
    });
  }
  return attach(tasks);
}

// collectRefParams walks the decompiled flow and pairs each TaskRef's
// params (per-flow overrides) with the canvas node that represents it.
function collectRefParams(
  canvas: Canvas,
  flow: Flow,
): Record<string, Record<string, unknown> | undefined> {
  const byName: Record<string, (Record<string, unknown> | undefined)[]> = {};
  walk(flow.tasks);
  function walk(refs: TaskRef[]) {
    for (const r of refs) {
      if (r.name) {
        (byName[r.name] ||= []).push(r.params);
      } else if (r.parallel) {
        for (const b of r.parallel.branches) walk(b.tasks);
      }
    }
  }
  const out: Record<string, Record<string, unknown> | undefined> = {};
  for (const n of canvas.nodes) {
    const stack = byName[n.taskName];
    if (stack && stack.length > 0) {
      out[n.id] = stack.shift();
    }
  }
  return out;
}

// collectBindings walks the decompiled flow and pairs each TaskRef's
// bind data with the canvas node that represents it.
function collectBindings(canvas: Canvas, flow: Flow): Record<string, Bind | undefined> {
  const byName: Record<string, (Bind | undefined)[]> = {};
  walk(flow.tasks);
  function walk(refs: TaskRef[]) {
    for (const r of refs) {
      if (r.name) {
        (byName[r.name] ||= []).push(r.bind);
      } else if (r.parallel) {
        for (const b of r.parallel.branches) walk(b.tasks);
      }
    }
  }
  const out: Record<string, Bind | undefined> = {};
  for (const n of canvas.nodes) {
    const stack = byName[n.taskName];
    if (stack && stack.length > 0) {
      out[n.id] = stack.shift();
    }
  }
  return out;
}

// applyBindings reattaches per-node bind data to the freshly compiled TaskRef
// tree by matching on task name + occurrence order.
function applyBindings(
  tasks: TaskRef[],
  canvas: Canvas,
  bindings: Record<string, Bind | undefined>,
): TaskRef[] {
  // Build a queue per task name in the order nodes appear in the canvas.
  const queues: Record<string, (Bind | undefined)[]> = {};
  for (const n of canvas.nodes) {
    const b = bindings[n.id];
    (queues[n.taskName] ||= []).push(b);
  }
  function attach(refs: TaskRef[]): TaskRef[] {
    return refs.map((r) => {
      if (r.name) {
        const q = queues[r.name];
        if (q && q.length > 0) {
          const b = q.shift();
          // Only attach bind if it has actual data
          if (
            b &&
            (Object.keys(b.in ?? {}).length > 0 ||
              Object.keys(b.out ?? {}).length > 0 ||
              (b.skip?.length ?? 0) > 0)
          ) {
            return { ...r, bind: b };
          }
        }
        return r;
      }
      if (r.parallel) {
        return {
          ...r,
          parallel: {
            ...r.parallel,
            branches: r.parallel.branches.map((b) => ({ ...b, tasks: attach(b.tasks) })),
          },
        };
      }
      return r;
    });
  }
  return attach(tasks);
}

// Parallel block configuration that lives outside the canvas topology. Each
// parallel block in the flow can carry an `on_branch_failure` directive that
// the compiler discards, so we round-trip it ourselves keyed by the branch
// path prefix that *contains* the fork (empty string = outermost).
interface ParallelConfig {
  on_branch_failure?: string;
  min_success?: number;
  timeout?: number;
  branch_timeouts?: Record<string, number>;
}

function parallelConfigIsEmpty(cfg: ParallelConfig | undefined): boolean {
  if (!cfg) return true;
  if (cfg.on_branch_failure) return false;
  if (cfg.min_success && cfg.min_success > 0) return false;
  if (cfg.timeout && cfg.timeout > 0) return false;
  if (cfg.branch_timeouts && Object.keys(cfg.branch_timeouts).length > 0) return false;
  return true;
}

function parallelKey(prefix: string[]): string {
  return prefix.join("/");
}

// Walk a flow's task tree and pull every parallel block's config keyed by
// the prefix of branch labels that lead to it.
function collectParallelConfigs(tasks: TaskRef[]): Record<string, ParallelConfig> {
  const out: Record<string, ParallelConfig> = {};
  function walk(refs: TaskRef[], prefix: string[]) {
    for (const r of refs) {
      if (r.parallel) {
        const cfg: ParallelConfig = {};
        if (r.parallel.on_branch_failure) cfg.on_branch_failure = r.parallel.on_branch_failure;
        if (r.parallel.min_success && r.parallel.min_success > 0) cfg.min_success = r.parallel.min_success;
        if (r.parallel.timeout && r.parallel.timeout > 0) cfg.timeout = r.parallel.timeout;
        const branchTimeouts: Record<string, number> = {};
        r.parallel.branches.forEach((b, i) => {
          const label = b.label || `branch_${i + 1}`;
          if (b.timeout && b.timeout > 0) branchTimeouts[label] = b.timeout;
        });
        if (Object.keys(branchTimeouts).length > 0) cfg.branch_timeouts = branchTimeouts;
        if (!parallelConfigIsEmpty(cfg)) out[parallelKey(prefix)] = cfg;
        r.parallel.branches.forEach((b, i) => {
          walk(b.tasks, [...prefix, b.label || `branch_${i + 1}`]);
        });
      }
    }
  }
  walk(tasks, []);
  return out;
}

// applyParallelConfigs reattaches per-parallel-block settings (on_branch_failure
// today; room to grow) to a freshly compiled tree.
function applyParallelConfigs(
  tasks: TaskRef[],
  configs: Record<string, ParallelConfig>,
): TaskRef[] {
  function attach(refs: TaskRef[], prefix: string[]): TaskRef[] {
    return refs.map((r) => {
      if (!r.parallel) return r;
      const cfg = configs[parallelKey(prefix)];
      const branchTimeouts = cfg?.branch_timeouts ?? {};
      const nextParallel = {
        ...r.parallel,
        branches: r.parallel.branches.map((b, i) => {
          const label = b.label || `branch_${i + 1}`;
          const t = branchTimeouts[label];
          const nb = { ...b, tasks: attach(b.tasks, [...prefix, label]) };
          if (t && t > 0) {
            nb.timeout = t;
          } else {
            delete (nb as { timeout?: number }).timeout;
          }
          return nb;
        }),
      };
      if (cfg?.on_branch_failure) {
        nextParallel.on_branch_failure = cfg.on_branch_failure;
      } else {
        delete (nextParallel as { on_branch_failure?: string }).on_branch_failure;
      }
      if (cfg?.min_success && cfg.min_success > 0) {
        nextParallel.min_success = cfg.min_success;
      } else {
        delete (nextParallel as { min_success?: number }).min_success;
      }
      if (cfg?.timeout && cfg.timeout > 0) {
        nextParallel.timeout = cfg.timeout;
      } else {
        delete (nextParallel as { timeout?: number }).timeout;
      }
      return { ...r, parallel: nextParallel };
    });
  }
  return attach(tasks, []);
}

// ---------------------------------------------------------------------------
// Task palette
// ---------------------------------------------------------------------------

function SaveDraftModal({
  flowName,
  onSave,
  onClose,
}: {
  flowName: string;
  onSave: (label: string) => void;
  onClose: () => void;
}) {
  const [label, setLabel] = useState(`${flowName} @ ${new Date().toLocaleString()}`);
  return (
    <div
      onClick={onClose}
      style={{
        position: "fixed", inset: 0, background: "#000a",
        display: "flex", alignItems: "flex-start", justifyContent: "center",
        paddingTop: 120, zIndex: 200,
      }}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        style={{
          background: "var(--panel)", border: "1px solid var(--border-strong)",
          borderRadius: 6, width: 460, overflow: "hidden",
          display: "flex", flexDirection: "column",
        }}
      >
        <div style={{ padding: "12px 16px", borderBottom: "1px solid var(--border)" }}>
          <strong>Save draft</strong>
          <div className="meta">Stored locally in your browser. Not pushed to the server.</div>
        </div>
        <div style={{ padding: 16 }}>
          <label className="meta" style={{ display: "block", marginBottom: 6 }}>Label</label>
          <input
            autoFocus
            className="input"
            value={label}
            onChange={(e) => setLabel(e.target.value)}
            onKeyDown={(e) => { if (e.key === "Enter") onSave(label); }}
            style={{ width: "100%" }}
          />
        </div>
        <div style={{ padding: "12px 16px", borderTop: "1px solid var(--border)", display: "flex", justifyContent: "flex-end", gap: 8 }}>
          <button className="btn" onClick={onClose}>Cancel</button>
          <button className="btn btn-primary" onClick={() => onSave(label)}>Save draft</button>
        </div>
      </div>
    </div>
  );
}

function DraftsModal({
  drafts,
  onLoad,
  onDelete,
  onRename,
  onClose,
}: {
  drafts: DraftEntry[];
  onLoad: (d: DraftEntry) => void;
  onDelete: (id: string) => void;
  onRename: (id: string, label: string) => void;
  onClose: () => void;
}) {
  const [editing, setEditing] = useState<string | null>(null);
  const [editLabel, setEditLabel] = useState("");
  const { confirm, ui: confirmUI } = useConfirmDialog();
  function startRename(d: DraftEntry) {
    setEditing(d.id);
    setEditLabel(d.label);
  }
  function commitRename() {
    if (editing) onRename(editing, editLabel);
    setEditing(null);
  }
  async function requestDelete(d: DraftEntry) {
    const ok = await confirm({
      title: "Delete draft?",
      message: <>Delete draft <code>{d.label}</code>? This can't be undone.</>,
      confirmLabel: "Delete",
      danger: true,
    });
    if (ok) onDelete(d.id);
  }
  return (
    <div
      onClick={onClose}
      style={{
        position: "fixed", inset: 0, background: "#000a",
        display: "flex", alignItems: "flex-start", justifyContent: "center",
        paddingTop: 100, zIndex: 200,
      }}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        style={{
          background: "var(--panel)", border: "1px solid var(--border-strong)",
          borderRadius: 6, width: 600, maxHeight: "70vh", overflow: "hidden",
          display: "flex", flexDirection: "column",
        }}
      >
        <div style={{ padding: "12px 16px", borderBottom: "1px solid var(--border)", display: "flex", alignItems: "center" }}>
          <strong style={{ flex: 1 }}>Drafts (this browser)</strong>
          <button className="btn" onClick={onClose}>Close</button>
        </div>
        <div style={{ overflow: "auto", flex: 1 }}>
          {drafts.length === 0 ? (
            <div className="empty" style={{ padding: 24 }}>
              No drafts yet. Use "Save draft" to stash the current graph locally.
            </div>
          ) : drafts.map((d) => (
            <div key={d.id} style={{
              padding: "10px 16px",
              borderBottom: "1px solid var(--border)",
              display: "flex", gap: 8, alignItems: "center",
            }}>
              <div style={{ flex: 1, minWidth: 0 }}>
                {editing === d.id ? (
                  <input
                    autoFocus
                    className="input"
                    value={editLabel}
                    onChange={(e) => setEditLabel(e.target.value)}
                    onBlur={commitRename}
                    onKeyDown={(e) => { if (e.key === "Enter") commitRename(); if (e.key === "Escape") setEditing(null); }}
                    style={{ width: "100%" }}
                  />
                ) : (
                  <>
                    <div style={{ fontWeight: 500, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                      {d.label}
                    </div>
                    <div className="meta">
                      saved {new Date(d.savedAt).toLocaleString()} · {Math.round(d.yaml.length / 1024)} kB
                    </div>
                  </>
                )}
              </div>
              <button className="btn" onClick={() => startRename(d)} disabled={editing === d.id}>Rename</button>
              <button className="btn btn-primary" onClick={() => onLoad(d)}>Load</button>
              <button
                className="btn"
                style={{ color: "var(--error)", borderColor: "var(--error)" }}
                onClick={() => requestDelete(d)}
              >
                Delete
              </button>
            </div>
          ))}
        </div>
      </div>
      {confirmUI}
    </div>
  );
}

function FlowPicker({
  flows,
  localTemplates,
  onPickFlow,
  onPickLocal,
  onClose,
}: {
  flows: FlowSummary[];
  localTemplates: DraftEntry[];
  onPickFlow: (name: string) => void;
  onPickLocal: (tpl: DraftEntry) => void;
  onClose: () => void;
}) {
  const [q, setQ] = useState("");
  const needle = q.toLowerCase();
  const filteredFlows = flows.filter((f) => f.name.toLowerCase().includes(needle)).slice(0, 50);
  const filteredLocal = localTemplates.filter((t) => t.label.toLowerCase().includes(needle)).slice(0, 50);
  return (
    <div
      onClick={onClose}
      style={{
        position: "fixed", inset: 0, background: "#000a",
        display: "flex", alignItems: "flex-start", justifyContent: "center",
        paddingTop: 120, zIndex: 200,
      }}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        style={{
          background: "var(--panel)", border: "1px solid var(--border-strong)",
          borderRadius: 6, width: 520, maxHeight: "60vh", overflow: "hidden",
          display: "flex", flexDirection: "column",
        }}
      >
        <div style={{ padding: "8px 16px", borderBottom: "1px solid var(--border)" }}>
          <strong>Insert flow as template</strong>
          <div className="meta">All nodes from the picked flow are copied (new ids) at the cursor.</div>
        </div>
        <input
          autoFocus
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder="search…"
          style={{
            background: "transparent", border: 0,
            borderBottom: "1px solid var(--border)",
            padding: "12px 16px", color: "var(--text)", fontSize: 14, outline: "none",
          }}
        />
        <div style={{ overflow: "auto", flex: 1 }}>
          {filteredLocal.length > 0 && (
            <>
              <div className="meta" style={{ padding: "6px 16px", textTransform: "uppercase", letterSpacing: 0.5, fontSize: 10 }}>
                Local templates
              </div>
              {filteredLocal.map((t) => (
                <div
                  key={t.id}
                  onClick={() => onPickLocal(t)}
                  style={{ padding: "8px 16px", cursor: "pointer", borderBottom: "1px solid var(--border)" }}
                  onMouseEnter={(e) => (e.currentTarget.style.background = "var(--panel-2)")}
                  onMouseLeave={(e) => (e.currentTarget.style.background = "transparent")}
                >
                  <div style={{ fontWeight: 500 }}>{t.label}</div>
                  <div className="meta">saved {new Date(t.savedAt).toLocaleString()} · browser-local</div>
                </div>
              ))}
            </>
          )}
          {filteredFlows.length > 0 && (
            <>
              <div className="meta" style={{ padding: "6px 16px", textTransform: "uppercase", letterSpacing: 0.5, fontSize: 10 }}>
                Saved flows
              </div>
              {filteredFlows.map((f) => (
                <div
                  key={f.name}
                  onClick={() => onPickFlow(f.name)}
                  style={{ padding: "8px 16px", cursor: "pointer", borderBottom: "1px solid var(--border)" }}
                  onMouseEnter={(e) => (e.currentTarget.style.background = "var(--panel-2)")}
                  onMouseLeave={(e) => (e.currentTarget.style.background = "transparent")}
                >
                  <div style={{ fontWeight: 500 }}>{f.name}</div>
                  <div className="meta">
                    {f.task_count} task{f.task_count === 1 ? "" : "s"}
                    {f.description ? ` · ${f.description}` : ""}
                  </div>
                </div>
              ))}
            </>
          )}
          {filteredLocal.length === 0 && filteredFlows.length === 0 && (
            <div className="empty" style={{ padding: 24 }}>no matches</div>
          )}
        </div>
      </div>
    </div>
  );
}

function TaskPalette({
  palette,
  onPick,
  onClose,
}: {
  palette: TaskSummary[];
  onPick: (name: string) => void;
  onClose: () => void;
}) {
  const [q, setQ] = useState("");
  const filtered = palette
    .filter((t) => t.name.toLowerCase().includes(q.toLowerCase()))
    .slice(0, 50);
  return (
    <div
      onClick={onClose}
      style={{
        position: "fixed", inset: 0, background: "#000a",
        display: "flex", alignItems: "flex-start", justifyContent: "center",
        paddingTop: 120, zIndex: 200,
      }}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        style={{
          background: "var(--panel)", border: "1px solid var(--border-strong)",
          borderRadius: 6, width: 480, maxHeight: "60vh", overflow: "hidden",
          display: "flex", flexDirection: "column",
        }}
      >
        <input
          autoFocus
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder="search tasks…"
          style={{
            background: "transparent", border: 0,
            borderBottom: "1px solid var(--border)",
            padding: "12px 16px", color: "var(--text)", fontSize: 14, outline: "none",
          }}
        />
        <div style={{ overflow: "auto", flex: 1 }}>
          {filtered.length === 0 && <div className="empty" style={{ padding: 24 }}>no matches</div>}
          {filtered.map((t) => (
            <div
              key={t.name}
              onClick={() => onPick(t.name)}
              style={{ padding: "8px 16px", cursor: "pointer", borderBottom: "1px solid var(--border)" }}
              onMouseEnter={(e) => (e.currentTarget.style.background = "var(--panel-2)")}
              onMouseLeave={(e) => (e.currentTarget.style.background = "transparent")}
            >
              <div style={{ fontWeight: 500 }}>{t.name}</div>
              {t.description && <div className="meta">{t.description}</div>}
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}


// ---------------------------------------------------------------------------
// Scope computation — mirrors the Go validator's scope-walk behaviour so the
// UI can show which scope keys are available at each node's position.
// ---------------------------------------------------------------------------

interface ScopeVar {
  name: string;   // scope key as visible to the consumer
  type: string;   // JSON-schema-style type label
  source: string; // task name that produced it (for tooltip)
}

interface TaskSchemaMap {
  [taskName: string]: {
    logic?: string;
    inputs: { name: string; type: string }[];
    outputs: { name: string; type: string }[];
  };
}

// computeScopeAtNode walks flowTasks in order, accumulating the scope that
// would be available *before* the identified target task executes.
//
// Target is identified by (taskName, branchPath, occurrence) because the
// same task can be placed multiple times with the same branchPath; occurrence
// is a 0-based counter that counts how many times we've already seen a node
// with matching (taskName, branchPath) before the target one.
function computeScopeAtNode(
  flowTasks: TaskRef[],
  targetName: string,
  targetBranchPath: string[] | undefined,
  targetOccurrence: number,
  taskSchemas: TaskSchemaMap,
): ScopeVar[] {
  // Mutable counter of remaining occurrences to skip.
  let remaining = targetOccurrence;
  let found = false;

  const scope = new Map<string, ScopeVar>();

  // Returns true if we should stop (we've reached the target).
  function walkTasks(refs: TaskRef[], branchPath: string[]): boolean {
    for (const ref of refs) {
      if (found) return true;

      if (ref.name) {
        // Check if this is the target node.
        const bpMatch =
          JSON.stringify(branchPath.length > 0 ? branchPath : undefined) ===
          JSON.stringify(targetBranchPath && targetBranchPath.length > 0 ? targetBranchPath : undefined);

        if (ref.name === targetName && bpMatch) {
          if (remaining === 0) {
            found = true;
            return true;
          }
          remaining--;
        }

        // Publish this task's outputs into scope (using bind.out renames).
        const schema = taskSchemas[ref.name];
        if (schema) {
          for (const out of schema.outputs) {
            const publishedName = ref.bind?.out?.[out.name] ?? out.name;
            scope.set(publishedName, { name: publishedName, type: out.type, source: ref.name });
          }
        }
      } else if (ref.parallel) {
        // For each branch: fork scope, walk branch, collect additions.
        // If target is inside a branch: return true immediately.
        // Otherwise: publish branch additions under <branchLabel>.<key>.
        const beforeKeys = new Set(scope.keys());

        let targetInBranch = false;
        for (const branch of ref.parallel.branches) {
          if (found) return true;
          const branchLabel = branch.label;
          const childPath = [...branchPath, branchLabel];

          // Clone scope for this branch walk.
          const branchScope = new Map<string, ScopeVar>(scope);
          const savedScope = new Map<string, ScopeVar>(scope);

          // Temporarily replace scope with branch scope.
          scope.clear();
          for (const [k, v] of branchScope) scope.set(k, v);

          const hitTarget = walkTasks(branch.tasks, childPath);

          if (hitTarget) {
            // Target is inside this branch — scope is already correct (branch's
            // local scope up to the target). Return immediately.
            targetInBranch = true;
            break;
          }

          // Collect keys added by this branch.
          const branchAdditions: ScopeVar[] = [];
          for (const [k, v] of scope) {
            if (!beforeKeys.has(k)) {
              branchAdditions.push(v);
            }
          }

          // Restore parent scope and publish branch additions namespaced.
          scope.clear();
          for (const [k, v] of savedScope) scope.set(k, v);

          for (const sv of branchAdditions) {
            const namespacedKey = `${branchLabel}.${sv.name}`;
            scope.set(namespacedKey, { name: namespacedKey, type: sv.type, source: sv.source });
          }
        }

        if (targetInBranch) return true;
      }
    }
    return false;
  }

  walkTasks(flowTasks, []);
  return Array.from(scope.values());
}

// typesCompatible returns true when type a and b are assignment-compatible.
// Exact equality is sufficient for most cases; "any"/empty is a wildcard.
function typesCompatible(a: string, b: string): boolean {
  if (!a || a === "any" || !b || b === "any") return true;
  return a === b;
}

function flattenSchemaProps(
  schema: JSONSchema | null | undefined,
): { name: string; type: string }[] | null {
  if (!schema) return null;
  const root = resolveSchemaRef(schema, schema);
  const props = root.properties || {};
  const entries = Object.entries(props);
  if (entries.length === 0) return null;
  return entries.map(([name, sub]) => ({
    name,
    type: schemaTypeLabel(resolveSchemaRef(sub, schema)),
  }));
}

function resolveSchemaRef(node: JSONSchema, root: JSONSchema): JSONSchema {
  if (!node.$ref) return node;
  const path = node.$ref.replace(/^#\//, "").split("/");
  let cur: any = root;
  for (const p of path) {
    if (cur == null) return node;
    cur = cur[p];
  }
  return cur || node;
}

// Section — collapsible group with a clickable header. Keeps the inspector
// scannable when many sections are open at once. Optional `right` slot for
// trailing controls (e.g. Save button) that should stay visible in the header.
function Section({
  title,
  defaultOpen = true,
  storageKey,
  right,
  hint,
  children,
}: {
  title: string;
  defaultOpen?: boolean;
  storageKey?: string;
  right?: React.ReactNode;
  hint?: React.ReactNode;
  children: React.ReactNode;
}) {
  const [open, setOpen] = useState<boolean>(() => {
    if (!storageKey) return defaultOpen;
    const v = localStorage.getItem("flowSection:" + storageKey);
    if (v === "open") return true;
    if (v === "closed") return false;
    return defaultOpen;
  });
  useEffect(() => {
    if (storageKey) localStorage.setItem("flowSection:" + storageKey, open ? "open" : "closed");
  }, [open, storageKey]);
  return (
    <div
      style={{
        border: "1px solid var(--border)",
        borderRadius: 4,
        marginBottom: 10,
        background: "var(--panel-2, transparent)",
      }}
    >
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: 6,
          padding: "6px 8px",
          cursor: "pointer",
          userSelect: "none",
        }}
        onClick={() => setOpen((v) => !v)}
      >
        <span style={{ width: 14, display: "inline-block", color: "var(--text-dim)", fontSize: 10 }}>
          {open ? "▼" : "▶"}
        </span>
        <strong style={{ fontSize: 12 }}>{title}</strong>
        {hint}
        <span style={{ flex: 1 }} />
        {right && (
          <span onClick={(e) => e.stopPropagation()} style={{ display: "flex", gap: 4 }}>
            {right}
          </span>
        )}
      </div>
      {open && <div style={{ padding: "8px 10px 10px", borderTop: "1px solid var(--border)" }}>{children}</div>}
    </div>
  );
}

// InfoHint — small (i) icon that reveals a hint on hover or click. Used to
// keep verbose helper text out of the way of the form fields.
function InfoHint({ children }: { children: React.ReactNode }) {
  const [open, setOpen] = useState(false);
  return (
    <span
      style={{ position: "relative", display: "inline-flex", alignItems: "center" }}
      onMouseEnter={() => setOpen(true)}
      onMouseLeave={() => setOpen(false)}
    >
      <button
        type="button"
        onClick={(e) => { e.stopPropagation(); setOpen((v) => !v); }}
        title="More info"
        style={{
          display: "inline-flex",
          alignItems: "center",
          justifyContent: "center",
          width: 16,
          height: 16,
          borderRadius: "50%",
          border: "1px solid var(--border-strong)",
          background: "var(--panel-2)",
          color: "var(--text-dim)",
          fontSize: 11,
          fontStyle: "italic",
          fontFamily: "serif",
          lineHeight: 1,
          cursor: "help",
          padding: 0,
        }}
      >
        i
      </button>
      {open && (
        <span
          role="tooltip"
          style={{
            position: "absolute",
            top: "100%",
            left: 0,
            marginTop: 4,
            zIndex: 50,
            maxWidth: 280,
            padding: "6px 8px",
            background: "var(--panel-2)",
            border: "1px solid var(--border-strong)",
            borderRadius: 4,
            color: "var(--text-dim)",
            fontSize: 12,
            lineHeight: 1.4,
            whiteSpace: "normal",
            boxShadow: "0 2px 8px rgba(0,0,0,0.4)",
          }}
        >
          {children}
        </span>
      )}
    </span>
  );
}

function schemaTypeLabel(s: JSONSchema): string {
  if (!s) return "any";
  if (Array.isArray(s.type)) return s.type.join(" | ");
  if (s.type === "array") {
    const item = s.items ? schemaTypeLabel(s.items) : "any";
    return `${item}[]`;
  }
  if (s.type === "object" && s.properties) {
    const names = Object.keys(s.properties);
    return names.length > 0 ? `{ ${names.join(", ")} }` : "object";
  }
  return (s.type as string) || (s.enum ? "enum" : "any");
}
