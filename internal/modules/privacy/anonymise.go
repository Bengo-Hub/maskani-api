package privacy

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/privacyrequest"
	"github.com/bengobox/maskani-api/internal/ent/reservation"
	"github.com/bengobox/maskani-api/internal/ent/unitaccount"
	"github.com/bengobox/maskani-api/internal/ent/unitparty"
	"github.com/bengobox/maskani-api/internal/ent/vehicle"
	"github.com/bengobox/maskani-api/internal/ent/visitorpass"
	"github.com/bengobox/maskani-api/internal/events"
	"github.com/bengobox/maskani-api/internal/http/httpx"
)

// Anonymise completes an erasure request. The person must have no active unit link or open
// reservation (an owner or occupant is still in a contract with the estate). Their name becomes
// "Former resident" with a short reference; phone, email, identity numbers, address, consents,
// notes and custom fields are cleared, portal access ends, vehicles are removed and live visitor
// passes cancelled. Bills, payments, contracts and the account history stay, pointing at the party
// by id only, as the law requires them kept.
func (s *Service) Anonymise(ctx context.Context, id, by uuid.UUID) (*ent.PrivacyRequest, error) {
	req, err := s.verified(ctx, id)
	if err != nil {
		return nil, err
	}
	if req.Kind != privacyrequest.KindDeletion {
		return nil, httpx.Invalid("only an erasure request erases a person's details")
	}
	pid := *req.PartyID
	if n, err := s.client.UnitParty.Query().Where(unitparty.PartyID(pid), unitparty.StatusEQ(unitparty.StatusActive)).Count(ctx); err != nil {
		return nil, err
	} else if n > 0 {
		return nil, httpx.Conflict("this person still owns, is buying or lives in a unit; end those links first, or reject the request with that reason")
	}
	if held, err := s.client.Reservation.Query().Where(reservation.PartyID(pid),
		reservation.StatusIn(reservation.StatusPendingPayment, reservation.StatusActive)).Exist(ctx); err != nil {
		return nil, err
	} else if held {
		return nil, httpx.Conflict("this person holds a reservation; cancel it first")
	}

	pt, err := s.client.Party.Get(ctx, pid)
	if err != nil {
		return nil, err
	}

	name := "Former resident " + pid.String()[:8]
	now := time.Now()
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	err = func() error {
		// The CRM contact may also carry other services' history, so maskani only unlinks it and
		// tells the CRM owner, with ids only, who decides whether to delete it.
		if pt.CrmContactID != nil {
			if err := events.Publish(ctx, tx.OutboxEvent, pt.TenantID, pid.String(), PartyAnonymised, map[string]any{
				"party_id": pid, "crm_contact_id": *pt.CrmContactID, "tenant_id": pt.TenantID}); err != nil {
				return err
			}
		}
		if err := tx.Party.UpdateOneID(pid).SetDisplayName(name).ClearFirstName().ClearLastName().ClearCompanyName().
			ClearRegistrationNumber().ClearPhone().ClearPhoneHash().ClearAltPhone().ClearEmail().ClearNationalIDEnc().
			ClearKraPinEnc().ClearNationality().ClearPostalAddress().ClearAuthUserID().ClearCrmContactID().
			ClearTermsAcceptedVersion().ClearTermsAcceptedAt().ClearInvitedAt().ClearNotes().ClearCustomFields().
			SetConsents(map[string]any{"erased_at": now, "privacy_request_id": req.ID.String()}).
			SetStatus("anonymised").Exec(ctx); err != nil {
			return err
		}
		// The account keeps its history; only the name and phone copied onto it go.
		if _, err := tx.UnitAccount.Update().Where(unitaccount.PrimaryPartyID(pid)).
			SetCustomerName(name).ClearCustomerPhone().Save(ctx); err != nil {
			return err
		}
		if _, err := tx.Vehicle.Delete().Where(vehicle.PartyID(pid)).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.VisitorPass.Update().Where(visitorpass.HostPartyID(pid), visitorpass.StatusEQ(visitorpass.StatusActive)).
			SetStatus(visitorpass.StatusCancelled).Save(ctx); err != nil {
			return err
		}
		// The requests themselves stay as the record of what was asked and done, without the name.
		if _, err := tx.PrivacyRequest.Update().Where(privacyrequest.PartyID(pid)).
			ClearRequesterName().ClearRequesterPhone().Save(ctx); err != nil {
			return err
		}
		return nil
	}()
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	req, err = s.client.PrivacyRequest.Get(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	return s.close(ctx, req, by, privacyrequest.StatusCompleted, "outcome", "details erased; financial records kept")
}

// PartyAnonymised is published, with ids only, when an erased party had a CRM contact. The
// contact is left in the CRM for its owner to decide on; maskani only drops the link.
const PartyAnonymised = "privacy.party_anonymised"
