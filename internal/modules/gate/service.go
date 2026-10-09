// Package gate runs visitor passes, gate tablets, entry logs, walk-in approvals and incidents
// (SRDD section 15). Pass codes and device keys are stored only as keyed hashes; the gate collects
// the minimum the law allows.
package gate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/sqljson"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/gatedevice"
	"github.com/bengobox/maskani-api/internal/ent/gateevent"
	"github.com/bengobox/maskani-api/internal/ent/incident"
	"github.com/bengobox/maskani-api/internal/ent/party"
	"github.com/bengobox/maskani-api/internal/ent/unit"
	"github.com/bengobox/maskani-api/internal/ent/vendorpersonnel"
	"github.com/bengobox/maskani-api/internal/ent/visitorpass"
	"github.com/bengobox/maskani-api/internal/events"
	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/sequence"
	"github.com/bengobox/maskani-api/internal/modules/settings"
	"github.com/bengobox/maskani-api/internal/platform/realtime"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
	"github.com/bengobox/maskani-api/internal/shared/page"
	"github.com/bengobox/maskani-api/internal/shared/richtext"
	"github.com/bengobox/maskani-api/internal/shared/secure"
)

// Retention of gate events (SRDD 15.2).
const Retention = 90 * 24 * time.Hour

// Service is the gate service.
type Service struct {
	client *ent.Client
	box    *secure.Box
	seq    *sequence.Allocator
	log    *zap.Logger
	rt     realtime.Publisher
}

// SetRealtime sets the publisher for gate hints (nil disables them).
func (s *Service) SetRealtime(p realtime.Publisher) { s.rt = p }

func (s *Service) emit(ctx context.Context, typ string, ev *ent.GateEvent) {
	tenantID, _ := tenantguard.TenantID(ctx)
	realtime.Emit(s.rt, tenantID, realtime.Event{Type: typ, ID: ev.ID.String(), PropertyID: ev.PropertyID.String(),
		UnitID: realtime.IDString(ev.HostUnitID)})
}

// NewService creates the gate service.
func NewService(client *ent.Client, box *secure.Box, seq *sequence.Allocator, log *zap.Logger) *Service {
	return &Service{client: client, box: box, seq: seq, log: log.Named("gate")}
}

func (s *Service) codeHash(propertyID uuid.UUID, code string) string {
	return s.box.Hash("pass:" + propertyID.String() + ":" + strings.TrimSpace(code))
}

// RegisterDevice registers a tablet for a property and returns its key once.
func (s *Service) RegisterDevice(ctx context.Context, propertyID, by uuid.UUID, name, gateName string) (*ent.GateDevice, string, error) {
	key, err := secure.RandomToken(32)
	if err != nil {
		return nil, "", err
	}
	if gateName == "" {
		gateName = "Main gate"
	}
	d, err := s.client.GateDevice.Create().SetPropertyID(propertyID).SetName(name).SetGateName(gateName).
		SetDeviceKeyHash(s.box.Hash("device:" + key)).SetRegisteredBy(by).Save(ctx)
	return d, key, err
}

// Device resolves a device key (system lookup) and returns the device with a tenant-scoped context.
func (s *Service) Device(ctx context.Context, key string) (*ent.GateDevice, context.Context, error) {
	if key == "" {
		return nil, ctx, httpx.Forbidden("device key required")
	}
	d, err := s.client.GateDevice.Query().
		Where(gatedevice.DeviceKeyHash(s.box.Hash("device:"+key)), gatedevice.StatusEQ(gatedevice.StatusActive)).
		Only(tenantguard.System(ctx))
	if err != nil {
		return nil, ctx, httpx.Forbidden("unknown or revoked device")
	}
	tctx := tenantguard.With(ctx, d.TenantID)
	// A busy tablet calls many times a minute; last-seen only needs minute precision for the
	// 15-minute offline alert, so skip the write when it is fresh.
	if d.LastSeenAt == nil || time.Since(*d.LastSeenAt) > time.Minute || d.OfflineAlerted {
		_ = s.client.GateDevice.UpdateOneID(d.ID).SetLastSeenAt(time.Now()).SetOfflineAlerted(false).Exec(tctx)
	}
	return d, tctx, nil
}

// PassInput creates a pass.
type PassInput struct {
	PropertyID   uuid.UUID      `json:"property_id"`
	UnitID       *uuid.UUID     `json:"unit_id"`
	PassType     string         `json:"pass_type"`
	VisitorName  string         `json:"visitor_name"`
	VisitorPhone string         `json:"visitor_phone"`
	VehiclePlate string         `json:"vehicle_plate"`
	ValidFrom    *time.Time     `json:"valid_from"`
	ValidTo      *time.Time     `json:"valid_to"`
	Recurrence   map[string]any `json:"recurrence"`
	MaxEntries   int            `json:"max_entries"`
	WorkOrderID  *uuid.UUID     `json:"work_order_id"`
	Notes        string         `json:"notes"`
}

// IssuedPass returns the code and QR token once, at creation.
type IssuedPass struct {
	*ent.VisitorPass
	Code    string `json:"code"`
	QRToken string `json:"qr_token"`
}

