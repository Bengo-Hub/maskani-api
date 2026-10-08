# maskani-api API specification

Base: `https://maskaniapi.codevertexafrica.com/api/v1/{tenant}/maskani` (tenant = slug or UUID).
Swagger UI at `/v1/docs/`. All bodies are JSON; money is a decimal string or number in KES.

## Conventions

| Topic | Rule |
|---|---|
| Auth | Staff: SSO bearer token. Customers and vendor supervisors: phone OTP token from auth-api. S2S: `X-API-Key` plus `X-Tenant-ID` |
| Tenant | Always from the signed token; the URL slug must match unless the caller is a platform owner |
| Lists | Keyset pagination: `?limit=50&cursor=<opaque>`; response `{ "data": [...], "next_cursor": "...", "has_more": true }`. Filters are query params; `q` searches names and codes |
| Writes | `Idempotency-Key` header honoured on POST; retries return the original result |
| Errors | `{ "error": "message", "code": "machine_code" }`. Codes include `module_not_enabled` (403), `feature_not_available` (403), `limit_reached` (402), `forbidden` (403), `not_found` (404), `conflict` (409), `validation_failed` (422) |
| Access | Each route lists its permission. Property-scoped roles see only assigned properties. Customers see only records linked to them through `unit_parties` |

## Session

| Method and path | Purpose | Access |
|---|---|---|
| GET `/auth/me` | User, roles, permissions, assigned properties, enabled modules, party links (for portal users) | Any signed-in user |

## Settings and catalogues

| Method and path | Purpose | Permission |
|---|---|---|
| GET, PUT `/settings` | Tenant settings (type, preset, billing day, due day, quiet hours, allocation order) | `settings.manage` (GET: `settings.view`) |
| GET, PUT `/settings/modules` | Module switches; preset application | `settings.manage` |
| GET `/catalogues/{kind}` | Platform defaults merged with tenant overrides | signed-in staff |
| POST, PUT, DELETE `/catalogues/{kind}/{code}` | Tenant override or custom entry | `settings.manage` |
| GET, POST, PUT `/settings/custom-fields` | Custom field definitions | `settings.manage` |
| GET, PUT `/settings/approval-rules`, `/settings/reminders` | Approval and reminder rules | `settings.manage` |
| GET, POST, PUT `/funds` | Funds with bank account, paybill and account prefix | `billing.manage` |

## Register

| Method and path | Purpose | Permission |
|---|---|---|
| GET, POST `/properties`; GET, PATCH `/properties/{id}` | Properties (creates the auth-api outlet) | `properties.view` / `properties.manage` |
| GET, POST `/properties/{id}/blocks`; PATCH `/blocks/{id}` | Blocks | `properties.manage` |
| GET, POST `/units`; GET, PATCH `/units/{id}` | Units; filters `property_id`, `block_id`, `sale_status`, `occupancy_status`, `q` | `units.view` / `units.manage` |
| GET `/units/{id}/timeline` | Bills, payments, readings, works, documents for one unit | `units.view` |
| GET, POST `/units/{id}/parties`; PATCH `/unit-parties/{id}`; POST `/unit-parties/{id}/end` | Ownerships and occupancies with dates and bill-to | `parties.manage` |
| GET, POST `/units/{id}/vehicles`; DELETE `/vehicles/{id}` | Vehicles | `parties.manage` |
| GET, POST `/parties`; GET, PATCH `/parties/{id}`; POST `/parties/{id}/invite` | Parties; invite links the auth user and sends the portal invite by WhatsApp and email | `parties.view` / `parties.manage` |
| GET, POST `/properties/{id}/staff`; DELETE `/staff-assignments/{id}` | Staff per property with property role and ERP employee | `users.manage` |
| POST `/imports` (multipart, `kind`, `dry_run`) ; GET `/imports/{id}` | CSV import of properties, units, parties, ownerships, opening balances | `imports.run` |

## Charges and billing

