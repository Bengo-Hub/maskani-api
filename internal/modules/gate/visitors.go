package gate

import (
	"context"
	"sort"
	"strings"
	"time"

	"entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/sqljson"
	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/block"
	"github.com/bengobox/maskani-api/internal/ent/gateevent"
	"github.com/bengobox/maskani-api/internal/ent/party"
	"github.com/bengobox/maskani-api/internal/ent/unit"
	"github.com/bengobox/maskani-api/internal/ent/unitparty"
	"github.com/bengobox/maskani-api/internal/ent/visitor"
	"github.com/bengobox/maskani-api/internal/events"
	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/register"
	"github.com/bengobox/maskani-api/internal/platform/realtime"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
	"github.com/bengobox/maskani-api/internal/shared/secure"
)

// insideWindow bounds "who is inside": an entry older than this without an exit is treated as
// gone (people leave by other gates or the exit was never recorded).
const insideWindow = 24 * time.Hour

// normPlate is a number plate in upper case without spaces.
func normPlate(p string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(p), " ", ""))
}

// VisitorDetails is what the gate knows about a person arriving.
type VisitorDetails struct {
	Name     string
	Phone    string
	IDNumber string
	Plate    string
	HostUnit *uuid.UUID
	// Visit counts this as a visit (an arrival at the gate); a pass being issued is not one.
	Visit bool
}

// ResolveVisitor finds the returning visitor by phone, then ID number, then number plate, and
// records this visit; a new person is added. Returns nil when there is nothing to match or keep
// (no name, phone, ID or plate).
func (s *Service) ResolveVisitor(ctx context.Context, propertyID uuid.UUID, in VisitorDetails) (*ent.Visitor, error) {
	phone, plate := secure.NormalizePhone(in.Phone), normPlate(in.Plate)
	idHash, idHint := "", ""
	if id := strings.ToUpper(strings.TrimSpace(in.IDNumber)); id != "" {
		idHash = s.box.Hash("visitor-id:" + id)
		if len(id) > 3 {
			idHint = id[len(id)-3:]
		}
	}
	name := strings.TrimSpace(in.Name)
	if phone == "" && idHash == "" && plate == "" {
		return nil, nil
	}
	q := s.client.Visitor.Query().Where(visitor.PropertyID(propertyID))
	var v *ent.Visitor
	var err error
	for _, p := range []func() (*ent.Visitor, error){
		func() (*ent.Visitor, error) {
			if phone == "" {
				return nil, nil
			}
			return q.Clone().Where(visitor.Phone(phone)).Order(recentFirst()).First(ctx)
		},
		func() (*ent.Visitor, error) {
			if idHash == "" {
				return nil, nil
			}
			return q.Clone().Where(visitor.IDNumberHash(idHash)).First(ctx)
		},
		func() (*ent.Visitor, error) {
			if plate == "" {
				return nil, nil
			}
			return q.Clone().Where(visitor.VehiclePlate(plate)).Order(recentFirst()).First(ctx)
		},
	} {
		v, err = p()
		if err != nil && !ent.IsNotFound(err) {
			return nil, err
		}
		if v != nil {
			break
		}
	}
	now := time.Now()
	if v == nil {
		if name == "" {
			name = "Visitor"
		}
		c := s.client.Visitor.Create().SetPropertyID(propertyID).SetName(name).SetPhone(phone).SetIDNumberHash(idHash).
			SetIDNumberHint(idHint).SetVehiclePlate(plate).SetNillableLastHostUnitID(in.HostUnit)
		if in.Visit {
			c.SetVisits(1).SetLastVisitAt(now)
		}
		if plate != "" {
			c.SetVehiclePlates([]string{plate})
		}
		return c.Save(ctx)
	}
	u := v.Update()
	if in.Visit {
		u.AddVisits(1).SetLastVisitAt(now)
	}
	if name != "" && (v.Name == "" || v.Name == "Visitor") {
		u.SetName(name)
	}
	if phone != "" && v.Phone == "" {
		u.SetPhone(phone)
	}
	if idHash != "" && v.IDNumberHash == "" {
		u.SetIDNumberHash(idHash).SetIDNumberHint(idHint)
	}
	if plate != "" {
		u.SetVehiclePlate(plate)
		if !contains(v.VehiclePlates, plate) {
			u.SetVehiclePlates(append(v.VehiclePlates, plate))
		}
	}
	if in.HostUnit != nil {
		u.SetLastHostUnitID(*in.HostUnit)
	}
	return u.Save(ctx)
}

