// Package works covers work orders from request to closure with SLA timers, and vendors with their
// compliance documents and personnel (SRDD section 14).
package works

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/vendor"
	"github.com/bengobox/maskani-api/internal/ent/vendordocument"
	"github.com/bengobox/maskani-api/internal/ent/vendorpersonnel"
	"github.com/bengobox/maskani-api/internal/ent/workorder"
	"github.com/bengobox/maskani-api/internal/ent/workorderevent"
	"github.com/bengobox/maskani-api/internal/events"
	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/sequence"
	"github.com/bengobox/maskani-api/internal/platform/realtime"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
	"github.com/bengobox/maskani-api/internal/shared/page"
	"github.com/bengobox/maskani-api/internal/shared/richtext"
	"golang.org/x/crypto/bcrypt"
)

// SLA targets per priority (SRDD 14.4): response and resolution.
var SLA = map[workorder.Priority][2]time.Duration{
	workorder.PriorityEmergency: {time.Hour, 4 * time.Hour},
	workorder.PriorityHigh:      {4 * time.Hour, 24 * time.Hour},
	workorder.PriorityNormal:    {24 * time.Hour, 72 * time.Hour},
	workorder.PriorityLow:       {72 * time.Hour, 14 * 24 * time.Hour},
}

// Service is the works service.
type Service struct {
	client *ent.Client
	seq    *sequence.Allocator
	log    *zap.Logger
	rt     realtime.Publisher
}

// SetRealtime sets the publisher for work order hints (nil disables them).
func (s *Service) SetRealtime(p realtime.Publisher) { s.rt = p }

func (s *Service) emit(ctx context.Context, wo *ent.WorkOrder) {
	tenantID, _ := tenantguard.TenantID(ctx)
	realtime.Emit(s.rt, tenantID, realtime.Event{Type: realtime.WorkOrderUpdated, ID: wo.ID.String(),
		PropertyID: wo.PropertyID.String(), UnitID: realtime.IDString(wo.UnitID)})
}

// NewService creates the works service.
func NewService(client *ent.Client, seq *sequence.Allocator, log *zap.Logger) *Service {
	return &Service{client: client, seq: seq, log: log.Named("works")}
}