// CreatePass issues a pass with a unique 6-digit code for the property and a QR token.
func (s *Service) CreatePass(ctx context.Context, createdByKind string, createdBy uuid.UUID, hostParty *uuid.UUID, in PassInput) (*IssuedPass, error) {
	if strings.TrimSpace(in.VisitorName) == "" {
		return nil, httpx.Invalid("visitor name is required")
	}
	pt := visitorpass.PassType(in.PassType)
	if in.PassType == "" {
		pt = visitorpass.PassTypeGuestSingle
	}
	if err := visitorpass.PassTypeValidator(pt); err != nil {
		return nil, httpx.Invalid("invalid pass type")
	}
	from := time.Now()
	if in.ValidFrom != nil {
		from = *in.ValidFrom
	}
	to := from.Add(12 * time.Hour)
	switch pt {
	case visitorpass.PassTypeDelivery:
		to = from.Add(2 * time.Hour)
	case visitorpass.PassTypeGuestRecurring, visitorpass.PassTypeDomesticStaff:
		to = from.AddDate(0, 0, 90)
	case visitorpass.PassTypeContractor, visitorpass.PassTypeAgency:
		to = from.AddDate(0, 0, 30)
	}
	if in.ValidTo != nil {
		to = *in.ValidTo
	}
	if !to.After(from) || to.Sub(from) > 91*24*time.Hour {
		return nil, httpx.Invalid("validity must be after the start and at most 90 days")
	}
	// The unit must be at the pass's property (the gate shows unit and block from it).
	if in.UnitID != nil {
		if ok, err := s.client.Unit.Query().Where(unit.ID(*in.UnitID), unit.PropertyID(in.PropertyID)).Exist(ctx); err != nil || !ok {
			return nil, httpx.Invalid("the unit is not at this property")
		}
	}
	// A returning visitor is recognised now, so the gate shows their history when the code is used.
	var visitorID *uuid.UUID
	if v, err := s.ResolveVisitor(ctx, in.PropertyID, VisitorDetails{Name: in.VisitorName, Phone: in.VisitorPhone,
		Plate: in.VehiclePlate, HostUnit: in.UnitID}); err == nil && v != nil {
		visitorID = &v.ID
	}
	maxEntries := in.MaxEntries
	if maxEntries <= 0 {
		maxEntries = 1
		if pt != visitorpass.PassTypeGuestSingle && pt != visitorpass.PassTypeDelivery {
			maxEntries = 0
		}
	}
	var code, hash string
	for i := 0; i < 5; i++ {
		c, err := secure.RandomDigits(6)
		if err != nil {
			return nil, err
		}
		h := s.codeHash(in.PropertyID, c)
		if exists, _ := s.client.VisitorPass.Query().Where(visitorpass.CodeHash(h), visitorpass.StatusEQ(visitorpass.StatusActive)).Exist(ctx); !exists {
			code, hash = c, h
			break
		}
	}
	if code == "" {
		return nil, httpx.Conflict("could not allocate a pass code, try again")
	}
	qr, err := secure.RandomToken(16)
	if err != nil {
		return nil, err
	}
	// The code and QR token are also kept encrypted so Sync can hand tablets device-salted hashes
	// for offline verification; the plain values are never stored or returned again.
	codeEnc, _ := s.box.Encrypt(code)
	qrEnc, _ := s.box.Encrypt(qr)
	c := s.client.VisitorPass.Create().SetMetadata(map[string]any{"code_enc": codeEnc, "qr_enc": qrEnc}).SetPropertyID(in.PropertyID).SetPassType(pt).
		SetCreatedByKind(visitorpass.CreatedByKind(createdByKind)).SetCreatedByID(createdBy).
		SetVisitorName(strings.TrimSpace(in.VisitorName)).SetVisitorPhone(secure.NormalizePhone(in.VisitorPhone)).
		SetVehiclePlate(strings.ToUpper(strings.ReplaceAll(in.VehiclePlate, " ", ""))).
		SetCodeHash(hash).SetCodeHint(code[4:]).SetQrTokenHash(s.box.Hash("qr:" + qr)).
		SetValidFrom(from).SetValidTo(to).SetMaxEntries(maxEntries).SetNotes(in.Notes).SetNillableVisitorID(visitorID)
	if in.UnitID != nil {
		c.SetUnitID(*in.UnitID)
	}
	if hostParty != nil {
		c.SetHostPartyID(*hostParty)
	}
	if in.Recurrence != nil {
		c.SetRecurrence(in.Recurrence)
	}
	if in.WorkOrderID != nil {
		c.SetWorkOrderID(*in.WorkOrderID)
	}
	p, err := c.Save(ctx)
	if err != nil {
		return nil, err
	}
	tenantID, _ := tenantguard.TenantID(ctx)
	_ = events.Publish(ctx, s.client.OutboxEvent, tenantID, p.ID.String(), events.PassCreated, map[string]any{
		"pass_id": p.ID, "visitor_name": p.VisitorName, "visitor_phone": p.VisitorPhone, "code": code,
		"valid_from": p.ValidFrom, "valid_to": p.ValidTo, "property_id": p.PropertyID,
	})
	return &IssuedPass{VisitorPass: p, Code: code, QRToken: qr}, nil
}

// CancelPass cancels a pass.
func (s *Service) CancelPass(ctx context.Context, id uuid.UUID) error {
	return s.client.VisitorPass.UpdateOneID(id).SetStatus(visitorpass.StatusCancelled).Exec(ctx)
}

// CancelHostPass cancels a pass only when one of the host parties issued it: one conditional
// update, and not found when the pass is someone else's.
func (s *Service) CancelHostPass(ctx context.Context, id uuid.UUID, hostParties []uuid.UUID) error {
	n, err := s.client.VisitorPass.Update().
		Where(visitorpass.ID(id), visitorpass.HostPartyIDIn(hostParties...)).
		SetStatus(visitorpass.StatusCancelled).Save(ctx)
	if err != nil {
		return err
	}
	if n == 0 {
		return &ent.NotFoundError{}
	}
	return nil
}

// VerifyResult is what the tablet shows: the pass, where the visitor is going and who they are
// visiting, and the returning visitor's history (a banned visitor is flagged for the guard).
type VerifyResult struct {
	Valid    bool             `json:"valid"`
	Reason   string           `json:"reason,omitempty"`
	Pass     *ent.VisitorPass `json:"pass,omitempty"`
	UnitCode string           `json:"unit_code,omitempty"`
	Block    string           `json:"block,omitempty"`
	HostName string           `json:"host_name,omitempty"`
	Visitor  *VisitorMatch    `json:"visitor,omitempty"`
}

