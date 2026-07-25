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

func TestLoadRequiresWebhookSecret(t *testing.T) {
	t.Setenv("REVI_WEBHOOK_SECRET", "")
	if _, err := Load(); err == nil {
		t.Error("Load() with no REVI_WEBHOOK_SECRET should error")
	}

	t.Setenv("REVI_WEBHOOK_SECRET", "shh")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.ListenAddr != ":8080" || cfg.Mode != ModePRReview || cfg.DigestTime != "08:00" {
		t.Errorf("unexpected defaults: %+v", cfg)
	}
}
