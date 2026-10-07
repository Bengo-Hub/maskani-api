# Data performance and growth

Design targets (SRDD NFR-04 to NFR-06): 500 tenants, 200,000 units, 2 million gate events and
300,000 payments a month, scaling tenfold without redesign. Reads p95 under 300 ms, writes under
600 ms, gate verify under 1 second online or offline, a billing run of 1,000 units under 2 minutes.

## Rules every query follows

1. **Tenant first.** Every index on a tenant-owned table starts with `tenant_id`, then the column the
   query filters on (usually `property_id`), then the sort column. The Ent tenant guard guarantees
   the `tenant_id` predicate is present, so these indexes are always usable.
2. **Keyset pagination** on every list (`pagination.ParseCursorParams`, cursor = `(created_at, id)`
   or the list's own sort key). No `OFFSET` scans on large tables.
3. **Aggregates in SQL.** Sums, counts and ageing buckets run as `SUM`/`COUNT ... FILTER` in one
   grouped query per report, never by loading rows into Go. No unbounded `.All()` over a table that
   grows with tenants or time.
4. **Bounded batches.** Billing runs, reminders, imports and purges work in batches of 500 with a
   progress row, so memory and lock time stay flat at any estate size.
5. **Reports off the hot path.** Report endpoints read through `POSTGRES_READONLY_URL` (the
   `maskani_ro` PgBouncer alias to the replica when present) and cache results for 60 seconds per
   tenant and filter; events invalidate the cache.
6. **Daily aggregates.** Dashboards read `daily_stats` (one row per property and day), updated
   incrementally by event consumers and rebuilt nightly for the previous day, so dashboards cost the
   same at 40 units or 40,000.
7. **Treasury stays authoritative.** Balances shown in maskani are a display cache refreshed from
   treasury events; reconciliation never sums local copies of money.

## Uniqueness that makes retries safe

| Table | Unique key | Protects against |
|---|---|---|
| `billing_runs` | (tenant, property, fund, period, run_kind) while not cancelled | Two runs for one month |
| `billing_run_lines` | (run, unit_account) | Two invoices for one unit in one run |
| `meter_readings` | (tenant, meter, period, source) | Double readings |
| `unit_accounts` | (tenant, account_ref) and (tenant, unit, fund) | Ambiguous paybill references |
| `instalments` | (schedule, seq) | Duplicate schedule lines |
| `gate_events`, `patrol_scans` | (tenant, device, client_event_id) | Duplicates from offline resync |
| `notice_deliveries` | (notice, party, channel) | Double sends |
| `consumed_events` | (event_id, consumer) | Redelivered events |
| `reservations` | one active per unit (partial unique) | Double booking a unit |

## Fast-growing tables

| Table | Growth | Measures |
|---|---|---|
| `gate_events` | Highest (every entry and exit) | Composite (tenant, property, occurred_at), 90 day retention purge in batches. BRIN and monthly partitions come together in sprint 6: the migrate binary runs a live diff with `WithDropIndex`, so any index not declared in the Ent schema (BRIN included) would be dropped on the next deploy; it needs Ent-side declaration or an excluded migration path first |
| `patrol_scans` | High | Same as gate events |
| `meter_readings` | One per meter per month | Composite (tenant, round, status) and (tenant, meter, period) |
| `audit_logs` | Every financial or config change | (tenant, entity_type, entity_id) and (tenant, created_at); retention per schedule |
| `notice_deliveries` | Audience size per notice | Unique key plus (tenant, notice) |
| `outbox_events` | Every event | Drained and pruned by the shared poller |

## Search

- Unit and party lookups use exact code match first (`(tenant_id, property_id, code)`), then a
  `lower(display_name)` prefix index. A `pg_trgm` GIN index on `parties.display_name` is added with a
  hand-written migration when tenants exceed a few thousand parties (the extension is installed by
  `create-service-database.sh`).
- JSON filters (`metadata`, `custom_fields`) use a partial or GIN index plus a hand-written
  migration and atlas re-hash, the established fleet pattern, only once a filter is real.

## Connection and pool settings

| Setting | Value | Reason |
|---|---|---|
| `POSTGRES_URL` | PgBouncer, transaction pooling | Many pods, few server connections |
| `POSTGRES_MAX_OPEN_CONNS` | 8 | Two replicas within the `maskani` pool size |
| `POSTGRES_STATEMENT_TIMEOUT` | 30s | No runaway query holds a connection |
| Migrations | `POSTGRES_MIGRATE_URL` direct, one pinned connection, advisory lock 727271007 | Session locks do not survive PgBouncer |

## Caching

| Key | TTL | Invalidated by |
|---|---|---|
| `maskani:{tenant}:modules` | 10 min | settings change, subscription update event |
| `maskani:{tenant}:report:{name}:{hash}` | 60 s | billing, payment, work order events |
| `maskani:{tenant}:gate:passes:{property}` | 5 min | pass create or cancel |
| Tenant projection | shared syncer cache | auth tenant events |