// Verify checks a 6-digit code or QR token at the device's property.
func (s *Service) Verify(ctx context.Context, d *ent.GateDevice, code, qr string) (*VerifyResult, error) {
	q := s.client.VisitorPass.Query().Where(visitorpass.PropertyID(d.PropertyID))
	switch {
	case qr != "":
		q = q.Where(visitorpass.QrTokenHash(s.box.Hash("qr:" + qr)))
	case code != "":
		q = q.Where(visitorpass.CodeHash(s.codeHash(d.PropertyID, code)))
	default:
		return nil, httpx.Invalid("code or qr is required")
	}
	p, err := q.Order(ent.Desc(visitorpass.FieldCreatedAt)).First(ctx)
	if ent.IsNotFound(err) {
		return &VerifyResult{Valid: false, Reason: "no such pass"}, nil
	}
	if err != nil {
		return nil, err
	}
	res := &VerifyResult{Pass: p}
	if p.UnitID != nil {
		info := s.unitCodes(ctx, func(add func(uuid.UUID)) { add(*p.UnitID) })[*p.UnitID]
		res.UnitCode, res.Block = info.code, info.block
	}
	if p.HostPartyID != nil {
		if h, err := s.client.Party.Query().Where(party.ID(*p.HostPartyID)).Select(party.FieldDisplayName).Only(ctx); err == nil {
			res.HostName = h.DisplayName
		}
	} else if p.UnitID != nil {
		res.HostName = s.unitHosts(ctx, []uuid.UUID{*p.UnitID})[*p.UnitID].Name
	}
	if p.VisitorID != nil {
		if v, err := s.client.Visitor.Get(ctx, *p.VisitorID); err == nil {
			res.Visitor = &VisitorMatch{ID: v.ID, Name: v.Name, Plate: v.VehiclePlate, Visits: v.Visits, LastAt: v.LastVisitAt,
				Banned: v.Status == "banned", Notes: v.Notes}
		}
	}
	now := time.Now()
	switch {
	case p.Status == visitorpass.StatusCancelled:
		res.Reason = "pass cancelled"
	case p.Status == visitorpass.StatusUsed || (p.MaxEntries > 0 && p.EntriesUsed >= p.MaxEntries):
		res.Reason = "pass already used"
	case now.Before(p.ValidFrom):
		res.Reason = "pass not yet valid"
	case now.After(p.ValidTo):
		res.Reason = "pass expired"
	case !withinRecurrence(p.Recurrence, now):
		res.Reason = "outside the allowed days or hours"
	default:
		res.Valid = true
	}
	return res, nil
}

// withinRecurrence checks {"days":[1..7 or 0..6],"from":"08:00","to":"17:00"}; empty = always.
func withinRecurrence(rec map[string]any, now time.Time) bool {
	if len(rec) == 0 {
		return true
	}
	if days, ok := rec["days"].([]any); ok && len(days) > 0 {
		wd := float64(now.Weekday())
		hit := false
		for _, d := range days {
			if v, ok := d.(float64); ok && (v == wd || (v == 7 && wd == 0)) {
				hit = true
			}
		}
		if !hit {
			return false
		}
	}
	hm := now.Format("15:04")
	if from, ok := rec["from"].(string); ok && from != "" && hm < from {
		return false
	}
	if to, ok := rec["to"].(string); ok && to != "" && hm > to {
		return false
	}
	return true
}

// EventInput is one gate event from a tablet (online or replayed from the offline queue).
type EventInput struct {
	ClientEventID string     `json:"client_event_id"`
	Kind          string     `json:"kind"`
	PassID        *uuid.UUID `json:"pass_id"`
	VisitorName   string     `json:"visitor_name"`
	VisitorPhone  string     `json:"visitor_phone"`
	HostUnitID    *uuid.UUID `json:"host_unit_id"`
	VehiclePlate  string     `json:"vehicle_plate"`
	IDSighted     bool       `json:"id_sighted"`
	OccurredAt    time.Time  `json:"occurred_at"`
	Offline       bool       `json:"offline"`
	GuardID       *uuid.UUID `json:"guard_personnel_id"`
	Notes         string     `json:"notes"`
	// IDNumber is the visitor's ID as seen at the gate; only its keyed hash is kept, on the visitor.
	IDNumber string `json:"id_number"`
	// An exit names the entry it closes: the server id, or the tablet's own id for an entry still
	// in the same offline batch. An exit that closes nothing is not recorded.
	EntryEventID       *uuid.UUID `json:"entry_event_id"`
	EntryClientEventID string     `json:"entry_client_event_id"`
}

