# Sprint 2: charges, meters, billing runs, collections

SRDD weeks 4 to 5. Requirements FR-06, FR-26 to FR-35.

## Scope

| Item | Detail | Demo slice |
|---|---|---|
| Funds | Estate and sales funds with treasury bank account, paybill shortcode, account prefix | Yes |
| Charge catalogue | Seeded platform charges (SRDD 4.6), tenant enable, rename, price, add custom; basis, frequency, scope, bill-to, fund, tax, proration, penalty, priority | Yes |
| Rates | Dated rates by tenant, property, unit type or unit; block tariffs for water | Yes |
| Unit accounts | One per unit and fund (`B07`, `S-B07`); C2B route registered in treasury on create | Yes |
| Meters and rounds | Unit, bulk and borehole meters; round in walking order; reading with photo; flags (lower than previous, zero for occupied, over 3x the 3 month average); estimate; replacement | Yes |
| Water balance | Supplied (bulk plus boreholes) vs billed vs common estimate; loss percentage and alert threshold | Yes |
| Billing run | Preview, issue: one treasury invoice per unit and fund with the month's lines; idempotent on (property, fund, period); batches of 500; retry failed lines | Yes |
| Collections | Portal STK via `account_payment` intent; paybill via treasury C2B routes; suspense queue from the treasury inbox and assignment | Yes |
| Payment consumer | `treasury.payment.succeeded` refreshes balances and publishes `maskani.payment.applied` | Yes |
| Statements | Per account and fund from treasury, PDF and CSV | Yes |
| Reminders | Escalating reminders at 1, 7, 14 days; call list at 30; demand letter at 45 | Reminder job only |
| Adjustments and bill queries | Credit notes under approval rules; query queue | After demo |

## Worked check (SRDD 8.2)

Unit B07, three bedroom, October 2026: service charge 4,500; water 1,284 less 1,275 = 9 m3 at 150 =
1,350; garbage 300; sinking fund 300; total 6,450 due 10 November. This is a unit test.

## Progress

API state as of 2026-10-08. Screens are in maskani-ui sprint 02 (not started).

- [x] Funds: list and update (bank account, paybill, prefix)
- [x] Charge catalogue: seeded, enable, create, update
- [x] `GET /charge-types/catalogue` lists standard charges not yet added (2026-10-08)
- [x] Rates: dated rates per charge
- [x] Unit accounts with C2B routes registered by the `maskani:c2b-routes` job
- [x] treasury C2B account routes, allocator and account ledger (treasury e8b5dbc, 059f27a)
- [x] Meters and rounds: readings with flags, estimate, verify
- [x] Water balance
- [x] Billing run: preview, issue, retry; invoices created and sent through `IssueInvoice`
- [x] Collections: STK with per-attempt references, paybill routes, suspense queue and assign
- [x] Payment consumer: account resolved from `metadata.unit_account_id`
- [x] Statements API from the treasury account ledger
- [x] Bill and receipt messages by email and WhatsApp, bills itemised with optional VAT, rate and paybill lines (notifications 5830c60)
- [x] Run detail `GET /billing-runs/{id}` with line counts; keyset runs and unit accounts; property scope on statements, staff pay, runs (2026-10-08)
- [x] Live hints: `billing_run.progress`, `payment.applied`, `reading.saved` (2026-10-08)
- [ ] Statement PDF and CSV
- [ ] Reminder job (1, 7, 14 days)
- [ ] Adjustments and bill queries (after demo)
- [ ] KES 1 live tests on both paybills (user confirms first)
- [x] maskani-ui billing, meters and collections screens (meter photo optional since `fb46c61`)

### Gaps found by the 2026-10-09 audit
Plan: `.claude/plans/maskani-r1-completion-r2-rentals-2026-10-09.md` (wave in brackets).

