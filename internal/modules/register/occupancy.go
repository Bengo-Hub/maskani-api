package register

import (
	"context"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/maskaniuseroutlet"
	"github.com/bengobox/maskani-api/internal/ent/meter"
	"github.com/bengobox/maskani-api/internal/ent/tenantsetting"
	"github.com/bengobox/maskani-api/internal/ent/unitaccount"
	"github.com/bengobox/maskani-api/internal/ent/unitparty"
	"github.com/bengobox/maskani-api/internal/ent/visitorpass"
	"github.com/bengobox/maskani-api/internal/events"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

// livesThere are the roles whose leaving needs a final meter reading (SRDD 8.3).
var livesThere = map[unitparty.Role]bool{unitparty.RoleOwner: true, unitparty.RoleJointOwner: true,
	unitparty.RoleBuyer: true, unitparty.RoleOccupant: true, unitparty.RoleLandlord: true}

// billedRoles may hold a unit account (the party its bills go to).
var billedRoles = []unitparty.Role{unitparty.RoleOwner, unitparty.RoleJointOwner, unitparty.RoleBuyer, unitparty.RoleLandlord}

// CloseLinks is the occupancy job (hourly; each link is handled once). Links whose end date has
// passed are ended; then each ended link's visitor passes are cancelled, a final reading is asked
// for when someone who lived there or owned it leaves a metered unit, and accounts billed to them
// move to the unit's remaining owner. Bill-to assignments on ended links are cleared once the
// estate's revert days have passed. Bounded by limit links per tenant per run.
func (s *Service) CloseLinks(ctx context.Context, tenants []uuid.UUID, now time.Time, limit int) (int, error) {
	closed := 0
	for _, tid := range tenants {
		tctx := tenantguard.With(ctx, tid)
		due, err := s.client.UnitParty.Query().Where(unitparty.StatusEQ(unitparty.StatusActive), unitparty.EndDateLTE(now)).
			Limit(limit).All(tctx)
		if err != nil {
			return closed, err
		}
		for _, l := range due {
			// Claim the link first, so two pods never run the effects twice.
			n, err := s.client.UnitParty.Update().Where(unitparty.ID(l.ID), unitparty.StatusEQ(unitparty.StatusActive)).
				SetStatus(unitparty.StatusEnded).Save(tctx)
			if err != nil || n == 0 {
				continue
			}
			if err := s.afterLinkEnded(tctx, tid, l, now); err != nil {
				s.log.Warn("occupancy: link end effects", zap.String("link", l.ID.String()), zap.Error(err))
			}
			closed++
		}
		if err := s.revertBillTo(tctx, now); err != nil {
			s.log.Warn("occupancy: bill-to revert", zap.String("tenant", tid.String()), zap.Error(err))
		}
	}
	return closed, nil
}

// afterLinkEnded applies what leaving a unit means: passes stop, a final reading is asked for, and
// accounts billed to the party move to the unit's remaining owner.
func (s *Service) afterLinkEnded(ctx context.Context, tenantID uuid.UUID, l *ent.UnitParty, now time.Time) error {
	if _, err := s.client.VisitorPass.Update().Where(visitorpass.UnitID(l.UnitID), visitorpass.HostPartyID(l.PartyID),
		visitorpass.StatusEQ(visitorpass.StatusActive), visitorpass.ValidToGT(now)).
		SetStatus(visitorpass.StatusCancelled).Save(ctx); err != nil {
		return err
	}
	if err := s.repointAccounts(ctx, l); err != nil {
		return err
	}
	if !livesThere[l.Role] {
		return nil
	}
	m, err := s.client.Meter.Query().Where(meter.UnitID(l.UnitID)).First(ctx)
	if ent.IsNotFound(err) {
		return nil // nothing metered
	}
	if err != nil {
		return err
	}
	u, err := s.client.Unit.Get(ctx, l.UnitID)
	if err != nil {
		return err
	}
	payload := map[string]any{"unit_id": u.ID, "unit_code": u.Code, "meter_id": m.ID, "meter_serial": m.Serial,
		"property_id": u.PropertyID, "role": string(l.Role), "left_on": now.Format("2006-01-02")}
	if rs, name, err := PropertyResponders(ctx, s.client, u.PropertyID, []maskaniuseroutlet.PropertyRole{
		maskaniuseroutlet.PropertyRoleCaretaker, maskaniuseroutlet.PropertyRolePropertyManager}, 10); err == nil {
		payload["property"] = name
		if len(rs) > 0 {
			payload["responders"] = rs
		}
	}
	return events.Publish(ctx, s.client.OutboxEvent, tenantID, l.ID.String(), events.FinalReadingNeeded, payload)
}

// repointAccounts moves the unit's accounts billed to the departed party to its remaining primary
// (or first) owner, so bills, reminders and receipts reach someone who is still there. With no owner
// left the account keeps its last contact until a new owner is linked.
func (s *Service) repointAccounts(ctx context.Context, l *ent.UnitParty) error {
	accs, err := s.client.UnitAccount.Query().Where(unitaccount.UnitID(l.UnitID), unitaccount.PrimaryPartyID(l.PartyID)).All(ctx)
	if err != nil || len(accs) == 0 {
		return err
	}
	next, err := s.client.UnitParty.Query().Where(unitparty.UnitID(l.UnitID), unitparty.PartyIDNEQ(l.PartyID),
		unitparty.RoleIn(billedRoles...), ActiveLink()).
		Order(ent.Desc(unitparty.FieldIsPrimary), ent.Asc(unitparty.FieldStartDate)).WithParty().First(ctx)
	if ent.IsNotFound(err) {
		return nil
	}
	if err != nil || next.Edges.Party == nil {
		return err
	}
	p := next.Edges.Party
	for _, a := range accs {
		meta := map[string]any{}
		for k, v := range a.Metadata {
			meta[k] = v
		}
		if p.Email != "" {
			meta["customer_email"] = p.Email
		} else {
			delete(meta, "customer_email")
		}
		if err := a.Update().SetPrimaryPartyID(p.ID).SetCustomerName(p.DisplayName).SetCustomerPhone(p.Phone).
			SetMetadata(meta).Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}

// revertBillTo clears the charges assigned to occupants who left more than the estate's revert days
// ago (the window lets them settle a final bill), so a later link never inherits them.
func (s *Service) revertBillTo(ctx context.Context, now time.Time) error {
	days := 30
	if st, err := s.client.TenantSetting.Query().Select(tenantsetting.FieldBillToRevertDays).Only(ctx); err == nil {
		days = st.BillToRevertDays
	}
	links, err := s.client.UnitParty.Query().Where(unitparty.StatusEQ(unitparty.StatusEnded), unitparty.BillToNotNil(),
		unitparty.EndDateLTE(now.AddDate(0, 0, -days))).Limit(500).All(ctx)
	if err != nil {
		return err
	}
	for _, l := range links {
		if len(l.BillTo) == 0 {
			continue
		}
		revert := days
		if l.RevertAfterDays != nil {
			revert = *l.RevertAfterDays
		}
		if l.EndDate != nil && l.EndDate.AddDate(0, 0, revert).After(now) {
			continue
		}
		if err := l.Update().ClearBillTo().Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}
