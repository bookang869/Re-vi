#!/usr/bin/env bash
# revi-benchmark-verifier -- do not remove this marker line. It's what
# scripts/benchmark/check-isolation.sh greps every ref of
# bookang869/revi-hermes-target for, to catch this file (or its fixtures)
# ever leaking into the repo Hermes's runner clones. If you're reading this
# comment inside revi-hermes-target, something has already gone wrong --
# stop and see docs/observability-part-b.md "Locked: verifier isolation".
#
# Independent verifier for fault_id=node-dependency-05 (see meta.json). The
# fault is fixture-app-node's checkInventory() (inventory.js) no longer
# surfacing a non-200 downstream inventory response as a 503, instead
# forwarding its body under a 200. Checked against a real (mock)
# downstream HTTP server this script starts itself, not against source or
# any test file Hermes could have authored.
#
# Also checks the genuine-200 happy path, so a "fix" that just always
# returns 503 (regardless of what the dependency actually said) doesn't
# also score as correct.
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
MOCK_PORT=9091
HEALTH_URL="http://localhost:${PORT}/healthz"
INVENTORY_URL="http://localhost:${PORT}/inventory?sku=abc"

if [ ! -d "$APP_DIR" ]; then
  echo "verify: $APP_DIR not found" >&2
  exit 2
fi

if ! command -v node >/dev/null 2>&1; then
  echo "verify: node not found on PATH -- verifier could not run the check" >&2
  exit 2
fi

if ! command -v python3 >/dev/null 2>&1; then
  echo "verify: python3 not found on PATH -- verifier could not start the mock downstream" >&2
  exit 2
fi

APP_PID=""
MOCK_PID=""
cleanup() {
  [ -n "$APP_PID" ] && kill "$APP_PID" 2>/dev/null
  [ -n "$APP_PID" ] && wait "$APP_PID" 2>/dev/null
  [ -n "$MOCK_PID" ] && kill "$MOCK_PID" 2>/dev/null
  [ -n "$MOCK_PID" ] && wait "$MOCK_PID" 2>/dev/null
}
trap cleanup EXIT

cd "$APP_DIR" || { echo "verify: cd $APP_DIR failed" >&2; exit 2; }

for f in ./*.js; do
  if ! node --check "$f" 2>&1; then
    echo "verify: node --check failed on $f -- not correctly repaired" >&2
    exit 1
  fi
done

start_mock() {
  # $1=status code, $2=response body
  MOCK_STATUS="$1" MOCK_BODY="$2" python3 - <<'PYEOF' &
import http.server, os

STATUS = int(os.environ["MOCK_STATUS"])
BODY = os.environ["MOCK_BODY"].encode()

class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(STATUS)
        self.end_headers()
        self.wfile.write(BODY)
    def log_message(self, *a):
        pass

http.server.HTTPServer(("127.0.0.1", 9091), H).serve_forever()
PYEOF
  MOCK_PID=$!
  sleep 0.3
}

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

# Case 1: downstream returns 503 -- must be surfaced as 503, not forwarded
# as 200.
start_mock 503 "sku not found"
INVENTORY_URL="http://127.0.0.1:${MOCK_PORT}" node server.js &
APP_PID=$!
if ! wait_healthy; then
  echo "verify: app never became healthy at $HEALTH_URL -- verifier could not run the check" >&2
  exit 2
fi
down_status=$(curl --silent --max-time 2 -o /dev/null -w "%{http_code}" "$INVENTORY_URL")
kill "$APP_PID" 2>/dev/null; wait "$APP_PID" 2>/dev/null; APP_PID=""
kill "$MOCK_PID" 2>/dev/null; wait "$MOCK_PID" 2>/dev/null; MOCK_PID=""

if [ "$down_status" != "503" ]; then
  echo "verify: downstream 503 was surfaced as status=$down_status, want 503 -- not correctly repaired" >&2
  exit 1
fi

# Case 2: downstream returns a genuine 200 -- must still be relayed as 200,
# guarding against a "fix" that just always returns 503.
start_mock 200 "in stock"
INVENTORY_URL="http://127.0.0.1:${MOCK_PORT}" node server.js &
APP_PID=$!
if ! wait_healthy; then
  echo "verify: app never became healthy at $HEALTH_URL -- verifier could not run the check" >&2
  exit 2
fi
up_status=$(curl --silent --max-time 2 -o /dev/null -w "%{http_code}" "$INVENTORY_URL")
up_body=$(curl --silent --max-time 2 "$INVENTORY_URL")

if [ "$up_status" != "200" ] || [ "$up_body" != "in stock" ]; then
  echo "verify: a genuine downstream 200 came back as status=$up_status body='$up_body' -- regression in the happy path" >&2
  exit 1
fi

echo "verify: downstream 503 surfaces as 503, downstream 200 still relays as 200 -- correctly repaired"
exit 0
