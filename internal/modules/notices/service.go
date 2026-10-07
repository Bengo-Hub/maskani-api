// Package notices sends notices and emergency alerts to an audience of residents over SMS,
// WhatsApp and email through notifications-api, with per-recipient delivery records (SRDD 16.5).
package notices

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/notice"
	"github.com/bengobox/maskani-api/internal/ent/noticedelivery"
	"github.com/bengobox/maskani-api/internal/ent/party"
	"github.com/bengobox/maskani-api/internal/ent/unit"
	"github.com/bengobox/maskani-api/internal/ent/unitparty"
	"github.com/bengobox/maskani-api/internal/events"
	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/notify"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

// Service is the notices service.
type Service struct {
	client *ent.Client
	notify *notify.Client
	loc    *time.Location
	log    *zap.Logger
}

// NewService creates the notices service.
func NewService(client *ent.Client, n *notify.Client, loc *time.Location, log *zap.Logger) *Service {
	return &Service{client: client, notify: n, loc: loc, log: log.Named("notices")}
}

// Input composes a notice.
type Input struct {
	PropertyID  *uuid.UUID     `json:"property_id"`
	Audience    map[string]any `json:"audience"`
	Channels    []string       `json:"channels"`
	Category    string         `json:"category"`
	Priority    string         `json:"priority"`
	Title       string         `json:"title"`
	Body        string         `json:"body"`
	ScheduledAt *time.Time     `json:"scheduled_at"`
	SendNow     bool           `json:"send_now"`
}

// Create stores a notice; with send_now it is sent immediately (emergency) or at the end of quiet
// hours (routine).
func (s *Service) Create(ctx context.Context, actor uuid.UUID, tenantSlug string, in Input) (*ent.Notice, error) {
	if in.Title == "" || in.Body == "" {
		return nil, httpx.Invalid("title and body are required")
	}
	if len(in.Channels) == 0 {
		in.Channels = []string{"sms"}
	}
	pr := notice.PriorityRoutine
	if in.Priority == "emergency" {
		pr = notice.PriorityEmergency
	}
	if in.Audience == nil {
		in.Audience = map[string]any{"scope": "estate", "roles": []string{"owner", "occupant"}}
	}
	c := s.client.Notice.Create().SetAudience(in.Audience).SetChannels(in.Channels).SetPriority(pr).
		SetTitle(in.Title).SetBody(in.Body).SetCreatedBy(actor)
	if in.Category != "" {
		c.SetCategory(in.Category)
	}
	if in.PropertyID != nil {
		c.SetPropertyID(*in.PropertyID)
	}
	if in.ScheduledAt != nil {
		c.SetScheduledAt(*in.ScheduledAt).SetStatus(notice.StatusScheduled)
	}
	n, err := c.Save(ctx)
	if err != nil {
		return nil, err
	}
	if in.SendNow {
		return s.Send(ctx, n.ID, tenantSlug)
	}
	return n, nil
}

// recipients resolves the audience to parties with phones, deduplicated.
func (s *Service) recipients(ctx context.Context, n *ent.Notice) ([]*ent.Party, error) {
	roles := []unitparty.Role{unitparty.RoleOwner, unitparty.RoleOccupant}
	if rs, ok := n.Audience["roles"].([]any); ok && len(rs) > 0 {
		roles = nil
		for _, r := range rs {
			if v, ok := r.(string); ok {
				roles = append(roles, unitparty.Role(v))
			}
		}
	}
	uq := s.client.Unit.Query().Where(unit.StatusEQ(unit.StatusActive))
	if n.PropertyID != nil {
		uq = uq.Where(unit.PropertyID(*n.PropertyID))
	}
	if ids := uuids(n.Audience["block_ids"]); len(ids) > 0 {
		uq = uq.Where(unit.BlockIDIn(ids...))
	}
	if ids := uuids(n.Audience["unit_ids"]); len(ids) > 0 {
		uq = uq.Where(unit.IDIn(ids...))
	}
	unitIDs, err := uq.IDs(ctx)
	if err != nil || len(unitIDs) == 0 {
		return nil, err
	}
	return s.client.Party.Query().Where(party.PhoneNEQ(""), party.StatusEQ(party.StatusActive),
		party.HasUnitLinksWith(unitparty.UnitIDIn(unitIDs...), unitparty.RoleIn(roles...), unitparty.StatusEQ(unitparty.StatusActive))).
		Limit(20000).All(ctx)
}

