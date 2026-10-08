package collections

import (
	"context"
	"time"

	eventslib "github.com/Bengo-Hub/shared-events"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/bengobox/maskani-api/internal/modules/accounts"
	"github.com/bengobox/maskani-api/internal/modules/reports"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/billingrunline"
	"github.com/bengobox/maskani-api/internal/ent/consumedevent"
	"github.com/bengobox/maskani-api/internal/ent/instalment"
	"github.com/bengobox/maskani-api/internal/ent/unitaccount"
	"github.com/bengobox/maskani-api/internal/events"
	"github.com/bengobox/maskani-api/internal/platform/realtime"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

const (
	paymentSubject  = "treasury.payment.succeeded"
	paymentDurable  = "maskani-payment-succeeded"
	consumerPayment = "collections.payment"
)

// SalesHook updates instalment and contract progress after a sales-fund payment.
type SalesHook func(ctx context.Context, accountID uuid.UUID) error

// Consumer reflects treasury payments on unit accounts (display balance, purchase progress) and
// publishes maskani.payment.applied for the receipt message. Idempotent on the event id.
type Consumer struct {
	client *ent.Client
	svc    *Service
	sales  SalesHook
	loc    *time.Location
	log    *zap.Logger
	// OnApplied runs after a payment is reflected (cache invalidation).
	OnApplied func(tenantID uuid.UUID)
	// RT receives a payment.applied hint for live screens (nil disables it).
	RT realtime.Publisher
}

// NewConsumer creates the payment consumer.
func NewConsumer(client *ent.Client, svc *Service, sales SalesHook, loc *time.Location, log *zap.Logger) *Consumer {
	return &Consumer{client: client, svc: svc, sales: sales, loc: loc, log: log.Named("payment-consumer")}
}

// Start subscribes with a durable deliver-group consumer.
func (c *Consumer) Start(js nats.JetStreamContext) {
	eventslib.SubscribeQueueWithRebind(c.log, js, "treasury", paymentSubject, paymentDurable, c.handle,
		nats.Durable(paymentDurable), nats.AckExplicit(), nats.AckWait(30*time.Second), nats.MaxDeliver(5), nats.DeliverNew())
}

func (c *Consumer) handle(msg *nats.Msg) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	evt, err := eventslib.FromJSON(msg.Data)
	if err != nil {
		_ = msg.Ack()
		return
	}
	p := evt.Payload
	refType, _ := p["reference_type"].(string)
	source, _ := p["source_service"].(string)
	if refType != "account_payment" && refType != "invoice" {
		_ = msg.Ack()
		return
	}
	if refType == "account_payment" && source != "" && source != "maskani" {
		_ = msg.Ack()
		return
	}
	tenantID := evt.TenantID
	if tenantID == uuid.Nil {
		if s, _ := p["tenant_id"].(string); s != "" {
			tenantID, _ = uuid.Parse(s)
		}
	}
	if tenantID == uuid.Nil {
		_ = msg.Ack()
		return
	}
	sys := tenantguard.System(ctx)
	if seen, _ := c.client.ConsumedEvent.Query().
		Where(consumedevent.EventID(evt.ID), consumedevent.Consumer(consumerPayment)).Exist(sys); seen {
		_ = msg.Ack()
		return
	}
	tctx := tenantguard.With(ctx, tenantID)
	accountID := c.resolveAccount(tctx, refType, p)
	if accountID == uuid.Nil {
		_ = msg.Ack()
		return
	}
	acc, err := c.apply(tctx, tenantID, accountID, p)
	if err != nil {
		c.log.Warn("payment apply failed; will redeliver", zap.Error(err))
		_ = msg.Nak()
		return
	}
	_ = c.client.ConsumedEvent.Create().SetEventID(evt.ID).SetConsumer(consumerPayment).SetTenantID(tenantID).
		SetSubject(msg.Subject).OnConflictColumns(consumedevent.FieldEventID, consumedevent.FieldConsumer).DoNothing().Exec(sys)
	if c.OnApplied != nil {
		c.OnApplied(tenantID)
	}
	ev := realtime.Event{Type: realtime.PaymentApplied, ID: acc.ID.String(), UnitID: acc.UnitID.String()}
	if acc.Edges.Unit != nil {
		ev.PropertyID = acc.Edges.Unit.PropertyID.String()
	}
	realtime.Emit(c.RT, tenantID, ev)
	_ = msg.Ack()
}

