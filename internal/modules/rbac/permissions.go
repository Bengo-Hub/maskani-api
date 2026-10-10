// Package rbac holds the Maskani permission catalogue and roles (global, never tenant scoped) and
// resolves a user's effective permissions within a tenant.
package rbac

// Permission codes. Format maskani.{module}.{action}.
const (
	PermTenantAdmin      = "maskani.tenant.admin"
	PermSettingsView     = "maskani.settings.view"
	PermSettingsManage   = "maskani.settings.manage"
	PermUsersView        = "maskani.users.view"
	PermUsersManage      = "maskani.users.manage"
	PermPropertiesView   = "maskani.properties.view"
	PermPropertiesManage = "maskani.properties.manage"
	PermUnitsView        = "maskani.units.view"
	PermUnitsManage      = "maskani.units.manage"
	PermPartiesView      = "maskani.parties.view"
	PermPartiesManage    = "maskani.parties.manage"
	PermImportsRun       = "maskani.imports.run"
	PermBillingView      = "maskani.billing.view"
	PermBillingManage    = "maskani.billing.manage"
	PermBillingRun       = "maskani.billing.run"
	PermBillingCollect   = "maskani.billing.collect"
	PermBillingAdjust    = "maskani.billing.adjust"
	PermBillingApprove   = "maskani.billing.approve"
	PermBillingVerify    = "maskani.billing.verify"
	PermUtilitiesRead    = "maskani.utilities.read"
	PermUtilitiesView    = "maskani.utilities.view"
	PermUtilitiesManage  = "maskani.utilities.manage"
	PermSalesView        = "maskani.sales.view"
	PermSalesManage      = "maskani.sales.manage"
	PermWorksView        = "maskani.works.view"
	PermWorksManage      = "maskani.works.manage"
	PermVendorsView      = "maskani.vendors.view"
	PermVendorsManage    = "maskani.vendors.manage"
	PermGateView         = "maskani.gate.view"
	PermGateManage       = "maskani.gate.manage"
	PermGatePasses       = "maskani.gate.passes"
	PermNoticesView      = "maskani.notices.view"
	PermNoticesManage    = "maskani.notices.manage"
	PermDocumentsView    = "maskani.documents.view"
	PermDocumentsIssue   = "maskani.documents.issue"
	PermDocumentsManage  = "maskani.documents.manage"
	PermReportsView      = "maskani.reports.view"
	PermReportsExport    = "maskani.reports.export"
	PermPrivacyManage    = "maskani.privacy.manage"
)

// PermissionDef describes one catalogue entry.
type PermissionDef struct {
	Code   string
	Name   string
	Module string
	Action string
}

// Catalogue is the full permission list, seeded on start.
var Catalogue = []PermissionDef{
	{PermTenantAdmin, "Administer the tenant", "tenant", "admin"},
	{PermSettingsView, "View settings", "settings", "view"},
	{PermSettingsManage, "Manage settings, modules and catalogues", "settings", "manage"},
	{PermUsersView, "View users and roles", "users", "view"},
	{PermUsersManage, "Manage users, roles and property staff", "users", "manage"},
	{PermPropertiesView, "View properties", "properties", "view"},
	{PermPropertiesManage, "Manage properties and blocks", "properties", "manage"},
	{PermUnitsView, "View units", "units", "view"},
	{PermUnitsManage, "Manage units", "units", "manage"},
	{PermPartiesView, "View owners and occupants", "parties", "view"},
	{PermPartiesManage, "Manage owners, occupants and household", "parties", "manage"},
	{PermImportsRun, "Run data imports", "imports", "run"},
	{PermBillingView, "View accounts, invoices and statements", "billing", "view"},
	{PermBillingManage, "Manage charges, rates and funds", "billing", "manage"},
	{PermBillingRun, "Run billing", "billing", "run"},
	{PermBillingCollect, "Record and assign payments", "billing", "collect"},
	{PermBillingAdjust, "Request credit notes and adjustments", "billing", "adjust"},
	{PermBillingApprove, "Approve credit notes and adjustments", "billing", "approve"},
	{PermBillingVerify, "Verify recorded bank, cash and cheque payments", "billing", "verify"},
	{PermUtilitiesRead, "Capture meter readings", "utilities", "read"},
	{PermUtilitiesView, "View meters, readings and water balance", "utilities", "view"},
	{PermUtilitiesManage, "Manage meters and verify readings", "utilities", "manage"},
	{PermSalesView, "View sales, contracts and statements", "sales", "view"},
	{PermSalesManage, "Manage price lists, reservations and contracts", "sales", "manage"},
	{PermWorksView, "View work orders", "works", "view"},
	{PermWorksManage, "Manage work orders and maintenance", "works", "manage"},
	{PermVendorsView, "View vendors", "vendors", "view"},
	{PermVendorsManage, "Manage vendors, contracts and personnel", "vendors", "manage"},
	{PermGateView, "View gate logs, passes and incidents", "gate", "view"},
	{PermGateManage, "Manage gate devices, posts and incidents", "gate", "manage"},
	{PermGatePasses, "Issue and cancel visitor passes", "gate", "passes"},
	{PermNoticesView, "View notices and who received them", "notices", "view"},
	{PermNoticesManage, "Write and send notices", "notices", "manage"},
	{PermDocumentsView, "View and download issued documents", "documents", "view"},
	{PermDocumentsIssue, "Issue documents from approved templates", "documents", "issue"},
	{PermDocumentsManage, "Manage and approve document templates", "documents", "manage"},
	{PermReportsView, "View reports and dashboards", "reports", "view"},
	{PermReportsExport, "Download reports as PDF or Excel", "reports", "export"},
	{PermPrivacyManage, "Handle data subject requests", "privacy", "manage"},
}

