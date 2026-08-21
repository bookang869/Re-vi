#!/usr/bin/env bash
# revi-benchmark-verifier -- do not remove this marker line. It's what
# scripts/benchmark/check-isolation.sh greps every ref of
# bookang869/revi-hermes-target for, to catch this file (or its fixtures)
# ever leaking into the repo Hermes's runner clones. If you're reading this
# comment inside revi-hermes-target, something has already gone wrong --
# stop and see docs/observability-part-b.md "Locked: verifier isolation".
#
# Independent verifier for fault_id=rust-discount-03 (see meta.json). The
# fault is fixture-app-rust's bulk_discount() (discount.rs) using
# `qty > 10` instead of `qty >= 10`, so an order of exactly 10 units
# silently skips its discount -- no crash, no error, just a wrong number.
# Checked directly against real HTTP response bodies, not against any test
# file Hermes could have authored.
#
# Checks three points (qty=9/10/11) rather than just the boundary alone, so
# a "fix" that overcorrects (e.g. discounting everything, or discounting
# qty=9 too) doesn't also score as correct.
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
DISCOUNT_URL="http://localhost:${PORT}/discount"
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

check() {
  local qty="$1" want="$2" got
  got=$(curl --silent --max-time 2 "${DISCOUNT_URL}?unit_price=100&qty=${qty}")
  if [ "$got" != "$want" ]; then
    echo "verify: qty=$qty returned '$got', want '$want' -- not correctly repaired" >&2
    return 1
  fi
  return 0
}

ok=1
check 9 "900" || ok=0
check 10 "900" || ok=0
check 11 "990" || ok=0

if [ "$ok" -ne 1 ]; then
  exit 1
fi

echo "verify: qty=9/10/11 all return the correct discounted total -- correctly repaired"
exit 0
