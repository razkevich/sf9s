# Round 2 — requirements & plan

Driven by the round-1 retrospective and measurements against 12 live orgs.

## R2-1 — Startup must be instant (defect, highest priority)

**Observed:** first paint took ~25 s with 12 authenticated orgs, because
`sf org list` probes every org's connection status serially. NFR3 promises
< 300 ms to interactive. Measured: `--skip-connection-status` returns in 3.2 s
(the residual is Node startup, which we can't remove).

**Requirement:** the org table appears as soon as the inventory is known;
connection status arrives afterward without blocking anything.

**Design:** two-phase load.
1. `sf org list --skip-connection-status` → render immediately, Status column
   shows a dim `checking…` placeholder.
2. In parallel, full `sf org list` → merge statuses into the existing rows by
   username, preserving cursor and filter.

Selecting an org and querying must work during phase 2. `R` re-runs both.

## R2-2 — SOQL autocomplete (the differentiator)

The reason people keep a browser tab open for SOQL is completion. sf9s already
holds a per-org describe cache, so completion costs nothing extra.

**Requirement:** `ctrl+space` (and `tab` when the cursor follows a word)
completes at the cursor:
- after `FROM ` → object API names (from the cached describeGlobal)
- inside the SELECT clause of a query whose FROM object is known → that
  object's field names, plus relationship prefixes (`Owner.`)
- after a `WHERE`/`ORDER BY` field position → same field list

Completion is a popup list under the editor: `↑↓` to move, `enter`/`tab` to
accept, `esc` to dismiss, keystrokes narrow it. Single unambiguous match
inserts directly. If the describe isn't cached yet it is fetched in the
background and the popup fills in when ready — never a blocking spinner.

**Non-goals:** full SOQL grammar parsing. A lightweight tokenizer that finds
the FROM object and the cursor's clause is enough and cannot mis-parse into
wrongness that matters (worst case: no suggestions).

## R2-3 — Get data out of a row fast

Copying a value currently requires exporting the whole result set.

**Requirement:** in query results, `y` copies the focused cell, `Y` copies the
whole row as JSON. In the record card, `y` copies the record as JSON. Toast
names what was copied and its length.

## R2-4 — Integration tier against a real emulator

Our e2e suite asserts against our own mock, so a wrong assumption about the
Salesforce API shape would be invisible. [sf-localstack](https://github.com/razkevich/sf-localstack)
emulates the REST/Tooling APIs for real.

**Requirement:** a build-tagged suite (`-tags localstack`) that runs the same
journeys against a live sf-localstack instance, skipped when the instance
isn't reachable so default `go test ./...` stays hermetic. Gaps found in the
emulator (`/limits`, Tooling `ApexLog`/`DeployRequest`, log `Body`) become a
PR upstream.

## Test plan

| Requirement | Verification |
|---|---|
| R2-1 | unit: merge-by-username preserves cursor/filter and never duplicates rows; e2e: phase-1 frame shows orgs with `checking…`, later frame shows `Connected`; manual: measure first paint against the 12-org profile |
| R2-2 | unit: tokenizer resolves the FROM object and clause for a table of realistic queries (multi-line, subqueries, aliases, mid-word cursor); unit: candidate list for each clause; e2e: type `SELECT Id FROM Acc`, complete, run |
| R2-3 | e2e: cell and row copies land in the fake clipboard with exact expected content |
| R2-4 | the localstack suite itself; must be green against a live instance |

## Out of scope for round 2

Live log tail, anonymous Apex, record editing, Homebrew tap (round 3).
