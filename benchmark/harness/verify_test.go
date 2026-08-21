package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func fixtureWithScript(t *testing.T, script string) Fixture {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "verify.sh")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return Fixture{FaultID: "x", dir: dir}
}

func TestRunVerifier_ExitZeroIsPassed(t *testing.T) {
	f := fixtureWithScript(t, "#!/usr/bin/env bash\necho ok\nexit 0\n")
	r := runVerifier(context.Background(), f, t.TempDir())
	if r.Outcome != verifyPassed || r.ExitCode != 0 {
		t.Fatalf("unexpected result: %+v", r)
	}
}

func TestRunVerifier_ExitOneIsFailed(t *testing.T) {
	f := fixtureWithScript(t, "#!/usr/bin/env bash\nexit 1\n")
	r := runVerifier(context.Background(), f, t.TempDir())
	if r.Outcome != verifyFailed || r.ExitCode != 1 {
		t.Fatalf("unexpected result: %+v", r)
	}
}

func TestRunVerifier_ExitTwoIsInconclusive(t *testing.T) {
	f := fixtureWithScript(t, "#!/usr/bin/env bash\nexit 2\n")
	r := runVerifier(context.Background(), f, t.TempDir())
	if r.Outcome != verifyInconclusive || r.ExitCode != 2 {
		t.Fatalf("unexpected result: %+v", r)
	}
}

func TestRunVerifier_MissingScriptIsInconclusive(t *testing.T) {
	f := Fixture{FaultID: "x", dir: t.TempDir()} // no verify.sh written
	r := runVerifier(context.Background(), f, t.TempDir())
	if r.Outcome != verifyInconclusive || r.ExitCode != -1 {
		t.Fatalf("unexpected result: %+v", r)
	}
}

func TestRunVerifier_ReceivesWorktreePathArg(t *testing.T) {
	f := fixtureWithScript(t, "#!/usr/bin/env bash\n[ \"$1\" = \"$EXPECTED\" ] && exit 0 || exit 1\n")
	worktree := t.TempDir()
	os.Setenv("EXPECTED", worktree)
	defer os.Unsetenv("EXPECTED")

	r := runVerifier(context.Background(), f, worktree)
	if r.Outcome != verifyPassed {
		t.Fatalf("verify.sh did not receive expected worktree path arg: %+v", r)
	}
}
