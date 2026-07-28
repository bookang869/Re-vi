// Package metrics exposes the 3 Prometheus counters required by TRD Sec 5
// on /metrics. Hand-rolled rather than pulling in prometheus/client_golang:
// three counters is a few lines of text/plain formatting, not enough to
// justify the first third-party dependency beyond nats.go.
package metrics

import (
	"fmt"
	"net/http"
	"sync/atomic"
)

var (
	alertsReceived        uint64
	modeRoutingPRReview   uint64
	modeRoutingAutonomous uint64

	// smokeFailures has no writer yet: the synthetic smoke test it counts
	// runs inside the GitHub Actions runner (Phase 4.1), which doesn't
	// exist in this repo yet. Exposed at a constant 0 until that phase adds
	// an increment call, wired through /v1/digest/entry's outcome field.
	smokeFailures uint64
)

// IncAlertsReceived records one alert successfully accepted by /v1/alerts
// (TRD "revi.alert.received": fired on successful webhook entry processing).
func IncAlertsReceived() {
	atomic.AddUint64(&alertsReceived, 1)
}

// IncModeRouting records one repository_dispatch call routed under mode
// ("PR_REVIEW" or "AUTONOMOUS" — already normalized by config.Load).
func IncModeRouting(mode string) {
	if mode == "AUTONOMOUS" {
		atomic.AddUint64(&modeRoutingAutonomous, 1)
		return
	}
	atomic.AddUint64(&modeRoutingPRReview, 1)
}

// Handler serves the Prometheus text exposition format for the Gateway's
// counters.
func Handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")

	fmt.Fprint(w, "# HELP revi_alerts_received_total Alerts successfully accepted by /v1/alerts.\n")
	fmt.Fprint(w, "# TYPE revi_alerts_received_total counter\n")
	fmt.Fprintf(w, "revi_alerts_received_total %d\n", atomic.LoadUint64(&alertsReceived))

	fmt.Fprint(w, "# HELP revi_mode_routing_total Dispatches sent, labeled by REVI_MODE.\n")
	fmt.Fprint(w, "# TYPE revi_mode_routing_total counter\n")
	fmt.Fprintf(w, "revi_mode_routing_total{mode=\"pr_review\"} %d\n", atomic.LoadUint64(&modeRoutingPRReview))
	fmt.Fprintf(w, "revi_mode_routing_total{mode=\"autonomous\"} %d\n", atomic.LoadUint64(&modeRoutingAutonomous))

	fmt.Fprint(w, "# HELP revi_synthetic_smoke_failures_total Autonomous-mode boot crashes caught by synthetic smoke testing.\n")
	fmt.Fprint(w, "# TYPE revi_synthetic_smoke_failures_total counter\n")
	fmt.Fprintf(w, "revi_synthetic_smoke_failures_total %d\n", atomic.LoadUint64(&smokeFailures))
}
