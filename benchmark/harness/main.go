package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("harness: config: %v", err)
	}

	ctx := context.Background()
	switch os.Args[1] {
	case "stage1":
		cmdStage1(ctx, cfg, os.Args[2:])
	case "stage2":
		cmdStage2(ctx, cfg, os.Args[2:])
	case "trial":
		cmdTrial(ctx, cfg, os.Args[2:])
	case "report":
		cmdReport(cfg, os.Args[2:])
	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: harness <command> [flags]

commands:
  stage1  [-experiment ID]           dispatch 1 trial per fault, all faults in parallel (~$4.80 / 32 faults)
  stage2  -experiment ID             dispatch the remaining 2 trials per fault, only if stage1's gate passed
  trial   -fault ID [-trial N] [-experiment ID]   dispatch a single ad hoc trial (fault authoring/testing)
  report  -experiment ID             print a summary of every trial recorded so far

env: REVI_GATEWAY_URL, REVI_WEBHOOK_SECRET (required), REVI_HARNESS_TARGET_CLONE,
     REVI_HARNESS_FIXTURES_DIR, REVI_HARNESS_RESULTS_DIR, REVI_HARNESS_POLL_INTERVAL_SECONDS,
     REVI_HARNESS_MAX_POLLS -- see config.go for defaults.`)
}

func cmdStage1(ctx context.Context, cfg Config, args []string) {
	fs := flag.NewFlagSet("stage1", flag.ExitOnError)
	experimentID := fs.String("experiment", "", "experiment id (default: generated, e.g. bench-20260821-090000)")
	fs.Parse(args)

	fixtures, err := loadAllFixtures(cfg.FixturesDir)
	if err != nil {
		log.Fatalf("harness: %v", err)
	}
	if *experimentID == "" {
		*experimentID = newExperimentID()
	}
	log.Printf("harness: stage1 experiment=%s, %d faults, 1 trial each", *experimentID, len(fixtures))

	store, err := openStore(cfg.ResultsDir, *experimentID)
	if err != nil {
		log.Fatalf("harness: %v", err)
	}

	gw := newGatewayClient(cfg.GatewayURL, cfg.GatewaySecret)
	records, gatePassed := runStage1(ctx, cfg, gw, store, fixtures)
	printReport(records)

	fmt.Println()
	fmt.Printf("experiment_id: %s\n", *experimentID)
	if gatePassed {
		fmt.Println("STAGE 1 GATE: PASSED -- safe to run `harness stage2 -experiment " + *experimentID + "`")
	} else {
		fmt.Println("STAGE 1 GATE: FAILED -- fix the harness/infra errors above before running stage2")
		os.Exit(1)
	}
}

func cmdStage2(ctx context.Context, cfg Config, args []string) {
	fs := flag.NewFlagSet("stage2", flag.ExitOnError)
	experimentID := fs.String("experiment", "", "experiment id from a completed stage1 run (required)")
	fs.Parse(args)
	if *experimentID == "" {
		log.Fatal("harness: stage2 requires -experiment <id> from a completed stage1 run")
	}

	fixtures, err := loadAllFixtures(cfg.FixturesDir)
	if err != nil {
		log.Fatalf("harness: %v", err)
	}

	store, err := openStore(cfg.ResultsDir, *experimentID)
	if err != nil {
		log.Fatalf("harness: %v", err)
	}

	gatePassed, missing := stage1Gate(store, fixtures)
	if !gatePassed {
		fmt.Println("STAGE 1 GATE: not satisfied, refusing to start stage2:")
		for _, m := range missing {
			fmt.Println("  - " + m)
		}
		os.Exit(1)
	}

	log.Printf("harness: stage2 experiment=%s, %d faults, 2 trials each (trials 2-3)", *experimentID, len(fixtures))
	gw := newGatewayClient(cfg.GatewayURL, cfg.GatewaySecret)
	records := runStage2(ctx, cfg, gw, store, fixtures)
	printReport(records)
	fmt.Println()
	fmt.Printf("stage2 complete for experiment_id: %s -- run `harness report -experiment %s` for the full picture\n",
		*experimentID, *experimentID)
}

func cmdTrial(ctx context.Context, cfg Config, args []string) {
	fs := flag.NewFlagSet("trial", flag.ExitOnError)
	faultID := fs.String("fault", "", "fault_id to dispatch (required)")
	trialNum := fs.Int("trial", 1, "trial number to record this as")
	experimentID := fs.String("experiment", "", "experiment id (default: generated)")
	fs.Parse(args)
	if *faultID == "" {
		log.Fatal("harness: trial requires -fault <fault_id>")
	}
	if *experimentID == "" {
		*experimentID = newExperimentID()
	}

	f, err := loadFixture(cfg.FixturesDir, *faultID)
	if err != nil {
		log.Fatalf("harness: %v", err)
	}
	store, err := openStore(cfg.ResultsDir, *experimentID)
	if err != nil {
		log.Fatalf("harness: %v", err)
	}

	gw := newGatewayClient(cfg.GatewayURL, cfg.GatewaySecret)
	rec := runTrial(ctx, cfg, gw, store, f, *experimentID, *trialNum)
	printReport([]TrialRecord{rec})
	fmt.Printf("\nexperiment_id: %s\n", *experimentID)
}

func cmdReport(cfg Config, args []string) {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	experimentID := fs.String("experiment", "", "experiment id (required)")
	fs.Parse(args)
	if *experimentID == "" {
		log.Fatal("harness: report requires -experiment <id>")
	}

	store, err := openStore(cfg.ResultsDir, *experimentID)
	if err != nil {
		log.Fatalf("harness: %v", err)
	}
	printReport(store.All())
}
