# sf9s — Requirements

**sf9s** is a terminal UI for Salesforce orgs, in the spirit of k9s for Kubernetes:
a single, fast, keyboard-driven cockpit over the orgs you have already authenticated
with the `sf` CLI.

## Problem statement

The `sf` CLI is powerful but strictly one-shot: every question about an org
(`org list`, `org display`, `data query`, `force:mdapi:listmetadata`, …) is a separate
command with JSON output that humans then eyeball or pipe through `jq`. Day-to-day org
work — "which orgs am I logged into?", "what's in this object?", "run this query I run
every week", "grab a token for a curl call", "what just got deployed?" — has no
interactive surface short of opening Setup in a browser. VS Code extensions exist but
require an editor and a project context. There is no maintained general-purpose
terminal UI for Salesforce (checked July 2026; nearest neighbor `salesforce/sf-pi` is a
read-only query explorer embedded in a coding agent).

## Personas

| Persona | Situation | What they need from sf9s |
|---|---|---|
| **Developer** | Lives in the terminal, juggles scratch orgs + sandboxes + a dev hub | Fast org switching, SOQL with history, schema lookup while writing code, access tokens for curl/REST tinkering, deploy status, debug logs |
| **Admin / RevOps** | Manages a small set of production/sandbox orgs | Data spot-checks via SOQL, org limits monitoring, metadata inventory ("who changed this layout last?"), record peeking |
| **Consultant / ISV support** | Authenticated into many customer orgs at once | The org list as home base; per-org context always visible to prevent "wrong org" mistakes; saved query library reusable across orgs |

Evidence used: interviews are out of scope for a v1; requirements were grounded in
(a) real shell-history mining of a working Salesforce ISV engineer (metadata list/describe
and deploy status dominated, followed by org auth/list and data queries), and (b) the
feature sets users praise in k9s/lazygit/gh-dash (single-keystroke actions, visible
context, zero configuration to start).

## Functional requirements

### FR0 — Never let the user act on the wrong org unknowingly

The costliest mistake this tool can enable is running something against
production. The `sf` CLI cannot distinguish a production tenant from a
Developer Edition (both are simply "not a sandbox"), so sf9s must ask the org
itself and say so unmistakably; must infer sandbox/scratch/developer/local
from the instance host immediately, before any API call; must never let a
keystroke intended as text change which org is targeted; and must keep the
numbering of org hotkeys stable so an accidental press is not random.

### FR1 — Org cockpit (home view)
- List all orgs known to the local `sf` CLI: alias, username, org type
  (prod/sandbox/scratch/dev hub), connected status, instance URL, API version,
  scratch-org expiry.
- Indicate default org and default dev hub.
- Actions: select as working org (Enter/space), open in browser (`o`, via
  `sf org open` so no token passes through sf9s), copy access token (`y`), copy
  instance URL (`Y`), inspect the org and diagnose a failing connection (`d`),
  sort (`s`), refresh list (`R`).
- Org selection sets a global "current org" shown in the header at all times,
  with its true edition and a production warning.

### FR2 — SOQL query view
- Multi-line query editor; Ctrl+Enter (or configurable) executes against current org.
- Results in a scrollable, horizontally navigable table; nested relationship fields
  flattened (`Account.Name`); aggregate queries supported.
- Automatic pagination via `queryMore` (fetch next page on demand, show
  `fetched/total`).
- Query history: persisted across sessions, navigable, per-user (not per-org),
  deduplicated.
