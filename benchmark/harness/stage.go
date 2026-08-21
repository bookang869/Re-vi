package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// runStage1 dispatches exactly one trial per fault, all faults in
// parallel -- distinct faults never share the flap lock, only repeat
// trials of the *same* fault do (docs/observability-part-b.md "Locked:
// staged rollout"). Returns every trial record plus whether the mechanical
// gate passed: every fault must have completed and scored (pass, fail, or
// a fault correctly failing its verifier) with zero harness/infra errors.
// A fault's fix being wrong is a valid Stage 1 result; a dispatch/poll/git
// failure is not -- conflating the two would block Stage 2 on the wrong
// signal.
func runStage1(ctx context.Context, cfg Config, gw *GatewayClient, store *Store, fixtures []Fixture) (records []TrialRecord, gatePassed bool) {
	records = make([]TrialRecord, len(fixtures))
	var wg sync.WaitGroup
	for i, f := range fixtures {
		wg.Add(1)
		go func(i int, f Fixture) {
			defer wg.Done()
			records[i] = runTrial(ctx, cfg, gw, store, f, store.experimentIDHint, 1)
		}(i, f)
	}
	wg.Wait()

	gatePassed = true
	for _, r := range records {
		if r.HarnessError != "" {
			gatePassed = false
			log.Printf("harness: STAGE 1 GATE FAIL: %s trial 1: %s", r.FaultID, r.HarnessError)
		}
	}
	return records, gatePassed
}

// runStage2 runs the remaining two trials per fault (docs/observability-
// part-b.md: 3 trials total). Within one fault, trials run strictly
// sequentially -- trial 3 must not dispatch until trial 2's outcome (and
// thus its POST /v1/digest/entry flap-lock release) is already known,
// same as trial 2 waits for trial 1 -- but the 32 faults still run
// concurrently with each other, so this stage is not 3x slower overall,
// only serialized per fault.
func runStage2(ctx context.Context, cfg Config, gw *GatewayClient, store *Store, fixtures []Fixture) []TrialRecord {
	var mu sync.Mutex
	var records []TrialRecord
	var wg sync.WaitGroup
	for _, f := range fixtures {
		wg.Add(1)
		go func(f Fixture) {
			defer wg.Done()
			for trialNum := 2; trialNum <= 3; trialNum++ {
				r := runTrial(ctx, cfg, gw, store, f, store.experimentIDHint, trialNum)
				mu.Lock()
				records = append(records, r)
				mu.Unlock()
			}
		}(f)
	}
	wg.Wait()
	return records
}

// stage1Gate re-derives Stage 1's pass/fail gate from whatever is already
// in the store, for a `stage2` invocation run as a separate process from
// `stage1` (the normal case -- Stage 1 and Stage 2 are deliberately
// separate operator-triggered steps, not one long-running process, so a
// human can inspect Stage 1's results before paying for Stage 2).
func stage1Gate(store *Store, fixtures []Fixture) (gatePassed bool, missing []string) {
	trial1ByFault := map[string]TrialRecord{}
	for _, r := range store.All() {
		if r.TrialNum == 1 {
			trial1ByFault[r.FaultID] = r
		}
	}

	gatePassed = true
	for _, f := range fixtures {
		r, ok := trial1ByFault[f.FaultID]
		if !ok {
			gatePassed = false
			missing = append(missing, f.FaultID+" (no trial 1 record)")
			continue
		}
		if r.HarnessError != "" {
			gatePassed = false
			missing = append(missing, fmt.Sprintf("%s (trial 1 harness_error: %s)", f.FaultID, r.HarnessError))
		}
	}
	return gatePassed, missing
}

func newExperimentID() string {
	return "bench-" + time.Now().UTC().Format("20060102-150405")
}
