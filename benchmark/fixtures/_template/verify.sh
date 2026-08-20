#!/usr/bin/env bash
# revi-benchmark-verifier -- do not remove this marker line. It's what
# scripts/benchmark/check-isolation.sh greps every ref of
# bookang869/revi-hermes-target for, to catch this file (or its fixtures)
# ever leaking into the repo Hermes's runner clones. If you're reading this
# comment inside revi-hermes-target, something has already gone wrong --
# stop and see docs/observability-part-b.md "Locked: verifier isolation".
#
# Independent verifier for fault_id=REPLACE-ME. This script never leaves
# `revi` -- it is never copied into or committed onto any branch/tag of
# revi-hermes-target, so Hermes's clone can never read it, regardless of
# what git commands Hermes runs inside its own workspace.
#
# Invoked by the harness ONLY -- never by hermes-triage.yml or the runner --
# after a benchmark trial's fix has already merged into
# revi-hermes-target@main (AUTONOMOUS mode). The harness checks out that
# merge commit itself, into its own separate working tree, and runs this
# script against it. Must not rely on Hermes's own self-authored test for
# anything -- that's the entire point of an *independent* verifier.
#
# Usage: verify.sh <path-to-checked-out-revi-hermes-target>
#   $1 -- absolute path to a clean checkout of revi-hermes-target at the
#         commit to grade (typically the merge commit for this trial).
#
# Exit code is the only signal the harness reads -- three-way, not
# pass/fail, so a broken verifier can't silently score as "Hermes failed"
# (that would corrupt the benchmark's headline correctness-rate numbers):
#   0  -- verified correct.                independent_verification_passed = true
#   1  -- verified incorrect.              independent_verification_passed = false
#   2+ -- verifier itself did not reach a verdict (setup/build/env failure
#         in the verifier's own logic, not a judgment about Hermes's fix).
#         The harness must record this as inconclusive/needs-review, not as
#         a scored failure. `set -euo pipefail` below means any unexpected
#         error here already exits non-zero and non-1 by default -- only
#         the deliberate correctness check at the bottom should ever exit 1.
# Do not print a verdict and exit 0 regardless -- the harness does not parse
# stdout, only the exit code.
set -euo pipefail

REPO_ROOT="${1:?usage: verify.sh <path-to-checked-out-revi-hermes-target>}"
FIXTURE_APP="REPLACE-ME"   # must match meta.json's fixture_app for this fault
cd "$REPO_ROOT/$FIXTURE_APP"

# REPLACE-ME: run whatever deterministic check proves expected_behavior
# (meta.json) actually holds -- a hidden test file that lives only here (not
# in revi-hermes-target), a scripted assertion against the built binary, an
# API probe against a booted instance, etc. Must not invoke the fixture
# app's own test suite as the sole check if Hermes could have authored or
# modified that suite as part of its patch.
#
# Reserve exit 1 for a deliberate "ran the check, it failed" verdict only --
# let any earlier unexpected error (missing binary, build failure, etc.)
# propagate through `set -e` with its own natural exit code instead of
# catching and re-throwing it as 1.
exit 2
