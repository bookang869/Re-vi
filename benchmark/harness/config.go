// Command harness automates docs/observability-part-b.md's benchmark loop:
// dispatch a fault's synthetic alert through the real Gateway, poll
// GET /v1/runs/{alert_id} for completion, apply the fault's independent
// verify.sh against whatever merged, and record the result. It never talks
// to Hermes or hermes-triage.yml directly -- from the pipeline's point of
// view a benchmark trial looks exactly like a real alert.
package main

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config is sourced from the environment (matching this repo's existing
// convention -- gateway/internal/config, scripts/rehearsals/run.sh -- so an
// operator running the harness alongside the rehearsal scripts doesn't have
// to learn a second configuration style).
type Config struct {
	// GatewayURL and GatewaySecret talk to the same Bearer-token scheme
	// GET /v1/runs/{alert_id} and POST /v1/alerts already use (REVI_
	// WEBHOOK_SECRET, reused -- see gateway/internal/runs/handler.go).
	GatewayURL    string
	GatewaySecret string

	// TargetClone is a local clone of bookang869/revi-hermes-target with a
	// working, authenticated "origin" remote (push access) -- the harness
	// force-resets each fault's benchmark/<fault_id> branch here before
	// every trial, and fetches it back after a MERGED outcome to verify
	// against. docs/observability-part-b.md's harness handoff note records
	// this as a local clone at ~/revi-hermes-target.
	TargetClone string

	// FixturesDir holds one benchmark/fixtures/<fault_id>/{meta.json,
	// verify.sh} per fault (see benchmark/README.md). _template is skipped.
	FixturesDir string

	// ResultsDir is where the harness's own per-experiment JSON result
	// files live -- docs/observability-part-b.md "Locked: verifier
	// isolation": independent_verification_passed lives in the harness's
	// own store, not a Gateway column.
	ResultsDir string

	// PollInterval/MaxPolls bound how long the harness waits for one
	// trial's outcome to appear. docs/observability-part-b.md's
	// harness/pipeline section sizes this at "up to 200 unattended polls".
	PollInterval time.Duration
	MaxPolls     int
}

func loadConfig() (Config, error) {
	cfg := Config{
		GatewayURL:    getenvDefault("REVI_GATEWAY_URL", "http://localhost:8080"),
		GatewaySecret: os.Getenv("REVI_WEBHOOK_SECRET"),
		TargetClone:   getenvDefault("REVI_HARNESS_TARGET_CLONE", os.ExpandEnv("$HOME/revi-hermes-target")),
		FixturesDir:   getenvDefault("REVI_HARNESS_FIXTURES_DIR", "../fixtures"),
		ResultsDir:    getenvDefault("REVI_HARNESS_RESULTS_DIR", "./results"),
		PollInterval:  15 * time.Second,
		MaxPolls:      200,
	}

	if cfg.GatewaySecret == "" {
		return cfg, fmt.Errorf("REVI_WEBHOOK_SECRET is required (same secret the Gateway itself uses)")
	}
	if v := os.Getenv("REVI_HARNESS_POLL_INTERVAL_SECONDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return cfg, fmt.Errorf("REVI_HARNESS_POLL_INTERVAL_SECONDS: %w", err)
		}
		cfg.PollInterval = time.Duration(n) * time.Second
	}
	if v := os.Getenv("REVI_HARNESS_MAX_POLLS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return cfg, fmt.Errorf("REVI_HARNESS_MAX_POLLS: %w", err)
		}
		cfg.MaxPolls = n
	}

	return cfg, nil
}

func getenvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
