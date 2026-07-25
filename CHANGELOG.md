# Changelog

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
