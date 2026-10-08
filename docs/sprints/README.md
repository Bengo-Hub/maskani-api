# maskani-api sprints

SRDD schedule: one-week sprint 0, two-week sprints, a hardening week and a launch week. The Shaba
Village demo (Sunday 11 October 2026) pulls the core of sprints 1 to 4 forward; each sprint file
marks what is in the **demo slice** and what remains.

| Sprint | File | Status |
|---|---|---|
| S0 | [sprint-00-bootstrap.md](sprint-00-bootstrap.md) | In progress |
| S1 | [sprint-01-register-parties-portal.md](sprint-01-register-parties-portal.md) | Demo slice in progress |
| S2 | [sprint-02-billing-utilities-collections.md](sprint-02-billing-utilities-collections.md) | Demo slice in progress |
| S3 | [sprint-03-sales-instalments.md](sprint-03-sales-instalments.md) | Demo slice in progress |
| S4 | [sprint-04-providers-works-gate.md](sprint-04-providers-works-gate.md) | Demo slice in progress |
| S5 | [sprint-05-reports-erp-documents.md](sprint-05-reports-erp-documents.md) | Planned |
| S6 | [sprint-06-hardening-launch.md](sprint-06-hardening-launch.md) | Planned |
| S7 | [sprint-07-r2-rentals.md](sprint-07-r2-rentals.md) | Planned (R2) |
| S8 | [sprint-08-r3-marketplace.md](sprint-08-r3-marketplace.md) | Planned (R3) |
| S9 | [sprint-09-r4-extensions.md](sprint-09-r4-extensions.md) | Planned (R4, by demand) |

UI work for each sprint is in `maskani-ui/docs/sprints/`; marketplace work in
`maskani-commerce/docs/sprints/`.

## Definition of done (every sprint)

- Code reviewed, `go build ./...`, `go vet ./...`, `go test ./...` green (one build per command).
- Migrations generated through the local PG17 `ent_dev` workflow; `atlas.sum` consistent.
- Cross-tenant tests extended for every new route.
- Swagger annotations on new handlers; docs in this folder updated with what shipped.
- Deployed through CI only, ArgoCD synced, pods healthy, then the relevant memory file updated.

## Standing backend rules (from `.claude/memory`)

Every sprint applies these; each sprint file adds the scenario-specific rules.

| Rule | Memory file |
|---|---|
| Ent and Atlas: `go generate`, reset `ent_dev`, `go run -mod=mod internal/ent/migrate/main.go <name>`; never edit generated migrations by hand except hand-written ones re-hashed with `cmd/hashmigrations`; watch CRLF in `atlas.sum` on Windows | `feedback_ent_atlas_migrations.md`, `feedback_treasury_migrations_toolchain.md` |
| Migrations run only in the migrate binary over `POSTGRES_MIGRATE_URL`, one pinned connection, advisory lock 727271007; never in the app process | `multi-pod-scaling-ratelimit-realtime-media-2026-09-29.md` |
| Scheduled jobs through `cache.ClaimPeriod` / `RunOnce`; rate limits through shared-ratelimit with `TrustedRealIP`; realtime through `events.Broadcaster` / `FanoutHub`; media through `httpware.StaticMedia` with a signer | `multi-pod-scaling-ratelimit-realtime-media-2026-09-29.md` |
| Events: outbox in the same tx, `SubscribeQueueWithRebind`, idempotency store, tenant from the envelope | `project_events_uniformity.md`, `nats-jetstream-topology.md` |
| S2S: one `INTERNAL_SERVICE_KEY` as `X-API-Key` plus `X-Tenant-ID`; in-cluster DNS, never public hosts through Cloudflare | `feedback_s2s_service_key.md`, `s2s-cloudflare-loopback-fleetwide-fix-2026-09-10.md` |
| Data ownership: store IDs of other services' records, never copies; roles and permissions are platform-wide | `feedback_service_data_ownership.md`, `feedback_shared_core_reference_data.md` |
| Tenant branding belongs to auth-api; services keep id, slug, name, status, use case only | `project_tenant_architecture.md` |
| Performance is never deferred: SQL aggregates, keyset pagination, no unbounded fleet-wide `.All()` | `feedback_never_defer_performance_resilience.md`, `boi-treasury-pos-recurring-discrepancy-root-audit-2026-09-11.md` |
| Money: numeric columns, one settle path in treasury, payments applied once under a row lock, never a second ledger | `treasury-payment-security-audit-2026-10-02.md`, `payhero-personal-offbooks-and-platform-billed-2026-10-05.md` |
| Customer key resolution sends both phone and contact ID to treasury, never one | `boi-treasury-duplicate-receipts-incident-2026-09-14.md` |
| Production data repair: bounded predicate, dry run, `*_bk_*` backup, verify, confirm with the user | `feedback_production_data_repair_safety.md` |
| Confirm with the user before any production-spending or state-changing live call (KES 1 STK, SMS blasts) | `feedback_confirm_sensitive_commands.md` (global memory) |
| HA: at least 2 replicas and a PDB, surge-first rollouts, lean CPU requests | `ha-min-2-pods-and-pdb.md` |
| Never commit secrets; placeholders only; `.env` ignored | `feedback_no_secrets_in_code.md`, global CLAUDE.md |
| Edit files with Edit/Write, never patch scripts; stage explicit paths; never push a red build | `feedback_edit_files_directly.md`, `feedback_stage_explicit_paths.md`, `feedback_no_errors_upstream.md` |
| One `go build` per shell command (machine freezes otherwise); at most 2 background agents | `feedback_build_one_service_at_a_time.md` (global memory), `feedback_max_background_agents.md` |
| Docs and comments: humanised, no em-dash connectors, no section sign, no revision diary | `feedback_humanize_docs_and_comments.md` |
| Swagger at `/v1/docs/`, base URL redirects there | `feedback_workflow_rules.md` |
