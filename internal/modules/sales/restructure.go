package sales

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/approvalrequest"
	"github.com/bengobox/maskani-api/internal/ent/instalment"
	"github.com/bengobox/maskani-api/internal/ent/instalmentschedule"
	"github.com/bengobox/maskani-api/internal/ent/salecontract"
	"github.com/bengobox/maskani-api/internal/events"
	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/approvals"
	"github.com/bengobox/maskani-api/internal/modules/rbac"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
	"github.com/bengobox/maskani-api/internal/shared/otp"
	"github.com/bengobox/maskani-api/internal/shared/secure"
)

// restructureModule is the approvals module for a new payment schedule on a sale.
const restructureModule = "restructure"

// RestructureProposed tells the buyer a new payment plan waits for their acceptance.
const RestructureProposed = "sale_contract.restructure_proposed"

// SetApprovals puts restructures on the central approvals engine (by default one approval by
// someone who manages sales, never the person who proposed it; an estate's rules add steps by
// amount) and gives the service the box that keeps one-time codes.
func (s *Service) SetApprovals(ap *approvals.Service, box *secure.Box) {
	s.approvals, s.box = ap, box
	ap.SetDefault(restructureModule, approvals.Step{Sequence: 1, Name: "Approve the new payment plan", Permission: rbac.PermSalesManage})
	ap.OnDecision(restructureModule, func(ctx context.Context, req *ent.ApprovalRequest) error {
		by := uuid.Nil
		for _, a := range approvals.Actions(req) {
			if a.ActedBy != nil {
				by = *a.ActedBy
			}
		}
		return s.decideRestructure(ctx, req.ObjectID, req.Status == approvalrequest.StatusApproved, by)
	})
}

// RestructureInput is a proposed new plan for what is not yet invoiced: instalments (monthly or
// quarterly over term_months from first_due) or outright (one balance payment on first_due, the
// buyer's early completion).
type RestructureInput struct {
	Reason     string `json:"reason"`
	Option     string `json:"option"` // instalments, outright
	Frequency  string `json:"frequency"`
	TermMonths int    `json:"term_months"`
	FirstDue   string `json:"first_due"` // YYYY-MM-DD
}

// beforeToday reports whether a calendar day (midnight in loc) is earlier than today in loc. Today
// itself is allowed. (Truncating to 24 hours would cut at UTC midnight, which is 03:00 in Nairobi,
// and refuse today.)
func beforeToday(day, now time.Time, loc *time.Location) bool {
	n := now.In(loc)
	return day.Before(time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, loc))
}

// ScheduleView is a schedule version with its lines.
type ScheduleView struct {
	*ent.InstalmentSchedule
	Instalments []*ent.Instalment `json:"instalments"`
	Total       decimal.Decimal   `json:"total"`
}