| Method and path | Purpose | Permission |
|---|---|---|
| GET, POST `/charge-types`; PATCH `/charge-types/{id}` | Charge catalogue (seeded plus custom) | `billing.view` / `billing.manage` |
| GET, POST `/charge-types/{id}/rates` | Dated rates by scope; issued invoices never change | `billing.manage` |
| GET, POST `/units/{id}/charges`; PATCH `/unit-charges/{id}` | Opt-in charges per unit | `billing.manage` |
| GET `/unit-accounts`; GET `/unit-accounts/{id}`; GET `/unit-accounts/{id}/statement` | Accounts per fund with balance; statement from treasury (PDF via `?format=pdf`) | `billing.view` |
| POST `/billing-runs/preview` | Compute a run without issuing: lines per unit, totals, warnings | `billing.run` |
| POST `/billing-runs`; GET `/billing-runs`; GET `/billing-runs/{id}`; GET `/billing-runs/{id}/lines` | Issue a run (idempotent per property, fund and period); progress and results | `billing.run` / `billing.view` |
| POST `/billing-runs/{id}/retry` | Retry failed lines only | `billing.run` |
| POST `/adjustments`; POST `/adjustments/{id}/approve`, `/reject` | Credit notes, waivers under approval rules | `billing.adjust` / `billing.approve` |
| GET `/collections/suspense`; POST `/collections/suspense/{trans_id}/assign` | Unmatched paybill payments from treasury; assign to an account | `billing.collect` |
| GET, POST `/bill-queries`; PATCH `/bill-queries/{id}` | Bill queries queue | `billing.view` / `billing.manage` |

## Meters and utilities

| Method and path | Purpose | Permission |
|---|---|---|
| GET, POST `/meters`; PATCH `/meters/{id}`; POST `/meters/{id}/replace` | Meters incl. bulk and borehole; replacement keeps closing and opening readings | `utilities.manage` |
| GET `/reading-rounds/{period}?property_id=` ; POST `/reading-rounds` | Round for a period with units in walking order | `utilities.read` |
| POST `/meters/{id}/readings` (multipart photo) | Record a reading; anomaly flags returned | `utilities.read` |
| POST `/meter-readings/{id}/verify`, `/estimate` | Accept, request recheck, or estimate (3 month average) | `utilities.manage` |
| POST `/reading-rounds/{id}/close` | Close round for billing | `utilities.manage` |
| GET `/water-balance?property_id=&period=` | Supplied vs billed vs common, loss percentage and trend | `utilities.view` |

## Sales

| Method and path | Purpose | Permission |
|---|---|---|
| GET, POST `/price-lists`; PATCH `/price-lists/{id}`; GET, POST `/price-lists/{id}/items` | Price lists by unit type and phase | `sales.manage` |
| GET `/availability?property_id=` | Units with sale status and current price | `sales.view` |
| POST `/reservations`; POST `/reservations/{id}/cancel`; GET `/reservations` | Reservation with fee invoice and expiry | `sales.manage` |
| GET, POST `/sale-contracts`; GET, PATCH `/sale-contracts/{id}` | Contracts with buyers, price, deposit, terms, advocates | `sales.view` / `sales.manage` |
| POST `/sale-contracts/{id}/activate` | Sign: create sales unit account, schedule, first invoices | `sales.manage` |
| POST `/sale-contracts/{id}/schedule` | Generate schedule (outright, monthly, quarterly, milestone) | `sales.manage` |
| POST `/sale-contracts/{id}/restructure` | Proposed new schedule; approval and buyer acceptance keep history | `sales.manage` |
| POST `/instalments/{id}/release` | Release a milestone instalment with evidence | `sales.manage` |
| GET `/sale-contracts/{id}/statement` | Purchase statement | `sales.view`; buyer own |
| POST `/sale-contracts/{id}/handover`; PATCH `/handovers/{id}` | Handover checklist, snags, readings, keys | `sales.manage` |
| GET, PUT `/sale-contracts/{id}/title-stages/{stage}` | Title tracking | `sales.manage` |

## Portal (owners, occupants, buyers)

| Method and path | Purpose |
|---|---|
| GET `/me/units` | Units linked to the caller with role and accounts |
| GET `/me/accounts/{id}/statement` | Statement for one of the caller's accounts |
| POST `/me/accounts/{id}/pay` | Create an `account_payment` intent (STK through the tenant gateway); returns treasury intent for the shared payment modal |
| GET `/me/purchase` | Purchase plans with schedule, paid and next due |
| GET, POST `/me/passes`; POST `/me/passes/{id}/cancel` | Visitor passes for own units |
| GET, POST `/me/requests` | Work requests for own units; confirm or reopen within 7 days |
| POST `/me/queries` | Bill query |
| GET `/me/household`; POST `/me/household` | Occupants, household, domestic staff, vehicles (owner only) |
| GET `/me/notices` | Notices addressed to the caller |
| POST `/me/terms/accept` | Record terms and privacy acceptance version |

