package main

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"sync"
	"time"
)

// verifyMu serializes every verify.sh invocation across the whole harness
// process, regardless of which fault or fixture_app it belongs to. Every
// fixture-app-* boots on a hardcoded :8080 (revi-hermes-target/scripts/
// resolve-boot-command.sh has no port override for any of the four
// languages), and go-nil-deref-01's verify.sh already documents the
// consequence: a verifier's health check only confirms *something*
// answers on that port, not that it's this trial's freshly-built binary.
// Two verify.sh runs racing for :8080 -- even across two different
// fixture_apps -- produces a silent false-negative "verified incorrect"
// against a fix that was actually correct, exactly what bit the first
// live trial for an unrelated reason (a stray local Gateway container on
// the same port). One mutex removes the whole class of collision rather
// than trying to track which faults happen to share a port.
var verifyMu sync.Mutex

const verifyTimeout = 10 * time.Minute

// verifyOutcome is the three-way verdict from benchmark/README.md's
// verify.sh contract -- never conflate "inconclusive" with "failed": a
// verifier that couldn't reach a verdict (build/boot/env failure in its
// own logic) must never silently count as a scored incorrect repair.
type verifyOutcome string

const (
	verifyPassed       verifyOutcome = "passed"
	verifyFailed       verifyOutcome = "failed"
	verifyInconclusive verifyOutcome = "inconclusive"
)

type verifyResult struct {
	Outcome  verifyOutcome
	ExitCode int
	Output   string
}

// runVerifier invokes a fault's verify.sh against a checked-out worktree
// and maps its exit code per the three-way contract. A non-exec.ExitError
// failure (verify.sh not found, not executable, timeout) is also mapped to
// verifyInconclusive with exitCode -1 -- it's a harness/environment
// problem, not a verdict about Hermes's fix, and must be handled
// identically to the verifier's own exit-2+ case by every caller.
func runVerifier(ctx context.Context, f Fixture, worktreePath string) verifyResult {
	verifyMu.Lock()
	defer verifyMu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, verifyTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, f.verifyScriptPath(), worktreePath)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()

	output := out.String()
	switch {
	case err == nil:
		return verifyResult{Outcome: verifyPassed, ExitCode: 0, Output: output}
	default:
		exitCode, ok := exitCodeOf(err)
		if !ok {
			return verifyResult{Outcome: verifyInconclusive, ExitCode: -1,
				Output: output + fmt.Sprintf("\nharness: verify.sh did not run to completion: %v", err)}
		}
		if exitCode == 1 {
			return verifyResult{Outcome: verifyFailed, ExitCode: 1, Output: output}
		}
		return verifyResult{Outcome: verifyInconclusive, ExitCode: exitCode, Output: output}
	}
}

func exitCodeOf(err error) (int, bool) {
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), true
	}
	return 0, false
}
