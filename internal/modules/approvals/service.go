// Package approvals is maskani's central approval engine, the same shape as treasury-api's and
// inventory-api's internal/modules/approvals: an estate configures ApprovalRules per module, each
// with an amount band and ordered steps naming the role allowed to act; submitting an object opens
// one ApprovalRequest with a pending action per step; approvers act on the current step in order;
// the request is approved after the last step and rejected the moment any step is rejected.
//
// Every workflow that needs sign-off (credit notes, waivers, manual payment verification, and the
// restructure, vendor bill, refund, quote, deposit, remittance and write-off flows as they land)
// calls this engine instead of keeping its own approval logic. Modules may register default steps
// used when no rule matches; a module without defaults needs no approval below its rules.
//
// Maskani differences from treasury: the person who submitted an object never approves it, one
// person approves at most one step of a request, a step may name a permission instead of a role
// (the built-in defaults), and requests carry the property for scoped inboxes.
package approvals

import (
	"context"
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/approvalrequest"
	"github.com/bengobox/maskani-api/internal/ent/approvalrule"
	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/shared/page"
)

// Step is one approval step. Exactly one of ApproverRole or Permission names who may act.
type Step struct {
	Sequence     int    `json:"sequence"`
	Name         string `json:"name"`
	ApproverRole string `json:"approver_role,omitempty"`
	Permission   string `json:"permission,omitempty"`
}

// approver is the step's inbox key: the role code, or "perm:" plus the permission.
func (st Step) approver() string {
	if st.ApproverRole != "" {
		return st.ApproverRole
	}
	return "perm:" + st.Permission
}

// Action is a step's decision on a request.
type Action struct {
	Step
	Status      string     `json:"status"` // pending, approved, rejected, skipped
	ActedBy     *uuid.UUID `json:"acted_by,omitempty"`
	ActedByName string     `json:"acted_by_name,omitempty"`
	ActedAt     *time.Time `json:"acted_at,omitempty"`
	Comment     string     `json:"comment,omitempty"`
}

// Actor is who acts on a step: their role codes, a permission check, and whether they are an
// administrator (tenant admin, platform owner) who may act on any step.
type Actor struct {
	UserID  uuid.UUID
	Name    string
	Roles   []string
	HasPerm func(string) bool
	Admin   bool
}

func (a Actor) may(st Step) bool {
	switch {
	case a.Admin:
		return true
	case st.ApproverRole != "":
		return slices.Contains(a.Roles, st.ApproverRole)
	case st.Permission != "":
		return a.HasPerm != nil && a.HasPerm(st.Permission)
	}
	return false
}

// Approvers lists the inbox keys an actor can act on (their roles and the permissions given).
func (a Actor) Approvers(perms []string) []string {
	out := append([]string{}, a.Roles...)
	for _, p := range perms {
		out = append(out, "perm:"+p)
	}
	return out
}

// Service is the engine.
type Service struct {
	client   *ent.Client
	mu       sync.RWMutex
	defaults map[string][]Step
	hooks    map[string]func(context.Context, *ent.ApprovalRequest) error
}

// NewService creates the engine.
func NewService(client *ent.Client) *Service {
	return &Service{client: client, defaults: map[string][]Step{}, hooks: map[string]func(context.Context, *ent.ApprovalRequest) error{}}
}

// OnDecision registers what a module does once one of its requests is approved or rejected (book
// the payment, raise the credit note). It also runs after a step that leaves the request pending,
// so it must do nothing then.
func (s *Service) OnDecision(module string, fn func(context.Context, *ent.ApprovalRequest) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hooks[module] = fn
}

// Decide acts on a request and runs the module's hook, so a decision from the central inbox has the
// same effect as one from the module's own screen.
func (s *Service) Decide(ctx context.Context, requestID uuid.UUID, by Actor, d Decision, comment string) (*ent.ApprovalRequest, error) {
	req, err := s.Act(ctx, requestID, by, d, comment)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	fn := s.hooks[string(req.Module)]
	s.mu.RUnlock()
	if fn != nil && req.Status != approvalrequest.StatusPending {
		if err := fn(ctx, req); err != nil {
			return req, err
		}
	}
	return req, nil
}

// Retry reruns the module's hook for an approved request whose effect failed (treasury was down).
func (s *Service) Retry(ctx context.Context, req *ent.ApprovalRequest) error {
	s.mu.RLock()
	fn := s.hooks[string(req.Module)]
	s.mu.RUnlock()
	if fn == nil || req.Status != approvalrequest.StatusApproved {
		return nil
	}
	return fn(ctx, req)
}

// SetDefault registers the steps a module uses when no rule matches an amount.
func (s *Service) SetDefault(module string, steps ...Step) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.defaults[module] = steps
}