// ProposeRestructure writes the next schedule version as "proposed" for what the active schedule
// has not invoiced yet, and sends it for approval. Invoiced instalments stay as they are in
// treasury. Nothing changes for the buyer until the plan is approved and they accept it.
func (s *Service) ProposeRestructure(ctx context.Context, contractID, by uuid.UUID, byName string, in RestructureInput) (*ScheduleView, error) {
	if s.approvals == nil {
		return nil, httpx.Unavailable("approvals are not set up")
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if in.Reason == "" || len(in.Reason) > 1000 {
		return nil, httpx.Invalid("give the reason for the new plan (up to 1000 characters)")
	}
	if in.Option != "instalments" && in.Option != "outright" {
		return nil, httpx.Invalid("option must be instalments or outright")
	}
	if in.Option == "instalments" {
		if in.Frequency != "monthly" && in.Frequency != "quarterly" {
			return nil, httpx.Invalid("frequency must be monthly or quarterly")
		}
		if in.TermMonths < 1 || in.TermMonths > 240 {
			return nil, httpx.Invalid("term_months must be between 1 and 240")
		}
	}
	first, err := time.ParseInLocation("2006-01-02", in.FirstDue, s.loc)
	if err != nil {
		return nil, httpx.Invalid("first_due must be a date (YYYY-MM-DD)")
	}
	if beforeToday(first, time.Now(), s.loc) {
		return nil, httpx.Invalid("the first payment cannot be in the past")
	}
	sc, err := s.client.SaleContract.Get(ctx, contractID)
	if err != nil {
		return nil, err
	}
	if sc.Status != salecontract.StatusActive && sc.Status != salecontract.StatusInDefault {
		return nil, httpx.Conflict("only an active contract, or one in default, can get a new plan")
	}
	if pending, err := s.client.InstalmentSchedule.Query().Where(instalmentschedule.ContractID(sc.ID),
		instalmentschedule.StatusEQ(instalmentschedule.StatusProposed)).Exist(ctx); err != nil {
		return nil, err
	} else if pending {
		return nil, httpx.Conflict("a new plan is already waiting for approval or the buyer; decide that one first")
	}
	active, err := s.client.InstalmentSchedule.Query().Where(instalmentschedule.ContractID(sc.ID),
		instalmentschedule.StatusEQ(instalmentschedule.StatusActive)).Only(ctx)
	if err != nil {
		return nil, err
	}
	rest, err := s.client.Instalment.Query().Where(instalment.ScheduleID(active.ID), instalment.StatusEQ(instalment.StatusScheduled)).All(ctx)
	if err != nil {
		return nil, err
	}
	remaining := decimal.Zero
	for _, r := range rest {
		remaining = remaining.Add(r.Amount)
	}
	if !remaining.IsPositive() {
		return nil, httpx.Conflict("everything on this plan is already billed; there is nothing left to restructure")
	}
	lines := BuildSchedule(ScheduleInput{NetPrice: remaining, Option: in.Option, Frequency: in.Frequency,
		TermMonths: in.TermMonths, Start: first, DepositDue: first, FirstDue: first})
	last, err := s.client.InstalmentSchedule.Query().Where(instalmentschedule.ContractID(sc.ID)).
		Order(ent.Desc(instalmentschedule.FieldVersion)).First(ctx)
	if err != nil {
		return nil, err
	}

	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	sch, err := tx.InstalmentSchedule.Create().SetContractID(sc.ID).SetVersion(last.Version + 1).
		SetStatus(instalmentschedule.StatusProposed).SetReason(in.Reason).
		SetMetadata(map[string]any{"proposed_by": by.String(), "proposed_by_name": byName, "replaces_version": active.Version,
			"remaining": remaining.StringFixed(2), "option": in.Option, "frequency": in.Frequency, "term_months": in.TermMonths}).Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	rows := make([]*ent.InstalmentCreate, len(lines))
	for i, l := range lines {
		rows[i] = tx.Instalment.Create().SetScheduleID(sch.ID).SetContractID(sc.ID).SetSeq(l.Seq).
			SetKind(instalment.Kind(l.Kind)).SetDueDate(l.DueDate).SetAmount(l.Amount).SetMilestoneLabel(l.Label)
	}
	if err := tx.Instalment.CreateBulk(rows...).Exec(ctx); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	pid := sc.PropertyID
	_, required, err := s.approvals.Submit(ctx, approvals.Submission{Module: restructureModule, ObjectID: sch.ID,
		Reference: sc.ContractNumber, Amount: remaining, PropertyID: &pid, By: by, ByName: byName,
		Meta: map[string]any{"label": fmt.Sprintf("New payment plan for %s: %s over %d payments", sc.ContractNumber,
			remaining.StringFixed(2), len(lines)), "contract_id": sc.ID.String()}})
	if err != nil {
		return nil, err
	}
	if !required {
		if err := s.decideRestructure(ctx, sch.ID, true, by); err != nil {
			return nil, err
		}
	}
	return s.scheduleView(ctx, sch.ID)
}

// decideRestructure applies the approval: approved marks the plan ready for the buyer and tells
// them; rejected closes it.
func (s *Service) decideRestructure(ctx context.Context, scheduleID uuid.UUID, approved bool, by uuid.UUID) error {
	sch, err := s.client.InstalmentSchedule.Get(ctx, scheduleID)
	if err != nil {
		return err
	}
	if sch.Status != instalmentschedule.StatusProposed {
		return nil
	}
	if !approved {
		return s.client.InstalmentSchedule.UpdateOneID(sch.ID).SetStatus(instalmentschedule.StatusRejected).Exec(ctx)
	}
	if err := s.client.InstalmentSchedule.UpdateOneID(sch.ID).SetApprovedAt(time.Now()).SetApprovedBy(by).Exec(ctx); err != nil {
		return err
	}
	sc, err := s.client.SaleContract.Get(ctx, sch.ContractID)
	if err != nil {
		return err
	}
	buyer, err := s.client.Party.Get(ctx, sc.PrimaryBuyerID)
	if err != nil {
		return err
	}
	v, err := s.scheduleView(ctx, sch.ID)
	if err != nil {
		return err
	}
	first := ""
	if len(v.Instalments) > 0 {
		first = v.Instalments[0].DueDate.In(s.loc).Format("2 Jan 2006")
	}
	tenantID, _ := tenantguard.TenantID(ctx)
	return events.Publish(ctx, s.client.OutboxEvent, tenantID, sch.ID.String(), RestructureProposed, map[string]any{
		"contract_id": sc.ID, "contract_number": sc.ContractNumber, "name": buyer.DisplayName, "phone": buyer.Phone,
		"email": buyer.Email, "amount": v.Total.StringFixed(2), "payments": len(v.Instalments), "first_due": first})
}

// SendRestructureCode sends the buyer a code to accept an approved plan at the office.
func (s *Service) SendRestructureCode(ctx context.Context, scheduleID uuid.UUID) error {
	sch, sc, err := s.acceptable(ctx, scheduleID)
	if err != nil {
		return err
	}
	buyer, err := s.client.Party.Get(ctx, sc.PrimaryBuyerID)
	if err != nil {
		return err
	}
	meta := maps.Clone(sch.Metadata)
	if meta == nil {
		meta = map[string]any{}
	}
	code, err := otp.Issue(meta, s.box, sch.ID.String())
	if err != nil {
		return err
	}
	if err := s.client.InstalmentSchedule.UpdateOneID(sch.ID).SetMetadata(meta).Exec(ctx); err != nil {
		return err
	}
	tenantID, _ := tenantguard.TenantID(ctx)
	return otp.Publish(ctx, s.client.OutboxEvent, tenantID, sch.ID.String(), meta, "restructure", buyer.DisplayName, buyer.Phone, buyer.Email, code)
}

// Acceptance is how the buyer accepted: signed in to the portal, or a code read back at the office.
type Acceptance struct {
	Via    string // portal, code
	Code   string
	Person uuid.UUID
}

// AcceptRestructure puts an approved plan in force once the buyer accepts: the old version is
// superseded (its invoiced lines stay and keep syncing with treasury), the new one becomes active,
// a contract in default is back in good standing, and anything already due is invoiced.
func (s *Service) AcceptRestructure(ctx context.Context, scheduleID uuid.UUID, a Acceptance) (*ScheduleView, error) {
	sch, sc, err := s.acceptable(ctx, scheduleID)
	if err != nil {
		return nil, err
	}
	meta := maps.Clone(sch.Metadata)
	if meta == nil {
		meta = map[string]any{}
	}
	switch a.Via {
	case "code":
		if err := otp.Check(meta, s.box, sch.ID.String(), a.Code); err != nil {
			if uerr := s.client.InstalmentSchedule.UpdateOneID(sch.ID).SetMetadata(meta).Exec(ctx); uerr != nil {
				return nil, uerr
			}
			return nil, err
		}
	case "portal":
	default:
		return nil, httpx.Invalid("accept in the portal or with the code")
	}
	otp.Clear(meta)
	meta["accepted_via"], meta["accepted_by"] = a.Via, a.Person.String()
	now := time.Now()
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := tx.InstalmentSchedule.Update().Where(instalmentschedule.ContractID(sc.ID),
		instalmentschedule.StatusEQ(instalmentschedule.StatusActive)).SetStatus(instalmentschedule.StatusSuperseded).Save(ctx); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	// Only proposed moves on: a second acceptance racing this one finds nothing and stops here.
	n, err := tx.InstalmentSchedule.Update().Where(instalmentschedule.ID(sch.ID), instalmentschedule.StatusEQ(instalmentschedule.StatusProposed)).
		SetStatus(instalmentschedule.StatusActive).SetBuyerAcceptedAt(now).SetMetadata(meta).Save(ctx)
	if err != nil || n == 0 {
		_ = tx.Rollback()
		if err == nil {
			err = httpx.Conflict("this plan has already been accepted or closed")
		}
		return nil, err
	}
	up := tx.SaleContract.UpdateOneID(sc.ID)
	if sc.Status == salecontract.StatusInDefault {
		up.SetStatus(salecontract.StatusActive).ClearDefaultSince()
	}
	if err := up.Exec(ctx); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	_, _ = s.InvoiceDue(ctx, &sc.ID, time.Now().AddDate(0, 0, 7))
	return s.scheduleView(ctx, sch.ID)
}

// acceptable loads an approved plan still waiting for the buyer.
func (s *Service) acceptable(ctx context.Context, scheduleID uuid.UUID) (*ent.InstalmentSchedule, *ent.SaleContract, error) {
	sch, err := s.client.InstalmentSchedule.Get(ctx, scheduleID)
	if err != nil {
		return nil, nil, err
	}
	if sch.Status != instalmentschedule.StatusProposed || sch.ApprovedAt == nil {
		return nil, nil, httpx.Conflict("this plan is not approved and waiting for the buyer")
	}
	sc, err := s.client.SaleContract.Get(ctx, sch.ContractID)
	if err != nil {
		return nil, nil, err
	}
	return sch, sc, nil
}

// Schedules lists a contract's schedule versions, newest first, with their lines.
func (s *Service) Schedules(ctx context.Context, contractID uuid.UUID) ([]*ScheduleView, error) {
	rows, err := s.client.InstalmentSchedule.Query().Where(instalmentschedule.ContractID(contractID)).
		Order(ent.Desc(instalmentschedule.FieldVersion)).WithInstalments(func(q *ent.InstalmentQuery) {
		q.Order(ent.Asc(instalment.FieldSeq))
	}).Limit(50).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*ScheduleView, 0, len(rows))
	for _, r := range rows {
		out = append(out, toScheduleView(r, r.Edges.Instalments))
	}
	return out, nil
}

