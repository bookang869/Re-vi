# Re:vi

Re:vi is a self-healing telemetry pipeline for teams too small to staff
round-the-clock on-call. Production alerts get triaged and patched overnight
by an AI agent ([Hermes](https://github.com/NousResearch/Hermes), agent-agnostic
via `REVI_AGENT_COMMAND`), and a human is only paged when a fix is ready to
review or something needs attention.

It runs in one of two modes, chosen per deployment based on how much you
trust your test suite:

- **`PR_REVIEW`** — Hermes diagnoses the crash, writes a fix and a sibling
  test, opens a PR on `hermes/hotfix-[alert-id]`, and pages the on-call
  engineer with a link. Nothing touches `main` without a human.
- **`AUTONOMOUS`** — Hermes does the same, then boots the app, runs synthetic
  smoke tests and the existing test suite, and only merges to `main` if
  everything passes. Failures abort, roll back, and page immediately.
  Successful runs are batched into a single digest posted to Slack each
  morning instead of paging in real time.

## Status

**Complete and live.** The observability/alerting infrastructure, the Go
Ingestion Gateway, `hermes-triage.yml`, and the Hermes wrapper script all
run end-to-end against a standing Oracle Cloud VPS, in both `PR_REVIEW` and
`AUTONOMOUS` modes, against the companion repo
[`revi-hermes-target`](https://github.com/bookang869/revi-hermes-target)
(which holds `hermes-triage.yml`, the Hermes wrapper, and fixture apps
standing in for "the production repo"). The repair loop's gate covers Go,
Rust, Node/TS, and Python — each with a build check, a lint pass
(`go vet`/`cargo clippy`/`eslint`/`ruff`), sibling-test-file enforcement,
and a test-quality check (the sibling test must fail against the pre-fix
code and only pass with the fix applied, catching a vacuous test that
would otherwise pass either way). A controlled 96-trial fault-injection
benchmark ([`docs/observability-part-b.md`](docs/observability-part-b.md))
independently verified a 99% correct-repair rate across 32 unique faults
spanning all four languages.

What's left — wiring in a real protected app, an onboarding wizard,
actor-based branch protection — is gated on a real team/app existing to
design around, not on more engineering time; see
[`docs/PLAN.md`](docs/PLAN.md) for the full list.

[`docs/PRD.md`](docs/PRD.md) / [`docs/TRD.md`](docs/TRD.md) are living specs
and remain the source of truth whenever this README, `diagram.md`, or
`easy-workflow.md` disagree with them.

## Pausing repair (cost control)

The observability/alerting stack runs 24/7 by design — that's the
"overnight self-healing" premise. But the part that actually spends Claude
tokens is narrower: only the moment `repository_dispatch` fires and Hermes
runs. If you want alerts to keep being watched, logged, and shown in the
Slack digest without spending tokens on repairs — you're not actively
iterating, or just want to bound spend — you can turn off *just* that step
without tearing down the stack:

```
# Disable — alerts still get ingested, enriched, and recorded, they just
# never reach Hermes
curl -X POST https://<your-gateway>/v1/admin/repair-enabled \
  -H "Authorization: Bearer $REVI_ADMIN_TOKEN" \
  -d '{"enabled": false}'

# Re-enable
curl -X POST https://<your-gateway>/v1/admin/repair-enabled \
  -H "Authorization: Bearer $REVI_ADMIN_TOKEN" \
  -d '{"enabled": true}'
```

Guarded by its own `REVI_ADMIN_TOKEN` (separate from `REVI_WEBHOOK_SECRET`
— see `.env.example`). **Default is enabled**: a fresh clone that never
touches this has repair on from the start, same behavior as before this
existed. An alert that fires while paused is still recorded (its outcome
shows `SKIPPED_OFF` via `GET /v1/runs/{alert_id}`) and still appears in the
next morning's digest — it just never reaches Hermes. It can't cancel a
repair already in progress, only stop new ones from starting. Full details
in `docs/TRD.md` §4.

## How it fits together

```mermaid
flowchart TD

    %% =========================
    %% 1. CRASH + OBSERVABILITY
    %% =========================
    subgraph THISREPO["This repo (Re:vi) — observability stack + Gateway"]
        direction TB

        subgraph VPC["Production VPC"]
            direction TB

            Crash["💥 App Crash"]
            App["Your App"]
            Otel["OTel Collector"]
            VM["VictoriaMetrics"]
            VL["VictoriaLogs"]
            vmalert["vmalert"]
            AM["Alertmanager"]

            Crash --> App
            App -->|OTLP/gRPC| Otel

            Otel -->|metrics| VM
            Otel -->|logs| VL

            VM -->|evaluate| vmalert
            vmalert --> AM
        end

        GW["Go Ingestion Gateway"]
        NATS["NATS JetStream"]

        AM -->|webhook| GW
        GW -.->|query log context| VL
        GW --> NATS
    end


    %% =========================
    %% 2. HERMES RUNNER
    %% =========================
    subgraph TARGETREPO["Target repo"]
        direction TB

        GHA["Runner<br/>hermes-triage.yml"]
        Hermes["Hermes Agent"]

        GHA --> Hermes

        PR["Open PR<br/>hermes/hotfix-*"]
        Exhaustion["No PR opened<br/>(EXHAUSTION)"]
        Merge["Smoke test + full suite<br/>→ merge to main"]

        Hermes -->|PR_REVIEW successful| PR
        Hermes -->|"PR_REVIEW<br/>3 failed attempts"| Exhaustion
        Hermes -->|AUTONOMOUS| Merge
    end

    NATS -->|repository_dispatch| GHA
    GHA -.->|"POST /v1/digest/entry<br/>every run"| GW


    %% =========================
    %% 3. OUTCOMES
    %% =========================
    subgraph OUT["Outcomes"]
        direction TB

        ESC["Escalation webhook<br/>(Grafana OnCall)<br/>pages on-call"]

        Digest["Digest → Slack<br/>#triage-morning-review<br/>(08:00)"]
    end

    PR -->|PR_READY| ESC
    Exhaustion -->|EXHAUSTION| ESC
    Merge -->|"boot failure / regression"| ESC
    GW -.-> Digest
```

The two subgraphs above are two different git repos: this repo hosts the
observability stack and Gateway; `hermes-triage.yml`, the Hermes wrapper, and
the app Hermes patches live in the companion
[`revi-hermes-target`](https://github.com/bookang869/revi-hermes-target) repo
(see Status above). The Gateway's `repository_dispatch` call reaches into
that repo's Actions; PRs and merges land on *its* `main`, not this repo's.

The Gateway (`gateway/`, a single Go binary) is one process with three jobs:

1. `POST /v1/alerts` — validates Alertmanager's native alert JSON, checks a
   NATS JetStream KV lock (dedupes flapping alerts), enriches with
   VictoriaLogs context, publishes to NATS.
2. An in-process NATS consumer that dequeues and fires GitHub's
   `repository_dispatch`, stamping the configured `REVI_MODE` into
   `client_payload`.
3. A cron goroutine that flushes the day's buffered digest to Slack
   `#triage-morning-review` at `REVI_DIGEST_TIME` (default 08:00).

`repository_dispatch` triggers `hermes-triage.yml` on a fresh, ephemeral
GitHub Actions runner in the target repo — that ephemeral clone *is* the
isolation boundary, there's no separate sandbox repo beyond it. The runner
invokes Hermes, verifies the patch (compile, smoke test, existing suite),
and either opens a PR or merges to the target repo's `main`, per the mode
above. Every run reports back to this repo's `POST /v1/digest/entry`
regardless of outcome.

For the full request/response schemas, token scoping, and NATS key design,
read TRD §2 and §4 before touching anything alert- or gateway-related — the
locking and token rules have constraints that aren't obvious from a
"reasonable-looking" implementation.

## Local stack

**Prerequisites:** Docker + Docker Compose v2, a GitHub token scoped to
`repository_dispatch` on the target repo (`GITHUB_TOKEN_DISPATCH`), and a
[Tailscale auth key](https://login.tailscale.com/admin/settings/keys)
(`TS_AUTHKEY`) if you want the Funnel tunnel up. Go is only needed if you're
building the Gateway outside Docker (see below).

```
docker compose up -d
```

Brings up `otel-collector`, `victoria-metrics`, `victoria-logs`, `nats`
(JetStream), `vmalert`, `alertmanager`, `gateway`, and `tailscale` (Funnel
tunnel so the GitHub-hosted runner can reach `/v1/digest/entry`).

Requires a gitignored `.env` for the `gateway` and `tailscale` services'
secrets — copy [`.env.example`](.env.example) to `.env` and fill it in.
`REVI_WEBHOOK_SECRET`, `GITHUB_TOKEN_DISPATCH`, `REVI_GITHUB_OWNER`,
`REVI_GITHUB_REPO`, and `TS_AUTHKEY` are required (compose fails fast if
unset); everything else has a safe default. `REVI_WEBHOOK_SECRET` must match
`secrets/revi_webhook_secret` (Alertmanager reads the file, the Gateway
reads the env var — same secret, two consumers):

```
mkdir -p secrets
openssl rand -hex 32 > secrets/revi_webhook_secret
```

**Verify it's running:**

```
docker compose ps                       # all services should be Up
curl http://localhost:8080/healthz      # Gateway -> 200 OK
curl http://localhost:8428/health       # VictoriaMetrics -> OK
```

This stack alone gets you the observability pipeline (App → Gateway) and
proves the Gateway is receiving alerts. It does **not** by itself show the
self-healing loop — that also needs the companion
[`revi-hermes-target`](https://github.com/bookang869/revi-hermes-target)
repo checked out and configured with its own tokens, since that's where
`hermes-triage.yml` and the app Hermes patches actually live (see
"How it fits together" above).

### Gateway (Go)

```
cd gateway
go build ./...              # compile
go test ./...                # run tests
go run ./cmd/server          # start the binary (needs REVI_WEBHOOK_SECRET set)
docker build -t revi-gateway .   # multi-stage build; what docker-compose builds too
```
