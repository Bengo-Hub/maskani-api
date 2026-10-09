// Package router builds the chi route table: platform middleware, tenant resolution, access, then
// per-route module gates and permissions (docs/architecture.md, request pipeline).
package router

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/Bengo-Hub/httpware"
	authclient "github.com/Bengo-Hub/shared-auth-client"
	ratelimit "github.com/Bengo-Hub/shared-ratelimit"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/http/handlers"
	mw "github.com/bengobox/maskani-api/internal/http/middleware"
	"github.com/bengobox/maskani-api/internal/modules/rbac"
	"github.com/bengobox/maskani-api/internal/modules/settings"
	"github.com/bengobox/maskani-api/internal/modules/tenant"
)

// Deps are the router's dependencies.
type Deps struct {
	Log            *zap.Logger
	Limiter        *ratelimit.Limiter
	Auth           *authclient.AuthMiddleware
	AllowedOrigins []string
	Ent            *ent.Client
	RBAC           *rbac.Service
	Settings       *settings.Service
	TenantSyncer   *tenant.Syncer
	H              *handlers.H
	Health         *handlers.Health
	MediaRoot      string
	MediaSigner    *httpware.MediaSigner
	// InternalKey is the fleet INTERNAL_SERVICE_KEY sibling services present on /api/v1/internal.
	InternalKey string
}

