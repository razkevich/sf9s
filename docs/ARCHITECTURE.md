# sf9s — Architecture

## Overview

```
┌────────────────────────────────────────────────────────────┐
│ cmd/sf9s            flag parsing, version, wiring          │
└──────────────┬─────────────────────────────────────────────┘
               │
┌──────────────▼─────────────────────────────────────────────┐
│ internal/ui         Bubble Tea app: root model, views,     │
│                     styles, keymaps, status bar            │
└───────┬──────────────────────────┬─────────────────────────┘
        │                          │
┌───────▼────────┐        ┌────────▼────────┐
│ internal/api   │        │ internal/sfcli  │
│ Salesforce     │ tokens │ bridge to the   │
│ REST + Tooling ◄────────┤ `sf` executable │
│ (direct HTTP)  │        │ (JSON output)   │
└───────┬────────┘        └────────┬────────┘
        │                          │
┌───────▼──────────────────────────▼─────────────────────────┐
│ internal/config     XDG paths, config, saved queries,      │
│                     query history, describe cache          │
└────────────────────────────────────────────────────────────┘
```

Two data paths, chosen per operation:

- **`sf` CLI bridge** (`internal/sfcli`) for what the CLI already does well and
  where it owns state: org inventory (`org list`), credential resolution
  (`org display`), metadata listing (`force mdapi listmetadata` /
  `describemetadata`). The CLI transparently refreshes expired access tokens —
  sf9s inherits that for free, exactly like k9s inherits kubeconfig auth.
- **Direct REST/Tooling calls** (`internal/api`) for everything interactive
  (query, describe, limits, logs, deployments), because spawning a Node process
  per keystroke-level action is too slow. Tokens come from the bridge and live
  only in memory.

## Key decisions (ADR-lite)

