// Package digest implements POST /v1/digest/entry, the bridge a GitHub
// Actions runner uses to report a triage run's outcome back into the
// private VPC (TRD Sec 4) — GitHub-hosted runners have no network path to
// NATS directly, so this HTTP endpoint is the only way in.
package digest

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/bookang869/Re-vi/gateway/internal/queue"
	"github.com/bookang869/Re-vi/gateway/internal/store"
)

// entry is the runner's POST body (TRD Sec 4, extended by
// docs/observability-part-a.md). Validated/Merged are explicit booleans
// rather than something inferred from Outcome/EscalationReason's free-form
// strings, so the Gateway can stamp validated_at/merged_at unambiguously —
// see store.RunOutcome. Attempts/InputTokens/OutputTokens/EstimatedCost are
// pointers so an omitted field (e.g. token/cost data the wrapper script
// doesn't collect yet) is stored as SQL NULL, not a misleading literal 0.
type entry struct {
	AlertID    string `json:"alert_id"`
	ReviMode   string `json:"revi_mode"`
	Outcome    string `json:"outcome"`
	Summary    string `json:"summary"`
	DigestDate string `json:"digest_date"`

	// ServiceName/ErrorSummary identify which flap lock (queue.LockKey) this
	// run held, so the Gateway can release it the moment the run genuinely
	// finishes instead of waiting on the lock's own TTL (2026-08-19,
	// release-on-completion — see releaseLock below). Both are already on
	// hand in the runner the whole time (SERVICE_NAME/ERROR_SUMMARY env
	// vars), so this is just including values that already exist, not new
	// plumbing on the runner side.
	ServiceName  string `json:"service_name"`
	ErrorSummary string `json:"error_summary"`

	Validated             bool     `json:"validated"`
	Merged                bool     `json:"merged"`
	EscalationReason      string   `json:"escalation_reason"`
	Attempts              *int     `json:"attempts,omitempty"`
	FailureStage          string   `json:"failure_stage"`
	FailureClassification string   `json:"failure_classification"`
	InputTokens           *int     `json:"input_tokens,omitempty"`
	OutputTokens          *int     `json:"output_tokens,omitempty"`
	Model                 string   `json:"model"`
	EstimatedCost         *float64 `json:"estimated_cost,omitempty"`
}

// NewHandler returns the /v1/digest/entry handler. secret is
// REVI_WEBHOOK_SECRET, reused here as the HMAC key (Bearer token for
// /v1/alerts, HMAC for this endpoint — deliberately different schemes
// matching what each caller can produce, TRD Sec 5). runStore may be nil
// (observability writes are best-effort, never block digest recording —
// docs/observability-part-a.md).
func NewHandler(secret string, q *queue.Queue, runStore *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read error", http.StatusBadRequest)
			return
		}

		if !validSignature(r.Header.Get("X-Revi-Signature"), secret, body) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		var e entry
		if err := json.Unmarshal(body, &e); err != nil || e.AlertID == "" || e.DigestDate == "" {
			http.Error(w, "malformed or incomplete digest entry", http.StatusBadRequest)
			return
		}

		record := queue.DigestEntry{
			AlertID:  e.AlertID,
			Message:  fmt.Sprintf("[%s/%s] %s", e.ReviMode, e.Outcome, e.Summary),
			LastSeen: time.Now().UTC().Format(time.RFC3339),
		}
		if err := q.UpsertDigestEntry(r.Context(), e.DigestDate, e.AlertID, record); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		// Release-on-completion (2026-08-19): the run genuinely finished —
		// success or failure, doesn't matter — so the flap lock no longer
		// needs to wait out its own TTL. Best-effort: an old runner that
		// hasn't been updated to send these fields yet, or a delete that
		// fails, just falls back to the TTL backstop rather than blocking
		// this response.
		if e.ServiceName != "" && e.ErrorSummary != "" {
			if err := q.Locks.Delete(r.Context(), queue.LockKey(e.ServiceName, e.ErrorSummary)); err != nil {
				log.Printf("digest: failed to release flap lock for alert_id=%s service=%s: %v (will fall back to TTL)", e.AlertID, e.ServiceName, err)
			}
		}

		runStore.RecordOutcome(r.Context(), store.RunOutcome{
			AlertID:               e.AlertID,
			Validated:             e.Validated,
			Merged:                e.Merged,
			Outcome:               e.Outcome,
			EscalationReason:      e.EscalationReason,
			Attempts:              e.Attempts,
			FailureStage:          e.FailureStage,
			FailureClassification: e.FailureClassification,
			InputTokens:           e.InputTokens,
			OutputTokens:          e.OutputTokens,
			Model:                 e.Model,
			EstimatedCost:         e.EstimatedCost,
			Summary:               e.Summary,
		})

		w.WriteHeader(http.StatusAccepted)
	}
}

// validSignature checks "X-Revi-Signature: sha256=<hex HMAC>" against body,
// keyed by secret. Constant-time to avoid leaking the correct value one
// byte at a time via response timing.
func validSignature(header, secret string, body []byte) bool {
	const prefix = "sha256="
	if secret == "" || !strings.HasPrefix(header, prefix) {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(strings.TrimPrefix(header, prefix)), []byte(want))
}
