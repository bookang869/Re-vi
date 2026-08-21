#!/usr/bin/env bash
# revi-benchmark-verifier -- do not remove this marker line. It's what
# scripts/benchmark/check-isolation.sh greps every ref of
# bookang869/revi-hermes-target for, to catch this file (or its fixtures)
# ever leaking into the repo Hermes's runner clones. If you're reading this
# comment inside revi-hermes-target, something has already gone wrong --
# stop and see docs/observability-part-b.md "Locked: verifier isolation".
#
# Independent verifier for fault_id=python-nil-deref-01 (see meta.json).
# The fault is fixture-app-python's summarize() (order.py) accessing
# order.customer.name without checking whether customer is present, so
# GET /summarize raises AttributeError for an Order with no Customer
# attached. Checked directly against real HTTP behavior, not against
# order_test.py (a pre-existing regression test that already happens to
# encode this fix -- present since before Part B, see benchmark/README.md
# for why this fault is not "blind" the same way its Go/Rust/Node
# counterparts are).
#
# Checks the HTTP status code, not "does the process stay alive" --
# Python's http.server/socketserver recovers a per-request exception
# without killing the process (BaseServer.handle_error logs a traceback
# and moves on), so "still alive after the request" would pass even for
# the *unfixed* code. A request that raised inside do_GET comes back to
# curl as a dropped connection (status "000"); only a real completed
# response comes back as a real status code.
#
# Known limitation (see benchmark/README.md): fixture-app-python always
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
FIXTURE_APP="fixture-app-python"
APP_DIR="$REPO_ROOT/$FIXTURE_APP"
PORT=8080
HEALTH_URL="http://localhost:${PORT}/healthz"
SUMMARIZE_URL="http://localhost:${PORT}/summarize"

if [ ! -d "$APP_DIR" ]; then
  echo "verify: $APP_DIR not found" >&2
  exit 2
fi

if ! command -v python3 >/dev/null 2>&1; then
  echo "verify: python3 not found on PATH -- verifier could not run the check" >&2
  exit 2
fi

APP_PID=""
cleanup() {
  [ -n "$APP_PID" ] && kill "$APP_PID" 2>/dev/null
  [ -n "$APP_PID" ] && wait "$APP_PID" 2>/dev/null
}
trap cleanup EXIT

cd "$APP_DIR" || { echo "verify: cd $APP_DIR failed" >&2; exit 2; }

if ! python3 -m py_compile *.py 2>&1; then
  echo "verify: python3 -m py_compile failed -- not correctly repaired" >&2
  exit 1
fi

python3 server.py &
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
