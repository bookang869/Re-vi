#!/usr/bin/env bash
# PLAN Phase 5.3 -- full-loop rehearsal runbook. Each scenario is a function;
# `main` dispatches by name. Encodes exactly what was validated manually
# against the real local stack + bookang869/revi-hermes-scratch on
# 2026-08-09/10 (see PLAN.md's 5.3 entry for the run log and findings).
#
# Requires: REVI_GITHUB_OWNER/REVI_GITHUB_REPO/REVI_WEBHOOK_SECRET from
# revi/.env, `gh` authenticated against the account with access to the
# scratch repo (export GH_TOKEN=$(gh auth token -u <account>) first if your
# default gh account differs), and the local docker-compose stack up.
#
# Usage: scripts/rehearsals/run.sh <d|e|a|c|h|f|g|i|b>
#   Scenarios d/e/i are Gateway-only (no GitHub Actions runs, no pages).
#   Scenarios a/c/h/f/g page a REAL escalation webhook and/or touch GitHub
#   (PRs, branches). Scenario b performs a REAL, irreversible merge to main
#   -- run it last, deliberately, never as part of a bulk "run everything".
set -euo pipefail

cd "$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

: "${REVI_GITHUB_OWNER:=$(grep -E '^REVI_GITHUB_OWNER=' .env | cut -d= -f2-)}"
: "${REVI_GITHUB_REPO:=$(grep -E '^REVI_GITHUB_REPO=' .env | cut -d= -f2-)}"
WEBHOOK_SECRET="$(grep -E '^REVI_WEBHOOK_SECRET=' .env | cut -d= -f2-)"
GATEWAY_URL="${GATEWAY_URL:-http://localhost:8080}"
REPO="${REVI_GITHUB_OWNER}/${REVI_GITHUB_REPO}"

log() { echo "[rehearsal] $*" >&2; }

alerts_curl() {
  curl -s -o /tmp/rehearsal_resp.txt -w '%{http_code}' -X POST "${GATEWAY_URL}/v1/alerts" \
    -H "Authorization: Bearer ${WEBHOOK_SECRET}" -H "Content-Type: application/json" "$@"
}

dispatch_rehearsal_workflow() {
  # $1=mode $2=alert_id $3=error_summary $@ extras...(-f key=value pairs)
  local mode="$1" alert_id="$2" summary="$3"; shift 3
  gh workflow run hermes-rehearsal.yml --repo "$REPO" \
    -f mode="$mode" -f alert_id="$alert_id" -f service_name="rehearsal-fixture-app" \
    -f error_summary="$summary" -f trace_id="trace-${alert_id}" "$@"
  sleep 8
  gh run list --repo "$REPO" --workflow=hermes-rehearsal.yml --limit 1 --json databaseId -q '.[0].databaseId'
}

scenario_d() {
  log "(d) malformed payload -> 400, no side effects"
  code=$(alerts_curl -d '{not valid json')
  [[ "$code" == "400" ]] || { log "FAIL: got $code"; return 1; }
  code=$(alerts_curl -d '{"status":"firing","alerts":[{"status":"firing","labels":{"alertname":"X","service":"s","trace_id":"t"},"annotations":{"summary":"s"},"startsAt":"2026-01-01T00:00:00Z","fingerprint":""}]}')
  [[ "$code" == "400" ]] || { log "FAIL: missing-field case got $code"; return 1; }
  code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "${GATEWAY_URL}/v1/alerts" -d '{}')
  [[ "$code" == "401" ]] || { log "FAIL: no-auth case got $code"; return 1; }
  log "PASS"
}

scenario_e() {
  log "(e) resolved alert dropped -> 200, no dispatch"
  code=$(alerts_curl -d '{"status":"resolved","alerts":[{"status":"resolved","labels":{"alertname":"X","service":"rehearsal-fixture-app","trace_id":"t"},"annotations":{"summary":"s"},"startsAt":"2026-01-01T00:00:00Z","fingerprint":"fp-rehearsal-e"}]}')
  [[ "$code" == "200" ]] || { log "FAIL: got $code"; return 1; }
  log "PASS (verify via: docker compose logs gateway | grep 'dropped 1 resolved')"
}

scenario_a() {
  log "(a) PR_REVIEW happy path -- real metric spike -> vmalert -> Alertmanager -> Gateway -> real Hermes"
  trace_id=$(bash scripts/rehearsals/spike.sh)
  log "pushed spike, trace_id=$trace_id -- now waiting for vmalert's for:2m + Alertmanager group_wait:10s (real-time wait, ~2-3 min)"
  log "poll: watch \`docker compose logs -f gateway\` for 'revi.alert.received', then \`gh run list --repo $REPO --workflow=hermes-triage.yml\`"
}

