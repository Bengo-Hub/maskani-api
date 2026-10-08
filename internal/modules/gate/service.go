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

	"github.com/google/uuid"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/gatedevice"
	"github.com/bengobox/maskani-api/internal/ent/gateevent"
	"github.com/bengobox/maskani-api/internal/ent/incident"
	"github.com/bengobox/maskani-api/internal/ent/unit"
	"github.com/bengobox/maskani-api/internal/ent/vendorpersonnel"
	"github.com/bengobox/maskani-api/internal/ent/visitorpass"
	"github.com/bengobox/maskani-api/internal/events"
	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/sequence"
	"github.com/bengobox/maskani-api/internal/platform/realtime"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
	"github.com/bengobox/maskani-api/internal/shared/page"
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
	_ = s.client.GateDevice.UpdateOneID(d.ID).SetLastSeenAt(time.Now()).SetOfflineAlerted(false).Exec(tctx)
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
		SetValidFrom(from).SetValidTo(to).SetMaxEntries(maxEntries).SetNotes(in.Notes)
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

// VerifyResult is what the tablet shows.
type VerifyResult struct {
	Valid    bool             `json:"valid"`
	Reason   string           `json:"reason,omitempty"`
	Pass     *ent.VisitorPass `json:"pass,omitempty"`
	UnitCode string           `json:"unit_code,omitempty"`
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
		if u, err := s.client.Unit.Query().Where(unit.ID(*p.UnitID)).Only(ctx); err == nil {
			res.UnitCode = u.Code
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
}

// Record stores events idempotently on (device, client_event_id). Entries against a pass consume it
// and notify the host.
func (s *Service) Record(ctx context.Context, d *ent.GateDevice, batch []EventInput) (int, error) {
	stored := 0
	tenantID, _ := tenantguard.TenantID(ctx)
	for _, in := range batch {
		if in.ClientEventID == "" || in.Kind == "" {
			continue
		}
		if in.OccurredAt.IsZero() || in.OccurredAt.After(time.Now().Add(5*time.Minute)) {
			in.OccurredAt = time.Now()
		}
		c := s.client.GateEvent.Create().SetPropertyID(d.PropertyID).SetDeviceID(d.ID).SetKind(gateevent.Kind(in.Kind)).
			SetClientEventID(in.ClientEventID).SetVisitorName(in.VisitorName).SetVisitorPhone(secure.NormalizePhone(in.VisitorPhone)).
			SetVehiclePlate(strings.ToUpper(strings.ReplaceAll(in.VehiclePlate, " ", ""))).SetIDSighted(in.IDSighted).
			SetOccurredAt(in.OccurredAt).SetOffline(in.Offline).SetNotes(in.Notes)
		if in.PassID != nil {
			c.SetPassID(*in.PassID)
		}
		if in.HostUnitID != nil {
			c.SetHostUnitID(*in.HostUnitID)
		}
		if in.GuardID != nil {
			c.SetGuardPersonnelID(*in.GuardID)
		}
		if in.Kind == string(gateevent.KindWalkInRequest) {
			c.SetDecision(gateevent.DecisionPending)
		}
		ev, err := c.Save(ctx)
		if ent.IsConstraintError(err) {
			continue
		}
		if err != nil {
			return stored, err
		}
		stored++
		if ev.Kind == gateevent.KindEntry && in.PassID != nil {
			s.consume(ctx, *in.PassID)
			_ = events.Publish(ctx, s.client.OutboxEvent, tenantID, ev.ID.String(), events.VisitorArrived, s.arrival(ctx, ev))
		}
		if ev.Kind == gateevent.KindWalkInRequest {
			_ = events.Publish(ctx, s.client.OutboxEvent, tenantID, ev.ID.String(), events.WalkInRequested, s.arrival(ctx, ev))
			s.emit(ctx, realtime.WalkInRequested, ev)
		} else {
			s.emit(ctx, realtime.GateEvent, ev)
		}
	}
	return stored, nil
}

func (s *Service) consume(ctx context.Context, passID uuid.UUID) {
	p, err := s.client.VisitorPass.Get(ctx, passID)
	if err != nil {
		return
	}
	u := p.Update().AddEntriesUsed(1)
	if p.MaxEntries > 0 && p.EntriesUsed+1 >= p.MaxEntries {
		u.SetStatus(visitorpass.StatusUsed)
	}
	_ = u.Exec(ctx)
}

func (s *Service) arrival(ctx context.Context, ev *ent.GateEvent) map[string]any {
	out := map[string]any{"event_id": ev.ID, "visitor_name": ev.VisitorName, "occurred_at": ev.OccurredAt,
		"property_id": ev.PropertyID, "kind": ev.Kind, "vehicle_plate": ev.VehiclePlate}
	if ev.HostUnitID != nil {
		out["host_unit_id"] = *ev.HostUnitID
		if u, err := s.client.Unit.Get(ctx, *ev.HostUnitID); err == nil {
			out["unit_code"] = u.Code
		}
	}
	if ev.PassID != nil {
		if p, err := s.client.VisitorPass.Get(ctx, *ev.PassID); err == nil && p.HostPartyID != nil {
			out["host_party_id"] = *p.HostPartyID
			if h, err := s.client.Party.Get(ctx, *p.HostPartyID); err == nil {
				out["host_phone"], out["host_name"], out["host_email"] = h.Phone, h.DisplayName, h.Email
			}
		}
	}
	return out
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

// SyncPayload is what a tablet caches for offline operation.
type SyncPayload struct {
	ServerTime time.Time     `json:"server_time"`
	Passes     []CachedPass  `json:"passes"`
	Badges     []CachedBadge `json:"badges"`
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
	out := &SyncPayload{ServerTime: now}
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
	people, err := s.client.VendorPersonnel.Query().Where(vendorpersonnel.StatusEQ(vendorpersonnel.StatusActive)).
		Order(ent.Asc(vendorpersonnel.FieldBadgeNumber)).Limit(2000).All(ctx)
	if err != nil {
		return nil, err
	}
	out.Badges = []CachedBadge{}
	for _, p := range people {
		if !deployedTo(p.PropertyIds, d.PropertyID) {
			continue
		}
		out.Badges = append(out.Badges, CachedBadge{ID: p.ID, BadgeNumber: p.BadgeNumber, Name: p.FullName, Role: p.Role,
			VendorID: p.VendorID, HasPIN: p.PinHash != ""})
	}
	if out.Passes == nil {
		out.Passes = []CachedPass{}
	}
	return out, nil
}

// ListEvents returns a keyset page of gate events for a property.
func (s *Service) ListEvents(ctx context.Context, propertyID uuid.UUID, p page.Params) (page.Result[*ent.GateEvent], error) {
	rows, err := s.client.GateEvent.Query().Where(gateevent.PropertyID(propertyID)).
		Where(p.Predicate()).Modify(page.Order()).Limit(p.Limit + 1).All(ctx)
	if err != nil {
		return page.Result[*ent.GateEvent]{}, err
	}
	return page.Build(rows, p.Limit, func(e *ent.GateEvent) (uuid.UUID, time.Time) { return e.ID, e.CreatedAt }), nil
}

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
func (s *Service) OfflineDevices(ctx context.Context) ([]*ent.GateDevice, error) {
	rows, err := s.client.GateDevice.Query().Where(gatedevice.StatusEQ(gatedevice.StatusActive),
		gatedevice.OfflineAlerted(false), gatedevice.LastSeenAtLT(time.Now().Add(-15*time.Minute))).Limit(200).All(ctx)
	if err != nil {
		return nil, err
	}
	for _, d := range rows {
		_ = s.client.GateDevice.UpdateOneID(d.ID).SetOfflineAlerted(true).Exec(tenantguard.With(ctx, d.TenantID))
	}
	return rows, nil
}
