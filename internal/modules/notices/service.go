// Package notices sends notices and emergency alerts to an audience of residents over WhatsApp and
// email (the active channels) through notifications-api, with per-recipient delivery records
// (SRDD 16.5).
package notices

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/notice"
	"github.com/bengobox/maskani-api/internal/ent/noticedelivery"
	"github.com/bengobox/maskani-api/internal/ent/party"
	"github.com/bengobox/maskani-api/internal/ent/predicate"
	"github.com/bengobox/maskani-api/internal/ent/unit"
	"github.com/bengobox/maskani-api/internal/ent/unitparty"
	"github.com/bengobox/maskani-api/internal/events"
	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/notify"
	"github.com/bengobox/maskani-api/internal/platform/realtime"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
	"github.com/bengobox/maskani-api/internal/shared/page"
	"github.com/bengobox/maskani-api/internal/shared/richtext"
)

// Service is the notices service.
type Service struct {
	client *ent.Client
	notify *notify.Client
	loc    *time.Location
	log    *zap.Logger
	rt     realtime.Publisher
}

// SetRealtime sets the publisher for notice status hints (nil disables them).
func (s *Service) SetRealtime(p realtime.Publisher) { s.rt = p }

func (s *Service) emit(tenantID uuid.UUID, n *ent.Notice) {
	if n == nil {
		return
	}
	realtime.Emit(s.rt, tenantID, realtime.Event{Type: realtime.NoticeStatus, ID: n.ID.String(),
		PropertyID: realtime.IDString(n.PropertyID)})
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
	in.Body = richtext.Sanitize(in.Body)
	if in.Title == "" || in.Body == "" {
		return nil, httpx.Invalid("title and body are required")
	}
	in.Channels = activeChannels(in.Channels)
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

// Audience is who a notice reaches: the people holding one of Roles on active units, narrowed to a
// property, blocks or units. It is the one filter for direct sending, the count and the reach list
// notifications-api pages through.
type Audience struct {
	PropertyID *uuid.UUID
	BlockIDs   []uuid.UUID
	UnitIDs    []uuid.UUID
	Roles      []string
}

// AudienceOf reads a notice's stored audience.
func AudienceOf(n *ent.Notice) Audience {
	a := Audience{PropertyID: n.PropertyID, BlockIDs: uuids(n.Audience["block_ids"]), UnitIDs: uuids(n.Audience["unit_ids"])}
	if rs, ok := n.Audience["roles"].([]any); ok {
		for _, r := range rs {
			if v, ok := r.(string); ok && v != "" {
				a.Roles = append(a.Roles, v)
			}
		}
	}
	return a
}

// partyQuery is the active, reachable parties holding the audience's roles on its active units. The
// unit filter is a subquery, so it stays one statement however large the estate is.
func (s *Service) partyQuery(a Audience) *ent.PartyQuery {
	roles := []unitparty.Role{unitparty.RoleOwner, unitparty.RoleOccupant}
	if len(a.Roles) > 0 {
		roles = roles[:0]
		for _, r := range a.Roles {
			roles = append(roles, unitparty.Role(r))
		}
	}
	unitPreds := []predicate.Unit{unit.StatusEQ(unit.StatusActive)}
	if a.PropertyID != nil {
		unitPreds = append(unitPreds, unit.PropertyID(*a.PropertyID))
	}
	if len(a.BlockIDs) > 0 {
		unitPreds = append(unitPreds, unit.BlockIDIn(a.BlockIDs...))
	}
	if len(a.UnitIDs) > 0 {
		unitPreds = append(unitPreds, unit.IDIn(a.UnitIDs...))
	}
	return s.client.Party.Query().Where(party.Or(party.PhoneNEQ(""), party.EmailNEQ("")), party.StatusEQ(party.StatusActive),
		party.HasUnitLinksWith(unitparty.RoleIn(roles...), unitparty.StatusEQ(unitparty.StatusActive), unitparty.HasUnitWith(unitPreds...)))
}

// recipients resolves the audience to parties for the direct-send fallback (bounded).
func (s *Service) recipients(ctx context.Context, n *ent.Notice) ([]*ent.Party, error) {
	return s.partyQuery(AudienceOf(n)).Limit(20000).All(ctx)
}

// Resident is one reachable person, as the internal reach list returns them.
type Resident struct {
	Key       string `json:"key"`
	Name      string `json:"name"`
	FirstName string `json:"first_name"`
	Email     string `json:"email,omitempty"`
	Phone     string `json:"phone,omitempty"`
}

// Reach returns one page of the audience ordered by party id, for notifications-api's
// maskani_residents resolver (keyset paging: after is the last key of the previous page).
func (s *Service) Reach(ctx context.Context, a Audience, after string, limit int) ([]Resident, string, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	q := s.partyQuery(a)
	if after != "" {
		id, err := uuid.Parse(after)
		if err != nil {
			return nil, "", httpx.Invalid("after must be a resident key")
		}
		q = q.Where(party.IDGT(id))
	}
	rows, err := q.Order(ent.Asc(party.FieldID)).Limit(limit + 1).
		Select(party.FieldID, party.FieldDisplayName, party.FieldFirstName, party.FieldEmail, party.FieldPhone).All(ctx)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		next = rows[len(rows)-1].ID.String()
	}
	out := make([]Resident, 0, len(rows))
	for _, p := range rows {
		first := p.FirstName
		if first == "" {
			first = firstName(p.DisplayName)
		}
		r := Resident{Key: p.ID.String(), Name: p.DisplayName, FirstName: first, Email: p.Email}
		if p.Phone != "" {
			r.Phone = "+" + strings.TrimPrefix(p.Phone, "+")
		}
		out = append(out, r)
	}
	return out, next, nil
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
	tenantID, _ := tenantguard.TenantID(ctx)
	if n.Priority == notice.PriorityRoutine && quiet(time.Now().In(s.loc)) {
		next := nextMorning(time.Now().In(s.loc))
		n, err = n.Update().SetStatus(notice.StatusScheduled).SetScheduledAt(next).Save(ctx)
		if err == nil {
			s.emit(tenantID, n)
		}
		return n, err
	}
	// Preferred path: notifications-api owns delivery (templates, rate limits, suppression,
	// per-recipient tracking) and pages the residents from this service as it sends. The direct
	// path below stays as the fallback when notifications-api cannot take the broadcast.
	if sent, err := s.sendAsBroadcast(ctx, tenantID, tenantSlug, n); err == nil {
		return sent, nil
	} else {
		s.log.Warn("notice broadcast hand-over failed; sending directly", zap.String("notice", n.ID.String()), zap.Error(err))
	}
	people, err := s.recipients(ctx, n)
	if err != nil {
		return nil, err
	}
	n, err = n.Update().SetStatus(notice.StatusSending).SetRecipientsCount(len(people)).Save(ctx)
	if err != nil {
		return nil, err
	}
	s.emit(tenantID, n)
	go s.deliver(tenantguard.With(context.Background(), tenantID), tenantID, tenantSlug, n, people)
	return n, nil
}