// New returns the HTTP handler.
func New(d Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(ratelimit.TrustedRealIP) // never chi RealIP: it trusts client-sent forwarding headers
	r.Use(httpware.RequestID)
	r.Use(httpware.Logging(d.Log))
	r.Use(httpware.Recover(d.Log))
	r.Use(httpware.BypassForStreaming(middleware.Timeout(60 * time.Second)))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   d.AllowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "Origin", "X-Request-ID", "X-Tenant-ID", "X-Tenant-Slug", "X-API-Key", "Idempotency-Key", "X-Outlet-ID", "X-Device-Key"},
		ExposedHeaders:   []string{"Link", "Retry-After", "X-Module-Read-Only"},
		AllowCredentials: true,
		MaxAge:           300,
	}))
	if d.Limiter != nil {
		r.Use(d.Limiter.Middleware(ratelimit.IPKey, 300, time.Minute))
	}

	r.Get("/healthz", d.Health.Liveness)
	r.Get("/readyz", d.Health.Readiness)
	r.Get("/metrics", d.Health.Metrics)
	r.Get("/", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/v1/docs/", http.StatusFound) })
	r.Get("/v1/docs/*", docsHandler)

	if d.MediaRoot != "" {
		r.Handle("/media/*", http.StripPrefix("/media", httpware.StaticMedia(d.MediaRoot, httpware.MediaOptions{
			Private: func(string) bool { return true },
			Signer:  d.MediaSigner,
		})))
	}

	h := d.H
	// Public market (showcase). Tighter limits; enquiries are rate limited hardest.
	r.Route("/api/v1/market", func(pr chi.Router) {
		if d.Limiter != nil {
			pr.Use(d.Limiter.Middleware(ratelimit.IPKey, 60, time.Minute))
		}
		pr.Get("/estates", h.PublicEstates)
		pr.Get("/estates/{slug}", h.PublicEstate)
		if d.Limiter != nil {
			pr.With(d.Limiter.Middleware(ratelimit.IPKey, 5, time.Minute)).Post("/enquiries", h.PublicEnquiry)
		} else {
			pr.Post("/enquiries", h.PublicEnquiry)
		}
	})

	// Internal reads for sibling services (internal service key, no user token): the estate
	// residents notifications-api pages through when it sends a notice broadcast.
	r.Route("/api/v1/internal", func(ir chi.Router) {
		ir.Use(mw.RequireInternalKey(d.InternalKey))
		ir.Get("/residents/reach", h.InternalResidentsReach)
	})

	// Gate tablets authenticate with their device key.
	r.Route("/api/v1/gate", func(gr chi.Router) {
		gr.Use(h.DeviceAuth)
		if d.Limiter != nil {
			// A busy gate verifies a few codes a minute; 120 per device stops code guessing from a
			// stolen tablet key without slowing a real queue. Event batches are one call per sync.
			gr.With(d.Limiter.MiddlewareWith(ratelimit.ValueKey("device", deviceKeyHash),
				ratelimit.Options{Name: "gate-verify-device", Limit: 120, Window: time.Minute})).Post("/verify", h.DeviceVerify)
			gr.With(d.Limiter.MiddlewareWith(ratelimit.ValueKey("device", deviceKeyHash),
				ratelimit.Options{Name: "gate-events-device", Limit: 60, Window: time.Minute})).Post("/events", h.DeviceEvents)
		} else {
			gr.Post("/verify", h.DeviceVerify)
			gr.Post("/events", h.DeviceEvents)
		}
		gr.Get("/sync", h.DeviceSync)
		gr.Get("/units", h.DeviceUnits)
		gr.Get("/walk-ins/{id}", h.DeviceWalkIn)
		gr.Post("/incidents", h.DeviceIncident)
		// PIN guessing is throttled per device and per client IP (shared Redis limiter, all pods).
		if d.Limiter != nil {
			gr.With(
				d.Limiter.MiddlewareWith(ratelimit.ValueKey("device", deviceKeyHash), ratelimit.Options{Name: "gate-signon-device", Limit: 10, Window: time.Minute}),
				d.Limiter.MiddlewareWith(ratelimit.IPKey, ratelimit.Options{Name: "gate-signon-ip", Limit: 30, Window: time.Minute}),
			).Post("/sign-on", h.DeviceSignOn)
		} else {
			gr.Post("/sign-on", h.DeviceSignOn)
		}
	})

	r.Route("/api/v1/{tenant}/maskani", func(tr chi.Router) {
		tr.Use(mw.StreamQueryToken) // acts on GET .../stream only
		tr.Use(d.Auth.RequireAuth)
		tr.Use(authclient.RequireActiveSubscriptionForMutationsWithGrace(7))
		tr.Use(httpware.TenantV2(httpware.TenantConfig{
			ClaimsExtractor: func(ctx context.Context) (string, string, bool, bool) {
				c, ok := authclient.ClaimsFromContext(ctx)
				if !ok {
					return "", "", false, false
				}
				return c.TenantID, c.GetTenantSlug(), c.IsPlatformOwner, true
			},
			URLParamFunc: chi.URLParam, URLParamName: "tenant", Required: true,
		}))
		tr.Use(tenantSync(d))
		tr.Use(mw.ResolveAccess(d.Ent, d.RBAC, d.Log))
		mount(tr, d)
	})
	routes = r
	return r
}

// deviceKeyHash keys the sign-on limiter by device without putting the raw key in Redis.
func deviceKeyHash(r *http.Request) string {
	k := r.Header.Get("X-Device-Key")
	if k == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(k))
	return hex.EncodeToString(sum[:8])
}

// forgetAccess clears this pod's cached access facts once a change to staff, roles, invites or
// unit links has run, so the next request here sees it (other pods within their 30 second TTL).
func forgetAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		mw.ForgetAccess()
	})
}

// userKey keys per-user limits by the signed-in user (resolved by ResolveAccess).
func userKey(r *http.Request) string {
	if a := mw.FromContext(r.Context()); a != nil && a.AuthUserID != uuid.Nil {
		return a.AuthUserID.String()
	}
	return ""
}

