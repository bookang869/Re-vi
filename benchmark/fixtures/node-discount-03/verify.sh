#!/usr/bin/env bash
# revi-benchmark-verifier -- do not remove this marker line. It's what
# scripts/benchmark/check-isolation.sh greps every ref of
# bookang869/revi-hermes-target for, to catch this file (or its fixtures)
# ever leaking into the repo Hermes's runner clones. If you're reading this
# comment inside revi-hermes-target, something has already gone wrong --
# stop and see docs/observability-part-b.md "Locked: verifier isolation".
#
# Independent verifier for fault_id=node-discount-03 (see meta.json). The
# fault is fixture-app-node's bulkDiscount() (discount.js) applying the
# 10% bulk discount only to qty > 10 instead of qty >= 10. Checked
# directly against real HTTP behavior, not against any test file Hermes
# could have authored.
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
DISCOUNT_URL="http://localhost:${PORT}/discount"

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

ten_body=$(curl --silent --max-time 2 "${DISCOUNT_URL}?unit_price=100&qty=10")
if [ "$ten_body" != "900" ]; then
  echo "verify: GET /discount?qty=10 returned '$ten_body', want '900' -- not correctly repaired" >&2
  exit 1
fi

eleven_body=$(curl --silent --max-time 2 "${DISCOUNT_URL}?unit_price=100&qty=11")
if [ "$eleven_body" != "990" ]; then
  echo "verify: GET /discount?qty=11 returned '$eleven_body', want '990' -- regression above the threshold" >&2
  exit 1
fi

nine_body=$(curl --silent --max-time 2 "${DISCOUNT_URL}?unit_price=100&qty=9")
if [ "$nine_body" != "900" ]; then
  echo "verify: GET /discount?qty=9 returned '$nine_body', want '900' -- regression below the threshold" >&2
  exit 1
fi

echo "verify: qty=10 returns 900, qty=11 still returns 990, qty=9 still returns 900 -- correctly repaired"
exit 0
