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

## Rules to apply in this sprint

Standing backend rules in [README.md](README.md), plus:

| Scenario | Rule | Memory file |
|---|---|---|
| Any production data fix found in UAT | Bounded predicate, dry run, backup table, verify, confirm | `feedback_production_data_repair_safety.md` |
| RLS through PgBouncer | Session-level settings and advisory locks do not survive transaction pooling; only `SET LOCAL` inside a transaction | `multi-pod-scaling-ratelimit-realtime-media-2026-09-29.md` |
| Rollouts during launch | Surge-first, at least 2 replicas, PDB | `ha-min-2-pods-and-pdb.md` |
| Alerts | Wire maskani hosts into fleet-health-watcher with the retry tolerance | `fleet-health-alerts-single-node-mitigation-2026-09-04.md` |
