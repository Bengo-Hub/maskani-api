package collections

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/billquery"
	"github.com/bengobox/maskani-api/internal/ent/maskaniuseroutlet"
	"github.com/bengobox/maskani-api/internal/ent/unitaccount"
	"github.com/bengobox/maskani-api/internal/events"
	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/accounts"
	"github.com/bengobox/maskani-api/internal/modules/register"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
	"github.com/bengobox/maskani-api/internal/shared/page"
)

// Bill query events: finance hears of a new query; the resident hears the answer.
const (
	BillQueryRaised   = "bill_query.raised"
	BillQueryAnswered = "bill_query.answered"
)

// BillQueryWindowDays is how long after a bill's date a resident may query it, and
// BillQueryAnswerDays how long finance has to answer (SRDD FR-30).
const (
	BillQueryWindowDays = 30
	BillQueryAnswerDays = 7
)

// BillQueryInput is a resident's question about a bill.
type BillQueryInput struct {
	InvoiceID *uuid.UUID `json:"invoice_id"`
	Subject   string     `json:"subject"`
	Body      string     `json:"body"`
}

// Raiser is the resident raising a query.
type Raiser struct {
	UserID  uuid.UUID
	PartyID *uuid.UUID
	Name    string
}

// RaiseBillQuery records a query on the account (optionally about one bill, which must be on the
// account and no older than the window) and tells finance and the manager.
func (s *Service) RaiseBillQuery(ctx context.Context, accountID uuid.UUID, by Raiser, in BillQueryInput) (*ent.BillQuery, error) {
	in.Subject, in.Body = strings.TrimSpace(in.Subject), strings.TrimSpace(in.Body)
	switch {
	case in.Subject == "" || len(in.Subject) > 120:
		return nil, httpx.Invalid("give the query a short subject (up to 120 characters)")
	case in.Body == "" || len(in.Body) > 2000:
		return nil, httpx.Invalid("describe what looks wrong (up to 2000 characters)")
	}
	acc, err := s.client.UnitAccount.Query().Where(unitaccount.ID(accountID)).WithUnit().Only(ctx)
	if err != nil {
		return nil, err
	}
	if acc.Edges.Unit == nil {
		return nil, httpx.Invalid("this account has no unit")
	}
	// Bounded: no one keeps more than a handful of open queries on one account.
	if n, err := s.client.BillQuery.Query().Where(billquery.UnitAccountID(acc.ID),
		billquery.StatusIn(billquery.StatusOpen, billquery.StatusInReview)).Count(ctx); err != nil {
		return nil, err
	} else if n >= 5 {
		return nil, httpx.Conflict("this account already has five open queries; wait for an answer first")
	}
	tenantID, _ := tenantguard.TenantID(ctx)
	meta := map[string]any{"account_ref": acc.AccountRef, "unit_code": acc.Edges.Unit.Code, "raised_by_name": by.Name}
	if in.InvoiceID != nil {
		led, err := s.treasury.Ledger(ctx, tenantID, acc.AccountRef, 200)
		if err != nil {
			return nil, httpx.Unavailable("the accounts service is not answering; try again shortly")
		}
		found := false
		for _, inv := range led.Invoices {
			if inv.ID != *in.InvoiceID {
				continue
			}
			found = true
			if time.Since(inv.InvoiceDate) > BillQueryWindowDays*24*time.Hour {
				return nil, httpx.Invalid("bills can be queried within 30 days of their date; contact the estate office about older ones")
			}
			meta["invoice_number"] = inv.InvoiceNumber
		}
		if !found {
			return nil, httpx.Invalid("that bill is not on this account")
		}
	}
	q, err := s.client.BillQuery.Create().SetUnitAccountID(acc.ID).SetPropertyID(acc.Edges.Unit.PropertyID).
		SetNillableTreasuryInvoiceID(in.InvoiceID).SetNillablePartyID(by.PartyID).SetSubject(in.Subject).SetBody(in.Body).
		SetDueBy(time.Now().AddDate(0, 0, BillQueryAnswerDays)).SetMetadata(meta).Save(ctx)
	if err != nil {
		return nil, err
	}
	payload := map[string]any{"bill_query_id": q.ID, "account_id": acc.ID, "account_ref": acc.AccountRef,
		"unit_code": acc.Edges.Unit.Code, "subject": in.Subject, "invoice_number": meta["invoice_number"],
		"raised_by": by.Name, "property_id": acc.Edges.Unit.PropertyID, "due_by": q.DueBy.Format("2006-01-02")}
	if rs, name, err := register.PropertyResponders(ctx, s.client, acc.Edges.Unit.PropertyID, []maskaniuseroutlet.PropertyRole{
		maskaniuseroutlet.PropertyRoleFinance, maskaniuseroutlet.PropertyRolePropertyManager}, 10); err == nil {
		payload["property"] = name
		if len(rs) > 0 {
			payload["responders"] = rs
		}
	}
	_ = events.Publish(ctx, s.client.OutboxEvent, tenantID, q.ID.String(), BillQueryRaised, payload)
	return q, nil
}

