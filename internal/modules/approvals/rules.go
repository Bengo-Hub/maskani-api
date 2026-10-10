package approvals

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/approvalrule"
	"github.com/bengobox/maskani-api/internal/http/httpx"
)

// RuleInput configures a module's approval for an amount band.
type RuleInput struct {
	Module    string           `json:"module"`
	Name      string           `json:"name"`
	MinAmount decimal.Decimal  `json:"min_amount"`
	MaxAmount *decimal.Decimal `json:"max_amount"`
	Steps     []Step           `json:"steps"`
	IsActive  *bool            `json:"is_active"`
}

func (in *RuleInput) validate() error {
	in.Name = strings.TrimSpace(in.Name)
	switch {
	case approvalrule.ModuleValidator(approvalrule.Module(in.Module)) != nil:
		return httpx.Invalid("unknown approval module")
	case in.MinAmount.IsNegative():
		return httpx.Invalid("the band cannot start below zero")
	case in.MaxAmount != nil && in.MaxAmount.LessThan(in.MinAmount):
		return httpx.Invalid("the band's top must be above its bottom")
	case len(in.Steps) < 1 || len(in.Steps) > 5:
		return httpx.Invalid("a rule has from 1 to 5 steps")
	case len(in.Name) > 120:
		return httpx.Invalid("keep the name under 120 characters")
	}
	for i := range in.Steps {
		st := &in.Steps[i]
		st.Sequence, st.Name, st.ApproverRole = i+1, strings.TrimSpace(st.Name), strings.TrimSpace(st.ApproverRole)
		st.Permission = "" // rules name roles; permission steps are the built-in defaults only
		if st.ApproverRole == "" {
			return httpx.Invalid("every step names the role that approves it")
		}
		if st.Name == "" {
			st.Name = "Step " + itoa(i+1)
		}
	}
	return nil
}

func itoa(n int) string { return decimal.NewFromInt(int64(n)).String() }

func stepsJSON(steps []Step) []map[string]any {
	out := make([]map[string]any, len(steps))
	for i, st := range steps {
		out[i] = map[string]any{"sequence": st.Sequence, "name": st.Name, "approver_role": st.ApproverRole}
	}
	return out
}

// Rules lists the estate's rules by module and band.
func (s *Service) Rules(ctx context.Context) ([]*ent.ApprovalRule, error) {
	return s.client.ApprovalRule.Query().Order(ent.Asc(approvalrule.FieldModule), ent.Asc(approvalrule.FieldMinAmount)).Limit(200).All(ctx)
}

// overlaps reports another active rule for the module whose band meets this one, so one amount
// never matches two rules.
func (s *Service) overlaps(ctx context.Context, in RuleInput, except *uuid.UUID) (bool, error) {
	rules, err := s.client.ApprovalRule.Query().Where(approvalrule.ModuleEQ(approvalrule.Module(in.Module)), approvalrule.IsActive(true)).
		Limit(50).All(ctx)
	if err != nil {
		return false, err
	}
	for _, r := range rules {
		if except != nil && r.ID == *except {
			continue
		}
		above := r.MaxAmount != nil && in.MinAmount.GreaterThan(*r.MaxAmount)
		below := in.MaxAmount != nil && in.MaxAmount.LessThan(r.MinAmount)
		if !above && !below {
			return true, nil
		}
	}
	return false, nil
}

// CreateRule adds a rule; active bands for one module may not overlap.
func (s *Service) CreateRule(ctx context.Context, in RuleInput) (*ent.ApprovalRule, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	if in.IsActive == nil || *in.IsActive {
		if clash, err := s.overlaps(ctx, in, nil); err != nil {
			return nil, err
		} else if clash {
			return nil, httpx.Conflict("this band overlaps another rule for the same workflow")
		}
	}
	c := s.client.ApprovalRule.Create().SetModule(approvalrule.Module(in.Module)).SetName(in.Name).SetMinAmount(in.MinAmount).
		SetNillableMaxAmount(in.MaxAmount).SetSteps(stepsJSON(in.Steps))
	if in.IsActive != nil {
		c.SetIsActive(*in.IsActive)
	}
	return c.Save(ctx)
}

// UpdateRule replaces a rule. Requests already open keep the steps they started with.
func (s *Service) UpdateRule(ctx context.Context, id uuid.UUID, in RuleInput) (*ent.ApprovalRule, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	if in.IsActive == nil || *in.IsActive {
		if clash, err := s.overlaps(ctx, in, &id); err != nil {
			return nil, err
		} else if clash {
			return nil, httpx.Conflict("this band overlaps another rule for the same workflow")
		}
	}
	u := s.client.ApprovalRule.UpdateOneID(id).SetModule(approvalrule.Module(in.Module)).SetName(in.Name).
		SetMinAmount(in.MinAmount).SetSteps(stepsJSON(in.Steps))
	if in.MaxAmount != nil {
		u.SetMaxAmount(*in.MaxAmount)
	} else {
		u.ClearMaxAmount()
	}
	if in.IsActive != nil {
		u.SetIsActive(*in.IsActive)
	}
	return u.Save(ctx)
}

// DeleteRule removes a rule; amounts in its band fall back to the module default.
func (s *Service) DeleteRule(ctx context.Context, id uuid.UUID) error {
	return s.client.ApprovalRule.DeleteOneID(id).Exec(ctx)
}
