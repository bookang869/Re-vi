package config

import "testing"

func TestNormalizeMode(t *testing.T) {
	cases := map[string]string{
		"AUTONOMOUS": ModeAutonomous,
		"PR_REVIEW":  ModePRReview,
		"":           ModePRReview,
		"bogus":      ModePRReview,
	}
	for in, want := range cases {
		if got := normalizeMode(in); got != want {
			t.Errorf("normalizeMode(%q) = %q, want %q", in, got, want)
		}
	}
}

// setRequiredEnv sets every env var Load() requires to succeed, so
// individual tests can unset just the one they're checking.
func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("REVI_WEBHOOK_SECRET", "shh")
	t.Setenv("GITHUB_TOKEN_DISPATCH", "ghp_test")
	t.Setenv("REVI_GITHUB_OWNER", "acme")
	t.Setenv("REVI_GITHUB_REPO", "widgets")
}

func TestLoadRequiresWebhookSecret(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("REVI_WEBHOOK_SECRET", "")
	if _, err := Load(); err == nil {
		t.Error("Load() with no REVI_WEBHOOK_SECRET should error")
	}
}

func TestLoadRequiresGitHubDispatchConfig(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("GITHUB_TOKEN_DISPATCH", "")
	if _, err := Load(); err == nil {
		t.Error("Load() with no GITHUB_TOKEN_DISPATCH should error")
	}

	setRequiredEnv(t)
	t.Setenv("REVI_GITHUB_OWNER", "")
	if _, err := Load(); err == nil {
		t.Error("Load() with no REVI_GITHUB_OWNER should error")
	}

	setRequiredEnv(t)
	t.Setenv("REVI_GITHUB_REPO", "")
	if _, err := Load(); err == nil {
		t.Error("Load() with no REVI_GITHUB_REPO should error")
	}
}

func TestLoadDefaults(t *testing.T) {
	setRequiredEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.ListenAddr != ":8080" || cfg.Mode != ModePRReview || cfg.DigestTime != "08:00" {
		t.Errorf("unexpected defaults: %+v", cfg)
	}
}