// RuleSteps reads a rule's steps in order.
func RuleSteps(r *ent.ApprovalRule) []Step {
	b, _ := json.Marshal(r.Steps)
	var out []Step
	_ = json.Unmarshal(b, &out)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Sequence < out[j].Sequence })
	return out
}

// MatchRule returns the active rule for the module whose band holds the amount; the most specific
// (highest minimum) wins. Nil when none matches.
func (s *Service) MatchRule(ctx context.Context, module string, amount decimal.Decimal) (*ent.ApprovalRule, error) {
	rules, err := s.client.ApprovalRule.Query().Where(approvalrule.ModuleEQ(approvalrule.Module(module)), approvalrule.IsActive(true)).
		Limit(50).All(ctx)
	if err != nil {
		return nil, err
	}
	var best *ent.ApprovalRule
	for _, r := range rules {
		if amount.LessThan(r.MinAmount) || (r.MaxAmount != nil && amount.GreaterThan(*r.MaxAmount)) {
			continue
		}
		if best == nil || r.MinAmount.GreaterThan(best.MinAmount) {
			best = r
		}
	}
	return best, nil
}

// Submission is an object sent for approval.
type Submission struct {
	Module     string
	ObjectID   uuid.UUID
	Reference  string
	Amount     decimal.Decimal
	PropertyID *uuid.UUID
	By         uuid.UUID
	ByName     string
	Meta       map[string]any
}

// Submit opens the object's approval from the matching rule, or the module default. Any earlier
// pending request for the object is cancelled, so one workflow is live. Returns nil and false when
// nothing requires approval.
func (s *Service) Submit(ctx context.Context, in Submission) (*ent.ApprovalRequest, bool, error) {
	rule, err := s.MatchRule(ctx, in.Module, in.Amount)
	if err != nil {
		return nil, false, err
	}
	var steps []Step
	var ruleID *uuid.UUID
	if rule != nil {
		steps, ruleID = RuleSteps(rule), &rule.ID
	} else {
		s.mu.RLock()
		steps = s.defaults[in.Module]
		s.mu.RUnlock()
	}
	if len(steps) == 0 {
		return nil, false, nil
	}
	actions := make([]map[string]any, len(steps))
	for i, st := range steps {
		actions[i] = actionJSON(Action{Step: st, Status: "pending"})
	}
	if _, err := s.client.ApprovalRequest.Update().Where(approvalrequest.ObjectID(in.ObjectID),
		approvalrequest.StatusEQ(approvalrequest.StatusPending)).
		SetStatus(approvalrequest.StatusCancelled).SetDecidedAt(time.Now()).Save(ctx); err != nil {
		return nil, false, err
	}
	req, err := s.client.ApprovalRequest.Create().SetModule(approvalrequest.Module(in.Module)).SetObjectID(in.ObjectID).
		SetObjectReference(in.Reference).SetAmount(in.Amount).SetNillablePropertyID(in.PropertyID).SetNillableRuleID(ruleID).
		SetCurrentSequence(steps[0].Sequence).SetCurrentApprover(steps[0].approver()).SetActions(actions).
		SetSubmittedBy(in.By).SetSubmittedByName(in.ByName).SetMetadata(in.Meta).Save(ctx)
	if err != nil {
		return nil, false, err
	}
	return req, true, nil
}

// Actions reads a request's steps and decisions in order.
func Actions(r *ent.ApprovalRequest) []Action {
	b, _ := json.Marshal(r.Actions)
	var out []Action
	_ = json.Unmarshal(b, &out)
	return out
}

