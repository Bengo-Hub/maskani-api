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
| `maskani.billing.readings_missing` | Billing schedule: meters unread before the billing day (`stage: reminder`) or on it with policy wait (`stage: waiting`), at most once a day | property_id, property, period, fund, billing_date, missing_count, missing_units (first 10), mode, missing_readings, responders (caretakers and managers; finance too when waiting) | notifications to responders or the estate contact |
| `maskani.billing.run_ready` | Billing schedule in remind mode: the run can start (once per period) | property_id, property, period, fund, billing_date, responders (finance and managers) | notifications to responders or the estate contact |
| `maskani.arrears.reminder` | Collections ladder reminder (days 1, 7, 14 by default, `arrears_steps` in settings) | account_id, account_ref, name, phone, email, balance, days_overdue, step_day, paybill, unit_code, property | notifications to the owner (WhatsApp and email) |
| `maskani.arrears.demand_letter` | Ladder day 45: a demand letter document was issued | the reminder fields plus document_id, document_number | notifications to the owner with the letter |
| `maskani.arrears.escalated` | Ladder day 60 | the reminder fields plus responders (finance and manager) | staff alert |
| `maskani.instalment.due` | Instalment reminder 3 days before, on the day, 7 and 14 days late | instalment_id, contract_number, seq, amount, outstanding, due_date, days_to_due, account_ref, name, phone, email, paybill | notifications to the buyer |
| `maskani.bill.issued` | One run line issued | account_ref, unit_code, period, invoice_number, invoice_date, due_date, `items` (description, amount, plus quantity and rate only for rated charges, tax only for taxed charges), subtotal, tax_total, amount (total), paybill, fund, fund_name, name, phone, email | notifications: itemised bill by email and WhatsApp (untaxed, VAT or no-paybill template) |
| `maskani.payment.applied` | Payment reflected on an account | account_ref, amount, receipt, balance, method, fund, name, phone, email | notifications: receipt by email and WhatsApp |
| `maskani.manual_payment.submitted` | A bank, cash, cheque or typed M-Pesa payment waits for review (not sent per line on a bank statement import) | manual_payment_id, account_ref, unit_code, amount, method, reference, submitted_by, property, responders (managers and caretakers) | staff alert (email and push) |
| `maskani.adjustment.requested` | A credit note or waiver waits for approval | adjustment_id, account_ref, unit_code, kind, amount, reason, invoice_number, requested_by, property, responders (finance and managers) | staff alert |
| `maskani.adjustment.applied` | The treasury credit note was raised | adjustment_id, account_ref, kind, amount, credit_note_number | notifications (optional) |
| `maskani.bill_query.raised` | A resident queried a bill | bill_query_id, account_ref, unit_code, subject, invoice_number, raised_by, due_by, property, responders (finance and managers) | staff alert |
| `maskani.bill_query.answered` | Finance resolved or rejected a query | bill_query_id, account_ref, subject, status, resolution, invoice_number, name, phone, email | notifications to the resident |
| `maskani.payment_plan.broken` | An agreed payment plan fell behind (an instalment more than 3 days late) | account_ref, unit_code, name, total, paid, balance, property, responders (finance and managers) | staff alert |
| `maskani.sale_contract.activated`, `.defaulted`, `.fully_paid` | Contract state changes | contract, unit, buyer | notifications, dashboard |
| `maskani.unit.handed_over` | Handover completed | unit, owner, date | estate billing start, welcome message |
| `maskani.work_order.created`, `.assigned`, `.completed`, `.sla_breached` | Works lifecycle | number, priority, unit, assignee, due | notifications to requester, assignee, manager |
| `maskani.work_order.created` with `source: resident` | A resident request from the portal | adds `unit_code`, `requested_by`, `category`, `description` (280 characters), `property`, `responders` `[{user_id, name, email, phone, role}]`: up to 10 active staff assigned to the property as caretaker or property manager, plus security for a security request | notifications-api sends each responder an email, the `maskani_resident_request_v1_btn` WhatsApp and a push that opens the work order; with no responders, the estate contact |
| `maskani.pass.created` | Visitor pass issued | pass_id, visitor_name, visitor_phone, code, valid_from, valid_to | notifications: gate code to the visitor on WhatsApp |
| `maskani.party.invited` | Portal invitation | party_id, name, phone, email, tenant_slug, portal_url | notifications: invite by email and WhatsApp (Open Portal button) |
| `maskani.visitor.arrived` | Entry logged against a pass or walk-in | event_id, visitor_name, unit_code, host_name, host_phone, host_email, vehicle_plate | notifications to host |
| `maskani.walk_in.requested` | Walk-in at the gate | event_id, visitor_name, unit_code, host_name, host_phone, host_email | notifications to host, "Allow or Decline" button to `/portal/walk-ins/{event_id}` |
| `maskani.incident.reported` | Incident recorded | incident_id, number, category, severity, title, urgent | notifications to the tenant contact when urgent |
| `maskani.vendor.document_expiring` | 30, 14, 7 days before expiry | vendor_id, vendor, doc_type, expires_at, days_left | notifications: email to the tenant contact |
| `maskani.notice.published` | Notice sent | notice, audience size, priority | notifications fan-out |
| `maskani.document.executed` | Document fully signed | document, entity, key dates | key-date reminders |
| `maskani.vendor_invoice.approved` | Manager confirms a vendor invoice (wave 2.8) | invoice_id, treasury_vendor_id, amount (net of service credit), etims_number, cost_center, budget_line, fund, wht | treasury: new arpa subscriber raises the vendor bill, idempotent on invoice_id |
| `maskani.arrears.step` | Arrears ladder step reached (wave 2.2) | account_ref, unit_code, step (reminder_1, reminder_7, reminder_14, call_list, demand_letter, escalation), balance, days_overdue | notifications: reminders by email and WhatsApp |
| `maskani.link.ended` | An occupancy or ownership link reached its end date (wave 2.7) | unit, party, role, end_date | gate pass revocation, final reading request |
| `maskani.lease.*` (R2), `maskani.listing.*`, `maskani.enquiry.created`, `maskani.tender.*` (R3) | Later releases | | |