// Record stores events idempotently on (device, client_event_id). Entries against a pass consume it
// and notify the host.
//
// A tablet replays its offline queue in one call, so the whole batch costs a fixed number of
// queries: one to find events already stored, one bulk insert, one read back, one update per
// distinct pass used, and one read each for the units, passes and hosts the messages need.
func (s *Service) Record(ctx context.Context, d *ent.GateDevice, batch []EventInput) (int, error) {
	tenantID, _ := tenantguard.TenantID(ctx)
	seen := map[string]bool{}
	ids := make([]string, 0, len(batch))
	for _, in := range batch {
		if in.ClientEventID != "" && in.Kind != "" && !seen[in.ClientEventID] {
			seen[in.ClientEventID] = true
			ids = append(ids, in.ClientEventID)
		}
	}
	if len(ids) == 0 {
		return 0, nil
	}
	known, err := s.client.GateEvent.Query().Where(gateevent.DeviceID(d.ID), gateevent.ClientEventIDIn(ids...)).
		Select(gateevent.FieldClientEventID).Strings(ctx)
	if err != nil {
		return 0, err
	}
	stored := map[string]bool{}
	for _, k := range known {
		stored[k] = true
	}
	// Passes the batch refers to, read once: an entry by pass takes the pass's unit, visitor and
	// name, so the log always says who came and for which unit.
	passes := s.batchPasses(ctx, batch)

	rows := make([]*ent.GateEventCreate, 0, len(ids))
	fresh := make([]string, 0, len(ids))
	var exits []EventInput
	for _, in := range batch {
		if in.ClientEventID == "" || in.Kind == "" || stored[in.ClientEventID] {
			continue
		}
		stored[in.ClientEventID] = true
		if in.OccurredAt.IsZero() || in.OccurredAt.After(time.Now().Add(5*time.Minute)) {
			in.OccurredAt = time.Now()
		}
		if in.Kind == string(gateevent.KindExit) {
			exits = append(exits, in) // after the entries, so an exit can close an entry from the same batch
			continue
		}
		c := s.client.GateEvent.Create().SetPropertyID(d.PropertyID).SetDeviceID(d.ID).SetKind(gateevent.Kind(in.Kind)).
			SetClientEventID(in.ClientEventID).SetVisitorPhone(secure.NormalizePhone(in.VisitorPhone)).
			SetVehiclePlate(normPlate(in.VehiclePlate)).SetIDSighted(in.IDSighted).
			SetOccurredAt(in.OccurredAt).SetOffline(in.Offline).SetNotes(in.Notes)
		name, hostUnit := strings.TrimSpace(in.VisitorName), in.HostUnitID
		var visitorID *uuid.UUID
		if in.PassID != nil {
			c.SetPassID(*in.PassID)
			if p := passes[*in.PassID]; p != nil {
				if name == "" {
					name = p.VisitorName
				}
				if hostUnit == nil {
					hostUnit = p.UnitID
				}
				visitorID = p.VisitorID
			}
		}
		if visitorID == nil && (in.Kind == string(gateevent.KindEntry) || in.Kind == string(gateevent.KindWalkInRequest)) {
			if v, err := s.ResolveVisitor(ctx, d.PropertyID, VisitorDetails{Name: name, Phone: in.VisitorPhone,
				IDNumber: in.IDNumber, Plate: in.VehiclePlate, HostUnit: hostUnit, Visit: true}); err == nil && v != nil {
				visitorID = &v.ID
			}
		}
		c.SetVisitorName(name).SetNillableHostUnitID(hostUnit).SetNillableVisitorID(visitorID)
		if in.GuardID != nil {
			c.SetGuardPersonnelID(*in.GuardID)
		}
		if in.Kind == string(gateevent.KindWalkInRequest) {
			c.SetDecision(gateevent.DecisionPending)
		}
		rows = append(rows, c)
		fresh = append(fresh, in.ClientEventID)
	}
	if len(rows) > 0 {
		// A second replay racing this one hits the (tenant, device, client_event_id) key and is skipped.
		if err := s.client.GateEvent.CreateBulk(rows...).
			OnConflictColumns(gateevent.FieldTenantID, gateevent.FieldDeviceID, gateevent.FieldClientEventID).
			DoNothing().Exec(ctx); err != nil {
			return 0, err
		}
	}
	for _, in := range exits {
		if id, ok := s.recordExit(ctx, d, in); ok {
			fresh = append(fresh, id)
		}
	}
	if len(fresh) == 0 {
		return 0, nil
	}
	evs, err := s.client.GateEvent.Query().Where(gateevent.DeviceID(d.ID), gateevent.ClientEventIDIn(fresh...)).All(ctx)
	if err != nil {
		return 0, err
	}
	s.consumePasses(ctx, evs)
	look := s.arrivalLookups(ctx, evs)
	for _, ev := range evs {
		switch {
		case ev.Kind == gateevent.KindEntry && (ev.PassID != nil || ev.HostUnitID != nil):
			_ = events.Publish(ctx, s.client.OutboxEvent, tenantID, ev.ID.String(), events.VisitorArrived, look.payload(ev))
			s.emit(ctx, realtime.GateEvent, ev)
		case ev.Kind == gateevent.KindWalkInRequest:
			_ = events.Publish(ctx, s.client.OutboxEvent, tenantID, ev.ID.String(), events.WalkInRequested, look.payload(ev))
			s.emit(ctx, realtime.WalkInRequested, ev)
		default:
			s.emit(ctx, realtime.GateEvent, ev)
		}
	}
	return len(evs), nil
}

// batchPasses reads the passes a batch of events refers to.
func (s *Service) batchPasses(ctx context.Context, batch []EventInput) map[uuid.UUID]*ent.VisitorPass {
	out := map[uuid.UUID]*ent.VisitorPass{}
	var ids []uuid.UUID
	for _, in := range batch {
		if in.PassID != nil {
			ids = append(ids, *in.PassID)
		}
	}
	if len(ids) == 0 {
		return out
	}
	if ps, err := s.client.VisitorPass.Query().Where(visitorpass.IDIn(ids...)).All(ctx); err == nil {
		for _, p := range ps {
			out[p.ID] = p
		}
	}
	return out
}

// recordExit closes one entry and stores the exit with the leaver's details. The entry is the one
// named (by server id or the tablet's own id), else the latest person inside with the same number
// plate or name. An exit that closes nothing (a double tap, a replay, nobody named) is dropped.
func (s *Service) recordExit(ctx context.Context, d *ent.GateDevice, in EventInput) (string, bool) {
	entryID := in.EntryEventID
	if entryID == nil && in.EntryClientEventID != "" {
		if e, err := s.client.GateEvent.Query().Where(gateevent.DeviceID(d.ID), gateevent.ClientEventID(in.EntryClientEventID)).
			Only(ctx); err == nil {
			entryID = &e.ID
		}
	}
	if entryID == nil {
		plate, name := normPlate(in.VehiclePlate), strings.TrimSpace(in.VisitorName)
		if plate == "" && name == "" {
			return "", false
		}
		inside, err := s.Inside(ctx, d.PropertyID)
		if err != nil {
			return "", false
		}
		for _, p := range inside {
			if (plate != "" && p.Plate == plate) || (plate == "" && strings.EqualFold(p.Name, name)) {
				id := p.EventID
				entryID = &id
				break
			}
		}
		if entryID == nil {
			return "", false
		}
	}
	entry, ok, err := s.claimExit(ctx, d.PropertyID, *entryID, in.OccurredAt)
	if err != nil || !ok {
		return "", false
	}
	c := s.client.GateEvent.Create().SetPropertyID(d.PropertyID).SetDeviceID(d.ID).SetKind(gateevent.KindExit).
		SetClientEventID(in.ClientEventID).SetOccurredAt(in.OccurredAt).SetOffline(in.Offline).SetNotes(in.Notes).
		SetEntryEventID(entry.ID).SetVisitorName(entry.VisitorName).SetVisitorPhone(entry.VisitorPhone).
		SetVehiclePlate(entry.VehiclePlate).SetNillableHostUnitID(entry.HostUnitID).SetNillablePassID(entry.PassID).
		SetNillableVisitorID(entry.VisitorID)
	if in.GuardID != nil {
		c.SetGuardPersonnelID(*in.GuardID)
	}
	if err := c.OnConflictColumns(gateevent.FieldTenantID, gateevent.FieldDeviceID, gateevent.FieldClientEventID).
		DoNothing().Exec(ctx); err != nil {
		s.log.Warn("exit not stored", zap.Error(err))
		return "", false
	}
	return in.ClientEventID, true
}

