#!/usr/bin/env bash
# revi-benchmark-verifier -- do not remove this marker line. It's what
# scripts/benchmark/check-isolation.sh greps every ref of
# bookang869/revi-hermes-target for, to catch this file (or its fixtures)
# ever leaking into the repo Hermes's runner clones. If you're reading this
# comment inside revi-hermes-target, something has already gone wrong --
# stop and see docs/observability-part-b.md "Locked: verifier isolation".
#
# Independent verifier for fault_id=go-nil-deref-01 (see meta.json). The
# fault is fixture-app-go's Summarize() dereferencing Order.Customer
# without a nil check, so GET /summarize panics for an Order with no
# Customer attached. Verified directly against the real HTTP behavior, not
# against source code or any test file Hermes could have authored --
# checked by hand against both the original bug and a plausible fix before
# this was written (see the commit introducing this file).
#
# Deliberately checks the HTTP status code of GET /summarize, not "does the
# process stay alive" -- Go's net/http recovers a per-request panic without
# killing the process, so "still alive after the request" would pass even
# for the *unfixed* code and be a worthless check. A crashed/panicking
# request comes back to curl as a dropped connection (status "000"); only a
# real, completed response comes back as "200".
#
# Known limitation, not solved here: fixture-app-go always binds :8080
# (main.go has no port override), so this cannot run concurrently with
# another fixture-app-go verification on the same machine. The harness must
# serialize verify.sh invocations for this fixture_app, even if it dispatches
# the underlying trials themselves in parallel -- see benchmark/README.md.
#
# Usage: verify.sh <path-to-checked-out-revi-hermes-target>
# Exit code (three-way, see benchmark/README.md):
#   0  -- verified correct.     independent_verification_passed = true
#   1  -- verified incorrect.   independent_verification_passed = false
#   2+ -- verifier itself did not reach a verdict (build/boot/env failure).
set -uo pipefail   # not -e: a failed build/health-check is a verdict (exit 1), not a script bug

REPO_ROOT="${1:?usage: verify.sh <path-to-checked-out-revi-hermes-target>}"
FIXTURE_APP="fixture-app-go"
APP_DIR="$REPO_ROOT/$FIXTURE_APP"
PORT=8080
HEALTH_URL="http://localhost:${PORT}/healthz"
SUMMARIZE_URL="http://localhost:${PORT}/summarize"
BIN="$(mktemp -t revi-verify-go-nil-deref-01-XXXXXX)"

if [ ! -d "$APP_DIR" ]; then
  echo "verify: $APP_DIR not found" >&2
  exit 2
fi

cleanup() {
  [ -n "${APP_PID:-}" ] && kill "$APP_PID" 2>/dev/null
  [ -n "${APP_PID:-}" ] && wait "$APP_PID" 2>/dev/null
  rm -f "$BIN"
}
trap cleanup EXIT

cd "$APP_DIR" || { echo "verify: cd $APP_DIR failed" >&2; exit 2; }

if ! go build -o "$BIN" . 2>&1; then
  echo "verify: go build failed -- not correctly repaired" >&2
  exit 1
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
