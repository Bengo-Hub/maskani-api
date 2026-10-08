# Demo guide: Shaba Village on codevertex-demo

codevertex-demo is the one demo tenant for every Codevertex product. Maskani's part of it is the
**Shaba Village** estate (outlet `SHABA`, use case `property`), loaded by `cmd/seed-tenant`.

## What sets it up

| Piece | Where | Runs |
|---|---|---|
| Estate outlet `SHABA` and the estate staff accounts | auth-api `cmd/seed` (`seed_tenants.go`, `seed_users.go`) | On every auth-api pod start |
| Estate data: 40 units, 32 owners and buyers, charges, readings, sales, vendors, work orders, passes, a notice | maskani-api `cmd/seed-tenant` | By hand, once (re-runs add nothing) |
| A real person as the B07 owner, invited to the owner portal | `cmd/seed-tenant` with `SEED_DEMO_RESIDENT_*` | Same run |

Run the estate seed inside the cluster, dry run first. The resident's name, phone and email are
personal, so they are passed for that one run and never committed:

```bash
kubectl exec -n maskani deploy/maskani-api -- /app/seed-tenant --tenant codevertex-demo --dry-run

kubectl exec -n maskani deploy/maskani-api -- env \
  SEED_DEMO_RESIDENT_NAME="<full name>" \
  SEED_DEMO_RESIDENT_PHONE="<07XXXXXXXX>" \
  SEED_DEMO_RESIDENT_EMAIL="<email>" \
  /app/seed-tenant --tenant codevertex-demo
```

The seed marks the messages it raises as delivered, so nobody is texted or emailed about demo
records (`--keep-events` delivers them). The resident invite still creates their auth-api member
with the `maskani_owner` role, added to any roles they already hold.

## Accounts

Staff sign in at `https://maskaniapp.codevertexafrica.com/codevertex-demo`, choose **Estate staff**,
and use SSO. Their password is the demo staff password (`SEED_DEMO_STAFF_PASSWORD`, default in
auth-api `cmd/seed/seed_users.go`); the admin has its own (`seedDemoTenantAdmin`).

| Account | Maskani role | What to show |
|---|---|---|
| `admin@demo.codevertexafrica.com` | Tenant admin | Everything: settings, modules, users, imports, every screen |
| `estate.manager@demo.codevertexafrica.com` | Property manager | Dashboard, register, billing runs, works, notices |
| `estate.accounts@demo.codevertexafrica.com` | Finance officer | Unit accounts, statements, collections, charges and funds |
| `estate.sales@demo.codevertexafrica.com` | Sales officer | Availability, reservations, sale contracts, instalments |
| `estate.caretaker@demo.codevertexafrica.com` | Caretaker | Meter readings round on a phone |
| `estate.security@demo.codevertexafrica.com` | Security manager | Visitor passes, gate log, incidents, gate devices |

Other codevertex-demo staff (POS cashiers, hotel managers, clinic staff) get no Maskani access:
their role names are not property ones. That is intended (see `docs/architecture.md`).

**Owner portal.** Open the estate, choose **I live or own here**, and enter the resident's phone.
The 6-digit code goes to their email when they have one, otherwise WhatsApp; the code screen also
offers **Send it on WhatsApp instead**. Only invited parties receive a code.

**Gate tablet.** A security manager registers the tablet under Security, Devices; guards then sign
on with the PIN set on their vendor personnel record. The tablet keeps working offline.

## Demo walk-through

1. **Admin:** Dashboard (collections by week, arrears ageing), then Settings to show modules.
2. **Register:** Units, filter Block B, open B07 to see its owner, account and meter.
3. **Import:** Units, Import CSV, download the template, check a small file, show the report.
4. **Caretaker (phone):** Meter readings, read two meters (a photo is optional).
5. **Finance:** Billing runs, preview this month, show B07's bill (service charge 4,500, water
   9 m3 at 150 = 1,350, garbage 300, sinking fund 300, total 6,450), approve and send.
6. **Owner (resident's phone):** sign in with the code, see the B07 statement, Pay now (treasury
   pay page; choose M-Pesa, the prompt goes only after choosing), raise a request, create a
   visitor pass and show its code.
7. **Security:** Passes shows the visitor; on the gate tablet enter the code to check them in.
8. **Works:** the owner's request appears as a work order; assign a vendor, close it, and the
   owner confirms in the portal.
9. **Sales:** Availability, the A16 reservation and the A15 and B15 contracts with instalments.
10. **Marketplace:** `https://maskani.codevertexafrica.com` lists Shaba Village once it is published.
