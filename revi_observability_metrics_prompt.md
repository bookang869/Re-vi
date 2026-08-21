# Re:vi Observability Metrics Prompt

Inspect the existing Re:vi architecture and implement an observability/benchmarking layer **on top of the current system without redesigning the remediation pipeline**.

The objective is to collect enough structured data from every remediation run to calculate defensible engineering benchmarks.

## Required Metrics

### 1. Total Remediation Runs

Track the total number of unique remediation attempts.

Each remediation run should have a unique `run_id` that can be followed from the initial alert through the final outcome.

Also track:

- total remediation runs
- total controlled benchmark runs
- total **unique injected fault scenarios**

Do not report only the number of runs. A benchmark with repeated trials of a small number of faults should distinguish between:

```text
200 total runs
40 unique fault scenarios
```

---

### 2. Remediation Success Rate

Measure:

```text
successful validated repairs / total eligible remediation runs
```

A "validated repair" should mean that the generated fix passes the existing Re:vi validation pipeline.

Report:

- total runs
- successful validated repairs
- failed repairs
- validated repair percentage

---

### 3. Independently Verified Repair Rate

For controlled benchmark runs, measure whether the proposed repair actually fixes the injected fault using an independent verifier.

This verifier may use:

- hidden tests
- fault-specific assertions
- known expected behavior
- another deterministic correctness check

Measure:

```text
independently verified repairs / total injected fault runs
```

Also report:

```text
independently verified repairs / successful validated repairs
```

This should be treated as a stronger correctness metric than simply passing the existing Re:vi validation pipeline.

---

### 4. Alert → Validated-Fix Latency

Capture timestamps necessary to calculate:

```text
alert received → repair successfully validated
```

Report:

- P50 latency
- P95 latency
- optionally mean and P99

Do not report only an average.

---

### 5. Alert → Merge Latency

For autonomous repairs that reach `main`, measure:

```text
alert received → successful merge
```

Report:

- P50
- P95

Keep this separate from validated-fix latency.

---

### 6. Autonomous Merge Rate

Measure:

```text
autonomously merged fixes / total remediation runs
```

Also report:

```text
autonomously merged fixes / successful validated repairs
```

These answer different questions, so preserve both.

---

### 7. Escalation Rate

Measure how often Re:vi cannot safely complete remediation automatically and escalates to a human.

```text
escalated runs / total remediation runs
```

Also capture the reason for escalation.

---

### 8. Incorrect / Regressive Merge Rate

This is one of the most important safety metrics.

For controlled benchmark runs where the expected correct behavior is known, measure:

```text
incorrect auto-merged fixes / total auto-merged fixes
```

Use the independent post-repair verifier to determine whether an auto-merged fix actually resolved the injected fault without violating the expected behavior.

Do not treat "existing tests passed" alone as proof that a patch is correct.

This metric should eventually allow a defensible statement such as:

```text
0 independently-invalid patches reached main across N controlled remediation runs
```

only if the recorded results actually support it.

---

### 9. Validation-Gate Rejection Rate

Track how often candidate patches are rejected before merge by Re:vi's safety gates.

Examples may include:

```text
compile/build
application startup
smoke test
regression test
independent verification
```

Measure:

```text
candidate patches rejected by validation gates / total candidate patches
```

Also report counts by gate.

This should make it possible to quantify how many invalid or regressive candidate fixes were prevented from progressing toward `main`.

---

### 10. Attempts Per Successful Repair

Track how many repair attempts were required before a successful fix.

Report:

- average attempts
- median attempts
- maximum attempts
- distribution of 1-attempt, 2-attempt, 3-attempt successes

---

### 11. Failure Stage

For failed or escalated runs, record where the process failed.

Examples may include:

```text
diagnosis
patch generation
compile/build
application startup
smoke test
regression test
independent verification
merge
```

Use stages that correspond to the existing Re:vi architecture.

Report counts and percentages by failure stage.

---

### 12. Remediation Failure vs. Infrastructure Failure

Separate failures caused by Re:vi being unable to repair the fault from failures caused by supporting infrastructure.

Examples of infrastructure failures may include:

```text
GitHub Actions failure
repository_dispatch failure
network failure
credential/authentication failure
runner startup failure
provider/API outage
```

Track each run as one of:

```text
remediation_failure
infrastructure_failure
successful_remediation
```

Do not allow infrastructure failures to silently distort the remediation success rate.

Report both:

```text
raw success rate across all runs
repair success rate excluding unrelated infrastructure failures
```

---

### 13. Runtime / Language Breakdown

For controlled benchmark runs, capture the target language/runtime.

Current examples may include:

```text
Go
Rust
Python
Node/TypeScript
```

Report for each runtime:

- number of runs
- number of unique fault scenarios
- validated repair rate
- independently verified repair rate
- P50 remediation latency
- P95 remediation latency
- escalation rate
- incorrect autonomous merge rate

