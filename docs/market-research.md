# Market research

Research done on 2026-10-09 to check Maskani's scope against products people already use. Each
finding names the release that picks it up. The SRDD's own comparison (section 2.2) covers Kenyan
rent tools, AppFolio, Buildium, Yardi, MRI, gated estate apps and property portals; this file adds
what that table did not cover.

## Kenyan property portals

BuyRentKenya (about 16,000 listings and 311,000 monthly visits) and Property24 (in Kenya since
2013) both offer free listings for sale and rent across houses, apartments, land and commercial
space. BuyRentKenya leads on trust with a "verified agencies" mark and market reports.

What Maskani takes from them (R3):
- A verified badge with the verification date, shown on the listing and the lister profile.
- Free listings for subscribed tenants up to a plan limit; paid plans for independent listers.
- Area pages and market reports built from the published projection, never from operational data.

What Maskani does differently: listings from managed properties are verified at the source, close
automatically when a lease or sale is signed, and route enquiries through masked contacts.

## Rent collection tools in Kenya

Nyumbani (nyumbani.ke) is typical: real-time M-Pesa paybill reconciliation, receipts by SMS or email,
automated reminders on WhatsApp and SMS, a tenant self-service portal, landlord dashboards per
property and unit, and QuickBooks integration.

Maskani already covers paybill matching by unit code, one allocator for every channel, receipts and
a portal (R1). It goes further because the ledger is treasury's own double-entry books, so there is
no export to an outside accounting package. Reminders run on email and WhatsApp, the platform's
active channels.

## Land fraud and title verification

ArdhiSasa is Kenya's official land information system. In 2026 an online search costs KES 1,000,
paid by M-Pesa. The registered owner must approve the search before the result is released. The
result shows current ownership, history, pending transactions, rates status and encumbrances. Some
registries (for example Ngong and Kikuyu) still need a manual search form.

What Maskani takes from this (R3):
- A land or sale listing is verified only after the lister uploads an official search (ArdhiSasa
  result or manual registry search) or the owner's written authority, checked by moderation.
- The listing shows the search date; a search older than the moderation window needs renewing.
- Seekers see the safe-payment rule: never pay before viewing and signing.

## Workspace marketplaces

LiquidSpace connects seekers with coworking spaces, hotels and corporate offices with spare room.
Seekers filter by term (hourly or monthly), neighbourhood, capacity and space type. It separates
workspaces booked whole (a meeting room, a private office) from shared areas where one booking takes
one seat.

What Maskani takes from this (R3): office and co-working listings carry hourly, daily or monthly
terms and a space type (whole room or shared seat). Hourly and daily bookings reuse the same booking
engine as short stays (pos-api) instead of a new one; monthly terms flow into R2 leasing.

## Owners hiring property managers

Proplexa lets property owners publish a request for proposal and receive competing bids, compared
side by side. ProVendorConnect does the same for associations and management firms buying services:
buyers use it free, providers pay a monthly subscription to bid. Light RFP and Synlio automate RFPs
for commercial real estate managers.

What Maskani takes from this (R3, user decision 2026-10-09): management tenders. An owner posts a
request for management (property, units, services wanted, budget). Verified property-management
tenants bid with fee basis, services and references. The owner compares bids side by side, and the
accepted bid creates the R2 mandate and portfolio in the winning tenant. Bidders pay through a
listing plan in treasury. The same tender pattern could later serve estate service procurement
(security, cleaning), reusing the vendor register from R1.

## Short-stay management

Guesty and Hostaway are the leading short-stay platforms: a channel manager syncing listings,
calendars and prices with Airbnb, Vrbo and Booking.com, a unified guest inbox, multi-calendar,
automation, payment processing and an owner portal.

What Maskani takes from this (R2, user decision 2026-10-09): an owner can switch a unit to short
stay. The unit becomes a room in pos-api's hotel module, which already has room bookings, folios, a
public booking widget and a booking policy engine. Maskani shows bookings and owner income and
charges the management fee. Channel sync with Airbnb and Booking.com is R4.

## Sources

- [Kenya's best real estate websites compared](https://commercialpropertykenya.com/kenyas-best-real-estate-websites-an-in-depth-comparison/)
- [BuyRentKenya relaunch](https://www.buyrentkenya.com/discover/a-fresh-new-experience-for-your-next-move-the-new-buyrentkenya-is-here)
- [Nyumbani for landlords](https://nyumbani.ke/landlords/)
- [How to conduct a land search using ArdhiSasa in 2026](https://amccopropertiesltd.co.ke/how-to-conduct-land-search-using-ardhisasa)
- [Verifying land ownership in Kenya](https://homeafrika.com/news/how-to-verify-land-ownership-in-kenya-before-you-buy-step-by-step-guide-1773303637)
- [LiquidSpace: finding the right space](https://support.liquidspace.com/hc/en-us/articles/203851029-Finding-the-right-space-to-work)
- [Proplexa competitive property management marketplace](https://smb.salisburypost.com/article/Proplexa-Secures-Enterprise-Adoption-in-2026-Signaling-a-Shift-Toward-a-Competitive-Property-Management-Marketplace/6a035528624914000279e93c)
- [ProVendorConnect B2B marketplace](https://www.digitalcommerce360.com/2026/03/31/provendorconnect-launches-b2b-marketplace/)
- [Hostaway vs Guesty](https://www.smoobu.com/en/?p=73883)