Payload rule (2026-10-09 audit): events carry ids, display names and only the contact the receiving
channel needs. `maskani.pass.created` must carry the plain code and visitor phone because
notifications sends the code to the visitor and keeps no maskani data, so the code exists in the
outbox row until it is published. Wave 1a keeps that window short (published outbox rows for
`maskani.pass.*` are pruned on publish, not on the normal schedule) and drops every field the
template does not use. As of
2026-10-09 `maskani.instalment.due`, `.sale_contract.defaulted` and `.unit.handed_over` are declared
but never published; waves 2.2 and 2.6 wire them.

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

## Realtime (live screens)

Separate from the outbox events above. After its database commit a service publishes a small hint
through `internal/platform/realtime` (an interface, so no domain service imports NATS; a nil
publisher is a no-op). The hub is the shared-events `FanoutHub` over a `Broadcaster` on core NATS,
subject `_rt.maskani.events.<tenant>._` (the platform realtime-fanout standard). The `_rt.` prefix is
outside the `maskani` JetStream stream, so nothing is persisted: every pod hears every message and
delivers it to its own SSE connections at `GET /api/v1/{tenant}/maskani/stream`.

Payload (the SSE `event:` field is `type`):

```json
{"type": "payment.applied", "id": "<uuid>", "property_id": "<uuid>", "unit_id": "<uuid>"}
```

| Type | `id` | Published by |
|---|---|---|
| `billing_run.progress` | billing run | billing: run created, after each batch of 500 lines, on finish, on retry |
| `payment.applied` | unit account | payment consumer, after the balance refresh and the outbox `maskani.payment.applied` |
| `work_order.updated` | work order | works: create and every action |
| `gate.event` | gate event | gate: entry, exit or denial stored |
| `walk_in.requested` | gate event | gate: walk-in request stored |
| `walk_in.decided` | gate event | gate: host decision |
| `reading.saved` | meter reading | utilities: record, estimate, verify |
| `notice.status` | notice | notices: scheduled, sending, sent or failed |

Filtering happens per connection: staff see their properties (all properties for admins, and events
with no property), portal users only events whose `unit_id` is one of their active unit links.
Hints carry ids only; clients refetch through the permission-checked endpoints and resync after a
reconnect, because a pod that is reconnecting to NATS misses messages.

`payment.applied` and `billing_run.progress` also drop each pod's cached dashboard figures for the
tenant (`reports.Invalidate` subscribed to the same relay).
