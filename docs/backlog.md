# Backlog and known gaps

Items deliberately left out of the demo slice, with the reason and the sprint that owns them.

| Item | Reason deferred | Owner sprint |
|---|---|---|
| PostgreSQL row-level security on every table | SRDD FR-01 and NFR-01 promise RLS. No fleet service uses it, and with PgBouncer transaction pooling it needs `SET LOCAL app.tenant_id` inside every transaction plus a wrapper on every Ent tx. The Ent tenant guard (query interceptor and write hook) is the R1 control; RLS is added once the wrapper is proven on staging | S6 |
| Treasury instalment plans linked to sale contracts | `InstallmentPlan` and `Installment` exist in treasury only as tables. maskani owns the schedule and raises one treasury invoice per due instalment until the treasury module is wired | S3 remainder |
| S2S vendor bills with withholding tax | Treasury has no S2S vendor-bill create; vendor invoices are recorded through S2S `POST /expenses` meanwhile | S4 remainder |
| Monthly partitions and BRIN indexes for `gate_events` and `patrol_scans` | Ent does not manage partitions, and the migrate binary's live diff (`WithDropIndex`) drops indexes not declared in Ent, so a hand-written BRIN would be removed on the next deploy. Needs an Ent-declared index or a migrate exclusion plus a partition rotation job | S6 |
| PayHero offline paybill | Hidden fleet-wide until a live payment on the documented template is seen to settle (`payhero.offline_paybill_verified`) | When verified |
| Vendor portal invoices with SLA credits and coverage report | Needs vendor bills S2S first | S4 remainder |
| Guard rosters and post coverage reports | Schema in place; UI and roster upload after the demo | S4 remainder |
| Budgets against actual, AGM pack | Budgets live in treasury; report wiring after the demo | S5 |
| ERP casual payments linked to work order cost lines | erp-api link after the demo | S5 |
| Privacy export and deletion jobs | Schema in place; jobs in S5 | S5 |
| Document templates with merge fields and PDF generation | Starter templates and generation in S5; demo uses treasury invoice PDFs | S5 |
| Daily paybill reconciliation report | Treasury already reconciles; maskani report in S5 | S5 |
| Field encryption key rotation | Single key at launch | S6 |
| Load test (1,000 unit billing run, 50 rps) and restore drill | Hardening week | S6 |
| `pg_trgm` index on party names | Only once party counts justify it | When needed |
