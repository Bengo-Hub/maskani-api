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
