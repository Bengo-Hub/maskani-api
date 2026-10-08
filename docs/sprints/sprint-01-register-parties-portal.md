# Sprint 1: register, parties, staff, import, owner portal

SRDD weeks 2 to 3. Requirements FR-01 to FR-04, FR-06 to FR-15, FR-18, FR-19.

## Scope

| Item | Detail | Demo slice |
|---|---|---|
| Tenant settings and modules | `tenant_settings`, `tenant_modules`, use case presets, module cache, `module_not_enabled` gate | Yes |
| Catalogues | Platform defaults seeded (property types, unit uses and types, amenities, work order categories, vendor categories and documents, pass types, incident types, notice categories, title stages); tenant overrides | Yes |
| Custom fields | Definitions per entity; values validated on write | Definitions only |
| Properties and blocks | CRUD; each property created as an auth-api outlet; photos via signed media | Yes |
| Units | CRUD with code, use, type, bedrooms, size, entitlement, parking, phase, sale and occupancy status | Yes |
| Parties | Persons and companies, encrypted ID and KRA PIN, phone hash, consents | Yes |
| Unit parties | Owners, joint owners, buyers, occupants, household, domestic staff with dated relationships and bill-to | Yes |
| Vehicles | Plates per unit | Yes |
| Staff per property | Property role on outlet assignment, ERP employee link | Yes |
| Import | CSV for properties, units, parties, ownerships, opening balances; dry run report; commit in batches of 500 | Units and owners |
| Owner portal identity | Party invite creates or links the auth user (`/s2s/tenants/{id}/members`), invite by WhatsApp and email; phone OTP sign-in through auth-api; terms acceptance recorded | Yes |
| `/auth/me` | Roles, permissions, assigned properties, modules, party links | Yes |
| Cross-tenant tests | Two tenants with overlapping unit codes | Yes |

## Progress

API state as of 2026-10-08. The console and portal screens are in maskani-ui sprint 01 (not started).

- [x] Tenant settings and modules: `/settings`, `/settings/modules`, module gate on every route group
- [x] Catalogues: 79 platform entries seeded, `/catalogues/{kind}` with tenant overrides
- [x] Custom fields schema
- [ ] Custom field definition and value endpoints
- [x] Properties and blocks: CRUD, auth-api outlet created on property create
- [x] Units: CRUD
- [x] Parties: CRUD with encrypted ID and KRA PIN, phone hash
- [x] Unit parties: link and end with dates
- [x] Vehicles per unit
- [x] Staff per property: list, assign, remove
- [x] CSV import with dry run (`7cf73f9`; routes on `imports.run` since `9593fb5`)
- [x] Only property outlets and property people admitted; demo estate seeded on codevertex-demo (`65683d3`, `84576a0`)
- [x] Catalogue entries can be added by whoever manages what the list describes (`9593fb5`)
- [x] Roles like hospital-api (customise default, create, edit, reset), staff invite over S2S, suspend (2026-10-08)
- [x] Owner portal API: party invite, `/me/*` routes, terms acceptance
- [x] Phone code sign-in in auth-api, code delivered on WhatsApp (auth-api dd2080c)
- [x] Portal invite message by WhatsApp and email (notifications 05efd7f)
- [x] Cross-tenant test: tenant guard integration test passes on the local database
- [x] Party detail `GET /parties/{id}` with masked ids and unit links (2026-10-08)
- [x] Property scope on staff list and assign, party links and vehicles; handler scope test (2026-10-08)
- [x] `/auth/me` returns `bypass` for platform owners, superusers and S2S (2026-10-08)
- [x] maskani-ui console and portal screens

## Acceptance

- A staff user assigned to one property cannot list or open another property's units (403 or empty).
- A portal user sees only units linked through active `unit_parties`.
- Import dry run reports row errors and totals without writing; commit is idempotent on re-run.
- Disabling a module removes its routes (403 `module_not_enabled`) and its `/auth/me` entry.

## Rules to apply in this sprint

Standing backend rules in [README.md](README.md), plus:

| Scenario | Rule | Memory file |
|---|---|---|
| Users and roles | JIT provisioning from the token; role catalogue pushed to auth-api with `/s2s/roles/sync`; `/auth/me` scoped to the active tenant | `reference_service_rbac_authme_sync.md`, `auth-me-active-tenant-role-scoping-2026-08-20.md` |
| Tenant UUIDs | auth-api owns tenant IDs; resolve by slug through the syncer, never mint local tenant IDs | global `reference_tenant_uuid_drift.md` |
| Outlets | Property is an outlet; outlet keys and caches scoped by tenant slug | `tenant-switch-outlet-cache-fix.md` |
| Customer identity | Store `auth_user_id`; send phone and contact ID together on any treasury customer key | `boi-treasury-duplicate-receipts-incident-2026-09-14.md` |
| Personal data | Encrypt ID numbers and KRA PINs; hash phones for matching; minimum data at the gate | SRDD 15.2, NFR-09 |
| Imports | Validate every row, dry run first, batches of 500, never a fleet-wide unbounded query | `feedback_never_defer_performance_resilience.md` |
