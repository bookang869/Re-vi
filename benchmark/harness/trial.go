package main

import (
	"context"
	"fmt"
	"log"
	"time"
)

// runTrial drives exactly one dispatch of one fault through the real
// pipeline, end to end: reset the fault's integration branch, fire the
// synthetic alert, poll until the Gateway reports an outcome, and -- only
// if that outcome was a real merge -- independently verify the merged
// commit. It always returns a TrialRecord and always persists it to the
// store before returning, whether the trial completed cleanly, produced a
// real (pass or fail) verdict, or hit a harness/infra error; the caller
// doesn't need its own error-handling path on top of what's already in
// rec.HarnessError.
func runTrial(ctx context.Context, cfg Config, gw *GatewayClient, store *Store, f Fixture, experimentID string, trialNum int) TrialRecord {
	rec := TrialRecord{
		ExperimentID:    experimentID,
		FaultID:         f.FaultID,
		FaultType:       f.FaultType,
		LanguageRuntime: f.LanguageRuntime,
		FixtureApp:      f.FixtureApp,
		TrialNum:        trialNum,
		DispatchedAt:    time.Now().UTC().Format(time.RFC3339),
	}
	defer func() {
		rec.CompletedAt = time.Now().UTC().Format(time.RFC3339)
		if err := store.Append(rec); err != nil {
			// The store itself is best-effort persistence, matching this
			// repo's own SQLite-is-observability-not-a-gate principle
			// (CLAUDE.md) -- a failed write is logged, not fatal, so one
			// bad disk write doesn't take down 31 other faults' goroutines.
			log.Printf("harness: %s trial %d: failed to persist record: %v", f.FaultID, trialNum, err)
		}
	}()

	log.Printf("harness: %s trial %d: resetting %s to %s", f.FaultID, trialNum, mergeTargetBranch(f.FaultID), f.BaseRef)
	if err := resetMergeTargetBranch(ctx, cfg.TargetClone, f); err != nil {
		rec.HarnessError = fmt.Sprintf("reset merge target: %v", err)
		return rec
	}

	fingerprint := fmt.Sprintf("t%d-%d", trialNum, time.Now().UnixNano())
	log.Printf("harness: %s trial %d: dispatching alert", f.FaultID, trialNum)
	alertID, err := gw.dispatchAlert(ctx, f, experimentID, trialNum, fingerprint)
	if err != nil {
		rec.HarnessError = fmt.Sprintf("dispatch alert: %v", err)
		return rec
	}
	rec.AlertID = alertID

	status, err := pollUntilOutcome(ctx, gw, alertID, cfg.PollInterval, cfg.MaxPolls)
	if err != nil {
		rec.HarnessError = fmt.Sprintf("poll for outcome: %v", err)
		return rec
	}

	rec.Outcome = status.Outcome
	rec.EscalationReason = status.EscalationReason
	rec.Attempts = status.Attempts
	rec.FailureStage = status.FailureStage
	rec.ValidatedAt = status.ValidatedAt
	rec.MergedAt = status.MergedAt

	if status.Outcome == nil || *status.Outcome != "MERGED" {
		rec.VerificationOutcome = verificationNotApplicable
		log.Printf("harness: %s trial %d: outcome=%v, nothing to independently verify", f.FaultID, trialNum, derefOrNil(status.Outcome))
		return rec
	}

	sha, err := mergedCommitSHA(ctx, cfg.TargetClone, f.FaultID)
	if err != nil {
		rec.HarnessError = fmt.Sprintf("resolve merged commit: %v", err)
		return rec
	}
	rec.MergeSHA = sha

	wt, err := checkoutWorktree(ctx, cfg.TargetClone, sha, f.FaultID, trialNum)
	if err != nil {
		rec.HarnessError = fmt.Sprintf("checkout worktree: %v", err)
		return rec
	}
	defer wt.Cleanup()

	log.Printf("harness: %s trial %d: running verify.sh against %s", f.FaultID, trialNum, sha[:min(len(sha), 12)])
	result := runVerifier(ctx, f, wt.Path)
	rec.VerificationOutcome = string(result.Outcome)
	rec.VerifierExitCode = &result.ExitCode
	rec.VerifierOutput = result.Output

	return rec
}

// pollUntilOutcome waits for GET /v1/runs/{alert_id} to report a non-nil
// outcome. A 404 (found=false, err=nil) means the row doesn't exist yet --
// this is the expected state until the runner's POST /v1/digest/entry
// lands, not an error -- so it's treated the same as "found but outcome
// still nil": keep waiting. docs/observability-part-b.md sizes the bound
// at "up to 200 unattended polls".
func pollUntilOutcome(ctx context.Context, gw *GatewayClient, alertID string, interval time.Duration, maxPolls int) (*runStatus, error) {
	for i := 0; i < maxPolls; i++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}

		status, found, err := gw.getRun(ctx, alertID)
		if err != nil {
			return nil, err
		}
		if found && status.Outcome != nil {
			return status, nil
		}
	}
	return nil, fmt.Errorf("no outcome after %d polls (alert_id=%s)", maxPolls, alertID)
}

func derefOrNil(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}
