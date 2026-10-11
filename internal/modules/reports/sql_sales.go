package reports

import (
	"context"
	"time"

	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

// Sales reports. Receivables run over the chosen months like the other range reports; the sales
// position and the handover and title tracker are as of today, so they take only the tenant ($1),
// the property ids literal or NULL ($2) and today or the time zone ($3). Every statement is one grouped query on the
// contracts' (tenant_id, property_id) and instalments' (tenant_id, contract_id) indexes.

// salesPositionSQL: one row per live contract with what is paid, what is left, what is overdue and
// the next date.
const salesPositionSQL = `
WITH c AS (
  SELECT c.id, c.contract_number, c.status, c.net_price, c.paid_total, c.unit_id, c.primary_buyer_id
    FROM sale_contracts c
   WHERE c.tenant_id = $1 AND c.status NOT IN ('draft', 'cancelled', 'terminated')
     AND ($2::text IS NULL OR c.property_id = ANY($2::text::uuid[]))
), ins AS (
  SELECT i.contract_id,
         COALESCE(SUM(i.amount - i.paid_amount) FILTER (WHERE i.status NOT IN ('paid', 'waived') AND i.due_date < $3::date
           AND i.treasury_invoice_id IS NOT NULL), 0) AS overdue,
         MIN(i.due_date) FILTER (WHERE i.status NOT IN ('paid', 'waived') AND i.due_date >= $3::date) AS next_due
    FROM instalments i JOIN instalment_schedules s ON s.id = i.schedule_id
   WHERE i.tenant_id = $1 AND i.contract_id IN (SELECT id FROM c)
     AND (s.status = 'active' OR i.treasury_invoice_id IS NOT NULL)
   GROUP BY 1
)
SELECT c.contract_number, u.code, p.display_name, c.status, c.net_price, c.paid_total, c.net_price - c.paid_total,
       COALESCE(ins.overdue, 0), to_char(ins.next_due, 'YYYY-MM-DD')
  FROM c JOIN units u ON u.id = c.unit_id AND u.tenant_id = $1
  JOIN parties p ON p.id = c.primary_buyer_id AND p.tenant_id = $1
  LEFT JOIN ins ON ins.contract_id = c.id
 ORDER BY COALESCE(ins.overdue, 0) DESC, c.contract_number
 LIMIT 2000`

// receivablesSQL: instalment money by due month over the chosen months, what is already billed
// and paid, and what is still to come. Range parameters as the other range reports.
const receivablesSQL = `
SELECT to_char(i.due_date, 'YYYY-MM'), COUNT(*),
       COALESCE(SUM(i.amount), 0),
       COALESCE(SUM(i.amount) FILTER (WHERE i.treasury_invoice_id IS NOT NULL), 0),
       COALESCE(SUM(i.paid_amount), 0),
       COALESCE(SUM(i.amount - i.paid_amount) FILTER (WHERE i.status NOT IN ('paid', 'waived')), 0)
  FROM instalments i
  JOIN instalment_schedules s ON s.id = i.schedule_id
  JOIN sale_contracts c ON c.id = i.contract_id AND c.tenant_id = $1
 WHERE i.tenant_id = $1 AND i.due_date >= $2::date AND i.due_date < $3::date
   AND (s.status = 'active' OR i.treasury_invoice_id IS NOT NULL)
   AND c.status NOT IN ('draft', 'cancelled', 'terminated')
   AND ($4::text IS NULL OR c.property_id = ANY($4::text::uuid[]))
 GROUP BY 1 ORDER BY 1`

// handoverTitleSQL: every paid-up sale on its way to title: handover state and date, snags still
// open, and title stages done.
const handoverTitleSQL = `
WITH c AS (
  SELECT c.id, c.contract_number, c.status, c.unit_id, c.primary_buyer_id
    FROM sale_contracts c
   WHERE c.tenant_id = $1 AND c.status IN ('fully_paid', 'handed_over', 'titled')
     AND ($2::text IS NULL OR c.property_id = ANY($2::text::uuid[]))
), t AS (
  SELECT ts.contract_id, COUNT(*) FILTER (WHERE ts.status = 'done') AS done,
         COUNT(*) FILTER (WHERE ts.status = 'blocked') AS blocked
    FROM title_stages ts WHERE ts.tenant_id = $1 AND ts.contract_id IN (SELECT id FROM c) GROUP BY 1
)
SELECT c.contract_number, u.code, p.display_name, c.status,
       COALESCE(h.status, 'not booked'), to_char(COALESCE(h.completed_at, h.scheduled_at) AT TIME ZONE $3, 'YYYY-MM-DD'),
       COALESCE((SELECT COUNT(*) FROM jsonb_array_elements(h.snag_items) e WHERE e->>'status' IS DISTINCT FROM 'fixed'), 0),
       COALESCE(t.done, 0), COALESCE(t.blocked, 0)
  FROM c JOIN units u ON u.id = c.unit_id AND u.tenant_id = $1
  JOIN parties p ON p.id = c.primary_buyer_id AND p.tenant_id = $1
  LEFT JOIN handovers h ON h.contract_id = c.id AND h.tenant_id = $1
  LEFT JOIN t ON t.contract_id = c.id
 ORDER BY c.status, c.contract_number
 LIMIT 2000`

// salesPosition is the sales position as of today.
func salesPosition(ctx context.Context, s *Service, sc Scope, t *Table, _ time.Time) (*Table, error) {
	tenantID, _ := tenantguard.TenantID(ctx)
	return s.fill(ctx, t, salesPositionSQL, tenantID, sc.sqlIDs(), time.Now().In(s.loc).Format("2006-01-02"))
}

// handoverTitle is the handover and title tracker as of today.
func handoverTitle(ctx context.Context, s *Service, sc Scope, t *Table, _ time.Time) (*Table, error) {
	tenantID, _ := tenantguard.TenantID(ctx)
	return s.fill(ctx, t, handoverTitleSQL, tenantID, sc.sqlIDs(), s.loc.String())
}
