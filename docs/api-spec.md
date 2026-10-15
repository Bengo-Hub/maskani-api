# maskani-api API specification

Base: `https://maskaniapi.codevertexafrica.com/api/v1/{tenant}/maskani` (tenant = slug or UUID).
`/v1/docs/` serves a route table today; Swagger annotations and the Swagger UI arrive in wave 2.13
of `.claude/plans/maskani-r1-completion-r2-rentals-2026-10-09.md`. All bodies are JSON; money is a
decimal string in KES.

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
| GET `/platform/tenants` | The tenants that use Maskani (`{data: [{id, slug, name}]}`, active, by name, up to 500) from the local tenant mirror, for the platform owner's tenant switcher (treasury's tenant filter) | Platform owner (token claim) only; 403 otherwise |
| GET `/auth/me` | User, roles, permissions, `all_properties`, `property_ids`, `party_ids`, `is_staff`, `is_portal_user`, `bypass` (true for platform owners, superusers and S2S: the API lets them through every permission and module gate, so the UI shows all navigation), enabled `modules`, `property_modules` (staff only: property id to module list, for each property whose use case or switches narrow the tenant set; other properties use `modules`), `settings` | Any signed-in user |
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
| GET `/settings`; PUT `/settings` | Tenant settings (type, billing day, due day, reading window, quiet hours, allocation order, terms versions, support contacts, `arrears_steps`, `walk_in_policy`, `late_charge` `{enabled, percent, fixed, cap, grace_days, day, funds}` kept in metadata). Invalid values answer 422 with the reason | `settings.view` / `settings.manage` |
| GET `/settings/modules`; PUT `/settings/modules` `{modules:[...]}` or `{preset}` | Module switches, presets, dependencies | `settings.view` / `settings.manage` |
| GET `/audit-logs?target_type=&target_id=&actor_id=&from=&to=` (keyset) | The audit trail: only critical security workflows, recorded by the `Audit` middleware from an allowlist (`internal/http/middleware/audit.go`) when they succeed. Access (roles, staff invites, role and status changes, property staff, portal invites, gate devices, vendor PINs), configuration (settings, modules, approval rules, document numbering and template approval, fund paybill and bank routing), money decisions (approvals, manual payment verification, credits, suspense assignment, bank statement imports, payment plans, billing without readings) and ownership (link ends, unit transfers, register imports). Routine work is never logged. Each entry: action, target, actor, IP, request id. A router test fails if an allowlisted route stops existing | `settings.manage` |
| GET `/document-sequences`; PUT `/document-sequences/{kind}` `{prefix, format, pad_width, reset_period, next_value}` | Numbering per kind (sale_contract, work_order, incident, document) with the next number; format tokens `{prefix} {seq} {yy} {yyyy} {mm}`; `reset_period` none, yearly or monthly, and a restarting series must show its year (and month) so numbers never repeat; `next_value` continues an existing series | `settings.view` / `settings.manage` |
| GET `/catalogues/{kind}` | Platform defaults merged with tenant overrides | signed-in staff |
| PUT `/catalogues/{kind}/{code}` `{name, active, attrs}` | Tenant override or custom entry | `settings.manage` |
| GET `/users?kind=`; GET `/roles`; PUT `/users/{id}/roles` `{roles}` | Users and roles | `users.view` / `users.manage` |
| GET `/permissions` | The permission catalogue `{code, name, module, action}` for the role matrix. Codes are `maskani.{module}.{action}`; the split-out codes `gate.passes`, `notices.view`, `documents.view`, `documents.issue` and `reports.export` were granted once to every estate role holding the code they came from | `users.view` |
| POST `/roles` `{code, name, description, permissions}`; POST `/roles/customize` `{code}`; PUT `/roles/{id}` `{name, description, permissions}`; DELETE `/roles/{id}` | Estate roles. A default role is changed by customising it (an estate copy that replaces it for this estate, holders moved over); deleting a copy returns its holders to the default; an estate role is deleted only when nobody holds it. The administrator role is locked to every permission. Nobody can write into a role, or grant a role with, a permission they do not hold | `users.manage` |
| DELETE `/catalogues/{kind}/{code}` | Remove an override | planned (sprint 1) |
| GET, POST, PUT `/settings/custom-fields` | Custom field definitions | planned (sprint 1) |
| GET `/approvals?status=&module=&property_id=&mine=true` (keyset); POST `/approvals/{id}/approve` `{comment}`; POST `/approvals/{id}/reject` `{comment}` | The central approvals inbox across every workflow (credit notes, waivers, manual payments, work order quotes, new sale payment plans, and the rest as they land). `mine` keeps requests whose current step the caller may act on. Deciding here has the same effect as on the workflow's own screen | `billing.approve`, `billing.verify`, `works.manage`, `sales.manage` or `settings.view` to list; the same less `settings.view` to act, and acting is checked against the step |
| GET, POST `/approvals/rules`; PUT, DELETE `/approvals/rules/{id}` `{module, name, min_amount, max_amount, steps: [{name, approver_role}], is_active}` | Approval rules per workflow and amount band with 1 to 5 ordered role steps; active bands for one module may not overlap. Without a rule each workflow uses its default (one step by permission) | `settings.view` or `billing.approve` / `settings.manage` |
| GET, PUT `/settings/reminders` | Reminder schedules beyond the collections ladder | planned |

## Register (module `properties`)

