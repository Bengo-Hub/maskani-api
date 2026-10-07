// Package events writes maskani domain events to the transactional outbox. Publishing means
// inserting an outbox_events row in the same Ent transaction as the domain write; the shared
// outbox poller drains it to NATS. The payload column holds the full shared-events envelope.
package events

import (
	"context"
	"encoding/json"
	"fmt"

	eventslib "github.com/Bengo-Hub/shared-events"
	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/ent"
)

// AggregateType is the aggregate type (and subject prefix) of every maskani event.
const AggregateType = "maskani"

// Event types (subject = maskani.{type}). See docs/events.md.
const (
	UnitAccountCreated     = "unit_account.created"
	BillingRunCompleted    = "billing_run.completed"
	BillIssued             = "bill.issued"
	PaymentApplied         = "payment.applied"
	InstalmentDue          = "instalment.due"
	SaleContractActivated  = "sale_contract.activated"
	SaleContractDefaulted  = "sale_contract.defaulted"
	SaleContractFullyPaid  = "sale_contract.fully_paid"
	UnitHandedOver         = "unit.handed_over"
	WorkOrderCreated       = "work_order.created"
	WorkOrderAssigned      = "work_order.assigned"
	WorkOrderCompleted     = "work_order.completed"
	WorkOrderSLABreached   = "work_order.sla_breached"
	VisitorArrived         = "visitor.arrived"
	WalkInRequested        = "walk_in.requested"
	IncidentReported       = "incident.reported"
	VendorDocumentExpiring = "vendor.document_expiring"
	NoticePublished        = "notice.published"
	PartyInvited           = "party.invited"
	PassCreated            = "pass.created"
)

// Publisher is client.OutboxEvent or tx.OutboxEvent.
type Publisher interface {
	Create() *ent.OutboxEventCreate
}

// Publish writes one outbox row. Use tx.OutboxEvent to publish atomically with the domain write.
func Publish(ctx context.Context, oc Publisher, tenantID uuid.UUID, aggregateID, eventType string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("events: marshal payload: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return fmt.Errorf("events: payload must be a JSON object: %w", err)
	}
	if _, ok := m["tenant_id"]; !ok {
		m["tenant_id"] = tenantID.String()
	}
	aggID, err := uuid.Parse(aggregateID)
	if err != nil {
		aggID = uuid.NewSHA1(tenantID, []byte(aggregateID))
	}
	ev := eventslib.NewEvent(eventType, AggregateType, aggID, tenantID, m)
	raw, err := ev.ToJSON()
	if err != nil {
		return fmt.Errorf("events: marshal envelope: %w", err)
	}
	return oc.Create().
		SetID(ev.ID).
		SetTenantID(tenantID).
		SetAggregateType(AggregateType).
		SetAggregateID(aggID.String()).
		SetEventType(eventType).
		SetPayload(json.RawMessage(raw)).
		Exec(ctx)
}
