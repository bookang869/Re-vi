// Package slack posts the compiled morning digest to a Slack incoming
// webhook. The destination channel (#triage-morning-review) is fixed by
// whichever webhook URL the operator configures — the Gateway itself has no
// channel/routing logic (TRD Sec 2 step 3).
package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/bookang869/Re-vi/gateway/internal/queue"
)

type Client struct {
	webhookURL string
	http       *http.Client
}

func NewClient(webhookURL string) *Client {
	return &Client{webhookURL: webhookURL, http: &http.Client{Timeout: 10 * time.Second}}
}

// PostDigest compiles entries into one Slack Block Kit message and posts it
// to the configured webhook. digestDate is used only in the header text.
func (c *Client) PostDigest(ctx context.Context, digestDate string, entries []queue.DigestEntry) error {
	body, err := json.Marshal(buildPayload(digestDate, entries))
	if err != nil {
		return fmt.Errorf("encode digest payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.webhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build digest request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("post digest: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("post digest: unexpected status %d", resp.StatusCode)
	}
	return nil
}

func buildPayload(digestDate string, entries []queue.DigestEntry) map[string]any {
	blocks := []map[string]any{
		{
			"type": "header",
			"text": map[string]any{"type": "plain_text", "text": fmt.Sprintf("Re:vi morning digest — %s", digestDate)},
		},
	}
	for _, e := range entries {
		text := fmt.Sprintf("*%s* (x%d)\n%s\n_first %s, last %s_", e.AlertID, e.Count, e.Message, e.FirstSeen, e.LastSeen)
		blocks = append(blocks, map[string]any{
			"type": "section",
			"text": map[string]any{"type": "mrkdwn", "text": text},
		})
	}
	return map[string]any{"blocks": blocks}
}
