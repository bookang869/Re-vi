package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// setupTestOriginAndClone builds a local bare "origin" repo with one
// commit tagged baseRef, plus a local clone of it (standing in for the
// harness's own ~/revi-hermes-target clone) -- all over file:// paths, no
// network involved. Returns the clone dir and a helper to push a follow-up
// commit onto a branch in origin (simulating what autonomous-promote.sh's
// merge would leave behind).
func setupTestOriginAndClone(t *testing.T, baseRef string) (cloneDir string, seedDir string) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	bare := filepath.Join(root, "origin.git")
	seedDir = filepath.Join(root, "seed")
	cloneDir = filepath.Join(root, "clone")

	if _, err := runGit(ctx, root, "init", "--bare", bare); err != nil {
		t.Fatalf("init bare: %v", err)
	}
	if _, err := runGit(ctx, root, "clone", bare, seedDir); err != nil {
		t.Fatalf("clone seed: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(seedDir, "fixture-app-go"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seedDir, "fixture-app-go", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, seedDir, withGitIdentity("add", "-A")...); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := runGit(ctx, seedDir, withGitIdentity("commit", "-m", "seed")...); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if _, err := runGit(ctx, seedDir, "tag", baseRef); err != nil {
		t.Fatalf("tag: %v", err)
	}
	if _, err := runGit(ctx, seedDir, "push", "origin", "HEAD", "refs/tags/"+baseRef); err != nil {
		t.Fatalf("push seed+tag: %v", err)
	}

	if _, err := runGit(ctx, root, "clone", bare, cloneDir); err != nil {
		t.Fatalf("clone harness dir: %v", err)
	}
	return cloneDir, seedDir
}

// withGitIdentity prefixes a command with an inline -c user.name/user.email
// override, needed for commits in a repo with no global git identity
// configured (e.g. a CI runner) -- always returns a fresh slice so callers
// can safely reuse the same args across multiple invocations.
func withGitIdentity(args ...string) []string {
	return append([]string{"-c", "user.name=test", "-c", "user.email=test@example.com"}, args...)
}

func TestResetMergeTargetBranch_CreatesBranchAtBaseRef(t *testing.T) {
	if _, err := runGit(context.Background(), ".", "--version"); err != nil {
		t.Skip("git not available")
	}
	cloneDir, seedDir := setupTestOriginAndClone(t, "fault/test-01-base")
	ctx := context.Background()

	f := Fixture{FaultID: "test-01", BaseRef: "fault/test-01-base"}
	if err := resetMergeTargetBranch(ctx, cloneDir, f); err != nil {
		t.Fatalf("resetMergeTargetBranch: %v", err)
	}

	// Confirm the branch now exists on origin (via the seed clone, a
	// second independent client of the same bare repo) and points at the
	// tagged commit.
	if _, err := runGit(ctx, seedDir, "fetch", "origin", "refs/heads/benchmark/test-01:refs/heads/check-branch"); err != nil {
		t.Fatalf("fetch created branch: %v", err)
	}
	branchSHA, err := runGit(ctx, seedDir, "rev-parse", "refs/heads/check-branch")
	if err != nil {
		t.Fatalf("rev-parse branch: %v", err)
	}
	tagSHA, err := runGit(ctx, seedDir, "rev-parse", "fault/test-01-base")
	if err != nil {
		t.Fatalf("rev-parse tag: %v", err)
	}
	if branchSHA != tagSHA {
		t.Fatalf("benchmark/test-01 (%s) does not point at base_ref (%s)", branchSHA, tagSHA)
	}
}

