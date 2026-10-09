# Sprint 3: unit sales and instalments

SRDD weeks 6 to 7, milestone 2 (end-to-end demonstration with live paybill payments).
Requirements FR-20 to FR-25.

## Scope

| Item | Detail | Demo slice |
|---|---|---|
| Price lists | By unit type and phase with effective dates; reservation fee, deposit percentage, maximum term | Yes |
| Availability | Units with sale status and current price | Yes |
| Reservations | Fee invoice (sales fund), expiry, automatic release job | Yes |
| Sale contracts | Buyers and shares, price, discount, net price, reservation credit, deposit, payment option, term, advocates, signed agreement upload | Yes |
| Schedules | Outright, monthly, quarterly, milestone; last instalment absorbs rounding; versioned | Yes |
| Instalment invoicing | Daily job raises a treasury invoice per instalment due within the lead window (sales fund, `S-` account) | Yes |
| Payments | Applied oldest due first by treasury; overpayment reduces the next instalment unless early completion is requested | Yes (treasury allocator) |
| Reminders and default | 3 days before, due date, 7 and 14 after; default after the agreement grace period; sales officer alerted | Reminders only |
| Restructure | New schedule under approval, original kept, buyer acceptance | After demo |
| Statements | Purchase statement and completion statement | Purchase statement |
| Handover | Checklist, snag list, meter readings, keys, signatures; estate billing starts prorated | After demo |
| Title tracking | Stages with dates and documents until release | After demo |

## Worked check (SRDD 9.2)

Price 7,500,000; reservation fee 100,000 credited to deposit; deposit 20% = 1,500,000 including the
fee; balance 6,000,000 over 24 monthly instalments of 250,000. Unit test.

## Progress

API state as of 2026-10-08. Screens are in maskani-ui sprint 03 (not started).

- [x] Price lists
- [x] Availability
- [x] Reservations: fee invoice and the `maskani:reservation-expiry` job
- [x] Sale contracts: create, get, activate
- [x] Schedules with the SRDD 9.2 test
- [x] Instalment invoicing: hourly job, invoices sent through `IssueInvoice`
- [x] Payments allocated oldest first by treasury
- [ ] Overpayment applied to the next instalment (treasury backlog: held credit)
- [ ] Reminders and default
- [x] Purchase position: `/me/purchase`, `/reports/sales-position`
- [x] Reservations list `GET /reservations`; keyset sale contracts; property scope on price lists, contracts and sales position (2026-10-08)
- [ ] Purchase and completion statement PDF
- [ ] Restructure, handover, title tracking (after demo)
- [x] maskani-ui sales screens

### Gaps found by the 2026-10-09 audit
Plan: `.claude/plans/maskani-r1-completion-r2-rentals-2026-10-09.md` (wave in brackets).

- [x] Property scope on Reserve, CreateContract, GetContract, ActivateContract, ReleaseMilestone and AssignSuspense (FR-13, `15375db`)
- [x] Instalment invoicing loads contracts, accounts and funds for the batch in three queries, each invoice's instalment and contract total commit together; schedules written with `CreateBulk`; reservation expiry in set-based batches (NFR-05, `b8be252`)
- [x] Progress sync reads all instalments in one query and keeps the mirrored amount for instalments older than the 500-invoice ledger page instead of resetting them to zero (bug fix, `b8be252`)
- [x] Indexes `sale_contracts(tenant_id, unit_account_id)` and `instalments(tenant_id, treasury_invoice_id)` (`3ae57a5`)
- [x] Portal purchase plans in one instalment query (`b8be252`)
- [ ] Instalment reminders 3 days before, on the due date and 7 and 14 days after, publishing `maskani.instalment.due` (FR-24, wave 2.2)
- [ ] Default after the agreement grace period: status `in_default`, `maskani.sale_contract.defaulted`, sales officer alerted with history (FR-24, wave 2.2)
- [ ] Overpayment reduces the next instalment, read from the ledger's unapplied amount, unless the buyer asks for early completion (SRDD 9.3, wave 2.6)
- [ ] Restructure as a new schedule version under approval, original kept, buyer acceptance by OTP (FR-23, wave 2.6)
- [ ] Handover with snag list, meter readings, keys and signatures; publishes `maskani.unit.handed_over` and starts estate billing prorated (FR-25, wave 2.6)
- [ ] Title stages with dates and documents (FR-25, wave 2.6)
- [ ] Purchase and completion statements as PDF and spreadsheet (FR-25, FR-35, wave 2.1)
- [ ] Console price list editor, reservations list and milestone release (wave 2.6)

## Acceptance

- A lapsed reservation releases the unit to available within 15 minutes.
- The purchase statement total always equals price less discount; the last instalment carries any
  rounding difference.
- The unit returns to available only after a termination refund is recorded.

## Rules to apply in this sprint

Standing backend rules in [README.md](README.md), plus:

| Scenario | Rule | Memory file |
|---|---|---|
| Instalment invoices | One invoice per instalment, idempotent on (schedule, seq); `invoice_id` stored before marking invoiced | `boi-treasury-duplicate-receipts-incident-2026-09-14.md` |
| Fund segregation | Sales collections only on the sales paybill and `S-` reference; never settle estate charges | SRDD 8.4 |
| Late charges | Only where the agreement provides; never compounded | SRDD 9.3 |
| Treasury instalment plans | Not wired yet; maskani owns the schedule (see backlog) | `docs/backlog.md` |
| Personal data | Buyer ID and KRA PIN encrypted, visible to the sales role only | NFR-09 |
