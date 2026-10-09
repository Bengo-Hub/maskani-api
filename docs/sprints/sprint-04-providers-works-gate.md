# Sprint 4: vendors, works and gate

SRDD weeks 8 to 10. Requirements FR-51 to FR-58, FR-60 to FR-62.

## Scope

| Item | Detail | Demo slice |
|---|---|---|
| Vendors | Linked to treasury vendors; categories; KRA PIN; locked payment details | Yes |
| Vendor documents | Licences (PSRA, NEMA, PCPB, EPRA, NCA, permits, insurance) with expiry alerts at 30, 14 and 7 days | Yes |
| Contracts | Scope, fee basis, SLA, service credits, renewal | Basic |
| Schedules and visits | Recurring visits, check-in and out with photos, missed visit flag | After demo |
| Personnel | Agency personnel register with badges; same-day deactivation | Yes |
| Work orders | Requests from residents and staff; triage, priority SLA (emergency 1h/4h, high 4h/24h, normal 24h/72h, low 3d/14d); vendor or ERP staff assignee; quotes; photos; resident confirm or reopen within 7 days; recharge to unit | Yes |
| Preventive maintenance | Schedules for shared assets generating work orders | After demo |
| Visitor passes | Guest single and recurring, domestic staff, delivery, contractor, agency; QR and 6-digit code (hashed), windows, one-time use | Yes |
| Gate tablet API | Device registration, verify, events, walk-in approval with 5 minute timeout, offline sync batch with 24 hour pass cache | Yes |
| Patrols and posts | Checkpoints, scans, posts, rosters, coverage | Checkpoints and scans |
| Incidents | Categories, severity, photos, immediate alerts for serious incidents; occurrence book | Yes |
| Vendor invoices | Through treasury `POST /expenses` until S2S vendor bills exist | After demo |

## Progress

API state as of 2026-10-08. Screens and the gate tablet app are in maskani-ui sprint 04 (not started).

- [x] Vendors: list, create
- [ ] Vendor link to treasury vendors
- [x] Vendor documents with the daily `maskani:vendor-doc-expiry` alert job
- [x] Personnel per vendor
- [ ] Vendor contracts endpoints (schema exists)
- [x] Work orders: create, actions, SLA breach job, resident requests in the portal
- [x] Visitor passes: staff and portal, hashed codes
- [x] Gate tablet API: verify, events, offline sync cache, walk-in decide, offline device job
- [ ] Patrols and posts endpoints (schema exists)
- [x] Incidents: staff and device reporting
- [x] Vendor detail `GET /vendors/{id}`, `has_pin` on personnel, vendor routes gated on the `providers` module (2026-10-08)
- [x] Guard PIN: `PUT /vendors/{id}/personnel/{pid}/pin`, tablet `POST /gate/sign-on` (rate limited), named badges in `/gate/sync` (2026-10-08)
- [x] Keyset passes and incidents with property scope; live hints `work_order.updated`, `gate.event`, `walk_in.requested`, `walk_in.decided` (2026-10-08)
- [ ] Schedules and visits, preventive maintenance, vendor invoices (after demo)
- [x] maskani-ui works screens and gate tablet app

### Gaps found by the 2026-10-09 audit
Plan: `.claude/plans/maskani-r1-completion-r2-rentals-2026-10-09.md` (wave in brackets).

- [x] Property scope on ActWorkOrder, staff ReportIncident, ListMeters and reading writes (FR-13, `15375db`)
- [x] Pass payloads: `pass.created` must keep the code and visitor phone (notifications sends them and holds no maskani data); the outbox, which nothing pruned before, now drops published pass events after 15 minutes and other published rows after 7 days (`15375db`)
- [x] Per-device rate limits on `/gate/verify` and `/gate/events` (`15375db`)
- [x] Gate event batches: one bulk upsert, one update per distinct pass, arrival lookups by id set; device `last_seen_at` written at most once a minute (NFR-04, `b8be252`)
- [x] `/gate/sync` filters personnel in SQL by deployment property (`b8be252`)
- [x] Portal pass cancel is one conditional update on id and host (`b8be252`)
- [ ] Gate log list order and index agree (`created_at` sort has no index today) (wave 1b)
- [ ] Partial indexes for system jobs: open SLA work orders by due time, devices by `last_seen_at` (wave 1b)
- [ ] Vendor linked to a treasury vendor by S2S lookup, not free text (FR-51, wave 2.8)
- [ ] Vendor contracts with SLA, fee basis, service credits and renewal (FR-52, wave 2.8)
- [ ] Service schedules and visits with photos and a missed-visit job; preventive maintenance schedules raising work orders (FR-53, FR-58, wave 2.8)
- [ ] Vendor invoices: entered in the vendor portal with the eTIMS number, compared with contract, visits and coverage, confirmed by the manager, then `maskani.vendor_invoice.approved` makes treasury raise the bill through a new subscriber modelled on `arpa/service_delivery_bill_subscriber.go`; `treasury.payout.completed` marks it paid (FR-56, wave 2.8)
- [ ] Vendor supervisor phone sign-in and vendor portal (FR-18, wave 2.8)
- [ ] ERP staff picker through an erp-api S2S employee list; casual payments linked to the work order cost line (FR-63, wave 2.8)
- [ ] Guard posts, rosters uploaded by the agency, sign-on and sign-off checked against the roster, unmanned time report (FR-54, wave 2.9)
- [ ] Patrol checkpoints with QR tags scanned on the tablet, missed checkpoints in the morning summary (FR-54, wave 2.9)
- [ ] Digital occurrence book (FR-62, wave 2.9)
- [ ] Gate device list and revoke (wave 2.9)
- [x] Guard and vendor PINs: decided to stay on bcrypt (reasons in `docs/backlog.md`, 2026-10-09)

## Acceptance

- Gate verify answers within 1 second; an offline entry queued on the tablet syncs once, with no
  duplicate after repeated uploads.
- A pass code is never stored in plain text; a used one-time pass is rejected.
- An SLA breach raises an alert to the estate manager.
- Gate collects only name, phone, host unit, times and plate; no ID photos.

## Rules to apply in this sprint

Standing backend rules in [README.md](README.md), plus:

| Scenario | Rule | Memory file |
|---|---|---|
| Gate events growth | BRIN plus composite index, 90 day purge in batches, partitions in S6 | `docs/data-performance.md` |
| SLA timers and expiry alerts | One job per fleet per period through `ClaimPeriod`; skip tenants without the module | `multi-pod-scaling-ratelimit-realtime-media-2026-09-29.md` |
| Realtime host notifications | Cross-pod fan-out through `events.Broadcaster` / `FanoutHub`, not per-pod memory | `multi-pod-scaling-ratelimit-realtime-media-2026-09-29.md` |
| Photos | Stored under `tenants/{id}/...`, served only through signed short-lived URLs | `multi-pod-scaling-ratelimit-realtime-media-2026-09-29.md` |
| Staff from ERP | Reference employee IDs; never copy staff records | `feedback_service_data_ownership.md` |
| WhatsApp approvals | Buttons, never raw links | `feedback_whatsapp_links_as_buttons.md` |
