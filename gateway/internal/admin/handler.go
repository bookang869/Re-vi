// Package admin implements GET/POST /v1/admin/repair-enabled, the runtime
// on/off switch for Hermes dispatch (queue.RepairEnabled) — the control
// surface for turning off Claude-token spend without stopping alert
// ingestion or observability (2026-08-24 grilling session).
package admin

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"

	"github.com/bookang869/Re-vi/gateway/internal/queue"
)

type statusResponse struct {
	Enabled bool `json:"enabled"`
}

type setRequest struct {
	Enabled bool `json:"enabled"`
}

// NewGetHandler returns the GET /v1/admin/repair-enabled handler: reports
// the current toggle state. token is REVI_ADMIN_TOKEN, checked as
// "Authorization: Bearer <token>" -- a separate credential from
// REVI_WEBHOOK_SECRET, since this endpoint controls spend, not just
// message delivery.
func NewGetHandler(token string, q *queue.Queue) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !validBearer(r.Header.Get("Authorization"), token) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		enabled, err := q.RepairEnabled(r.Context())
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(statusResponse{Enabled: enabled})
	}
}

// NewSetHandler returns the POST /v1/admin/repair-enabled handler: sets the
// toggle state from a {"enabled": bool} JSON body.
func NewSetHandler(token string, q *queue.Queue) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !validBearer(r.Header.Get("Authorization"), token) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		var req setRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "malformed JSON body", http.StatusBadRequest)
			return
		}

		if err := q.SetRepairEnabled(r.Context(), req.Enabled); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(statusResponse{Enabled: req.Enabled})
	}
}

func validBearer(header, secret string) bool {
	const prefix = "Bearer "
	if secret == "" || len(header) <= len(prefix) || header[:len(prefix)] != prefix {
		return false
	}
	got := header[len(prefix):]
	return subtle.ConstantTimeCompare([]byte(got), []byte(secret)) == 1
}