- [ ] Statements carry a running balance from the API and page through the whole treasury ledger, not the first 50 rows (FR-35, wave 1c and 2.1)
- [ ] Statement export `?format=pdf|csv|xlsx` on the copied fleet docs engine (FR-35, wave 2.1)
- [ ] Arrears ladder driven by `arrears_steps` and `ReminderSchedule`: reminders on days 1, 7 and 14, a late charge only where the charge type allows it (capped, never compounded), a call list at 30, a demand letter at 45, escalation at 60. Reuses treasury dunning if it fits (FR-34, wave 2.2)
- [ ] Payment plans and clearance certificates (FR-34, wave 2.2)
- [ ] Adjustments and credit notes through treasury under `ApprovalRule` thresholds; bill queries with a finance queue and portal submission (FR-29, FR-30, wave 2.5)
- [ ] Meter replacement route with closing and opening readings; batch bulk and borehole readings (FR-27, wave 2.7)
- [ ] Arrears search and minimum balance filtered in SQL, not over loaded pages; suspense total from the API (wave 1c)
- [x] Water balance and arrears read from the replica with a 60 second cache invalidated across pods (`bf20a0d`)
- [x] Billing issue and retry run under a per-run fleet lease, mark themselves alive per batch, and a two-minute job resumes runs a stopped pod left in "issuing" (NFR-06, `bf20a0d`)
- [x] Billing run accounts read once per batch of 500 instead of once per line (`bf20a0d`)
- [x] Partial index on unregistered paybill accounts for the route job (`3ae57a5`); the job still makes one treasury call per account, which the route API requires
- [x] Payment consumer: consumed-event row, daily collection and receipt event in one transaction; collections counted on the payment's own day (NFR-08, `15375db`)
- [x] `billing_run_lines(tenant_id, treasury_invoice_id)` index for the payment consumer lookup (`3ae57a5`)
- [x] Treasury and auth-api errors keep only the service's own message before they are stored or returned (`15375db`)

## Acceptance

- Retrying a billing run never creates a second invoice for a unit and period.
- A paybill payment to `b 07` lands on account `B07` and settles the oldest invoice first.
- A payment to `S-B07` never settles estate invoices, and the reverse.
- An unknown reference appears in suspense with payer name and phone.
- No supply is ever cut off or access denied for arrears; the platform has no such action.

## Rules to apply in this sprint

Standing backend rules in [README.md](README.md), plus:

| Scenario | Rule | Memory file |
|---|---|---|
| Any payment, intent, webhook or allocation | One atomic settle path in treasury, callbacks confirmed by API, payments applied once under a row lock, money columns numeric | `treasury-payment-security-audit-2026-10-02.md` |
| Gateway choice | Tenant-owned gateway resolution (Daraja, PayHero, Paystack); PayHero gated on `mpesa_integration`; offline paybill hidden until verified; gateways fail closed | `pos-payment-bar-gateways-payhero-c2b-room-gate-2026-10-03.md`, `payhero-personal-offbooks-and-platform-billed-2026-10-05.md` |
| Invoice types and AR figures | One invoice-type registry; one "still owed" definition including credit notes | `treasury-receivables-credit-notes-dashboard-sql-stats-2026-09-26.md` |
| Fleet-wide jobs | Keyset pagination with SQL predicates, never an unbounded `.All()`; jobs once per fleet via `ClaimPeriod` | `boi-treasury-pos-recurring-discrepancy-root-audit-2026-09-11.md`, `multi-pod-scaling-ratelimit-realtime-media-2026-09-29.md` |
| Duplicate protection | Anchor idempotency on stable keys (period, receipt, intent), never on a created_at window | `boi-treasury-duplicate-receipts-incident-2026-09-14.md` |
| Messages | Email and WhatsApp (the active channels) through notifications-api; WhatsApp links always buttons; quiet hours | `feedback_whatsapp_links_as_buttons.md`, `feedback_notification_policies.md` |
| Live tests | KES 1 tests on both paybills only after the user confirms | `feedback_confirm_sensitive_commands.md` (global) |
