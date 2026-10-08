package notices

import (
	"context"
	"time"

	eventslib "github.com/Bengo-Hub/shared-events"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"go.uber.org/zap"

	"github.com/bengobox/maskani-api/internal/ent/consumedevent"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

const (
	completedSubject  = "notifications.broadcast.completed"
	completedDurable  = "maskani-broadcast-completed"
	consumerCompleted = "notices.broadcast_completed"
)

// StartCompletedConsumer records finished notice broadcasts: notifications-api publishes
// notifications.broadcast.completed with source "maskani" and source_ref the notice id. Durable
// deliver-group consumer, idempotent on the event id (ConsumedEvent).
func (s *Service) StartCompletedConsumer(js nats.JetStreamContext) {
	eventslib.SubscribeQueueWithRebind(s.log, js, "notifications", completedSubject, completedDurable, s.handleCompleted,
		nats.Durable(completedDurable), nats.AckExplicit(), nats.AckWait(30*time.Second), nats.MaxDeliver(5), nats.DeliverNew())
}

func (s *Service) handleCompleted(msg *nats.Msg) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	evt, err := eventslib.FromJSON(msg.Data)
	if err != nil {
		_ = msg.Ack()
		return
	}
	p := evt.Payload
	if src, _ := p["source"].(string); src != "maskani" {
		_ = msg.Ack()
		return
	}
	ref, _ := p["source_ref"].(string)
	noticeID, err := uuid.Parse(ref)
	if err != nil || evt.TenantID == uuid.Nil {
		_ = msg.Ack()
		return
	}
	sys := tenantguard.System(ctx)
	if seen, _ := s.client.ConsumedEvent.Query().
		Where(consumedevent.EventID(evt.ID), consumedevent.Consumer(consumerCompleted)).Exist(sys); seen {
		_ = msg.Ack()
		return
	}
	if err := s.Completed(tenantguard.With(ctx, evt.TenantID), evt.TenantID, noticeID, intOf(p["sent"]), intOf(p["target"])); err != nil {
		s.log.Warn("notice broadcast completion failed; will redeliver", zap.Error(err))
		_ = msg.Nak()
		return
	}
	_ = s.client.ConsumedEvent.Create().SetEventID(evt.ID).SetConsumer(consumerCompleted).SetTenantID(evt.TenantID).
		SetSubject(msg.Subject).OnConflictColumns(consumedevent.FieldEventID, consumedevent.FieldConsumer).DoNothing().Exec(sys)
	_ = msg.Ack()
}

// intOf reads a JSON number (decoded as float64) or int.
func intOf(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}