// ScheduleContract returns the contract a schedule belongs to (for scope checks).
func (s *Service) ScheduleContract(ctx context.Context, scheduleID uuid.UUID) (*ent.SaleContract, error) {
	sch, err := s.client.InstalmentSchedule.Get(ctx, scheduleID)
	if err != nil {
		return nil, err
	}
	return s.client.SaleContract.Get(ctx, sch.ContractID)
}

func (s *Service) scheduleView(ctx context.Context, id uuid.UUID) (*ScheduleView, error) {
	sch, err := s.client.InstalmentSchedule.Query().Where(instalmentschedule.ID(id)).WithInstalments(func(q *ent.InstalmentQuery) {
		q.Order(ent.Asc(instalment.FieldSeq))
	}).Only(ctx)
	if err != nil {
		return nil, err
	}
	return toScheduleView(sch, sch.Edges.Instalments), nil
}

func toScheduleView(sch *ent.InstalmentSchedule, ins []*ent.Instalment) *ScheduleView {
	if ins == nil {
		ins = []*ent.Instalment{}
	}
	sch.Metadata = otp.Hide(sch.Metadata)
	v := &ScheduleView{InstalmentSchedule: sch, Instalments: ins, Total: decimal.Zero}
	for _, in := range ins {
		v.Total = v.Total.Add(in.Amount)
	}
	return v
}
