# Sprint 7: Release 2, rental and portfolio management

Development March to May 2027, general availability June 2027. Pilot: one property management firm
and one self-managing landlord. Requirements FR-05, FR-16, FR-17, FR-36 to FR-45, FR-59.

## Scope

| Item | Detail |
|---|---|
| Self-service sign-up | Tenant type selection, trial, guided set-up wizard asking only what enabled modules need |
| Landlord clients and mandates | `portfolios` of kind `client` with `mandates`: services, fee basis, letting and renewal fees, expense approval limit, remittance account (OTP verified), remittance day, float, withholding agent flag |
| Landlord portal | Occupancy, rent roll, arrears, expenses, statements, remittances, approvals above limit, documents |
| Applications | Consent, identity, income evidence, references, next of kin; manager and landlord approval |
| Leases | From templates: parties, unit, term, rent, escalation, deposit, charges, utilities, notice periods, special conditions; OTP acceptance or wet signature; registration and stamp duty flags for leases over two years |
| Deposits | Invoiced into a deposit liability fund; never counted in remittances; itemised deductions on move-out; refund through treasury under approval |
| Inspections | Move-in and move-out, room by room, photos, readings, keys, signatures |
| Rent | Invoices on the lease schedule with utilities and service charge; proration; per-lease paybill reference (`L-` prefix routes through the same C2B account routes) |
| Reviews and renewals | Escalations with notice letters; reminders at 90, 60, 30 days |
| Commercial leases | VAT on rent, service charge recovery, rent-free periods, lessee withholding with certificate capture |
| Turnover | Projects with repaint, repair, cleaning tasks as work orders; landlord approval above the mandate limit; ready date drives listing availability |
| Remittances | Rent collected less fees, approved expenses and withholding (MRI 7.5% resident, 30% non-resident) paid by treasury on the mandate day with a statement |

All of these attach to existing `units`, `parties`, `unit_accounts`, `work_orders` and `documents`.

## Rules to apply

Standing backend rules in [README.md](README.md), plus the payment, gateway and fund rules of
[sprint 2](sprint-02-billing-utilities-collections.md). Client money for landlords is a separate fund
(`client_rent`) with its own ledger accounts; the manager's own income never mixes with it.