// ListBillQueries returns a keyset page (newest first) by status, for staff (properties) or for a
// resident (their accounts).
func (s *Service) ListBillQueries(ctx context.Context, status string, propertyIDs []uuid.UUID, all bool, accountIDs []uuid.UUID, p page.Params) (page.Result[*ent.BillQuery], error) {
	q := s.client.BillQuery.Query()
	if status != "" {
		q = q.Where(billquery.StatusEQ(billquery.Status(status)))
	}
	if accountIDs != nil {
		q = q.Where(billquery.UnitAccountIDIn(accountIDs...))
	}
	if !all {
		q = q.Where(billquery.PropertyIDIn(propertyIDs...))
	}
	rows, err := q.Where(p.Predicate()).Modify(page.Order()).Limit(p.Limit + 1).All(ctx)
	if err != nil {
		return page.Result[*ent.BillQuery]{}, err
	}
	return page.Build(rows, p.Limit, func(b *ent.BillQuery) (uuid.UUID, time.Time) { return b.ID, b.CreatedAt }), nil
}

// BillQueryPropertyID returns the property of a query (for scope checks).
func (s *Service) BillQueryPropertyID(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	b, err := s.client.BillQuery.Query().Where(billquery.ID(id)).Select(billquery.FieldPropertyID).Only(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	return b.PropertyID, nil
}

// BillQueryAnswer moves a query on: in_review (taken, assigned to the caller), resolved or
// rejected (with the answer the resident sees).
type BillQueryAnswer struct {
	Status     string `json:"status"`
	Resolution string `json:"resolution"`
}

// AnswerBillQuery updates a query; resolving or rejecting needs an answer and tells the resident.
func (s *Service) AnswerBillQuery(ctx context.Context, id uuid.UUID, by Reviewer, in BillQueryAnswer) (*ent.BillQuery, error) {
	in.Resolution = strings.TrimSpace(in.Resolution)
	status := billquery.Status(in.Status)
	q, err := s.client.BillQuery.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if q.Status == billquery.StatusResolved || q.Status == billquery.StatusRejected {
		return nil, httpx.Conflict("this query has already been answered")
	}
	upd := s.client.BillQuery.Update().Where(billquery.ID(id), billquery.StatusIn(billquery.StatusOpen, billquery.StatusInReview))
	switch status {
	case billquery.StatusInReview:
		upd = upd.SetStatus(status).SetAssignedTo(by.UserID)
	case billquery.StatusResolved, billquery.StatusRejected:
		if in.Resolution == "" || len(in.Resolution) > 2000 {
			return nil, httpx.Invalid("write the answer the resident will see (up to 2000 characters)")
		}
		meta := map[string]any{}
		for k, v := range q.Metadata {
			meta[k] = v
		}
		meta["answered_by_name"], meta["answered_at"] = by.Name, time.Now().UTC().Format(time.RFC3339)
		upd = upd.SetStatus(status).SetResolution(in.Resolution).SetMetadata(meta)
		if q.AssignedTo == nil {
			upd = upd.SetAssignedTo(by.UserID)
		}
	default:
		return nil, httpx.Invalid("status must be in_review, resolved or rejected")
	}
	n, err := upd.Save(ctx)
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, httpx.Conflict("this query has already been answered")
	}
	if status != billquery.StatusInReview {
		s.tellResident(ctx, q, string(status), in.Resolution)
	}
	return s.client.BillQuery.Get(ctx, id)
}

// tellResident sends the answer to the account holder by the channels notifications chooses.
func (s *Service) tellResident(ctx context.Context, q *ent.BillQuery, status, answer string) {
	acc, err := s.client.UnitAccount.Get(ctx, q.UnitAccountID)
	if err != nil {
		return
	}
	tenantID, _ := tenantguard.TenantID(ctx)
	_ = events.Publish(ctx, s.client.OutboxEvent, tenantID, q.ID.String(), BillQueryAnswered, map[string]any{
		"bill_query_id": q.ID, "account_id": acc.ID, "account_ref": acc.AccountRef, "subject": q.Subject,
		"status": status, "resolution": answer, "name": acc.CustomerName, "phone": acc.CustomerPhone,
		"email": accounts.CustomerEmail(acc), "invoice_number": q.Metadata["invoice_number"]})
}
