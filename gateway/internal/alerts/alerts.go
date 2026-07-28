// Package alerts maps Alertmanager's native grouped-alerts webhook JSON
// (the only wire format its webhook_configs can send, TRD Sec 4) onto the
// Gateway's internal Alert model.
package alerts

import "fmt"

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
}

// mapAlerts maps each firing element of payload.Alerts to the internal
// model. "resolved" entries are dropped (returned in droppedCount) — no
// error, no required-field validation, per TRD Sec 4.
func mapAlerts(payload webhookPayload) (fired []Alert, droppedCount int, err error) {
	for _, raw := range payload.Alerts {
		if raw.Status == "resolved" {
			droppedCount++
			continue
		}

		alertname := raw.Labels["alertname"]
		service := raw.Labels["service"]
		traceID := raw.Labels["trace_id"]
		summary := raw.Annotations["summary"]

		switch {
		case alertname == "":
			return nil, droppedCount, fmt.Errorf("missing required field: labels.alertname")
		case raw.Fingerprint == "":
			return nil, droppedCount, fmt.Errorf("missing required field: fingerprint")
		case service == "":
			return nil, droppedCount, fmt.Errorf("missing required field: labels.service")
		case traceID == "":
			return nil, droppedCount, fmt.Errorf("missing required field: labels.trace_id")
		case summary == "":
			return nil, droppedCount, fmt.Errorf("missing required field: annotations.summary")
		}

		fired = append(fired, Alert{
			AlertID:      alertname + "-" + raw.Fingerprint,
			Status:       "firing",
			ServiceName:  service,
			TraceID:      traceID,
			ErrorSummary: summary,
			Timestamp:    raw.StartsAt,
		})
	}
	return fired, droppedCount, nil
}
