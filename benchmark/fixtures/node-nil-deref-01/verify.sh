#!/usr/bin/env bash
# revi-benchmark-verifier -- do not remove this marker line. It's what
# scripts/benchmark/check-isolation.sh greps every ref of
# bookang869/revi-hermes-target for, to catch this file (or its fixtures)
# ever leaking into the repo Hermes's runner clones. If you're reading this
# comment inside revi-hermes-target, something has already gone wrong --
# stop and see docs/observability-part-b.md "Locked: verifier isolation".
#
# Independent verifier for fault_id=node-nil-deref-01 (see meta.json). The
# fault is fixture-app-node's summarize() (order.js) accessing
# order.customer.name without checking whether customer is present, so
# GET /summarize throws for an Order with no Customer attached. Checked
# directly against real HTTP behavior, not against any test file Hermes
# could have authored.
#
# Checks the HTTP status code, not "does the process stay alive" -- unlike
# Go/Rust/Python, Node's http.Server has no built-in per-request exception
# isolation, so this fixture's server.js wraps every route in its own
# top-level try/catch (see fixture-app-node-baseline-v1) to keep that
# property true here too. A request that throws inside the handler comes
# back to curl as a real 500, not a dropped connection -- "still alive
# after the request" is expected either way, so the status code is what
# actually distinguishes fixed from unfixed.
#
# Known limitation (see benchmark/README.md): fixture-app-node always
# binds :8080, shared with every other fixture_app -- the harness
# serializes all verify.sh invocations process-wide for exactly this
# reason. Free :8080 (e.g. `docker compose stop gateway`) before running
# this by hand.
#
# Usage: verify.sh <path-to-checked-out-revi-hermes-target>
# Exit code (three-way, see benchmark/README.md):
#   0  -- verified correct.     independent_verification_passed = true
#   1  -- verified incorrect.   independent_verification_passed = false
#   2+ -- verifier itself did not reach a verdict (build/boot/env failure).
set -uo pipefail   # not -e: a failed check is a verdict (exit 1), not a script bug

REPO_ROOT="${1:?usage: verify.sh <path-to-checked-out-revi-hermes-target>}"
FIXTURE_APP="fixture-app-node"
APP_DIR="$REPO_ROOT/$FIXTURE_APP"
PORT=8080
HEALTH_URL="http://localhost:${PORT}/healthz"
SUMMARIZE_URL="http://localhost:${PORT}/summarize"

if [ ! -d "$APP_DIR" ]; then
  echo "verify: $APP_DIR not found" >&2
  exit 2
fi

if ! command -v node >/dev/null 2>&1; then
  echo "verify: node not found on PATH -- verifier could not run the check" >&2
  exit 2
fi

APP_PID=""
cleanup() {
  [ -n "$APP_PID" ] && kill "$APP_PID" 2>/dev/null
  [ -n "$APP_PID" ] && wait "$APP_PID" 2>/dev/null
}
trap cleanup EXIT

cd "$APP_DIR" || { echo "verify: cd $APP_DIR failed" >&2; exit 2; }

for f in ./*.js; do
  if ! node --check "$f" 2>&1; then
    echo "verify: node --check failed on $f -- not correctly repaired" >&2
    exit 1
  fi
done

node server.js &
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