// consumePasses counts entries against their passes: one increment per distinct pass, then one
// statement marks every pass that reached its entry limit as used.
func (s *Service) consumePasses(ctx context.Context, evs []*ent.GateEvent) {
	uses := map[uuid.UUID]int{}
	for _, ev := range evs {
		if ev.Kind == gateevent.KindEntry && ev.PassID != nil {
			uses[*ev.PassID]++
		}
	}
	if len(uses) == 0 {
		return
	}
	ids := make([]uuid.UUID, 0, len(uses))
	for id, n := range uses {
		ids = append(ids, id)
		_ = s.client.VisitorPass.UpdateOneID(id).AddEntriesUsed(n).Exec(ctx)
	}
	_ = s.client.VisitorPass.Update().Where(visitorpass.IDIn(ids...), visitorpass.MaxEntriesGT(0),
		func(sel *sql.Selector) {
			sel.Where(sql.ColumnsGTE(sel.C(visitorpass.FieldEntriesUsed), sel.C(visitorpass.FieldMaxEntries)))
		}).
		SetStatus(visitorpass.StatusUsed).Exec(ctx)
}

// arrivalInfo holds the units, passes and hosts a batch's host messages need, read once. unitHosts
// answers for walk-ins and units without a pass host: the resident who decides for the unit.
type arrivalInfo struct {
	units     map[uuid.UUID]*ent.Unit
	passes    map[uuid.UUID]*ent.VisitorPass
	hosts     map[uuid.UUID]*ent.Party
	unitHosts map[uuid.UUID]HostContact
}

func (s *Service) arrivalLookups(ctx context.Context, evs []*ent.GateEvent) arrivalInfo {
	info := arrivalInfo{units: map[uuid.UUID]*ent.Unit{}, passes: map[uuid.UUID]*ent.VisitorPass{}, hosts: map[uuid.UUID]*ent.Party{}}
	var unitIDs, passIDs, hostIDs []uuid.UUID
	for _, ev := range evs {
		if ev.HostUnitID != nil {
			unitIDs = append(unitIDs, *ev.HostUnitID)
		}
		if ev.PassID != nil {
			passIDs = append(passIDs, *ev.PassID)
		}
	}
	if len(unitIDs) > 0 {
		if us, err := s.client.Unit.Query().Where(unit.IDIn(unitIDs...)).All(ctx); err == nil {
			for _, u := range us {
				info.units[u.ID] = u
			}
		}
	}
	if len(passIDs) > 0 {
		if ps, err := s.client.VisitorPass.Query().Where(visitorpass.IDIn(passIDs...)).All(ctx); err == nil {
			for _, p := range ps {
				info.passes[p.ID] = p
				if p.HostPartyID != nil {
					hostIDs = append(hostIDs, *p.HostPartyID)
				}
			}
		}
	}
	if len(hostIDs) > 0 {
		if hs, err := s.client.Party.Query().Where(party.IDIn(hostIDs...)).All(ctx); err == nil {
			for _, h := range hs {
				info.hosts[h.ID] = h
			}
		}
	}
	// Units whose event has no pass host (walk-ins, entries by unit) get their resident host.
	var noHost []uuid.UUID
	for _, ev := range evs {
		if ev.HostUnitID == nil {
			continue
		}
		if ev.PassID != nil {
			if p := info.passes[*ev.PassID]; p != nil && p.HostPartyID != nil {
				continue
			}
		}
		noHost = append(noHost, *ev.HostUnitID)
	}
	info.unitHosts = s.unitHosts(ctx, noHost)
	return info
}

// payload is the host message data for one event (notifications needs the host's contact).
func (a arrivalInfo) payload(ev *ent.GateEvent) map[string]any {
	out := map[string]any{"event_id": ev.ID, "visitor_name": ev.VisitorName, "occurred_at": ev.OccurredAt,
		"property_id": ev.PropertyID, "kind": ev.Kind, "vehicle_plate": ev.VehiclePlate}
	if ev.HostUnitID != nil {
		out["host_unit_id"] = *ev.HostUnitID
		if u := a.units[*ev.HostUnitID]; u != nil {
			out["unit_code"] = u.Code
		}
	}
	if ev.PassID != nil {
		if p := a.passes[*ev.PassID]; p != nil && p.HostPartyID != nil {
			out["host_party_id"] = *p.HostPartyID
			if h := a.hosts[*p.HostPartyID]; h != nil {
				out["host_phone"], out["host_name"], out["host_email"] = h.Phone, h.DisplayName, h.Email
				if h.AuthUserID != nil {
					out["host_user_id"] = *h.AuthUserID
				}
			}
			return out
		}
	}
	if ev.HostUnitID != nil {
		if h, ok := a.unitHosts[*ev.HostUnitID]; ok {
			out["host_party_id"], out["host_phone"], out["host_name"], out["host_email"] = h.PartyID, h.Phone, h.Name, h.Email
			// notifications-api sends the push to this user's registered devices.
			if h.UserID != nil {
				out["host_user_id"] = *h.UserID
			}
		}
	}
	return out
}

