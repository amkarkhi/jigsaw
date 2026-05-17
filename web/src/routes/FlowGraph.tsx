import { useCallback, useEffect, useMemo, useState } from "react";
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
import { api, Diagnostic, FlowSummary, TaskSummary } from "../api/client";
import { autoLayout, Canvas, CanvasEdge, CanvasNode, decompile, layoutKey, safeCompile } from "../graph/dag";
import { Flow, FlowFile, TaskRef } from "../graph/types";
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
  selected: boolean;
  isStart: boolean;
  isEnd: boolean;
  hasError?: boolean;
  hasWarning?: boolean;
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

  const [nodes, setNodes, onNodesChange] = useNodesState<NodeData>([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState([]);
  const { screenToFlowPosition } = useReactFlow();

  const [history, setHistory] = useState<UndoSnapshot[]>([]);
  const [future, setFuture] = useState<UndoSnapshot[]>([]);

  const dirty = history.length > 0;
  const blocker = useUnsavedGuard(dirty);
  // Discard-edits confirm for the YAML-relock case (separate from navigation).
  const [yamlDiscardConfirm, setYamlDiscardConfirm] = useState(false);

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

        setFilePath(path);
        setFlow(stripLayoutFromFlow(f)); // canonical state holds no layout; save() re-embeds it
        setOverrides(nodeOverrides);
        setNodes(canvasToRFNodes(canvas));
        setEdges(canvasToRFEdges(canvas));
        setHistory([]);
        setFuture([]);
      } catch (e) {
        setError((e as Error).message);
      }
    })();

    api.tasks().then(setPalette).catch(() => {});
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

  // Reapply visual flags to nodes/edges every render.
  const styledNodes = useMemo(
    () =>
      nodes.map((n) => ({
        ...n,
        data: {
          ...(n.data as NodeData),
          selected: n.id === selectedNode,
          isStart: startEnd.starts.has(n.id),
          isEnd: startEnd.ends.has(n.id),
          hasError: validation.problemNodes.has(n.id),
          hasWarning: validation.warnNodes.has(n.id) && !validation.problemNodes.has(n.id),
        },
      })),
    [nodes, selectedNode, startEnd, validation],
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
    };
  }, [nodes, edges, overrides]);

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
    const merged: Flow = { ...flow, tasks: applied };
    setYamlDraft(yaml.dump({ flows: [merged] }, { lineWidth: 100, noRefs: true }));
  }, [nodes, edges, flow, yamlUnlocked, overrides]);

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

  function setNodeOverrides(nodeId: string, next: TaskRef["overrides"]) {
    commit(() =>
      setOverrides((cur) => ({
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
    const layout: Record<string, { x: number; y: number }> = {};
    for (const n of canvas.nodes) {
      layout[layoutKey(n)] = n.position;
    }
    const merged: Flow = {
      ...flow,
      tasks: applied,
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
        setFlow(stripLayoutFromFlow(f));
        setNodes(canvasToRFNodes(canvas));
        setEdges(canvasToRFEdges(canvas));
        setOverrides(nodeOverrides);
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
        setFlow(stripLayoutFromFlow(f));
        setNodes(canvasToRFNodes(canvas));
        setEdges(canvasToRFEdges(canvas));
        setOverrides(nodeOverrides);
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
      // Build the layout map once: keyed by taskName + label so a task
      // placed multiple times in the flow keeps distinct positions.
      const layout: Record<string, { x: number; y: number }> = {};
      for (const n of canvas.nodes) {
        layout[layoutKey(n)] = n.position;
      }
      // Layout lives in a sidecar (.jigsaw/layouts/<flow>.json), not the YAML.
      // Strip any layout that older saves embedded in metadata so the YAML
      // gets cleaned up the next time the user saves.
      const merged: Flow = { ...stripLayoutFromFlow(flow), tasks: applied };
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
          gridTemplateColumns: showRight ? "1fr 400px" : "1fr",
          gap: 16,
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
                onChangeOverrides={(next) => setNodeOverrides(selectedNode, next)}
                onChangeLabel={(label) => setNodeLabel(selectedNode, label)}
                onRenameBranchSegment={(prefix, next) => renameBranchPathSegment(prefix, next)}
                onDelete={() => deleteNode(selectedNode)}
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
                        if (!yamlMirrorsGraph(yamlDraft, flow, nodes, edges, overrides)) {
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
  onChangeOverrides,
  onChangeLabel,
  onRenameBranchSegment,
  onDelete,
}: {
  nodeId: string;
  data: NodeData;
  taskInfo: TaskSummary | undefined;
  overrides: TaskRef["overrides"];
  onChangeOverrides: (next: TaskRef["overrides"]) => void;
  onChangeLabel: (label: string) => void;
  onRenameBranchSegment: (prefix: string[], next: string) => void;
  onDelete: () => void;
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
): boolean {
  if (!flow) return true;
  const canvas = rfToCanvas(nodes, edges);
  const compiled = safeCompile(canvas);
  if (!compiled.ok) return draft.trim() === "";
  const applied = applyOverrides(compiled.tasks, canvas, overrides);
  const expected = yaml.dump({ flows: [{ ...flow, tasks: applied }] }, { lineWidth: 100, noRefs: true });
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
    <div style={{ marginBottom: 12 }}>
      <div style={{ display: "flex", alignItems: "center", marginBottom: 6 }}>
        <strong style={{ flex: 1 }}>Task parameters</strong>
        <button
          className="btn btn-primary"
          onClick={save}
          disabled={!dirty || busy}
        >
          {busy ? "Saving…" : "Save task"}
        </button>
      </div>
      {flash && <div className="diag" style={{ borderLeftColor: "var(--accent)", marginBottom: 8 }}>{flash}</div>}
      {error && <div className="diag error" style={{ marginBottom: 8 }}>{error}</div>}

      <div style={{ display: "grid", gridTemplateColumns: "max-content 1fr", gap: "8px 12px", marginBottom: 8 }}>
        <label className="meta">label</label>
        <input className="input" value={task.label} onChange={(e) => patch("label", e.target.value)} placeholder="(optional flow-local name)" />

        <label className="meta">description</label>
        <input className="input" value={task.description} onChange={(e) => patch("description", e.target.value)} />

        <label className="meta">version</label>
        <input className="input" value={task.version} onChange={(e) => patch("version", e.target.value)} placeholder="e.g. 1.0.0" />

        <label className="meta">timeout (ms)</label>
        <input
          className="input" type="number" min={0}
          value={task.timeout === "" ? "" : task.timeout}
          onChange={(e) => patch("timeout", e.target.value === "" ? "" : Number(e.target.value))}
        />

        <label className="meta">retry</label>
        <input
          className="input" type="number" min={0}
          value={task.retry === "" ? "" : task.retry}
          onChange={(e) => patch("retry", e.target.value === "" ? "" : Number(e.target.value))}
        />

        <label className="meta">logic</label>
        <input className="input" value={task.logic} onChange={(e) => patch("logic", e.target.value)} />

        <label className="meta">provider</label>
        <input className="input" value={task.provider} onChange={(e) => patch("provider", e.target.value)} placeholder="(optional)" />

        <label className="meta">inherits</label>
        <input className="input" value={task.inherits} onChange={(e) => patch("inherits", e.target.value)} placeholder="(optional)" />
      </div>

      <div className="meta">
        Edits write to <code>{filePath || "tasks/…"}</code>. Inputs/outputs are
        not yet editable here — use the raw editor for those.
      </div>
    </div>
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
    <div>
      <div style={{ display: "flex", alignItems: "center", marginBottom: 8 }}>
        <strong style={{ flex: 1 }}>Overrides</strong>
        <button className="btn" onClick={add}>+ Add</button>
      </div>
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
    </div>
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
  const cls = [
    "gnode",
    "task",
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
        {data.hasError && <span className="chip err">!</span>}
        {data.hasWarning && !data.hasError && <span className="chip warn">!</span>}
      </div>
      <div className="gnode-title">{data.taskName}</div>
      {data.label && (
        <div className="gnode-sub" style={{ color: "var(--accent)" }}>
          @{data.label}
        </div>
      )}
      <Handle type="source" position={Position.Bottom} className="port port-bottom" />
    </div>
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

