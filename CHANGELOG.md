# Changelog

## Unreleased

- **Live Apex log tail**: `t` on the logs view watches for new debug logs and
  prepends them as they arrive; stops on `t`, on leaving the view, on org
  switch, or on error
- `sf9s -h` now introduces the tool, both flags, the config/cache locations and
  the `sf` CLI prerequisite
- `y` in the record inspector copies the record as JSON
- **SOQL autocomplete**: `tab` / `ctrl+space` completes objects after `FROM`,
  fields in `SELECT` / `WHERE` / `ORDER BY`, and relationship paths, driven by
  the org's own schema via the describe cache
- **Instant startup**: the org inventory paints before connection statuses are
  probed (which cost ~25 s with a dozen orgs) and is restored from disk cache
  on relaunch; statuses stream in and merge without moving your cursor
- Copy the focused cell (`y`) or the whole row as JSON (`Y`) from results
- `R` reloads the schema and metadata views after a transient failure
- Integration test tier against a live sf-localstack instance (`-tags localstack`)
- Fixed: keystrokes typed immediately after selecting an org were dropped
- Fixed: 25 findings from three adversarial reviews (cross-org response
  handling, hidden 30 s HTTP timeout, paginated schema drift, escape-sequence
  injection from org data, cache-path traversal, export hardening, and more)

## v0.1.0 — 2026-07-25

Initial release.

- Org cockpit: every `sf`-authenticated org with type, status, scratch expiry,
  default markers; open in browser (frontdoor), copy access token / instance URL
- SOQL query view: multi-line editor, Tooling API toggle, persisted history,
  saved-query library (`queries.yaml`), pagination, row inspector card,
  CSV/JSON export, relationship flattening with server column order
- Schema browser: fuzzy-filtered objects → typed field tables (references,
  picklist values, flags), SELECT-builder handoff to the query view
- Org limits with usage bars and thresholds
- Metadata inventory (types → components with audit fields)
- Recent deployments (Tooling `DeployRequest`)
- Apex debug logs: list, body viewer with search, delete
- k9s-style command palette (`:`), help overlay (`?`), universal `/` filtering
- Zero config: reuses `sf` CLI auth; tokens never persisted