// ImpliedBy names, for permissions split out of an older one, the permission that used to cover
// them. When a new code is first seeded, every role (estate copies included) holding the older
// code gets it once, so no one loses access on upgrade; later removals by an admin stick.
var ImpliedBy = map[string]string{
	PermGatePasses:     PermGateManage,
	PermNoticesView:    PermNoticesManage,
	PermDocumentsView:  PermDocumentsManage,
	PermDocumentsIssue: PermDocumentsManage,
	PermReportsExport:  PermReportsView,
	PermBillingVerify:  PermBillingApprove,
}

// RoleDef describes a seeded system role.
type RoleDef struct {
	Code        string
	Name        string
	Description string
	Customer    bool
	Permissions []string
}

func allCodes() []string {
	out := make([]string, 0, len(Catalogue))
	for _, p := range Catalogue {
		out = append(out, p.Code)
	}
	return out
}

// Role codes.
const (
	RoleTenantAdmin      = "tenant_admin"
	RolePropertyManager  = "property_manager"
	RoleFinanceOfficer   = "finance_officer"
	RoleSalesOfficer     = "sales_officer"
	RoleCaretaker        = "caretaker"
	RoleSecurityManager  = "security_manager"
	RoleViewer           = "viewer"
	RoleOwner            = "owner"
	RoleOccupant         = "occupant"
	RoleVendorSupervisor = "vendor_supervisor"
	RoleGuard            = "guard"
)

