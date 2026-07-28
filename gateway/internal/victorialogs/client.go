// Package victorialogs queries VictoriaLogs for the log context the
// Gateway embeds in each alert dispatch (TRD FR-01b). The OTel Collector
// only forwards logs there and stores nothing itself, so this is the only
// place that context can come from.
package victorialogs

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

const (
	maxLines        = 50
	maxContextBytes = 48 * 1024 // TRD FR-01b: 48KB budget within GitHub's 64KB client_payload cap
)

type Client struct {
	baseURL string
	http    *http.Client
	timeout time.Duration
}

func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		http:    &http.Client{},
		timeout: 3 * time.Second,
	}
}

// FetchContext returns the preceding log lines for traceID, oldest first,
// truncated to the byte budget by dropping the oldest lines. It never
// returns an error: a query failure, timeout, or empty traceID all just
// yield "" — enrichment must never block dispatching the alert itself.
func (c *Client) FetchContext(ctx context.Context, traceID string) string {
	if c == nil || traceID == "" {
		return ""
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	query := fmt.Sprintf(`trace_id:%q | sort by (_time) desc | limit %d`, traceID, maxLines)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/select/logsql/query", nil)
	if err != nil {
		log.Printf("victorialogs: building request failed, proceeding with empty log_context: %v", err)
		return ""
	}
	q := req.URL.Query()
	q.Set("query", query)
	req.URL.RawQuery = q.Encode()

	resp, err := c.http.Do(req)
	if err != nil {
		log.Printf("victorialogs: query failed, proceeding with empty log_context: %v", err)
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("victorialogs: query returned %d, proceeding with empty log_context", resp.StatusCode)
		return ""
	}

	// response is JSONL (one JSON object per line), newest first
	var lines []string
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var rec struct {
			Time string `json:"_time"`
			Msg  string `json:"_msg"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			continue // skip a malformed line rather than fail the whole query
		}
		lines = append(lines, rec.Time+" "+rec.Msg)
	}
	if err := scanner.Err(); err != nil {
		log.Printf("victorialogs: reading response failed, proceeding with empty log_context: %v", err)
		return ""
	}

	reverse(lines) // chronological (oldest first)
	return truncateOldestFirst(lines, maxContextBytes)
}

func reverse(lines []string) {
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}
}

// truncateOldestFirst joins lines (oldest first) into a single string,
// dropping whole lines from the oldest end until the result fits maxBytes.
func truncateOldestFirst(lines []string, maxBytes int) string {
	total := 0
	start := len(lines)
	for i := len(lines) - 1; i >= 0; i-- {
		lineBytes := len(lines[i]) + 1 // +1 for the joining newline
		if total+lineBytes > maxBytes {
			break
		}
		total += lineBytes
		start = i
	}
	return strings.Join(lines[start:], "\n")
}
