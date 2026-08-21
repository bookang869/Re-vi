# Benchmark fixtures

Fault corpus metadata and independent verifiers for
[docs/observability-part-b.md](../docs/observability-part-b.md). The actual
broken code lives in `bookang869/revi-hermes-target` (a separate repo); this
directory holds only what must never be visible from inside that repo's
clone — the answer key. See that doc's "Locked: verifier isolation" for why.

## Layout

```
benchmark/fixtures/<fault_id>/
  meta.json     one fixed record per fault, authored once
  verify.sh     independent check, invoked by the harness only
```

`fixtures/_template/` is a scaffold, not a real fault — copy it to start a
new one.

## `meta.json`

| field | meaning |
|---|---|
| `fault_id` | Stable identifier, assigned once, never reused or reassigned (Scale, docs/observability-part-b.md). |
| `fault_type` | One of the six categories in docs/observability-part-b.md's Scale section. |
| `language_runtime` | `go` \| `rust` \| `python` \| `node`. |
| `fixture_app` | Which `fixture-app-*` subdirectory of `revi-hermes-target` this fault touches. Exactly one — faults are never stacked (isolation rule). |
| `base_ref` | The tag/branch in `revi-hermes-target` this fault is cut from, fixed at authoring time. Every trial (including repeats) re-cuts its working branch from this same ref, never from `main` at trial time — see "clean baseline" in the Part B doc. **The freeze only covers fixture content, not shared tooling** (`scripts/`, `.github/workflows/`): a tag cut before a later fix to those lands on `main` inherits the old, broken tooling (found running `go-nil-deref-01`'s first live trial, 2026-08-21 — its tag predated a `resolve-boot-command.sh` fix and reproduced a bug that fix was supposed to have already closed). If a trial fails on something that looks like stale tooling rather than the fault itself, check whether `base_ref` predates a relevant `revi-hermes-target` fix; if so, cut a new tag at `origin/main`'s tip (after confirming via `git diff --stat -- fixture-app-*/` that nothing in the fault's own fixture app changed) rather than force-moving the original — see `docs/observability-part-b.md`'s "First live trial" section for the `-v2` tag precedent. |
| `service_name` / `error_summary` | Sent as `labels.service` / `annotations.summary` on every trial's synthetic alert. Must stay identical across repeat trials of the same fault — that's what makes the flap lock serialize them correctly (repeat-trial identity rule). `alert_id`/`trace_id` vary per trial instead; the harness generates those, not this file. `service_name` must be one of `revi-hermes-target/scripts/resolve-boot-command.sh`'s recognized values (`fixture-app-go`/`-rust`/`-node`/`-python`) — AUTONOMOUS mode's smoke test resolves boot command/health URL from it via a fixed table, not codebase inspection, and an unrecognized value fails that step before Hermes gets a chance to fix anything (found authoring `go-nil-deref-01`). Add a new case there if a fault ever needs a service name outside that table. |
| `expected_behavior` | One sentence: what correct behavior looks like, written from the outside (observable behavior), not the bug's internals or how to fix it. Recorded on the `runs` table for later reporting. |

`experiment_id` is deliberately not in this file — it identifies a
benchmark *run* (e.g. one full 96-trial pass), not a fault, so the harness
assigns it at dispatch time.

## Dispatching a trial

Two more fields ride along on the synthetic alert itself (`labels.ref`,
`labels.merge_target`), not stored in `meta.json` since they're derived,
not authored — see docs/observability-part-b.md "Locked: dispatch routing
to a fault's isolated ref" for the full story of why these exist:

- **`ref` = this fault's `base_ref`, unchanged, every trial.** Tells the
  runner to check out the fault's fixed tag instead of `main`.
- **`merge_target` = `benchmark/<fault_id>`.** Before dispatching *any*
  trial (including repeats), the harness must force-reset this branch to
  `base_ref` on the remote — it's what `autonomous-promote.sh` merges into
  instead of `main`, so repeat trials never collide with each other's
  independently-worded fixes to the same lines.

## `verify.sh`

Contract: `verify.sh <path-to-checked-out-revi-hermes-target>`. Exit code is
the only signal the harness reads (not stdout) — and it's three-way, not
plain pass/fail, so a broken verifier can't silently score as "Hermes
failed" and corrupt the benchmark's headline correctness-rate numbers:

| exit code | meaning |
|---|---|
| `0` | Verified correct. `independent_verification_passed = true`. |
| `1` | Verified incorrect. `independent_verification_passed = false`. Reserved for a *deliberate* correctness verdict only. |
| `2` (or any other non-0/1 code) | The verifier itself didn't reach a verdict — a setup/build/environment failure in the verifier's own logic, not a judgment about Hermes's fix. The harness must record this as inconclusive/needs-review, never as a scored failure. |

Two rules that exist to keep verification actually independent:

- **Never rely on Hermes's own self-authored test as the sole check.**
  Hermes writes both the patch and its own test; a self-graded test proves
  Hermes believes it's correct, not that it is. Enforced by review only —
  no mechanical check for this one.
- **Never let this script, or anything it depends on, be committed onto a
  branch/tag of `revi-hermes-target`.** The runner's clone of that repo is
  the only thing Hermes ever sees. If the check needs a hidden fixture
  (input file, golden output, etc.), keep that fixture in this directory
  too and reference it by a path relative to this script — never copy it
  into the target repo, even temporarily. **Mechanically checked**, not
  just documented: every `verify.sh` carries a `revi-benchmark-verifier`
  marker line (don't remove it), and
  `scripts/benchmark/check-isolation.sh <path-to-local-clone>` scans every
  ref of `revi-hermes-target` for that marker (and for any bare
  `verify.sh` filename, in case the marker itself was stripped) — including
  refs where it was later reverted, since the blob stays reachable via
  history until an actual `gc`/`prune`. Run it before dispatching any trial
  and before merging any fixture-authoring commit in that repo. A human
  authoring 32 of these by hand will eventually forget a rule that only
  lives in a comment; this doesn't rely on remembering.
- **A verifier that health-checks a fixed port before hitting the app under
  test is trusting that nothing else on the machine answers that port.**
  Found 2026-08-21 running `go-nil-deref-01`'s first live trial: a stray
  local `gateway` container was still bound to the same port
  `fixture-app-go` uses, so the verifier's health check passed against the
  *Gateway* instead of the freshly-built fixture binary, and the real check
  (`GET /summarize`) then failed against the wrong process — a silent
  false-negative, not an honest exit-1 verdict. Free whatever port a
  fixture app binds before running its verifier locally; the harness should
  eventually pre-flight-check this rather than relying on the operator to
  remember (not built yet).

## Running the harness

`benchmark/harness/` (a separate Go module —
`github.com/bookang869/Re-vi/benchmark/harness`, intentionally standalone
rather than a package inside the Gateway module) automates the dispatch ->
poll -> verify -> record loop described above and in
docs/observability-part-b.md's "Locked: harness/pipeline interaction" and
"Locked: staged rollout" sections. From the pipeline's point of view a
trial it dispatches looks exactly like a real alert — same
`POST /v1/alerts`, same Bearer secret, no lower-level shortcut into NATS or
`repository_dispatch`.

```
cd benchmark/harness
go build ./...
go test ./...

# env (REVI_WEBHOOK_SECRET required, rest default to sensible local values — see config.go):
export REVI_GATEWAY_URL=http://localhost:8080          # or the Oracle VPS's tailnet address
export REVI_WEBHOOK_SECRET=...                          # same secret the Gateway itself uses
export REVI_HARNESS_TARGET_CLONE=$HOME/revi-hermes-target   # needs a working, push-authenticated "origin"

go run . stage1                        # 1 trial x 32 faults, parallel, prints an experiment_id
go run . stage2 -experiment <id>       # remaining 2 trials x 32 faults, only if stage1's gate passed
go run . trial -fault <fault_id>       # a single ad hoc trial, e.g. while authoring a new fixture
go run . report -experiment <id>       # re-print a summary from a past run's results file
```

Two things it does that aren't obvious from the commands above:

- **Every `verify.sh` invocation is globally serialized** (verify.go), not
  just per fault or per fixture_app. Every `fixture-app-*` boots on a
  hardcoded `:8080` (`revi-hermes-target/scripts/resolve-boot-command.sh`
  has no port override for any of the four languages), so two verifiers
  racing for that port — even across two different fixture_apps — would
  reproduce the same silent false-negative the first live trial hit from an
  unrelated stray process on the same port (see "First live trial" in
  docs/observability-part-b.md). This costs some wall-clock time (verifiers
  queue behind each other) in exchange for never needing to reason about
  which fixture_apps happen to share a port.
- **`independent_verification_passed` lives only in this harness's own
  per-experiment JSON file** (`benchmark/harness/results/<experiment_id>.json`,
  gitignored), never written back to the Gateway — see "What isn't decided
  here" below. A trial whose outcome never reached `MERGED` records
  `verification_outcome: "not_applicable"`, not a scored failure — there's
  nothing to independently verify if nothing merged.

## What isn't decided here

Verification *results* (`independent_verification_passed` per trial) are
not written back into the Gateway's `runs` table — there's no endpoint for
that, deliberately (docs/observability-part-b.md "Locked: verifier
isolation," second `/grill-me` pass). The harness (`benchmark/harness/`,
built — see "Running the harness" above) owns that data in its own store
instead, joined against the Gateway's data (`GET /v1/runs/{alert_id}`) by
`alert_id`/`fault_id` at report time.
