# Schema versioning

Every config file under `tasks/`, `flows/`, `providers/`, and `endpoints/` may
declare a top-level `schema_version` integer. The framework binary refuses to
load files whose version falls outside the range it supports, so a config
push cannot silently break a running service.

```yaml
schema_version: 1
tasks:
  - name: query_builder
    ...
```

Files that omit `schema_version` are treated as version `1`. This is purely
for backward compatibility with configs written before this contract existed —
new files should always set it explicitly.

## The contract

The framework exports two constants in `pkg/config`:

| Constant                    | Meaning                                       |
| --------------------------- | --------------------------------------------- |
| `CurrentSchemaVersion`      | What new files should declare.                |
| `MinSupportedSchemaVersion` | Oldest version this binary still accepts.     |

A file loads iff `MinSupportedSchemaVersion ≤ schema_version ≤ CurrentSchemaVersion`.

## Deploy order

The contract is designed so a config repo and a code repo can be deployed
independently without taking prod down:

1. **Introduce a new shape.** Bump `CurrentSchemaVersion` to `N` in the
   framework. Keep `MinSupportedSchemaVersion` at `N-1`. Release the binary.
   At this point production runs on the new binary but still reads the old
   `N-1` configs — nothing changes for the running service.

2. **Migrate configs.** Update the config repo files to `schema_version: N`.
   The mounted config volume reloads via the file watcher; the loader keeps
   the previously loaded config if any file is rejected, so a malformed push
   cannot drop endpoints.

3. **Retire the old shape.** In a later release, bump
   `MinSupportedSchemaVersion` to `N`. Any leftover `N-1` files are now
   rejected at load.

## Failure modes

- **Cold start, unsupported file** → server exits with an error naming the
  file, the file's version, and the supported range. The operator must fix
  the config or roll the binary back.
- **Hot reload, unsupported file** → the new config is rejected; the
  previously loaded config keeps serving. The rejection is logged.
- **Missing `schema_version`** → treated as `1`. Update the file when you
  next touch it.