func actionJSON(a Action) map[string]any {
	b, _ := json.Marshal(a)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

// Decision is approve or reject.
type Decision string

const (
	Approve Decision = "approve"
	Reject  Decision = "reject"
)

// Act records the actor's decision on the request's current step. The submitter never acts, one
// person decides at most one step, and a reject needs a comment. The step is claimed with a
// compare-and-set on the pending status and current step.
func (s *Service) Act(ctx context.Context, requestID uuid.UUID, by Actor, d Decision, comment string) (*ent.ApprovalRequest, error) {
	req, err := s.client.ApprovalRequest.Get(ctx, requestID)
	if err != nil {
		return nil, err
	}
	if req.Status != approvalrequest.StatusPending {
		return nil, httpx.Conflict("this approval has already been decided")
	}
	if req.SubmittedBy != nil && *req.SubmittedBy == by.UserID {
		return nil, httpx.Forbidden("someone other than the person who sent it must decide this")
	}
	comment = strings.TrimSpace(comment)
	if d == Reject && comment == "" {
		return nil, httpx.Invalid("say why it is rejected")
	}
	acts := Actions(req)
	cur := -1
	for i, a := range acts {
		if a.ActedBy != nil && *a.ActedBy == by.UserID && a.Status == "approved" {
			return nil, httpx.Conflict("you have already approved a step of this; it needs another approver")
		}
		if a.Sequence == req.CurrentSequence && a.Status == "pending" {
			cur = i
		}
	}
	if cur < 0 {
		return nil, httpx.Conflict("there is no step waiting for a decision")
	}
	if !by.may(acts[cur].Step) {
		who := acts[cur].ApproverRole
		if who == "" {
			who = "someone who may approve this"
		}
		return nil, httpx.Forbidden("this step needs " + who)
	}
	now := time.Now()
	acts[cur].ActedBy, acts[cur].ActedByName, acts[cur].ActedAt, acts[cur].Comment = &by.UserID, by.Name, &now, comment
	upd := s.client.ApprovalRequest.Update().Where(approvalrequest.ID(req.ID),
		approvalrequest.StatusEQ(approvalrequest.StatusPending), approvalrequest.CurrentSequence(req.CurrentSequence))
	if d == Reject {
		acts[cur].Status = "rejected"
		for i := cur + 1; i < len(acts); i++ {
			acts[i].Status = "skipped"
		}
		upd = upd.SetStatus(approvalrequest.StatusRejected).SetDecidedAt(now).SetCurrentApprover("")
	} else {
		acts[cur].Status = "approved"
		if cur+1 < len(acts) {
			upd = upd.SetCurrentSequence(acts[cur+1].Sequence).SetCurrentApprover(acts[cur+1].approver())
		} else {
			upd = upd.SetStatus(approvalrequest.StatusApproved).SetDecidedAt(now).SetCurrentApprover("")
		}
	}
	out := make([]map[string]any, len(acts))
	for i, a := range acts {
		out[i] = actionJSON(a)
	}
	n, err := upd.SetActions(out).Save(ctx)
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, httpx.Conflict("someone else decided this step at the same moment; reload and try again")
	}
	return s.client.ApprovalRequest.Get(ctx, req.ID)
}

// Get returns one request.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*ent.ApprovalRequest, error) {
	return s.client.ApprovalRequest.Get(ctx, id)
}

// Latest returns the object's most recent request (nil when it never needed approval).
func (s *Service) Latest(ctx context.Context, objectID uuid.UUID) (*ent.ApprovalRequest, error) {
	r, err := s.client.ApprovalRequest.Query().Where(approvalrequest.ObjectID(objectID)).
		Order(ent.Desc(approvalrequest.FieldCreatedAt)).First(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	return r, err
}

// ForObjects returns each object's latest request, in one query, for lists that show approval
// progress beside their own rows.
func (s *Service) ForObjects(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]*ent.ApprovalRequest, error) {
	out := map[uuid.UUID]*ent.ApprovalRequest{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.client.ApprovalRequest.Query().Where(approvalrequest.ObjectIDIn(ids...)).
		Order(ent.Asc(approvalrequest.FieldCreatedAt)).All(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.ObjectID] = r // ascending, so the latest wins
	}
	return out, nil
}

// Cancel closes an object's pending request (the object was withdrawn).
func (s *Service) Cancel(ctx context.Context, objectID uuid.UUID) error {
	_, err := s.client.ApprovalRequest.Update().Where(approvalrequest.ObjectID(objectID),
		approvalrequest.StatusEQ(approvalrequest.StatusPending)).
		SetStatus(approvalrequest.StatusCancelled).SetDecidedAt(time.Now()).SetCurrentApprover("").Save(ctx)
	return err
}

// Filter narrows the inbox.
type Filter struct {
	Status      string
	Module      string
	PropertyIDs []uuid.UUID
	All         bool
	// Approvers, when set, keeps requests whose current step one of these keys may act on.
	Approvers []string
}

// List returns a keyset page of requests, newest first. Requests without a property are visible to
// callers who see all properties only.
func (s *Service) List(ctx context.Context, f Filter, p page.Params) (page.Result[*ent.ApprovalRequest], error) {
	q := s.client.ApprovalRequest.Query()
	if f.Status != "" {
		q = q.Where(approvalrequest.StatusEQ(approvalrequest.Status(f.Status)))
	}
	if f.Module != "" {
		q = q.Where(approvalrequest.ModuleEQ(approvalrequest.Module(f.Module)))
	}
	if !f.All {
		q = q.Where(approvalrequest.PropertyIDIn(f.PropertyIDs...))
	}
	if f.Approvers != nil {
		q = q.Where(approvalrequest.CurrentApproverIn(f.Approvers...))
	}
	rows, err := q.Where(p.Predicate()).Modify(page.Order()).Limit(p.Limit + 1).All(ctx)
	if err != nil {
		return page.Result[*ent.ApprovalRequest]{}, err
	}
	return page.Build(rows, p.Limit, func(r *ent.ApprovalRequest) (uuid.UUID, time.Time) { return r.ID, r.CreatedAt }), nil
}
