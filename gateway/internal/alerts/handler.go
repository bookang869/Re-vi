package alerts

import (
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
)

// NewHandler returns the /v1/alerts handler. secret is REVI_WEBHOOK_SECRET;
// requests must carry it as "Authorization: Bearer <secret>".
func NewHandler(secret string) http.HandlerFunc {
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

		// ponytail: NATS publish (flap lock + dispatch) lands in 2.3/2.4;
		// for now a structurally valid firing alert is just accepted.
		log.Printf("alerts: %d alert(s) mapped, ready for NATS publish", len(fired))
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
