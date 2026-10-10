package settings

import (
	"context"
	"slices"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/approvalrule"
	"github.com/bengobox/maskani-api/internal/http/httpx"
)

// ApprovalRuleInput sets who approves an action in an amount band: how many approvals (levels)
// and, optionally, which role codes may give them.
type ApprovalRuleInput struct {
	Action        string           `json:"action"`
	MinAmount     decimal.Decimal  `json:"min_amount"`
	MaxAmount     *decimal.Decimal `json:"max_amount"`
	Levels        int              `json:"levels"`
	ApproverRoles []string         `json:"approver_roles"`
	Active        *bool            `json:"active"`
}

// ruleActions are the actions a rule can govern today (credits); the rest of the enum waits for
// the flows that use it.
var ruleActions = []string{string(approvalrule.ActionCreditNote), string(approvalrule.ActionAdjustment)}

func (in ApprovalRuleInput) validate() error {
	switch {
	case !slices.Contains(ruleActions, in.Action):
		return httpx.Invalid("action must be credit_note or adjustment")
	case in.MinAmount.IsNegative():
		return httpx.Invalid("the band cannot start below zero")
	case in.MaxAmount != nil && in.MaxAmount.LessThan(in.MinAmount):
		return httpx.Invalid("the band's top must be above its bottom")
	case in.Levels < 1 || in.Levels > 3:
		return httpx.Invalid("from 1 to 3 approvals")
	case len(in.ApproverRoles) > 10:
		return httpx.Invalid("at most 10 roles")
	}
	return nil
}

// ApprovalRules lists the estate's rules, credits first, by band.
func (s *Service) ApprovalRules(ctx context.Context) ([]*ent.ApprovalRule, error) {
	return s.client.ApprovalRule.Query().Order(ent.Asc(approvalrule.FieldAction), ent.Asc(approvalrule.FieldMinAmount)).Limit(100).All(ctx)
}

// overlaps reports an active rule for the same action whose band meets this one, so one amount
// never matches two rules.
func (s *Service) overlaps(ctx context.Context, in ApprovalRuleInput, except *uuid.UUID) (bool, error) {
	rules, err := s.client.ApprovalRule.Query().Where(approvalrule.ActionEQ(approvalrule.Action(in.Action)), approvalrule.Active(true)).All(ctx)
	if err != nil {
		return false, err
	}
	for _, r := range rules {
		if except != nil && r.ID == *except {
			continue
		}
		aboveTheirTop := r.MaxAmount != nil && in.MinAmount.GreaterThan(*r.MaxAmount)
		belowTheirBottom := in.MaxAmount != nil && in.MaxAmount.LessThan(r.MinAmount)
		if !aboveTheirTop && !belowTheirBottom {
			return true, nil
		}
	}
	return false, nil
}

// CreateApprovalRule adds a rule; bands of active rules for one action may not overlap.
func (s *Service) CreateApprovalRule(ctx context.Context, in ApprovalRuleInput) (*ent.ApprovalRule, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	if clash, err := s.overlaps(ctx, in, nil); err != nil {
		return nil, err
	} else if clash {
		return nil, httpx.Conflict("this band overlaps another rule for the same action")
	}
	c := s.client.ApprovalRule.Create().SetAction(approvalrule.Action(in.Action)).SetMinAmount(in.MinAmount).
		SetNillableMaxAmount(in.MaxAmount).SetLevels(in.Levels).SetApproverRoles(in.ApproverRoles)
	if in.Active != nil {
		c.SetActive(*in.Active)
	}
	return c.Save(ctx)
}

// UpdateApprovalRule replaces a rule's band, levels, roles and switch.
func (s *Service) UpdateApprovalRule(ctx context.Context, id uuid.UUID, in ApprovalRuleInput) (*ent.ApprovalRule, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	if in.Active == nil || *in.Active {
		if clash, err := s.overlaps(ctx, in, &id); err != nil {
			return nil, err
		} else if clash {
			return nil, httpx.Conflict("this band overlaps another rule for the same action")
		}
	}
	u := s.client.ApprovalRule.UpdateOneID(id).SetAction(approvalrule.Action(in.Action)).SetMinAmount(in.MinAmount).
		SetLevels(in.Levels).SetApproverRoles(in.ApproverRoles)
	if in.MaxAmount != nil {
		u.SetMaxAmount(*in.MaxAmount)
	} else {
		u.ClearMaxAmount()
	}
	if in.Active != nil {
		u.SetActive(*in.Active)
	}
	return u.Save(ctx)
}

// DeleteApprovalRule removes a rule; credits in its band fall back to one approval.
func (s *Service) DeleteApprovalRule(ctx context.Context, id uuid.UUID) error {
	return s.client.ApprovalRule.DeleteOneID(id).Exec(ctx)
}
