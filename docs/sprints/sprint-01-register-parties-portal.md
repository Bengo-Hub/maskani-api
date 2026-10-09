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

### Gaps found by the 2026-10-09 audit
Plan: `.claude/plans/maskani-r1-completion-r2-rentals-2026-10-09.md` (wave in brackets).

- [x] Property scope on EndLink, UpdateParty, InviteParty, RemoveStaff; ListParties scoped by linked units; CreateParty returns only id and name for a party the caller cannot see (FR-13, `15375db`)
- [x] Nobody grants or writes a role with permissions they do not hold; a property-limited admin invites only into their own properties (FR-13, `15375db`)
- [x] Portal `OwnsAccount` follows bill-to and role: an occupant reads and pays only estate accounts assigned to them, never the purchase account, and links stop at their `end_date` (FR-14, `15375db`)
- [x] Media upload needs a staff or portal session (portal users only their kinds), per-user rate limits, image dimensions checked before decoding; signing limited to the caller's kinds; signing key derived from the field key instead of the internal service key (NFR-09, `15375db`)
- [x] EndLink keeps a link active until a future end date (`15375db`)
- [x] Phone numbers validated with the fleet's httpware `contact` rules, stored form unchanged (`b8be252`)
- [ ] Custom field definitions with validation of `custom_fields` on write, filters and exports (FR-07, wave 2.11)
- [ ] Layered configuration: property overrides read by the module check (FR-07, FR-08, wave 2.11)
- [x] Module switched off (or off the plan) keeps its data readable: reads pass with `X-Module-Read-Only`, writes refused (FR-09, `3ae57a5`); jobs skipping switched-off modules still open
- [x] Terms acceptance read back from the server: `/auth/me` carries `terms_accepted_version` (FR-19, `4d05518`)
- [x] Tenant syncer on shared-service-client with a slug cache; access facts cached 30 seconds and cleared on staff, role, invite and link changes (`35df00b`)
- [x] Keyset indexes on units and parties (`3ae57a5`)
- [x] Imports: validated jobs expire after 7 days and lose their raw rows; stuck commits resume (`35df00b`)
- [ ] Plan limits on units and staff users counted in SQL (FR-03, wave 2.4)
- [ ] Terms and privacy acceptance history (version, time, IP) read back by the portal instead of device storage (FR-19, wave 2.4)
- [ ] Links end on their `end_date` by a daily job: portal and pass access revoked, final reading requested, bill-to reverts after `revert_after_days` (FR-14, SRDD 8.3 and 16.4, wave 2.7)
- [ ] Resale and transfer: clearance, close ownership, invite the new owner, split reading, apportion by days (SRDD 16.3, wave 2.7)
- [ ] Portal household, vehicles and domestic staff, each able to get gate passes (SRDD 16.2 step 5, wave 2.7)
- [ ] Import: natural-key maps preloaded per 500-row batch, validation once, no raw contacts kept after validation, uncommitted jobs purged after 7 days (wave 1a and 1b)
- [ ] Tenant syncer on shared-service-client and `cache.GetTenantDetails`; access resolution cached per user and tenant (wave 1b)
- [ ] Keyset indexes `(tenant_id, property_id, created_at, id)` on units and parties (wave 1b)

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