// arrival builds the host message data for a single event (walk-in decisions outside a batch).
func (s *Service) arrival(ctx context.Context, ev *ent.GateEvent) map[string]any {
	evs := []*ent.GateEvent{ev}
	return s.arrivalLookups(ctx, evs).payload(ev)
}

// Event returns one gate event.
func (s *Service) Event(ctx context.Context, id uuid.UUID) (*ent.GateEvent, error) {
	return s.client.GateEvent.Get(ctx, id)
}

// Incident returns one incident (the deep link in incident alerts opens it).
func (s *Service) Incident(ctx context.Context, id uuid.UUID) (*ent.Incident, error) {
	return s.client.Incident.Get(ctx, id)
}

// DeviceEvent returns an event a tablet recorded, found by the server id or by the tablet's own
// client_event_id. Tablets only know the id they generated, so polling a walk-in uses that one.
func (s *Service) DeviceEvent(ctx context.Context, deviceID uuid.UUID, id string) (*ent.GateEvent, error) {
	if uid, err := uuid.Parse(id); err == nil {
		if ev, err := s.client.GateEvent.Query().
			Where(gateevent.ID(uid), gateevent.DeviceID(deviceID)).Only(ctx); err == nil {
			return ev, nil
		}
	}
	return s.client.GateEvent.Query().
		Where(gateevent.DeviceID(deviceID), gateevent.ClientEventID(id)).Only(ctx)
}

// Decide records the host's walk-in decision.
func (s *Service) Decide(ctx context.Context, eventID uuid.UUID, approve bool) (*ent.GateEvent, error) {
	ev, err := s.client.GateEvent.Get(ctx, eventID)
	if err != nil {
		return nil, err
	}
	if ev.Decision != gateevent.DecisionPending {
		return ev, nil
	}
	d := gateevent.DecisionDeclined
	if approve {
		d = gateevent.DecisionApproved
	}
	if time.Since(ev.OccurredAt) > 5*time.Minute {
		d = gateevent.DecisionTimeout
	}
	ev, err = ev.Update().SetDecision(d).SetDecidedAt(time.Now()).Save(ctx)
	if err != nil {
		return nil, err
	}
	s.emit(ctx, realtime.WalkInDecided, ev)
	return ev, nil
}

// SyncPayload is what a tablet caches for offline operation. WalkInPolicy tells it whether the guard
// may let a walk-in in (guard_decides, the default) or must wait for the host (ask_host).
type SyncPayload struct {
	ServerTime   time.Time     `json:"server_time"`
	Passes       []CachedPass  `json:"passes"`
	Badges       []CachedBadge `json:"badges"`
	WalkInPolicy string        `json:"walk_in_policy"`
}

// CachedBadge is an active badge deployed to the device's property, with the holder's name. The
// PIN hash never leaves the server; has_pin tells the tablet whether sign-on is possible.
type CachedBadge struct {
	ID          uuid.UUID `json:"id"`
	BadgeNumber string    `json:"badge_number"`
	Name        string    `json:"name"`
	Role        string    `json:"role,omitempty"`
	VendorID    uuid.UUID `json:"vendor_id"`
	HasPIN      bool      `json:"has_pin"`
}

// SignOnResult is returned to the tablet when a guard signs on.
type SignOnResult struct {
	Guard      SignOnGuard `json:"guard"`
	SignedOnAt time.Time   `json:"signed_on_at"`
}

// SignOnGuard identifies the guard on duty.
type SignOnGuard struct {
	ID    uuid.UUID `json:"id"`
	Name  string    `json:"name"`
	Badge string    `json:"badge"`
}

// ErrSignOn is the single answer for every failed sign-on, so a guesser learns nothing.
var ErrSignOn = httpx.Forbidden("badge or PIN is not correct")

// SignOn checks a guard's badge and PIN at the device's property. The tenant comes from the device
// context; personnel not deployed to the device's property are rejected.
func (s *Service) SignOn(ctx context.Context, d *ent.GateDevice, badge, pin string) (*SignOnResult, error) {
	badge = strings.TrimSpace(badge)
	if badge == "" || pin == "" {
		return nil, httpx.Invalid("badge and pin are required")
	}
	p, err := s.client.VendorPersonnel.Query().
		Where(vendorpersonnel.BadgeNumber(badge), vendorpersonnel.StatusEQ(vendorpersonnel.StatusActive)).Only(ctx)
	if err != nil || p.PinHash == "" || !deployedTo(p.PropertyIds, d.PropertyID) {
		return nil, ErrSignOn
	}
	if bcrypt.CompareHashAndPassword([]byte(p.PinHash), []byte(pin)) != nil {
		return nil, ErrSignOn
	}
	now := time.Now()
	meta := map[string]any{}
	for k, v := range p.Metadata {
		meta[k] = v
	}
	meta["last_sign_on"] = map[string]any{"at": now, "device_id": d.ID.String(), "property_id": d.PropertyID.String()}
	if err := p.Update().SetMetadata(meta).Exec(ctx); err != nil {
		s.log.Warn("sign-on record failed", zap.Error(err))
	}
	return &SignOnResult{Guard: SignOnGuard{ID: p.ID, Name: p.FullName, Badge: p.BadgeNumber}, SignedOnAt: now}, nil
}

func deployedTo(propertyIDs []string, property uuid.UUID) bool {
	for _, id := range propertyIDs {
		if id == property.String() {
			return true
		}
	}
	return false
}

// CachedPass carries only device-salted hashes: the tablet computes sha256(device_id + ":" + code)
// for what the visitor shows and compares, so a stolen cache is useless on any other device and
// holds no plain codes.
type CachedPass struct {
	ID          uuid.UUID      `json:"id"`
	CodeHash    string         `json:"code_hash"`
	QRHash      string         `json:"qr_hash"`
	VisitorName string         `json:"visitor_name"`
	UnitID      *uuid.UUID     `json:"unit_id,omitempty"`
	ValidFrom   time.Time      `json:"valid_from"`
	ValidTo     time.Time      `json:"valid_to"`
	Recurrence  map[string]any `json:"recurrence,omitempty"`
	MaxEntries  int            `json:"max_entries"`
	EntriesUsed int            `json:"entries_used"`
}

