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
	fired, dropped, invalid := mapAlerts(webhookPayload{Status: "firing", Alerts: []webhookAlert{firingAlert()}})
	if len(invalid) != 0 {
		t.Fatalf("unexpected invalid alerts: %+v", invalid)
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

	fired, dropped, invalid := mapAlerts(webhookPayload{Status: "resolved", Alerts: []webhookAlert{resolved}})
	if len(invalid) != 0 {
		t.Fatalf("unexpected invalid alerts: %+v", invalid)
	}
	if len(fired) != 0 || dropped != 1 {
		t.Fatalf("got fired=%d dropped=%d, want fired=0 dropped=1", len(fired), dropped)
	}
}

func TestMapAlerts_ResolvedSkipsFieldValidation(t *testing.T) {
	resolved := webhookAlert{Status: "resolved"} // no labels/annotations/fingerprint at all
	_, dropped, invalid := mapAlerts(webhookPayload{Alerts: []webhookAlert{resolved}})
	if len(invalid) != 0 {
		t.Fatalf("resolved alert with missing fields should not be flagged invalid: %+v", invalid)
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
			fired, _, invalid := mapAlerts(webhookPayload{Alerts: []webhookAlert{a}})
			if len(invalid) != 1 {
				t.Fatalf("got %d invalid alerts, want 1", len(invalid))
			}
			if len(fired) != 0 {
				t.Errorf("got %d fired, want 0", len(fired))
			}
		})
	}
}

func TestMapAlerts_OneMalformedDoesNotDropRestOfBatch(t *testing.T) {
	good := firingAlert()
	bad := firingAlert()
	bad.Fingerprint = ""
	bad.Labels = map[string]string{"alertname": "OtherAlert", "service": "unrelated-service", "trace_id": "xyz"}

	fired, dropped, invalid := mapAlerts(webhookPayload{Status: "firing", Alerts: []webhookAlert{bad, good}})
	if dropped != 0 {
		t.Errorf("dropped = %d, want 0", dropped)
	}
	if len(invalid) != 1 || invalid[0].alertname != "OtherAlert" {
		t.Fatalf("got invalid=%+v, want exactly the malformed OtherAlert entry", invalid)
	}
	if len(fired) != 1 || fired[0].AlertID != "ServiceErrorRateHigh-abc123" {
		t.Fatalf("got fired=%+v, want the good alert to still be published", fired)
	}
}
