package reports

import (
	"context"
	stdsql "database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// rollupSQL rebuilds one day of daily_stats for every active property of a tenant in one statement.
// Collected and payments_count belong to the payment consumer (written once per payment, in the
// same transaction as its consumed-event row), so a rebuild never touches them. Arrears and unit
// counts are the position when the rebuild runs, which for the nightly job is the close of the day.
//
// Parameters: $1 tenant, $2 day (date), $3 day start and $4 next day start (timestamptz, the
// tenant's local midnight), $5 time zone name. Raw SQL bypasses the Ent guard, so every table is
// filtered by tenant_id here, and every time filter is a plain range on an indexed column.
const rollupSQL = `
WITH props AS (
  SELECT id FROM properties WHERE tenant_id = $1 AND status = 'active'
), billed AS (
  SELECT r.property_id, SUM(l.total) AS amt, COUNT(*) AS n
    FROM billing_run_lines l JOIN billing_runs r ON r.id = l.run_id AND r.tenant_id = $1
   WHERE l.tenant_id = $1 AND l.status = 'issued' AND (r.invoice_date AT TIME ZONE $5)::date = $2::date
   GROUP BY 1
), units_now AS (
  SELECT property_id, COUNT(*) AS total,
         COUNT(*) FILTER (WHERE occupancy_status IN ('owner_occupied','tenanted')) AS occupied,
         COUNT(*) FILTER (WHERE sale_status IN ('under_agreement','fully_paid','handed_over','titled','in_default')) AS sold
    FROM units WHERE tenant_id = $1 AND status = 'active' GROUP BY 1
), acc AS (
  SELECT ua.id, ua.balance, u.property_id
    FROM unit_accounts ua JOIN units u ON u.id = ua.unit_id AND u.tenant_id = $1
   WHERE ua.tenant_id = $1 AND ua.status = 'active' AND ua.balance > 0
), dues AS (
  SELECT l.unit_account_id AS acc_id, r.due_date, l.total AS amount
    FROM billing_run_lines l JOIN billing_runs r ON r.id = l.run_id AND r.tenant_id = $1
   WHERE l.tenant_id = $1 AND l.status = 'issued' AND l.unit_account_id IN (SELECT id FROM acc)
  UNION ALL
  SELECT sc.unit_account_id, i.due_date, i.amount
    FROM instalments i JOIN sale_contracts sc ON sc.id = i.contract_id AND sc.tenant_id = $1
   WHERE i.tenant_id = $1 AND i.treasury_invoice_id IS NOT NULL AND sc.unit_account_id IN (SELECT id FROM acc)
), running AS (
  SELECT acc_id, due_date, amount,
         SUM(amount) OVER (PARTITION BY acc_id ORDER BY due_date DESC ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) AS upto
    FROM dues
), oldest AS (
  SELECT r.acc_id, MIN(r.due_date) AS oldest_due
    FROM running r JOIN acc a ON a.id = r.acc_id
   WHERE r.upto - r.amount < a.balance
   GROUP BY r.acc_id
), aged AS (
  SELECT a.property_id, a.balance,
         GREATEST(0, $2::date - COALESCE(o.oldest_due::date, $2::date)) AS age
    FROM acc a LEFT JOIN oldest o ON o.acc_id = a.id
), arrears AS (
  SELECT property_id, SUM(balance) AS total,
         COALESCE(SUM(balance) FILTER (WHERE age <= 30), 0) AS a0,
         COALESCE(SUM(balance) FILTER (WHERE age > 30 AND age <= 60), 0) AS a31,
         COALESCE(SUM(balance) FILTER (WHERE age > 60 AND age <= 90), 0) AS a61,
         COALESCE(SUM(balance) FILTER (WHERE age > 90), 0) AS a90
    FROM aged GROUP BY 1
), water AS (
  SELECT m.property_id,
         COALESCE(SUM(r.consumption) FILTER (WHERE m.kind IN ('bulk_supply','borehole')), 0) AS supplied,
         COALESCE(SUM(r.consumption) FILTER (WHERE m.kind = 'unit'), 0) AS billed
    FROM meter_readings r JOIN meters m ON m.id = r.meter_id AND m.tenant_id = $1
   WHERE r.tenant_id = $1 AND r.status = 'accepted' AND r.read_at >= $3 AND r.read_at < $4
   GROUP BY 1
), wo AS (
  SELECT property_id,
         COUNT(*) FILTER (WHERE created_at >= $3 AND created_at < $4) AS opened,
         COUNT(*) FILTER (WHERE completed_at >= $3 AND completed_at < $4) AS closed,
         COUNT(*) FILTER (WHERE sla_breached AND resolution_due_at >= $3 AND resolution_due_at < $4) AS breaches
    FROM work_orders
   WHERE tenant_id = $1 AND ((created_at >= $3 AND created_at < $4) OR (completed_at >= $3 AND completed_at < $4)
         OR (resolution_due_at >= $3 AND resolution_due_at < $4))
   GROUP BY 1
), gate AS (
  SELECT property_id, COUNT(*) AS visitors
    FROM gate_events WHERE tenant_id = $1 AND kind = 'entry' AND occurred_at >= $3 AND occurred_at < $4
   GROUP BY 1
), inc AS (
  SELECT property_id, COUNT(*) AS n
    FROM incidents WHERE tenant_id = $1 AND occurred_at >= $3 AND occurred_at < $4
   GROUP BY 1
)
INSERT INTO daily_stats (id, tenant_id, created_at, updated_at, property_id, day, billed, collected, invoices_issued,
  payments_count, arrears_total, arrears_0_30, arrears_31_60, arrears_61_90, arrears_90_plus, units_total,
  units_occupied, units_vacant, units_sold, water_supplied_m3, water_billed_m3, wo_opened, wo_closed,
  wo_sla_breaches, visitors, incidents)
SELECT gen_random_uuid(), $1, now(), now(), p.id, $2::date,
       COALESCE(b.amt, 0), 0, COALESCE(b.n, 0), 0,
       COALESCE(ar.total, 0), COALESCE(ar.a0, 0), COALESCE(ar.a31, 0), COALESCE(ar.a61, 0), COALESCE(ar.a90, 0),
       COALESCE(un.total, 0), COALESCE(un.occupied, 0), COALESCE(un.total, 0) - COALESCE(un.occupied, 0), COALESCE(un.sold, 0),
       COALESCE(w.supplied, 0), COALESCE(w.billed, 0),
       COALESCE(wo.opened, 0), COALESCE(wo.closed, 0), COALESCE(wo.breaches, 0),
       COALESCE(g.visitors, 0), COALESCE(i.n, 0)
  FROM props p
  LEFT JOIN billed b ON b.property_id = p.id
  LEFT JOIN units_now un ON un.property_id = p.id
  LEFT JOIN arrears ar ON ar.property_id = p.id
  LEFT JOIN water w ON w.property_id = p.id
  LEFT JOIN wo ON wo.property_id = p.id
  LEFT JOIN gate g ON g.property_id = p.id
  LEFT JOIN inc i ON i.property_id = p.id
ON CONFLICT (tenant_id, property_id, day) DO UPDATE SET
  updated_at = now(), billed = EXCLUDED.billed, invoices_issued = EXCLUDED.invoices_issued,
  arrears_total = EXCLUDED.arrears_total, arrears_0_30 = EXCLUDED.arrears_0_30, arrears_31_60 = EXCLUDED.arrears_31_60,
  arrears_61_90 = EXCLUDED.arrears_61_90, arrears_90_plus = EXCLUDED.arrears_90_plus,
  units_total = EXCLUDED.units_total, units_occupied = EXCLUDED.units_occupied, units_vacant = EXCLUDED.units_vacant,
  units_sold = EXCLUDED.units_sold, water_supplied_m3 = EXCLUDED.water_supplied_m3, water_billed_m3 = EXCLUDED.water_billed_m3,
  wo_opened = EXCLUDED.wo_opened, wo_closed = EXCLUDED.wo_closed, wo_sla_breaches = EXCLUDED.wo_sla_breaches,
  visitors = EXCLUDED.visitors, incidents = EXCLUDED.incidents`

// RebuildDay recomputes one local day of daily_stats for a tenant (primary database handle).
func RebuildDay(ctx context.Context, db *stdsql.DB, tenantID uuid.UUID, day time.Time, loc *time.Location) error {
	local := day.In(loc)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	end := start.AddDate(0, 0, 1)
	if _, err := db.ExecContext(ctx, rollupSQL, tenantID, start.Format("2006-01-02"), start, end, loc.String()); err != nil {
		return fmt.Errorf("daily stats rollup: %w", err)
	}
	return nil
}

// RebuildTenants recomputes yesterday's daily_stats for every tenant with an active property, one
// statement per tenant (the nightly job).
func RebuildTenants(ctx context.Context, db *stdsql.DB, loc *time.Location) (int, error) {
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT tenant_id FROM properties WHERE status = 'active'`)
	if err != nil {
		return 0, err
	}
	var tenants []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		tenants = append(tenants, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	yesterday := time.Now().In(loc).AddDate(0, 0, -1)
	done := 0
	for _, t := range tenants {
		if ctx.Err() != nil {
			return done, ctx.Err()
		}
		if err := RebuildDay(ctx, db, t, yesterday, loc); err != nil {
			return done, fmt.Errorf("tenant %s: %w", t, err)
		}
		done++
	}
	return done, nil
}
