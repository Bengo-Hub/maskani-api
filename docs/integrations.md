# maskani-api integrations

All service-to-service calls send `X-API-Key: ${INTERNAL_SERVICE_KEY}` plus `X-Tenant-ID` (an API key
alone never identifies a tenant). Base URLs come from config and default to in-cluster DNS in k8s.
Every outbound write carries an `Idempotency-Key`.

| Variable | In-cluster value |
|---|---|
| `AUTH_SERVICE_URL` | `https://sso.codevertexafrica.com` (JWKS and OIDC, same as hospital-api) |
| `AUTH_API_URL` | `http://auth-api.auth.svc.cluster.local:4000` (S2S member and role calls) |
| `TREASURY_SERVICE_URL` | `http://treasury-api.treasury.svc.cluster.local:4000` |
| `ERP_SERVICE_URL` | `http://erp-api.erp.svc.cluster.local:80` |
| `NOTIFICATIONS_SERVICE_URL` | `http://notifications-api.notifications.svc.cluster.local:4000` |
| `SUBSCRIPTION_BASE_URL` | `http://subscription-api.subscriptions.svc.cluster.local:4000` |

Hostnames and ports were checked against `devops-k8s/apps/*/values.yaml` on 2026-10-07 (erp-api's
service listens on 80, the others on 4000). Re-check there before changing them.

## auth-api

| Use | Call |
|---|---|
| Staff sign-in | SSO (OIDC + PKCE), client `maskani-ui`; JWT validated through JWKS |
| Owner, occupant, vendor supervisor sign-in | `POST /api/v1/auth/phone/otp/request` then `/verify` with `{tenant_slug, phone, client_id}`; returns the standard token pair (new in this release) |
| Create or link a customer user | `POST /api/v1/s2s/tenants/{tenant_id}/members` with phone, name, role; returns the real user ID stored on `parties.auth_user_id` |
| Property as branch | Properties are auth-api outlets: `POST /api/v1/tenants/{slug}/outlets` on create; local projection kept by `auth.outlet.created/updated/archived` |
| Roles | Push the maskani role catalogue to `POST /api/v1/s2s/roles/sync` (idempotent) |
| Tenant lookup | `GET /api/v1/tenants/by-slug/{slug}`, `/by-id/{id}` through the tenant syncer |
| Events consumed | `auth.tenant.created`, `auth.outlet.*`, `auth.user.*`, `auth.apikey.changed` (broadcast) |

Phone OTP rules: codes hashed, 5 minute expiry, rate limited per phone and per IP, delivered on
WhatsApp through notifications-api (`auth_otp` template). Built in auth-api dd2080c. Only a phone already linked to a tenant member can
request a code, so the endpoint cannot be used to enumerate or create accounts.

## treasury-api

### Invoices

`POST /api/v1/s2s/{tenant}/invoices`

```json
{
  "customer_name": "Jane Wanjiru",
  "customer_phone": "254712345678",
  "invoice_type": "standard",
  "invoice_date": "2026-11-01T00:00:00Z",
  "due_date": "2026-11-10T00:00:00Z",
  "currency": "KES",
  "reference_type": "maskani_bill",
  "reference_id": "<billing run line id>",
  "settlement_account_id": "<fund bank account id>",
  "lines": [{ "description": "Service charge, 3 bedroom", "item_sku": "SERVICE_CHARGE", "item_type": "service", "quantity": 1, "unit_price": 4500 }],
  "metadata": { "account_ref": "B07", "unit_account_id": "<unit account id>", "fund": "estate", "period": "2026-11", "source_service": "maskani" }
}
```

- Reference types: `maskani_bill` (run line id), `maskani_instalment` (instalment id). The account
  link is `metadata.account_ref`, which treasury's allocator and account ledger filter on (GIN index).
- `treasury.Client.IssueInvoice` creates then sends. treasury creates S2S invoices as drafts, and only
  `POST /invoices/{id}/send` moves one to `sent`, posts it to the GL, projects AR and makes it
  eligible for allocation. `treasury.invoice_sent` is also emitted on send.
- Idempotency: a retry first checks `GET /api/v1/s2s/{tenant}/invoices/by-reference` and sends only a
  draft, so a retried line never issues or posts twice. maskani stores `invoice_id` on the run line or
  instalment.
- Credit notes: `POST /invoices/{id}/create-credit-note` (behind maskani approval rules).

### Payments

`POST /api/v1/s2s/{tenant}/payments/intents`

```json
{
  "reference_type": "account_payment",
  "reference_id": "MSK-PAY-<account id prefix>-<attempt key>",
  "payment_method": "mpesa",
  "amount": 6450,
  "currency": "KES",
  "phone_number": "254712345678",
  "source_service": "maskani",
  "outlet_id": "<property outlet id>",
  "idempotency_key": "<same as reference_id>",
  "metadata": { "account_ref": "B07", "unit_account_id": "<unit account id>", "entity_id": "<unit account id>", "fund": "estate" }
}
```

- treasury returns the existing intent for a known `reference_id` whatever its status, so every pay
  attempt has its own reference. The client sends one `idempotency_key` per attempt (a double tap
  reuses the intent, a retry after a cancelled prompt starts a new one); with none, the API makes one.

- The gateway is resolved by treasury from the tenant (or property outlet) configuration: Daraja,
  PayHero (`mpesa`, `payhero_momo`, card, bank) or Paystack. `gateway` may pin one when several
  serve the rail. The owner portal reads `GET /pay/{tenant}/gateways` and uses the shared
  `TreasuryPaymentModal`, so the choices match the tenant's real configuration.
