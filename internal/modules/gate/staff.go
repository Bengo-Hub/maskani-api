package gate

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/gatedevice"
	"github.com/bengobox/maskani-api/internal/ent/visitorpass"
	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/shared/page"
)

// PassView is a pass as staff screens list it: with its unit and block spelled out.
type PassView struct {
	*ent.VisitorPass
	UnitCode string `json:"unit_code,omitempty"`
	Block    string `json:"block,omitempty"`
}

// passViews adds unit codes and blocks to a page of passes in one or two queries.
func (s *Service) passViews(ctx context.Context, rows []*ent.VisitorPass) []PassView {
	codes := s.unitCodes(ctx, func(add func(uuid.UUID)) {
		for _, p := range rows {
			if p.UnitID != nil {
				add(*p.UnitID)
			}
		}
	})
	out := make([]PassView, len(rows))
	for i, p := range rows {
		out[i] = PassView{VisitorPass: p}
		if p.UnitID != nil {
			out[i].UnitCode, out[i].Block = codes[*p.UnitID].code, codes[*p.UnitID].block
		}
	}
	return out
}

// PagePassViews is PagePasses with units and blocks for the staff list.
func (s *Service) PagePassViews(ctx context.Context, f ScopeFilter, activeOnly bool, p page.Params) (page.Result[PassView], error) {
	res, err := s.PagePasses(ctx, f, activeOnly, p)
	if err != nil {
		return page.Result[PassView]{}, err
	}
	return page.Result[PassView]{Data: s.passViews(ctx, res.Data), NextCursor: res.NextCursor, HasMore: res.HasMore}, nil
}

// Pass returns one pass with its unit and block.
func (s *Service) Pass(ctx context.Context, id uuid.UUID) (*PassView, error) {
	p, err := s.client.VisitorPass.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	v := s.passViews(ctx, []*ent.VisitorPass{p})[0]
	return &v, nil
}

// CancelActivePass cancels a pass that can still be used; a used, expired or cancelled one is a
// conflict rather than a silent no-op.
func (s *Service) CancelActivePass(ctx context.Context, id uuid.UUID) error {
	n, err := s.client.VisitorPass.Update().Where(visitorpass.ID(id), visitorpass.StatusEQ(visitorpass.StatusActive)).
		SetStatus(visitorpass.StatusCancelled).Save(ctx)
	if err != nil {
		return err
	}
	if n == 0 {
		return httpx.Conflict("this pass is no longer active")
	}
	return nil
}

// DeviceView is a registered tablet with whether it has been seen lately.
type DeviceView struct {
	ID         uuid.UUID  `json:"id"`
	PropertyID uuid.UUID  `json:"property_id"`
	Name       string     `json:"name"`
	GateName   string     `json:"gate_name"`
	Status     string     `json:"status"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
	Online     bool       `json:"online"`
	AppVersion string     `json:"app_version,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// Devices lists a property's gate tablets, active first.
func (s *Service) Devices(ctx context.Context, propertyID uuid.UUID) ([]DeviceView, error) {
	rows, err := s.client.GateDevice.Query().Where(gatedevice.PropertyID(propertyID)).
		Order(ent.Asc(gatedevice.FieldStatus), ent.Asc(gatedevice.FieldName)).Limit(200).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]DeviceView, len(rows))
	for i, d := range rows {
		out[i] = DeviceView{ID: d.ID, PropertyID: d.PropertyID, Name: d.Name, GateName: d.GateName, Status: string(d.Status),
			LastSeenAt: d.LastSeenAt, AppVersion: d.AppVersion, CreatedAt: d.CreatedAt,
			Online: d.LastSeenAt != nil && time.Since(*d.LastSeenAt) < 15*time.Minute}
	}
	return out, nil
}

// RevokeDevice stops a tablet's key from working (lost or replaced tablet).
func (s *Service) RevokeDevice(ctx context.Context, id uuid.UUID) error {
	return s.client.GateDevice.UpdateOneID(id).SetStatus(gatedevice.StatusRevoked).Exec(ctx)
}

// DevicePropertyID returns the property a tablet belongs to (for scope checks).
func (s *Service) DevicePropertyID(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	d, err := s.client.GateDevice.Query().Where(gatedevice.ID(id)).Select(gatedevice.FieldPropertyID).Only(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	return d.PropertyID, nil
}