// Sync returns passes valid within the next 24 hours and active badges at the device's property.
func (s *Service) Sync(ctx context.Context, d *ent.GateDevice) (*SyncPayload, error) {
	now := time.Now()
	passes, err := s.client.VisitorPass.Query().
		Where(visitorpass.PropertyID(d.PropertyID), visitorpass.StatusEQ(visitorpass.StatusActive),
			visitorpass.ValidToGT(now), visitorpass.ValidFromLT(now.Add(24*time.Hour))).Limit(5000).All(ctx)
	if err != nil {
		return nil, err
	}
	out := &SyncPayload{ServerTime: now, WalkInPolicy: settings.WalkInGuardDecides}
	if st, err := s.client.TenantSetting.Query().First(ctx); err == nil {
		out.WalkInPolicy = settings.WalkInPolicy(st)
	}
	salted := func(enc any) string {
		s2, _ := enc.(string)
		plain, err := s.box.Decrypt(s2)
		if err != nil || plain == "" {
			return ""
		}
		sum := sha256.Sum256([]byte(d.ID.String() + ":" + plain))
		return hex.EncodeToString(sum[:])
	}
	for _, p := range passes {
		out.Passes = append(out.Passes, CachedPass{ID: p.ID, CodeHash: salted(p.Metadata["code_enc"]),
			QRHash: salted(p.Metadata["qr_enc"]), VisitorName: p.VisitorName,
			UnitID: p.UnitID, ValidFrom: p.ValidFrom, ValidTo: p.ValidTo, Recurrence: p.Recurrence,
			MaxEntries: p.MaxEntries, EntriesUsed: p.EntriesUsed})
	}
	// Only people deployed to this gate's property, filtered in SQL (JSON containment), so a tenant
	// with many sites never ships every guard to every tablet.
	people, err := s.client.VendorPersonnel.Query().Where(vendorpersonnel.StatusEQ(vendorpersonnel.StatusActive),
		func(sel *sql.Selector) {
			sel.Where(sqljson.ValueContains(vendorpersonnel.FieldPropertyIds, d.PropertyID.String()))
		}).
		Order(ent.Asc(vendorpersonnel.FieldBadgeNumber)).Limit(2000).All(ctx)
	if err != nil {
		return nil, err
	}
	out.Badges = []CachedBadge{}
	for _, p := range people {
		out.Badges = append(out.Badges, CachedBadge{ID: p.ID, BadgeNumber: p.BadgeNumber, Name: p.FullName, Role: p.Role,
			VendorID: p.VendorID, HasPIN: p.PinHash != ""})
	}
	if out.Passes == nil {
		out.Passes = []CachedPass{}
	}
	return out, nil
}

// EventView is a gate log row with the unit, block and guard spelled out.
type EventView struct {
	*ent.GateEvent
	UnitCode  string `json:"unit_code,omitempty"`
	Block     string `json:"block,omitempty"`
	GuardName string `json:"guard_name,omitempty"`
}

// ListEvents returns a keyset page of gate events for a property, optionally one kind, with the
// unit, block and guard read in two extra queries for the whole page.
func (s *Service) ListEvents(ctx context.Context, propertyID uuid.UUID, kind string, p page.Params) (page.Result[EventView], error) {
	q := s.client.GateEvent.Query().Where(gateevent.PropertyID(propertyID))
	if kind != "" {
		q = q.Where(gateevent.KindEQ(gateevent.Kind(kind)))
	}
	rows, err := q.Where(p.Predicate()).Modify(page.Order()).Limit(p.Limit + 1).All(ctx)
	if err != nil {
		return page.Result[EventView]{}, err
	}
	codes := s.unitCodes(ctx, func(add func(uuid.UUID)) {
		for _, r := range rows {
			if r.HostUnitID != nil {
				add(*r.HostUnitID)
			}
		}
	})
	var guardIDs []uuid.UUID
	for _, r := range rows {
		if r.GuardPersonnelID != nil {
			guardIDs = append(guardIDs, *r.GuardPersonnelID)
		}
	}
	guards := map[uuid.UUID]string{}
	if len(guardIDs) > 0 {
		if ps, err := s.client.VendorPersonnel.Query().Where(vendorpersonnel.IDIn(guardIDs...)).
			Select(vendorpersonnel.FieldID, vendorpersonnel.FieldFullName).All(ctx); err == nil {
			for _, g := range ps {
				guards[g.ID] = g.FullName
			}
		}
	}
	views := make([]EventView, len(rows))
	for i, r := range rows {
		views[i] = EventView{GateEvent: r}
		if r.HostUnitID != nil {
			views[i].UnitCode, views[i].Block = codes[*r.HostUnitID].code, codes[*r.HostUnitID].block
		}
		if r.GuardPersonnelID != nil {
			views[i].GuardName = guards[*r.GuardPersonnelID]
		}
	}
	return page.Build(views, p.Limit, func(e EventView) (uuid.UUID, time.Time) { return e.ID, e.CreatedAt }), nil
}

// ValidEventKind reports whether s is a gate event kind (for list filters).
func ValidEventKind(s string) bool { return gateevent.KindValidator(gateevent.Kind(s)) == nil }

// ScopeFilter narrows a staff list to one property or the caller's properties.
type ScopeFilter struct {
	PropertyID    *uuid.UUID
	Scope         []uuid.UUID
	AllProperties bool
}