- One invoice can also be paid with `reference_type: "invoice"` and the invoice public token.

### Paybill (C2B) account routes (new)

Registered by maskani whenever a unit account is created or its reference changes:

`POST /api/v1/s2s/{tenant}/c2b/account-routes`

```json
{ "account_ref": "B07", "shortcode": "<estate paybill>", "source_service": "maskani",
  "reference_type": "account_payment", "reference_id": "<unit_account_id>", "fund": "estate" }
```

On a Daraja confirmation treasury normalises `BillRefNumber` with `payments.AccountMatchKey` (upper
case, spaces and dashes removed, leading zeros dropped, so `b 07`, `B-07` and `B7` are one key),
matches a route only on the route tenant's own shortcodes, books a succeeded `account_payment` intent
(`reference_id = "C2B-" + TransID`, provider `mpesa_c2b`) and marks the inbox row claimed. A key that
matches two tenants on one paybill, or no route, stays `unreconciled`; maskani's suspense queue reads
it with `GET /api/v1/s2s/{tenant}/c2b/payments?status=unreconciled` and finance assigns it with
`POST /c2b/payments/{trans_id}/claim` (`reference_type`, `reference_id`, `account_ref`).

### Allocation

treasury's invoicing subscriber allocates every `account_payment` intent, paybill or STK alike:
oldest due first across the account's open invoices (`metadata.account_ref`), each through
`SettleInvoiceFromGatewayPayment` with a sub-intent derived from the payment and invoice ids, so a
redelivery never settles twice. The rest is kept on the intent as `metadata.unapplied_amount`. Applying
that credit to a later invoice is not built yet (treasury backlog).

### Statements and balances

- Statement per account: `GET /api/v1/s2s/{tenant}/accounts/{account_ref}/ledger` returns invoices,
  payments, billed and paid totals, held credit and balance. maskani never recomputes ledger figures.
- `treasury.payment.succeeded` (account resolved from `metadata.unit_account_id`, then `entity_id`)
  refreshes maskani's cached `unit_accounts.balance` and purchase progress and publishes
  `maskani.payment.applied`; the cached figure is display only.

### Deferred (after the demo)

- Treasury instalment plans wired to sale contracts (today maskani owns the schedule and raises one
  invoice per due instalment).
- S2S vendor bills with withholding tax (today vendor invoices are recorded through `POST /expenses`).

## erp-api

| Use | Call |
|---|---|
| Staff picker for work orders and service checks | `GET /api/v1/hrm/employees` with `X-API-Key` and `X-Tenant-ID` |
| Casual worker payments | Recorded in ERP casual payments; maskani stores the employee ID on the work order cost line |

## notifications-api

`POST /api/v1/{tenant}/notifications/messages`

Used directly only for notices to an audience. Everything else is event driven.

- Active channels are **email and WhatsApp** (no SMS). notifications-api's maskani consumer
  (`cmd/worker/maskani_consumer.go`, durable `notifications-maskani`) sends each mapped event in
  [events.md](events.md) by email when the payload has an address (owners' and buyers' email is
  kept in `unit_accounts.metadata.customer_email`) and by WhatsApp with approved `maskani_*`
  UTILITY templates.
- WhatsApp links are always URL buttons on `https://maskaniapp.codevertexafrica.com/{{1}}` with
  the suffix `<slug>/<path>`, never body text. Deep links: `portal`, `portal/purchase`,
  `portal/walk-ins/{id}`, `security/incidents/{id}`, `works/{id}`, `vendors/{id}` (maskani-ui must
  serve these; see its UX spec).
- Bills are itemised. Optional lines show only when present: quantity x rate for rated charges,
  VAT per taxed charge, subtotal and VAT rows only on a taxed bill, the paybill block only when the
  fund has one. WhatsApp uses one of three templates for that reason.
- Phone sign-in codes go on WhatsApp (`auth_otp`, platform number), so tenant plans never block
  sign-in.
- Quiet hours 21:00 to 07:00 for routine notices; emergency alerts bypass them.

## subscriptions-api

- Product `maskani`, plans `MASKANI_STARTER`, `MASKANI_GROWTH`, `MASKANI_PROFESSIONAL`,
  `MASKANI_ENTERPRISE`, service tag `maskani`.
- Seeded in subscriptions-api 641e99c (`cmd/seed/plans_maskani.go`): 10,000, 20,000, 35,000 and
  quote. Each tier also includes the ERP and treasury subscription at that tier.
- Module codes (service tag `maskani`): `maskani_properties`, `maskani_billing`,
  `maskani_utilities`, `maskani_sales`, `maskani_estate`, `maskani_maintenance`,
  `maskani_providers`, `maskani_gate`, `maskani_staff`, `maskani_communication`,
  `maskani_leasing`, `maskani_commercial`, `maskani_portfolios` (every tier),
  `maskani_marketplace` (Growth up).
- Tier capabilities reuse existing codes: `budgeting`, `approval_workflows`, `asset_management`,
  `bi_reports` (Growth up); `api_access`, `custom_workflows`, `audit_trail`, `priority_support`
  (Professional up).
- Limits: `max_units` (150, 400, 1,000, unlimited), `max_employees` (ERP staff 25, 75, 200),
  `max_users` (staff users 10, 30, 100). Portal users never count as staff.
- Runtime: features arrive in the JWT `sub_features` claim; limit checks call
  `GET /api/v1/tenants/{id}/subscription` (cached). A lapsed subscription keeps data readable and
  blocks new records.

## marketflow-api and maps (R3)

Enquiries become CRM leads with the source listing; geocoding and map search use the Codevertex maps
service. Not wired in R1.
