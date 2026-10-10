package collections

import (
	"context"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/sqljson"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/accountcollection"
	"github.com/bengobox/maskani-api/internal/ent/unitaccount"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

// collectionsCutoff is the last day the backfill owns. The payment consumer started writing
// account_collections on 2026-10-10; for every day up to and including that one the treasury
// ledger is the truth and its totals replace whatever rows exist, so the deploy day is neither
// missed nor counted twice. Later days belong to the consumer alone.
var collectionsCutoff = time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)

const backfillKey = "collections_backfilled"

// BackfillAccountCollections fills account_collections from treasury's account ledgers for up to
// limit accounts not yet done (one ledger read each), marking each done. Returns how many it did.
func (s *Service) BackfillAccountCollections(ctx context.Context, tenants []uuid.UUID, loc *time.Location, limit int) (int, error) {
	if s.treasury == nil || !s.treasury.Enabled() {
		return 0, nil
	}
	done := 0
	for _, tid := range tenants {
		if done >= limit {
			break
		}
		tctx := tenantguard.With(ctx, tid)
		accs, err := s.client.UnitAccount.Query().Where(
			func(sel *entsql.Selector) {
				sel.Where(entsql.Not(sqljson.HasKey(unitaccount.FieldMetadata, sqljson.Path(backfillKey))))
			}).WithUnit().Limit(limit - done).All(tctx)
		if err != nil {
			return done, err
		}
		for _, acc := range accs {
			if err := s.backfillOne(tctx, tid, acc, loc); err != nil {
				s.log.Warn("collections backfill", zap.String("account", acc.AccountRef), zap.Error(err))
				continue
			}
			done++
		}
	}
	return done, nil
}

func (s *Service) backfillOne(ctx context.Context, tenantID uuid.UUID, acc *ent.UnitAccount, loc *time.Location) error {
	if acc.Edges.Unit == nil {
		return s.markBackfilled(ctx, acc)
	}
	led, err := s.treasury.Ledger(ctx, tenantID, acc.AccountRef, 500)
	if err != nil {
		return err
	}
	type dayTotal struct {
		amount decimal.Decimal
		count  int
	}
	days := map[time.Time]*dayTotal{}
	for _, p := range led.Payments {
		local := p.PaidAt.In(loc)
		day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
		if day.After(collectionsCutoff) || !p.Amount.IsPositive() {
			continue
		}
		t := days[day]
		if t == nil {
			t = &dayTotal{}
			days[day] = t
		}
		t.amount = t.amount.Add(p.Amount)
		t.count++
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.AccountCollection.Delete().Where(accountcollection.UnitAccountID(acc.ID),
		accountcollection.DayLTE(collectionsCutoff)).Exec(ctx); err != nil {
		return err
	}
	if len(days) > 0 {
		rows := make([]*ent.AccountCollectionCreate, 0, len(days))
		for day, t := range days {
			rows = append(rows, tx.AccountCollection.Create().SetTenantID(tenantID).SetUnitAccountID(acc.ID).
				SetUnitID(acc.UnitID).SetPropertyID(acc.Edges.Unit.PropertyID).SetFundID(acc.FundID).SetDay(day).
				SetAmount(t.amount).SetPaymentsCount(t.count))
		}
		if err := tx.AccountCollection.CreateBulk(rows...).Exec(ctx); err != nil {
			return err
		}
	}
	meta := map[string]any{}
	for k, v := range acc.Metadata {
		meta[k] = v
	}
	meta[backfillKey] = true
	if err := tx.UnitAccount.UpdateOneID(acc.ID).SetMetadata(meta).Exec(ctx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) markBackfilled(ctx context.Context, acc *ent.UnitAccount) error {
	meta := map[string]any{}
	for k, v := range acc.Metadata {
		meta[k] = v
	}
	meta[backfillKey] = true
	return s.client.UnitAccount.UpdateOneID(acc.ID).SetMetadata(meta).Exec(ctx)
}
