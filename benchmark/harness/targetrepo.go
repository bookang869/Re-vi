package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// gitMu serializes every git operation this process issues against the
// shared revi-hermes-target clone. Stage 1 dispatches up to 32 faults
// concurrently, and each needs to mutate/read refs in the same working
// copy (reset its own benchmark/<fault_id> branch, fetch it back, add a
// worktree). Per-ref file locking would likely make most of this safe to
// run in parallel, but the git operations here are a few seconds each and
// nowhere near the harness's actual bottleneck (dispatch + poll, which can
// take minutes per trial) -- serializing them trades a little wall-clock
// time for not having to reason about packed-refs rewrites or worktree
// metadata races under real concurrency.
var gitMu sync.Mutex

// runGit runs a git command in dir with a bounded timeout, returning
// combined stdout+stderr on failure so the caller's error message is
// actionable without needing -v.
func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s (in %s): %w: %s", strings.Join(args, " "), dir, err, out.String())
	}
	return strings.TrimSpace(out.String()), nil
}

// resetMergeTargetBranch force-resets a fault's per-fault integration
// branch (benchmark/<fault_id>) to its frozen base_ref tag, on the remote,
// before every trial including repeats -- the "clean baseline" rule
// (docs/observability-part-b.md) requires every trial to start from the
// same fixed commit, never from whatever the branch or main happens to be
// at trial time. autonomous-promote.sh's MERGE_TARGET then merges each
// trial's independent fix into this branch instead of main, so repeat
// trials never collide with each other's differently-worded fixes.
func resetMergeTargetBranch(ctx context.Context, cloneDir string, f Fixture) error {
	gitMu.Lock()
	defer gitMu.Unlock()

	// Fetch the fault's frozen tag by name (not "--tags", which would pull
	// every tag in the target repo unnecessarily on every trial).
	if _, err := runGit(ctx, cloneDir, "fetch", "--force", "origin",
		"refs/tags/"+f.BaseRef+":refs/tags/"+f.BaseRef); err != nil {
		return fmt.Errorf("fetch base_ref %s: %w", f.BaseRef, err)
	}

	branch := mergeTargetBranch(f.FaultID)
	if _, err := runGit(ctx, cloneDir, "push", "--force", "origin",
		"refs/tags/"+f.BaseRef+":refs/heads/"+branch); err != nil {
		return fmt.Errorf("reset %s to %s: %w", branch, f.BaseRef, err)
	}
	return nil
}

// mergedCommitSHA resolves the current tip of a fault's integration
// branch on the remote -- after a MERGED outcome, this literally is the
// merge commit autonomous-promote.sh created (docs/observability-part-b.md
// "Locked: verifier isolation": "the harness checks out that merge commit
// itself"). Fetches into a per-fault local ref (refs/harness/<fault_id>)
// rather than the ambiguous shared FETCH_HEAD, so concurrent fetches for
// other faults can't race on it.
func mergedCommitSHA(ctx context.Context, cloneDir string, faultID string) (string, error) {
	gitMu.Lock()
	defer gitMu.Unlock()

	branch := mergeTargetBranch(faultID)
	localRef := "refs/harness/" + faultID
	if _, err := runGit(ctx, cloneDir, "fetch", "--force", "origin",
		"refs/heads/"+branch+":"+localRef); err != nil {
		return "", fmt.Errorf("fetch %s: %w", branch, err)
	}
	sha, err := runGit(ctx, cloneDir, "rev-parse", localRef)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", localRef, err)
	}
	return sha, nil
}

// verifyWorktree is a checked-out copy of the target repo at a specific
// commit, ready for a verifier to run against. Path is absolute; Cleanup
// removes the worktree (and its branch registration) when the caller is
// done -- always call it, even on a failed verify, so worktrees don't pile
// up across 96 trials.
type verifyWorktree struct {
	Path    string
	Cleanup func()
}

// checkoutWorktree adds a detached git worktree at the given commit,
// isolated per trial (distinct directory, no shared working tree state)
// so a fault's verify.sh invocation can't be disturbed by another fault's
// concurrent git operations. Verifier execution itself is still globally
// serialized elsewhere (verify.go) -- this only isolates the checkout.
func checkoutWorktree(ctx context.Context, cloneDir, sha, faultID string, trialNum int) (*verifyWorktree, error) {
	gitMu.Lock()
	worktreeRoot := filepath.Join(os.TempDir(), "revi-harness-worktrees")
	if err := os.MkdirAll(worktreeRoot, 0o755); err != nil {
		gitMu.Unlock()
		return nil, fmt.Errorf("create worktree root: %w", err)
	}
	path := filepath.Join(worktreeRoot, fmt.Sprintf("%s-t%d-%d", faultID, trialNum, time.Now().UnixNano()))

	_, err := runGit(ctx, cloneDir, "worktree", "add", "--detach", "--force", path, sha)
	gitMu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("add worktree for %s: %w", sha, err)
	}

	cleanup := func() {
		gitMu.Lock()
		defer gitMu.Unlock()
		_, _ = runGit(ctx, cloneDir, "worktree", "remove", "--force", path)
	}
	return &verifyWorktree{Path: path, Cleanup: cleanup}, nil
}
