package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/party"
	"github.com/bengobox/maskani-api/internal/ent/tenant"
	"github.com/bengobox/maskani-api/internal/http/httpx"
)

// CRMLinks tells marketflow whether maskani still links a CRM contact, so marketflow can decide
// between deleting an erased person's contact and stripping it to a minimal record (S2-8 review
// M-12). Maskani keeps a CRM contact id on parties only. Erasing a party clears its own id, but
// two parties may carry the same contact (a resident who is also an owner, a re-registered
// tenant), so the contact must not be deleted while any other party still links it.
//
// GET /api/v1/s2s/{tenant}/crm-links/{contactID} (X-API-Key) -> {"referenced": bool}
//
// {tenant} is the tenant UUID or slug and must be known here: an unknown one answers 404, never
// "not referenced", so marketflow keeps the contact. Parties are matched in every tenant: contact
// ids are random UUIDs minted by marketflow, and a party under a drifted tenant id can only answer
// "referenced", which keeps the contact. The answer is the flag only, and logs carry ids only.
func CRMLinks(db *ent.Client, log *zap.Logger) http.HandlerFunc {
	if log == nil {
		log = zap.NewNop()
	}
	log = log.Named("crm_links")
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		contactID, err := uuid.Parse(chi.URLParam(r, "contactID"))
		if err != nil || contactID == uuid.Nil {
			httpx.Error(w, http.StatusBadRequest, "invalid_contact_id", "contact id must be a UUID")
			return
		}
		raw := chi.URLParam(r, "tenant")
		pred := tenant.Slug(raw)
		if id, perr := uuid.Parse(raw); perr == nil {
			pred = tenant.ID(id)
		}
		known, err := db.Tenant.Query().Where(pred).Exist(ctx)
		if err != nil {
			log.Error("crm links: tenant lookup failed", zap.Error(err))
			httpx.Error(w, http.StatusInternalServerError, "internal", "failed to check links")
			return
		}
		if !known {
			httpx.Error(w, http.StatusNotFound, "tenant_not_found", "tenant not found")
			return
		}
		found, err := db.Party.Query().Where(party.CrmContactID(contactID)).Exist(ctx)
		if err != nil {
			log.Error("crm links check failed", zap.String("contact_id", contactID.String()), zap.Error(err))
			httpx.Error(w, http.StatusInternalServerError, "internal", "failed to check links")
			return
		}
		httpx.JSON(w, http.StatusOK, map[string]bool{"referenced": found})
	}
}
