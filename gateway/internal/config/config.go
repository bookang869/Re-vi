// Package config loads Gateway configuration from environment variables.
package config

import (
	"fmt"
	"os"
)

// Mode values for REVI_MODE. Invalid/unset falls back to ModePRReview,
// per TRD FR-01 ("safest operational baseline").
const (
	ModePRReview   = "PR_REVIEW"
	ModeAutonomous = "AUTONOMOUS"
)

type Config struct {
	ListenAddr string // REVI_LISTEN_ADDR, default ":8080"
	Mode       string // REVI_MODE, default ModePRReview

	WebhookSecret string // REVI_WEBHOOK_SECRET: Bearer token for /v1/alerts, HMAC key for /v1/digest/entry

	GitHubTokenDispatch string // GITHUB_TOKEN_DISPATCH: repository_dispatch-only scope
	GitHubOwner         string // REVI_GITHUB_OWNER: target repo for repository_dispatch
	GitHubRepo          string // REVI_GITHUB_REPO

	DigestTime      string // REVI_DIGEST_TIME, default "08:00" (local)
	SlackWebhookURL string // SLACK_DIGEST_WEBHOOK_URL

	EscalationWebhookURL    string // REVI_ESCALATION_WEBHOOK_URL
	EscalationWebhookSecret string // REVI_ESCALATION_WEBHOOK_SECRET

	VictoriaLogsURL string // REVI_VICTORIALOGS_URL
	NATSURL         string // REVI_NATS_URL
}

// Load reads configuration from the environment. It only errors on values
// with no safe default that later handlers cannot function without.
func Load() (Config, error) {
	cfg := Config{
		ListenAddr: getEnvDefault("REVI_LISTEN_ADDR", ":8080"),
		Mode:       normalizeMode(os.Getenv("REVI_MODE")),

		WebhookSecret: os.Getenv("REVI_WEBHOOK_SECRET"),

		GitHubTokenDispatch: os.Getenv("GITHUB_TOKEN_DISPATCH"),
		GitHubOwner:         os.Getenv("REVI_GITHUB_OWNER"),
		GitHubRepo:          os.Getenv("REVI_GITHUB_REPO"),

		DigestTime:      getEnvDefault("REVI_DIGEST_TIME", "08:00"),
		SlackWebhookURL: os.Getenv("SLACK_DIGEST_WEBHOOK_URL"),

		EscalationWebhookURL:    os.Getenv("REVI_ESCALATION_WEBHOOK_URL"),
		EscalationWebhookSecret: os.Getenv("REVI_ESCALATION_WEBHOOK_SECRET"),

		VictoriaLogsURL: getEnvDefault("REVI_VICTORIALOGS_URL", "http://victoria-logs:9428"),
		NATSURL:         getEnvDefault("REVI_NATS_URL", "nats://nats:4222"),
	}

	if cfg.WebhookSecret == "" {
		return cfg, fmt.Errorf("REVI_WEBHOOK_SECRET is required")
	}

	return cfg, nil
}

func normalizeMode(m string) string {
	if m == ModeAutonomous {
		return ModeAutonomous
	}
	return ModePRReview
}

func getEnvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
