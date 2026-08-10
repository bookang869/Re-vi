#!/usr/bin/env bash
# PLAN Phase 5.3 scenario (a) -- pushes a synthetic error-rate spike straight
# into VictoriaMetrics via its Prometheus import endpoint, so vmalert's real
# ServiceErrorRateHigh rule (vmalert-rules.yml) evaluates against real data
# and fires through the real Alertmanager -> Gateway chain. Skips the OTel
# Collector's OTLP hop deliberately: that passthrough isn't what 5.3 needs
# to prove, vmalert's rule -> Alertmanager -> Gateway wiring is (see PLAN.md
# 5.3's design notes).
#
# rate(revi_http_requests_total{status=~"5.."}[5m]) is a plain counter rate,
# not a fraction -- pushing a handful of points spanning the last ~2 minutes
# with a real counter delta between them satisfies `> 0.05` at every vmalert
# evaluation tick for the next 5 minutes (rate()'s range window looks
# backward from evaluation time), so one batch push is enough; no live drip
# needed. vmalert's `for: 2m` still requires ~2 real minutes of evaluation
# ticks (--evaluationInterval=30s) after the first tick that observes it
# true before Alertmanager receives anything -- that wait is real, this
# script doesn't shortcut it.
set -euo pipefail

VM_URL="${VM_URL:-http://localhost:8428}"
SERVICE="${SPIKE_SERVICE:-rehearsal-fixture-app}"
TRACE_ID="${SPIKE_TRACE_ID:-trace-rehearsal-a-$(date +%s)}"
POINTS="${SPIKE_POINTS:-8}"        # number of counter samples
SPAN_SECONDS="${SPIKE_SPAN_SECONDS:-150}"  # time covered by the samples

now_ms=$(($(date +%s) * 1000))
step_ms=$(( SPAN_SECONDS * 1000 / (POINTS - 1) ))
start_ms=$(( now_ms - SPAN_SECONDS * 1000 ))

lines=""
for i in $(seq 0 $((POINTS - 1))); do
  ts=$(( start_ms + i * step_ms ))
  val=$(( (i + 1) * 3 ))  # 3x margin above the 0.05/s threshold, not a razor-thin pass
  lines="${lines}revi_http_requests_total{service=\"${SERVICE}\",status=\"500\",trace_id=\"${TRACE_ID}\"} ${val} ${ts}
"
done

echo "spike.sh: pushing ${POINTS} samples for service=${SERVICE} trace_id=${TRACE_ID} spanning ${SPAN_SECONDS}s" >&2
resp=$(printf '%s' "$lines" | curl -s -o /dev/null -w '%{http_code}' --data-binary @- "${VM_URL}/api/v1/import/prometheus")
if [[ "$resp" != "204" ]]; then
  echo "spike.sh: unexpected VictoriaMetrics import status $resp" >&2
  exit 1
fi
echo "spike.sh: import accepted (204)" >&2
echo "$TRACE_ID"
