# maskani-api events

Envelope and transport follow `shared-events`: subject `{aggregate_type}.{event_type}` with
aggregate type `maskani`, `tenant_id` on the envelope, outbox row in the same transaction as the
domain write, JetStream stream `maskani` (`maskani.>`). Consumers read `tenant_id` from the
envelope first and are idempotent on event ID.

## Published

| Subject | When | Payload highlights | Consumers |
|---|---|---|---|
| `maskani.unit_account.created` | Account opened for a unit and fund | account_id, unit_code, account_ref, fund, party phone | treasury (route registration fallback) |
| `maskani.billing_run.completed` | Run finished issuing | run_id, property, period, fund, issued, failed, total | maskani dashboard refresh, notifications (finance summary) |
| `maskani.bill.issued` | One run line issued | account_ref, amount, due_date, invoice_number, party phone, paybill | notifications: bill SMS or WhatsApp |
| `maskani.payment.applied` | Payment reflected on an account | account_ref, amount, receipt, balance | notifications: receipt |
| `maskani.instalment.due` | Reminder offsets (3 days before, due, 7 and 14 after) | contract, seq, amount, due_date | notifications |
| `maskani.sale_contract.activated`, `.defaulted`, `.fully_paid` | Contract state changes | contract, unit, buyer | notifications, dashboard |
| `maskani.unit.handed_over` | Handover completed | unit, owner, date | estate billing start, welcome message |
| `maskani.work_order.created`, `.assigned`, `.completed`, `.sla_breached` | Works lifecycle | number, priority, unit, assignee, due | notifications to requester, assignee, manager |
| `maskani.visitor.arrived` | Entry logged against a pass or walk-in | pass, host, unit, visitor name | notifications to host |
| `maskani.walk_in.requested` | Walk-in at the gate | request id, host phone, visitor | notifications to host with approve buttons |
| `maskani.incident.reported` | Incident recorded | number, category, severity | notifications to manager and agency supervisor |
| `maskani.vendor.document_expiring` | 30, 14, 7 days before expiry | vendor, doc type, expires_at | notifications |
| `maskani.notice.published` | Notice sent | notice, audience size, priority | notifications fan-out |
| `maskani.document.executed` | Document fully signed | document, entity, key dates | key-date reminders |
| `maskani.lease.*` (R2), `maskani.listing.*`, `maskani.enquiry.created` (R3) | Later releases | | |

## Consumed

| Subject | Publisher | Action |
|---|---|---|
| `treasury.payment.succeeded` | treasury | `reference_type` in (`account_payment`, `invoice`) and `source_service = maskani`: refresh `unit_accounts.balance` from treasury, update instalment and contract paid totals, timeline entry, publish `maskani.payment.applied` |
| `treasury.payment.failed` | treasury | Portal shows the failure; no state change |
| `invoice.payment` | treasury | Invoice-level paid amounts for run lines and instalments |
| `treasury.payout.completed` | treasury | Mark vendor invoice paid (sprint 4 remainder) |
| `auth.tenant.created` | auth | Tenant projection, default settings when the tenant subscribes to Maskani |
| `auth.outlet.created`, `.updated`, `.archived` | auth | Outlet projection used by properties |
| `auth.user.created`, `.updated`, `.deleted` | auth | User projection |
| `auth.apikey.changed` (broadcast) | auth | Drop cached API key hashes |
| `subscriptions.tenant.subscription.updated` | subscriptions | Invalidate the tenant module cache |

Durable consumer names are `maskani-{purpose}` with deliver group `maskani-workers`, `AckExplicit`,
`MaxDeliver 5`, `DeliverNew` at first start.
