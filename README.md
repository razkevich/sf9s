<div align="center">

# ⚡ sf9s

**k9s for Salesforce.** A fast, keyboard-driven terminal cockpit for every org
you're logged into — query, schema, limits, metadata, deployments and debug
logs, without leaving your terminal or configuring anything.

[![ci](https://github.com/razkevich/sf9s/actions/workflows/ci.yml/badge.svg)](https://github.com/razkevich/sf9s/actions/workflows/ci.yml)
![go](https://img.shields.io/badge/go-1.26-00ADD8?logo=go&logoColor=white)
![license](https://img.shields.io/badge/license-MIT-blue)

<img src="docs/img/orgs.png" alt="sf9s orgs view, showing a production org marked" width="850">

</div>

## Why

The `sf` CLI is powerful but one-shot: every question about an org is another
command and another wall of JSON. The daily loop — *which orgs am I in? what's
in this object? run that query again, grab a token for curl, what just got
deployed, why did that Apex blow up* — deserves the k9s treatment: one binary,
zero config, single keystrokes.

sf9s reuses the auth you already have in the Salesforce CLI, the same way k9s
reuses your kubeconfig. If `sf org list` works, `sf9s` works.

## Install

```bash
# Homebrew (coming soon) / releases
# grab a binary from https://github.com/razkevich/sf9s/releases

# or with Go
go install github.com/razkevich/sf9s/cmd/sf9s@latest
```

Requires the [Salesforce CLI](https://developer.salesforce.com/tools/salesforcecli)
on your PATH with at least one authenticated org (`sf org login web`).

## Autocomplete

Press `tab` (or `ctrl+space`) and sf9s completes from your org's real schema:
objects after `FROM`, fields inside `SELECT` / `WHERE` / `ORDER BY`, and
relationship paths — `Owner.` then `tab` again lists the target object's
fields. Unambiguous prefixes insert directly; ambiguous ones show a picker
with each candidate's type. Suggestions come from the describe cache, so they
are instant once an object has been touched, and a request made while the
schema is still downloading is answered when it arrives.

<img src="docs/img/complete.png" alt="sf9s SOQL autocomplete" width="850">

## It tells you when you are in production

The `sf` CLI cannot tell a production tenant from a Developer Edition — both
are simply "not a sandbox" — so every org looks alike in `sf org list`. sf9s
asks the org what it is and marks it: a red `PROD` badge, the real edition,
`PRODUCTION` in the list, and a warning when you switch in. Sandboxes, scratch
orgs, Developer Editions and local emulators are recognised from the instance
host immediately, before any API call.

Acting on the wrong org is the costliest mistake this tool can help you avoid,
so nothing you type can change which org you are pointed at: the number keys
switch org only on the orgs view, and they never renumber themselves.

## Views

| view | what you get |
|---|---|
| **orgs** | every authenticated org: edition, status, scratch expiry, defaults. `d` explains a failing connection in plain English, `o` opens the org in the browser (via `sf org open`), `y` copies an access token, `Y` the instance URL |
| **query** | multi-line SOQL editor with **autocomplete** (`tab`), history (`ctrl+p/n`), saved-query library (`ctrl+s`), Tooling API toggle (`ctrl+t`), pagination (`m`), row inspector (`enter`), copy cell/row (`y`/`Y`), CSV/JSON export (`e`/`E`) |
| **schema** | fuzzy-searchable objects → fields with types, reference targets and whether they are required; `enter` opens a field card with the *complete* picklist, `c` builds a SELECT for the query view, `y` copies API names |
| **limits** | org limits sorted by usage, with thresholds that go amber at 75% and red at 90% |
| **meta** | metadata inventory: 200+ types → components with *last modified by/at* — "who touched this layout?" in three keystrokes |
| **deploys** | recent deployments; `enter` lists the component and Apex test failures with line numbers, `enter` again shows one in full with its stack trace |
| **logs** | Apex debug logs: browse, open, search inside a log (`/`, `n/N`), **tail** new logs live (`t`), delete |

Every table filters with `/` and sorts with `s`. Exports land in `~/Downloads`
(override with `SF9S_EXPORT_DIR`).



## Navigation (k9s conventions)

If you know k9s, you already know this. The header shows your org context on
the left, what the current view can do in the middle, and your orgs on
numbered hotkeys — the same place k9s puts numbered namespaces.

```
:query  :schema  :limits  :meta  :deploys  :logs  :orgs      command mode
:sc  :lim  :md  :dep  :sql  :apex                            aliases (ctrl+a lists them all)
:q                                                           quit
:org <alias>                                                 switch to any org
1 … 9                                                        switch org (orgs view)
esc                                                          bail out one level
? (f1 while typing)                                          keys for this view
/                                                            filter any table
h j k l   g G   pgup/pgdn                                    move, jump, page
```

`esc` unwinds one thing at a time — a filter, then an open card or drill-down,
then the view itself — and the breadcrumb at the bottom shows where you are
(`orgs › schema › limits`). `q` does the same, and quits from the orgs view.

Run `sf9s -o my-alias` to land directly on a specific org, and `sf9s -h` for
the full introduction.

## Saved queries

The first time you open the picker (`ctrl+s`) sf9s writes a starter library
you can edit. `sf9s -h` prints the exact path for your machine — it is
`~/.config/sf9s/queries.yaml` on Linux and
`~/Library/Application Support/sf9s/queries.yaml` on macOS. Add your own:

```yaml
queries:
  - name: Hot accounts
    query: SELECT Id, Name, Owner.Name FROM Account WHERE LastActivityDate = LAST_N_DAYS:7
  - name: Flow versions
    query: SELECT DefinitionId, VersionNumber, Status FROM Flow ORDER BY LastModifiedDate DESC
    tooling: true
```

They're org-agnostic and appear in the `ctrl+s` picker, run on `enter`.

<img src="docs/img/deploys.png" alt="sf9s deploy failure details" width="850">

## Design notes

- **Zero config, zero credentials stored.** Tokens are resolved through the
  `sf` CLI on demand (`org display`, falling back to `org auth
  show-access-token` on CLIs that no longer expose them), cached in memory
  only, and never written to disk.
  The only file sf9s ever writes into an org is nothing; the only org write at
  all is deleting an Apex log, behind a confirm.
- **Fast where it counts.** Interactive reads (query/describe/limits) go
  straight to the REST API with a cached token — ~100–300 ms round trips —
  while org inventory and metadata listing delegate to the CLI that already
  owns that logic. Describes are disk-cached for instant schema browsing.
- **It tells you when you are in production.** The `sf` CLI cannot distinguish
  a production tenant from a Developer Edition, so sf9s asks the org itself and
  marks it — a red `PROD` badge, the real edition, and a warning when you
  switch in. Acting on the wrong org is the costliest mistake this tool can
  help you avoid.
- **Honest tables.** Query columns come back in *your* SELECT order (raw JSON
  order-preserving parse), numbers aren't mangled through float64, child
  subqueries summarize as `(n rows)`, nulls render empty.

Architecture, requirements and testing strategy live in [docs/](docs/).

## Testing

Four layers. Unit tests per package; message-loop tests for every view; an
end-to-end suite that boots the **real TUI** (via
[teatest](https://github.com/charmbracelet/x)) against a fake `sf` binary and
an in-process mock org, asserting on rendered terminal frames; and an
integration tier that runs the same journeys against a live
[sf-localstack](https://github.com/razkevich/sf-localstack) — a real Salesforce
API emulator — so wrong assumptions about the API surface there instead of in
front of a user.

```bash
go test ./...                                  # everything hermetic, no org needed
docker run -p 8080:8080 razkevich/sf-localstack
go test -tags localstack ./e2e/                # against the emulator
```

That last tier paid for itself immediately: it caught keystrokes being dropped
when you typed straight after selecting an org — a bug our own mock had been
masking.

## Roadmap

- Anonymous Apex execution
- Record editing / DML behind explicit safeguards
- Bulk API job monitor; SOSL search
- Homebrew tap, Bubble Tea v2 migration

## License

MIT © Alex Razkevich
