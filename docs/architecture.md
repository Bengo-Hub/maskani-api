# maskani-api architecture

## Components

| Component | Status | Role |
|---|---|---|
| maskani-ui | New, R1 | Next.js PWA: management console, owner and occupant portal, gate tablet, vendor portal |
| maskani-api | New, R1 | This service: the multi-tenant property domain |
| maskani-commerce | New, R3 (showcase in R1) | Public server-rendered marketplace reading a published projection |
| auth-api | Existing, extended | Tenants, branches (properties), SSO, roles; extended with customer phone OTP sign-in |
| treasury-api | Existing, extended | Invoices, payment intents, M-Pesa, PayHero, Paystack, C2B, AR, vendor bills, payouts, GL; extended with C2B account routes and the `account_payment` allocator |
| erp-api | Existing | Staff, attendance, payroll, casual payments, assets |
| notifications-api | Existing | Email and WhatsApp (the active channels) with per-tenant sender; maskani consumer added 2026-10-08 |
| subscriptions-api | Existing | Plans, feature entitlements, limits |
| marketflow-api, maps | Existing, R3 | Leads from enquiries; geocoding and map tiles |

Platform: PostgreSQL behind PgBouncer (transaction pooling), Redis, NATS JetStream, local media
volume (object storage later), Cloudflare, k3s, ArgoCD.

## Code layout

```
cmd/
  api/             HTTP server
  migrate/         Atlas versioned migrations, advisory lock 727271007, direct DSN
  seed/            Platform default catalogues, roles and permissions
  seed-tenant/     Demo tenant dataset (Shaba Village)
  hashmigrations/  Rewrites atlas.sum after a hand-written migration
internal/
  app/             Wiring
  config/          envconfig (no prefix)
  ent/schema/      Ent schemas; ent/migrate/migrations holds versioned SQL
  events/          Outbox publish helper (event row in the same tx as the domain write)
  http/handlers/   HTTP handlers, one file per area
  http/middleware/ Permission, property scope, module gate
  http/router/     chi router and route table
  modules/         Domain services: register, parties, billing, utilities, sales, works,
                   vendors, gate, notices, documents, reports, tenant, identity, rbac,
                   auditlog, sequence; integration clients treasury, authapi, erp,
                   notifications
  platform/        database, events (NATS), subscriptions (gate), tenantguard, media
  shared/          logger, money helpers
```

## Tenant hierarchy

```
Tenant (auth-api organisation)
  Portfolio (maskani: one for an estate operator, one per landlord client in R2)
    Property (maskani; also an auth-api branch/outlet, so branch-scoped roles apply)
      Block
        Unit (house, apartment, office, shop, bay, plot)
          Unit parties (owner, joint owner, buyer, occupant, household, domestic staff)
          Unit accounts (one per fund: estate "B07", sales "S-B07")
```

A property is registered as an auth-api outlet. maskani keeps a local `outlets` projection fed by
`auth.outlet.*` events, and `properties.outlet_id` points at it. Staff assignment to a property is
an outlet assignment, so the existing branch scoping in tokens and in `/auth/me` covers it.

## Request pipeline

1. `ratelimit.TrustedRealIP`, request ID, logging, recover, timeout (streaming bypass), CORS,
   per-IP limiter.
2. `RequireAuth` (JWKS, or `X-API-Key` for S2S), subscription mutation gate.
3. JIT user provisioning (`identity.EnsureUserFromToken`).
4. `httpware.TenantV2`: tenant from the signed token, URL slug only for platform owners.
5. Tenant sync: slug to UUID, local `tenants` projection (Redis cached).
6. Tenant guard context: `tenantguard.With(ctx, tenantID)`.
7. Property scope: property-level roles see only their assigned properties.
8. Route gates: `subscriptions.RequireFeature`, `modules.Require(module)`,
   `RequireServicePermission`.

## Tenant isolation

| Control | Implementation |
|---|---|
| Rows | `tenant_id` on every tenant-owned table |
| Query guard | Ent interceptor adds `tenant_id = ctx tenant` to every query on a tenant-owned type; a query without tenant context fails closed |
| Write guard | Ent hook stamps `tenant_id` on create and rejects a mismatch on update or delete |
| System jobs | Opt out explicitly with `tenantguard.System(ctx)`; each job then filters per tenant itself |
| Files | Media keys prefixed `tenants/{tenant_id}/...`; served only through signed, short-lived URLs |
| Events | `tenant_id` on the envelope; consumers resolve tenant from the envelope first |
| Cache | Keys prefixed `maskani:{tenant_id}:` |
| Tests | Cross-tenant suite: two tenants with overlapping unit codes, every route checked |

