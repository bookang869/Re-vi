#!/usr/bin/env bash
# revi-benchmark-verifier -- do not remove this marker line. It's what
# scripts/benchmark/check-isolation.sh greps every ref of
# bookang869/revi-hermes-target for, to catch this file (or its fixtures)
# ever leaking into the repo Hermes's runner clones. If you're reading this
# comment inside revi-hermes-target, something has already gone wrong --
# stop and see docs/observability-part-b.md "Locked: verifier isolation".
#
# Independent verifier for fault_id=node-config-04 (see meta.json). The
# fault is fixture-app-node's maxOrderAmount() (config.js) returning
# parseInt()'s result unchecked, so an invalid MAX_ORDER_AMOUNT parses to
# NaN and every `amount <= NaN` comparison in validateOrder is silently
# always false, rejecting every order instead of falling back to the
# documented 100000 default. Checked directly against real HTTP behavior,
# not against any test file Hermes could have authored.
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
VALIDATE_URL="http://localhost:${PORT}/validate-order"

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

MAX_ORDER_AMOUNT=not-a-number node server.js &
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

within_body=$(curl --silent --max-time 2 "${VALIDATE_URL}?amount=50000")
if [ "$within_body" != "accepted" ]; then
  echo "verify: GET /validate-order?amount=50000 with an invalid MAX_ORDER_AMOUNT returned '$within_body', want 'accepted' (fallback to default) -- not correctly repaired" >&2
  exit 1
fi

over_body=$(curl --silent --max-time 2 "${VALIDATE_URL}?amount=200000")
if [ "$over_body" != "rejected" ]; then
  echo "verify: GET /validate-order?amount=200000 with an invalid MAX_ORDER_AMOUNT returned '$over_body', want 'rejected' (still over the fallback default) -- not correctly repaired" >&2
  exit 1
fi

echo "verify: invalid MAX_ORDER_AMOUNT falls back to the 100000 default -- correctly repaired"
exit 0
