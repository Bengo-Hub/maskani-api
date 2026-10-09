package jobs

import (
	"context"
	"time"

	eventslib "github.com/Bengo-Hub/shared-events"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/outboxevent"
	"github.com/bengobox/maskani-api/internal/events"
)

// Outbox retention. The shared poller only marks rows published; nothing else removes them, so
// without this the table grows with every event and keeps plain gate codes (pass.created carries
// the code notifications sends to the visitor) for ever.
const (
	pruneBatch         = 1000
	pruneMaxBatches    = 50
	passEventRetention = 15 * time.Minute
	publishedRetention = 7 * 24 * time.Hour
	failedRetention    = 30 * 24 * time.Hour
)

// pruneRule removes rows of one status older than a cut-off, optionally only some event types.
type pruneRule struct {
	status string
	before time.Time
	types  []string
}

// PruneOutbox deletes published and long-failed outbox rows in batches of 1,000, oldest first, using
// the (status, created_at) index. passesOnly runs just the short gate-code rule. It returns how many
// rows went.
func PruneOutbox(ctx context.Context, client *ent.Client, passesOnly bool) (int, error) {
	now := time.Now()
	rules := []pruneRule{{eventslib.StatusPublished, now.Add(-passEventRetention), []string{events.PassCreated}}}
	if !passesOnly {
		rules = append(rules,
			pruneRule{eventslib.StatusPublished, now.Add(-publishedRetention), nil},
			pruneRule{eventslib.StatusFailed, now.Add(-failedRetention), nil})
	}
	total := 0
	for _, rule := range rules {
		for i := 0; i < pruneMaxBatches; i++ {
			q := client.OutboxEvent.Query().
				Where(outboxevent.Status(rule.status), outboxevent.CreatedAtLT(rule.before))
			if len(rule.types) > 0 {
				q = q.Where(outboxevent.EventTypeIn(rule.types...))
			}
			ids, err := q.Order(ent.Asc(outboxevent.FieldCreatedAt)).Limit(pruneBatch).IDs(ctx)
			if err != nil {
				return total, err
			}
			if len(ids) == 0 {
				break
			}
			n, err := client.OutboxEvent.Delete().Where(outboxevent.IDIn(ids...)).Exec(ctx)
			if err != nil {
				return total, err
			}
			total += n
			if len(ids) < pruneBatch {
				break
			}
		}
	}
	return total, nil
}
