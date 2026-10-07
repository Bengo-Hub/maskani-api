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
| Owner portal identity | Party invite creates or links the auth user (`/s2s/tenants/{id}/members`), SMS invite; phone OTP sign-in through auth-api; terms acceptance recorded | Yes |
| `/auth/me` | Roles, permissions, assigned properties, modules, party links | Yes |
| Cross-tenant tests | Two tenants with overlapping unit codes | Yes |

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
