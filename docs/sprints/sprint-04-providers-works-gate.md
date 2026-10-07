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
- [ ] Schedules and visits, preventive maintenance, vendor invoices (after demo)
- [ ] maskani-ui works screens and gate tablet app

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
