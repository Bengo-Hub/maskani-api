package privacy

import (
	"context"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/ent/auditlog"
	"github.com/bengobox/maskani-api/internal/ent/documentaccesslog"
	"github.com/bengobox/maskani-api/internal/ent/gateevent"
	"github.com/bengobox/maskani-api/internal/ent/patrolscan"
	"github.com/bengobox/maskani-api/internal/ent/visitor"
	"github.com/bengobox/maskani-api/internal/ent/visitorpass"
)

// The retention schedule: how long personal data is kept once it has done its job. Records the law
// requires (bills, payments, contracts, account history) are not on it and are never purged here.
// Outbox events and import uploads have their own, shorter clean-ups (jobs/outbox.go, imports).
const (
	GateRetention       = 90 * 24 * time.Hour      // entries, exits and patrol scans (SRDD 15.2)
	PassRetention       = 90 * 24 * time.Hour      // after a visitor pass ends: visitor name, phone, plate
	VisitorRetention    = 365 * 24 * time.Hour     // a returning visitor not seen for a year (never a banned one)
	AccessLogRetention  = 2 * 365 * 24 * time.Hour // who opened a document and from where
	AuditRetention      = 2 * 365 * 24 * time.Hour // the security audit trail
	retentionBatch      = 1000
	retentionMaxBatches = 20
)

// Purged counts what one retention run removed, by kind.
type Purged map[string]int

// PurgeExpired applies the schedule across every tenant in batches of 1,000 (system job). Each kind
// reads its ids by an age index and deletes them; a kind stops after 20 batches and picks up the
// rest on the next run, so one run never holds the database for long.
func (s *Service) PurgeExpired(ctx context.Context) (Purged, error) {
	now := time.Now()
	out := Purged{}
	steps := []struct {
		kind string
		ids  func() ([]uuid.UUID, error)
		del  func(ids []uuid.UUID) (int, error)
	}{
		{"gate_events",
			func() ([]uuid.UUID, error) {
				return s.client.GateEvent.Query().Where(gateevent.OccurredAtLT(now.Add(-GateRetention))).Limit(retentionBatch).IDs(ctx)
			},
			func(ids []uuid.UUID) (int, error) {
				return s.client.GateEvent.Delete().Where(gateevent.IDIn(ids...)).Exec(ctx)
			}},
		{"patrol_scans",
			func() ([]uuid.UUID, error) {
				return s.client.PatrolScan.Query().Where(patrolscan.ScannedAtLT(now.Add(-GateRetention))).Limit(retentionBatch).IDs(ctx)
			},
			func(ids []uuid.UUID) (int, error) {
				return s.client.PatrolScan.Delete().Where(patrolscan.IDIn(ids...)).Exec(ctx)
			}},
		{"visitor_passes",
			func() ([]uuid.UUID, error) {
				return s.client.VisitorPass.Query().Where(visitorpass.ValidToLT(now.Add(-PassRetention))).Limit(retentionBatch).IDs(ctx)
			},
			func(ids []uuid.UUID) (int, error) {
				return s.client.VisitorPass.Delete().Where(visitorpass.IDIn(ids...)).Exec(ctx)
			}},
		{"visitors",
			func() ([]uuid.UUID, error) {
				// Never one a kept pass still points at (a new pass for someone not seen in a year).
				passCut := now.Add(-PassRetention)
				noKeptPass := func(sel *sql.Selector) {
					t := sql.Table(visitorpass.Table)
					sel.Where(sql.Not(sql.Exists(sql.Select(t.C(visitorpass.FieldID)).From(t).Where(sql.And(
						sql.ColumnsEQ(t.C(visitorpass.FieldVisitorID), sel.C(visitor.FieldID)),
						sql.GTE(t.C(visitorpass.FieldValidTo), passCut))))))
				}
				return s.client.Visitor.Query().Where(visitor.LastVisitAtLT(now.Add(-VisitorRetention)), visitor.StatusNEQ(visitor.StatusBanned), noKeptPass).
					Limit(retentionBatch).IDs(ctx)
			},
			func(ids []uuid.UUID) (int, error) {
				return s.client.Visitor.Delete().Where(visitor.IDIn(ids...)).Exec(ctx)
			}},
		{"document_access_logs",
			func() ([]uuid.UUID, error) {
				return s.client.DocumentAccessLog.Query().Where(documentaccesslog.CreatedAtLT(now.Add(-AccessLogRetention))).Limit(retentionBatch).IDs(ctx)
			},
			func(ids []uuid.UUID) (int, error) {
				return s.client.DocumentAccessLog.Delete().Where(documentaccesslog.IDIn(ids...)).Exec(ctx)
			}},
		{"audit_logs",
			func() ([]uuid.UUID, error) {
				return s.client.AuditLog.Query().Where(auditlog.CreatedAtLT(now.Add(-AuditRetention))).Limit(retentionBatch).IDs(ctx)
			},
			func(ids []uuid.UUID) (int, error) {
				return s.client.AuditLog.Delete().Where(auditlog.IDIn(ids...)).Exec(ctx)
			}},
	}
	for _, st := range steps {
		for i := 0; i < retentionMaxBatches; i++ {
			ids, err := st.ids()
			if err != nil {
				return out, err
			}
			if len(ids) == 0 {
				break
			}
			n, err := st.del(ids)
			out[st.kind] += n
			if err != nil {
				return out, err
			}
		}
	}
	return out, nil
}