| # | Decision | Rationale | Alternatives rejected |
|---|---|---|---|
| 1 | Go + Bubble Tea v1 (+ bubbles, lipgloss) | Battle-tested stack of gh-dash & most modern TUIs; single static binary; Elm architecture keeps async UI tractable | Rust/ratatui (slower to ship, no team familiarity); Bubble Tea v2 (younger API, fewer examples — roadmap item) |
| 2 | Reuse `sf` CLI auth instead of own OAuth | Zero-setup for the entire target audience; no credential storage liability; token refresh handled by sf | Own PKCE flow + keychain (weeks of work, security surface); reading `~/.sfdx` auth files directly (undocumented, encrypted on some platforms, breaks on CLI updates) |
| 2a | Read the token from `org display`, falling back to `org auth show-access-token` | CLI 2.136.8 (May 2026) removed credentials from `org display` and added the dedicated command. Older CLIs still answer from `org display`, so trying it first costs one process on modern CLIs and none on old ones | Requiring a minimum CLI version (excludes users we cannot see); `SF_TEMP_SHOW_SECRETS` (Salesforce has announced its removal) |
| 3 | Direct HTTP for hot-path reads | `sf data query` costs 1–3 s of Node startup per call; REST GET is ~100 ms | Shelling out for everything |
| 4 | 401 → one forced token re-resolve → retry once | Covers token expiry mid-session without loops | Proactive expiry tracking (sf doesn't expose expiry reliably) |
| 5 | Column order from parsing the SELECT clause, falling back to record keys | JSON objects lose order; users expect columns in the order they typed | Alphabetical-only (surprising) |
| 6 | `json.Number` decoding for query results | Avoids `1.8e+07` rendering of Ids/numbers | float64 default |
| 7 | Describe cache on disk (XDG cache, per org, 15 min TTL) | Schema browsing must feel instant; describes are heavy (100s of KB) | In-memory only (cold every launch) |
| 8 | Read-only v1 (sole write: apex log delete, confirmed) | Trust is the adoption blocker for org tools; destructive ops need more UX care | — |
| 9 | Name `sf9s` | Zero GitHub collisions (checked 2026-07); instantly signals "k9s for Salesforce" | s9s (taken: ClusterControl), f9s (taken), orgtop (weaker signal) |

## Package contracts

### `internal/sfcli`
- `Runner` interface: `Run(ctx, args ...string) ([]byte, error)` — real impl execs
  `sf` with `--json`; tests substitute a fake. All parsing is of documented
  `--json` envelopes (`{"status":0,"result":…}`).
- `Client.Orgs(ctx)` → `[]Org` (alias, username, org type, defaults, expiry, status)
  from `org list`.
- `Client.Credentials(ctx, usernameOrAlias)` → `Credentials{AccessToken, InstanceURL,
  APIVersion, OrgID}` from `org display`, with the token fetched separately via
  `org auth show-access-token` on CLIs that no longer expose it.
- `Client.OpenOrg` / `Client.OpenPath` — hand the browser hand-off back to the
  CLI so a session token never passes through our argv or a URL we build.
- `Client.MetadataTypes(ctx, org)` / `Client.ListMetadata(ctx, org, type)` from
  mdapi describe/list.
- Sentinel errors: `ErrCLINotFound`, `ErrNoOrgs`; rich `CLIError` carrying sf's
  message for everything else.
- Every exec sets `WaitDelay`: `sf` is a Node program that spawns helpers, and
  because output is captured through pipes, one orphaned grandchild would
  otherwise block `Run` forever — past any context deadline, since the deadline
  kills a process that has already exited.

### `internal/api`
- `TokenSource` interface `{ Credentials(ctx, force bool) (Credentials, error) }` —
  implemented by an adapter over `sfcli` with in-memory caching; `force=true`
  bypasses cache (the 401 path).
- `Client` (one per selected org): `Query`, `QueryMore`, `ToolingQuery`,
  `DescribeGlobal`, `DescribeSObject`, `Limits`, `RecentDeployments`, `ApexLogs`,
  `ApexLogBody`, `DeleteApexLog`.
- Requests are bounded by the caller's context only — a client-level timeout
  silently capped long log downloads below the deadline the UI had set.
  Redirects are refused so a bearer token cannot follow one off the instance
  host, and credentialed requests require https (or loopback, for emulators).
- `FetchOrgInfo` reads `Organization` so sf9s can tell production from a
  Developer Edition, which the CLI cannot.
- Query results: `Result{TotalSize, Done, NextRecordsURL, Columns, Rows}` — rows
  pre-flattened (`Owner.Name` dot paths, `json.Number` preserved, relationship
  sub-queries summarized as `(n rows)`).

### `internal/config`
- XDG-compliant: config `~/.config/sf9s/config.yaml`, state (history)
  `~/.local/state/sf9s/`, cache `~/.cache/sf9s/` (per-OS via `os.UserConfigDir`
  etc.).
- `SavedQueries` loaded from `queries.yaml`, written with starter examples the
  first time the picker is opened; `History` append-only with dedup, cap 500,
  serialized and written atomically.
- Cache keys are hashed: sObject and metadata type names come from the org, and
  an unhashed key would let one escape the cache directory.
- Describe cache: JSON files keyed by org ID + kind, TTL 15 min.

### `internal/ui`
- Root model holds: current org and what that org reports itself to be
  (edition, sandbox, trial — the CLI cannot tell production from Developer
  Edition), the view stack, status bar, help overlay, command palette, and one
  model per view (lazy-initialized per org).
- Navigation follows k9s: `:` command mode with aliases, `ctrl+a` for the alias
  list, numbered hotkeys switching *org* (k9s numbers switch namespace) on the
  orgs view only, and `esc` unwinding one level at a time through each view's
  `Bail()`.
- Keys are structured (`[]keyHint`) rather than a hint string, so the header
  legend and the `?` overlay cannot drift apart from what the view does.
- Views implement a narrow `view` interface (`Init`, `Update`, `View`, `Title`,
  `Keys`, `Bail`, `Capturing`); navigation is a crumb trail that truncates
  rather than looping when a view is revisited.
- Every I/O action is a `tea.Cmd` returning a typed msg; a generation counter per
  view discards stale responses (org switched mid-flight).
- Styles: lipgloss adaptive colors only (light/dark safe); honors NO_COLOR.

## Error handling

- Startup: missing `sf` → full-screen friendly message with install hint (not a
  panic); no orgs → same pattern with `sf org login web` hint.
- Per-action errors surface in the status bar (transient, styled) with the
  Salesforce error message verbatim; the view keeps its last good state.
- All exec/HTTP calls context-based; quitting or switching views cancels.

## Testing strategy

- `sfcli`: table-driven tests over recorded real `sf --json` fixtures + fake runner
  (error paths: missing binary, non-zero exit, malformed JSON).
- `api`: `httptest.Server` fixtures for every endpoint incl. 401-refresh-retry,
  pagination, SOQL error bodies, `json.Number` handling, flattening edge cases.
- `config`: temp-dir round-trips (history dedup/cap, saved queries, cache TTL).
- `ui`: pure `Update()` unit tests — feed msgs, assert model state + emitted cmds;
  no PTY needed. Smoke test via `tea.NewProgram` with `tea.WithoutRenderer`.
- Real-TUI e2e (`e2e/`): teatest drives the actual program against a fake `sf`
  binary and an in-process mock org, asserting on rendered frames. A
  build-tagged tier (`-tags localstack`) runs the same journeys against a live
  sf-localstack, which is where wrong assumptions about the real API surface.
- CI gate: `go vet` for all three GOOS on the fast runner (platform-specific
  build breaks should not wait for the Windows job), `golangci-lint`,
  `go test -race ./...` on Linux, macOS and Windows.
