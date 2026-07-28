// Package github calls the GitHub REST API's repository_dispatch endpoint —
// the only GitHub call the Gateway itself makes (TRD Sec 2 step 2). It uses
// GITHUB_TOKEN_DISPATCH, a repository_dispatch-only credential that never
// enters the runner (TRD Sec 5).
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const defaultBaseURL = "https://api.github.com"

type Client struct {
	// BaseURL defaults to the real GitHub API; overridable (e.g. to an
	// httptest.Server) in tests.
	BaseURL string

	token string
	owner string
	repo  string
	http  *http.Client
}

func NewClient(token, owner, repo string) *Client {
	return &Client{
		BaseURL: defaultBaseURL,
		token:   token,
		owner:   owner,
		repo:    repo,
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

// Dispatch fires a repository_dispatch event with the given event_type and
// client_payload.
func (c *Client) Dispatch(ctx context.Context, eventType string, clientPayload any) error {
	body, err := json.Marshal(map[string]any{
		"event_type":     eventType,
		"client_payload": clientPayload,
	})
	if err != nil {
		return fmt.Errorf("encode dispatch payload: %w", err)
	}

	url := fmt.Sprintf("%s/repos/%s/%s/dispatches", c.BaseURL, c.owner, c.repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build dispatch request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("repository_dispatch request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("repository_dispatch: unexpected status %d: %s", resp.StatusCode, respBody)
	}
	return nil
}
