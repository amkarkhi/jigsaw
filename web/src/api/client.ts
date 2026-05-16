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
}

export interface FieldDef {
  name: string;
  type: string;
  required?: boolean;
  default?: unknown;
  from?: string;
  field?: string;
}

export interface FullTask {
  name: string;
  description?: string;
  version?: string;
  label?: string;
  inherits?: string;
  inputs?: FieldDef[];
  outputs?: FieldDef[];
  provider?: string;
  logic?: string;
  timeout?: number;
  retry?: number;
  fallback?: unknown;
  metadata?: Record<string, unknown>;
}

export interface TaskDetail {
  task: FullTask;
  logic_implemented: boolean;
}

export interface LogicHandler {
  name: string;
  input_schema: FieldDef[] | null;
  output_schema: FieldDef[] | null;
  used_by: string[];
}

export interface LogicResponse {
  manifest_loaded: boolean;
  handlers: LogicHandler[];
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
  me: () => get<{ authenticated: boolean; label?: string; role?: "admin" | "viewer" }>("/api/me"),
  login: (username: string, password: string) =>
    postJSON<{ ok: boolean; label?: string; role?: string }>("/api/login", { username, password }),
  logout: () => fetch("/api/logout", { method: "POST" }),
  providers: () => get<ProviderSummary[]>("/api/providers"),
  provider: (name: string) => get<ProviderDetail>(`/api/providers?name=${encodeURIComponent(name)}`),
  endpoints: () => get<EndpointSummary[]>("/api/endpoints"),
  logic: () => get<LogicResponse>("/api/logic"),
  diagnostics: () => get<Diagnostic[]>("/api/diagnostics"),
  tree: () => get<string[]>("/api/tree"),
  file: (path: string) => getText(`/api/file?path=${encodeURIComponent(path)}`),
  flowLocation: (name: string) => get<{ path: string }>(`/api/flow-location?name=${encodeURIComponent(name)}`),
  taskLocation: (name: string) => get<{ path: string }>(`/api/task-location?name=${encodeURIComponent(name)}`),
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
