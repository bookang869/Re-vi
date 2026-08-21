package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// GatewayClient talks to the real Gateway exactly the way Alertmanager and
// the benchmark harness's poll loop do -- POST /v1/alerts (Bearer token,
// Alertmanager's native grouped-alerts shape) and GET /v1/runs/{alert_id}
// (same Bearer secret, reused -- gateway/internal/runs/handler.go). There is
// deliberately no lower-level path into the pipeline (e.g. calling NATS or
// repository_dispatch directly): a benchmark trial must look exactly like a
// real alert from the Gateway's point of view, per docs/observability-
// part-b.md.
type GatewayClient struct {
	baseURL string
	secret  string
	http    *http.Client
}

func newGatewayClient(baseURL, secret string) *GatewayClient {
	return &GatewayClient{baseURL: baseURL, secret: secret, http: &http.Client{Timeout: 15 * time.Second}}
}

// webhookAlert/webhookPayload mirror gateway/internal/alerts/alerts.go's
// unexported wire types exactly -- there's no shared package to import
// (this module is intentionally standalone, see benchmark/README.md), so
// the shape is duplicated here on purpose rather than reaching into the
// Gateway module's internal/ package.
type webhookAlert struct {
	Status      string            `json:"status"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	StartsAt    string            `json:"startsAt"`
	Fingerprint string            `json:"fingerprint"`
}

type webhookPayload struct {
	Status string         `json:"status"`
	Alerts []webhookAlert `json:"alerts"`
}

// dispatchAlert builds and sends one synthetic firing alert for a fault
// trial. It returns the alert_id the Gateway will have computed
// (alertname + "-" + fingerprint, per mapAlerts in gateway/internal/
// alerts/alerts.go) so the caller can poll for it -- the harness controls
// both halves and so can predict it up front, rather than needing the
// Gateway to echo it back.
func (c *GatewayClient) dispatchAlert(ctx context.Context, f Fixture, experimentID string, trialNum int, fingerprint string) (alertID string, err error) {
	alertID = f.FaultID + "-" + fingerprint

	payload := webhookPayload{
		Status: "firing",
		Alerts: []webhookAlert{{
			Status: "firing",
			Labels: map[string]string{
				"alertname":        f.FaultID,
				"service":          f.ServiceName,
				"trace_id":         fmt.Sprintf("trace-%s", fingerprint),
				"experiment_id":    experimentID,
				"fault_id":         f.FaultID,
				"fault_type":       f.FaultType,
				"language_runtime": f.LanguageRuntime,
				"ref":              f.BaseRef,
				"merge_target":     mergeTargetBranch(f.FaultID),
			},
			Annotations: map[string]string{
				"summary":           f.ErrorSummary,
				"expected_behavior": f.ExpectedBehavior,
			},
			StartsAt:    time.Now().UTC().Format(time.RFC3339),
			Fingerprint: fingerprint,
		}},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal alert payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/alerts", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.secret)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("POST /v1/alerts: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	// gateway/internal/alerts/handler.go returns 202 when at least one
	// alert in the batch was actually published (the normal success case
	// for a dispatch), and 200 only when every alert in the batch hit an
	// active flap lock (nothing new published) -- both are non-error
	// responses from the harness's point of view. Anything else (401,
	// 400, 5xx) is a real dispatch failure.
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return "", fmt.Errorf("POST /v1/alerts: status %d: %s", resp.StatusCode, string(respBody))
	}

	return alertID, nil
}

// runStatus mirrors gateway/internal/runs/handler.go's response struct.
type runStatus struct {
	AlertID          string  `json:"alert_id"`
	Outcome          *string `json:"outcome"`
	ValidatedAt      *string `json:"validated_at"`
	MergedAt         *string `json:"merged_at"`
	EscalationReason *string `json:"escalation_reason"`
	Attempts         *int    `json:"attempts"`
	FailureStage     *string `json:"failure_stage"`
}

// getRun polls GET /v1/runs/{alert_id}. found=false with a nil error means
// "row not written yet" (404) -- a normal, expected state early in a
// trial, not an error condition; the poll loop keeps waiting.
func (c *GatewayClient) getRun(ctx context.Context, alertID string) (status *runStatus, found bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/runs/"+alertID, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Authorization", "Bearer "+c.secret)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("GET /v1/runs/%s: %w", alertID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, false, nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, false, fmt.Errorf("GET /v1/runs/%s: status %d: %s", alertID, resp.StatusCode, string(body))
	}

	var s runStatus
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return nil, false, fmt.Errorf("GET /v1/runs/%s: decode response: %w", alertID, err)
	}
	return &s, true, nil
}

// mergeTargetBranch is the per-fault integration branch autonomous-
// promote.sh's MERGE_TARGET override points at (docs/observability-
// part-b.md "Locked: dispatch routing to a fault's isolated ref") -- the
// harness resets it to the fault's base_ref before every trial so repeat
// trials never collide with each other's independently-worded fixes.
func mergeTargetBranch(faultID string) string {
	return "benchmark/" + faultID
}
