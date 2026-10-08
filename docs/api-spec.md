# maskani-api API specification

Base: `https://maskaniapi.codevertexafrica.com/api/v1/{tenant}/maskani` (tenant = slug or UUID).
Swagger UI at `/v1/docs/`. All bodies are JSON; money is a decimal string in KES.

Routes marked **planned (sprint N)** are designed but not built yet; everything else is live.

## Conventions

| Topic | Rule |
|---|---|
| Auth | Staff: SSO bearer token. Customers and vendor supervisors: phone OTP token from auth-api. S2S: `X-API-Key` plus `X-Tenant-ID`. The live stream also accepts `?token=` (EventSource cannot send headers) |
| Tenant | Always from the signed token; the URL slug must match unless the caller is a platform owner |
| Lists | Growing lists use keyset pagination: `?limit=20&cursor=<opaque>`; response `{ "data": [...], "next_cursor": "...", "has_more": true }`. `limit` defaults to 20 and is capped at 100 (a larger value returns 100 rows). Order is newest first unless stated. Short lists (funds, charge types, price lists, meters, a reading round) return `{ "data": [...] }` without paging |
| Writes | `Idempotency-Key` header honoured on POST where noted; retries return the original result |
| Errors | `{ "error": "message", "code": "machine_code" }`. Codes include `module_not_enabled` (403), `feature_not_available` (403), `forbidden` (403), `not_found` (404), `conflict` (409), `validation_failed` (422), `invalid_credentials` (401, gate sign-on) |
| Access | Each route lists its permission. Property-scoped staff see only assigned properties: a `property_id` outside their scope is 403, and lists without `property_id` are limited to their properties. Customers see only records linked to them through `unit_parties` |

## Session and live updates

| Method and path | Purpose | Access |
|---|---|---|
| GET `/auth/me` | User, roles, permissions, `all_properties`, `property_ids`, `party_ids`, `is_staff`, `is_portal_user`, `bypass` (true for platform owners, superusers and S2S: the API lets them through every permission and module gate, so the UI shows all navigation), enabled `modules`, `settings` | Any signed-in user |
| GET `/stream` | Server-sent change hints (see below) | Any signed-in user; filtered by access |

### GET /stream

`text/event-stream`. Authenticate with the bearer header, or `?token=<access token>` (the token is
moved into the Authorization header and removed from the URL before anything logs it). A comment
`: ping` arrives every 15 seconds; `retry: 5000` is sent on connect. Each event is:

```
event: work_order.updated
data: {"type":"work_order.updated","id":"<uuid>","property_id":"<uuid>","unit_id":"<uuid>"}
```

`property_id` and `unit_id` are omitted when not relevant. Payloads are hints only: refetch the
record through its normal endpoint. Staff receive events for properties they can see (events with
no property reach all staff); portal users receive only events whose `unit_id` is one of their
units. Delivery is best effort across pods; refetch after a reconnect.

| Event | `id` is | Raised when |
|---|---|---|
| `billing_run.progress` | billing run | Run created, after each batch of 500 lines, on finish, on retry |
| `payment.applied` | unit account | A treasury payment is reflected on the account (after the balance refresh) |
| `work_order.updated` | work order | Created (staff or portal) or any lifecycle action |
| `gate.event` | gate event | Entry, exit or denial stored from a tablet |
| `walk_in.requested` | gate event | Walk-in request stored |
| `walk_in.decided` | gate event | Host approved, declined or timed out |
| `reading.saved` | meter reading | Reading recorded, estimated, or verified |
| `notice.status` | notice | Notice moves to scheduled, sending, sent or failed |

## Settings and catalogues

