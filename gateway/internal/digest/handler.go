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
	"net/http"
	"strings"
	"time"

	"github.com/bookang869/Re-vi/gateway/internal/queue"
)

// entry is the runner's POST body (TRD Sec 4).
type entry struct {
	AlertID    string `json:"alert_id"`
	ReviMode   string `json:"revi_mode"`
	Outcome    string `json:"outcome"`
	Summary    string `json:"summary"`
	DigestDate string `json:"digest_date"`
}

// NewHandler returns the /v1/digest/entry handler. secret is
// REVI_WEBHOOK_SECRET, reused here as the HMAC key (Bearer token for
// /v1/alerts, HMAC for this endpoint — deliberately different schemes
// matching what each caller can produce, TRD Sec 5).
func NewHandler(secret string, q *queue.Queue) http.HandlerFunc {
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
