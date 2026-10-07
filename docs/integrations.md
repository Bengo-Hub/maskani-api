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

Phone OTP rules: codes hashed, 5 minute expiry, rate limited per phone and per IP, delivered by SMS
through notifications-api with WhatsApp fallback. Only a phone already linked to a tenant member can
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
  "reference_type": "maskani_unit_account",
  "reference_id": "<unit_account_id>",
  "outlet_id": "<property outlet id>",
  "settlement_account_id": "<fund bank account id>",
  "lines": [{ "description": "Service charge, 3 bedroom", "quantity": 1, "unit_price": 4500, "tax_rate": 0 }],
  "metadata": { "fund": "estate", "unit_code": "B07", "period": "2026-11", "billing_run_id": "...", "source_service": "maskani" }
}
```

- Idempotency: maskani stores `invoice_id` on the run line or instalment; a retry first checks
  `GET /api/v1/s2s/{tenant}/invoices/by-reference` before creating.
- PDF and pay link: `GET /invoices/{id}/pdf-url`; send with `POST /invoices/{id}/send`.
- Credit notes: `POST /invoices/{id}/create-credit-note` (behind maskani approval rules).

### Payments

`POST /api/v1/s2s/{tenant}/payments/intents`

```json
{
  "reference_type": "account_payment",
  "reference_id": "<unit_account_id>",
  "payment_method": "mpesa",
  "amount": 6450,
  "currency": "KES",
  "phone_number": "254712345678",
  "source_service": "maskani",
  "outlet_id": "<property outlet id>",
  "idempotency_key": "MSK-...",
  "metadata": { "fund": "estate", "unit_code": "B07", "entity_id": "<unit_account_id>" }
}
```

- The gateway is resolved by treasury from the tenant (or property outlet) configuration: Daraja,
  PayHero (`mpesa`, `payhero_momo`, card, bank) or Paystack. `gateway` may pin one when several
  serve the rail. The owner portal reads `GET /pay/{tenant}/gateways` and uses the shared
  `TreasuryPaymentModal`, so the choices match the tenant's real configuration.
- One invoice can also be paid with `reference_type: "invoice"` and the invoice public token.
- Payment references follow `payref`: `MSK-{SLUG6}-{ENTITY12}`.

### Paybill (C2B) account routes (new)

Registered by maskani whenever a unit account is created or its reference changes:

`POST /api/v1/s2s/{tenant}/c2b/account-routes`

```json
{ "account_ref": "B07", "shortcode": "<estate paybill>", "source_service": "maskani",
  "reference_type": "account_payment", "reference_id": "<unit_account_id>", "fund": "estate" }
```

On a Daraja confirmation treasury normalises `BillRefNumber` (upper case, spaces and dashes
removed, leading zeros of the numeric part normalised so `b 07`, `B-07` and `B7` resolve to `B07`),
looks the route up within the tenant's own shortcodes, creates and settles an `account_payment`
intent through `settleIntent` (provider `mpesa_c2b`), and marks the inbox row claimed. An unresolved
reference stays `unreconciled`; maskani's suspense queue reads it with
`GET /api/v1/s2s/{tenant}/c2b/payments?status=unreconciled` and finance assigns it manually.

### Allocation

`account_payment` intents are settled by one treasury hook: oldest due first across the account's
open invoices (`reference_type = maskani_unit_account`, `reference_id` = account), applied through
the invoice payment path that locks the row and is idempotent on the intent. Late payment charges
settle last. Any surplus is held as customer credit and applied to the next invoice.

### Statements and balances

- Statement per account: maskani lists the account's invoices and payments from treasury
  (`/invoices?reference_type=...&reference_id=...` and payment transactions) and shows them; it never
  recomputes ledger figures.
- `treasury.payment.succeeded` updates maskani's cached `unit_accounts.balance` and purchase
  progress; the figure is display only, treasury stays authoritative.

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

```json
{ "channel": "sms", "tenant": "<slug>", "template": "maskani_bill_issued",
  "data": { "unit_code": "B07", "amount": "6,450", "due_date": "10 Nov 2026", "paybill": "...", "account": "B07" },
  "to": ["254712345678"], "metadata": { "source_service": "maskani", "entity_id": "..." } }
```

- Event-driven messages: notifications-api subscribes to `maskani.>` and sends the templated
  messages listed in [events.md](events.md).
- WhatsApp links are always buttons (`_btn` templates, fixed domain, URL suffix as
  `template_button_param`), never raw link text.
- Quiet hours 21:00 to 07:00 for routine notices; emergency alerts bypass them.
- SMS and WhatsApp consume the tenant's messaging credits; OTP and payment receipts are never
  blocked by plan gating.

## subscriptions-api

- Product `maskani`, plans `MASKANI_STARTER`, `MASKANI_GROWTH`, `MASKANI_PROFESSIONAL`,
  `MASKANI_ENTERPRISE`, service tag `maskani`.
- Feature codes (service tag `maskani`): `maskani_properties`, `maskani_billing`,
  `maskani_utilities`, `maskani_sales`, `maskani_estate`, `maskani_maintenance`,
  `maskani_providers`, `maskani_gate`, `maskani_staff`, `maskani_communication`,
  `maskani_budgets`, `maskani_multilevel_approvals`, `maskani_bi_reports`, `maskani_api_access`,
  `maskani_leasing`, `maskani_commercial`, `maskani_portfolios`, `maskani_marketplace`.
- Limits: `max_units`, `max_staff_users`, `max_erp_staff`.
- Runtime: features arrive in the JWT `sub_features` claim; limit checks call
  `GET /api/v1/tenants/{id}/subscription` (cached). A lapsed subscription keeps data readable and
  blocks new records.

## marketflow-api and maps (R3)

Enquiries become CRM leads with the source listing; geocoding and map search use the Codevertex maps
service. Not wired in R1.