// Roles is the seeded role catalogue. Portal roles carry no staff permissions: what a customer can
// see is decided by their unit links, not by permissions.
var Roles = []RoleDef{
	{RoleTenantAdmin, "Tenant administrator", "All settings, staff, roles and approvals", false, allCodes()},
	{RolePropertyManager, "Property manager", "Runs assigned properties", false, []string{
		PermSettingsView, PermUsersView, PermPropertiesView, PermPropertiesManage, PermUnitsView, PermUnitsManage,
		PermPartiesView, PermPartiesManage, PermImportsRun, PermBillingView, PermUtilitiesView, PermUtilitiesManage,
		PermUtilitiesRead, PermSalesView, PermWorksView, PermWorksManage, PermVendorsView, PermVendorsManage,
		PermGateView, PermGateManage, PermGatePasses, PermNoticesView, PermNoticesManage, PermDocumentsView, PermBillingVerify,
		PermDocumentsIssue, PermDocumentsManage, PermReportsView, PermReportsExport,
	}},
	{RoleFinanceOfficer, "Finance officer", "Billing, collections and adjustments", false, []string{
		PermSettingsView, PermPropertiesView, PermUnitsView, PermPartiesView, PermBillingView, PermBillingManage,
		PermBillingRun, PermBillingCollect, PermBillingAdjust, PermUtilitiesView, PermSalesView, PermVendorsView,
		PermNoticesView, PermDocumentsView, PermDocumentsIssue, PermReportsView, PermReportsExport,
	}},
	{RoleSalesOfficer, "Sales officer", "Price lists, reservations and sale contracts", false, []string{
		PermPropertiesView, PermUnitsView, PermPartiesView, PermPartiesManage, PermSalesView, PermSalesManage,
		PermBillingView, PermDocumentsView, PermDocumentsIssue, PermDocumentsManage, PermReportsView, PermReportsExport,
	}},
	{RoleCaretaker, "Caretaker", "On-site readings, work orders, resident requests and notices", false, []string{
		PermPropertiesView, PermUnitsView, PermPartiesView, PermUtilitiesRead, PermUtilitiesView, PermWorksView,
		PermWorksManage, PermGateView, PermGatePasses, PermNoticesView, PermNoticesManage, PermBillingVerify,
	}},
	{RoleSecurityManager, "Security manager", "Gate, passes, patrols and incidents", false, []string{
		PermPropertiesView, PermUnitsView, PermGateView, PermGateManage, PermGatePasses, PermVendorsView, PermNoticesView,
	}},
	{RoleViewer, "Viewer", "Read-only staff access", false, []string{
		PermPropertiesView, PermUnitsView, PermPartiesView, PermBillingView, PermUtilitiesView, PermSalesView,
		PermWorksView, PermVendorsView, PermGateView, PermNoticesView, PermDocumentsView, PermReportsView,
	}},
	{RoleOwner, "Owner or buyer", "Portal access to own units", true, nil},
	{RoleOccupant, "Occupant", "Portal access to the unit they live in", true, nil},
	{RoleVendorSupervisor, "Vendor supervisor", "Vendor portal", true, nil},
	{RoleGuard, "Guard", "Gate tablet only", true, nil},
}

// CatalogueManagePerms lists, per catalogue kind, the permissions besides settings.manage that may add
// to or rename entries in it, so the person creating a unit can add a new unit type from the
// dropdown without a settings role. A kind not listed needs settings.manage.
var CatalogueManagePerms = map[string][]string{
	"unit_type":       {PermUnitsManage, PermPropertiesManage},
	"property_type":   {PermPropertiesManage},
	"wo_category":     {PermWorksManage},
	"vendor_category": {PermVendorsManage},
	"incident_type":   {PermGateManage},
	"notice_category": {PermNoticesManage},
}

// MapSSORole maps a global auth-api role onto a Maskani role, or "" when the role says nothing about
// property work.
//
// One auth-api tenant can run several products (codevertex-demo hosts every vertical), and role
// names such as manager, cashier, supervisor and member are reused by all of them. So only two kinds
// of role grant Maskani access on their own: the tenant's admin-level roles and property-specific
// names. The generic names map only when propertyTenant is true, meaning the tenant's own use case
// is property, so a POS cashier or a hotel manager in a mixed tenant never lands in Maskani.
// Portal users are invited with explicit maskani_* roles and are also recognised by their party link.
func MapSSORole(role string, propertyTenant bool) string {
	switch role {
	// In auth-api "owner" is the business owner, an admin-level role.
	case "superuser", "super_admin", "admin", "owner", "tenant_admin", "administrator", "director":
		return RoleTenantAdmin
	case "property_manager", "estate_manager", "maskani_manager":
		return RolePropertyManager
	case "estate_accountant", "property_accountant", "maskani_finance":
		return RoleFinanceOfficer
	case "property_sales", "letting_officer", "maskani_sales":
		return RoleSalesOfficer
	case "caretaker", "maskani_caretaker":
		return RoleCaretaker
	case "estate_security", "maskani_security":
		return RoleSecurityManager
	case "maskani_owner":
		return RoleOwner
	case "maskani_occupant":
		return RoleOccupant
	case "maskani_vendor":
		return RoleVendorSupervisor
	case "maskani_guard":
		return RoleGuard
	}
	if !propertyTenant {
		return ""
	}
	switch role {
	case "manager", "supervisor":
		return RolePropertyManager
	case "accountant", "finance", "finance_officer", "cashier":
		return RoleFinanceOfficer
	case "sales", "sales_officer":
		return RoleSalesOfficer
	case "security", "security_manager":
		return RoleSecurityManager
	case "member", "staff":
		return RoleViewer
	}
	return ""
}
