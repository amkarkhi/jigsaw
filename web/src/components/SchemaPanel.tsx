import { JSONSchema } from "../api/client";

// SchemaPanel renders a read-only summary of a JSONSchema's top-level
// properties (name + type + description + required flag). Originally lived
// in the Logic page; extracted so the Task page can render the same view
// for its linked logic's input/output schema (tasks no longer carry their
// own I/O — the logic does).

interface FieldRow {
  name: string;
  type: string;
  required: boolean;
  description?: string;
}

export function SchemaPanel({
  title,
  schema,
  emptyText,
  tone,
  skippable,
}: {
  title: string;
  schema: JSONSchema | null | undefined;
  emptyText?: string;
  tone: "in" | "out" | "param";
  skippable?: string[];
}) {
  const fields = expandTopLevelFields(schema);
  const skipSet = new Set(skippable ?? []);
  const skippableCount = fields.filter((f) => skipSet.has(f.name)).length;
  const accent =
    tone === "in" ? "#4a9eff" : tone === "out" ? "#7ab87a" : "var(--text-dim)";
  return (
    <div
      style={{
        border: "1px solid var(--border)",
        borderRadius: 4,
        padding: "10px 12px",
        background: "var(--panel-2)",
      }}
    >
      <div
        style={{
          fontWeight: 600,
          color: "var(--text)",
          marginBottom: 8,
          fontSize: 12,
          textTransform: "uppercase",
          letterSpacing: 0.5,
          borderLeft: `3px solid ${accent}`,
          paddingLeft: 6,
        }}
      >
        {title}
        <span style={{ marginLeft: 6, color: "var(--text-dim)", fontWeight: 400, textTransform: "none", letterSpacing: 0 }}>
          ({fields.length})
        </span>
        {skippableCount > 0 && (
          <span
            title="Inputs marked `jig:&quot;skippable&quot;` may be omitted per task ref via bind.skip"
            style={{
              marginLeft: 8,
              fontWeight: 400,
              fontSize: 10,
              textTransform: "none",
              letterSpacing: 0,
              color: "var(--text-dim)",
              border: "1px dashed var(--border)",
              borderRadius: 3,
              padding: "1px 6px",
            }}
          >
            {skippableCount} skippable
          </span>
        )}
      </div>
      {fields.length === 0 ? (
        <div className="meta">{emptyText ?? "—"}</div>
      ) : (
        <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
          {fields.map((f) => (
            <FieldRowView key={f.name} field={f} skippable={skipSet.has(f.name)} />
          ))}
        </div>
      )}
    </div>
  );
}

function FieldRowView({ field, skippable }: { field: FieldRow; skippable?: boolean }) {
  return (
    <div
      style={{
        display: "grid",
        gridTemplateColumns: "auto 1fr",
        gap: "2px 10px",
        fontSize: 12,
      }}
    >
      <div style={{ fontFamily: "var(--font-mono)", color: "var(--text)" }}>
        {field.name}
        {field.required && <span style={{ color: "var(--danger, #c84)" }}> *</span>}
      </div>
      <div style={{ color: "var(--text-dim)", fontFamily: "var(--font-mono)", display: "flex", alignItems: "center", gap: 6 }}>
        <code>{field.type}</code>
        {skippable && (
          <span
            title='Marked `jig:"skippable"` — flows may omit this field via bind.skip; logic receives the Go zero value.'
            style={{
              fontFamily: "var(--font-sans, inherit)",
              fontSize: 10,
              color: "var(--text-dim)",
              border: "1px dashed var(--border)",
              borderRadius: 3,
              padding: "0 5px",
            }}
          >
            skippable
          </span>
        )}
      </div>
      {field.description && (
        <div style={{ gridColumn: "1 / -1", color: "var(--text-dim)", fontSize: 11 }}>
          {field.description}
        </div>
      )}
    </div>
  );
}

export function hasFields(schema: JSONSchema | null | undefined): boolean {
  if (!schema) return false;
  const root = resolveRef(schema, schema);
  return !!root.properties && Object.keys(root.properties).length > 0;
}

export function expandTopLevelFields(schema: JSONSchema | null | undefined): FieldRow[] {
  if (!schema) return [];
  const root = resolveRef(schema, schema);
  const props = root.properties || {};
  const required = new Set(root.required || []);
  return Object.entries(props).map(([name, sub]) => {
    const resolved = resolveRef(sub, schema);
    return {
      name,
      type: jsonSchemaTypeLabel(resolved),
      required: required.has(name),
      description: resolved.description,
    };
  });
}

function resolveRef(node: JSONSchema, root: JSONSchema): JSONSchema {
  if (!node.$ref) return node;
  const path = node.$ref.replace(/^#\//, "").split("/");
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  let cur: any = root;
  for (const p of path) {
    if (cur == null) return node;
    cur = cur[p];
  }
  return cur || node;
}

function jsonSchemaTypeLabel(s: JSONSchema): string {
  if (!s) return "any";
  if (Array.isArray(s.type)) return s.type.join(" | ");
  if (s.type === "array") {
    const item = s.items ? jsonSchemaTypeLabel(s.items) : "any";
    return `${item}[]`;
  }
  if (s.type === "object" && s.properties) {
    const names = Object.keys(s.properties);
    return names.length > 0 ? `{ ${names.join(", ")} }` : "object";
  }
  return (s.type as string) || (s.enum ? "enum" : "any");
}
