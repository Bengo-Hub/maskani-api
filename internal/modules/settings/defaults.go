// Package settings owns tenant settings, module switches, catalogues and the defaults a new tenant
// starts from (SRDD 4.3 to 4.7: configuration over customisation).
package settings

// Module codes (SRDD 4.3).
const (
	ModProperties    = "properties"
	ModBilling       = "billing"
	ModUtilities     = "utilities"
	ModSales         = "sales"
	ModEstate        = "estate"
	ModMaintenance   = "maintenance"
	ModProviders     = "providers"
	ModGate          = "gate"
	ModStaff         = "staff"
	ModCommunication = "communication"
	ModLeasing       = "leasing"
	ModCommercial    = "commercial"
	ModPortfolios    = "portfolios"
	ModMarketplace   = "marketplace"
	ModAmenities     = "amenities"
)

// ReleasedModules are live in this release; later ones stay hidden for everyone.
var ReleasedModules = map[string]bool{
	ModProperties: true, ModBilling: true, ModUtilities: true, ModSales: true, ModEstate: true,
	ModMaintenance: true, ModProviders: true, ModGate: true, ModStaff: true, ModCommunication: true,
}

// ModuleDependencies lists what each module needs switched on.
var ModuleDependencies = map[string][]string{
	ModBilling:     {ModProperties},
	ModUtilities:   {ModBilling},
	ModSales:       {ModBilling},
	ModEstate:      {ModBilling},
	ModMaintenance: {ModProperties},
	ModProviders:   {ModMaintenance},
	ModGate:        {ModProperties},
	ModStaff:       {ModProperties},
	ModLeasing:     {ModBilling},
	ModCommercial:  {ModLeasing},
	ModPortfolios:  {ModLeasing},
	ModAmenities:   {ModBilling},
}

// Presets switch on a sensible module set in one step (SRDD 4.3).
var Presets = map[string][]string{
	"estate_developer":       {ModProperties, ModBilling, ModUtilities, ModSales, ModEstate, ModMaintenance, ModProviders, ModGate, ModStaff, ModCommunication},
	"developer_sales":        {ModProperties, ModBilling, ModSales, ModCommunication},
	"owners_association":     {ModProperties, ModBilling, ModUtilities, ModEstate, ModMaintenance, ModProviders, ModGate, ModCommunication},
	"residential_manager":    {ModProperties, ModBilling, ModUtilities, ModLeasing, ModPortfolios, ModMaintenance, ModProviders, ModCommunication},
	"commercial_manager":     {ModProperties, ModBilling, ModUtilities, ModLeasing, ModCommercial, ModPortfolios, ModMaintenance, ModProviders, ModCommunication},
	"self_managing_landlord": {ModProperties, ModBilling, ModUtilities, ModLeasing, ModMaintenance, ModProviders, ModCommunication},
	"agent_listings":         {ModProperties, ModMarketplace},
}

// FeatureCode is the subscriptions-api feature code gating a module.
func FeatureCode(module string) string { return "maskani_" + module }

// CatalogDefault is one platform catalogue entry.
type CatalogDefault struct {
	Kind, Code, Name string
	Attrs            map[string]any
}

