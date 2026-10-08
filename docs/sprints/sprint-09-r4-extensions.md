# Sprint 9: Release 4 extensions (by demand)

From Q4 2027, each item scoped with the client (SRDD 23.4 and figure 1). UI:
maskani-ui `docs/sprints/sprint-09-r4-extensions.md`.

| Item | Detail |
|---|---|
| Smart meters | Ingest readings from meter vendors by API or MQTT bridge into `meter_readings` (`source=smart`); flags for no reading, tamper and leak; estimates only when a meter is silent |
| Electronic signatures | Advanced e-signature provider behind `document_acceptances` (method `e_signature`, already in the schema enum), certificate stored with the document hash |
| Tenant credit checks | Consent-recorded call to a credit bureau from the R2 application; only the summary is stored; retention as the privacy schedule sets |
| Owner voting | Resolutions with options, weighting by unit entitlement, proxies, quorum, sealed results and a results document through the documents module |
| Assistant | Read-only answers over the caller's own records through the existing permission and property scope; no write actions |
| Native app support | Push registration through notifications-api and any API the native wrappers need |

## Progress

- [ ] Not started.

## Rules to apply

Standing backend rules in [README.md](README.md). Smart-meter ingestion is a scheduled or streamed
system job: `ClaimPeriod`/`RunOnce`, batches of 500, idempotent on the vendor reading id.
