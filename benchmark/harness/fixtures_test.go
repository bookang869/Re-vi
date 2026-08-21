package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFixture(t *testing.T, dir, faultID, metaJSON string, withVerify bool) {
	t.Helper()
	fdir := filepath.Join(dir, faultID)
	if err := os.MkdirAll(fdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fdir, "meta.json"), []byte(metaJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	if withVerify {
		if err := os.WriteFile(filepath.Join(fdir, "verify.sh"), []byte("#!/usr/bin/env bash\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

const validMeta = `{
  "fault_id": "go-nil-deref-01",
  "fault_type": "runtime exception",
  "language_runtime": "go",
  "fixture_app": "fixture-app-go",
  "base_ref": "fault/go-nil-deref-01-base-v2",
  "service_name": "fixture-app-go",
  "error_summary": "panic: nil pointer",
  "expected_behavior": "GET /summarize returns HTTP 200"
}`

func TestLoadFixture_Valid(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "go-nil-deref-01", validMeta, true)

	f, err := loadFixture(dir, "go-nil-deref-01")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.FaultID != "go-nil-deref-01" || f.FixtureApp != "fixture-app-go" || f.BaseRef != "fault/go-nil-deref-01-base-v2" {
		t.Fatalf("unexpected fixture: %+v", f)
	}
}

func TestLoadFixture_MissingVerifyScript(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "go-nil-deref-01", validMeta, false)

	if _, err := loadFixture(dir, "go-nil-deref-01"); err == nil {
		t.Fatal("expected error for missing verify.sh, got nil")
	}
}

func TestLoadFixture_FaultIDMismatch(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "wrong-dir-name", validMeta, true)

	if _, err := loadFixture(dir, "wrong-dir-name"); err == nil {
		t.Fatal("expected error for fault_id/directory mismatch, got nil")
	}
}

func TestLoadFixture_MissingRequiredField(t *testing.T) {
	dir := t.TempDir()
	meta := `{"fault_id":"x","fault_type":"","language_runtime":"go","fixture_app":"fixture-app-go","base_ref":"t","service_name":"s","error_summary":"e","expected_behavior":"b"}`
	writeFixture(t, dir, "x", meta, true)

	if _, err := loadFixture(dir, "x"); err == nil {
		t.Fatal("expected error for empty fault_type, got nil")
	}
}

func TestLoadAllFixtures_SkipsTemplate(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "_template", validMeta, true) // fault_id won't match dir, but should never be loaded
	writeFixture(t, dir, "go-nil-deref-01", validMeta, true)
	writeFixture(t, dir, "aaa-first", `{"fault_id":"aaa-first","fault_type":"runtime exception","language_runtime":"go","fixture_app":"fixture-app-go","base_ref":"t","service_name":"s","error_summary":"e","expected_behavior":"b"}`, true)

	fixtures, err := loadAllFixtures(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fixtures) != 2 {
		t.Fatalf("expected 2 fixtures (template skipped), got %d: %+v", len(fixtures), fixtures)
	}
	// sorted by fault_id
	if fixtures[0].FaultID != "aaa-first" || fixtures[1].FaultID != "go-nil-deref-01" {
		t.Fatalf("expected sorted order, got %s, %s", fixtures[0].FaultID, fixtures[1].FaultID)
	}
}