**Known gap:** the SRDD promises PostgreSQL row-level security. The fleet does not use RLS today,
and with PgBouncer in transaction mode RLS needs `SET LOCAL app.tenant_id` inside every
transaction. The Ent guard is the R1 control; RLS is scheduled for sprint 6 hardening
(see [backlog.md](backlog.md)).

## Module gating

A feature is available only where three conditions meet:

1. The tenant's plan includes the feature (`sub_features` claim, subscriptions-api).
2. The tenant has switched the module on (`tenant_modules`).
3. The property's use case includes the module (`properties.use_case` preset, adjustable).

The resolved set is cached with the tenant profile in Redis and invalidated on change. It is applied
in routes (403 `module_not_enabled`), jobs and consumers (skip), navigation (`/auth/me` returns
`modules`), forms, reports and message templates. Turning a module off hides it and stops its jobs;
data stays readable and exportable.

| Module code | Depends on | Release |
|---|---|---|
| `properties` | always on | 1 |
| `billing` | properties | 1 |
| `utilities` | billing | 1 |
| `sales` | billing | 1 |
| `estate` | billing | 1 |
| `maintenance` | properties | 1 |
| `providers` | maintenance | 1 |
| `gate` | properties | 1 |
| `staff` | ERP plan | 1 |
| `communication` | properties | 1 |
| `leasing` | billing | 2 |
| `commercial` | leasing | 2 |
| `portfolios` | leasing | 2 |
| `marketplace` | properties, leasing or sales | 3 |
| `amenities` | billing | 4 |

Use case presets: `estate_developer` (Shaba), `developer_sales`, `owners_association`,
`residential_manager`, `commercial_manager`, `self_managing_landlord`, `agent_listings`.

## Money boundaries

maskani-api never collects, holds or pays money.

- **Invoices:** billing runs and instalments create and send treasury invoices
  (`reference_type` `maskani_bill` or `maskani_instalment`, `metadata.account_ref` linking the unit
  account, `settlement_account_id` = the fund's bank account, `metadata.fund`).
- **Portal payments:** an intent with `reference_type: "account_payment"` (pay balance) or the
  existing `"invoice"` public-token path (one invoice). The tenant's gateway resolver picks Daraja,
  PayHero or Paystack.
- **Paybill:** treasury resolves `BillRefNumber` through C2B account routes that maskani registers
  per unit account, settles an `account_payment` intent, and allocates oldest due first.
- **Result:** every channel ends in `treasury.payment.succeeded`; maskani refreshes its balance
  view and purchase progress from that event.

Funds never mix: each fund maps to its own paybill, bank account, cost centre and ledger accounts,
and a payment to one account reference can only settle that account's invoices.

## Background jobs

All jobs run once per fleet per period through `cache.ClaimPeriod` / `RunOnce`. Session advisory
locks are never used through PgBouncer.

| Job | Period | Action |
|---|---|---|
| Reservation expiry | 15 min | Release lapsed reservations, unit back to available |
| Instalment invoicing | daily 06:00 EAT | Raise treasury invoices for instalments due within the lead window |
| Reminders | daily 08:00 EAT | Bill and instalment reminders per schedule, quiet hours respected |
| SLA timers | 5 min | Flag work orders past response or resolution targets |
| Vendor document expiry | daily | Alert manager and vendor at 30, 14 and 7 days |
| Gate device heartbeat | 5 min | Alert when a tablet is offline over 15 minutes |
| Gate event retention | daily | Purge gate events older than 90 days in batches |
| Daily stats rebuild | nightly 02:00 EAT | Rebuild `daily_stats` for the previous day per property |

Each job skips tenants and properties whose module is off.

## Events

Outbox rows are written in the same transaction as the domain change, drained by the shared outbox
poller to JetStream stream `maskani`. Consumers are idempotent on event ID (`consumed_events`). See
[events.md](events.md).

## Observability

Structured zap logs with `tenant_id`, request ID and route; Prometheus `/metrics`; health at
`/healthz` and `/readyz`. Alerts (fleet-health-watcher): failed billing runs, unmatched payment
build-up, offline gate devices, error rate, latency.
