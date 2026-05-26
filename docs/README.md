# Jigsaw documentation

A short index of everything under `docs/`. Pick the section that matches what
you're trying to do.

## Guides — how to use Jigsaw

Walkthroughs and operator-facing docs. Start here.

- [GETTING_STARTED.md](guides/GETTING_STARTED.md) — installation, your first flow, end-to-end tutorial.
- [QUICK_REFERENCE.md](guides/QUICK_REFERENCE.md) — CLI commands, YAML schema, and common patterns in one page.
- [PACKAGE_USAGE.md](guides/PACKAGE_USAGE.md) — using Jigsaw as a library inside another Go program.
- [EXTERNAL_USAGE.md](guides/EXTERNAL_USAGE.md) — end-to-end example of wiring Jigsaw into an external service.
- [UI_GUIDE.md](guides/UI_GUIDE.md) — overview of the two UIs (legacy webui + the configuration dashboard).
- [DASHBOARD_FEATURES.md](guides/DASHBOARD_FEATURES.md) — feature surface and operator setup for the dashboard.
- [TESTING_SSO.md](guides/TESTING_SSO.md) — verifying GitLab OAuth login end-to-end.
- [REWRITE_WITH_JIGSAW.md](guides/REWRITE_WITH_JIGSAW.md) — drop-in prompt for using an AI agent to migrate an existing service onto Jigsaw.

## Reference — how Jigsaw works

Architectural deep-dives, schemas, and runtime behaviour.

- [ARCHITECTURE.md](reference/ARCHITECTURE.md) — system design, key components, and execution model.
- [ERD.md](reference/ERD.md) — entity-relationship diagram and data model.
- [parallel-execution.md](reference/parallel-execution.md) — design reference for parallel task blocks.
- [VERSIONING.md](reference/VERSIONING.md) — how flow / task / provider versions flow through execution.
- [WRAPPER_PATTERN.md](reference/WRAPPER_PATTERN.md) — generic task wrappers (caching, metrics, retry, etc.).
- [LOGIC_VALIDATION.md](reference/LOGIC_VALIDATION.md) — registration checks and validation rules.

## Design — proposals & RFCs

Documents that explain *why* something is the way it is.

- [CONFIG_MANAGER.md](design/CONFIG_MANAGER.md) — RFC v2 for the configuration manager.
- [CONFIG_TOOLING.md](design/CONFIG_TOOLING.md) — `jigsaw check`, `jigsaw fmt`, `dump-symbols`, `lsp`.

## Archive — historical / superseded

Kept for context. Not authoritative; may reference paths or behaviour that
has since changed.

- [HANDOFF.md](archive/HANDOFF.md) — task-wrapper + `Engine.InvokeTask` work handoff.
- [SUMMARY.md](archive/SUMMARY.md) — early project summary (largely overlaps the root README).
- [WRAPPER_IMPLEMENTATION_SUMMARY.md](archive/WRAPPER_IMPLEMENTATION_SUMMARY.md) — implementation log for the wrapper pattern feature.

---

The repo-root [README](../README.md) and [CHANGELOG](../CHANGELOG.md) live
where the ecosystem expects them; they are not under `docs/`.
