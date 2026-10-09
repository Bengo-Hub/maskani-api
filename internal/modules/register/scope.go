package register

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/ent/enquiry"
	"github.com/bengobox/maskani-api/internal/ent/instalment"
	"github.com/bengobox/maskani-api/internal/ent/maskaniuseroutlet"
	"github.com/bengobox/maskani-api/internal/ent/meter"
	"github.com/bengobox/maskani-api/internal/ent/meterreading"
	"github.com/bengobox/maskani-api/internal/ent/notice"
	"github.com/bengobox/maskani-api/internal/ent/property"
	"github.com/bengobox/maskani-api/internal/ent/salecontract"
	"github.com/bengobox/maskani-api/internal/ent/unitparty"
	"github.com/bengobox/maskani-api/internal/ent/workorder"
)

// Record names a kind of record whose property decides who may act on it.
type Record string

const (
	RecordUnit       Record = "unit"
	RecordAccount    Record = "unit_account"
	RecordContract   Record = "sale_contract"
	RecordInstalment Record = "instalment"
	RecordMeter      Record = "meter"
	RecordReading    Record = "meter_reading"
	RecordWorkOrder  Record = "work_order"
	RecordNotice     Record = "notice"
	RecordEnquiry    Record = "enquiry"
	RecordUnitParty  Record = "unit_party"
	RecordStaff      Record = "staff_assignment"
)

// PropertyOf returns the property a record belongs to, for property-scope checks. Each lookup is one
// or two primary-key reads. uuid.Nil means the record is tenant wide (an estate-wide notice), which
// only users with every property may touch.
func (s *Service) PropertyOf(ctx context.Context, kind Record, id uuid.UUID) (uuid.UUID, error) {
	switch kind {
	case RecordUnit:
		return s.UnitPropertyID(ctx, id)
	case RecordAccount:
		return s.AccountPropertyID(ctx, id)
	case RecordContract:
		c, err := s.client.SaleContract.Query().Where(salecontract.ID(id)).Select(salecontract.FieldPropertyID).Only(ctx)
		if err != nil {
			return uuid.Nil, err
		}
		return c.PropertyID, nil
	case RecordInstalment:
		in, err := s.client.Instalment.Query().Where(instalment.ID(id)).Select(instalment.FieldContractID).Only(ctx)
		if err != nil {
			return uuid.Nil, err
		}
		return s.PropertyOf(ctx, RecordContract, in.ContractID)
	case RecordMeter:
		m, err := s.client.Meter.Query().Where(meter.ID(id)).Select(meter.FieldPropertyID).Only(ctx)
		if err != nil {
			return uuid.Nil, err
		}
		return m.PropertyID, nil
	case RecordReading:
		rd, err := s.client.MeterReading.Query().Where(meterreading.ID(id)).Select(meterreading.FieldMeterID).Only(ctx)
		if err != nil {
			return uuid.Nil, err
		}
		return s.PropertyOf(ctx, RecordMeter, rd.MeterID)
	case RecordWorkOrder:
		wo, err := s.client.WorkOrder.Query().Where(workorder.ID(id)).Select(workorder.FieldPropertyID).Only(ctx)
		if err != nil {
			return uuid.Nil, err
		}
		return wo.PropertyID, nil
	case RecordNotice:
		n, err := s.client.Notice.Query().Where(notice.ID(id)).Select(notice.FieldPropertyID).Only(ctx)
		if err != nil {
			return uuid.Nil, err
		}
		if n.PropertyID == nil {
			return uuid.Nil, nil
		}
		return *n.PropertyID, nil
	case RecordEnquiry:
		e, err := s.client.Enquiry.Query().Where(enquiry.ID(id)).Select(enquiry.FieldPropertyID).Only(ctx)
		if err != nil {
			return uuid.Nil, err
		}
		return e.PropertyID, nil
	case RecordUnitParty:
		l, err := s.client.UnitParty.Query().Where(unitparty.ID(id)).Select(unitparty.FieldUnitID).Only(ctx)
		if err != nil {
			return uuid.Nil, err
		}
		return s.UnitPropertyID(ctx, l.UnitID)
	case RecordStaff:
		a, err := s.client.MaskaniUserOutlet.Query().Where(maskaniuseroutlet.ID(id)).Select(maskaniuseroutlet.FieldOutletID).Only(ctx)
		if err != nil {
			return uuid.Nil, err
		}
		p, err := s.client.Property.Query().Where(property.OutletID(a.OutletID)).Select(property.FieldID).Only(ctx)
		if err != nil {
			return uuid.Nil, err
		}
		return p.ID, nil
	}
	return uuid.Nil, fmt.Errorf("register: unknown record kind %q", kind)
}

// PartyPropertyIDs returns the properties a party is linked to through any unit link, ended or not,
// so staff of those properties can read and edit the party. A party with no links belongs to no
// property and only all-property staff can touch it.
func (s *Service) PartyPropertyIDs(ctx context.Context, partyID uuid.UUID) ([]uuid.UUID, error) {
	var rows []struct {
		PropertyID uuid.UUID `json:"property_id"`
	}
	err := s.client.UnitParty.Query().
		Where(unitparty.PartyID(partyID)).
		QueryUnit().
		GroupBy("property_id").
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	out := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.PropertyID)
	}
	return out, nil
}
