# Changelog

## Unreleased

Four independent reviews — a Salesforce developer, an admin/consultant, a
product strategist and a QA engineer — test-drove the app against a mock org
and 13 real ones. This release is what they found.

### Fixed — would have broken for most users

- **Access tokens on current Salesforce CLIs.** CLI 2.136.8 removed
  credentials from `sf org display`, which sf9s depended on entirely. It now
  falls back to `org auth show-access-token`, and says what to do on CLIs that
  have neither.
- **A permanent startup hang.** `sf` spawns helper processes; capturing output
  through pipes meant one orphaned grandchild blocked forever, past every
  timeout. Bounded, and cancellation now kills the process group.
- **Typing a query could silently switch org.** Focus moved to the results
  table after a run, so the next query's keystrokes became commands — a digit
  retargeted everything at another org. Focus stays in the editor, `shift+tab`
  moves to results, and org hotkeys work only on the orgs view.
- **The help overlay was clipped** on an 80x24 terminal — no bottom border, no
  close hint, five keys missing. It scrolls now. `f1`/`f2` reach help and
  command mode from inside the editor, where `?` and `:` are query characters.

### Added

- **Deploy failure details**: `enter` lists component and Apex test failures
  with line numbers; `enter` again shows one in full with its stack trace.
- **Field detail card**: `enter` on a field shows the complete picklist, the
  field's identity and whether it is required; `y` copies the values.
- **Org detail card** (`d`): everything known about an org plus the *full*
  connection status, translated into a diagnosis and a next step.
- **Production awareness**: sf9s asks the org what edition it is and marks
  production unmistakably; sandbox/scratch/developer/local are inferred from
  the instance host immediately.
- Sortable columns (`s`), open the focused record in Salesforce (`o`),
  `:org <alias>` to reach orgs beyond the numbered nine, copy cell/row/record.

### Changed

- Exports go to `~/Downloads` (`SF9S_EXPORT_DIR` overrides) and the path stays
  on screen instead of a four-second toast.
- Credentials are resolved when you pick an org, so the first query is no
  longer billed ~9s of CLI startup.
- Query failures stay visible instead of reverting to a message identical to
  "never ran"; stale limits are labelled with the time they were last good.
- `Flags` (`ncu`/`cu`) became a `Required` column and readable badges.
- Metadata names are percent-decoded, so `(Marketing)` is findable.

## Unreleased

- **k9s-style navigation.** The header now carries an org context block, the
  current view's key legend, and numbered org hotkeys (`1`…`9`) — where k9s
  puts numbered namespaces. Command mode gained aliases (`:sc`, `:lim`, `:md`,
  `:dep`, `:sql`, `:apex`), `ctrl+a` lists every view with its aliases, and
  `:q` now quits instead of matching "query".
- `esc` bails out one level at a time (filter → card/drill-down → view), with a
  breadcrumb trail at the bottom showing how you got there.
- The header collapses to a single line on terminals too small for it.

## v0.2.0 — 2026-07-25

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
- Fixed: 34 findings across four adversarial reviews — cross-org response
  handling, a hidden 30 s HTTP timeout, paginated schema drift, escape-sequence
  injection from org data, cache-path traversal, export hardening, completion
  cursor math on wrapped lines and wide runes, deleting the wrong Apex log when
  one arrived mid-confirmation, keywords inside SOQL string literals, and a
  stale cached org outliving its authentication

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
