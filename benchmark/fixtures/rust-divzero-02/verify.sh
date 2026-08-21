#!/usr/bin/env bash
# revi-benchmark-verifier -- do not remove this marker line. It's what
# scripts/benchmark/check-isolation.sh greps every ref of
# bookang869/revi-hermes-target for, to catch this file (or its fixtures)
# ever leaking into the repo Hermes's runner clones. If you're reading this
# comment inside revi-hermes-target, something has already gone wrong --
# stop and see docs/observability-part-b.md "Locked: verifier isolation".
#
# Independent verifier for fault_id=rust-divzero-02 (see meta.json). The
# fault is fixture-app-rust's divide_share() (divide.rs) dividing by
# parts without checking for zero, so GET /divide-share?parts=0 panics
# (Rust's integer division panics on a zero divisor). Checked directly
# against real HTTP behavior, not against any test file Hermes could have
# authored.
#
# Checks the HTTP status code, not "does the process stay alive" -- main.rs
# wraps every request in std::panic::catch_unwind so a per-request panic
# doesn't kill the process, so "still alive after the request" would pass
# even for the *unfixed* code. A panicking request comes back to curl as a
# dropped connection (status "000"); only a real completed response comes
# back as a real status code. Also re-checks the non-zero happy path
# (parts=4) so a "fix" that just stubs out the whole handler doesn't score
# as correct.
#
# Known limitation (see benchmark/README.md): fixture-app-rust always binds
# :8080, shared with every other fixture_app -- the harness serializes all
# verify.sh invocations process-wide for exactly this reason. Free :8080
# (e.g. `docker compose stop gateway`) before running this by hand.
#
# Usage: verify.sh <path-to-checked-out-revi-hermes-target>
# Exit code (three-way, see benchmark/README.md):
#   0  -- verified correct.     independent_verification_passed = true
#   1  -- verified incorrect.   independent_verification_passed = false
#   2+ -- verifier itself did not reach a verdict (build/boot/env failure).
set -uo pipefail   # not -e: a failed check is a verdict (exit 1), not a script bug

REPO_ROOT="${1:?usage: verify.sh <path-to-checked-out-revi-hermes-target>}"
FIXTURE_APP="fixture-app-rust"
APP_DIR="$REPO_ROOT/$FIXTURE_APP"
PORT=8080
HEALTH_URL="http://localhost:${PORT}/healthz"
DIVIDE_URL="http://localhost:${PORT}/divide-share"
BIN="$APP_DIR/target/debug/revi-fixture-app-rust"

if [ ! -d "$APP_DIR" ]; then
  echo "verify: $APP_DIR not found" >&2
  exit 2
fi

cleanup() {
  [ -n "${APP_PID:-}" ] && kill "$APP_PID" 2>/dev/null
  [ -n "${APP_PID:-}" ] && wait "$APP_PID" 2>/dev/null
}
trap cleanup EXIT

cd "$APP_DIR" || { echo "verify: cd $APP_DIR failed" >&2; exit 2; }

if ! cargo build 2>&1; then
  echo "verify: cargo build failed -- not correctly repaired" >&2
  exit 1
fi
if [ ! -x "$BIN" ]; then
  echo "verify: expected binary $BIN not found after a successful build -- verifier could not run the check" >&2
  exit 2
fi

"$BIN" &
APP_PID=$!

BOOTED=0
for _ in $(seq 1 20); do
  if curl --fail --silent --max-time 1 -o /dev/null "$HEALTH_URL"; then
    BOOTED=1
    break
  fi
  if ! kill -0 "$APP_PID" 2>/dev/null; then
    break
  fi
  sleep 0.5
done
if [ "$BOOTED" -ne 1 ]; then
  echo "verify: app never became healthy at $HEALTH_URL -- verifier could not run the check" >&2
  exit 2
fi

zero_status=$(curl --silent --max-time 2 -o /dev/null -w "%{http_code}" "${DIVIDE_URL}?total=100&parts=0")
if [ "$zero_status" != "400" ]; then
  echo "verify: GET /divide-share?parts=0 returned status=$zero_status, want 400 -- not correctly repaired" >&2
  exit 1
fi

if ! kill -0 "$APP_PID" 2>/dev/null; then
  echo "verify: process died after the parts=0 request -- not correctly repaired" >&2
  exit 1
fi

happy_body=$(curl --silent --max-time 2 "${DIVIDE_URL}?total=100&parts=4")
if [ "$happy_body" != "25" ]; then
  echo "verify: GET /divide-share?total=100&parts=4 returned '$happy_body', want '25' -- regression in the happy path" >&2
  exit 1
fi

echo "verify: parts=0 returned 400, parts=4 still returns 25 -- correctly repaired"
exit 0
