package reports

// Every statement takes $1 tenant, $2 first day (date), $3 the day after the range (date), and
// $4 the property ids literal or NULL for the whole tenant. Each is one grouped statement served by
// the tables' (tenant_id, property_id, date) indexes, so it costs the same at any size.

const collectionsMonthSQL = `
WITH months AS (
  SELECT to_char(gs, 'YYYY-MM') AS p FROM generate_series($2::date, ($3::date - interval '1 month'), interval '1 month') gs
), billed AS (
  SELECT r.period AS p, SUM(l.total) AS amt
    FROM billing_run_lines l JOIN billing_runs r ON r.id = l.run_id AND r.tenant_id = $1
   WHERE l.tenant_id = $1 AND l.status = 'issued' AND r.period >= to_char($2::date, 'YYYY-MM') AND r.period < to_char($3::date, 'YYYY-MM')
     AND ($4::text IS NULL OR r.property_id = ANY($4::text::uuid[]))
   GROUP BY 1
), coll AS (
  SELECT to_char(d.day, 'YYYY-MM') AS p, SUM(d.collected) AS amt
    FROM daily_stats d
   WHERE d.tenant_id = $1 AND d.day >= $2::date AND d.day < $3::date
     AND ($4::text IS NULL OR d.property_id = ANY($4::text::uuid[]))
   GROUP BY 1
)
SELECT m.p, COALESCE(b.amt, 0), COALESCE(c.amt, 0),
       CASE WHEN COALESCE(b.amt, 0) > 0 THEN ROUND(COALESCE(c.amt, 0) * 100 / b.amt, 1) END
  FROM months m LEFT JOIN billed b ON b.p = m.p LEFT JOIN coll c ON c.p = m.p
 ORDER BY m.p`

const collectionsBlockSQL = `
WITH bu AS (
  SELECT u.id, u.block_id FROM units u
   WHERE u.tenant_id = $1 AND u.status = 'active' AND ($4::text IS NULL OR u.property_id = ANY($4::text::uuid[]))
), n AS (
  SELECT block_id, COUNT(*) AS units FROM bu GROUP BY 1
), billed AS (
  SELECT bu.block_id, SUM(l.total) AS amt
    FROM billing_run_lines l JOIN billing_runs r ON r.id = l.run_id AND r.tenant_id = $1 JOIN bu ON bu.id = l.unit_id
   WHERE l.tenant_id = $1 AND l.status = 'issued' AND r.period >= to_char($2::date, 'YYYY-MM') AND r.period < to_char($3::date, 'YYYY-MM')
   GROUP BY 1
), coll AS (
  SELECT bu.block_id, SUM(ac.amount) AS amt
    FROM account_collections ac JOIN bu ON bu.id = ac.unit_id
   WHERE ac.tenant_id = $1 AND ac.day >= $2::date AND ac.day < $3::date
   GROUP BY 1
), owing AS (
  SELECT bu.block_id, SUM(ua.balance) AS amt
    FROM unit_accounts ua JOIN bu ON bu.id = ua.unit_id
   WHERE ua.tenant_id = $1 AND ua.status = 'active' AND ua.balance > 0
   GROUP BY 1
)
SELECT COALESCE(b.name, 'No block'), n.units, COALESCE(bi.amt, 0), COALESCE(c.amt, 0),
       CASE WHEN COALESCE(bi.amt, 0) > 0 THEN ROUND(COALESCE(c.amt, 0) * 100 / bi.amt, 1) END, COALESCE(o.amt, 0)
  FROM n LEFT JOIN blocks b ON b.id = n.block_id
  LEFT JOIN billed bi ON bi.block_id IS NOT DISTINCT FROM n.block_id
  LEFT JOIN coll c ON c.block_id IS NOT DISTINCT FROM n.block_id
  LEFT JOIN owing o ON o.block_id IS NOT DISTINCT FROM n.block_id
 ORDER BY 1`

const collectionsChargeSQL = `
WITH lines AS (
  SELECT x->>'charge_code' AS code, NULLIF(x->>'amount', '')::numeric AS amt, l.id AS line_id
    FROM billing_run_lines l JOIN billing_runs r ON r.id = l.run_id AND r.tenant_id = $1,
         jsonb_array_elements(l.lines) x
   WHERE l.tenant_id = $1 AND l.status = 'issued' AND r.period >= to_char($2::date, 'YYYY-MM') AND r.period < to_char($3::date, 'YYYY-MM')
     AND ($4::text IS NULL OR r.property_id = ANY($4::text::uuid[]))
), g AS (
  SELECT code, SUM(amt) AS amt, COUNT(DISTINCT line_id) AS bills FROM lines GROUP BY 1
)
SELECT COALESCE(ct.name, g.code), g.amt, ROUND(g.amt * 100 / NULLIF(SUM(g.amt) OVER (), 0), 1), g.bills
  FROM g LEFT JOIN charge_types ct ON ct.tenant_id = $1 AND ct.code = g.code
 ORDER BY g.amt DESC`

const paymentChannelsSQL = `
WITH total AS (
  SELECT COALESCE(SUM(d.collected), 0) AS amt, COALESCE(SUM(d.payments_count), 0) AS n
    FROM daily_stats d
   WHERE d.tenant_id = $1 AND d.day >= $2::date AND d.day < $3::date
     AND ($4::text IS NULL OR d.property_id = ANY($4::text::uuid[]))
), manual AS (
  SELECT CASE m.method WHEN 'bank_transfer' THEN 'Bank transfer' WHEN 'cheque' THEN 'Cheque' WHEN 'cash' THEN 'Cash'
                       ELSE 'M-Pesa code entered by staff' END AS channel,
         SUM(m.amount) AS amt, COUNT(*) AS n
    FROM manual_payments m
   WHERE m.tenant_id = $1 AND m.status = 'approved' AND m.reviewed_at >= $2::date AND m.reviewed_at < $3::date
     AND ($4::text IS NULL OR m.property_id = ANY($4::text::uuid[]))
   GROUP BY 1
), rows AS (
  SELECT 'M-Pesa (prompt and paybill)' AS channel,
         GREATEST(t.amt - COALESCE((SELECT SUM(amt) FROM manual), 0), 0) AS amt,
         GREATEST(t.n - COALESCE((SELECT SUM(n) FROM manual), 0), 0) AS n
    FROM total t
  UNION ALL SELECT channel, amt, n FROM manual
)
SELECT channel, amt, ROUND(amt * 100 / NULLIF(SUM(amt) OVER (), 0), 1), n FROM rows ORDER BY amt DESC`