var linkPattern = regexp.MustCompile(`\s*\(?\bhttps?://\S+\)?`)

// sendAsBroadcast hands the notice to notifications-api as an approved service notice whose
// audience is resolved from this service (maskani_residents). Delivery counts arrive later on
// notifications.broadcast.completed (Completed).
func (s *Service) sendAsBroadcast(ctx context.Context, tenantID uuid.UUID, slug string, n *ent.Notice) (*ent.Notice, error) {
	a := AudienceOf(n)
	count, err := s.partyQuery(a).Count(ctx)
	if err != nil {
		return nil, err
	}
	estate := s.estateName(ctx, n, slug)
	subject := n.Title
	if n.Priority == notice.PriorityEmergency {
		subject = "Urgent: " + n.Title
	}
	text := richtext.PlainText(n.Body)
	audience := map[string]any{"type": "maskani_residents"}
	if a.PropertyID != nil {
		audience["property_id"] = a.PropertyID.String()
	}
	if len(a.BlockIDs) > 0 {
		audience["block_ids"] = uuidStrings(a.BlockIDs)
	}
	if len(a.UnitIDs) > 0 {
		audience["unit_ids"] = uuidStrings(a.UnitIDs)
	}
	if len(a.Roles) > 0 {
		audience["roles"] = a.Roles
	}
	b := notify.Broadcast{Title: subject, Kind: "service_notice", Channels: activeChannels(n.Channels), Audience: audience}
	for _, ch := range b.Channels {
		switch ch {
		case "email":
			b.Content.Email = &notify.BroadcastEmail{Subject: subject + " (" + estate + ")", Body: text}
		case "whatsapp":
			// The platform service notice template: {{1}} first name, {{2}} estate, {{3}} the notice
			// on one line. Meta rejects links in a parameter, so they are dropped from this copy.
			b.Content.WhatsApp = &notify.BroadcastWhatsApp{Template: "broadcast_service_notice_v1",
				Params: []string{"first_name", "sender_name", "message"}, Message: oneLine(n.Title + ": " + linkPattern.ReplaceAllString(text, ""))}
		}
	}
	res, err := s.notify.CreateBroadcast(ctx, tenantID, "maskani", n.ID.String(), true, b)
	if err != nil {
		return nil, err
	}
	meta := n.Metadata
	if meta == nil {
		meta = map[string]any{}
	}
	meta["broadcast_id"], meta["delivery"] = res.ID, "notifications_broadcast"
	n, err = n.Update().SetStatus(notice.StatusSending).SetRecipientsCount(count).SetMetadata(meta).Save(ctx)
	if err != nil {
		return nil, err
	}
	s.emit(tenantID, n)
	return n, nil
}

