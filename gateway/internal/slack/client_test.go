package slack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bookang869/Re-vi/gateway/internal/queue"
)

func TestPostDigest_SendsExpectedBlocks(t *testing.T) {
	var gotContentType string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	c.http = srv.Client()

	entries := []queue.DigestEntry{
		{AlertID: "alert-1", Message: "fixed nil pointer dereference", Count: 2, FirstSeen: "t1", LastSeen: "t2"},
	}
	if err := c.PostDigest(context.Background(), "2026-07-28", entries); err != nil {
		t.Fatalf("PostDigest() error: %v", err)
	}

	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q", gotContentType)
	}
	blocks, ok := gotBody["blocks"].([]any)
	if !ok || len(blocks) != 2 {
		t.Fatalf("blocks = %v, want header + 1 entry", gotBody["blocks"])
	}
}

func TestPostDigest_NonOKIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	c.http = srv.Client()

	err := c.PostDigest(context.Background(), "2026-07-28", nil)
	if err == nil {
		t.Fatal("expected an error on non-200 response")
	}
}