// CatalogDefaults are seeded once with no tenant; tenants override or extend them (SRDD 4.7).
var CatalogDefaults = []CatalogDefault{
	{"property_type", "estate", "Estate", nil},
	{"property_type", "apartment_block", "Apartment block", nil},
	{"property_type", "office", "Office building", nil},
	{"property_type", "retail", "Retail centre", nil},
	{"property_type", "mixed_use", "Mixed use", nil},
	{"property_type", "industrial", "Industrial park", nil},
	{"property_type", "land", "Land", nil},

	{"unit_type", "studio", "Studio", map[string]any{"bedrooms": 0}},
	{"unit_type", "1br_apartment", "One bedroom apartment", map[string]any{"bedrooms": 1}},
	{"unit_type", "2br_apartment", "Two bedroom apartment", map[string]any{"bedrooms": 2}},
	{"unit_type", "3br_apartment", "Three bedroom apartment", map[string]any{"bedrooms": 3}},
	{"unit_type", "3br_house", "Three bedroom house", map[string]any{"bedrooms": 3}},
	{"unit_type", "4br_house", "Four bedroom house", map[string]any{"bedrooms": 4}},
	{"unit_type", "penthouse", "Penthouse", nil},
	{"unit_type", "office_suite", "Office suite", nil},
	{"unit_type", "shop", "Shop", nil},
	{"unit_type", "warehouse", "Warehouse", nil},
	{"unit_type", "parking_bay", "Parking bay", nil},
	{"unit_type", "plot", "Plot", nil},

	{"amenity", "borehole", "Borehole", nil},
	{"amenity", "backup_generator", "Backup generator", nil},
	{"amenity", "lift", "Lift", nil},
	{"amenity", "pool", "Swimming pool", nil},
	{"amenity", "gym", "Gym", nil},
	{"amenity", "parking", "Parking", nil},
	{"amenity", "cctv", "CCTV", nil},
	{"amenity", "electric_fence", "Electric fence", nil},
	{"amenity", "solar_water", "Solar water heating", nil},
	{"amenity", "fibre", "Fibre internet", nil},
	{"amenity", "playground", "Children's playground", nil},

	{"wo_category", "plumbing", "Plumbing", map[string]any{"default_priority": "normal"}},
	{"wo_category", "electrical", "Electrical", map[string]any{"default_priority": "normal"}},
	{"wo_category", "structural", "Structural", map[string]any{"default_priority": "normal"}},
	{"wo_category", "water_supply", "Water supply", map[string]any{"default_priority": "high"}},
	{"wo_category", "security_systems", "Security systems", map[string]any{"default_priority": "high"}},
	{"wo_category", "common_areas", "Common areas", map[string]any{"default_priority": "low"}},
	{"wo_category", "appliances", "Appliances", map[string]any{"default_priority": "normal"}},
	{"wo_category", "landscaping", "Landscaping", map[string]any{"default_priority": "low"}},

	{"vendor_category", "security", "Security agency", map[string]any{"required_docs": []string{"psra", "business_permit", "kra", "insurance"}}},
	{"vendor_category", "garbage", "Garbage collection", map[string]any{"required_docs": []string{"nema", "business_permit", "kra"}}},
	{"vendor_category", "cleaning", "Cleaning", map[string]any{"required_docs": []string{"business_permit", "kra", "insurance"}}},
	{"vendor_category", "landscaping", "Landscaping", map[string]any{"required_docs": []string{"business_permit", "kra"}}},
	{"vendor_category", "borehole", "Borehole and pumps", map[string]any{"required_docs": []string{"business_permit", "kra"}}},
	{"vendor_category", "plumbing_electrical", "Plumbing and electrical", map[string]any{"required_docs": []string{"nca", "epra", "kra"}}},
	{"vendor_category", "pest_control", "Pest control", map[string]any{"required_docs": []string{"pcpb", "kra"}}},
	{"vendor_category", "tank_cleaning", "Tank cleaning and water testing", map[string]any{"required_docs": []string{"business_permit"}}},

	{"vendor_doc_type", "psra", "PSRA licence", nil},
	{"vendor_doc_type", "nema", "NEMA licence", nil},
	{"vendor_doc_type", "pcpb", "PCPB licence", nil},
	{"vendor_doc_type", "epra", "EPRA licence", nil},
	{"vendor_doc_type", "nca", "NCA registration", nil},
	{"vendor_doc_type", "business_permit", "Business permit", nil},
	{"vendor_doc_type", "insurance", "Insurance certificate", nil},
	{"vendor_doc_type", "kra", "KRA PIN certificate", nil},

	{"pass_type", "guest_single", "Guest, single visit", map[string]any{"default_hours": 12}},
	{"pass_type", "guest_recurring", "Guest, recurring", map[string]any{"max_days": 90}},
	{"pass_type", "domestic_staff", "Domestic staff", nil},
	{"pass_type", "delivery", "Delivery", map[string]any{"default_hours": 2}},
	{"pass_type", "contractor", "Contractor", nil},
	{"pass_type", "agency", "Agency personnel", nil},

	{"incident_type", "intrusion", "Intrusion", nil},
	{"incident_type", "theft", "Theft", nil},
	{"incident_type", "fire", "Fire", nil},
	{"incident_type", "medical", "Medical", nil},
	{"incident_type", "dispute", "Dispute", nil},
	{"incident_type", "damage", "Damage", nil},
	{"incident_type", "other", "Other", nil},

	{"notice_category", "general", "General", nil},
	{"notice_category", "water", "Water", nil},
	{"notice_category", "security", "Security", nil},
	{"notice_category", "maintenance", "Maintenance", nil},
	{"notice_category", "meeting", "Meeting", nil},
	{"notice_category", "emergency", "Emergency", nil},

	{"title_stage", "sectional_plan", "Sectional plan registered", nil},
	{"title_stage", "consents", "Consents obtained", nil},
	{"title_stage", "stamp_duty", "Stamp duty paid", nil},
	{"title_stage", "lease_registration", "Lease or transfer registered", nil},
	{"title_stage", "title_issued", "Title issued", nil},
	{"title_stage", "title_released", "Title released to owner", nil},
}

