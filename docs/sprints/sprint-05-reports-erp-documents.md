# Sprint 5: reports, budgets, ERP link, notices, documents, privacy

SRDD weeks 11 to 12, milestone 3 (feature complete). Requirements FR-46 to FR-49, FR-63 to FR-67.

## Scope

| Item | Detail | Demo slice |
|---|---|---|
| Dashboard | Collections this month, arrears over 60 days, open work orders and past SLA, water loss, vendors due for renewal | Yes (early) |
| Reports | Collections summary, arrears ageing, payment channels, owner statement, sales position, instalment receivables, handover and title tracker, water consumption and balance, maintenance performance, vendor scorecard, security summary; CSV and PDF | Collections, arrears, sales position, water balance |
| Daily aggregates | `daily_stats` updated by events, rebuilt nightly | Yes |
| Budgets | Budget against actual and income and expenditure from treasury budgets and ledger (Growth tier) | After demo |
| ERP | Staff picker, work orders assignable to ERP staff, casual payments linked to cost lines | Picker only |
| Notices | Audience by estate, block, owners, occupants; channels; quiet hours; emergency bypass; delivery status | Yes |
| Documents | Template library with merge fields and versions, approval, PDF generation with reference and verification code, OTP acceptance, wet-signed upload, key-date reminders, access log | After demo |
| Privacy | Data subject requests with deadlines, export, deletion with statutory retention | After demo |
| Audit | Financial, contract and access changes in `audit_logs` | Yes |

## Progress

API state as of 2026-10-08.

- [x] Dashboard: `/reports/dashboard`
- [x] Arrears, sales position and water balance reports
- [x] Dashboard `collections_by_week` and `arrears_ageing` in grouped SQL on the read-only database, bounded 60 second cache dropped on every pod by payments and runs, property scope (2026-10-08)
- [x] Keyset arrears (largest first), notices and enquiries; `notice.status` live hint (2026-10-08)
- [ ] Remaining reports, CSV and PDF exports
- [x] Daily collections aggregate updated by the payment consumer
- [ ] Nightly `daily_stats` rebuild
- [x] Notices: create, send, scheduled job, deliveries, portal list
- [x] Notices delivered by WhatsApp and email only, one-line WhatsApp parameter, urgent subject for emergencies (bddeb41, notifications 0cbe513)
- [x] Notices handed to notifications-api as approved service-notice broadcasts: `maskani_residents` audience paged from `GET /api/v1/internal/residents/reach`, completion consumer updates counts; direct send kept as fallback (`9593fb5`, notifications `2a0d2c8`)
- [x] Rich text sanitised on save (bluemonday), plain text for WhatsApp and the marketplace (`9593fb5`)
- [ ] ERP staff picker
- [ ] Audit log writes (schema exists)
- [ ] Budgets, documents, privacy (after demo)
- [ ] maskani-ui reports and notices screens

### Gaps found by the 2026-10-09 audit
Plan: `.claude/plans/maskani-r1-completion-r2-rentals-2026-10-09.md` (wave in brackets).

- [ ] Correction: the dashboard ageing summary exists, but there is no standalone arrears ageing report with 0 to 30, 31 to 60, 61 to 90 and over 90 bands and last payment date (SRDD 19, wave 2.3)
- [x] `daily_stats`: collections written in the consumer transaction on the payment's day (`15375db`); nightly rebuild of every other metric for every tenant in one statement each (`bf20a0d`)
- [x] `weeklySQL` compares `daily_stats.day` directly (`bf20a0d`)
- [x] Sales position cached 60 seconds (`bf20a0d`)
- [ ] Reports suite: collections by month, charge type and block; payment channel; 24-month instalment receivables; handover and title tracker; water trend; maintenance performance; vendor scorecard; security summary; every figure links to its records (FR-66, wave 2.3)
- [ ] Exports `?format=pdf|csv|xlsx` on every report through the copied docs engine (FR-66, wave 2.1)
- [x] Notice direct-send fallback pages recipients in 500s and resumes a send a stopped pod left half done (`b8be252`); deliveries list paging still open
- [x] Property scope on notice create (estate-wide needs every property), send and deliveries and on enquiry update (`15375db`)
- [x] Broadcast completion applied once with its notice.published event (`15375db`)
- [ ] `AuditLog` written by one helper from money, contract, configuration, role and access changes (FR-67, wave 2.4)
- [ ] Privacy requests with OTP identity, deadline, export and anonymisation, statutory records kept; retention purge jobs (FR-67, NFR-10, wave 2.4)
- [ ] Documents: templates with merge fields, versions and approval; generation with reference and verification code; OTP acceptance; wet-signed upload; access log; public verify route (FR-46 to FR-49, wave 2.1)
- [ ] Budgets against actual, income and expenditure and the sinking fund statement read from treasury budgets, ledger and cost-centre S2S; AGM pack (FR-65, wave 2.10)
- [ ] ERP client: no client exists yet although `ERP_URL` is configured (FR-63, wave 2.8)
- [ ] Swagger annotations: `/v1/docs/` is a route table today, not Swagger UI (wave 2.13)

## Rules to apply in this sprint

Standing backend rules in [README.md](README.md), plus:

| Scenario | Rule | Memory file |
|---|---|---|
| Report queries | SQL aggregates, read replica, 60 second cache; totals reconcile to treasury because invoices and payments are read from it | `treasury-receivables-credit-notes-dashboard-sql-stats-2026-09-26.md` |
| PDF documents | Reuse the platform document engine pattern; check the shared render bugs before copying | `feedback_document_generation_architecture.md`, global `reference_docs_engine_shared_across_services.md` |
| Exports | Branded export through the established report module pattern, not ad hoc CSV | `treasury-customer-statement-datatable-pagination-export-2026-09-12.md` |
| Backups and tenant export | Tenant-scoped export pattern | `feedback_tenant_scoped_backups.md` |
| Notices | Templates per channel; WhatsApp buttons; messaging plans never block sign-in codes or receipts | `feedback_notification_policies.md` |
