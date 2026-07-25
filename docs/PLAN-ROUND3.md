# Round 3 — requirements & plan

## Retrospective going in

Round 2 delivered autocomplete and instant startup. What the round exposed:

- **Async state changes lose input.** Selecting an org through a `tea.Cmd`
  meant the next keystrokes reached the previous view. Any state change the
  user immediately types into must be applied in the same `Update` call.
- **A mock that's too permissive hides bugs.** Our mock answered any query
  containing `FROM Account`, so a dropped keystroke still "worked". Test
  doubles should be as strict as the real thing about inputs.
- **Portability needs CI, not intent.** The Windows job caught an `.exe`
  suffix assumption that no amount of local testing would have.

## R3-1 — Live Apex log tail

`sf apex tail` is how Salesforce developers watch a debug session; it needs a
terminal, a trace flag and a separate window. sf9s already lists and reads
logs, so tailing is a small step with high daily value.

**Requirement:** `t` on the logs view starts tailing. New logs are detected by
polling `ApexLog` (2 s interval — the Tooling API has no streaming for this)
and appended to the top of the list with a `NEW` marker; the newest log's body
opens automatically if the user is already viewing a body. `t` again stops.
The status bar shows `tailing` while active. Tailing stops automatically on
org switch, on navigating away, and on error (reporting it once).

**Non-goals:** creating trace flags (sf9s doesn't write config), streaming API.

## R3-2 — `--help` that explains the app

`sf9s -h` currently prints bare Go flag output. It should introduce the tool,
list flags with meaning, name the config locations, and state the `sf` CLI
requirement — the first thing a new user sees.

## R3-3 — Copy a record from the inspector card

The card shows one record; `y` should copy it as JSON, matching the results
table's `Y`. Consistency: the same key means "copy what I'm looking at".

## R3-4 — Release v0.2.0

Tag, verify the release workflow produces binaries for all six
platform/arch pairs, and confirm the archive contents.

## Test plan

| Requirement | Verification |
|---|---|
| R3-1 | unit: tail poll appends only unseen ids, marker applied, stops on org switch/navigate/error; e2e: start tail, mock emits a new log, assert it appears with the marker and that stopping ends polling |
| R3-2 | unit: help text mentions the sf requirement, every flag, and the config paths |
| R3-3 | e2e: open card, `y`, assert clipboard holds the record JSON |
| R3-4 | release job green; `tar -tzf` on one archive lists the binary, LICENSE, README |
