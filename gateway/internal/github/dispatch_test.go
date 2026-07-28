package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDispatch_SendsExpectedRequest(t *testing.T) {
	var gotPath, gotAuth, gotAccept string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := NewClient("test-token", "acme", "widgets")
	c.http = srv.Client()
	c.BaseURL = srv.URL

	payload := map[string]string{"mode": "PR_REVIEW", "alert_id": "a1"}
	if err := c.Dispatch(context.Background(), "revi-triage", payload); err != nil {
		t.Fatalf("Dispatch() error: %v", err)
	}

	if gotPath != "/repos/acme/widgets/dispatches" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer test-token" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotAccept != "application/vnd.github+json" {
		t.Errorf("Accept = %q", gotAccept)
	}
	if gotBody["event_type"] != "revi-triage" {
		t.Errorf("event_type = %v", gotBody["event_type"])
	}
	cp, ok := gotBody["client_payload"].(map[string]any)
	if !ok || cp["alert_id"] != "a1" {
		t.Errorf("client_payload = %v", gotBody["client_payload"])
	}
}

func TestDispatch_NonNoContentIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write([]byte(`{"message":"bad event_type"}`))
	}))
	defer srv.Close()

	c := NewClient("test-token", "acme", "widgets")
	c.http = srv.Client()
	c.BaseURL = srv.URL

	err := c.Dispatch(context.Background(), "revi-triage", map[string]string{})
	if err == nil {
		t.Fatal("expected an error on non-204 response")
	}
}
