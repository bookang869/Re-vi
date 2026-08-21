#!/usr/bin/env bash
# revi-benchmark-verifier -- do not remove this marker line. It's what
# scripts/benchmark/check-isolation.sh greps every ref of
# bookang869/revi-hermes-target for, to catch this file (or its fixtures)
# ever leaking into the repo Hermes's runner clones. If you're reading this
# comment inside revi-hermes-target, something has already gone wrong --
# stop and see docs/observability-part-b.md "Locked: verifier isolation".
#
# Independent verifier for fault_id=rust-config-04 (see meta.json). The
# fault is fixture-app-rust's max_order_amount() (config.rs) falling back
# to unwrap_or(0) when MAX_ORDER_AMOUNT is set but unparsable, so an
# invalid config value silently becomes max=0 instead of falling back to
# the documented 100000 default. Checked by actually launching the binary
# with a deliberately invalid MAX_ORDER_AMOUNT and hitting the real
# endpoint, not by inspecting source or any test file Hermes could have
# authored.
#
# Also checks the over-limit case under a *valid* default config, so a
# "fix" that just always accepts (removing the check entirely) doesn't
# also score as correct.
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
VALIDATE_URL="http://localhost:${PORT}/validate-order"
BIN="$APP_DIR/target/debug/revi-fixture-app-rust"

if [ ! -d "$APP_DIR" ]; then
  echo "verify: $APP_DIR not found" >&2
  exit 2
fi

APP_PID=""
cleanup() {
  [ -n "$APP_PID" ] && kill "$APP_PID" 2>/dev/null
  [ -n "$APP_PID" ] && wait "$APP_PID" 2>/dev/null
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

wait_healthy() {
  for _ in $(seq 1 20); do
    if curl --fail --silent --max-time 1 -o /dev/null "$HEALTH_URL"; then
      return 0
    fi
    if ! kill -0 "$APP_PID" 2>/dev/null; then
      return 1
    fi
    sleep 0.5
  done
  return 1
}

# Case 1: invalid config must fall back to the default, not reject
# everything.
MAX_ORDER_AMOUNT=notanumber "$BIN" &
APP_PID=$!
if ! wait_healthy; then
  echo "verify: app never became healthy at $HEALTH_URL -- verifier could not run the check" >&2
  exit 2
fi
bad_config_result=$(curl --silent --max-time 2 "${VALIDATE_URL}?amount=50000")
kill "$APP_PID" 2>/dev/null; wait "$APP_PID" 2>/dev/null; APP_PID=""

if [ "$bad_config_result" != "accepted" ]; then
  echo "verify: with an invalid MAX_ORDER_AMOUNT, amount=50000 returned '$bad_config_result', want 'accepted' -- not correctly repaired" >&2
  exit 1
fi

# Case 2: default config must still reject a genuinely over-limit amount --
# guards against a "fix" that just always accepts.
"$BIN" &
APP_PID=$!
if ! wait_healthy; then
  echo "verify: app never became healthy at $HEALTH_URL -- verifier could not run the check" >&2
  exit 2
fi
over_limit_result=$(curl --silent --max-time 2 "${VALIDATE_URL}?amount=999999999")

if [ "$over_limit_result" != "rejected" ]; then
  echo "verify: under the default config, amount=999999999 returned '$over_limit_result', want 'rejected' -- regression in the happy path" >&2
  exit 1
fi

echo "verify: invalid config falls back to the default, and the default still rejects over-limit amounts -- correctly repaired"
exit 0