// PagePasses returns a keyset page of passes for staff, newest first.
func (s *Service) PagePasses(ctx context.Context, f ScopeFilter, activeOnly bool, p page.Params) (page.Result[*ent.VisitorPass], error) {
	q := s.client.VisitorPass.Query()
	if f.PropertyID != nil {
		q = q.Where(visitorpass.PropertyID(*f.PropertyID))
	} else if !f.AllProperties {
		q = q.Where(visitorpass.PropertyIDIn(f.Scope...))
	}
	if activeOnly {
		q = q.Where(visitorpass.StatusEQ(visitorpass.StatusActive), visitorpass.ValidToGT(time.Now()))
	}
	rows, err := q.Where(p.Predicate()).Modify(page.Order()).Limit(p.Limit + 1).All(ctx)
	if err != nil {
		return page.Result[*ent.VisitorPass]{}, err
	}
	return page.Build(rows, p.Limit, func(v *ent.VisitorPass) (uuid.UUID, time.Time) { return v.ID, v.CreatedAt }), nil
}

// PageIncidents returns a keyset page of incidents for staff, newest first.
func (s *Service) PageIncidents(ctx context.Context, f ScopeFilter, open bool, p page.Params) (page.Result[*ent.Incident], error) {
	q := s.client.Incident.Query()
	if f.PropertyID != nil {
		q = q.Where(incident.PropertyID(*f.PropertyID))
	} else if !f.AllProperties {
		q = q.Where(incident.PropertyIDIn(f.Scope...))
	}
	if open {
		q = q.Where(incident.StatusIn(incident.StatusOpen, incident.StatusInvestigating))
	}
	rows, err := q.Where(p.Predicate()).Modify(page.Order()).Limit(p.Limit + 1).All(ctx)
	if err != nil {
		return page.Result[*ent.Incident]{}, err
	}
	return page.Build(rows, p.Limit, func(v *ent.Incident) (uuid.UUID, time.Time) { return v.ID, v.CreatedAt }), nil
}

// ListPasses returns passes for a property (or a host party), newest first. The portal uses it
// with the caller's own parties; staff lists use PagePasses.
func (s *Service) ListPasses(ctx context.Context, propertyID *uuid.UUID, hostParties []uuid.UUID, activeOnly bool, limit int) ([]*ent.VisitorPass, error) {
	q := s.client.VisitorPass.Query()
	if propertyID != nil {
		q = q.Where(visitorpass.PropertyID(*propertyID))
	}
	if hostParties != nil {
		q = q.Where(visitorpass.HostPartyIDIn(hostParties...))
	}
	if activeOnly {
		q = q.Where(visitorpass.StatusEQ(visitorpass.StatusActive), visitorpass.ValidToGT(time.Now()))
	}
	return q.Order(ent.Desc(visitorpass.FieldCreatedAt)).Limit(min(max(limit, 1), 500)).All(ctx)
}

// IncidentInput reports an incident.
type IncidentInput struct {
	PropertyID  uuid.UUID  `json:"property_id"`
	UnitID      *uuid.UUID `json:"unit_id"`
	Category    string     `json:"category"`
	Severity    string     `json:"severity"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	OccurredAt  *time.Time `json:"occurred_at"`
	Photos      []string   `json:"photos"`
}

// ReportIncident records an incident; high and critical ones alert at once.
func (s *Service) ReportIncident(ctx context.Context, kind string, by uuid.UUID, in IncidentInput) (*ent.Incident, error) {
	if in.Title == "" || in.Category == "" {
		return nil, httpx.Invalid("title and category are required")
	}
	in.Description = richtext.Sanitize(in.Description)
	number, err := s.seq.Next(ctx, "incident", "INC")
	if err != nil {
		return nil, err
	}
	at := time.Now()
	if in.OccurredAt != nil {
		at = *in.OccurredAt
	}
	sev := incident.Severity(in.Severity)
	if in.Severity == "" {
		sev = incident.SeverityMedium
	}
	c := s.client.Incident.Create().SetNumber(number).SetPropertyID(in.PropertyID).SetCategory(in.Category).
		SetSeverity(sev).SetTitle(in.Title).SetDescription(in.Description).SetOccurredAt(at).
		SetReportedByKind(kind).SetReportedByID(by).SetPhotos(in.Photos)
	if in.UnitID != nil {
		c.SetUnitID(*in.UnitID)
	}
	inc, err := c.Save(ctx)
	if err != nil {
		return nil, err
	}
	tenantID, _ := tenantguard.TenantID(ctx)
	_ = events.Publish(ctx, s.client.OutboxEvent, tenantID, inc.ID.String(), events.IncidentReported, map[string]any{
		"incident_id": inc.ID, "number": inc.Number, "category": inc.Category, "severity": inc.Severity,
		"title": inc.Title, "property_id": inc.PropertyID, "urgent": sev == incident.SeverityHigh || sev == incident.SeverityCritical,
	})
	return inc, nil
}

// PurgeOld deletes gate events past retention in batches (system job).
func (s *Service) PurgeOld(ctx context.Context) (int, error) {
	cutoff := time.Now().Add(-Retention)
	total := 0
	for i := 0; i < 20; i++ {
		ids, err := s.client.GateEvent.Query().Where(gateevent.OccurredAtLT(cutoff)).Limit(1000).IDs(ctx)
		if err != nil || len(ids) == 0 {
			return total, err
		}
		n, err := s.client.GateEvent.Delete().Where(gateevent.IDIn(ids...)).Exec(ctx)
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// OfflineDevices flags tablets silent for over 15 minutes (system job) and returns them once.
func (s *Service) OfflineDevices(ctx context.Context, tenants []uuid.UUID) ([]*ent.GateDevice, error) {
	if len(tenants) == 0 {
		return nil, nil
	}
	rows, err := s.client.GateDevice.Query().Where(gatedevice.TenantIDIn(tenants...), gatedevice.StatusEQ(gatedevice.StatusActive),
		gatedevice.OfflineAlerted(false), gatedevice.LastSeenAtLT(time.Now().Add(-15*time.Minute))).Limit(200).All(ctx)
	if err != nil {
		return nil, err
	}
	for _, d := range rows {
		_ = s.client.GateDevice.UpdateOneID(d.ID).SetOfflineAlerted(true).Exec(tenantguard.With(ctx, d.TenantID))
	}
	return rows, nil
}
