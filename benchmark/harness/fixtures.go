package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Fixture mirrors benchmark/README.md's meta.json field table one-to-one.
// FaultID doubles as the directory name it was loaded from.
type Fixture struct {
	FaultID          string `json:"fault_id"`
	FaultType        string `json:"fault_type"`
	LanguageRuntime  string `json:"language_runtime"`
	FixtureApp       string `json:"fixture_app"`
	BaseRef          string `json:"base_ref"`
	ServiceName      string `json:"service_name"`
	ErrorSummary     string `json:"error_summary"`
	ExpectedBehavior string `json:"expected_behavior"`

	// dir is this fixture's directory (benchmark/fixtures/<fault_id>) --
	// verify.sh lives alongside meta.json, resolved relative to it.
	dir string
}

func (f Fixture) verifyScriptPath() string {
	return filepath.Join(f.dir, "verify.sh")
}

// loadFixture reads and validates one fault's meta.json. Validation here is
// deliberately about the harness's own required inputs (never dispatch an
// alert with an empty ref/service_name/base_ref) -- it does not duplicate
// fault-authoring judgment calls like fault_type category weighting, which
// stay a human decision per docs/observability-part-b.md's Scale section.
func loadFixture(fixturesDir, faultID string) (Fixture, error) {
	dir := filepath.Join(fixturesDir, faultID)
	raw, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return Fixture{}, fmt.Errorf("reading %s/meta.json: %w", dir, err)
	}

	var f Fixture
	if err := json.Unmarshal(raw, &f); err != nil {
		return Fixture{}, fmt.Errorf("parsing %s/meta.json: %w", dir, err)
	}
	f.dir = dir

	if f.FaultID != faultID {
		return Fixture{}, fmt.Errorf("%s/meta.json: fault_id %q does not match directory name %q", dir, f.FaultID, faultID)
	}
	for field, v := range map[string]string{
		"fault_type":        f.FaultType,
		"language_runtime":  f.LanguageRuntime,
		"fixture_app":       f.FixtureApp,
		"base_ref":          f.BaseRef,
		"service_name":      f.ServiceName,
		"error_summary":     f.ErrorSummary,
		"expected_behavior": f.ExpectedBehavior,
	} {
		if v == "" {
			return Fixture{}, fmt.Errorf("%s/meta.json: %s is required", dir, field)
		}
	}
	if _, err := os.Stat(f.verifyScriptPath()); err != nil {
		return Fixture{}, fmt.Errorf("%s: verify.sh missing or unreadable: %w", dir, err)
	}

	return f, nil
}

// loadAllFixtures returns every real fault under fixturesDir, sorted by
// fault_id for deterministic ordering across runs. "_template" is a
// scaffold (benchmark/README.md), never a real fault, and is skipped.
func loadAllFixtures(fixturesDir string) ([]Fixture, error) {
	entries, err := os.ReadDir(fixturesDir)
	if err != nil {
		return nil, fmt.Errorf("reading fixtures dir %s: %w", fixturesDir, err)
	}

	var ids []string
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "_template" {
			continue
		}
		ids = append(ids, e.Name())
	}
	sort.Strings(ids)

	fixtures := make([]Fixture, 0, len(ids))
	for _, id := range ids {
		f, err := loadFixture(fixturesDir, id)
		if err != nil {
			return nil, err
		}
		fixtures = append(fixtures, f)
	}
	return fixtures, nil
}