// ChargeDefault is a platform charge the tenant can enable (SRDD 4.6).
type ChargeDefault struct {
	Code, Name, Group, Basis, Frequency, BillTo, Fund string
	Reassignable                                      bool
	Priority                                          int
	TariffKind                                        string
	Presets                                           []string // presets that enable it automatically
}

var estatePresets = []string{"estate_developer", "owners_association"}

// ChargeDefaults is the seeded charge catalogue.
var ChargeDefaults = []ChargeDefault{
	{"service_charge", "Service charge", "services", "per_unit_type", "monthly", "owner", "estate", false, 10, "flat", estatePresets},
	{"water", "Water", "utilities", "metered", "monthly", "owner", "estate", true, 20, "flat", estatePresets},
	{"garbage", "Garbage collection", "services", "fixed", "monthly", "owner", "estate", true, 30, "flat", estatePresets},
	{"security_levy", "Security levy", "services", "fixed", "monthly", "owner", "estate", false, 40, "flat", nil},
	{"sinking_fund", "Sinking fund", "reserves", "fixed", "monthly", "owner", "estate", false, 50, "flat", estatePresets},
	{"extra_parking", "Extra parking bay", "amenities", "fixed", "monthly", "owner", "estate", true, 60, "flat", estatePresets},
	{"special_levy", "Special levy", "reserves", "entitlement", "one_off", "owner", "estate", false, 70, "flat", estatePresets},
	{"repair_recharge", "Repair recharge", "recoveries", "one_off", "one_off", "owner", "estate", false, 80, "flat", estatePresets},
	{"late_payment", "Late payment charge", "recoveries", "percentage", "one_off", "owner", "estate", false, 999, "flat", nil},
	{"cleaning", "Cleaning", "services", "fixed", "monthly", "owner", "estate", false, 35, "flat", nil},
	{"landscaping", "Landscaping", "services", "fixed", "monthly", "owner", "estate", false, 36, "flat", nil},
	{"common_power", "Common area power", "services", "entitlement", "monthly", "owner", "estate", false, 37, "flat", nil},
	{"lift_maintenance", "Lift maintenance", "services", "fixed", "monthly", "owner", "estate", false, 38, "flat", nil},
	{"generator", "Generator and backup power", "services", "fixed", "monthly", "owner", "estate", false, 39, "flat", nil},
	{"borehole_water", "Borehole water", "utilities", "metered", "monthly", "owner", "estate", true, 21, "flat", nil},
	{"sewer", "Sewer", "utilities", "fixed", "monthly", "owner", "estate", true, 22, "flat", nil},
	{"internet", "Internet", "utilities", "fixed", "monthly", "occupant", "estate", true, 23, "flat", nil},
	{"rent", "Rent", "occupancy", "fixed", "monthly", "occupant", "client_rent", false, 5, "flat", nil},
	{"deposit", "Deposit", "occupancy", "one_off", "on_event", "occupant", "deposits", false, 1, "flat", nil},
	{"unit_price", "Unit price", "sales", "one_off", "one_off", "buyer", "sales", false, 10, "flat", []string{"estate_developer", "developer_sales"}},
	{"reservation_fee", "Reservation fee", "sales", "one_off", "one_off", "buyer", "sales", false, 5, "flat", []string{"estate_developer", "developer_sales"}},
	{"sale_deposit", "Sale deposit", "sales", "one_off", "one_off", "buyer", "sales", false, 6, "flat", []string{"estate_developer", "developer_sales"}},
	{"instalment", "Instalment", "sales", "one_off", "one_off", "buyer", "sales", false, 7, "flat", []string{"estate_developer", "developer_sales"}},
	{"transfer_fees", "Transfer and legal fees recovery", "sales", "one_off", "one_off", "buyer", "sales", false, 8, "flat", nil},
}

// FundDefault is a fund created for a preset.
type FundDefault struct {
	Code, Name, Kind, Prefix string
	Default                  bool
	Presets                  []string
}

// FundDefaults are created per preset (SRDD 8.4: funds never mix).
var FundDefaults = []FundDefault{
	{"estate", "Estate service fund", "estate", "", true, []string{"estate_developer", "owners_association", "residential_manager", "commercial_manager", "self_managing_landlord"}},
	{"sales", "Sales collections", "sales", "S-", false, []string{"estate_developer", "developer_sales"}},
	{"deposits", "Deposits held", "deposits", "D-", false, []string{"residential_manager", "commercial_manager", "self_managing_landlord"}},
	{"client_rent", "Rent collected for landlords", "client_rent", "L-", false, []string{"residential_manager", "commercial_manager"}},
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
