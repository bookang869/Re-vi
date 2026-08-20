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
| `base_ref` | The tag/branch in `revi-hermes-target` this fault is cut from, fixed at authoring time. Every trial (including repeats) re-cuts its working branch from this same ref, never from `main` at trial time — see "clean baseline" in the Part B doc. |
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

## What isn't decided here

Verification *results* (`independent_verification_passed` per trial) are
not written back into the Gateway's `runs` table — there's no endpoint for
that, deliberately (docs/observability-part-b.md "Locked: verifier
isolation," second `/grill-me` pass). The harness that will exist to
orchestrate trials owns that data in its own store, joined against the
Gateway's data (`GET /v1/runs/{alert_id}`) by `alert_id`/`fault_id` at
report time. Not built yet.
