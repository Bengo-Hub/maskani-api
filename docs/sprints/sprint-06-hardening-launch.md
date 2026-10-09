# Sprint 6: hardening, parallel run, launch

SRDD weeks 13 and 14 (hardening week, launch week, milestone 4).

## Scope

| Item | Detail |
|---|---|
| Row-level security | Policies `tenant_id = current_setting('app.tenant_id')::uuid` on every tenant-owned table; Ent tx wrapper running `SET LOCAL app.tenant_id` per request transaction (PgBouncer transaction mode safe); system role for migrations and jobs; cross-tenant suite re-run with the guard disabled to prove RLS alone holds |
| Partitions | Monthly partitions for `gate_events` and `patrol_scans` via hand-written migration and a rotation job; purge by dropping partitions |
| Security review | OWASP ASVS L2 checklist; access between units; payment adjustments; OTP rate limits; file type checks on uploads |
| Load test | Billing run for 1,000 units under 2 minutes; 50 requests per second within NFR-04 |
| Restore drill | Restore the latest offsite backup into an isolated namespace; compare record counts and ledger balances |
| Parallel billing run | One month billed both ways; unit by unit comparison signed off with Shaba finance |
| Live payment tests | KES 1 on both paybills, portal prompt, a vendor payout; each reconciled (user confirms each call) |
| Offline gate test | Network removed for 2 hours; passes verified, entries queued, synchronised without loss |
| Configuration tests | Each preset and module switched on and off; no screen, route, job or message of a disabled module reachable |
| UAT and launch | Shaba staff and pilot owners; owner invitations; training; handover |

## Progress

As of 2026-10-08.

- [x] Ent tenant guard, fails closed, integration tested (ahead of this sprint)
- [x] Gate event BRIN and composite indexes (ahead of this sprint)
- [ ] Row-level security (gap tracked in `docs/backlog.md`)
- [ ] Partitions for `gate_events` and `patrol_scans`
- [ ] Security review, load test, restore drill
- [ ] Parallel billing run, live payment tests, offline gate test
- [ ] Configuration tests, UAT and launch

### Gaps found by the 2026-10-09 audit
Plan: `.claude/plans/maskani-r1-completion-r2-rentals-2026-10-09.md`, wave 3 unless noted.

- [ ] The live-diff migrate drops undeclared indexes and partitions: partitions need an Atlas exclusion and a rotation job before they ship
- [ ] Cross-tenant suite extended to every route, export, file link, event and report added in waves 1 and 2
- [x] `tenantguard.With` now drops an inherited system flag, so a job narrowing to one tenant is guarded again (test, `35df00b`)
- [ ] Load script in the repo: 1,000-unit billing run and 50 requests per second
- [ ] Ops items, each confirmed with the user first: Meta sync of `maskani_*` templates, prune-users dry runs then `--apply`, fleet-health-watcher URLs, KES 1 tests, restore drill

## Rules to apply in this sprint

Standing backend rules in [README.md](README.md), plus:

| Scenario | Rule | Memory file |
|---|---|---|
| Any production data fix found in UAT | Bounded predicate, dry run, backup table, verify, confirm | `feedback_production_data_repair_safety.md` |
| RLS through PgBouncer | Session-level settings and advisory locks do not survive transaction pooling; only `SET LOCAL` inside a transaction | `multi-pod-scaling-ratelimit-realtime-media-2026-09-29.md` |
| Rollouts during launch | Surge-first, at least 2 replicas, PDB | `ha-min-2-pods-and-pdb.md` |
| Alerts | Wire maskani hosts into fleet-health-watcher with the retry tolerance | `fleet-health-alerts-single-node-mitigation-2026-09-04.md` |