// Completed records a finished notifications-api broadcast on its notice (consumer of
// notifications.broadcast.completed with source "maskani"). Idempotent.
func (s *Service) Completed(ctx context.Context, tenantID, noticeID uuid.UUID, sent, target int) error {
	n, err := s.client.Notice.Get(ctx, noticeID)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil
		}
		return err
	}
	status := notice.StatusSent
	if sent == 0 && target > 0 {
		status = notice.StatusFailed
	}
	u := n.Update().SetStatus(status).SetDeliveredCount(sent)
	if n.SentAt == nil {
		u.SetSentAt(time.Now())
	}
	if target > 0 {
		u.SetRecipientsCount(target)
	}
	if n, err = u.Save(ctx); err != nil {
		return err
	}
	s.emit(tenantID, n)
	return events.Publish(ctx, s.client.OutboxEvent, tenantID, n.ID.String(), events.NoticePublished, map[string]any{
		"notice_id": n.ID, "title": n.Title, "audience_size": target, "priority": n.Priority, "delivered": sent,
	})
}

func uuidStrings(ids []uuid.UUID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}
	return out
}

func (s *Service) deliver(ctx context.Context, tenantID uuid.UUID, slug string, n *ent.Notice, people []*ent.Party) {
	delivered := 0
	estate := s.estateName(ctx, n, slug)
	subject := n.Title
	if n.Priority == notice.PriorityEmergency {
		subject = "Urgent: " + n.Title
	}
	for _, p := range people {
		for _, ch := range activeChannels(n.Channels) {
			dest := p.Phone
			meta := map[string]any{
				// Business-initiated WhatsApp needs an approved template; the platform's service
				// notice template carries the notice as one line (Meta rejects newlines in a
				// parameter).
				"template_name":     "broadcast_service_notice_v1",
				"template_language": "en_US",
				"template_params":   []string{firstName(p.DisplayName), estate, oneLine(n.Title + ": " + n.Body)},
				"default_dial_code": "254",
			}
			if ch == "email" {
				dest = p.Email
				meta = map[string]any{"subject": subject}
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
				Channel: ch, Template: "maskani/notice", To: []string{dest}, Metadata: meta,
				Data: map[string]any{"title": n.Title, "body": n.Body, "name": firstName(p.DisplayName),
					"priority": string(n.Priority), "estate": estate},
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
	if err := s.client.Notice.UpdateOneID(n.ID).SetStatus(status).SetSentAt(time.Now()).SetDeliveredCount(delivered).Exec(ctx); err == nil {
		s.emit(tenantID, n)
	}
	_ = events.Publish(ctx, s.client.OutboxEvent, tenantID, n.ID.String(), events.NoticePublished, map[string]any{
		"notice_id": n.ID, "title": n.Title, "audience_size": len(people), "priority": n.Priority, "delivered": delivered,
	})
}

// List returns a keyset page of notices, newest first. With a property, tenant-wide notices are
// included; without one, a property-limited caller sees tenant-wide notices and their properties.
func (s *Service) List(ctx context.Context, propertyID *uuid.UUID, scope []uuid.UUID, all bool, status string, p page.Params) (page.Result[*ent.Notice], error) {
	q := s.client.Notice.Query()
	if propertyID != nil {
		q = q.Where(notice.Or(notice.PropertyID(*propertyID), notice.PropertyIDIsNil()))
	} else if !all {
		q = q.Where(notice.Or(notice.PropertyIDIn(scope...), notice.PropertyIDIsNil()))
	}
	if status != "" {
		q = q.Where(notice.StatusEQ(notice.Status(status)))
	}
	rows, err := q.Where(p.Predicate()).Modify(page.Order()).Limit(p.Limit + 1).All(ctx)
	if err != nil {
		return page.Result[*ent.Notice]{}, err
	}
	return page.Build(rows, p.Limit, func(n *ent.Notice) (uuid.UUID, time.Time) { return n.ID, n.CreatedAt }), nil
}

// Deliveries returns a notice's delivery records.
func (s *Service) Deliveries(ctx context.Context, noticeID uuid.UUID) ([]*ent.NoticeDelivery, error) {
	return s.client.NoticeDelivery.Query().Where(noticedelivery.NoticeID(noticeID)).Limit(20000).All(ctx)
}

// DueScheduled returns scheduled notices whose time has come (system job).
func (s *Service) DueScheduled(ctx context.Context) ([]*ent.Notice, error) {
	return s.client.Notice.Query().Where(notice.StatusEQ(notice.StatusScheduled), notice.ScheduledAtLTE(time.Now())).Limit(100).All(ctx)
}

// activeChannels keeps the channels notifications can deliver (WhatsApp and email), defaulting to
// both. Notices saved earlier with "sms" fall back to WhatsApp.
func activeChannels(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, 2)
	for _, ch := range in {
		ch = strings.ToLower(strings.TrimSpace(ch))
		if ch == "sms" {
			ch = "whatsapp"
		}
		if (ch == "whatsapp" || ch == "email") && !seen[ch] {
			seen[ch] = true
			out = append(out, ch)
		}
	}
	if len(out) == 0 {
		return []string{"whatsapp", "email"}
	}
	return out
}

// estateName names the sender: the notice's property, else the tenant slug.
func (s *Service) estateName(ctx context.Context, n *ent.Notice, slug string) string {
	if n.PropertyID != nil {
		if p, err := s.client.Property.Get(ctx, *n.PropertyID); err == nil && p.Name != "" {
			return p.Name
		}
	}
	return slug
}

func firstName(display string) string {
	if f := strings.Fields(display); len(f) > 0 {
		return f[0]
	}
	return "there"
}

// maxNoticeParam keeps the WhatsApp notice parameter inside Meta's 1,024 character body.
const maxNoticeParam = 900

// oneLine flattens text for a WhatsApp template parameter: no newlines or tabs, no runs of spaces.
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > maxNoticeParam {
		s = string(r[:maxNoticeParam-3]) + "..."
	}
	return s
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