| Method and path | Purpose | Permission |
|---|---|---|
| GET, POST `/properties`; GET, PATCH `/properties/{id}` | Properties (POST creates the auth-api outlet with the caller's token). `use_case` (a preset code) and `module_overrides` (`{module: true or false}` over the preset) decide the property's modules; changing either on PATCH needs `settings.manage`. `photos` is the gallery in order (first is the cover, up to 12), each a key uploaded with `kind=properties`. The list marks one property `is_default`, the one the console opens on: the property on the tenant's head office outlet (auth-api `is_hq`, mirrored by the tenant syncer), otherwise the first by name | `properties.view` / `properties.manage` |
| POST `/properties/{id}/blocks` | Add a block | `properties.manage` |
| GET `/properties/{id}/staff`; POST `/properties/{id}/staff` `{auth_user_id, property_role, erp_employee_id}`; DELETE `/staff-assignments/{id}` | Staff per property | `users.view` / `users.manage` |
| GET `/units` (keyset; `property_id`, `block_id`, `sale_status`, `occupancy_status`, `q`); POST `/units`; GET, PATCH `/units/{id}` | Units with owner name and balance. `photos` on PATCH: up to 12 keys uploaded with `kind=units` | `units.view` / `units.manage` |
| Plan limits on POST `/units`, POST `/imports/{id}/commit` (the whole file's new units) and POST `/users/invite` | The subscription tier's `max_units` and `max_users` from the token (as pos-api's structural limits): 402 `usage_limit_exceeded` with `metric`, `limit` and `used` when the change would cross the cap. Staff users are active staff holding a role; owners, occupants, guards and vendor supervisors never count. `-1` or unset is unlimited; exempt tenants (platform owner, demo, subscription exempt) are never blocked | as the route |
| POST `/units/{id}/parties` `{party_id, role, ownership_share, is_primary, start_date, end_date, bill_to, source}`; POST `/unit-parties/{id}/end` `{end_date}` | Ownerships and occupancies. A future end date keeps the link active until the hourly `maskani:occupancy-links` job ends it; today or earlier ends it now. Ending cancels the passes the person hosted for the unit, asks the caretakers for a final reading when an owner, buyer, landlord or occupant leaves a metered unit (`occupancy.final_reading`), and moves accounts billed to them to the unit's remaining owner. Bill-to on ended links clears after the estate's `bill_to_revert_days` (or the link's own `revert_after_days`) | `parties.manage` |
| POST `/units/{id}/transfer` `{new_owner_id, date, reading, invite, note}` | Resale or transfer: refused while any account on the unit owes (clear it and issue the clearance certificate first). Links the new owner as primary owner (source transfer), ends the current owners' links on the date with the occupancy effects (passes, final reading request, accounts move to the new owner), records an optional handover reading as a move-out reading (never billed twice), returns the month split by days (`apportion`: outgoing and incoming days, handover water use) and can invite the new owner to the portal. Publishes `unit.transferred` | `parties.manage` |
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
| GET `/funds`; PATCH `/funds/{id}` | Funds with bank account, paybill and account prefix. `paybill_account_format` (kept in the fund metadata) is what owners type as the paybill account number: `{ref}` (default, the unit reference), `2362010#{ref}` for a bank paybill that takes a reference, or the bank account alone (`2362010`), when the unit reference is quoted separately and payments come in from the bank side (statement import or recorded payments). Statements, bills, reminders and documents carry the resulting `pay_account` and `pay_reference`, and C2B routes follow it | `billing.view` / `billing.manage` |
| GET `/charge-types?all=`; POST `/charge-types`; POST `/charge-types/enable` `{code}`; PATCH `/charge-types/{id}` | Charge catalogue | `billing.view` / `billing.manage` |
| POST `/charge-types/{id}/rates` | Dated rate by scope; issued invoices never change | `billing.manage` |
| POST `/billing-runs/preview` `{property_id, fund, period}` | Compute a run without issuing | `billing.run` |
| POST `/billing-runs` | Issue a run (one per property, fund and period) | `billing.run` |
| GET `/billing-schedule?property_id=` | The property's schedule `{enabled, fund, mode (auto or remind), missing_readings (wait or skip), remind_days_before, approved, sent}` with its next run: `billing_day`, `period`, `billing_date`, `stage` (off, scheduled, collecting_readings, waiting_for_readings, ready_to_run, due, issued), `metered`, `missing_count`, `missing` (up to 200 unread meters with unit codes), `run_id`, `run_status`, `approved_by`. A billing day after the reading window bills the same month; an earlier one bills the month before | `billing.view` |
| PUT `/billing-schedule` `{property_id, enabled, fund, mode, missing_readings, remind_days_before}` | Save the schedule (kept in the property metadata). An hourly job reminds reading takers before the day, then runs the bill (auto), says it is ready (remind), or waits for missing readings and reminds daily (wait) | `billing.manage` |
| POST `/billing-schedule/approve` `{property_id, period}` | Run the period now without the readings still missing (those units get no metered line); the approval is kept on the schedule | `billing.run` |
| GET `/billing-runs` (keyset; `property_id`) | Runs | `billing.view` |
| GET `/billing-runs/{id}` | Run with `counts: {pending, issued, failed, skipped, total}` | `billing.view` |
| GET `/billing-runs/{id}/lines?status=&cursor=&limit=`; POST `/billing-runs/{id}/retry` | Lines as a keyset page in unit code order (cursor on unit code then id), optionally one status; retry failed lines | `billing.view` / `billing.run` |
| GET `/unit-accounts` (keyset; `property_id`, `owing=true`, `fund`) | Accounts with cached balance, fund and unit | `billing.view` |
| GET `/unit-accounts/{id}/statement` | Account with treasury ledger and `entries` (bills and payments newest first, each with `balance_after`, worked back from the balance less credit) and `trimmed` (a ledger list came back full, older history exists) | `billing.view` |
| GET `/unit-accounts/{id}/statement/export?format=pdf\|csv\|xlsx` | Branded statement download with up to 500 bills and 500 payments, oldest first, built on the shared `github.com/Bengo-Hub/reports` engine; `Cache-Control: private, no-store`; 20 per user per minute | `billing.view` |
| POST `/unit-accounts/{id}/pay` | Staff-initiated STK for an owner | `billing.collect` |
| GET `/collections/suspense?days=`; POST `/collections/suspense/{trans_id}/assign` `{unit_account_id}` | Unmatched paybill payments | `billing.collect` |
| GET `/collections/call-list?property_id=` | Accounts the collections ladder put on the call list (day 30 by default) that still owe, largest first: account, customer, phone, unit, balance, last payment, oldest due, promise date, last note | `billing.collect` |
| POST `/unit-accounts/{id}/manual-payments` `{amount, method, reference, paid_on, payer_name, note, evidence_key}` | Staff record a bank transfer, cash, cheque or typed M-Pesa payment; it waits for review. One slip once (method and reference, a rejected one can be resubmitted) | `billing.collect` |
| POST `/collections/bank-lines` `{fund, property_id, lines: [{date, amount, reference, description, payer}]}` | Bank statement credits (up to 2,000) matched to the fund's accounts by a unit reference in the text (`TAN7`, `2362010#TAN7`; normalised like paybill references), else by the owner's phone (never a phone shared by two accounts), and queued for review as bank transfers. Each line comes back queued, duplicate (that bank reference is already queued), unmatched or invalid | `billing.collect` |
| POST `/me/accounts/{id}/manual-payments` | Portal: a resident gives a bank transfer or cheque reference (M-Pesa prompts stop at KES 250,000); it waits for review | portal user |
| GET `/collections/manual-payments?status=&property_id=&account_id=` (keyset) | The review queue and its history | `billing.collect` or `billing.verify` |
| POST `/manual-payments/{id}/approve` `{note}`; POST `/manual-payments/{id}/reject` `{reason}` | Decides the current step of the payment's approval (module `manual_payment`, default one `billing.verify` step); the last approval books it in treasury (S2S manual-payments, allocated oldest due first, ledger by method). The person who recorded it never verifies it. A booking failure leaves it pending with the approval done; approving again retries. The list carries `approvals` (object id to request) for progress | `billing.verify` (property managers, caretakers, administrators) |
| GET `/unit-accounts/{id}/collections`; POST `/unit-accounts/{id}/collection-notes` `{outcome, promise_date, note}` | The account's place on the ladder (episode, steps done, call list, promise) and up to 20 notes. Outcomes: reached, no_answer, promised (needs a date; holds the demand letter and escalation until it passes), disputed, wrong_number, paid. A promise or paid takes it off the call list | `billing.view` / `billing.collect` |
| PUT `/unit-accounts/{id}/payment-plan` `{instalments: [{due, amount}], note}`; DELETE to cancel | An agreed plan for the arrears, kept on the ladder (`plan`): 1 to 24 instalments from today, together no more than the balance. While active it holds back the demand letter and escalation and takes the account off the call list. A daily check counts payments since the start (`account_collections`): paid in full is completed; an instalment more than 3 days late and unpaid breaks it, releases the ladder and publishes `payment_plan.broken`. The agreement document is the `payment_plan` template | `billing.collect` |
| GET `/reports/arrears` (keyset by balance, largest first; `property_id`) | `{account_id, account_ref, customer_name, customer_phone, balance, last_payment_at}` | `reports.view` |
| GET, POST `/units/{id}/charges`; PATCH `/unit-charges/{id}` | Opt-in charges per unit | planned (sprint 2) |
| GET `/unit-accounts/{id}`; statement `?format=pdf` | | planned (sprint 2) |
| POST `/unit-accounts/{id}/adjustments` `{kind: credit_note or waiver, invoice_id, amount, reason}` | Asks to credit part of one unpaid bill on the account (never more than it still owes; one open request per bill) | `billing.adjust` |
| GET `/collections/adjustments?status=&property_id=&account_id=` (keyset) | Credits waiting for approval and their history | `billing.adjust` or `billing.approve` |
| POST `/adjustments/{id}/approve` `{note}`; POST `/adjustments/{id}/reject` `{reason}` | Decides the current step of the credit's approval on the central engine (module `credit_note` or `adjustment`; default one `billing.approve` step; rules add steps by amount). The requester never approves, one person decides one step, and the last approval raises the treasury credit note (S2S create-credit-note). A treasury failure leaves it approved; approving again retries. The list carries `approvals` | `billing.approve` |
| GET `/collections/bill-queries?status=&property_id=` (keyset) | Residents' bill queries, answer due 7 days after raising | `billing.view` |
| POST `/bill-queries/{id}/answer` `{status: in_review, resolved or rejected, resolution}` | Take a query, or answer it; the resident is told | `billing.adjust` or `billing.manage` |
| POST `/me/accounts/{id}/bill-queries` `{invoice_id, subject, body}`; GET `/me/bill-queries` | Portal: query a bill within 30 days of its date (five open per account at most) and see the answers | portal user |
| GET `/me/units/{id}/household`; POST `/me/units/{id}/household` `{name, phone, role: household_member or domestic_staff}`; POST `/me/units/{id}/household/{link}/end`; POST `/me/units/{id}/vehicles` `{plate, make, model, colour}`; POST `/me/units/{id}/vehicles/{vehicle}/remove` | Portal: owners and occupants (not household members themselves) list and manage the unit's household, domestic staff and vehicles. A person is matched by phone when already known; up to 30 per unit; only household roles can be added or ended here. Domestic staff then get passes like any visitor | portal user (owner, joint owner, buyer, landlord or occupant of the unit) |

## Meters and utilities (module `utilities`)

| Method and path | Purpose | Permission |
|---|---|---|
| GET `/meters?property_id=`; POST `/meters` | Meters | `utilities.view` or `utilities.read` / `utilities.manage` |
| GET `/reading-rounds/{period}?property_id=` | Round for a period in walking order (opens it if needed); each row has `previous_reading`, `average_use` (mean use over the meter's last three periods) and `spike_above` (the use the server flags as much higher than usual: three times the average) | `utilities.read` |
| POST `/meters/{id}/readings` `{period, reading, photo_key, read_at, notes}` | Record a reading; anomaly flags returned | `utilities.read` |
| POST `/meters/{id}/estimate` `{period}`; POST `/meter-readings/{id}/verify` `{action}` | Estimate; accept, reject or recheck | `utilities.manage` |
| POST `/meters/{id}/replace` `{period, closing_reading, new_serial, new_make, opening_reading, read_at, photo_key, notes}` | Replaces a meter in one transaction: the closing reading (source replacement, billed with the period) and a new meter with the old one's unit, kind, multiplier, walking order and charge, from its opening reading. The period's use is the old meter's last stretch plus the new meter's use; the bill note shows both | `utilities.manage` |
| POST `/properties/{id}/readings/batch` `{period, readings: [{meter_id, reading, read_at, notes, photo_key}]}` | Up to 500 readings of one property and period at once (bulk supply and borehole sheets, offline rounds); each line goes through the same checks and flags and reports its own result | `utilities.read` |
| GET `/water-balance?property_id=&period=` | Supplied, billed, common and loss for six periods | `utilities.view` |
| GET `/water-balance/export?format=pdf\|csv\|xlsx&property_id=&period=` | The same as a branded document with a loss chart | `utilities.view` and `reports.export` |
| PATCH `/meters/{id}`; POST `/meters/{id}/replace`; POST `/reading-rounds/{id}/close` | | planned (sprint 2) |

## Sales (module `sales`)

| Method and path | Purpose | Permission |
|---|---|---|
| GET `/price-lists?property_id=`; POST `/price-lists` | Price lists with items | `sales.view` / `sales.manage` |
| GET `/availability?property_id=&status=` | The sales board: `{groups:[{name, available, units:[...]}]}`, grouped by block (phase when a unit has no block), blocks in their set order and units in natural code order; each unit carries only the tile fields (code, type, bedrooms, size, sale status, active price, fee, deposit). Not paged: it shows a whole property and is bounded by its unit count. `status` narrows to one sale status | `sales.view` |
| GET `/reservations` (keyset; `property_id`, `status`) | Reservations plus `unit_code`, `property_id`, `buyer_name` | `sales.view` |
| POST `/reservations` `{unit_id, party_id, days}` | Reserve, open the sales account, invoice the fee | `sales.manage` |
| GET `/sale-contracts` (keyset; `property_id`, `status`); POST `/sale-contracts`; GET `/sale-contracts/{id}` | Contracts; GET one returns the active schedule, `next_due`, `balance`, `pending_plan`, `handover`, and while a fully paid unit waits for handover `handover_meters` `[{id, serial, last_reading}]` (the unit's working meters, so whoever completes the handover needs no access to meters) | `sales.view` / `sales.manage` |
| POST `/sale-contracts/{id}/activate` `{signed_at}` | Sign: sales account, schedule v1, first invoices | `sales.manage` |
| POST `/sale-contracts/{id}/restructure` `{reason, option: instalments\|outright, frequency, term_months, first_due}` | A new plan for what the active schedule has not billed yet, written as the next version with status `proposed` and sent to the central approvals engine (module `restructure`; default one approval by `sales.manage`, never the proposer). Billed lines stay as they are. One proposal at a time; the first payment cannot be before today in the tenant's time zone (today is allowed). `outright` is early completion: one balance payment | `sales.manage` |
| GET `/sale-contracts/{id}/schedules` | Every plan version, newest first, with lines and total. A contract's view (`GET /sale-contracts/{id}`, `/me/purchase`) shows the active plan plus lines billed under earlier versions, and `pending_plan` while an approved plan waits for the buyer | `sales.view` |
| POST `/sale-contracts/{id}/handover` `{scheduled_at}`; PATCH `/handovers/{id}` `{scheduled_at, keys, checklist, snags}` | Book the handover of a fully paid unit (one per contract; it starts with a walk-through checklist) and record keys `[{item, count}]`, checklist `[{item, ok}]` and snags `[{id, description (up to 500 characters), location, photos (up to 6), status: open\|fixed}]`. After completion only snags change; a fixed snag gets its date. The contract view carries `handover` | `sales.manage` |
| POST `/handovers/{id}/complete` `{handed_over_at, readings: [{meter_id, reading}]}` | Needs the keys recorded. Each reading is stored as a `handover` reading (not billed; the month's round reading measures from it, so the owner never pays for use before handover). Unit and contract move to handed over, `units.handed_over_at` starts estate billing prorated from that day, the buyer becomes the primary owner (estate account in their name) and `unit.handed_over` is published. Audited | `sales.manage` |
| GET `/sale-contracts/{id}/title`; PUT `/sale-contracts/{id}/title/{stage}` `{status, stage_date, reference, document_id, notes}` | The estate's title stages (catalogue `title_stage`, in order) with this sale's progress. Work starts once fully paid; a done stage needs its date; the document must be on record. `title_released` done moves contract and unit to titled. Changes are audited | `sales.view` / `sales.manage` |
| POST `/schedules/{id}/send-code`; POST `/schedules/{id}/accept` `{code}` | The buyer accepts an approved plan at the office by reading back a code sent to the phone and email on record (5 minutes, 5 tries, 5 codes). Accepting supersedes the old version, makes the new one active, cures a contract in default and invoices anything due | `sales.manage` |
| POST `/instalments/{id}/release` `{evidence_key}` | Release a milestone | `sales.manage` |
| GET `/reports/sales-position?property_id=` | Units by sale status, contract value, collected | `reports.view` or `sales.view` |
| GET `/enquiries` (keyset; `property_id`, `status`); PATCH `/enquiries/{id}` | Showcase enquiries | `sales.view` / `sales.manage` |
| POST `/reservations/{id}/cancel`; PATCH `/price-lists/{id}`; PATCH `/sale-contracts/{id}` | | planned (sprint 3) |
| `/sale-contracts/{id}/statement` (purchase and completion statements) | | planned (sprint 3) |

## Portal (owners, occupants, buyers; `/me`)

| Method and path | Purpose |
|---|---|
| GET `/me/units` | Units linked to the caller with role, accounts, the last reading, and `pay` (per account id: paybill, pay_account, pay_reference, bank_matched) |
| GET `/me/accounts/{id}/statement`; GET `/me/accounts/{id}/statement/export?format=`; POST `/me/accounts/{id}/pay` | Statement (with entries), its branded download, and STK payment for an own account |
| GET `/me/purchase` | Purchase plans with schedule and next due, and `pending_plan` when a new plan waits for acceptance |
| POST `/me/plans/{id}/accept` | The signed-in buyer accepts an approved new plan on their own contract (audited) |
| GET, POST `/me/passes`; POST `/me/passes/{id}/cancel` | Visitor passes for own units |
| GET, POST `/me/requests`; POST `/me/requests/{id}/actions` | Work requests; confirm or reopen within 7 days |
| GET `/me/notices`; POST `/me/terms/accept` `{version}`; GET `/me/terms` | Notices; terms acceptance. Each acceptance sets the current version on every party of the caller and adds `{version, privacy_version, at, ip}` to the party's `consents.terms_history` (the estate's privacy version at that moment, the address from the edge; the last 50 kept). GET `/me/terms` reads the history back, newest first, once per acceptance |
| POST `/me/walk-ins/{id}/decide` `{approve}` | Host decision on a walk-in, recorded with `decided_by: host`. The 5 minutes run from the latest ask (a ring restarts them). A walk-in the guard already settled comes back unchanged, so the page can say who answered |
| POST `/me/queries`; GET, POST `/me/household` | planned (sprint 2, sprint 1) |

## Works (module `maintenance`) and vendors (module `providers`)

| Method and path | Purpose | Permission |
|---|---|---|
| GET `/work-orders` (keyset; `property_id`, `status` incl. `open`, `priority`, `overdue`); POST `/work-orders`; GET `/work-orders/{id}` | Work orders with timeline | `works.view` / `works.manage` |
| POST `/work-orders/{id}/actions` `{action, vendor_id, erp_employee_id, assigned_user_id, quote_amount, cost_amount, recharge, photos, minutes_on_site, note}` | assign, quote (opens the quote approval), approve_quote and reject_quote (decide its current step on the central engine; the person who entered the quote never decides; reject needs a note), start, complete, confirm, reopen, cancel, close | `works.manage` |
| GET `/vendors` (keyset; `status`) | Vendors with `documents`, `next_expiry`, `expired_documents`, `replaced_documents`, `personnel` (each with `has_pin`). Only a vendor's newest document of each type counts towards expiry (`works.CurrentDocument`); older ones of that type are listed in `replaced_documents`. The same rule drives the dashboard, the vendor scorecard and the expiry alerts | `vendors.view` |
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
| GET `/gate/events?property_id=&kind=` (keyset) | Gate log; each row adds `unit_code`, `block` and `guard_name`; entries carry `exited_at`, exits `entry_event_id`, walk-ins `decision` and `decided_by` (host, guard) | `gate.view` |
| GET `/gate/inside?property_id=` | Who is inside now: entries and admitted walk-ins without an exit in the last 24 hours | `gate.view` |
| GET `/visitor-passes` (keyset; `property_id`, `active`); POST `/visitor-passes` | Passes, each with `unit_code` and `block`; POST returns `code` and `qr_token` once, needs a `unit_id` at the property when given, and links the returning visitor | `gate.view` / `gate.passes` or `gate.manage` |
| GET `/visitor-passes/{id}`; POST `/visitor-passes/{id}/cancel` | One pass; cancel an active pass (`409` when no longer active) | `gate.view` / `gate.passes` or `gate.manage` |
| GET `/gate/devices?property_id=`; POST `/gate/devices/{id}/revoke` | Tablets with `last_seen_at` and `online` (seen in 15 minutes); revoke a lost or replaced tablet's key | `gate.manage` |
| GET `/incidents` (keyset; `property_id`, `open`); POST `/incidents` | Incidents | `gate.view` |
| GET `/incidents/{id}` | One incident (incident alert deep link), property scoped | `gate.view` |
| `/guard-posts`, `/patrol-checkpoints`, `/patrols/scans`; PATCH `/incidents/{id}` | | planned (sprint 4) |

Tablet side, `/api/v1/gate` with header `X-Device-Key`:

| Method and path | Purpose |
|---|---|
| POST `/gate/verify` `{code}` or `{qr}` | Verify a pass at the device's property: `{valid, reason, pass, unit_code, block, host_name, visitor: {name, vehicle_plate, visits, last_visit_at, banned, notes}}` |
| POST `/gate/events` `{events:[...]}` | Record entries, exits, denials and walk-ins (idempotent by `client_event_id`, at most 500). Each event may carry `id_number` (kept only as a keyed hash on the visitor). An entry by pass takes the pass's unit, name and visitor. Entries and walk-ins link to the returning visitor matched by phone, ID number or plate (created when new). An exit names the entry it closes with `entry_event_id`, or `entry_client_event_id` for an entry in the same offline batch, or failing those the latest person inside with the same plate or name; an exit that closes nothing (a repeat tap, nobody named) is not recorded |
| GET `/gate/sync` | `{device, cache: {server_time, passes: [...device-salted hashes], badges: [{id, badge_number, name, role, vendor_id, has_pin}], walk_in_policy}}`; badges are active personnel deployed to the device's property; `walk_in_policy` is `guard_decides` (default: the guard lets walk-ins in and the host is told) or `ask_host` (wait for the host or override) |
| GET `/gate/inside` | Who is inside now, for the exit picker: `[{event_id, visitor_name, vehicle_plate, unit_code, block, since, pass_id, visitor_id, walk_in}]` |
| GET `/gate/visitors?q=` | Returning visitors matching a phone (part of the number), plate (start) or name: at most 8, with masked phone, visits, last unit, banned flag |
| POST `/gate/walk-ins/{id}/resolve` `{admit, note}` | The guard lets the walk-in in or turns them away on the walk-in's own row (one log line), at any time; a host's own answer stands. Admitting tells the host |
| POST `/gate/walk-ins/{id}/ring` | Ring the host again: a push that pops up on their phone plus WhatsApp; at most every 30 seconds; reopens a timed-out walk-in |
| POST `/gate/sign-on` `{badge, pin}` | Guard sign-on; `200 {guard: {id, name, badge}, signed_on_at}`; `401 invalid_credentials` for any wrong badge, PIN or a guard not deployed to this property. Rate limited per device (10 a minute) and per IP (30 a minute) |
| GET `/gate/units` | Unit codes for the walk-in host picker |
| GET `/gate/walk-ins/{id}` | Decision so far `{id, decision, decided_at, decided_by, rings}`; `{id}` is the event id or the `client_event_id` the tablet generated, scoped to the calling device |
| POST `/gate/incidents` | Incident from the gate |

## Communication (module `communication`)

| Method and path | Purpose | Permission |
|---|---|---|
| GET `/notices` (keyset; `property_id`, `status`); POST `/notices`; POST `/notices/{id}/send`; GET `/notices/{id}/deliveries` | Notices by audience over WhatsApp and email | lists and deliveries `notices.view` or `notices.manage`; write and send `notices.manage` |
| GET `/document-templates` | Per kind (clearance_certificate, demand_letter, payment_plan, bill_to_consent, offer_letter) the template in use (the estate's approved version, else the Codevertex starter, version 0) and any draft, with `merge_fields` and `inputs`; `kinds` lists them all | `documents.view`, `.issue` or `.manage` |
| PUT `/document-templates/{kind}` `{name, body}`; POST `/document-templates/{kind}/approve` `{version}` | Save new wording as a draft (unknown `{{field}}`s refused); approving puts it in use and retires the version it replaces | `documents.manage` |
| POST `/documents` `{kind, entity_id, values}` | Issue from the template in use about a unit account or sale contract at a property in scope: numbered from the `document` sequence, rendered as a branded PDF with signature lines, stored with its SHA-256 and a 10-character verification code (printed with the verify link). `values` carries the kind's inputs (payment plan: instalments, instalment_amount, first_due) | `documents.issue` or `.manage` |
| GET `/documents?unit_id=` or `?entity_type=&entity_id=`; GET `/documents/{id}/file` | Issued documents, newest first, within the caller's properties; the file download is logged in `document_access_logs` | `documents.view`, `.issue` or `.manage` |
| GET `/me/documents`; GET `/me/documents/{id}/file` | Portal: documents addressed to the signed-in owner or resident, each with `needs_signature` and `accepted_by_me` (staff lists carry `needs_signature` too) | portal user |
| POST `/documents/{id}/send-code` `{party_id}`; POST `/documents/{id}/accept` `{party_id, code}`; POST `/documents/{id}/signed-copy` `{keys}`; GET `/documents/{id}/signatures` | Agreements (kinds with `needs_signature`: payment plan, bill-to consent, letter of offer). Each person named accepts by reading back a code sent to the phone and email on their record (shared one-time code, 5 minutes); each acceptance is a `document_signatures` row with the document's SHA-256, time, IP and device. The office can instead record a signed paper copy: photos uploaded through `/media/upload` (kind `documents`), keys passed here. Partly signed until everyone named has accepted, then executed (`document.executed`). Code hashes never leave the API. Acceptances and signed copies are audited | `documents.issue` or `.manage` (signatures: also `.view`) |
| POST `/me/documents/{id}/send-code`; POST `/me/documents/{id}/accept` `{code}` | Portal: the signed-in person named on an agreement accepts it with the code sent to their record | portal user |
| GET `/api/v1/public/documents/verify/{code}` | Public check of a printed code: number, title, status, issue date, issuer (property name) and file hash, nothing personal; 30 a minute per IP | none |

## Reports

| Method and path | Purpose | Permission |
|---|---|---|
| GET `/reports/dashboard?property_id=&from=&to=&block_id=&fund=` (`period=` is one month) | See below. `from` and `to` are months (YYYY-MM, at most 12; billed and collected cover the range, the weekly chart every week of it); `block_id` (its property must be in scope) and `fund` narrow billed, outstanding, arrears and ageing, units, work orders and sales; collections are kept per property, so with a block or fund `collections_scope` is `property` | `reports.view` |
| GET `/reports/insights?property_id=&from=&to=&block_id=&fund=` (`period=` means one month) | Staff dashboard business view, following the dashboard's filters (at most 12 months; a block or fund narrows every figure it applies to, a fund leaves work orders and contracts as they are): `from`, `to`, `months` (the 12 months to `to`: billed, collected, collection_rate, work_opened, work_closed, contracts_signed, sales_value), `kpis` (billed, collected and collection rate over the range, each with `previous` (the same number of months just before) and `last_year`; outstanding, days_sales_outstanding, occupancy_pct, available_for_sale, open_work_orders, avg_resolve_hours_90d now), `forecast` (12 months after `to`), `forecast_basis`, `sales` (pace over the 6 months to `to`), `revenue_mix`, `blocks` (`billed` over the range, owing now) and `work_by_category` (requests opened in the range) | `reports.view` |
| GET `/reports/catalog` | Every report grouped by module (`modules: [{module, display_name, reports: [{id, name, description, module, source, filters, formats, export_path, needs_property}]}]`), only for the modules the tenant runs and reports the caller may see. Built from one registry (`internal/modules/reports/registry.go`) that also drives the tables and the downloads, the same pattern as TruLoad's report catalog | `reports.view` |
| GET `/reports/{id}/table?from=&to=&property_id=` | A table report from the registry (`columns`, `rows`, `totals`, `note`) over a month range (default the last 12, at most 36): `collections_month`, `collections_block`, `collections_charge` (billed by charge), `payment_channels` (M-Pesa is collected less verified manual payments; manual ones by method), `maintenance`, `vendors`, `security`, `water` (one property). One grouped statement each on the read replica, cached like the dashboard | `reports.view` |
| GET `/reports/{id}/export?format=pdf|xlsx|csv&from=&to=&property_id=&block_id=&fund=&q=&min=` | Any report in the registry through the one branded docs engine (`docs.Export`): table reports with a chart and a total row, `arrears` and `performance` in their own layouts. `/reports/arrears/export`, `/reports/insights/export` and `/water-balance/export` are aliases of this | `reports.view` and `reports.export` |
| GET `/reports/arrears?property_id=&q=&min=` | Owing accounts, largest first (keyset); `q` matches the account reference prefix or the owner's name, `min` the smallest balance; phones masked without `billing.collect` | `reports.view` |
| GET `/reports/arrears/export?format=&property_id=&q=&min=` | Every matching owing account (up to 10,000, the document says when cut) with the ageing chart; same phone masking | `reports.view` and `reports.export` |
| GET `/reports/insights/export?format=&property_id=&from=&to=&block_id=&fund=` | The performance report for the dashboard's filters: KPI cards with the change on the months before, 12-month collected chart and table, cash-in forecast with its method, revenue by charge, blocks, maintenance by category, occupancy and sales | `reports.view` and `reports.export` |

All exports render on `github.com/Bengo-Hub/reports`, send `Cache-Control: private, no-store`, and share a limit of 20 per user per minute.
| GET `/reports/collections`, `/instalment-receivables`, `/maintenance`, `/vendor-scorecard`, `/security`; CSV and PDF export | | planned (sprint 5) |

The dashboard reads through the read-only database and is cached for 60 seconds per tenant, scope
and period (dropped on every pod when a payment is applied or a billing run progresses). Response:
`period`, `billed`, `collected`, `collection_rate`, `outstanding`, `accounts_owing`,
`arrears_60_accounts`, `arrears_60_amount` (the 61 to 90 and 90+ buckets of `arrears_ageing`, which ages each balance by its oldest unpaid bill; the arrears report uses the same `reports.Over60`), `open_work_orders`, `past_sla`, `water_loss_pct`
(with a property only), `vendors_due_for_renewal`, `units`, `occupied`, `units_sold`, `sales_value`,
`sales_collected`, plus

- `collections_by_week: [{week_start: "YYYY-MM-DD", billed, collected}]`, one row per week (weeks start Monday) touching the period;
- `arrears_ageing: [{bucket: "0-30"|"31-60"|"61-90"|"90+", accounts, amount}]`, always four rows. Age is the days since the oldest unpaid due date, laying each balance against the newest bills first.

Money values are decimal strings. Without `property_id` a property-limited user gets figures for
their properties only.

## Privacy and export

| Method and path | Purpose | Permission |
|---|---|---|
| GET `/privacy/requests` (keyset; `status`, `overdue=true`, `party_id`); POST `/privacy/requests` `{party_id, kind, notes}` | Data subject requests under the Kenya Data Protection Act: `kind` is `access`, `correction` or `deletion`; the deadline from receipt is 7 days for access and 14 for the others. One open request per kind per person. An office request starts `received`; the estate contact gets a `privacy.requested` email | `privacy.manage` |
| POST `/privacy/requests/{id}/send-code`; POST `/privacy/requests/{id}/verify` `{code}` or `{in_person: true, note}` | Identity: a 6 digit code to the phone and email on record (never a number the caller gives), valid 5 minutes, 5 tries per code and 5 codes per request; or an in-person check with what was seen. The code hash never leaves the API | `privacy.manage` |
| POST `/privacy/requests/{id}/complete` `{text}`; POST `/privacy/requests/{id}/reject` `{text}` | Close an access or correction request with what was done, or any request with the reason | `privacy.manage` |
| GET `/privacy/requests/{id}/export` | The person's data as a zip built on demand (no stored copy): `data.json`, a CSV per section (units, accounts, vehicles, visitor passes, requests, bill queries, privacy requests) and a readme. Identity numbers and visitors' phones masked; up to 500 rows a section | `privacy.manage` |
| POST `/privacy/requests/{id}/anonymise` | Completes an erasure: refused while the person has an active unit link or open reservation. Clears name (becomes "Former resident" and a short reference), phone, email, identity numbers, address, consents, notes, custom fields and portal access; removes their vehicles, cancels live passes, takes the name and phone off their accounts and off their privacy requests. Bills, payments, contracts and account history stay | `privacy.manage` |
| GET `/me/privacy-requests`; POST `/me/privacy-requests` `{kind, notes}`; GET `/me/privacy-requests/{id}/export` | The resident's own requests, verified by the signed-in session and made for each of their records at the estate; the export downloads once the office completes an access request | portal |

Verify, export, erasure and reject are in the audit allowlist (the export is the one read recorded).

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

## Media visibility
Estate and unit photos (`kind=properties` and `kind=units`) are published images: `/media/...` serves
them without a signature, with immutable caching, and the market projection returns them as absolute
links. Every other kind (documents, readings, works, incidents, vendors, evidence) needs a signed
link from `POST /media/sign` and is never cached by shared caches.

## Late payment charge
Off unless an estate switches it on (`late_charge.enabled`). The hourly job `maskani:late-charges`
looks at each owing account in the chosen funds once a month, from `day`. The charge is `percent`
of the overdue amount plus `fixed`, at most `cap` when set. The overdue amount is the unpaid,
uncredited part of bills more than `grace_days` past due, and leaves out earlier late charges
(treasury metadata `kind: late_charge`), so the charge never compounds. Each charge is a treasury
invoice with reference type `maskani_late_charge` and a reference derived from the account and the
month, so a retry finds the same invoice. It shows on the statement as "Late payment charge, Month".

## Central approvals
One engine (`internal/modules/approvals`), the same shape as treasury-api and inventory-api: rules per
module and amount band with ordered role steps; submitting an object opens one `approval_requests`
row whose `actions` hold each step's decision; approvers act on the current step in order; any
rejection ends it. Each step is claimed with a compare-and-set on the request's status and current
step. Maskani adds: the submitter never decides, one person decides at most one step, steps may name
a permission (the built-in defaults), and requests carry the property for scoped inboxes
(`current_approver` makes "waiting for me" a SQL filter). A waiting step that names a role publishes `approval.pending` to the people who hold it; permission steps are announced by the workflow's own alert.

| Module | Object | Default without a rule | On approval | On rejection |
|---|---|---|---|---|
| `credit_note`, `adjustment` | adjustment | one step, `billing.approve` | treasury credit note raised, adjustment applied | adjustment rejected with the reason |
| `manual_payment` | manual payment | one step, `billing.verify` | booked in treasury, account balance refreshed | closed with the reason |
| `work_order_quote` | work order | one step, `works.manage` | work order approved, work can start | quote rejected, back to the assignee |
| `restructure` | instalment schedule (the proposed version) | one step, `sales.manage` | the plan waits for the buyer (`sale_contract.restructure_proposed`); it takes effect when they accept | the version is marked rejected; the current plan stays |
| `vendor_bill`, `refund`, `deposit_deduction`, `remittance`, `write_off` | as those flows land | none yet | | |

Not on the engine by design: the billing schedule's "run without missing readings" and document
template publishing are operational sign-offs with no amount; each records who acted.
