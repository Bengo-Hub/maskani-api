package reports

import (
	"context"
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

// RoleSummary is what each kind of staff acts on today, for the focus panels at the top of the
// dashboard. The screen shows only the parts the caller's role uses; the API returns them all
// because they come from one statement.
type RoleSummary struct {
	Period string `json:"period"`
	// Caretaker: this month's reading round.
	Readings struct {
		Meters    int `json:"meters"`
		Read      int `json:"read"`
		ToRecheck int `json:"to_recheck"`
		Pending   int `json:"pending_review"`
	} `json:"readings"`
	// Maintenance queue.
	Works struct {
		Open     int `json:"open"`
		Urgent   int `json:"urgent_open"`
		Overdue  int `json:"past_due"`
		ToReview int `json:"awaiting_confirmation"`
	} `json:"works"`
	// Security: today at the gate.
	Gate struct {
		EntriesToday   int `json:"entries_today"`
		WalkInsPending int `json:"walk_ins_pending"`
		ActivePasses   int `json:"active_passes"`
		OpenIncidents  int `json:"open_incidents"`
		TabletsOffline int `json:"tablets_offline"`
	} `json:"gate"`
	// Finance queues.
	Finance struct {
		FailedBillLines int             `json:"failed_bill_lines"`
		AccountsOwing   int             `json:"accounts_owing"`
		Owing           decimal.Decimal `json:"owing"`
	} `json:"finance"`
	// Sales follow-ups.
	Sales struct {
		HoldsExpiring      int             `json:"holds_expiring_3d"`
		InstalmentsOverdue int             `json:"instalments_overdue"`
		OverdueAmount      decimal.Decimal `json:"overdue_amount"`
		InDefault          int             `json:"contracts_in_default"`
	} `json:"sales"`
}

// roleSQL: one row of scalar counts. $1 tenant, $2 property ids literal or NULL, $3 period
// (YYYY-MM), $4 start of today and $5 start of tomorrow (timestamptz, local midnight).
const roleSQL = `
SELECT
  (SELECT COUNT(*) FROM meters m WHERE m.tenant_id = $1 AND m.kind = 'unit' AND m.status = 'active'
     AND ($2::text IS NULL OR m.property_id = ANY($2::text::uuid[]))),
  (SELECT COUNT(DISTINCT r.meter_id) FROM meter_readings r JOIN meters m ON m.id = r.meter_id AND m.tenant_id = $1
     WHERE r.tenant_id = $1 AND r.period = $3 AND m.kind = 'unit' AND ($2::text IS NULL OR m.property_id = ANY($2::text::uuid[]))),
  (SELECT COUNT(*) FROM meter_readings r JOIN meters m ON m.id = r.meter_id AND m.tenant_id = $1
     WHERE r.tenant_id = $1 AND r.period = $3 AND r.status = 'recheck' AND ($2::text IS NULL OR m.property_id = ANY($2::text::uuid[]))),
  (SELECT COUNT(*) FROM meter_readings r JOIN meters m ON m.id = r.meter_id AND m.tenant_id = $1
     WHERE r.tenant_id = $1 AND r.period = $3 AND r.status = 'pending' AND ($2::text IS NULL OR m.property_id = ANY($2::text::uuid[]))),

  (SELECT COUNT(*) FROM work_orders w WHERE w.tenant_id = $1 AND w.status NOT IN ('completed','confirmed','closed','cancelled')
     AND ($2::text IS NULL OR w.property_id = ANY($2::text::uuid[]))),
  (SELECT COUNT(*) FROM work_orders w WHERE w.tenant_id = $1 AND w.status NOT IN ('completed','confirmed','closed','cancelled')
     AND w.priority IN ('emergency','high') AND ($2::text IS NULL OR w.property_id = ANY($2::text::uuid[]))),
  (SELECT COUNT(*) FROM work_orders w WHERE w.tenant_id = $1 AND w.status NOT IN ('completed','confirmed','closed','cancelled')
     AND w.resolution_due_at < now() AND ($2::text IS NULL OR w.property_id = ANY($2::text::uuid[]))),
  (SELECT COUNT(*) FROM work_orders w WHERE w.tenant_id = $1 AND w.status = 'completed'
     AND ($2::text IS NULL OR w.property_id = ANY($2::text::uuid[]))),

  (SELECT COUNT(*) FROM gate_events g WHERE g.tenant_id = $1 AND g.kind = 'entry' AND g.occurred_at >= $4 AND g.occurred_at < $5
     AND ($2::text IS NULL OR g.property_id = ANY($2::text::uuid[]))),
  (SELECT COUNT(*) FROM gate_events g WHERE g.tenant_id = $1 AND g.kind = 'walk_in_request' AND g.decision = 'pending'
     AND g.occurred_at >= $4 AND ($2::text IS NULL OR g.property_id = ANY($2::text::uuid[]))),
  (SELECT COUNT(*) FROM visitor_passes p WHERE p.tenant_id = $1 AND p.status = 'active' AND p.valid_from <= now() AND p.valid_to > now()
     AND ($2::text IS NULL OR p.property_id = ANY($2::text::uuid[]))),
  (SELECT COUNT(*) FROM incidents i WHERE i.tenant_id = $1 AND i.status IN ('open','investigating')
     AND ($2::text IS NULL OR i.property_id = ANY($2::text::uuid[]))),
  (SELECT COUNT(*) FROM gate_devices d WHERE d.tenant_id = $1 AND d.status = 'active'
     AND (d.last_seen_at IS NULL OR d.last_seen_at < now() - interval '15 minutes')
     AND ($2::text IS NULL OR d.property_id = ANY($2::text::uuid[]))),

  (SELECT COUNT(*) FROM billing_run_lines l JOIN billing_runs r ON r.id = l.run_id AND r.tenant_id = $1
     WHERE l.tenant_id = $1 AND l.status = 'failed' AND ($2::text IS NULL OR r.property_id = ANY($2::text::uuid[]))),
  (SELECT COUNT(*) FROM unit_accounts ua JOIN units u ON u.id = ua.unit_id AND u.tenant_id = $1
     WHERE ua.tenant_id = $1 AND ua.status = 'active' AND ua.balance > 0 AND ($2::text IS NULL OR u.property_id = ANY($2::text::uuid[]))),
  (SELECT COALESCE(SUM(ua.balance), 0) FROM unit_accounts ua JOIN units u ON u.id = ua.unit_id AND u.tenant_id = $1
     WHERE ua.tenant_id = $1 AND ua.status = 'active' AND ua.balance > 0 AND ($2::text IS NULL OR u.property_id = ANY($2::text::uuid[]))),

  (SELECT COUNT(*) FROM reservations rv JOIN units u ON u.id = rv.unit_id AND u.tenant_id = $1
     WHERE rv.tenant_id = $1 AND rv.status IN ('pending_payment','active') AND rv.expires_at < now() + interval '3 days'
     AND ($2::text IS NULL OR u.property_id = ANY($2::text::uuid[]))),
  (SELECT COUNT(*) FROM instalments i JOIN sale_contracts c ON c.id = i.contract_id AND c.tenant_id = $1
     WHERE i.tenant_id = $1 AND i.status NOT IN ('paid','waived') AND i.due_date < now()
     AND c.status NOT IN ('draft','cancelled','terminated') AND ($2::text IS NULL OR c.property_id = ANY($2::text::uuid[]))),
  (SELECT COALESCE(SUM(i.amount - i.paid_amount), 0) FROM instalments i JOIN sale_contracts c ON c.id = i.contract_id AND c.tenant_id = $1
     WHERE i.tenant_id = $1 AND i.status NOT IN ('paid','waived') AND i.due_date < now()
     AND c.status NOT IN ('draft','cancelled','terminated') AND ($2::text IS NULL OR c.property_id = ANY($2::text::uuid[]))),
  (SELECT COUNT(*) FROM sale_contracts c WHERE c.tenant_id = $1 AND c.status = 'in_default'
     AND ($2::text IS NULL OR c.property_id = ANY($2::text::uuid[])))`

// Roles returns the role focus counts for a scope, cached 60 seconds like the dashboard.
func (s *Service) Roles(ctx context.Context, sc Scope) (*RoleSummary, error) {
	v, err := s.cached(ctx, "roles:"+sc.key(), func() (any, error) { return s.roles(ctx, sc) })
	if err != nil {
		return nil, err
	}
	return v.(*RoleSummary), nil
}

func (s *Service) roles(ctx context.Context, sc Scope) (*RoleSummary, error) {
	now := time.Now().In(s.loc)
	out := &RoleSummary{Period: now.Format("2006-01")}
	if s.db == nil {
		return out, nil
	}
	tenantID, _ := tenantguard.TenantID(ctx)
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, s.loc)
	err := s.db.QueryRowContext(ctx, roleSQL, tenantID, sc.sqlIDs(), out.Period, day, day.AddDate(0, 0, 1)).Scan(
		&out.Readings.Meters, &out.Readings.Read, &out.Readings.ToRecheck, &out.Readings.Pending,
		&out.Works.Open, &out.Works.Urgent, &out.Works.Overdue, &out.Works.ToReview,
		&out.Gate.EntriesToday, &out.Gate.WalkInsPending, &out.Gate.ActivePasses, &out.Gate.OpenIncidents, &out.Gate.TabletsOffline,
		&out.Finance.FailedBillLines, &out.Finance.AccountsOwing, &out.Finance.Owing,
		&out.Sales.HoldsExpiring, &out.Sales.InstalmentsOverdue, &out.Sales.OverdueAmount, &out.Sales.InDefault)
	if err != nil {
		return nil, fmt.Errorf("role summary: %w", err)
	}
	return out, nil
}