// recentFirst orders visitors by their last visit, newest first, with never-visited ones (a pass not
// used yet) last rather than first, which is where Postgres puts NULLs in descending order.
func recentFirst() visitor.OrderOption {
	return visitor.ByLastVisitAt(sql.OrderDesc(), sql.OrderNullsLast())
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// VisitorMatch is a returning visitor the guard can pick while typing a phone, plate or name.
type VisitorMatch struct {
	ID       uuid.UUID  `json:"id"`
	Name     string     `json:"name"`
	Phone    string     `json:"phone,omitempty"`
	Plate    string     `json:"vehicle_plate,omitempty"`
	IDHint   string     `json:"id_number_hint,omitempty"`
	Visits   int        `json:"visits"`
	LastAt   *time.Time `json:"last_visit_at,omitempty"`
	UnitID   *uuid.UUID `json:"last_host_unit_id,omitempty"`
	UnitCode string     `json:"last_unit_code,omitempty"`
	Banned   bool       `json:"banned"`
	Notes    string     `json:"notes,omitempty"`
}

// LookupVisitors finds returning visitors at a property by the start of a phone number or plate,
// or part of a name: at most 8, most recent first.
func (s *Service) LookupVisitors(ctx context.Context, propertyID uuid.UUID, q string) ([]VisitorMatch, error) {
	q = strings.TrimSpace(q)
	if len([]rune(q)) < 2 {
		return []VisitorMatch{}, nil
	}
	// A partial number ("0712 34") is not a valid phone yet, so match its digits inside the stored
	// number without the trunk 0 or the country code.
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, q)
	digits = strings.TrimPrefix(strings.TrimPrefix(digits, "254"), "0")
	preds := []func(*sql.Selector){}
	if len(digits) >= 3 {
		preds = append(preds, func(sel *sql.Selector) { sel.Where(sql.Contains(sel.C(visitor.FieldPhone), digits)) })
	}
	plate := normPlate(q)
	rows, err := s.client.Visitor.Query().Where(visitor.PropertyID(propertyID), visitor.Or(
		visitor.VehiclePlateHasPrefix(plate), visitor.NameContainsFold(q),
		func(sel *sql.Selector) {
			for _, p := range preds {
				p(sel)
			}
			if len(preds) == 0 {
				sel.Where(sql.False())
			}
		},
		func(sel *sql.Selector) { sel.Where(sqljson.ValueContains(visitor.FieldVehiclePlates, plate)) },
	)).Order(recentFirst()).Limit(8).All(ctx)
	if err != nil {
		return nil, err
	}
	codes := s.unitCodes(ctx, func(add func(uuid.UUID)) {
		for _, r := range rows {
			if r.LastHostUnitID != nil {
				add(*r.LastHostUnitID)
			}
		}
	})
	out := make([]VisitorMatch, len(rows))
	for i, r := range rows {
		out[i] = VisitorMatch{ID: r.ID, Name: r.Name, Phone: secure.MaskPhone(r.Phone), Plate: r.VehiclePlate, IDHint: r.IDNumberHint,
			Visits: r.Visits, LastAt: r.LastVisitAt, UnitID: r.LastHostUnitID, Banned: r.Status == visitor.StatusBanned, Notes: r.Notes}
		if r.LastHostUnitID != nil {
			out[i].UnitCode = codes[*r.LastHostUnitID].code
		}
	}
	return out, nil
}

type unitInfo struct{ code, block string }

