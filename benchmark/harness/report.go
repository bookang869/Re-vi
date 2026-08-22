package main

import (
	"fmt"
	"sort"
)

// printReport prints a compact human-readable summary of every trial
// recorded for one experiment -- enough for an operator to sanity-check a
// run without opening the JSON file, not a replacement for
// docs/observability-part-c.md's (not yet written) results dashboard.
func printReport(records []TrialRecord) {
	records = dedupeLatestPerTrial(records)
	if len(records) == 0 {
		fmt.Println("no trials recorded")
		return
	}

	sort.Slice(records, func(i, j int) bool {
		if records[i].FaultID != records[j].FaultID {
			return records[i].FaultID < records[j].FaultID
		}
		return records[i].TrialNum < records[j].TrialNum
	})

	fmt.Printf("%-22s %-5s %-9s %-14s %-9s %s\n", "fault_id", "trial", "outcome", "verification", "verifier", "harness_error")
	var totalRuns, infraErrors, verifiedPass, verifiedFail, notApplicable, inconclusive int
	faultSeen := map[string]bool{}
	for _, r := range records {
		totalRuns++
		faultSeen[r.FaultID] = true

		outcome := "<pending>"
		if r.Outcome != nil {
			outcome = *r.Outcome
		}
		exitCode := "-"
		if r.VerifierExitCode != nil {
			exitCode = fmt.Sprintf("%d", *r.VerifierExitCode)
		}
		harnessErr := r.HarnessError

		switch r.VerificationOutcome {
		case string(verifyPassed):
			verifiedPass++
		case string(verifyFailed):
			verifiedFail++
		case string(verifyInconclusive):
			inconclusive++
		case verificationNotApplicable:
			notApplicable++
		}
		if harnessErr != "" {
			infraErrors++
		}

		fmt.Printf("%-22s %-5d %-9s %-14s %-9s %s\n",
			r.FaultID, r.TrialNum, outcome, valueOr(r.VerificationOutcome, "-"), exitCode, harnessErr)
	}

	fmt.Println()
	fmt.Printf("total runs: %d, unique faults: %d\n", totalRuns, len(faultSeen))
	fmt.Printf("independently verified: passed=%d failed=%d inconclusive=%d not_applicable=%d\n",
		verifiedPass, verifiedFail, inconclusive, notApplicable)
	fmt.Printf("harness/infra errors: %d\n", infraErrors)
	if verifiedPass+verifiedFail > 0 {
		fmt.Printf("independently verified repair rate (of merged+verified runs): %.1f%%\n",
			100*float64(verifiedPass)/float64(verifiedPass+verifiedFail))
	}
}

func valueOr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// dedupeLatestPerTrial collapses to the last-recorded row per (fault_id,
// trial_num), same "last write wins" rule stage1Gate already applies via its
// own map. Store.Append never overwrites -- a retried trial (e.g. a harness/
// infra error followed by a clean re-dispatch under the same trial_num, as
// happened for go-nil-deref-01's Stage 1 tag-push bug) leaves both records
// in the file, and without this the report would double-count it in
// total_runs/harness_errors forever, long after the retry succeeded.
func dedupeLatestPerTrial(records []TrialRecord) []TrialRecord {
	type key struct {
		faultID  string
		trialNum int
	}
	latest := map[key]TrialRecord{}
	var order []key
	for _, r := range records {
		k := key{r.FaultID, r.TrialNum}
		if _, seen := latest[k]; !seen {
			order = append(order, k)
		}
		latest[k] = r
	}
	out := make([]TrialRecord, 0, len(order))
	for _, k := range order {
		out = append(out, latest[k])
	}
	return out
}
