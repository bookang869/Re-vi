package alerts

import (
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"

	"github.com/bookang869/Re-vi/gateway/internal/queue"
)

// NewHandler returns the /v1/alerts handler. secret is REVI_WEBHOOK_SECRET;
// requests must carry it as "Authorization: Bearer <secret>". mode is the
// Gateway's own REVI_MODE, stamped into the lock record for visibility.
func NewHandler(secret string, q *queue.Queue, mode string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !validBearer(r.Header.Get("Authorization"), secret) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		var payload webhookPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "malformed JSON body", http.StatusBadRequest)
			return
		}

		fired, dropped, err := mapAlerts(payload)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if dropped > 0 {
			log.Printf("alerts: dropped %d resolved alert(s)", dropped)
		}

		if len(fired) == 0 {
			w.WriteHeader(http.StatusOK)
			return
		}

		published := 0
		for _, a := range fired {
			ok, err := Publish(r.Context(), q, mode, a)
			if err != nil {
				log.Printf("alerts: publish failed for %s: %v", a.AlertID, err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			if ok {
				published++
			}
		}

		if published == 0 {
			// every alert in this group hit an active flap lock
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusAccepted)
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
