#!/usr/bin/env bash
# revi-benchmark-verifier -- do not remove this marker line. It's what
# scripts/benchmark/check-isolation.sh greps every ref of
# bookang869/revi-hermes-target for, to catch this file (or its fixtures)
# ever leaking into the repo Hermes's runner clones. If you're reading this
# comment inside revi-hermes-target, something has already gone wrong --
# stop and see docs/observability-part-b.md "Locked: verifier isolation".
#
# Independent verifier for fault_id=node-badvalidation-07 (see meta.json).
# The fault is fixture-app-node's formatOrder() (format.js) accepting and
# echoing back negative amounts instead of rejecting them with 400.
# Checked directly against real HTTP behavior, not against any test file
# Hermes could have authored.
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
FORMAT_URL="http://localhost:${PORT}/format-order"

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

neg_status=$(curl --silent --max-time 2 -o /dev/null -w "%{http_code}" "${FORMAT_URL}?amount=-5")
if [ "$neg_status" != "400" ]; then
  echo "verify: GET /format-order?amount=-5 returned status=$neg_status, want 400 -- not correctly repaired" >&2
  exit 1
fi

pos_body=$(curl --silent --max-time 2 "${FORMAT_URL}?amount=5")
if [ "$pos_body" != '{"amount":5,"currency":"USD"}' ]; then
  echo "verify: GET /format-order?amount=5 returned '$pos_body', want '{\"amount\":5,\"currency\":\"USD\"}' -- regression in the happy path" >&2
  exit 1
fi

echo "verify: amount=-5 returns 400, amount=5 still returns the expected body -- correctly repaired"
exit 0
