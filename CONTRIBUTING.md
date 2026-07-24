# Contributing

## Commit messages

This repo follows [Conventional Commits 1.0.0](https://www.conventionalcommits.org/en/v1.0.0/):

```
<type>[optional scope]: <description>

[optional body]

[optional footer(s)]
```

- **Types**: `feat`, `fix`, `docs`, `chore`, `ci`, `refactor`, `perf`, `test`, `build`, `style`.
- **Scopes** (match re:vi's components; add new ones as they land): `gateway`, `hermes`, `otel`, `vm` (VictoriaMetrics/vmalert), `alertmanager`, `nats`, `docker`, `docs`.
- **Breaking change**: `!` after the type/scope (`feat(gateway)!: ...`) and/or a `BREAKING CHANGE:` footer — required for anything that changes the `/v1/alerts` or `/v1/digest/entry` wire schema, token scoping, or `client_payload` shape.
- Description: imperative mood, lowercase, no trailing period (`fix(alertmanager): correct webhook path`, not `Fixed the webhook path.`).
- Body explains *why*, not what — the diff already shows what.

Examples:

```
feat(gateway): add NATS JetStream KV lock check to /v1/alerts
fix(vm): correct scrape interval in vm-scrape.yml
feat(gateway)!: rename client_payload.mode to client_payload.re_vi_mode

BREAKING CHANGE: hermes-triage.yml must read the new field name.
```
