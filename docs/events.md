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
| `maskani.bill.issued` | One run line issued | account_ref, unit_code, period, invoice_number, invoice_date, due_date, `items` (description, amount, plus quantity and rate only for rated charges, tax only for taxed charges), subtotal, tax_total, amount (total), paybill, fund, fund_name, name, phone, email | notifications: itemised bill by email and WhatsApp (untaxed, VAT or no-paybill template) |
| `maskani.payment.applied` | Payment reflected on an account | account_ref, amount, receipt, balance, method, fund, name, phone, email | notifications: receipt by email and WhatsApp |
| `maskani.instalment.due` | Reminder offsets (3 days before, due, 7 and 14 after) | contract, seq, amount, due_date | notifications |
| `maskani.sale_contract.activated`, `.defaulted`, `.fully_paid` | Contract state changes | contract, unit, buyer | notifications, dashboard |
| `maskani.unit.handed_over` | Handover completed | unit, owner, date | estate billing start, welcome message |
| `maskani.work_order.created`, `.assigned`, `.completed`, `.sla_breached` | Works lifecycle | number, priority, unit, assignee, due | notifications to requester, assignee, manager |
| `maskani.pass.created` | Visitor pass issued | pass_id, visitor_name, visitor_phone, code, valid_from, valid_to | notifications: gate code to the visitor on WhatsApp |
| `maskani.party.invited` | Portal invitation | party_id, name, phone, email, tenant_slug, portal_url | notifications: invite by email and WhatsApp (Open Portal button) |
| `maskani.visitor.arrived` | Entry logged against a pass or walk-in | event_id, visitor_name, unit_code, host_name, host_phone, host_email, vehicle_plate | notifications to host |
| `maskani.walk_in.requested` | Walk-in at the gate | event_id, visitor_name, unit_code, host_name, host_phone, host_email | notifications to host, "Allow or Decline" button to `/portal/walk-ins/{event_id}` |
| `maskani.incident.reported` | Incident recorded | incident_id, number, category, severity, title, urgent | notifications to the tenant contact when urgent |
| `maskani.vendor.document_expiring` | 30, 14, 7 days before expiry | vendor_id, vendor, doc_type, expires_at, days_left | notifications: email to the tenant contact |
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
