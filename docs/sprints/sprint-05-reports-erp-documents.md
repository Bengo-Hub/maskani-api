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
- [ ] Remaining reports, CSV and PDF exports
- [x] Daily collections aggregate updated by the payment consumer
- [ ] Nightly `daily_stats` rebuild
- [x] Notices: create, send, scheduled job, deliveries, portal list
- [x] Notices delivered by WhatsApp and email only, one-line WhatsApp parameter, urgent subject for emergencies (bddeb41, notifications 0cbe513)
- [ ] ERP staff picker
- [ ] Audit log writes (schema exists)
- [ ] Budgets, documents, privacy (after demo)
- [ ] maskani-ui reports and notices screens

## Rules to apply in this sprint

Standing backend rules in [README.md](README.md), plus:

| Scenario | Rule | Memory file |
|---|---|---|
| Report queries | SQL aggregates, read replica, 60 second cache; totals reconcile to treasury because invoices and payments are read from it | `treasury-receivables-credit-notes-dashboard-sql-stats-2026-09-26.md` |
| PDF documents | Reuse the platform document engine pattern; check the shared render bugs before copying | `feedback_document_generation_architecture.md`, global `reference_docs_engine_shared_across_services.md` |
| Exports | Branded export through the established report module pattern, not ad hoc CSV | `treasury-customer-statement-datatable-pagination-export-2026-09-12.md` |
| Backups and tenant export | Tenant-scoped export pattern | `feedback_tenant_scoped_backups.md` |
| Notices | Templates per channel; WhatsApp buttons; messaging plans never block sign-in codes or receipts | `feedback_notification_policies.md` |
