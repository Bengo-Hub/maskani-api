# Backlog and known gaps

The 2026-10-09 audit compared the SRDD, every sprint checklist and the code. Each gap is listed as a
`[ ]` item in its sprint file under "Gaps found by the 2026-10-09 audit"; this page is the summary
and the record of deliberate deferrals. The plan that closes them is
`.claude/plans/maskani-r1-completion-r2-rentals-2026-10-09.md`.

## Gap summary by wave

| Wave | Area | Sprint files |
|---|---|---|
| 1a | Security: property scope on every write route, role escalation, occupant account access, media upload and signing, PII in event payloads and stored errors, consumer idempotency | S1 to S5 |
| 1b | Performance: N+1 loops in sales, billing, gate, notices, portal, market and imports; unbounded lists; missing indexes; resumable billing, import and notice work; `daily_stats` correctness and nightly rebuild; replica and cache for reports; module switched off becomes read only; shared library bumps | S1 to S5 |
| 2 | Remaining Release 1 features: documents and exports, arrears ladder, reports suite, audit log and plan limits, privacy, adjustments and bill queries, sales completion, occupancy rules, providers and vendor invoices, ERP staff, security operations, budgets and AGM, custom fields, marketplace leftovers, Swagger | S1 to S5 |
| 3 | Hardening: RLS, partitions, cross-tenant and configuration tests, load script, confirmed live tests | S6 |
| 4 | Release 2 rentals, portfolios and short stays | S7 |

## Schema built but not yet used (2026-10-09)

ServiceSchedule, ServiceVisit, MaintenanceSchedule, GuardPost, Roster, PatrolCheckpoint, PatrolScan,
OccurrenceEntry, CustomFieldDef, ReminderSchedule, PrivacyRequest, Handover, TitleStage, Portfolio,
AuditLog. (Adjustment, BillQuery and ApprovalRule went live 2026-10-10; the document tables with
wave 2.1.) ApprovalRule has no settings screen yet, so every credit needs one `billing.approve`
approval until rules are added (wave 2.11). VendorContract is only counted on the dashboard; Vehicle has no list
or delete; `daily_stats` fills 2 of its 19 metrics. Each is picked up by a wave 2 or wave 4 item.

## Deliberate deferrals

| Item | Reason | Owner |
|---|---|---|
| Treasury instalment plans linked to sale contracts | `InstallmentPlan` exists in treasury only as tables. Maskani owns the schedule and raises one invoice per due instalment, which already works; wiring the treasury module adds nothing for R1 | After R2 |
| PayHero offline paybill | Hidden fleet-wide until a live payment on the documented template is seen to settle (`payhero.offline_paybill_verified`) | When verified |
| `pg_trgm` index on party names | Only once party counts justify it | When needed |
| Field encryption key rotation | Single key at launch | S6 |
| Channel sync with Airbnb and Booking.com | Short stays run on pos-api's hotel engine from R2; channel sync is a separate integration | R4 |

## Decisions recorded here

- **Credit notes on unit accounts (2026-10-10):** treasury credit notes inherit the bill's
  `account_ref`; the account ledger nets them off (`total_credited`, per bill `amount_credited`) and
  the payment allocator skips them and sees partial credits. Only the unpaid part of a bill can be
  credited; money already paid needs a refund, which is not built. Credit note lines carry no VAT,
  so a VAT-bearing charge credited this way under-reverses output VAT; estate service charges are
  untaxed today.
- **Public media (2026-10-10):** estate and unit photos (`kind=properties`, `kind=units`) are served
  without a signature and cached immutably, so the public site and next/image can use them; every
  other kind stays signed. Keys stay unguessable.

- **Vendor bills:** treasury has no S2S vendor-bill create, but it already raises bills from events
  (`arpa/service_delivery_bill_subscriber.go`, `POBillSubscriber`). Maskani publishes
  `maskani.vendor_invoice.approved` and treasury gets a subscriber of the same shape. No new S2S
  write route.
- **Row-level security:** the Ent tenant guard (query interceptor and write hook) is the R1 control.
  RLS needs a `SET LOCAL app.tenant_id` wrapper inside every transaction because PgBouncer runs in
  transaction mode. It ships in wave 3 behind a flag, staging first.
- **Partitions:** the migrate binary's live diff drops indexes and objects that Ent does not
  declare, so partitions need an Atlas exclusion and a rotation job before they ship (wave 3).
- **Guard and vendor PINs stay on bcrypt** (2026-10-09), not the shared Argon2id password-hasher.
  Switching would invalidate every PIN already set, or keep two verify paths. A 4 to 6 digit PIN is
  protected by the per-device and per-IP sign-on limits, not by the hash cost, and Argon2id's
  64 MB per verify is heavy on the gate path.
- **Phone normalisation** goes through httpware `contact.NormalizePhone` (each country's own rules)
  but keeps maskani's stored form, digits with the country code and no plus, because phone hashes
  and treasury customer keys were built on it (`secure.NormalizePhone`, pinned by its test).
- **Short stays:** supported from R2 through pos-api's hotel module (user decision 2026-10-09);
  Maskani builds no booking engine.
