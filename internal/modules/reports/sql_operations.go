package reports

// Every statement takes $1 tenant, $2 first day (date), $3 the day after the range (date), and
// $4 the property ids literal or NULL for the whole tenant. Each is one grouped statement served by
// the tables' (tenant_id, property_id, date) indexes, so it costs the same at any size.

const maintenanceSQL = `
SELECT w.category, COUNT(*),
       COUNT(*) FILTER (WHERE w.completed_at IS NOT NULL),
       COUNT(*) FILTER (WHERE w.sla_breached),
       ROUND((AVG(EXTRACT(EPOCH FROM (w.responded_at - w.created_at))) FILTER (WHERE w.responded_at IS NOT NULL) / 3600)::numeric, 1),
       ROUND((AVG(EXTRACT(EPOCH FROM (w.completed_at - w.created_at))) FILTER (WHERE w.completed_at IS NOT NULL) / 3600)::numeric, 1),
       COALESCE(SUM(w.cost_amount), 0)
  FROM work_orders w
 WHERE w.tenant_id = $1 AND w.created_at >= $2::date AND w.created_at < $3::date AND w.status <> 'cancelled'
   AND ($4::text IS NULL OR w.property_id = ANY($4::text::uuid[]))
 GROUP BY 1
 ORDER BY 2 DESC`

const vendorsSQL = `
WITH jobs AS (
  SELECT w.vendor_id, COUNT(*) AS jobs,
         COUNT(*) FILTER (WHERE w.completed_at IS NOT NULL) AS done,
         COUNT(*) FILTER (WHERE w.completed_at IS NOT NULL AND NOT w.sla_breached) AS on_time,
         AVG(EXTRACT(EPOCH FROM (w.completed_at - w.created_at))) FILTER (WHERE w.completed_at IS NOT NULL) / 3600 AS hours,
         COALESCE(SUM(w.cost_amount), 0) AS cost
    FROM work_orders w
   WHERE w.tenant_id = $1 AND w.vendor_id IS NOT NULL AND w.created_at >= $2::date AND w.created_at < $3::date AND w.status <> 'cancelled'
     AND ($4::text IS NULL OR w.property_id = ANY($4::text::uuid[]))
   GROUP BY 1
), docs AS (
  SELECT d.vendor_id, COUNT(*) FILTER (WHERE d.expires_at < now()) AS expired,
         COUNT(*) FILTER (WHERE d.expires_at >= now() AND d.expires_at < now() + interval '30 days') AS expiring
    FROM vendor_documents d WHERE d.tenant_id = $1 GROUP BY 1
)
SELECT v.name, COALESCE(j.jobs, 0), COALESCE(j.done, 0),
       CASE WHEN COALESCE(j.done, 0) > 0 THEN ROUND(j.on_time * 100.0 / j.done, 1) END,
       ROUND(j.hours::numeric, 1), COALESCE(j.cost, 0), COALESCE(d.expired, 0), COALESCE(d.expiring, 0)
  FROM vendors v LEFT JOIN jobs j ON j.vendor_id = v.id LEFT JOIN docs d ON d.vendor_id = v.id
 WHERE v.tenant_id = $1 AND v.status = 'active'
 ORDER BY 2 DESC, 1`

const securitySQL = `
WITH months AS (
  SELECT to_char(gs, 'YYYY-MM') AS p FROM generate_series($2::date, ($3::date - interval '1 month'), interval '1 month') gs
), gate AS (
  SELECT to_char(g.occurred_at, 'YYYY-MM') AS p,
         COUNT(*) FILTER (WHERE g.kind = 'entry') AS entries,
         COUNT(*) FILTER (WHERE g.kind = 'exit') AS exits,
         COUNT(*) FILTER (WHERE g.kind = 'walk_in_request') AS walk_ins,
         COUNT(*) FILTER (WHERE g.kind = 'walk_in_request' AND g.decision = 'declined') AS declined,
         COUNT(*) FILTER (WHERE g.kind = 'denied') AS denied
    FROM gate_events g
   WHERE g.tenant_id = $1 AND g.occurred_at >= $2::date AND g.occurred_at < $3::date
     AND ($4::text IS NULL OR g.property_id = ANY($4::text::uuid[]))
   GROUP BY 1
), inc AS (
  SELECT to_char(i.occurred_at, 'YYYY-MM') AS p, COUNT(*) AS n, COUNT(*) FILTER (WHERE i.severity IN ('high', 'critical')) AS serious
    FROM incidents i
   WHERE i.tenant_id = $1 AND i.occurred_at >= $2::date AND i.occurred_at < $3::date
     AND ($4::text IS NULL OR i.property_id = ANY($4::text::uuid[]))
   GROUP BY 1
)
SELECT m.p, COALESCE(g.entries, 0), COALESCE(g.exits, 0), COALESCE(g.walk_ins, 0), COALESCE(g.declined, 0), COALESCE(g.denied, 0),
       COALESCE(i.n, 0), COALESCE(i.serious, 0)
  FROM months m LEFT JOIN gate g ON g.p = m.p LEFT JOIN inc i ON i.p = m.p
 ORDER BY m.p`
