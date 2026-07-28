package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandler_RendersCounters(t *testing.T) {
	IncAlertsReceived()
	IncAlertsReceived()
	IncModeRouting("AUTONOMOUS")
	IncModeRouting("PR_REVIEW")
	IncModeRouting("")

	rec := httptest.NewRecorder()
	Handler(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain", ct)
	}

	body := rec.Body.String()
	for _, want := range []string{
		"revi_alerts_received_total 2",
		`revi_mode_routing_total{mode="autonomous"} 1`,
		`revi_mode_routing_total{mode="pr_review"} 2`,
		"revi_synthetic_smoke_failures_total 0",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\ngot:\n%s", want, body)
		}
	}
}