// unitCodes reads the codes and block names of the units collect adds, in one or two queries.
func (s *Service) unitCodes(ctx context.Context, collect func(add func(uuid.UUID))) map[uuid.UUID]unitInfo {
	seen := map[uuid.UUID]bool{}
	var ids []uuid.UUID
	collect(func(id uuid.UUID) {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	})
	out := map[uuid.UUID]unitInfo{}
	if len(ids) == 0 {
		return out
	}
	us, err := s.client.Unit.Query().Where(unit.IDIn(ids...)).Select(unit.FieldID, unit.FieldCode, unit.FieldBlockID).All(ctx)
	if err != nil {
		return out
	}
	var blockIDs []uuid.UUID
	for _, u := range us {
		if u.BlockID != nil {
			blockIDs = append(blockIDs, *u.BlockID)
		}
	}
	names := map[uuid.UUID]string{}
	if len(blockIDs) > 0 {
		if bs, err := s.client.Block.Query().Where(block.IDIn(blockIDs...)).Select(block.FieldID, block.FieldCode, block.FieldName).All(ctx); err == nil {
			for _, b := range bs {
				names[b.ID] = firstNonEmpty(b.Name, b.Code)
			}
		}
	}
	for _, u := range us {
		info := unitInfo{code: u.Code}
		if u.BlockID != nil {
			info.block = names[*u.BlockID]
		}
		out[u.ID] = info
	}
	return out
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// InsidePerson is someone let in who has not been recorded leaving.
type InsidePerson struct {
	EventID   uuid.UUID  `json:"event_id"`
	Name      string     `json:"visitor_name"`
	Plate     string     `json:"vehicle_plate,omitempty"`
	UnitID    *uuid.UUID `json:"host_unit_id,omitempty"`
	UnitCode  string     `json:"unit_code,omitempty"`
	Block     string     `json:"block,omitempty"`
	Since     time.Time  `json:"since"`
	PassID    *uuid.UUID `json:"pass_id,omitempty"`
	VisitorID *uuid.UUID `json:"visitor_id,omitempty"`
	WalkIn    bool       `json:"walk_in"`
}

// Inside lists who is inside a property now, newest first (at most 300).
func (s *Service) Inside(ctx context.Context, propertyID uuid.UUID) ([]InsidePerson, error) {
	rows, err := s.client.GateEvent.Query().Where(gateevent.PropertyID(propertyID), gateevent.ExitedAtIsNil(),
		gateevent.OccurredAtGT(time.Now().Add(-insideWindow)),
		gateevent.Or(gateevent.KindEQ(gateevent.KindEntry),
			gateevent.And(gateevent.KindEQ(gateevent.KindWalkInRequest), gateevent.DecisionEQ(gateevent.DecisionApproved)))).
		Order(ent.Desc(gateevent.FieldOccurredAt)).Limit(300).All(ctx)
	if err != nil {
		return nil, err
	}
	codes := s.unitCodes(ctx, func(add func(uuid.UUID)) {
		for _, r := range rows {
			if r.HostUnitID != nil {
				add(*r.HostUnitID)
			}
		}
	})
	out := make([]InsidePerson, len(rows))
	for i, r := range rows {
		out[i] = InsidePerson{EventID: r.ID, Name: r.VisitorName, Plate: r.VehiclePlate, UnitID: r.HostUnitID, Since: r.OccurredAt,
			PassID: r.PassID, VisitorID: r.VisitorID, WalkIn: r.Kind == gateevent.KindWalkInRequest}
		if r.HostUnitID != nil {
			info := codes[*r.HostUnitID]
			out[i].UnitCode, out[i].Block = info.code, info.block
		}
	}
	return out, nil
}

// claimExit marks an entry as left, once: a second exit for the same entry (a double tap, a
// replayed queue) finds it already closed and records nothing.
func (s *Service) claimExit(ctx context.Context, propertyID, entryID uuid.UUID, at time.Time) (*ent.GateEvent, bool, error) {
	n, err := s.client.GateEvent.Update().Where(gateevent.ID(entryID), gateevent.PropertyID(propertyID), gateevent.ExitedAtIsNil(),
		gateevent.Or(gateevent.KindEQ(gateevent.KindEntry),
			gateevent.And(gateevent.KindEQ(gateevent.KindWalkInRequest), gateevent.DecisionEQ(gateevent.DecisionApproved)))).
		SetExitedAt(at).Save(ctx)
	if err != nil || n == 0 {
		return nil, false, err
	}
	entry, err := s.client.GateEvent.Get(ctx, entryID)
	return entry, err == nil, err
}

// HostContact is who answers for a unit at the gate.
type HostContact struct {
	PartyID uuid.UUID
	Name    string
	Phone   string
	Email   string
	UserID  *uuid.UUID
}

// unitHosts picks who to ask about a visitor for each unit: the primary occupant, else an occupant,
// else the primary owner, else any owner or buyer, among links that hold today.
func (s *Service) unitHosts(ctx context.Context, unitIDs []uuid.UUID) map[uuid.UUID]HostContact {
	out := map[uuid.UUID]HostContact{}
	if len(unitIDs) == 0 {
		return out
	}
	links, err := s.client.UnitParty.Query().Where(unitparty.UnitIDIn(unitIDs...), register.ActiveLink(),
		unitparty.RoleIn(unitparty.RoleOccupant, unitparty.RoleOwner, unitparty.RoleJointOwner, unitparty.RoleBuyer)).All(ctx)
	if err != nil || len(links) == 0 {
		return out
	}
	rank := func(l *ent.UnitParty) int {
		r := 4
		switch l.Role {
		case unitparty.RoleOccupant:
			r = 0
		case unitparty.RoleOwner:
			r = 2
		}
		if l.IsPrimary {
			r--
		}
		return r
	}
	sort.SliceStable(links, func(i, j int) bool { return rank(links[i]) < rank(links[j]) })
	best := map[uuid.UUID]uuid.UUID{}
	var partyIDs []uuid.UUID
	for _, l := range links {
		if _, ok := best[l.UnitID]; !ok {
			best[l.UnitID] = l.PartyID
			partyIDs = append(partyIDs, l.PartyID)
		}
	}
	people, err := s.client.Party.Query().Where(party.IDIn(partyIDs...)).All(ctx)
	if err != nil {
		return out
	}
	byID := map[uuid.UUID]*ent.Party{}
	for _, p := range people {
		byID[p.ID] = p
	}
	for unitID, partyID := range best {
		if p := byID[partyID]; p != nil {
			out[unitID] = HostContact{PartyID: p.ID, Name: p.DisplayName, Phone: p.Phone, Email: p.Email, UserID: p.AuthUserID}
		}
	}
	return out
}

// ResolveWalkIn records the guard's decision on a walk-in on the same row the request made, so a
// refusal is one log line, not two. The guard may decide at any time: before the host answers,
// after the host stops answering, or to override. A walk-in the host already decided stays as the
// host decided it.
func (s *Service) ResolveWalkIn(ctx context.Context, d *ent.GateDevice, eventID uuid.UUID, admit bool, note string) (*ent.GateEvent, error) {
	ev, err := s.client.GateEvent.Query().Where(gateevent.ID(eventID), gateevent.PropertyID(d.PropertyID),
		gateevent.KindEQ(gateevent.KindWalkInRequest)).Only(ctx)
	if err != nil {
		return nil, err
	}
	if ev.DecidedBy == "host" && (ev.Decision == gateevent.DecisionApproved || ev.Decision == gateevent.DecisionDeclined) {
		return ev, nil
	}
	dec := gateevent.DecisionDeclined
	if admit {
		dec = gateevent.DecisionApproved
	}
	u := ev.Update().SetDecision(dec).SetDecidedBy("guard").SetDecidedAt(time.Now())
	if note = strings.TrimSpace(note); note != "" {
		u.SetNotes(note)
	} else if !admit {
		u.SetNotes("Turned away at the gate")
	}
	ev, err = u.Save(ctx)
	if err != nil {
		return nil, err
	}
	s.emit(ctx, realtime.WalkInDecided, ev)
	if admit {
		tenantID, _ := tenantguard.TenantID(ctx)
		_ = events.Publish(ctx, s.client.OutboxEvent, tenantID, ev.ID.String(), events.VisitorArrived, s.arrival(ctx, ev))
	}
	return ev, nil
}

// ringGap is the least time between two rings for the same walk-in.
const ringGap = 30 * time.Second

// RingHost asks the host again, loudly: a push that pops up on their phone plus the WhatsApp
// message, for a walk-in still waiting. At most once every 30 seconds.
func (s *Service) RingHost(ctx context.Context, d *ent.GateDevice, eventID uuid.UUID) (*ent.GateEvent, error) {
	ev, err := s.client.GateEvent.Query().Where(gateevent.ID(eventID), gateevent.PropertyID(d.PropertyID),
		gateevent.KindEQ(gateevent.KindWalkInRequest)).Only(ctx)
	if err != nil {
		return nil, err
	}
	if ev.Decision != gateevent.DecisionPending && ev.Decision != gateevent.DecisionTimeout {
		return nil, httpx.Conflict("this walk-in has already been decided")
	}
	if ev.HostUnitID == nil {
		return nil, httpx.Invalid("choose the unit the visitor is going to first")
	}
	meta := map[string]any{}
	for k, v := range ev.Metadata {
		meta[k] = v
	}
	if last, ok := meta["rang_at"].(string); ok {
		if t, err := time.Parse(time.RFC3339Nano, last); err == nil && time.Since(t) < ringGap {
			return nil, httpx.Conflict("the host was rung a moment ago; wait a few seconds")
		}
	}
	rings, _ := meta["rings"].(float64)
	meta["rang_at"], meta["rings"] = time.Now().Format(time.RFC3339Nano), rings+1
	// Ringing reopens a walk-in the host has not answered in time.
	ev, err = ev.Update().SetMetadata(meta).SetDecision(gateevent.DecisionPending).Save(ctx)
	if err != nil {
		return nil, err
	}
	payload := s.arrival(ctx, ev)
	payload["ring"] = true
	tenantID, _ := tenantguard.TenantID(ctx)
	if err := events.Publish(ctx, s.client.OutboxEvent, tenantID, ev.ID.String(), events.WalkInRequested, payload); err != nil {
		return nil, err
	}
	s.emit(ctx, realtime.WalkInRequested, ev)
	return ev, nil
}