// RequestInput raises a request or work order.
type RequestInput struct {
	PropertyID  uuid.UUID  `json:"property_id"`
	UnitID      *uuid.UUID `json:"unit_id"`
	Area        string     `json:"area"`
	Category    string     `json:"category"`
	Priority    string     `json:"priority"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Photos      []string   `json:"photos"`
}

// Actor identifies who acts on a work order.
type Actor struct {
	UserID  uuid.UUID
	PartyID *uuid.UUID
	Kind    string // staff, resident, vendor
}

// Create raises a work order with SLA due times from its priority.
func (s *Service) Create(ctx context.Context, a Actor, in RequestInput) (*ent.WorkOrder, error) {
	if in.Title == "" || in.Category == "" {
		return nil, httpx.Invalid("title and category are required")
	}
	pr := workorder.Priority(in.Priority)
	if in.Priority == "" {
		pr = workorder.PriorityNormal
	}
	if err := workorder.PriorityValidator(pr); err != nil {
		return nil, httpx.Invalid("invalid priority")
	}
	in.Description = richtext.Sanitize(in.Description)
	number, err := s.seq.Next(ctx, "work_order", "WO")
	if err != nil {
		return nil, err
	}
	now := time.Now()
	src := workorder.SourceStaff
	status := workorder.StatusTriaged
	if a.Kind == "resident" {
		src, status = workorder.SourceResident, workorder.StatusRequested
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	c := tx.WorkOrder.Create().SetNumber(number).SetPropertyID(in.PropertyID).SetArea(in.Area).
		SetCategory(in.Category).SetPriority(pr).SetTitle(in.Title).SetDescription(in.Description).
		SetSource(src).SetStatus(status).SetResponseDueAt(now.Add(SLA[pr][0])).SetResolutionDueAt(now.Add(SLA[pr][1])).
		SetCreatedBy(a.UserID)
	if in.UnitID != nil {
		c.SetUnitID(*in.UnitID)
	}
	if in.Photos != nil {
		c.SetPhotosBefore(in.Photos)
	}
	if a.PartyID != nil {
		c.SetRequestedByPartyID(*a.PartyID)
	} else {
		c.SetRequestedByUserID(a.UserID)
	}
	wo, err := c.Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := s.event(ctx, tx, wo.ID, a, "created", "", string(status), in.Description); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	tenantID, _ := tenantguard.TenantID(ctx)
	if err := events.Publish(ctx, tx.OutboxEvent, tenantID, wo.ID.String(), events.WorkOrderCreated, s.payload(wo)); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	s.emit(ctx, wo)
	return wo, nil
}

func (s *Service) event(ctx context.Context, tx *ent.Tx, id uuid.UUID, a Actor, kind, from, to, note string) error {
	c := tx.WorkOrderEvent.Create().SetWorkOrderID(id).SetKind(kind).SetFromStatus(from).SetToStatus(to).
		SetNote(note).SetActorKind(a.Kind)
	if a.UserID != uuid.Nil {
		c.SetActorID(a.UserID)
	}
	return c.Exec(ctx)
}

func (s *Service) payload(wo *ent.WorkOrder) map[string]any {
	return map[string]any{"work_order_id": wo.ID, "number": wo.Number, "title": wo.Title, "priority": wo.Priority,
		"status": wo.Status, "property_id": wo.PropertyID, "unit_id": wo.UnitID, "vendor_id": wo.VendorID,
		"resolution_due_at": wo.ResolutionDueAt, "requested_by_party_id": wo.RequestedByPartyID}
}

// ActionInput moves a work order through its lifecycle.
type ActionInput struct {
	Action        string     `json:"action"` // assign, quote, approve_quote, start, complete, confirm, reopen, cancel, close
	VendorID      *uuid.UUID `json:"vendor_id"`
	ERPEmployeeID string     `json:"erp_employee_id"`
	AssignedUser  *uuid.UUID `json:"assigned_user_id"`
	QuoteAmount   *float64   `json:"quote_amount"`
	CostAmount    *float64   `json:"cost_amount"`
	Recharge      *bool      `json:"recharge"`
	Photos        []string   `json:"photos"`
	MinutesOnSite *int       `json:"minutes_on_site"`
	Note          string     `json:"note"`
}

var transitions = map[string][]workorder.Status{
	"assign":        {workorder.StatusRequested, workorder.StatusTriaged, workorder.StatusReopened, workorder.StatusAssigned},
	"quote":         {workorder.StatusAssigned},
	"approve_quote": {workorder.StatusQuoted},
	"start":         {workorder.StatusAssigned, workorder.StatusApproved, workorder.StatusReopened},
	"complete":      {workorder.StatusInProgress, workorder.StatusAssigned, workorder.StatusApproved},
	"confirm":       {workorder.StatusCompleted},
	"reopen":        {workorder.StatusCompleted},
	"cancel":        {workorder.StatusRequested, workorder.StatusTriaged, workorder.StatusAssigned, workorder.StatusQuoted},
	"close":         {workorder.StatusConfirmed, workorder.StatusCompleted},
}

// Act applies a lifecycle action and records it on the timeline.
func (s *Service) Act(ctx context.Context, id uuid.UUID, a Actor, in ActionInput) (*ent.WorkOrder, error) {
	in.Note = richtext.Sanitize(in.Note)
	wo, err := s.client.WorkOrder.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	allowed, ok := transitions[in.Action]
	if !ok {
		return nil, httpx.Invalid("unknown action")
	}
	valid := false
	for _, st := range allowed {
		if st == wo.Status {
			valid = true
		}
	}
	if !valid {
		return nil, httpx.Conflict("this action is not allowed in status " + string(wo.Status))
	}
	if a.Kind == "resident" && in.Action != "confirm" && in.Action != "reopen" {
		return nil, httpx.Forbidden("residents can only confirm or reopen")
	}
	if in.Action == "reopen" && wo.CompletedAt != nil && time.Since(*wo.CompletedAt) > 7*24*time.Hour {
		return nil, httpx.Conflict("work orders can be reopened within 7 days of completion")
	}
	now := time.Now()
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	u := tx.WorkOrder.UpdateOneID(id)
	from := wo.Status
	var to workorder.Status
	evType := ""
	switch in.Action {
	case "assign":
		to = workorder.StatusAssigned
		switch {
		case in.VendorID != nil:
			u.SetAssigneeKind(workorder.AssigneeKindVendor).SetVendorID(*in.VendorID)
		case in.ERPEmployeeID != "" || in.AssignedUser != nil:
			u.SetAssigneeKind(workorder.AssigneeKindStaff).SetErpEmployeeID(in.ERPEmployeeID)
			if in.AssignedUser != nil {
				u.SetAssignedUserID(*in.AssignedUser)
			}
		default:
			_ = tx.Rollback()
			return nil, httpx.Invalid("assign needs vendor_id or a staff member")
		}
		if wo.RespondedAt == nil {
			u.SetRespondedAt(now)
		}
		evType = events.WorkOrderAssigned
	case "quote":
		if in.QuoteAmount == nil {
			_ = tx.Rollback()
			return nil, httpx.Invalid("quote_amount is required")
		}
		to = workorder.StatusQuoted
		u.SetQuoteAmount(decimal.NewFromFloat(*in.QuoteAmount)).SetQuoteStatus(workorder.QuoteStatusPending)
	case "approve_quote":
		to = workorder.StatusApproved
		u.SetQuoteStatus(workorder.QuoteStatusApproved)
	case "start":
		to = workorder.StatusInProgress
		if wo.RespondedAt == nil {
			u.SetRespondedAt(now)
		}
	case "complete":
		to = workorder.StatusCompleted
		u.SetCompletedAt(now)
		if in.CostAmount != nil {
			u.SetCostAmount(decimal.NewFromFloat(*in.CostAmount))
		}
		if in.Recharge != nil {
			u.SetRecharge(*in.Recharge)
		}
		if in.Photos != nil {
			u.SetPhotosAfter(in.Photos)
		}
		if in.MinutesOnSite != nil {
			u.SetMinutesOnSite(*in.MinutesOnSite)
		}
		if wo.ResolutionDueAt != nil && now.After(*wo.ResolutionDueAt) {
			u.SetSLABreached(true)
		}
		evType = events.WorkOrderCompleted
	case "confirm":
		to = workorder.StatusConfirmed
		u.SetConfirmedAt(now)
	case "reopen":
		to = workorder.StatusReopened
		u.AddReopenedCount(1).ClearCompletedAt()
	case "cancel":
		to = workorder.StatusCancelled
	case "close":
		to = workorder.StatusClosed
	}
	wo, err = u.SetStatus(to).Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := s.event(ctx, tx, id, a, in.Action, string(from), string(to), in.Note); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if evType != "" {
		tenantID, _ := tenantguard.TenantID(ctx)
		if err := events.Publish(ctx, tx.OutboxEvent, tenantID, id.String(), evType, s.payload(wo)); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	s.emit(ctx, wo)
	return s.client.WorkOrder.Get(ctx, id)
}

// Filter narrows a work order list.
type Filter struct {
	PropertyID    *uuid.UUID
	Status        string
	Priority      string
	Overdue       bool
	PartyID       *uuid.UUID
	Scope         []uuid.UUID
	AllProperties bool
}

// List returns a keyset page of work orders.
func (s *Service) List(ctx context.Context, f Filter, p page.Params) (page.Result[*ent.WorkOrder], error) {
	q := s.client.WorkOrder.Query()
	if f.PropertyID != nil {
		q = q.Where(workorder.PropertyID(*f.PropertyID))
	} else if !f.AllProperties {
		q = q.Where(workorder.PropertyIDIn(f.Scope...))
	}
	if f.Status == "open" {
		q = q.Where(workorder.StatusNotIn(workorder.StatusConfirmed, workorder.StatusClosed, workorder.StatusCancelled))
	} else if f.Status != "" {
		q = q.Where(workorder.StatusEQ(workorder.Status(f.Status)))
	}
	if f.Priority != "" {
		q = q.Where(workorder.PriorityEQ(workorder.Priority(f.Priority)))
	}
	if f.Overdue {
		q = q.Where(workorder.ResolutionDueAtLT(time.Now()),
			workorder.StatusNotIn(workorder.StatusCompleted, workorder.StatusConfirmed, workorder.StatusClosed, workorder.StatusCancelled))
	}
	if f.PartyID != nil {
		q = q.Where(workorder.RequestedByPartyID(*f.PartyID))
	}
	rows, err := q.Where(p.Predicate()).Modify(page.Order()).Limit(p.Limit + 1).All(ctx)
	if err != nil {
		return page.Result[*ent.WorkOrder]{}, err
	}
	return page.Build(rows, p.Limit, func(w *ent.WorkOrder) (uuid.UUID, time.Time) { return w.ID, w.CreatedAt }), nil
}

// Get returns a work order with its timeline.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*ent.WorkOrder, error) {
	return s.client.WorkOrder.Query().Where(workorder.ID(id)).
		WithEvents(func(q *ent.WorkOrderEventQuery) { q.Order(ent.Asc(workorderevent.FieldCreatedAt)) }).Only(ctx)
}

// FlagBreaches marks open work orders past their resolution time and publishes one event each
// (system job; bounded batch).
func (s *Service) FlagBreaches(ctx context.Context, tenants []uuid.UUID) (int, error) {
	if len(tenants) == 0 {
		return 0, nil
	}
	rows, err := s.client.WorkOrder.Query().
		Where(workorder.TenantIDIn(tenants...), workorder.SLABreached(false), workorder.ResolutionDueAtLT(time.Now()),
			workorder.StatusNotIn(workorder.StatusCompleted, workorder.StatusConfirmed, workorder.StatusClosed, workorder.StatusCancelled)).
		Limit(500).All(ctx)
	if err != nil {
		return 0, err
	}
	for _, wo := range rows {
		tctx := tenantguard.With(ctx, wo.TenantID)
		if err := s.client.WorkOrder.UpdateOneID(wo.ID).SetSLABreached(true).Exec(tctx); err != nil {
			continue
		}
		_ = events.Publish(tctx, s.client.OutboxEvent, wo.TenantID, wo.ID.String(), events.WorkOrderSLABreached, s.payload(wo))
	}
	return len(rows), nil
}

// VendorInput creates or updates a vendor.
type VendorInput struct {
	Name               string     `json:"name"`
	Categories         []string   `json:"categories"`
	RegistrationNumber string     `json:"registration_number"`
	ContactName        string     `json:"contact_name"`
	Phone              string     `json:"phone"`
	Email              string     `json:"email"`
	TreasuryVendorID   *uuid.UUID `json:"treasury_vendor_id"`
}

// CreateVendor adds a vendor.
func (s *Service) CreateVendor(ctx context.Context, actor uuid.UUID, in VendorInput) (*ent.Vendor, error) {
	if in.Name == "" {
		return nil, httpx.Invalid("name is required")
	}
	c := s.client.Vendor.Create().SetName(in.Name).SetCategories(in.Categories).SetRegistrationNumber(in.RegistrationNumber).
		SetContactName(in.ContactName).SetPhone(in.Phone).SetEmail(in.Email).SetCreatedBy(actor)
	if in.TreasuryVendorID != nil {
		c.SetTreasuryVendorID(*in.TreasuryVendorID)
	}
	return c.Save(ctx)
}

// VendorView is a vendor with its documents and the nearest expiry.
type VendorView struct {
	*ent.Vendor
	Documents   []*ent.VendorDocument `json:"documents"`
	NextExpiry  *time.Time            `json:"next_expiry,omitempty"`
	ExpiredDocs int                   `json:"expired_documents"`
}

func viewVendor(v *ent.Vendor, now time.Time) VendorView {
	out := VendorView{Vendor: v, Documents: v.Edges.Documents}
	if out.Documents == nil {
		out.Documents = []*ent.VendorDocument{}
	}
	for _, d := range v.Edges.Documents {
		if d.ExpiresAt == nil {
			continue
		}
		if d.ExpiresAt.Before(now) {
			out.ExpiredDocs++
		}
		if out.NextExpiry == nil || d.ExpiresAt.Before(*out.NextExpiry) {
			out.NextExpiry = d.ExpiresAt
		}
	}
	return out
}

// VendorListRow is a vendor list row: documents status plus personnel with has_pin.
type VendorListRow struct {
	VendorView
	Personnel []PersonnelView `json:"personnel"`
}

// ListVendors returns a keyset page of active and suspended vendors, newest first, with document
// status and personnel. Documents and personnel load in one batched query each for the page.
func (s *Service) ListVendors(ctx context.Context, status string, p page.Params) (page.Result[VendorListRow], error) {
	q := s.client.Vendor.Query()
	if status != "" {
		q = q.Where(vendor.StatusEQ(vendor.Status(status)))
	} else {
		q = q.Where(vendor.StatusNEQ(vendor.StatusInactive))
	}
	vs, err := q.Where(p.Predicate()).Modify(page.Order()).Limit(p.Limit + 1).
		WithDocuments().WithPersonnel().All(ctx)
	if err != nil {
		return page.Result[VendorListRow]{}, err
	}
	res := page.Build(vs, p.Limit, func(v *ent.Vendor) (uuid.UUID, time.Time) { return v.ID, v.CreatedAt })
	now := time.Now()
	rows := make([]VendorListRow, len(res.Data))
	for i, v := range res.Data {
		rows[i] = VendorListRow{VendorView: viewVendor(v, now), Personnel: ViewPersonnel(v.Edges.Personnel)}
	}
	return page.Result[VendorListRow]{Data: rows, NextCursor: res.NextCursor, HasMore: res.HasMore}, nil
}

// DocumentInput records a compliance document.
type DocumentInput struct {
	DocType   string     `json:"doc_type"`
	Number    string     `json:"number"`
	IssuedAt  *time.Time `json:"issued_at"`
	ExpiresAt *time.Time `json:"expires_at"`
	FileKey   string     `json:"file_key"`
}

// AddDocument records a vendor licence or certificate.
func (s *Service) AddDocument(ctx context.Context, vendorID uuid.UUID, in DocumentInput) (*ent.VendorDocument, error) {
	if in.DocType == "" {
		return nil, httpx.Invalid("doc_type is required")
	}
	c := s.client.VendorDocument.Create().SetVendorID(vendorID).SetDocType(in.DocType).SetNumber(in.Number).SetFileKey(in.FileKey)
	if in.IssuedAt != nil {
		c.SetIssuedAt(*in.IssuedAt)
	}
	if in.ExpiresAt != nil {
		c.SetExpiresAt(*in.ExpiresAt)
		if in.ExpiresAt.Before(time.Now()) {
			c.SetStatus(vendordocument.StatusExpired)
		} else if in.ExpiresAt.Before(time.Now().AddDate(0, 0, 30)) {
			c.SetStatus(vendordocument.StatusExpiring)
		}
	}
	return c.Save(ctx)
}

// AlertExpiring publishes expiry alerts at 30, 14 and 7 days (system job), once per threshold.
func (s *Service) AlertExpiring(ctx context.Context, tenants []uuid.UUID) (int, error) {
	if len(tenants) == 0 {
		return 0, nil
	}
	now := time.Now()
	docs, err := s.client.VendorDocument.Query().
		Where(vendordocument.TenantIDIn(tenants...), vendordocument.ExpiresAtNotNil(), vendordocument.ExpiresAtLT(now.AddDate(0, 0, 31))).
		WithVendor().Limit(1000).All(ctx)
	if err != nil {
		return 0, err
	}
	sent := 0
	for _, d := range docs {
		days := int(d.ExpiresAt.Sub(now).Hours() / 24)
		threshold := 0
		switch {
		case days < 0:
			threshold = -1
		case days <= 7:
			threshold = 7
		case days <= 14:
			threshold = 14
		default:
			threshold = 30
		}
		if d.LastAlertDays != nil && *d.LastAlertDays <= threshold {
			continue
		}
		tctx := tenantguard.With(ctx, d.TenantID)
		st := vendordocument.StatusExpiring
		if days < 0 {
			st = vendordocument.StatusExpired
		}
		if err := s.client.VendorDocument.UpdateOneID(d.ID).SetLastAlertDays(threshold).SetStatus(st).Exec(tctx); err != nil {
			continue
		}
		name := ""
		if d.Edges.Vendor != nil {
			name = d.Edges.Vendor.Name
		}
		_ = events.Publish(tctx, s.client.OutboxEvent, d.TenantID, d.ID.String(), events.VendorDocumentExpiring, map[string]any{
			"vendor_id": d.VendorID, "vendor": name, "doc_type": d.DocType, "expires_at": d.ExpiresAt, "days_left": days,
		})
		sent++
	}
	return sent, nil
}

// PersonnelView is a personnel row for the console: has_pin instead of the hash, which never leaves
// the server (pin_hash is a Sensitive column, so it is not serialised either way).
type PersonnelView struct {
	*ent.VendorPersonnel
	HasPIN bool `json:"has_pin"`
}

// ViewPersonnel wraps rows with has_pin.
func ViewPersonnel(rows []*ent.VendorPersonnel) []PersonnelView {
	out := make([]PersonnelView, len(rows))
	for i, p := range rows {
		out[i] = PersonnelView{VendorPersonnel: p, HasPIN: p.PinHash != ""}
	}
	return out
}

// ValidGuardPIN reports whether pin is 4 to 6 digits.
func ValidGuardPIN(pin string) bool {
	if len(pin) < 4 || len(pin) > 6 {
		return false
	}
	for _, c := range pin {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// SetGuardPIN stores a bcrypt hash of a guard's gate PIN (the fleet's PIN hashing, as pos-api).
// The hash lives in the existing sensitive pin_hash column, never in metadata, because metadata is
// returned in API responses.
func (s *Service) SetGuardPIN(ctx context.Context, vendorID, personnelID uuid.UUID, pin string) (*PersonnelView, error) {
	if !ValidGuardPIN(pin) {
		return nil, httpx.Invalid("the PIN must be 4 to 6 digits")
	}
	p, err := s.client.VendorPersonnel.Query().
		Where(vendorpersonnel.ID(personnelID), vendorpersonnel.VendorID(vendorID)).Only(ctx)
	if err != nil {
		return nil, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(pin), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	p, err = p.Update().SetPinHash(string(hash)).Save(ctx)
	if err != nil {
		return nil, err
	}
	return &PersonnelView{VendorPersonnel: p, HasPIN: true}, nil
}

// VendorDetail is one vendor with documents and personnel.
type VendorDetail struct {
	VendorView
	Personnel []PersonnelView `json:"personnel"`
}

// GetVendor returns a vendor with its documents and personnel. When scope is limited, personnel
// deployed only to properties outside it are left out.
func (s *Service) GetVendor(ctx context.Context, id uuid.UUID, scope []uuid.UUID, all bool) (*VendorDetail, error) {
	v, err := s.client.Vendor.Query().Where(vendor.ID(id)).
		WithDocuments(func(q *ent.VendorDocumentQuery) { q.Order(ent.Asc(vendordocument.FieldExpiresAt)) }).
		WithPersonnel(func(q *ent.VendorPersonnelQuery) { q.Order(ent.Asc(vendorpersonnel.FieldBadgeNumber)) }).Only(ctx)
	if err != nil {
		return nil, err
	}
	out := &VendorDetail{VendorView: viewVendor(v, time.Now())}
	visible := map[string]bool{}
	for _, p := range scope {
		visible[p.String()] = true
	}
	var people []*ent.VendorPersonnel
	for _, p := range v.Edges.Personnel {
		if all || len(p.PropertyIds) == 0 {
			people = append(people, p)
			continue
		}
		for _, pid := range p.PropertyIds {
			if visible[pid] {
				people = append(people, p)
				break
			}
		}
	}
	out.Personnel = ViewPersonnel(people)
	return out, nil
}

// AddPersonnel registers an agency person with a badge.
func (s *Service) AddPersonnel(ctx context.Context, vendorID uuid.UUID, name, role, phone, badge string, propertyIDs []string) (*ent.VendorPersonnel, error) {
	if name == "" || badge == "" {
		return nil, httpx.Invalid("name and badge_number are required")
	}
	return s.client.VendorPersonnel.Create().SetVendorID(vendorID).SetFullName(name).SetRole(role).
		SetPhone(phone).SetBadgeNumber(badge).SetPropertyIds(propertyIDs).Save(ctx)
}
