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

**Only the observability/alerting infrastructure exists today.** The Go
Ingestion Gateway, `hermes-triage.yml`, and the Hermes wrapper are specified
in detail but not yet implemented — see [`docs/PRD.md`](docs/PRD.md) and
[`docs/TRD.md`](docs/TRD.md), which are living specs and the source of truth
whenever this README, `diagram.md`, or `easy-workflow.md` disagree with them.
`hermes-triage.yml`, the Hermes wrapper, and a fixture app stand-in for "the
production repo" live in a companion repo,
[`revi-hermes-target`](https://github.com/bookang869/revi-hermes-target).

## How it fits together

```
App --OTLP/gRPC--> OTel Collector --scrape--> VictoriaMetrics --evaluate--> vmalert
  --> Alertmanager --webhook--> Go Ingestion Gateway
```

The Gateway (single Go binary, not yet built) is one process with three jobs:

1. `POST /v1/alerts` — validates Alertmanager's native alert JSON, checks a
   NATS JetStream KV lock (dedupes flapping alerts), enriches with
   VictoriaLogs context, publishes to NATS.
2. An in-process NATS consumer that dequeues and fires GitHub's
   `repository_dispatch`, stamping the configured `REVI_MODE` into
   `client_payload`.
3. A cron goroutine that flushes the day's buffered digest to Slack
   `#triage-morning-review` at `REVI_DIGEST_TIME` (default 08:00).

`repository_dispatch` triggers `hermes-triage.yml` on a fresh, ephemeral
GitHub Actions runner — that ephemeral clone *is* the isolation boundary,
there's no separate sandbox repo. The runner invokes Hermes, verifies the
patch (compile, smoke test, existing suite), and either opens a PR or merges
to `main`, per the mode above. Every run reports back to
`POST /v1/digest/entry` regardless of outcome.

For the full request/response schemas, token scoping, and NATS key design,
read TRD §2 and §4 before touching anything alert- or gateway-related — the
locking and token rules have constraints that aren't obvious from a
"reasonable-looking" implementation.

## Local stack

```
docker compose up -d
```

Brings up `otel-collector`, `victoria-metrics`, `nats` (JetStream), `vmalert`,
and `alertmanager`. The `gateway` service isn't in `docker-compose.yml` yet —
its hostname is pre-wired in `alertmanager.yml` for when it lands.

Before starting, create the gitignored webhook secret Alertmanager expects:

```
mkdir -p secrets
echo "<your-secret>" > secrets/revi_webhook_secret
```

No build/lint/test commands exist yet since there's no application code —
once the Go Gateway is scaffolded, its `go build` / `go test ./...` commands
belong here.
