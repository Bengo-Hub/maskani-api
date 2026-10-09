# Maskani plan

Source: SRDD CVX-MASKANI-SRDD-001 v1.0, 6 October 2026 (private docs repo,
`codevertex-private-docs/shaba-village-proposal/`). This file is the engineering plan derived from it.

## Product

Maskani is one platform serving three kinds of business, each an isolated tenant:

1. **Estate operators and developers** (launch tenant Shaba Village, Syokimau): sell units outright
   or by instalments, then run the estate and bill owners for water, service charge and services.
2. **Property management firms** (Release 2): let and manage buildings for many landlords.
3. **Landlords, developers and agents** (Release 2 and 3): manage their own portfolios or advertise.

Maskani Marketplace (repo `maskani-commerce`, host `maskani.codevertexafrica.com`) publishes verified
vacancies and units for sale in Release 3. A small estate showcase ships with the demo.

### Who the platform connects

Maskani is meant to be the one place where every side of a Kenyan property deal meets. Market
research behind this list is in [docs/market-research.md](docs/market-research.md).

| Person or business | What they come for | Release |
|---|---|---|
| Estate operator or developer | Sell units outright or by instalments, then run the estate and bill owners | R1 |
| Unit owner | Pay estate charges, follow a purchase, manage household and visitors; choose to let the unit long term or run it as a short stay | R1, R2 |
| Service provider (security, cleaning, garbage, maintenance) | Contracts, schedules, evidence, invoices paid on delivered service | R1 |
| Landlord | Let or lease units directly, or hand them to a manager under a mandate | R2 |
| Property management firm (for example Lockwood) | Manage portfolios for landlords, collect rent, remit net of fees; bid for new mandates | R2, R3 |
| Short-stay host | Run a unit as a short stay, with bookings in pos-api's hotel engine and income shown in Maskani | R2 |
| House hunter, buyer, diaspora investor | Find real, verified homes and land; enquire, view, apply or offer safely | R3 |
| Office, shop or co-working seeker | Find workspace by the hour, day, month or on a lease | R3 |
| Agent or land seller | List verified property and land, receive masked enquiries as leads | R3 |
| Property owner seeking a manager | Post a management tender and compare bids side by side | R3 |

## Releases

| Release | Scope | Timing |
|---|---|---|
| R1 MVP | Multi-tenant core, register, staff per property, estate billing, utilities, sales and instalments, owner portal, providers, works, gate, ERP staff, reports | Shaba launch, February 2027 |
| R2 | Landlord portfolios and mandates, applications, leases, deposits, inspections, turnover, remittances, commercial leases, short stays through pos-api's hotel engine | Q2 2027 |
| R3 | Public marketplace: listings (homes, offices and co-working, shops, warehouses, land) for sale, rent, lease and short stay; search and map; enquiries, viewings, applications; verified listers; featured listings; management tenders | Q3 2027 |
| R4 | Native apps, smart meters, e-signatures, credit checks, owner voting, amenity booking, channel sync with Airbnb and Booking.com | Later |

Excluded from every release: conveyancing and legal advice, mortgage origination, Codevertex holding
client money. The SRDD also excluded short-stay booking; the user changed that on 2026-10-09: short
stays are supported in R2, but the booking engine stays in pos-api and Maskani never builds a second
one.

## Current plan

`.claude/plans/maskani-r1-completion-r2-rentals-2026-10-09.md` closes every Release 1 gap found by
the 2026-10-09 audit, revamps queries and jobs for growth, then builds Release 2. Gaps are listed in
each sprint file under "Gaps found by the 2026-10-09 audit" and summarised in
[docs/backlog.md](docs/backlog.md).

## Demo MVP slice (Shaba Village, Sunday 11 October 2026)

Integrated testing by Friday 9 October. The slice pulls the core of sprints 1 to 4 forward:

| Area | In the demo |
|---|---|
| Tenancy | Tenant sync, staff SSO, RBAC, module gating, tenant guard |
| Register | Properties, blocks, units, owners and occupants, staff assignment, CSV import with dry run |
| Billing | Charge catalogue (seeded and custom), dated rates, unit accounts per fund, billing run to treasury invoices |
| Utilities | Meters, reading round with photo, anomaly flags, water balance |
| Collections | Portal STK (Daraja, PayHero or Paystack per tenant gateway), paybill by unit code, suspense queue, statements |
| Sales | Price list, reservation, sale contract, instalment schedule, purchase statement |
| Works | Requests, work orders with SLA, vendors with document expiry |
| Gate | Visitor passes (QR and 6-digit code), gate verify, entry log, offline sync |
| Communication | Notices to estate or block over SMS |
| Owner portal | Phone OTP sign-in, balance per fund, pay, purchase plan, passes, requests |
| Reports | Dashboard tiles, collections, arrears ageing, sales position, water balance |
| Marketplace | Estate showcase of units for sale with an enquiry form |

## Cross-service work this slice needs

| Service | Change |
|---|---|
| auth-api | Customer phone OTP sign-in returning the standard token pair; OAuth client `maskani-ui` |
| treasury-api | C2B account routes and one `account_payment` allocator used by paybill and portal STK |
| subscriptions-api | `plans_maskani.go`: Starter, Growth, Professional, Enterprise; feature catalog codes |
| notifications-api | Subscribe to `maskani.>`; SMS templates for bills, receipts, instalments, passes, work orders |
| shared-ui-lib | App switcher entry with service tag `maskani` |
| devops-k8s | Apps, namespace, pgbouncer, secrets, network policies, Cloudflare hosts, health checks |

## Sprint map

| Sprint | Scope | Weeks (SRDD) |
|---|---|---|
| [S0](docs/sprints/sprint-00-bootstrap.md) | Bootstrap, devops, docs, platform registration | 1 |
| [S1](docs/sprints/sprint-01-register-parties-portal.md) | Register, parties, staff, import, owner OTP portal | 2 to 3 |
| [S2](docs/sprints/sprint-02-billing-utilities-collections.md) | Charges, meters, billing runs, collections, statements, reminders | 4 to 5 |
| [S3](docs/sprints/sprint-03-sales-instalments.md) | Price lists, reservations, contracts, instalments, handover, title | 6 to 7 |
| [S4](docs/sprints/sprint-04-providers-works-gate.md) | Vendors, contracts, visits, work orders, gate, patrols, incidents | 8 to 10 |
| [S5](docs/sprints/sprint-05-reports-erp-documents.md) | Reports, budgets, ERP link, notices, documents, privacy | 11 to 12 |
| [S6](docs/sprints/sprint-06-hardening-launch.md) | RLS, partitions, load test, restore drill, parallel run, UAT, launch | 13 to 14 |
| [S7](docs/sprints/sprint-07-r2-rentals.md) | Release 2 rentals and portfolios | Q2 2027 |
| [S8](docs/sprints/sprint-08-r3-marketplace.md) | Release 3 marketplace, R4 notes | Q3 2027 |

## Commercial tiers (subscriptions-api)

| Tier | Units | ERP staff | Staff users | KES per month |
|---|---|---|---|---|
| Starter | 150 | 25 | 10 | 10,000 |
| Growth | 400 | 75 | 30 | 20,000 |
| Professional | 1,000 | 200 | 100 | 35,000 |
| Enterprise | Unlimited | Unlimited | Unlimited | By quote |

Owners, occupants, guards and vendor supervisors are never counted as staff users. Starter has
single-level approvals; budgets against actual, asset register and BI reports start at Growth; API
access and extended audit at Professional.
