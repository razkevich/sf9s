# Retrospectives

## Round 1 → v0.1.0 (2026-07-25)

### What shipped
Seven views (orgs, query, schema, limits, meta, deploys, logs), zero-config
auth reuse, three test layers (unit, message-loop, real-TUI e2e), CI matrix on
three OSes, goreleaser packaging, README with screenshots.

### What went well
- **`sf` CLI as the auth substrate** eliminated the entire credential-storage
  problem and made "install and it works" real. Validated against 12 live orgs.
- **Direct REST for hot paths** measured 16 s (cold, CLI-mediated token) →
  278 ms (warm) for the same query. The hybrid was the right call.
- **Order-preserving JSON parse** paid off immediately: users see their SELECT
  order, and Ids/large numbers never render as `1.8e+07`.
- **Real-TUI e2e tests** (teatest + fake `sf` + mock org) caught bugs the unit
  tests structurally could not — e.g. search-on-enter never reporting its match
  position, because that only manifests as a missing rendered frame.

### What the adversarial reviews exposed (25 findings, 3 reviewers)
The pattern in the confirmed ones is instructive:

1. **Per-instance state used as a global invariant.** Generation counters were
   per-view `int`s starting at 0; destroying and rebuilding a view produced two
   instances that agreed on `gen == 1`, so a *previous org's* response could be
   rendered as the current org's. Lesson: identity for "is this response mine"
   must come from a scope that outlives the thing being identified.
2. **Two mechanisms for one concern.** A 30 s `http.Client.Timeout` silently
   overrode every 60–120 s context the UI set. Lesson: pick one authority for
   deadlines (the context) and leave the other unset.
3. **Trusting the far side of a boundary.** sObject names from `describeGlobal`
   went into filesystem paths (traversal), and log bodies went to the terminal
   unfiltered (OSC 52 clipboard rewrite). Lesson: org data is untrusted input,
   even from "your own" org — a `System.debug` of a customer-supplied field is
   an attacker-controlled string.
4. **Errors that discard working state.** A transient `sf org list` failure on
   `R` replaced a live session with the fatal startup screen. Lesson: startup
   errors and steady-state errors need different handling; never route both
   through one flag.
5. **Per-batch schema assumed stable.** Paginated results derive columns per
   response, so page 2 could carry columns page 1 lacked; appending misaligned
   cells and silently dropped data from exports.

### Process notes
- Mining the user's own shell history (`sf force:mdapi:listmetadata` was the
  single most-used command, 49×) changed the feature set: the metadata view
  wasn't in the original plan and became a headline feature.
- Three reviewers with *disjoint* briefs (correctness, UX, security) produced
  almost no overlap. A single "review this" pass would have found a fraction.
- The visual pass needed a real terminal: rendering to tmux and screenshotting
  caught chrome problems (padded logo chip breaking the top-bar band) that no
  assertion would have.

### Carried into round 2
- No integration coverage against a *real* Salesforce API implementation
  (only our own mock) — sf-localstack tier.
- Query view lacks the two things people use SOQL editors for most: field
  autocomplete and a record-count-first workflow.
- No `--help`-level docs for the CLI flags; no man page.
- Limits view is a flat list; the interesting question ("what changed since
  yesterday?") isn't answerable.
