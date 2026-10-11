// Package audit is maskani's central, append-only trail of who changed what (SRDD 4.8 and wave
// 2.4), the same shape as inventory-api's and pos-api's internal/audit: Record is best effort, so a
// failed audit write is logged and never breaks the change it describes.
//
// Every successful staff write is recorded by the Audit middleware (method, route, target, actor,
// IP, request id). Places where the old and new values matter (roles, settings, money decisions)
// also call Record with before and after.
package audit

import (
	"context"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/auditlog"
	"github.com/bengobox/maskani-api/internal/shared/page"
)

// Entry is one audit record.
type Entry struct {
	TenantID   uuid.UUID
	ActorID    uuid.UUID // auth-api user; uuid.Nil for system jobs
	ActorEmail string
	Action     string // e.g. "POST /manual-payments/{id}/approve" or "settings.update"
	TargetType string // e.g. "manual-payments"
	TargetID   uuid.UUID
	Before     map[string]any
	After      map[string]any
	IP         string
	RequestID  string
}

// Service writes and reads the trail.
type Service struct {
	client *ent.Client
	log    *zap.Logger
}

// NewService creates the audit service.
func NewService(client *ent.Client, log *zap.Logger) *Service {
	return &Service{client: client, log: log.Named("audit")}
}

// Record writes one entry; failures are logged, never returned.
func (s *Service) Record(ctx context.Context, e Entry) {
	if s == nil || s.client == nil || e.TenantID == uuid.Nil || e.Action == "" {
		return
	}
	if e.TargetType == "" {
		e.TargetType = "tenant"
	}
	c := s.client.AuditLog.Create().SetTenantID(e.TenantID).SetActorUserID(e.ActorID).SetAction(truncate(e.Action, 200)).
		SetTargetType(truncate(e.TargetType, 60)).SetTargetID(e.TargetID)
	if e.ActorEmail != "" {
		c.SetActorEmail(e.ActorEmail)
	}
	if e.Before != nil {
		c.SetBefore(e.Before)
	}
	if e.After != nil {
		c.SetAfter(e.After)
	}
	if e.IP != "" {
		c.SetIP(e.IP)
	}
	if e.RequestID != "" {
		c.SetRequestID(e.RequestID)
	}
	if err := c.Exec(ctx); err != nil {
		s.log.Warn("audit entry not written", zap.String("action", e.Action), zap.Error(err))
	}
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// Filter narrows the trail.
type Filter struct {
	TargetType string
	TargetID   *uuid.UUID
	ActorID    *uuid.UUID
	From, To   *time.Time
}

// List returns a keyset page of the tenant's trail, newest first.
func (s *Service) List(ctx context.Context, tenantID uuid.UUID, f Filter, p page.Params) (page.Result[*ent.AuditLog], error) {
	q := s.client.AuditLog.Query().Where(auditlog.TenantID(tenantID))
	if f.TargetType != "" {
		q = q.Where(auditlog.TargetType(f.TargetType))
	}
	if f.TargetID != nil {
		q = q.Where(auditlog.TargetID(*f.TargetID))
	}
	if f.ActorID != nil {
		q = q.Where(auditlog.ActorUserID(*f.ActorID))
	}
	if f.From != nil {
		q = q.Where(auditlog.CreatedAtGTE(*f.From))
	}
	if f.To != nil {
		q = q.Where(auditlog.CreatedAtLT(*f.To))
	}
	rows, err := q.Where(p.Predicate()).Modify(page.Order()).Limit(p.Limit + 1).All(ctx)
	if err != nil {
		return page.Result[*ent.AuditLog]{}, err
	}
	return page.Build(rows, p.Limit, func(a *ent.AuditLog) (uuid.UUID, time.Time) { return a.ID, a.CreatedAt }), nil
}