---

### 14. Fault-Type Breakdown

For controlled fault-injection benchmarks, attach a known fault category to each experiment.

Examples:

```text
runtime exception
compile error
incorrect API behavior
configuration failure
dependency failure
logic regression
```

Use categories that actually correspond to the benchmark corpus.

Report:

- number of runs
- number of unique fault scenarios
- validated repair rate
- independently verified repair rate
- P50/P95 latency
- escalation rate

by fault type.

---

### 15. Cost Per Remediation

If the remediation system uses paid model/provider calls, record enough information to calculate cost.

At minimum, where available:

```text
input tokens
output tokens
model/provider
estimated API cost
```

Report:

```text
mean cost per remediation run
median cost per remediation run
mean cost per successful repair
median cost per successful repair
```

This metric is secondary to reliability and correctness, but it is useful for evaluating the operational efficiency of the system.

---

## Data Required Per Run

Capture enough structured information to calculate the metrics above.

At minimum:

```text
run_id

experiment_id
fault_id
fault_type

alert_received_at
validation_completed_at
merge_completed_at

final_outcome

number_of_repair_attempts

failure_stage
failure_classification
escalation_reason

language/runtime

autonomously_merged: true/false

independent_verification_passed: true/false/null

candidate_patch_count
validation_gate_rejections

input_tokens
output_tokens
estimated_cost
```

`failure_classification` should distinguish at minimum:

```text
remediation_failure
infrastructure_failure
```

Add other timestamps or fields only where necessary for the existing architecture.

---

## Controlled Benchmark Support

The observability layer should support repeated fault-injection experiments.

Each benchmark run should optionally record:

```text
experiment_id
fault_id
fault_type
language/runtime
expected_behavior
```

Each `fault_id` should identify a unique injected fault scenario.

Repeated runs of the same fault should share the same `fault_id` so the system can report both:

```text
total benchmark runs
unique fault scenarios
```

This should make it possible to run a fixed benchmark corpus repeatedly and compare Re:vi performance over time.

---

## Required Aggregate Output

Provide a command/script/report that produces something like:

```text
Total runs:                               N
Controlled benchmark runs:               N
Unique fault scenarios:                  N

Validated repair rate:                   X%
Independently verified repair rate:      X%
Autonomous merge rate:                   X%
Escalation rate:                         X%
Incorrect autonomous merge rate:         X%

Validation-gate rejected patches:        N
Validation-gate rejection rate:          X%

Alert → validated repair:
P50                                       X min
P95                                       X min

Alert → merge:
P50                                       X min
P95                                       X min

Attempts per successful repair:
Median                                    X
Mean                                      X

Failure classification:
Remediation failures                      N
Infrastructure failures                   N

Failures by stage:
Diagnosis                                 N
Patch generation                          N
Build                                     N
Boot                                      N
Smoke test                                N
Regression                               N
Independent verification                  N
Merge                                     N

Cost per remediation:
Median                                    $X
Mean                                      $X

Cost per successful repair:
Median                                    $X
Mean                                      $X
```

Also provide breakdowns by:

```text
language/runtime
fault category
unique fault scenario
```

---

## Baseline for Any MTTR Reduction Claim

Do **not** calculate or claim "MTTR reduction" unless a defensible baseline exists.

If comparing Re:vi against a baseline process, record the baseline using the same fault corpus and equivalent start/end definitions.

For example:

```text
baseline alert → validated fix latency
vs.
Re:vi alert → validated fix latency
```

Only then calculate:

```text
remediation_time_reduction =
(baseline_latency - revi_latency) / baseline_latency * 100
```

If no valid baseline exists, report absolute metrics such as:

```text
5.4-minute median alert-to-validated-fix latency
```

rather than:

```text
75% lower MTTR
```

---

## Most Important Resume Metrics

Prioritize capturing these values accurately:

```text
1. Number of controlled remediation runs
2. Number of unique injected fault scenarios
3. Independently verified repair rate
4. P50/P95 alert-to-validated-fix latency
5. Incorrect/regressive autonomous merge rate
6. Number/rate of candidate patches blocked by validation gates
7. Autonomous merge rate or escalation rate
```

Secondary but useful metrics:

```text
8. Attempts per successful repair
9. Repair performance by language/runtime
10. Repair performance by fault type
11. Cost per successful remediation
12. Infrastructure failure rate
```

These should eventually allow claims such as:

> Repaired X% of Y controlled trials across Z unique injected faults and four runtimes, with N-minute median alert-to-validated-fix latency.

and:

> Blocked X invalid/regressive candidate patches through build, boot, smoke, and regression gates, with 0 independently-invalid fixes reaching `main` across Y controlled trials.

Only produce those claims if the measured data actually supports them.

Do not fabricate, estimate, or hard-code benchmark results.

Work with the existing Re:vi architecture and add the minimum instrumentation necessary to capture these metrics reliably.