// TestResetMergeTargetBranch_AnnotatedTag guards against a real bug found
// running go-nil-deref-01's Stage 1 trial 2026-08-21: its base_ref
// (fault/go-nil-deref-01-base-v2) is an annotated tag (git tag -a, cut
// during an earlier postmortem), unlike every other fault's lightweight
// tag. Pushing an annotated tag's ref directly to refs/heads/* pushes the
// *tag object*, not the commit it points at -- git/GitHub reject that
// silently ("remote rejected", no reason given) since branches may only
// point at commits. resetMergeTargetBranch must dereference to the commit
// first so it works for either kind of tag.
func TestResetMergeTargetBranch_AnnotatedTag(t *testing.T) {
	if _, err := runGit(context.Background(), ".", "--version"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	root := t.TempDir()
	bare := filepath.Join(root, "origin.git")
	seedDir := filepath.Join(root, "seed")
	cloneDir := filepath.Join(root, "clone")

	if _, err := runGit(ctx, root, "init", "--bare", bare); err != nil {
		t.Fatalf("init bare: %v", err)
	}
	if _, err := runGit(ctx, root, "clone", bare, seedDir); err != nil {
		t.Fatalf("clone seed: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(seedDir, "fixture-app-go"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seedDir, "fixture-app-go", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, seedDir, withGitIdentity("add", "-A")...); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := runGit(ctx, seedDir, withGitIdentity("commit", "-m", "seed")...); err != nil {
		t.Fatalf("commit: %v", err)
	}
	baseRef := "fault/test-annotated-01-base"
	if _, err := runGit(ctx, seedDir, withGitIdentity("tag", "-a", baseRef, "-m", "annotated base ref")...); err != nil {
		t.Fatalf("annotated tag: %v", err)
	}
	if _, err := runGit(ctx, seedDir, "push", "origin", "HEAD", "refs/tags/"+baseRef); err != nil {
		t.Fatalf("push seed+tag: %v", err)
	}
	if _, err := runGit(ctx, root, "clone", bare, cloneDir); err != nil {
		t.Fatalf("clone harness dir: %v", err)
	}

	f := Fixture{FaultID: "test-annotated-01", BaseRef: baseRef}
	if err := resetMergeTargetBranch(ctx, cloneDir, f); err != nil {
		t.Fatalf("resetMergeTargetBranch with annotated tag: %v", err)
	}

	if _, err := runGit(ctx, seedDir, "fetch", "origin", "refs/heads/benchmark/test-annotated-01:refs/heads/check-branch"); err != nil {
		t.Fatalf("fetch created branch: %v", err)
	}
	branchSHA, err := runGit(ctx, seedDir, "rev-parse", "refs/heads/check-branch")
	if err != nil {
		t.Fatalf("rev-parse branch: %v", err)
	}
	branchType, err := runGit(ctx, seedDir, "cat-file", "-t", branchSHA)
	if err != nil {
		t.Fatalf("cat-file -t branch tip: %v", err)
	}
	if branchType != "commit" {
		t.Fatalf("benchmark/test-annotated-01 points at a %s, want commit", branchType)
	}
	commitSHA, err := runGit(ctx, seedDir, "rev-parse", baseRef+"^{commit}")
	if err != nil {
		t.Fatalf("rev-parse tag^{commit}: %v", err)
	}
	if branchSHA != commitSHA {
		t.Fatalf("benchmark/test-annotated-01 (%s) does not point at base_ref's commit (%s)", branchSHA, commitSHA)
	}
}

func TestMergedCommitSHAAndCheckoutWorktree(t *testing.T) {
	if _, err := runGit(context.Background(), ".", "--version"); err != nil {
		t.Skip("git not available")
	}
	cloneDir, seedDir := setupTestOriginAndClone(t, "fault/test-02-base")
	ctx := context.Background()
	f := Fixture{FaultID: "test-02", BaseRef: "fault/test-02-base"}

	if err := resetMergeTargetBranch(ctx, cloneDir, f); err != nil {
		t.Fatalf("resetMergeTargetBranch: %v", err)
	}

	// Simulate autonomous-promote.sh's merge: push a new commit directly
	// onto the integration branch from an independent clone of the same
	// bare repo.
	if _, err := runGit(ctx, seedDir, "fetch", "origin", "refs/heads/benchmark/test-02:refs/heads/benchmark/test-02"); err != nil {
		t.Fatalf("fetch integration branch into seed: %v", err)
	}
	if _, err := runGit(ctx, seedDir, "checkout", "benchmark/test-02"); err != nil {
		t.Fatalf("checkout integration branch: %v", err)
	}
	if err := os.WriteFile(filepath.Join(seedDir, "fixture-app-go", "main.go"), []byte("package main\n// fixed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, seedDir, withGitIdentity("commit", "-am", "Hermes autonomous fix")...); err != nil {
		t.Fatalf("commit fix: %v", err)
	}
	if _, err := runGit(ctx, seedDir, "push", "origin", "benchmark/test-02"); err != nil {
		t.Fatalf("push merge: %v", err)
	}
	expectedSHA, err := runGit(ctx, seedDir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}

	gotSHA, err := mergedCommitSHA(ctx, cloneDir, f.FaultID)
	if err != nil {
		t.Fatalf("mergedCommitSHA: %v", err)
	}
	if gotSHA != expectedSHA {
		t.Fatalf("mergedCommitSHA = %s, want %s", gotSHA, expectedSHA)
	}

	wt, err := checkoutWorktree(ctx, cloneDir, gotSHA, f.FaultID, 1)
	if err != nil {
		t.Fatalf("checkoutWorktree: %v", err)
	}
	defer wt.Cleanup()

	content, err := os.ReadFile(filepath.Join(wt.Path, "fixture-app-go", "main.go"))
	if err != nil {
		t.Fatalf("reading checked-out file: %v", err)
	}
	if string(content) != "package main\n// fixed\n" {
		t.Fatalf("unexpected checked-out content: %q", string(content))
	}

	wt.Cleanup()
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Fatalf("expected worktree dir removed after Cleanup, stat err=%v", err)
	}
}