## Works and vendors

| Method and path | Purpose | Permission |
|---|---|---|
| GET, POST `/work-orders`; GET, PATCH `/work-orders/{id}` | Requests and work orders; filters status, priority, property, overdue | `works.view` / `works.manage` |
| POST `/work-orders/{id}/assign`, `/quote`, `/approve-quote`, `/start`, `/complete`, `/confirm`, `/reopen`, `/cancel` | Lifecycle with timeline and SLA timers | `works.manage` (resident confirm/reopen own) |
| GET, POST `/maintenance-schedules`; PATCH `/maintenance-schedules/{id}` | Preventive maintenance | `works.manage` |
| GET, POST `/vendors`; GET, PATCH `/vendors/{id}` | Vendors (linked to treasury vendor) | `vendors.view` / `vendors.manage` |
| `/vendors/{id}/documents`, `/contracts`, `/personnel`, `/visits` | Compliance documents, contracts, personnel badges, visits | `vendors.manage` |
| POST `/service-visits/{id}/check-in`, `/check-out` | Visit evidence with photos | vendor supervisor, `vendors.manage` |

## Gate and security

| Method and path | Purpose | Access |
|---|---|---|
| POST `/gate/devices/register` | Register a tablet to a property (returns device key once) | `gate.manage` |
| POST `/gate/verify` | Verify a QR token or 6-digit code; returns pass and host | registered device + guard PIN |
| POST `/gate/events` | Record entry, exit, denial, walk-in | registered device |
| POST `/gate/sync` | Batch upload of offline events (idempotent by `client_event_id`); returns passes valid for 24 hours and active badges | registered device |
| POST `/gate/walk-ins`; GET `/gate/walk-ins/{id}` | Walk-in host approval (portal, opened from the WhatsApp button), 5 minute timeout | device; host via portal |
| GET `/gate/events`; GET `/visitor-passes` | Logs | `gate.view` |
| GET, POST `/guard-posts`, `/patrol-checkpoints`; POST `/patrols/scans` | Posts and patrol scans | `gate.manage`; device |
| GET, POST `/incidents`; PATCH `/incidents/{id}` | Incidents and occurrence book | `gate.view` / `gate.manage`; device |

## Communication and documents

| Method and path | Purpose | Permission |
|---|---|---|
| GET, POST `/notices`; POST `/notices/{id}/send`; GET `/notices/{id}/deliveries` | Notices by audience and channel | `notices.manage` |
| GET, POST `/templates`; POST `/templates/{id}/approve` | Template library | `documents.manage` |
| POST `/documents/generate`; GET `/documents/{id}/link` | PDF generation and signed link | `documents.manage`; parties own |
| POST `/documents/{id}/accept`, `/upload-signed` | OTP acceptance or wet-signed upload | party own / `documents.manage` |
| GET `/documents/verify/{code}` | Public verification of a document copy | public, rate limited |

## Reports

| Method and path | Purpose | Permission |
|---|---|---|
| GET `/reports/dashboard?property_id=` | Collections this month, arrears over 60 days, open work orders and SLA, water loss, vendors due | `reports.view` |
| GET `/reports/collections`, `/arrears`, `/sales-position`, `/instalment-receivables`, `/water-balance`, `/maintenance`, `/vendor-scorecard`, `/security` | Report data; `?format=csv` or `pdf` for export | `reports.view` |

## Privacy and export

| Method and path | Purpose | Permission |
|---|---|---|
| GET, POST `/privacy/requests`; POST `/privacy/requests/{id}/complete` | Data subject requests with deadlines | `privacy.manage` |
| POST `/privacy/export` | Per-tenant export job | `tenant.admin` |

## S2S (`/api/v1/s2s/{tenant}/maskani`)

| Method and path | Purpose |
|---|---|
| GET `/unit-accounts/by-ref/{account_ref}` | Resolve an account reference (diagnostics, treasury fallback) |
| GET `/units/{id}` | Unit summary for other services |

## Public market (`/api/v1/market`)

| Method and path | Purpose |
|---|---|
| GET `/estates/{tenant_slug}` | Published estate profile and units for sale (showcase) |
| GET `/units/{id}` | Published unit with price and photos |
| POST `/enquiries` | Enquiry with masked contact; rate limited per IP and phone |
