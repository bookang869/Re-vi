package victorialogs

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func jsonlServer(t *testing.T, lines []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, l := range lines {
			fmt.Fprintln(w, l)
		}
	}))
}

func TestFetchContext_HappyPath(t *testing.T) {
	// server returns newest-first, as a real LogsQL "sort by (_time) desc" would
	srv := jsonlServer(t, []string{
		`{"_time":"2026-07-20T19:30:03Z","_msg":"line 3"}`,
		`{"_time":"2026-07-20T19:30:02Z","_msg":"line 2"}`,
		`{"_time":"2026-07-20T19:30:01Z","_msg":"line 1"}`,
	})
	defer srv.Close()

	c := NewClient(srv.URL)
	got := c.FetchContext(context.Background(), "abc123")

	want := "2026-07-20T19:30:01Z line 1\n2026-07-20T19:30:02Z line 2\n2026-07-20T19:30:03Z line 3"
	if got != want {
		t.Errorf("FetchContext() =\n%q\nwant\n%q", got, want)
	}
}

func TestFetchContext_EmptyTraceID(t *testing.T) {
	c := NewClient("http://example.invalid")
	if got := c.FetchContext(context.Background(), ""); got != "" {
		t.Errorf("FetchContext(empty traceID) = %q, want empty", got)
	}
}

func TestFetchContext_TimeoutFallsBackToEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		fmt.Fprintln(w, `{"_time":"2026-07-20T19:30:01Z","_msg":"too slow"}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	c.timeout = 20 * time.Millisecond // short enough to trip reliably without a slow test

	got := c.FetchContext(context.Background(), "abc123")
	if got != "" {
		t.Errorf("FetchContext() on timeout = %q, want empty (never block the alert)", got)
	}
}

func TestFetchContext_NonOKStatusFallsBackToEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	if got := c.FetchContext(context.Background(), "abc123"); got != "" {
		t.Errorf("FetchContext() on 500 = %q, want empty", got)
	}
}

func TestFetchContext_TruncatesOldestFirstToByteBudget(t *testing.T) {
	// each line ~1KB; 60 lines is well past the 48KB budget. Markers use a
	// trailing delimiter (LINE:0: vs LINE:50:) so substring checks below
	// can't accidentally match the wrong line (e.g. "LINE:5" inside "LINE:50").
	var lines []string
	for i := 0; i < 60; i++ {
		padding := strings.Repeat("x", 1000)
		lines = append(lines, fmt.Sprintf(`{"_time":"2026-07-20T19:%02d:00Z","_msg":"%s LINE:%d:"}`, i%60, padding, i))
	}
	// server returns newest first (i=59 first), matching real query order
	reversed := make([]string, len(lines))
	for i, l := range lines {
		reversed[len(lines)-1-i] = l
	}

	srv := jsonlServer(t, reversed)
	defer srv.Close()

	c := NewClient(srv.URL)
	got := c.FetchContext(context.Background(), "abc123")

	if len(got) > maxContextBytes {
		t.Errorf("result is %d bytes, want <= %d", len(got), maxContextBytes)
	}
	if !strings.Contains(got, "LINE:59:") {
		t.Error("most recent line (59) should survive truncation")
	}
	if strings.Contains(got, "LINE:0:") {
		t.Error("oldest line (0) should have been dropped by truncation")
	}
}
