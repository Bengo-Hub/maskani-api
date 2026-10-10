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

A property is registered as an auth-api outlet. maskani keeps a local `outlets` projection and
`properties.outlet_id` points at it. The projection is pulled from auth-api's public outlet list
(`tenant.SyncOutlets`) when a pod first sees a tenant and at the start of `seed-tenant`. Staff
assignment to a property is an outlet assignment, so the existing branch scoping in tokens and in
`/auth/me` covers it.

### Only property outlets and property people are synced

One auth-api tenant can run several products: codevertex-demo hosts a hotel, shops, a clinic, a
weighbridge and the Shaba Village estate under one tenant, and role names such as manager,
cashier and member are shared by all of them. Two rules keep other products out of Maskani:

- **Outlets.** `SyncOutlets` keeps an outlet only when its use case is a property one
  (`rbac.IsPropertyUseCase`: property, estate, real_estate, maskani), when auth-api lists
  `maskani-api` in its `applicable_services`, or when it is the tenant HQ. Archived outlets are skipped.
- **Users.** `rbac.EnsureUser` creates a local user only for platform owners and superusers, the
  tenant's admin-level roles, property-specific roles (`property_manager`, `estate_accountant`,
  `property_sales`, `caretaker`, `estate_security`, `maskani_*`), tokens minted for a property
  outlet, people linked to an estate party, and staff a Maskani admin has already given a role.
  Generic names (manager, supervisor, accountant, cashier, sales, security, member, staff) map
  only when the tenant's own use case is property. Everyone else resolves to no roles and no
  permissions, and roles the earlier unconditional mapping granted them automatically are
  removed at their next request. The Users screen lists staff only once they hold a role.

## Request pipeline

1. `ratelimit.TrustedRealIP`, request ID, logging, recover, timeout (streaming bypass), CORS,
   per-IP limiter.
2. `RequireAuth` (JWKS, or `X-API-Key` for S2S), subscription mutation gate.
3. JIT user provisioning (`rbac.EnsureUser`, limited to property people as described above).
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
3. The property's use case includes the module (`properties.use_case` preset, adjusted by
   `properties.module_overrides`), the way POS scopes modules by outlet. The tenant's set is the
   ceiling; a property only narrows it (or adds back a tenant module its preset leaves out).

The tenant set is cached per pod for 60 seconds and the property rule in a bounded LRU with the
same expiry, dropped on the pod that saves a change. It is applied in routes (403
`module_not_enabled`, or `module_not_enabled_for_property` when the request names a property in
its `property_id` query or JSON body; reads answer with `X-Module-Read-Only: plan`, `disabled` or
`property`), jobs and consumers (skip), navigation (`/auth/me` returns `modules` and
`property_modules`), forms, reports and message templates. Turning a module off hides it and stops
its jobs; data stays readable and exportable.

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

## Approvals

Every workflow that needs sign-off runs on one engine, `internal/modules/approvals`, with the same
shape as treasury-api and inventory-api (rules per module and amount band with ordered role steps,
one request per object, steps decided in order). Workflows register default steps and a decision
hook (`SetDefault`, `OnDecision`); deciding from the central inbox (`/approvals`) or from the
workflow's own screen runs the same hook. See "Central approvals" in api-spec.md.

## Background jobs

All jobs run once per fleet per period through `cache.ClaimPeriod` / `RunOnce`. Session advisory
locks are never used through PgBouncer.

Defined in `internal/jobs/jobs.go`; each claimed job runs in its own goroutine with a timeout of its
period and panic recovery.

| Job | Period | Runs for | Action |
|---|---|---|---|
| `c2b-routes` | 5 min | all tenants | Register paybill routes for new unit accounts with treasury |
| `billing-resume` | 2 min | all tenants | Carry on billing runs a pod stopped mid-issue |
| `imports-housekeeping` | 15 min | all tenants | Expire unchecked imports after 7 days, resume stuck commits |
| `reservation-expiry` | 15 min | all tenants | Release lapsed reservations, unit back to available |
| `instalment-invoicing` | hourly | sales on | Raise treasury invoices for instalments due within 7 days |
| `sla-breaches` | 5 min | maintenance on | Flag work orders past their resolution target and publish the breach |
| `vendor-doc-expiry` | daily | providers or maintenance on | Alert at 30, 14 and 7 days and on expiry |
| `gate-offline` | 5 min | gate on | Alert once when a tablet has not been seen for 15 minutes |
| `gate-retention` | daily | all tenants | Purge gate events past retention in batches |
| `scheduled-notices` | 5 min | communication on (new sends) | Send notices whose time has come; direct sends a pod left mid-way resume for every tenant |
| `daily-stats-rebuild` | daily | all tenants | Rebuild yesterday's `daily_stats` from source tables |
| `outbox-prune-passes`, `outbox-prune` | 15 min, daily | all tenants | Drop published visitor pass events after 15 minutes, other published rows after 7 days, failed after 30 |

A switched-off module is read only (FR-09), so jobs that alert, send or invoice for it skip that
tenant (`settings.TenantsWithModule`, filtered inside each query so a skipped tenant's rows never
crowd out others under the batch limit). Housekeeping that keeps data true, finishes work already
under way, or enforces retention runs for every tenant. Plan coverage is not checked by jobs; it
lives in each user's token. Bill and instalment reminders arrive with the arrears ladder (wave 2.2).

## Events

Outbox rows are written in the same transaction as the domain change, drained by the shared outbox
poller to JetStream stream `maskani`. Consumers are idempotent on event ID (`consumed_events`). See
[events.md](events.md).

## Observability

Structured zap logs with `tenant_id`, request ID and route; Prometheus `/metrics`; health at
`/healthz` and `/readyz`. Alerts (fleet-health-watcher): failed billing runs, unmatched payment
build-up, offline gate devices, error rate, latency.