- Saved queries: named library in a user-editable YAML file; picker UI; ships with
  useful examples (recent users, API-enabled users, recent Apex classes). Org-agnostic
  by default. This is also the extension point for company-specific workflows
  (e.g. an ISV's custom-object queries) without touching core.
- Tooling API toggle (`t`) to run the same editor against `/tooling/query`.
- Export current result set to JSON or CSV file (`e`), path announced in status bar.
- Errors (MALFORMED_QUERY etc.) shown inline with the API's message and kept on
  screen until the next successful run — a failure must never look like an
  empty result or like nothing having been run.
- Completion (`tab`) for objects and fields, driven by the describe cache.

### FR3 — Schema browser
- List all SObjects (describeGlobal) with fuzzy filter; show label, custom flag,
  key prefix.
- Drill into an object: fields table with API name, type (incl. reference targets,
  picklist values, formula), label, nillable/createable/updateable flags.
- Actions: copy field API name (`y`), generate `SELECT <all fields> FROM X` into the
  query view (`q`), fuzzy-filter fields (`/`).
- Describe results cached per org with TTL to keep navigation instant.

### FR4 — Org detail & limits
- Show `/limits` for the current org with usage bars (API requests, data/file storage,
  bulk, streaming), plus org identity (id, edition via `Organization` query, user).
- Highlight limits above 75% (warn) and 90% (critical).

### FR5 — Metadata inventory
- Browse metadata types (describeMetadata via `sf` CLI), drill into a type to list
  components with full name, last modified by/date, manageable state.
- Fuzzy filter; copy component name.
- Rationale: `force:mdapi:listmetadata` was the single most-used command in mined
  history — answering "what's in this org and who touched it last?".

### FR6 — Recent deployments
- List recent metadata deployments (Tooling API `DeployRequest`): status, who, when,
  components/tests counts; drill in for failure details.

### FR7 — Apex debug logs
- List ApexLog entries (Tooling API): user, operation, status, length, time.
- View a log body in a pager with search; delete logs (with confirm).

### FR8 — Global UX
- k9s-style command palette (`:` to jump between views: `orgs`, `query`, `schema`,
  `limits`, `meta`, `deploys`, `logs`), `?` help overlay with all keybindings,
  `q`/Esc to go back, Ctrl+C always exits cleanly.
- Status bar: current org (alias + username + org type), current view, transient
  messages (copied!, exported to …, errors).
- All I/O asynchronous with spinners — the UI never blocks; slow calls cancellable
  by navigating away.
- Works over SSH; no mouse required (mouse optional for scroll).

## Non-functional requirements

- **NFR1 Zero config**: if `sf` CLI is installed and has authenticated orgs, `sf9s`
  works with no setup. Clear, friendly error if `sf` is missing or has no orgs.
- **NFR2 Read-only by default**: v1 performs no writes to any org except explicit
  user actions (delete apex log). No DML, no metadata deploys.
- **NFR3 Speed**: startup to interactive < 300 ms (org list may stream in after);
  cached describes render instantly; all network calls have timeouts.
- **NFR4 Security**: never write tokens to disk or logs; tokens live only in process
  memory and the clipboard when explicitly requested.
- **NFR5 Portability**: macOS + Linux + Windows builds (amd64/arm64); single static
  binary; no runtime deps beyond `sf` CLI on PATH.
- **NFR6 Quality gate**: unit tests for every package, `go vet` + `golangci-lint`
  clean, race-detector clean, CI on every push.
- **NFR7 Terminal citizenship**: adapts to light/dark terminals, degrades gracefully
  on narrow windows, respects NO_COLOR.

## Out of scope for v1 (roadmap)

- Editing records / DML, metadata deploy/retrieve from the TUI
- Creating trace flags so there is something to tail
- Bulk API job monitoring, Event Monitoring
- SOSL search; cross-org record/schema diff
- Auth flows (device/web login) — delegated to `sf org login`
- Bubble Tea v2 migration
- Plugin system

## Success criteria

1. A Salesforce developer with `sf` installed runs one `brew install`/`go install`
   command, types `sf9s`, and is browsing their org within seconds — nothing to
   configure.
2. The five most common daily lookups (orgs, query, field lookup, limits, last
   deploy) each take ≤ 3 keystrokes from launch.
3. No crashes on: no sf CLI, no orgs, expired org, revoked token, malformed query,
   0-row results, 10k-row results, 5k-field describes, tiny terminal windows.
4. Test suite green with `-race`; lint clean; CI passing; documented keybindings.