scenario_c() {
  log "(c) flapping alert suppressed by lock+digest -- reuses (a)'s service+summary so it hits the same lock key"
  local trace_id="${1:?scenario_c requires the trace_id scenario_a printed}"
  code=$(alerts_curl -d "{\"status\":\"firing\",\"alerts\":[{\"status\":\"firing\",\"labels\":{\"alertname\":\"ServiceErrorRateHigh\",\"service\":\"rehearsal-fixture-app\",\"trace_id\":\"$trace_id\"},\"annotations\":{\"summary\":\"High error rate on rehearsal-fixture-app (trace $trace_id)\"},\"startsAt\":\"2026-01-01T00:00:00Z\",\"fingerprint\":\"fp-rehearsal-c-duplicate\"}]}")
  [[ "$code" == "200" ]] || { log "FAIL: got $code"; return 1; }
  log "PASS (verify: no new hermes-triage.yml run, and a HEALING LOOP HALTED digest entry exists for today)"
}

scenario_h() {
  log "(h) exhaustion, both modes -- fake-agent fails all 3 attempts"
  local run_id
  run_id=$(dispatch_rehearsal_workflow PR_REVIEW "rehearsal-h-prreview-$(date +%s)" \
    "synthetic nil pointer dereference in Summarize (5.3 rehearsal h/PR_REVIEW)" \
    -f fake_agent_sequence="fail_always,fail_always,fail_always")
  log "PR_REVIEW run: https://github.com/$REPO/actions/runs/$run_id"
  run_id=$(dispatch_rehearsal_workflow AUTONOMOUS "rehearsal-h-autonomous-$(date +%s)" \
    "synthetic nil pointer dereference in Summarize (5.3 rehearsal h/AUTONOMOUS)" \
    -f fake_agent_sequence="fail_always,fail_always,fail_always")
  log "AUTONOMOUS run: https://github.com/$REPO/actions/runs/$run_id"
  log "both should end with a real EXHAUSTION page; \`gh run watch <id> --repo $REPO\` to confirm"
}

scenario_f() {
  log "(f) boot failure page -- valid fix, health_url points at a dead port"
  local run_id
  run_id=$(dispatch_rehearsal_workflow AUTONOMOUS "rehearsal-f-bootfail-$(date +%s)" \
    "synthetic nil pointer dereference in Summarize (5.3 rehearsal f/boot-failure)" \
    -f fake_agent_mode=succeed -f health_url="http://localhost:9999/healthz" -f smoke_budget_seconds="5")
  log "run: https://github.com/$REPO/actions/runs/$run_id -- expect a real APP_BOOT_FAILURE page"
}

scenario_g() {
  log "(g) regression + branch pruning -- valid fix, full test-grid command forced to fail"
  local run_id
  run_id=$(dispatch_rehearsal_workflow AUTONOMOUS "rehearsal-g-regression-$(date +%s)" \
    "synthetic nil pointer dereference in Summarize (5.3 rehearsal g/regression)" \
    -f fake_agent_mode=succeed -f test_command_override="go test ./... && exit 1")
  log "run: https://github.com/$REPO/actions/runs/$run_id -- expect a real REGRESSION page + pruned hermes/hotfix-* branch + main untouched"
}

scenario_i() {
  log "(i) digest flush -- requires SLACK_DIGEST_WEBHOOK_URL set in .env"
  local fire_at="${1:?usage: scenario_i HH:MM (a few minutes from now, container/UTC time)}"
  sed -i.bak "/^REVI_DIGEST_TIME=/d" .env && rm -f .env.bak
  echo "REVI_DIGEST_TIME=${fire_at}" >> .env
  docker compose up -d --force-recreate gateway
  log "gateway rescheduled for ${fire_at} -- poll: docker compose logs -f gateway | grep 'digestcron: flushed'"
  log "after it fires, remove REVI_DIGEST_TIME from .env and force-recreate gateway again to restore the 08:00 default"
}

scenario_b() {
  log "(b) AUTONOMOUS happy path -- REAL, IRREVERSIBLE MERGE TO MAIN. Run this last, deliberately."
  local run_id
  run_id=$(dispatch_rehearsal_workflow AUTONOMOUS "rehearsal-b-autonomous-$(date +%s)" \
    "synthetic nil pointer dereference in Summarize (5.3 rehearsal b/AUTONOMOUS happy path)" \
    -f fake_agent_mode=succeed)
  log "run: https://github.com/$REPO/actions/runs/$run_id -- expect main to advance to a new merge commit"
}

case "${1:-}" in
  d) scenario_d ;;
  e) scenario_e ;;
  a) scenario_a ;;
  c) scenario_c "${2:?scenario c needs the trace_id scenario a printed, as \$2}" ;;
  h) scenario_h ;;
  f) scenario_f ;;
  g) scenario_g ;;
  i) scenario_i "${2:?scenario i needs a target HH:MM as \$2}" ;;
  b) scenario_b ;;
  *)
    echo "usage: $0 <d|e|a|c|h|f|g|i|b> [scenario-specific arg]" >&2
    echo "  a/c/h/f/g page a real escalation webhook and/or touch GitHub." >&2
    echo "  b performs a real, irreversible merge to main -- run it last." >&2
    exit 1
    ;;
esac
