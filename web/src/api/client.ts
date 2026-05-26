// Centralized API client. Every call to the dashboard backend goes through
// here so we can swap auth, change bases, or generate from OpenAPI later
// without touching component code.

async function get<T>(path: string): Promise<T> {
  const res = await fetch(path);
  if (!res.ok) {
    throw new Error(`${res.status} ${res.statusText}`);
  }
  return res.json() as Promise<T>;
}

async function getText(path: string): Promise<string> {
  const res = await fetch(path);
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
  return res.text();
}

async function postJSON<T>(path: string, body: unknown): Promise<{ status: number; data: T }> {
  const res = await fetch(path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  const data = (await res.json()) as T;
  return { status: res.status, data };
}

export interface SaveResult {
  ok: boolean;
  written?: string[];
  diagnostics?: Diagnostic[];
  mode: string;
}

export interface ServerInfo {
  mode: "local" | "server";
  edit: boolean;
  config_path: string;
  server_name: string;
  service_name?: string;
  playground?: boolean;
}

export interface Overview {
  flows: number;
  tasks: number;
  providers: number;
  endpoints: number;
  logic_handlers: number;
  unimplemented_logic: number;
  manifest_loaded: boolean;
}

export interface FlowSummary {
  name: string;
  description: string;
  version: string;
  inherits: string;
  task_count: number;
}

export interface TaskSummary {
  name: string;
  description: string;
  logic: string;
  logic_implemented: boolean;
  provider: string;
  inputs: number;
  outputs: number;
  inherits: string;
  // Name of the wrapper task that intercepts this task's execution (set via
  // YAML `wrapper: { task: <name> }`). The wrapper task receives ctx.Nested
  // pointing back here. Undefined when no wrapper is bound.
  wrapped_by?: string;
}

export interface ProviderSummary {
  name: string;
  type: string;
  version?: string;
  init_mode: string;
  pool_size: number;
  user_count: number;
}

export interface ProviderDetail {
  provider: {
    name: string;
    type: string;
    version?: string;
    init_mode: string;
    pool_size?: number;
    config?: Record<string, unknown>;
    metadata?: Record<string, unknown>;
  };
  used_by: string[];
}

export interface EndpointSummary {
  name: string;
  path: string;
  method: string;
  description: string;
  flows: { sub: number; flow: string }[];
  // Scope keys the HTTP layer seeds into ExecutionContext before the flow
  // runs. The validator uses these to clear first-task input checks; the UI
  // surfaces them as chips on the endpoint card.
  request_params?: string[];
}

// FieldDef is retained for legacy UI surfaces (TaskDetail form, Tasks list)
// that still render the pre-refactor task shape. New code should consume
// JSONSchema directly.
export interface FieldDef {
  name: string;
  type: string;
  required?: boolean;
  default?: unknown;
  from?: string;
  field?: string;
}

// JSONSchema mirrors the subset of github.com/invopop/jsonschema we render.
// We only consume what we render: type, properties, required, description,
// items (for arrays), and nested objects.
export interface JSONSchema {
  type?: string | string[];
  description?: string;
  properties?: Record<string, JSONSchema>;
  required?: string[];
  items?: JSONSchema;
  additionalProperties?: boolean | JSONSchema;
  enum?: unknown[];
  default?: unknown;
  $ref?: string;
  $defs?: Record<string, JSONSchema>;
  definitions?: Record<string, JSONSchema>;
  format?: string;
  title?: string;
}

export interface FullTask {
  name: string;
  description?: string;
  version?: string;
  // Legacy fields surfaced by older UI screens. The backend no longer
  // populates them after the schema-driven refactor; they remain typed
  // here so screens that still reference them keep compiling.
  label?: string;
  inputs?: FieldDef[];
  outputs?: FieldDef[];
  inherits?: string;
  provider?: string;
  logic?: string;
  timeout?: number;
  retry?: number;
  params?: Record<string, unknown>;
  fallback?: unknown;
  metadata?: Record<string, unknown>;
  wrapper?: {
    task: string;
    params?: Record<string, unknown>;
  };
}

export interface TaskDetail {
  task: FullTask;
  logic_implemented: boolean;
}

export interface LogicHandler {
  name: string;
  description?: string;
  version?: string;
  input_schema: JSONSchema | null;
  output_schema: JSONSchema | null;
  params_schema?: JSONSchema | null;
  // Input fields the logic author marked `jig:"skippable"` — only these
  // can appear in a TaskRef's bind.skip list.
  skippable_inputs?: string[];
  used_by: string[];
}

export interface LogicResponse {
  manifest_loaded: boolean;
  handlers: LogicHandler[];
}

export interface TaskTrace {
  name: string;
  label?: string;
  status: string;
  started_at: string;
  completed_at?: string;
  duration_ms: number;
  inputs: Record<string, unknown>;
  outputs: Record<string, unknown>;
  error?: string;
  provider?: string;
  logic?: string;
  fallback_used?: boolean;
  skipped?: boolean;
}

export interface PlaygroundResult {
  ok: boolean;
  flow: string;
  status: string;
  tasks: TaskTrace[];
  result?: unknown;
  request_id?: string;
  error?: string;
}

export interface UserRow {
  username: string;
  role: string;
  email?: string;
  access: string[];
  created_at: string;
}

export interface Diagnostic {
  Severity: "error" | "warning";
  File: string;
  Message: string;
}

export const api = {
  info: () => get<ServerInfo>("/api/info"),
  overview: () => get<Overview>("/api/overview"),
  flows: () => get<FlowSummary[]>("/api/flows"),
  flow: (name: string) => get<unknown>(`/api/flows?name=${encodeURIComponent(name)}`),
  tasks: () => get<TaskSummary[]>("/api/tasks"),
  task: (name: string) => get<TaskDetail>(`/api/tasks?name=${encodeURIComponent(name)}`),
  taskUsage: (name: string) => get<string[]>(`/api/task-usage?name=${encodeURIComponent(name)}`),
  me: () => get<{ authenticated: boolean; label?: string; role?: "admin" | "viewer"; access?: string[] }>("/api/me"),
  login: (username: string, password: string) =>
    postJSON<{ ok: boolean; label?: string; role?: string }>("/api/login", { username, password }),
  logout: () => fetch("/api/logout", { method: "POST" }),
  authInfo: () => get<{ password: boolean; gitlab: boolean }>("/api/auth-info"),
  getGitSettings: () =>
    get<{
      base_url: string;
      project: string;
      default_branch: string;
      author_name: string;
      author_email: string;
      pat_configured: boolean;
      secret_key_set: boolean;
    }>("/api/git/settings"),
  saveGitSettings: (s: {
    base_url: string;
    project: string;
    default_branch: string;
    author_name: string;
    author_email: string;
    pat?: string;
    clear_pat?: boolean;
  }) => postJSON<{ ok: boolean }>("/api/git/settings", s),
  listUsers: () =>
    get<{ users: UserRow[]; resources: string[] }>("/api/users"),
  createUser: (u: { username: string; password: string; role: string; email?: string; access?: string[] }) =>
    postJSON<{ ok: boolean }>("/api/users", u),
  updateUser: (username: string, patch: { role?: string; email?: string; access?: string[] }) =>
    fetch(`/api/users/${encodeURIComponent(username)}`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(patch),
    }).then(async (r) => ({ status: r.status, data: (await r.json().catch(() => ({}))) as { ok?: boolean } })),
  deleteUser: (username: string) =>
    fetch(`/api/users/${encodeURIComponent(username)}`, { method: "DELETE" })
      .then(async (r) => ({ status: r.status, data: (await r.json().catch(() => ({}))) as { ok?: boolean } })),
  playgroundRun: (flow: string, inputs: Record<string, unknown>, headers?: Record<string, string>, sub?: number) =>
    postJSON<PlaygroundResult>("/api/playground/run", { flow, inputs, headers, sub }),
  playgroundRunYAML: (flowYAML: string, inputs: Record<string, unknown>, headers?: Record<string, string>, sub?: number) =>
    postJSON<PlaygroundResult>("/api/playground/run", { flow_yaml: flowYAML, inputs, headers, sub }),
  playgroundTask: (task: string, inputs: Record<string, unknown>, headers?: Record<string, string>, params?: Record<string, unknown>, sub?: number) =>
    postJSON<PlaygroundResult>("/api/playground/task", { task, inputs, headers, params, sub }),
  playgroundLogic: (logic: string, inputs: Record<string, unknown>, headers?: Record<string, string>, params?: Record<string, unknown>, sub?: number) =>
    postJSON<PlaygroundResult>("/api/playground/logic", { logic, inputs, headers, params, sub }),
  gitPush: (branch: string, commitMessage: string) =>
    postJSON<{ ok: boolean; branch?: string; output?: string; browse_url?: string; error?: string }>(
      "/api/git/push",
      { branch, commit_message: commitMessage },
    ),
  providers: () => get<ProviderSummary[]>("/api/providers"),
  provider: (name: string) => get<ProviderDetail>(`/api/providers?name=${encodeURIComponent(name)}`),
  endpoints: () => get<EndpointSummary[]>("/api/endpoints"),
  logic: () => get<LogicResponse>("/api/logic"),
  diagnostics: () => get<Diagnostic[]>("/api/diagnostics"),
  tree: () => get<string[]>("/api/tree"),
  file: (path: string) => getText(`/api/file?path=${encodeURIComponent(path)}`),
  flowLocation: (name: string) => get<{ path: string }>(`/api/flow-location?name=${encodeURIComponent(name)}`),
  taskLocation: (name: string) => get<{ path: string }>(`/api/task-location?name=${encodeURIComponent(name)}`),
  endpointLocation: (name: string) => get<{ path: string }>(`/api/endpoint-location?name=${encodeURIComponent(name)}`),
  loadLayout: (flow: string) => get<Record<string, { x: number; y: number }>>(`/api/layout?flow=${encodeURIComponent(flow)}`),
  saveLayout: (flow: string, layout: Record<string, { x: number; y: number }>) =>
    postJSON<{ ok: boolean }>(`/api/layout?flow=${encodeURIComponent(flow)}`, layout),
  saveFiles: (files: Record<string, string>) =>
    postJSON<SaveResult>("/api/files", { files }),
  downloadBundle: (files: Record<string, string>) =>
    fetch("/api/bundle", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ files }),
    }),
};
