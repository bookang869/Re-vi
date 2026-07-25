package alerts

import "testing"

func firingAlert() webhookAlert {
	return webhookAlert{
		Status:      "firing",
		Labels:      map[string]string{"alertname": "ServiceErrorRateHigh", "service": "payment-processor", "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736"},
		Annotations: map[string]string{"summary": "high error rate"},
		StartsAt:    "2026-07-20T19:30:00Z",
		Fingerprint: "abc123",
	}
}

func TestMapAlerts_Valid(t *testing.T) {
	fired, dropped, err := mapAlerts(webhookPayload{Status: "firing", Alerts: []webhookAlert{firingAlert()}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dropped != 0 || len(fired) != 1 {
		t.Fatalf("got fired=%d dropped=%d, want fired=1 dropped=0", len(fired), dropped)
	}
	got := fired[0]
	want := Alert{
		AlertID:      "ServiceErrorRateHigh-abc123",
		Status:       "firing",
		ServiceName:  "payment-processor",
		TraceID:      "4bf92f3577b34da6a3ce929d0e0e4736",
		ErrorSummary: "high error rate",
		Timestamp:    "2026-07-20T19:30:00Z",
	}
	if got != want {
		t.Errorf("mapAlerts() = %+v, want %+v", got, want)
	}
}

func TestMapAlerts_Resolved(t *testing.T) {
	resolved := firingAlert()
	resolved.Status = "resolved"

	fired, dropped, err := mapAlerts(webhookPayload{Status: "resolved", Alerts: []webhookAlert{resolved}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fired) != 0 || dropped != 1 {
		t.Fatalf("got fired=%d dropped=%d, want fired=0 dropped=1", len(fired), dropped)
	}
}

func TestMapAlerts_ResolvedSkipsFieldValidation(t *testing.T) {
	resolved := webhookAlert{Status: "resolved"} // no labels/annotations/fingerprint at all
	_, dropped, err := mapAlerts(webhookPayload{Alerts: []webhookAlert{resolved}})
	if err != nil {
		t.Fatalf("resolved alert with missing fields should not error: %v", err)
	}
	if dropped != 1 {
		t.Fatalf("dropped = %d, want 1", dropped)
	}
}

func TestMapAlerts_MissingRequiredFields(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(a *webhookAlert)
	}{
		{"missing alertname", func(a *webhookAlert) { delete(a.Labels, "alertname") }},
		{"missing fingerprint", func(a *webhookAlert) { a.Fingerprint = "" }},
		{"missing service label", func(a *webhookAlert) { delete(a.Labels, "service") }},
		{"missing trace_id label", func(a *webhookAlert) { delete(a.Labels, "trace_id") }},
		{"missing summary annotation", func(a *webhookAlert) { delete(a.Annotations, "summary") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := firingAlert()
			tc.mutate(&a)
			_, _, err := mapAlerts(webhookPayload{Alerts: []webhookAlert{a}})
			if err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}
