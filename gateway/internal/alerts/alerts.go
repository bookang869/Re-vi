// Package alerts maps Alertmanager's native grouped-alerts webhook JSON
// (the only wire format its webhook_configs can send, TRD Sec 4) onto the
// Gateway's internal Alert model.
package alerts

// webhookPayload is Alertmanager's native webhook body.
type webhookPayload struct {
	Status string         `json:"status"`
	Alerts []webhookAlert `json:"alerts"`
}

type webhookAlert struct {
	Status      string            `json:"status"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	StartsAt    string            `json:"startsAt"`
	Fingerprint string            `json:"fingerprint"`
}

// Alert is the Gateway's internal model, post-mapping (TRD Sec 4).
type Alert struct {
	AlertID      string `json:"alert_id"`
	Status       string `json:"status"`
	ServiceName  string `json:"service_name"`
	TraceID      string `json:"trace_id"`
	ErrorSummary string `json:"error_summary"`
	Timestamp    string `json:"timestamp"`
	LogContext   string `json:"log_context,omitempty"`

	// Benchmark-only (docs/observability-part-b.md), read from labels/
	// annotations when present; empty on every real alert since Alertmanager
	// never sets them. Not required fields — mapAlerts doesn't reject an
	// alert for omitting these.
	ExperimentID     string `json:"experiment_id,omitempty"`
	FaultID          string `json:"fault_id,omitempty"`
	FaultType        string `json:"fault_type,omitempty"`
	LanguageRuntime  string `json:"language_runtime,omitempty"`
	ExpectedBehavior string `json:"expected_behavior,omitempty"`

	// Benchmark-only dispatch routing (docs/observability-part-b.md "Locked:
	// verifier isolation" grilling session, 2026-08-20). Unlike the fields
	// above, these two flow all the way into client_payload -- the runner
	// needs them, not just the Gateway's own SQLite record. Empty on every
	// real alert, which is a no-op end to end: hermes-triage.yml's checkout
	// treats an empty ref as "use the default branch" (unchanged behavior),
	// and autonomous-promote.sh treats an empty merge target as "main"
	// (unchanged behavior).
	//
	// Ref is the fixed base_ref tag to check out instead of main -- set to
	// the same tag on every trial of a fault, including repeats, per the
	// "clean baseline" rule (never re-derived from main at trial time).
	//
	// MergeTarget is a per-fault integration branch (not main) that the
	// harness resets to Ref before every trial. Necessary because repeat
	// trials of the same fault each write an independent fix starting from
	// the same still-broken baseline; merging trial 2's fix into a main that
	// already has trial 1's differently-worded fix on the same lines is a
	// near-guaranteed git conflict that would misreport as "Hermes failed"
	// rather than "trials collided with each other."
	Ref         string `json:"ref,omitempty"`
	MergeTarget string `json:"merge_target,omitempty"`
}

// invalidAlert records why a single firing alert within a batch was
// skipped, along with whatever identifying fields it did carry, so the
// caller can log which alert was dropped and why.
type invalidAlert struct {
	alertname   string
	fingerprint string
	reason      string
}

// mapAlerts maps each firing element of payload.Alerts to the internal
// model. "resolved" entries are dropped (returned in droppedCount) — no
// error, no required-field validation, per TRD Sec 4. A firing entry
// missing a required field is skipped individually (returned in invalid)
// rather than failing the whole batch: Alertmanager's grouping can legally
// bundle unrelated services into one webhook call (TRD Sec 4), so one
// malformed alert must never suppress delivery of the other, valid alerts
// in the same batch (decided 2026-08-20, PLAN.md 6.11).
func mapAlerts(payload webhookPayload) (fired []Alert, droppedCount int, invalid []invalidAlert) {
	for _, raw := range payload.Alerts {
		if raw.Status == "resolved" {
			droppedCount++
			continue
		}

		alertname := raw.Labels["alertname"]
		service := raw.Labels["service"]
		traceID := raw.Labels["trace_id"]
		summary := raw.Annotations["summary"]

		var reason string
		switch {
		case alertname == "":
			reason = "missing required field: labels.alertname"
		case raw.Fingerprint == "":
			reason = "missing required field: fingerprint"
		case service == "":
			reason = "missing required field: labels.service"
		case traceID == "":
			reason = "missing required field: labels.trace_id"
		case summary == "":
			reason = "missing required field: annotations.summary"
		}
		if reason != "" {
			invalid = append(invalid, invalidAlert{alertname: alertname, fingerprint: raw.Fingerprint, reason: reason})
			continue
		}

		fired = append(fired, Alert{
			AlertID:      alertname + "-" + raw.Fingerprint,
			Status:       "firing",
			ServiceName:  service,
			TraceID:      traceID,
			ErrorSummary: summary,
			Timestamp:    raw.StartsAt,

			ExperimentID:     raw.Labels["experiment_id"],
			FaultID:          raw.Labels["fault_id"],
			FaultType:        raw.Labels["fault_type"],
			LanguageRuntime:  raw.Labels["language_runtime"],
			ExpectedBehavior: raw.Annotations["expected_behavior"],

			Ref:         raw.Labels["ref"],
			MergeTarget: raw.Labels["merge_target"],
		})
	}
	return fired, droppedCount, invalid
}