// Send delivers the notice to every recipient and channel, recording each delivery.
func (s *Service) Send(ctx context.Context, noticeID uuid.UUID, tenantSlug string) (*ent.Notice, error) {
	n, err := s.client.Notice.Get(ctx, noticeID)
	if err != nil {
		return nil, err
	}
	if n.Status == notice.StatusSent || n.Status == notice.StatusSending {
		return n, nil
	}
	if n.Priority == notice.PriorityRoutine && quiet(time.Now().In(s.loc)) {
		next := nextMorning(time.Now().In(s.loc))
		return n.Update().SetStatus(notice.StatusScheduled).SetScheduledAt(next).Save(ctx)
	}
	people, err := s.recipients(ctx, n)
	if err != nil {
		return nil, err
	}
	n, err = n.Update().SetStatus(notice.StatusSending).SetRecipientsCount(len(people)).Save(ctx)
	if err != nil {
		return nil, err
	}
	tenantID, _ := tenantguard.TenantID(ctx)
	go s.deliver(tenantguard.With(context.Background(), tenantID), tenantID, tenantSlug, n, people)
	return n, nil
}

func (s *Service) deliver(ctx context.Context, tenantID uuid.UUID, slug string, n *ent.Notice, people []*ent.Party) {
	delivered := 0
	for _, p := range people {
		for _, ch := range n.Channels {
			dest := p.Phone
			if ch == "email" {
				dest = p.Email
			}
			if dest == "" {
				continue
			}
			d, err := s.client.NoticeDelivery.Create().SetNoticeID(n.ID).SetPartyID(p.ID).SetChannel(ch).SetDestination(dest).Save(ctx)
			if ent.IsConstraintError(err) {
				continue
			}
			if err != nil {
				continue
			}
			res, err := s.notify.Send(ctx, tenantID, slug, fmt.Sprintf("MSK-NOTICE-%s-%s-%s", n.ID, p.ID, ch), notify.Message{
				Channel: ch, Template: "maskani_notice", To: []string{dest},
				Data: map[string]any{"title": n.Title, "body": n.Body, "name": p.DisplayName, "priority": string(n.Priority)},
			})
			u := d.Update().SetSentAt(time.Now())
			if err != nil {
				u.SetStatus(noticedelivery.StatusFailed).SetError(err.Error())
			} else {
				u.SetStatus(noticedelivery.StatusSent).SetNotificationMessageID(res.ID)
				delivered++
			}
			_ = u.Exec(ctx)
		}
	}
	status := notice.StatusSent
	if delivered == 0 && len(people) > 0 {
		status = notice.StatusFailed
	}
	_ = s.client.Notice.UpdateOneID(n.ID).SetStatus(status).SetSentAt(time.Now()).SetDeliveredCount(delivered).Exec(ctx)
	_ = events.Publish(ctx, s.client.OutboxEvent, tenantID, n.ID.String(), events.NoticePublished, map[string]any{
		"notice_id": n.ID, "title": n.Title, "audience_size": len(people), "priority": n.Priority, "delivered": delivered,
	})
}

// List returns recent notices.
func (s *Service) List(ctx context.Context, propertyID *uuid.UUID, limit int) ([]*ent.Notice, error) {
	q := s.client.Notice.Query()
	if propertyID != nil {
		q = q.Where(notice.Or(notice.PropertyID(*propertyID), notice.PropertyIDIsNil()))
	}
	return q.Order(ent.Desc(notice.FieldCreatedAt)).Limit(min(max(limit, 1), 200)).All(ctx)
}

// Deliveries returns a notice's delivery records.
func (s *Service) Deliveries(ctx context.Context, noticeID uuid.UUID) ([]*ent.NoticeDelivery, error) {
	return s.client.NoticeDelivery.Query().Where(noticedelivery.NoticeID(noticeID)).Limit(20000).All(ctx)
}

// DueScheduled returns scheduled notices whose time has come (system job).
func (s *Service) DueScheduled(ctx context.Context) ([]*ent.Notice, error) {
	return s.client.Notice.Query().Where(notice.StatusEQ(notice.StatusScheduled), notice.ScheduledAtLTE(time.Now())).Limit(100).All(ctx)
}

func quiet(t time.Time) bool { h := t.Hour(); return h >= 21 || h < 7 }

func nextMorning(t time.Time) time.Time {
	m := time.Date(t.Year(), t.Month(), t.Day(), 7, 0, 0, 0, t.Location())
	if !t.Before(m) {
		m = m.AddDate(0, 0, 1)
	}
	return m
}

func uuids(v any) []uuid.UUID {
	list, _ := v.([]any)
	out := []uuid.UUID{}
	for _, x := range list {
		if s, ok := x.(string); ok {
			if id, err := uuid.Parse(s); err == nil {
				out = append(out, id)
			}
		}
	}
	return out
}