| Method and path | Purpose | Permission |
|---|---|---|
| GET `/settings`; PUT `/settings` | Tenant settings (type, billing day, due day, reading window, quiet hours, allocation order, terms versions, support contacts) | `settings.view` / `settings.manage` |
| GET `/settings/modules`; PUT `/settings/modules` `{modules:[...]}` or `{preset}` | Module switches, presets, dependencies | `settings.view` / `settings.manage` |
| GET `/catalogues/{kind}` | Platform defaults merged with tenant overrides | signed-in staff |
| PUT `/catalogues/{kind}/{code}` `{name, active, attrs}` | Tenant override or custom entry | `settings.manage` |
| GET `/users?kind=`; GET `/roles`; PUT `/users/{id}/roles` `{roles}` | Users and roles | `users.view` / `users.manage` |
| DELETE `/catalogues/{kind}/{code}` | Remove an override | planned (sprint 1) |
| GET, POST, PUT `/settings/custom-fields` | Custom field definitions | planned (sprint 1) |
| GET, PUT `/settings/approval-rules`, `/settings/reminders` | Approval and reminder rules | planned (sprint 2) |

## Register (module `properties`)

| Method and path | Purpose | Permission |
|---|---|---|
| GET, POST `/properties`; GET, PATCH `/properties/{id}` | Properties (POST creates the auth-api outlet with the caller's token) | `properties.view` / `properties.manage` |
| POST `/properties/{id}/blocks` | Add a block | `properties.manage` |
| GET `/properties/{id}/staff`; POST `/properties/{id}/staff` `{auth_user_id, property_role, erp_employee_id}`; DELETE `/staff-assignments/{id}` | Staff per property | `users.view` / `users.manage` |
| GET `/units` (keyset; `property_id`, `block_id`, `sale_status`, `occupancy_status`, `q`); POST `/units`; GET, PATCH `/units/{id}` | Units with owner name and balance | `units.view` / `units.manage` |
| POST `/units/{id}/parties` `{party_id, role, ownership_share, is_primary, start_date, end_date, bill_to, source}`; POST `/unit-parties/{id}/end` `{end_date}` | Ownerships and occupancies | `parties.manage` |
| POST `/units/{id}/vehicles` `{party_id, plate, make, model, colour}` | Vehicles | `parties.manage` |
| GET `/parties` (keyset; `q`); POST `/parties`; GET, PATCH `/parties/{id}`; POST `/parties/{id}/invite` | Parties (identity numbers masked) | `parties.view` / `parties.manage` |
| GET `/units/{id}/timeline`; PATCH `/blocks/{id}`; PATCH `/unit-parties/{id}`; DELETE `/vehicles/{id}` | | planned (sprint 1) |
| GET `/imports/template` | CSV template (header plus one example row) | `units.manage` |
| POST `/imports` (multipart `property_id`, `file`) | Checks a units and owners CSV and records the job; writes nothing else. 201 with the job | `units.manage` and `parties.manage` |
| GET `/imports` (`limit` up to 50); GET `/imports/{id}` | Recent jobs; one job with `summary.counts`, `summary.plans` and `errors` | `units.manage` and `parties.manage` |
| POST `/imports/{id}/commit` | Saves a validated job in the background (202); poll GET `/imports/{id}` for `rows_committed` | `units.manage` and `parties.manage` |

CSV import rules (`internal/modules/imports`):

- Columns in any order, unknown ones ignored, Excel's byte order mark tolerated. Only `unit_code`
  is required. Template columns: `unit_code, block, unit_type, bedrooms, bathrooms, size_sqm,
  floor, sale_status, occupancy_status, owner_name, owner_phone, owner_email, owner_since`.
- At most 5,000 rows and 4 MB per file. Errors carry the file's own line number.
- Every row matches existing records by natural key through `register/lookup.go` (block code, unit
  code, owner phone hash, active owner link), so a re-run updates and never duplicates. The same
  lookups serve `cmd/seed-tenant`.
- Commit re-checks each row, then applies it through the register service (so validation and
  phone encryption are the same as the screens), updating `rows_committed` every 500 rows.
  A job can be committed once (the status moves from `validated` to `committing` atomically).
- Owner phones and emails sit in the job only between the check and the commit; responses never
  include them, and the raw rows are dropped when the commit finishes.

GET `/parties/{id}` returns the party fields plus `national_id_masked`, `kra_pin_masked` and
`units: [{id, unit_id, unit_code, property_id, role, is_primary, start_date, end_date, bill_to, status}]`
(links outside the caller's properties are left out; 403 when none is visible).

## Charges, billing and collections (module `billing`)

| Method and path | Purpose | Permission |
|---|---|---|
| GET `/funds`; PATCH `/funds/{id}` | Funds with bank account, paybill and account prefix | `billing.view` / `billing.manage` |
| GET `/charge-types?all=`; POST `/charge-types`; POST `/charge-types/enable` `{code}`; PATCH `/charge-types/{id}` | Charge catalogue | `billing.view` / `billing.manage` |
| POST `/charge-types/{id}/rates` | Dated rate by scope; issued invoices never change | `billing.manage` |
| POST `/billing-runs/preview` `{property_id, fund, period}` | Compute a run without issuing | `billing.run` |
| POST `/billing-runs` | Issue a run (one per property, fund and period) | `billing.run` |
| GET `/billing-runs` (keyset; `property_id`) | Runs | `billing.view` |
| GET `/billing-runs/{id}` | Run with `counts: {pending, issued, failed, skipped, total}` | `billing.view` |
| GET `/billing-runs/{id}/lines`; POST `/billing-runs/{id}/retry` | Lines; retry failed lines | `billing.view` / `billing.run` |
| GET `/unit-accounts` (keyset; `property_id`, `owing=true`, `fund`) | Accounts with cached balance, fund and unit | `billing.view` |
| GET `/unit-accounts/{id}/statement` | Account with treasury ledger | `billing.view` |
| POST `/unit-accounts/{id}/pay` | Staff-initiated STK for an owner | `billing.collect` |
| GET `/collections/suspense?days=`; POST `/collections/suspense/{trans_id}/assign` `{unit_account_id}` | Unmatched paybill payments | `billing.collect` |
| GET `/reports/arrears` (keyset by balance, largest first; `property_id`) | `{account_id, account_ref, customer_name, customer_phone, balance, last_payment_at}` | `reports.view` |
| GET, POST `/units/{id}/charges`; PATCH `/unit-charges/{id}` | Opt-in charges per unit | planned (sprint 2) |
| GET `/unit-accounts/{id}`; statement `?format=pdf` | | planned (sprint 2) |
| POST `/adjustments`; `/adjustments/{id}/approve`, `/reject`; `/bill-queries` | Credit notes, waivers, bill queries | planned (sprint 2) |

## Meters and utilities (module `utilities`)

| Method and path | Purpose | Permission |
|---|---|---|
| GET `/meters?property_id=`; POST `/meters` | Meters | `utilities.view` or `utilities.read` / `utilities.manage` |
| GET `/reading-rounds/{period}?property_id=` | Round for a period in walking order (opens it if needed) | `utilities.read` |
| POST `/meters/{id}/readings` `{period, reading, photo_key, read_at, notes}` | Record a reading; anomaly flags returned | `utilities.read` |
| POST `/meters/{id}/estimate` `{period}`; POST `/meter-readings/{id}/verify` `{action}` | Estimate; accept, reject or recheck | `utilities.manage` |
| GET `/water-balance?property_id=&period=` | Supplied, billed, common and loss for six periods | `utilities.view` |
| PATCH `/meters/{id}`; POST `/meters/{id}/replace`; POST `/reading-rounds/{id}/close` | | planned (sprint 2) |

## Sales (module `sales`)

| Method and path | Purpose | Permission |
|---|---|---|
| GET `/price-lists?property_id=`; POST `/price-lists` | Price lists with items | `sales.view` / `sales.manage` |
| GET `/availability?property_id=` | Units with sale status and current price | `sales.view` |
| GET `/reservations` (keyset; `property_id`, `status`) | Reservations plus `unit_code`, `property_id`, `buyer_name` | `sales.view` |
| POST `/reservations` `{unit_id, party_id, days}` | Reserve, open the sales account, invoice the fee | `sales.manage` |
| GET `/sale-contracts` (keyset; `property_id`, `status`); POST `/sale-contracts`; GET `/sale-contracts/{id}` | Contracts; GET one returns the active schedule, `next_due` and `balance` | `sales.view` / `sales.manage` |
| POST `/sale-contracts/{id}/activate` `{signed_at}` | Sign: sales account, schedule v1, first invoices | `sales.manage` |
| POST `/instalments/{id}/release` `{evidence_key}` | Release a milestone | `sales.manage` |
| GET `/reports/sales-position?property_id=` | Units by sale status, contract value, collected | `reports.view` or `sales.view` |
| GET `/enquiries` (keyset; `property_id`, `status`); PATCH `/enquiries/{id}` | Showcase enquiries | `sales.view` / `sales.manage` |
| POST `/reservations/{id}/cancel`; PATCH `/price-lists/{id}`; PATCH `/sale-contracts/{id}` | | planned (sprint 3) |
| `/sale-contracts/{id}/schedule`, `/restructure`, `/statement`, `/handover`, `/title-stages/{stage}` | | planned (sprint 3) |

## Portal (owners, occupants, buyers; `/me`)

| Method and path | Purpose |
|---|---|
| GET `/me/units` | Units linked to the caller with role and accounts |
| GET `/me/accounts/{id}/statement`; POST `/me/accounts/{id}/pay` | Statement and STK payment for an own account |
| GET `/me/purchase` | Purchase plans with schedule and next due |
| GET, POST `/me/passes`; POST `/me/passes/{id}/cancel` | Visitor passes for own units |
| GET, POST `/me/requests`; POST `/me/requests/{id}/actions` | Work requests; confirm or reopen within 7 days |
| GET `/me/notices`; POST `/me/terms/accept` `{version}` | Notices; terms acceptance |
| POST `/me/walk-ins/{id}/decide` `{approve}` | Host decision on a walk-in |
| POST `/me/queries`; GET, POST `/me/household` | planned (sprint 2, sprint 1) |

## Works (module `maintenance`) and vendors (module `providers`)

| Method and path | Purpose | Permission |
|---|---|---|
| GET `/work-orders` (keyset; `property_id`, `status` incl. `open`, `priority`, `overdue`); POST `/work-orders`; GET `/work-orders/{id}` | Work orders with timeline | `works.view` / `works.manage` |
| POST `/work-orders/{id}/actions` `{action, vendor_id, erp_employee_id, assigned_user_id, quote_amount, cost_amount, recharge, photos, minutes_on_site, note}` | assign, quote, approve_quote, start, complete, confirm, reopen, cancel, close | `works.manage` |
| GET `/vendors` (keyset; `status`) | Vendors with `documents`, `next_expiry`, `expired_documents`, `personnel` (each with `has_pin`) | `vendors.view` |
| POST `/vendors` | Create a vendor | `vendors.manage` |
| GET `/vendors/{id}` | Vendor with documents and personnel (`has_pin`, never the hash); personnel deployed only outside the caller's properties are left out | `vendors.view` |
| POST `/vendors/{id}/documents` `{doc_type, number, issued_at, expires_at, file_key}` | Compliance document | `vendors.manage` |
| POST `/vendors/{id}/personnel` `{full_name, role, phone, badge_number, property_ids}` | Personnel badge | `vendors.manage` |
| PUT `/vendors/{id}/personnel/{pid}/pin` `{pin}` | Set a guard's gate PIN (4 to 6 digits, stored as a bcrypt hash); returns the personnel row with `has_pin: true` | `vendors.manage` |
| PATCH `/vendors/{id}`; `/vendors/{id}/contracts`, `/visits`; `/service-visits/{id}/check-in`, `/check-out`; `/maintenance-schedules` | | planned (sprint 4) |

## Gate and security

Staff side (module `gate`):

| Method and path | Purpose | Permission |
|---|---|---|
| POST `/gate/devices` `{property_id, name, gate_name}` | Register a tablet; returns `device_key` once | `gate.manage` |
| GET `/gate/events?property_id=` (keyset) | Gate log | `gate.view` |
| GET `/visitor-passes` (keyset; `property_id`, `active`); POST `/visitor-passes` | Passes; POST returns `code` and `qr_token` once | `gate.view` / `gate.manage` |
| GET `/incidents` (keyset; `property_id`, `open`); POST `/incidents` | Incidents | `gate.view` |
| GET `/incidents/{id}` | One incident (incident alert deep link), property scoped | `gate.view` |
| `/guard-posts`, `/patrol-checkpoints`, `/patrols/scans`; PATCH `/incidents/{id}` | | planned (sprint 4) |

Tablet side, `/api/v1/gate` with header `X-Device-Key`:

| Method and path | Purpose |
|---|---|
| POST `/gate/verify` `{code}` or `{qr}` | Verify a pass at the device's property |
| POST `/gate/events` `{events:[...]}` | Record entries, exits, denials and walk-ins (idempotent by `client_event_id`, at most 500) |
| GET `/gate/sync` | `{device, cache: {server_time, passes: [...device-salted hashes], badges: [{id, badge_number, name, role, vendor_id, has_pin}]}}`; badges are active personnel deployed to the device's property |
| POST `/gate/sign-on` `{badge, pin}` | Guard sign-on; `200 {guard: {id, name, badge}, signed_on_at}`; `401 invalid_credentials` for any wrong badge, PIN or a guard not deployed to this property. Rate limited per device (10 a minute) and per IP (30 a minute) |
| GET `/gate/units` | Unit codes for the walk-in host picker |
| GET `/gate/walk-ins/{id}` | Host decision so far; `{id}` is the event id or the `client_event_id` the tablet generated, scoped to the calling device |
| POST `/gate/incidents` | Incident from the gate |

## Communication (module `communication`)

| Method and path | Purpose | Permission |
|---|---|---|
| GET `/notices` (keyset; `property_id`, `status`); POST `/notices`; POST `/notices/{id}/send`; GET `/notices/{id}/deliveries` | Notices by audience over WhatsApp and email | `notices.manage` |
| `/templates`, `/documents/...` | Templates, generated documents, signatures | planned (sprint 5) |

## Reports

| Method and path | Purpose | Permission |
|---|---|---|
| GET `/reports/dashboard?property_id=&period=` | See below | `reports.view` |
| GET `/reports/collections`, `/instalment-receivables`, `/maintenance`, `/vendor-scorecard`, `/security`; CSV and PDF export | | planned (sprint 5) |

The dashboard reads through the read-only database and is cached for 60 seconds per tenant, scope
and period (dropped on every pod when a payment is applied or a billing run progresses). Response:
`period`, `billed`, `collected`, `collection_rate`, `outstanding`, `accounts_owing`,
`arrears_60_accounts`, `arrears_60_amount`, `open_work_orders`, `past_sla`, `water_loss_pct`
(with a property only), `vendors_due_for_renewal`, `units`, `occupied`, `units_sold`, `sales_value`,
`sales_collected`, plus

- `collections_by_week: [{week_start: "YYYY-MM-DD", billed, collected}]`, one row per week (weeks start Monday) touching the period;
- `arrears_ageing: [{bucket: "0-30"|"31-60"|"61-90"|"90+", accounts, amount}]`, always four rows. Age is the days since the oldest unpaid due date, laying each balance against the newest bills first.

Money values are decimal strings. Without `property_id` a property-limited user gets figures for
their properties only.

## Privacy and export

| Method and path | Purpose | Permission |
|---|---|---|
| `/privacy/requests`, `/privacy/export` | Data subject requests, tenant export | planned (sprint 5) |

## S2S (`/api/v1/s2s/{tenant}/maskani`)

| Method and path | Purpose |
|---|---|
| GET `/unit-accounts/by-ref/{account_ref}`; GET `/units/{id}` | planned (sprint 6) |

## Public market (`/api/v1/market`, rate limited per IP)

| Method and path | Purpose |
|---|---|
| GET `/estates` | Published estates |
| GET `/estates/{slug}` | Published estate with units for sale and prices |
| POST `/enquiries` | Enquiry (5 a minute per IP) |
| GET `/units/{id}` | planned (sprint 8) |
