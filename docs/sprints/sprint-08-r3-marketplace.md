# Sprint 8: Release 3 marketplace (and Release 4 notes)

Development June to August 2027, launch September 2027 seeded with managed vacancies and Shaba
resales. Requirements FR-68 to FR-76. UI lives in `maskani-commerce`.

## Scope (maskani-api side)

| Item | Detail |
|---|---|
| Listings | For rent or sale: residential, office, retail, industrial, land; price, size, photos, video link, amenities, geo; publish a vacant managed unit in one step; close automatically when a lease or sale is signed |
| Published projection | A denormalised `listings` read model the public API serves; no route from public endpoints to operational tables |
| Lister verification | Identity by OTP and document check; EARB number for agents; title, search or owner authority for sale listings; verified badge with date |
| Moderation | Duplicate photo detection, price outliers by area, contacts hidden in text, public reports, takedown with reasons |
| Search | Filters, map search with clustered pins and drawn areas (maps service), sort, share links; server-rendered pages with structured data |
| Enquiries and viewings | Masked contacts; lead into marketflow with source listing; viewing slots from the lister's calendar, reminders, attended or missed |
| Applications and offers | Rental applications into the R2 leasing workflow; offers into sales |
| Saved searches and favourites | Alerts by SMS, WhatsApp or email |
| Monetisation | Listing plans for independent listers, featured placement paid through treasury |
| Listing kinds | Sale, rent, lease and short stay (short-stay units link to their pos-api booking widget). Workspace listings (offices and co-working) carry hourly, daily or monthly terms, following LiquidSpace's split between whole workspaces and shared seats |
| Land listings | Plot size, zoning, title type; ArdhiSasa official search evidence (owner-approved search, KES 1,000 in 2026) uploaded and checked before a land or sale listing is verified |
| Management tenders (new, user decision 2026-10-09) | An owner posts a request for management: property, units, services wanted (letting only, rent collection, full management), budget. Verified property-management tenants bid with fee basis, services and references. The owner compares bids side by side (Proplexa and ProVendorConnect pattern). The accepted bid creates the R2 mandate and portfolio in the winning tenant, with the owner as landlord client. Bidders pay a listing plan through treasury |
| Seeker accounts | One phone sign-in for house hunters, office seekers and buyers; marketplace profile separate from any tenant's records, shared with a lister only when the seeker enquires |

The R1 showcase endpoints (`/api/v1/market/estates/{slug}`, `/units/{id}`, `/enquiries`) are the
first version of this projection and are kept compatible.

## Release 4 notes

Native apps, smart meters (readings by API instead of rounds), advanced electronic signatures
through an accredited provider, tenant credit checks, owner voting, amenity booking with fees, an AI
assistant. Each arrives as a module behind the same gating.

## Rules to apply

Standing backend rules in [README.md](README.md). Public endpoints are rate limited per IP and phone
through shared-ratelimit with `TrustedRealIP`, cached at the edge where safe, and never expose a
tenant's operational data or a lister's contact before the seeker chooses to share.