// tenantSync resolves slug to the auth-api UUID when TenantV2 left the id empty (platform owners
// visiting another tenant, S2S callers), and keeps the local projection current.
func tenantSync(d Deps) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			slug := httpware.GetTenantSlug(ctx)
			if slug == "" {
				slug = chi.URLParam(r, "tenant")
			}
			if id := httpware.GetTenantID(ctx); id == "" || slug != "" {
				if tid, err := d.TenantSyncer.SyncTenant(ctx, slug); err == nil && tid != uuid.Nil {
					if httpware.GetTenantID(ctx) == "" {
						ctx = context.WithValue(ctx, httpware.TenantIDKey, tid.String())
					}
				} else if err != nil {
					d.Log.Warn("tenant sync failed", zap.String("slug", slug), zap.Error(err))
				}
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func mount(r chi.Router, d Deps) {
	h := d.H
	perm := mw.RequirePermission
	mod := func(m string) func(http.Handler) http.Handler { return mw.RequireModule(d.Settings, m) }

	// Document downloads render PDFs from up to a thousand ledger rows: limited per user across pods.
	export := func(next http.Handler) http.Handler { return next }
	if d.Limiter != nil {
		export = d.Limiter.MiddlewareWith(ratelimit.ValueKey("user", userKey),
			ratelimit.Options{Name: "export-user", Limit: 20, Window: time.Minute})
	}

	r.Get("/auth/me", h.Me)
	// Live change hints (SSE). The router's timeout already bypasses event streams.
	r.Get("/stream", h.Stream)
	if d.Limiter != nil {
		// Per signed-in user across every pod: uploads are heavy, signing is cheap but bulk.
		r.With(d.Limiter.MiddlewareWith(ratelimit.ValueKey("user", userKey),
			ratelimit.Options{Name: "media-upload-user", Limit: 30, Window: time.Minute})).Post("/media/upload", h.Media.Upload)
		r.With(d.Limiter.MiddlewareWith(ratelimit.ValueKey("user", userKey),
			ratelimit.Options{Name: "media-sign-user", Limit: 120, Window: time.Minute})).Post("/media/sign", h.Media.Sign)
	} else {
		r.Post("/media/upload", h.Media.Upload)
		r.Post("/media/sign", h.Media.Sign)
	}

	// Settings, users, catalogues.
	r.With(perm(rbac.PermSettingsView)).Get("/settings", h.GetSettings)
	r.With(perm(rbac.PermSettingsManage)).Put("/settings", h.UpdateSettings)
	r.With(perm(rbac.PermSettingsView)).Get("/settings/modules", h.GetModules)
	r.With(perm(rbac.PermSettingsManage)).Put("/settings/modules", h.SetModules)
	r.With(perm(rbac.PermSettingsView)).Get("/document-sequences", h.ListSequences)
	r.With(perm(rbac.PermSettingsManage)).Put("/document-sequences/{kind}", h.SaveSequence)
	r.Get("/catalogues/{kind}", h.Catalogue)
	// Settings managers edit any list; the people who use a list may add to it (CatalogueManagePerms).
	r.Put("/catalogues/{kind}/{code}", h.UpsertCatalogue)
	r.With(perm(rbac.PermUsersView)).Get("/users", h.ListUsers)
	r.With(perm(rbac.PermUsersView)).Get("/roles", h.ListRoles)
	r.With(perm(rbac.PermUsersView)).Get("/permissions", h.ListPermissions)
	r.With(perm(rbac.PermUsersManage)).Post("/roles", h.CreateRole)
	r.With(perm(rbac.PermUsersManage)).Post("/roles/customize", h.CustomizeRole)
	r.With(perm(rbac.PermUsersManage)).Put("/roles/{id}", h.UpdateRole)
	r.With(perm(rbac.PermUsersManage)).Delete("/roles/{id}", h.DeleteRole)
	r.With(perm(rbac.PermUsersManage), forgetAccess).Post("/users/invite", h.InviteStaff)
	r.With(perm(rbac.PermUsersManage), forgetAccess).Put("/users/{id}/roles", h.SetUserRoles)
	r.With(perm(rbac.PermUsersManage), forgetAccess).Put("/users/{id}/status", h.SetUserStatus)

	// Register.
	r.Group(func(g chi.Router) {
		g.Use(mod(settings.ModProperties))
		g.With(perm(rbac.PermPropertiesView)).Get("/properties", h.ListProperties)
		g.With(perm(rbac.PermPropertiesManage)).Post("/properties", h.CreateProperty)
		g.With(perm(rbac.PermPropertiesView)).Get("/properties/{id}", h.GetProperty)
		g.With(perm(rbac.PermPropertiesManage)).Patch("/properties/{id}", h.UpdateProperty)
		g.With(perm(rbac.PermPropertiesManage)).Post("/properties/{id}/blocks", h.CreateBlock)
		g.With(perm(rbac.PermUsersView)).Get("/properties/{id}/staff", h.ListStaff)
		g.With(perm(rbac.PermUsersManage), forgetAccess).Post("/properties/{id}/staff", h.AssignStaff)
		g.With(perm(rbac.PermUsersManage), forgetAccess).Delete("/staff-assignments/{id}", h.RemoveStaff)
		g.With(perm(rbac.PermUnitsView)).Get("/units", h.ListUnits)
		g.With(perm(rbac.PermUnitsManage)).Post("/units", h.CreateUnit)
		g.With(perm(rbac.PermUnitsView)).Get("/units/{id}", h.GetUnit)
		g.With(perm(rbac.PermUnitsManage)).Patch("/units/{id}", h.UpdateUnit)
		g.With(perm(rbac.PermPartiesManage), forgetAccess).Post("/units/{id}/parties", h.LinkParty)
		g.With(perm(rbac.PermPartiesManage)).Post("/units/{id}/vehicles", h.AddVehicle)
		g.With(perm(rbac.PermPartiesManage), forgetAccess).Post("/unit-parties/{id}/end", h.EndLink)
		g.With(perm(rbac.PermPartiesView)).Get("/parties", h.ListParties)
		g.With(perm(rbac.PermPartiesManage)).Post("/parties", h.CreateParty)
		g.With(perm(rbac.PermPartiesView)).Get("/parties/{id}", h.GetParty)
		g.With(perm(rbac.PermPartiesManage)).Patch("/parties/{id}", h.UpdateParty)
		g.With(perm(rbac.PermPartiesManage), forgetAccess).Post("/parties/{id}/invite", h.InviteParty)
		// CSV import of units and owners: validate first, then commit in the background.
		g.With(perm(rbac.PermImportsRun)).Get("/imports/template", h.ImportTemplate)
		g.With(perm(rbac.PermImportsRun)).Get("/imports", h.ListImports)
		g.With(perm(rbac.PermImportsRun)).Post("/imports", h.CreateImport)
		g.With(perm(rbac.PermImportsRun)).Get("/imports/{id}", h.GetImport)
		g.With(perm(rbac.PermImportsRun)).Post("/imports/{id}/commit", h.CommitImport)
	})

	// Billing and collections.
	r.Group(func(g chi.Router) {
		g.Use(mod(settings.ModBilling))
		g.With(perm(rbac.PermBillingView)).Get("/funds", h.ListFunds)
		g.With(perm(rbac.PermBillingManage)).Patch("/funds/{id}", h.UpdateFund)
		g.With(perm(rbac.PermBillingView)).Get("/charge-types", h.ListCharges)
		g.With(perm(rbac.PermBillingManage)).Get("/charge-types/catalogue", h.ChargeCatalogue)
		g.With(perm(rbac.PermBillingManage)).Post("/charge-types", h.CreateCharge)
		g.With(perm(rbac.PermBillingManage)).Post("/charge-types/enable", h.EnableCharge)
		g.With(perm(rbac.PermBillingManage)).Patch("/charge-types/{id}", h.UpdateCharge)
		g.With(perm(rbac.PermBillingManage)).Post("/charge-types/{id}/rates", h.AddRate)
		g.With(perm(rbac.PermBillingRun)).Post("/billing-runs/preview", h.PreviewRun)
		g.With(perm(rbac.PermBillingRun)).Post("/billing-runs", h.IssueRun)
		g.With(perm(rbac.PermBillingView)).Get("/billing-runs", h.ListRuns)
		g.With(perm(rbac.PermBillingView)).Get("/billing-runs/{id}", h.GetRun)
		g.With(perm(rbac.PermBillingView)).Get("/billing-runs/{id}/lines", h.RunLines)
		g.With(perm(rbac.PermBillingRun)).Post("/billing-runs/{id}/retry", h.RetryRun)
		g.With(perm(rbac.PermBillingView)).Get("/unit-accounts", h.ListAccounts)
		g.With(perm(rbac.PermBillingView)).Get("/unit-accounts/{id}/statement", h.Statement)
		g.With(perm(rbac.PermBillingView), export).Get("/unit-accounts/{id}/statement/export", h.StatementExport)
		g.With(perm(rbac.PermBillingCollect)).Post("/unit-accounts/{id}/pay", h.StaffPay)
		g.With(perm(rbac.PermBillingCollect)).Get("/collections/suspense", h.Suspense)
		g.With(perm(rbac.PermBillingCollect)).Post("/collections/suspense/{trans_id}/assign", h.AssignSuspense)
		g.With(perm(rbac.PermReportsView)).Get("/reports/arrears", h.Arrears)
		g.With(perm(rbac.PermReportsView), export).Get("/reports/arrears/export", h.ArrearsExport)
	})

	// Utilities.
	r.Group(func(g chi.Router) {
		g.Use(mod(settings.ModUtilities))
		g.With(perm(rbac.PermUtilitiesView, rbac.PermUtilitiesRead)).Get("/meters", h.ListMeters)
		g.With(perm(rbac.PermUtilitiesManage)).Post("/meters", h.CreateMeter)
		g.With(perm(rbac.PermUtilitiesRead)).Get("/reading-rounds/{period}", h.GetRound)
		g.With(perm(rbac.PermUtilitiesRead)).Post("/meters/{id}/readings", h.RecordReading)
		g.With(perm(rbac.PermUtilitiesManage)).Post("/meters/{id}/estimate", h.EstimateReading)
		g.With(perm(rbac.PermUtilitiesManage)).Post("/meter-readings/{id}/verify", h.VerifyReading)
		g.With(perm(rbac.PermUtilitiesView)).Get("/water-balance", h.WaterBalance)
		g.With(perm(rbac.PermUtilitiesView), export).Get("/water-balance/export", h.WaterBalanceExport)
	})

	// Sales.
	r.Group(func(g chi.Router) {
		g.Use(mod(settings.ModSales))
		g.With(perm(rbac.PermSalesView)).Get("/price-lists", h.ListPriceLists)
		g.With(perm(rbac.PermSalesManage)).Post("/price-lists", h.CreatePriceList)
		g.With(perm(rbac.PermSalesView)).Get("/availability", h.Availability)
		g.With(perm(rbac.PermSalesView)).Get("/reservations", h.ListReservations)
		g.With(perm(rbac.PermSalesManage)).Post("/reservations", h.Reserve)
		g.With(perm(rbac.PermSalesView)).Get("/sale-contracts", h.ListContracts)
		g.With(perm(rbac.PermSalesManage)).Post("/sale-contracts", h.CreateContract)
		g.With(perm(rbac.PermSalesView)).Get("/sale-contracts/{id}", h.GetContract)
		g.With(perm(rbac.PermSalesManage)).Post("/sale-contracts/{id}/activate", h.ActivateContract)
		g.With(perm(rbac.PermSalesManage)).Post("/instalments/{id}/release", h.ReleaseMilestone)
		g.With(perm(rbac.PermReportsView, rbac.PermSalesView)).Get("/reports/sales-position", h.SalesPosition)
		g.With(perm(rbac.PermSalesView)).Get("/enquiries", h.ListEnquiries)
		g.With(perm(rbac.PermSalesManage)).Patch("/enquiries/{id}", h.UpdateEnquiry)
	})

	// Works and vendors.
	r.Group(func(g chi.Router) {
		g.Use(mod(settings.ModMaintenance))
		g.With(perm(rbac.PermWorksView)).Get("/work-orders", h.ListWorkOrders)
		g.With(perm(rbac.PermWorksManage)).Post("/work-orders", h.CreateWorkOrder)
		g.With(perm(rbac.PermWorksView)).Get("/work-orders/{id}", h.GetWorkOrder)
		g.With(perm(rbac.PermWorksManage)).Post("/work-orders/{id}/actions", h.ActWorkOrder)
	})

	// Vendors (service providers module).
	r.Group(func(g chi.Router) {
		g.Use(mod(settings.ModProviders))
		g.With(perm(rbac.PermVendorsView)).Get("/vendors", h.ListVendors)
		g.With(perm(rbac.PermVendorsManage)).Post("/vendors", h.CreateVendor)
		g.With(perm(rbac.PermVendorsView)).Get("/vendors/{id}", h.GetVendor)
		g.With(perm(rbac.PermVendorsManage)).Post("/vendors/{id}/documents", h.AddVendorDocument)
		g.With(perm(rbac.PermVendorsManage)).Post("/vendors/{id}/personnel", h.AddPersonnel)
		g.With(perm(rbac.PermVendorsManage)).Put("/vendors/{id}/personnel/{pid}/pin", h.SetPersonnelPIN)
	})

	// Gate (staff side).
	r.Group(func(g chi.Router) {
		g.Use(mod(settings.ModGate))
		g.With(perm(rbac.PermGateManage)).Post("/gate/devices", h.RegisterDevice)
		g.With(perm(rbac.PermGateView)).Get("/gate/events", h.ListGateEvents)
		g.With(perm(rbac.PermGateView)).Get("/visitor-passes", h.ListPasses)
		g.With(perm(rbac.PermGateManage)).Post("/visitor-passes", h.StaffCreatePass)
		g.With(perm(rbac.PermGateView)).Get("/incidents", h.ListIncidents)
		g.With(perm(rbac.PermGateView)).Get("/incidents/{id}", h.GetIncident)
		g.With(perm(rbac.PermGateView)).Post("/incidents", h.ReportIncident)
	})

	// Communication and reports.
	r.Group(func(g chi.Router) {
		g.Use(mod(settings.ModCommunication))
		g.With(perm(rbac.PermNoticesManage)).Get("/notices", h.ListNotices)
		g.With(perm(rbac.PermNoticesManage)).Post("/notices", h.CreateNotice)
		g.With(perm(rbac.PermNoticesManage)).Post("/notices/{id}/send", h.SendNotice)
		g.With(perm(rbac.PermNoticesManage)).Get("/notices/{id}/deliveries", h.NoticeDeliveries)
	})
	r.With(perm(rbac.PermReportsView)).Get("/reports/dashboard", h.Dashboard)
	r.With(perm(rbac.PermReportsView)).Get("/reports/insights", h.Insights)
	r.With(perm(rbac.PermReportsView), export).Get("/reports/insights/export", h.PerformanceExport)
	r.Get("/reports/role-summary", h.RoleSummary) // any staff; panels filtered by role on screen

	// Portal: scoped by the caller's own unit links.
	r.Route("/me", func(m chi.Router) {
		m.Use(mw.RequirePortalUser)
		m.Get("/units", h.MyUnits)
		m.Get("/accounts/{id}/statement", h.MyStatement)
		m.With(export).Get("/accounts/{id}/statement/export", h.MyStatementExport)
		m.Post("/accounts/{id}/pay", h.MyPay)
		m.Get("/purchase", h.MyPurchase)
		m.Get("/passes", h.MyPasses)
		m.Post("/passes", h.MyCreatePass)
		m.Post("/passes/{id}/cancel", h.MyCancelPass)
		m.Get("/requests", h.MyRequests)
		m.Post("/requests", h.MyCreateRequest)
		m.Post("/requests/{id}/actions", h.MyRequestAction)
		m.Get("/notices", h.MyNotices)
		m.Post("/terms/accept", h.MyAcceptTerms)
		m.Post("/walk-ins/{id}/decide", h.MyDecideWalkIn)
	})
}