// resolveAccount finds the unit account a payment belongs to.
func (c *Consumer) resolveAccount(ctx context.Context, refType string, p map[string]any) uuid.UUID {
	meta, _ := p["metadata"].(map[string]any)
	if refType == "account_payment" {
		// reference_id is per payment (C2B-<TransID>, MSK-PAY-...); the account is in metadata.
		for _, cand := range []string{str(meta["unit_account_id"]), str(p["entity_id"]), str(p["reference_id"])} {
			if id, err := uuid.Parse(cand); err == nil {
				if ok, _ := c.client.UnitAccount.Query().Where(unitaccount.ID(id)).Exist(ctx); ok {
					return id
				}
			}
		}
		return uuid.Nil
	}
	invID, err := uuid.Parse(str(meta["invoice_id"]))
	if err != nil {
		return uuid.Nil
	}
	if l, err := c.client.BillingRunLine.Query().Where(billingrunline.TreasuryInvoiceID(invID)).First(ctx); err == nil {
		return l.UnitAccountID
	}
	if in, err := c.client.Instalment.Query().Where(instalment.TreasuryInvoiceID(invID)).WithSchedule(func(q *ent.InstalmentScheduleQuery) { q.WithContract() }).First(ctx); err == nil {
		if in.Edges.Schedule != nil && in.Edges.Schedule.Edges.Contract != nil && in.Edges.Schedule.Edges.Contract.UnitAccountID != nil {
			return *in.Edges.Schedule.Edges.Contract.UnitAccountID
		}
	}
	return uuid.Nil
}

func (c *Consumer) apply(ctx context.Context, tenantID, accountID uuid.UUID, p map[string]any) (*ent.UnitAccount, error) {
	acc, err := c.client.UnitAccount.Query().Where(unitaccount.ID(accountID)).WithFund().WithUnit().Only(ctx)
	if err != nil {
		return nil, err
	}
	led, err := c.svc.accounts.Refresh(ctx, acc)
	if err != nil {
		return nil, err
	}
	if amt, err := decimal.NewFromString(str(p["amount"])); err == nil && amt.IsPositive() && acc.Edges.Unit != nil {
		if err := reports.RecordCollection(ctx, c.client, tenantID, acc.Edges.Unit.PropertyID, time.Now(), amt, c.loc); err != nil {
			c.log.Warn("daily collection update failed", zap.Error(err))
		}
	}
	if c.sales != nil && acc.Edges.Fund != nil && acc.Edges.Fund.Kind == "sales" {
		if err := c.sales(ctx, acc.ID); err != nil {
			c.log.Warn("sales progress update failed", zap.Error(err))
		}
	}
	return acc, events.Publish(ctx, c.client.OutboxEvent, tenantID, acc.ID.String(), events.PaymentApplied, map[string]any{
		"account_id": acc.ID, "account_ref": acc.AccountRef, "amount": str(p["amount"]),
		"receipt": str(p["provider_reference"]), "balance": led.Balance.StringFixed(2),
		"phone": acc.CustomerPhone, "name": acc.CustomerName, "intent_id": str(p["intent_id"]),
		"email": accounts.CustomerEmail(acc), "fund": fundCode(acc), "method": str(p["payment_method"]),
	})
}

func fundCode(acc *ent.UnitAccount) string {
	if acc.Edges.Fund != nil {
		return acc.Edges.Fund.Code
	}
	return ""
}

func str(v any) string {
	s, _ := v.(string)
	return s
}
