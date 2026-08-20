// Package runs implements GET /v1/runs/{alert_id}, the benchmark harness's
// completion-poll endpoint (docs/observability-part-b.md "Locked:
// harness/pipeline interaction"). Deliberately narrow: five fields needed
// to tell a poll loop a trial is done and let it score the result, not a
// general-purpose stats API (that question stays deferred, see
// docs/observability-part-a.md's "Consumption layer sequencing").
package runs

import (
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/bookang869/Re-vi/gateway/internal/store"
)

// response is deliberately a plain struct with pointer fields, not
// sql.Null* types, so a still-pending run serializes as JSON null rather
// than a Go-specific {Valid,String} shape.
type response struct {
	AlertID          string  `json:"alert_id"`
	Outcome          *string `json:"outcome"`
	ValidatedAt      *string `json:"validated_at"`
	MergedAt         *string `json:"merged_at"`
	EscalationReason *string `json:"escalation_reason"`
	Attempts         *int    `json:"attempts"`
	FailureStage     *string `json:"failure_stage"`
}

// NewHandler returns the /v1/runs/{alert_id} handler. secret is
// REVI_WEBHOOK_SECRET, reused as the Bearer token here (same scheme as
// /v1/alerts, not /v1/digest/entry's HMAC) -- the harness runs locally like
// Alertmanager does, not from inside an ephemeral GitHub Actions runner, so
// it can hold and send a static bearer token the same way.
func NewHandler(secret string, s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !validBearer(r.Header.Get("Authorization"), secret) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		alertID := r.PathValue("alert_id")
		if alertID == "" {
			http.Error(w, "missing alert_id", http.StatusBadRequest)
			return
		}

		if s == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		var outcome, escalationReason, failureStage sql.NullString
		var validatedAt, mergedAt sql.NullString
		var attempts sql.NullInt64
		err := s.DB().QueryRowContext(r.Context(), `
			SELECT outcome, validated_at, merged_at, escalation_reason, attempts, failure_stage
			FROM runs WHERE alert_id = ?`, alertID).
			Scan(&outcome, &validatedAt, &mergedAt, &escalationReason, &attempts, &failureStage)
		if err == sql.ErrNoRows {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		resp := response{AlertID: alertID}
		if outcome.Valid {
			resp.Outcome = &outcome.String
		}
		if validatedAt.Valid {
			resp.ValidatedAt = &validatedAt.String
		}
		if mergedAt.Valid {
			resp.MergedAt = &mergedAt.String
		}
		if escalationReason.Valid {
			resp.EscalationReason = &escalationReason.String
		}
		if attempts.Valid {
			v := int(attempts.Int64)
			resp.Attempts = &v
		}
		if failureStage.Valid {
			resp.FailureStage = &failureStage.String
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
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
