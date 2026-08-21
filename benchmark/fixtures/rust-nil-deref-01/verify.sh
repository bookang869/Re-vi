#!/usr/bin/env bash
# revi-benchmark-verifier -- do not remove this marker line. It's what
# scripts/benchmark/check-isolation.sh greps every ref of
# bookang869/revi-hermes-target for, to catch this file (or its fixtures)
# ever leaking into the repo Hermes's runner clones. If you're reading this
# comment inside revi-hermes-target, something has already gone wrong --
# stop and see docs/observability-part-b.md "Locked: verifier isolation".
#
# Independent verifier for fault_id=rust-nil-deref-01 (see meta.json). The
# fault is fixture-app-rust's summarize() (order.rs) calling
# .unwrap() on a None Customer, so GET /summarize panics for an Order with
# no Customer attached. Verified directly against real HTTP behavior, not
# against source code or any test file Hermes could have authored.
#
# Deliberately checks the HTTP status code of GET /summarize, not "does the
# process stay alive" -- main.rs wraps every request in
# std::panic::catch_unwind so a per-request panic doesn't kill the process
# (mirrors Go's net/http, which recovers panics per request), so "still
# alive after the request" would pass even for the *unfixed* code and be a
# worthless check. A panicking request comes back to curl as a dropped
# connection (status "000"); only a real, completed response comes back as
# "200".
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
set -uo pipefail   # not -e: a failed build/health-check is a verdict (exit 1), not a script bug

REPO_ROOT="${1:?usage: verify.sh <path-to-checked-out-revi-hermes-target>}"
FIXTURE_APP="fixture-app-rust"
APP_DIR="$REPO_ROOT/$FIXTURE_APP"
PORT=8080
HEALTH_URL="http://localhost:${PORT}/healthz"
SUMMARIZE_URL="http://localhost:${PORT}/summarize"
BIN="$APP_DIR/target/debug/revi-fixture-app-rust"

if [ ! -d "$APP_DIR" ]; then
  echo "verify: $APP_DIR not found" >&2
  exit 2
fi

if ! command -v cargo >/dev/null 2>&1; then
  echo "verify: cargo not found on PATH -- verifier could not run the check (this machine has no Rust toolchain; run inside a container that has one, e.g. rust:1-slim)" >&2
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

status=$(curl --silent --max-time 2 -o /dev/null -w "%{http_code}" "$SUMMARIZE_URL")
if [ "$status" != "200" ]; then
  echo "verify: GET /summarize returned status=$status, want 200 -- not correctly repaired" >&2
  exit 1
fi

echo "verify: GET /summarize returned 200 -- correctly repaired"
exit 0
