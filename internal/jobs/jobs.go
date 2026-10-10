// Package jobs runs background work once per fleet per period using the shared Redis lease
// (cache.ClaimPeriod), never session advisory locks (they do not survive PgBouncer).
package jobs

import (
	"context"
	stdsql "database/sql"
	"time"

	sharedcache "github.com/Bengo-Hub/cache"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/salecontract"
	"github.com/bengobox/maskani-api/internal/ent/tenant"
	"github.com/bengobox/maskani-api/internal/modules/accounts"
	"github.com/bengobox/maskani-api/internal/modules/billing"
	"github.com/bengobox/maskani-api/internal/modules/collections"
	"github.com/bengobox/maskani-api/internal/modules/gate"
	"github.com/bengobox/maskani-api/internal/modules/imports"
	"github.com/bengobox/maskani-api/internal/modules/notices"
	"github.com/bengobox/maskani-api/internal/modules/reminders"
	"github.com/bengobox/maskani-api/internal/modules/reports"
	"github.com/bengobox/maskani-api/internal/modules/sales"
	"github.com/bengobox/maskani-api/internal/modules/settings"
	"github.com/bengobox/maskani-api/internal/modules/works"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

// Job is one scheduled task.
type Job struct {
	Name   string
	Period time.Duration
	Run    func(ctx context.Context) error
}

// Runner ticks every minute and runs each due job on exactly one pod.
type Runner struct {
	jobs []Job
	log  *zap.Logger
}

// Deps are the services jobs call.
type Deps struct {
	Client      *ent.Client
	SQL         *stdsql.DB // primary handle for set-based rollups
	Loc         *time.Location
	Accounts    *accounts.Service
	Billing     *billing.Service
	Imports     *imports.Service
	Sales       *sales.Service
	Works       *works.Service
	Gate        *gate.Service
	Notices     *notices.Service
	Settings    *settings.Service
	Reminders   *reminders.Service
	Collections *collections.Service
	Log         *zap.Logger
}

// New builds the runner with the standard maskani jobs (docs/architecture.md, background jobs).
// Jobs that alert, send or invoice for one module run only for tenants with that module switched
// on (FR-09: a switched-off module is read only). Housekeeping that keeps data true (expiring
// holds, resuming runs and sends already under way, retention, pruning) runs for every tenant.
func New(d Deps) *Runner {
	log := d.Log.Named("jobs")
	sys := func(ctx context.Context) context.Context { return tenantguard.System(ctx) }
	return &Runner{log: log, jobs: []Job{
		{"maskani:c2b-routes", 5 * time.Minute, func(ctx context.Context) error {
			_, err := d.Accounts.RegisterPending(sys(ctx), 200)
			return err
		}},
		{"maskani:billing-resume", 2 * time.Minute, func(ctx context.Context) error {
			_, err := d.Billing.ResumeStuck(sys(ctx))
			return err
		}},
		{"maskani:imports-housekeeping", 15 * time.Minute, func(ctx context.Context) error {
			_, _, err := d.Imports.Housekeep(sys(ctx))
			return err
		}},
		{"maskani:reservation-expiry", 15 * time.Minute, func(ctx context.Context) error {
			_, err := d.Sales.ExpireReservations(sys(ctx))
			return err
		}},
		// Billing schedules: reading reminders before the billing day, then the run (or a wait for
		// missing readings, or a "ready" note where the schedule only reminds).
		{"maskani:billing-schedules", time.Hour, func(ctx context.Context) error {
			on, err := d.Settings.TenantsWithModule(sys(ctx), "billing")
			if err != nil || len(on) == 0 {
				return err
			}
			n, err := d.Billing.RunSchedules(ctx, on)
			if n > 0 {
				log.Info("scheduled billing runs started", zap.Int("runs", n))
			}
			return err
		}},
		// Per-account collections before 2026-10-10 come from treasury's ledgers, 200 accounts an
		// hour, each once; after that the payment consumer writes them.
		{"maskani:collections-backfill", time.Hour, func(ctx context.Context) error {
			if d.Collections == nil {
				return nil
			}
			on, err := d.Settings.TenantsWithModule(sys(ctx), "billing")
			if err != nil || len(on) == 0 {
				return err
			}
			n, err := d.Collections.BackfillAccountCollections(ctx, on, d.Loc, 200)
			if n > 0 {
				log.Info("account collections backfilled", zap.Int("accounts", n))
			}
			return err
		}},
		// Collections ladder: one step a day at most per owing account, in working hours only.
		{"maskani:collections-ladder", time.Hour, func(ctx context.Context) error {
			if d.Reminders == nil {
				return nil
			}
			on, err := d.Settings.TenantsWithModule(sys(ctx), "billing")
			if err != nil || len(on) == 0 {
				return err
			}
			n, err := d.Reminders.RunLadder(ctx, on, time.Now())
			if n > 0 {
				log.Info("collections steps run", zap.Int("steps", n))
			}
			return err
		}},
		// Instalments: reminders 3 days before, on the day, 7 and 14 days late; overdue marking;
		// contracts in and out of default after their grace days.
		{"maskani:instalment-reminders", time.Hour, func(ctx context.Context) error {
			if d.Reminders == nil {
				return nil
			}
			on, err := d.Settings.TenantsWithModule(sys(ctx), "sales")
			if err != nil || len(on) == 0 {
				return err
			}
			_, err = d.Reminders.RunInstalments(ctx, on, time.Now())
			return err
		}},
		{"maskani:instalment-invoicing", time.Hour, func(ctx context.Context) error {
			on, err := d.Settings.TenantsWithModule(sys(ctx), "sales")
			if err != nil || len(on) == 0 {
				return err
			}
			tenants, err := d.Client.SaleContract.Query().Where(salecontract.TenantIDIn(on...),
				salecontract.StatusIn(salecontract.StatusActive, salecontract.StatusInDefault)).
				Unique(true).Select(salecontract.FieldTenantID).All(sys(ctx))
			if err != nil {
				return err
			}
			seen := map[uuid.UUID]bool{}
			for _, t := range tenants {
				if seen[t.TenantID] {
					continue
				}
				seen[t.TenantID] = true
				if _, err := d.Sales.InvoiceDue(tenantguard.With(ctx, t.TenantID), nil, time.Now().AddDate(0, 0, 7)); err != nil {
					log.Warn("instalment invoicing failed", zap.String("tenant", t.TenantID.String()), zap.Error(err))
				}
			}
			return nil
		}},
		{"maskani:sla-breaches", 5 * time.Minute, func(ctx context.Context) error {
			on, err := d.Settings.TenantsWithModule(sys(ctx), "maintenance")
			if err != nil {
				return err
			}
			_, err = d.Works.FlagBreaches(sys(ctx), on)
			return err
		}},
		{"maskani:vendor-doc-expiry", 24 * time.Hour, func(ctx context.Context) error {
			on, err := d.Settings.TenantsWithModule(sys(ctx), "providers", "maintenance")
			if err != nil {
				return err
			}
			_, err = d.Works.AlertExpiring(sys(ctx), on)
			return err
		}},
		{"maskani:gate-retention", 24 * time.Hour, func(ctx context.Context) error {
			_, err := d.Gate.PurgeOld(sys(ctx))
			return err
		}},
		{"maskani:gate-offline", 5 * time.Minute, func(ctx context.Context) error {
			on, err := d.Settings.TenantsWithModule(sys(ctx), "gate")
			if err != nil {
				return err
			}
			_, err = d.Gate.OfflineDevices(sys(ctx), on)
			return err
		}},
		{"maskani:scheduled-notices", 5 * time.Minute, func(ctx context.Context) error {
			on, err := d.Settings.TenantsWithModule(sys(ctx), "communication")
			if err != nil {
				return err
			}
			// Scheduled notices wait while communication is off; sends already under way finish below.
			due, err := d.Notices.DueScheduled(sys(ctx), on)
			if err != nil {
				return err
			}
			// Tenant slugs for the whole batch in one read.
			ids := make([]uuid.UUID, 0, len(due))
			for _, n := range due {
				ids = append(ids, n.TenantID)
			}
			slugs := map[uuid.UUID]string{}
			if len(ids) > 0 {
				if ts, err := d.Client.Tenant.Query().Where(tenant.IDIn(ids...)).All(sys(ctx)); err == nil {
					for _, t := range ts {
						slugs[t.ID] = t.Slug
					}
				}
			}
			for _, n := range due {
				if _, err := d.Notices.Send(tenantguard.With(ctx, n.TenantID), n.ID, slugs[n.TenantID]); err != nil {
					log.Warn("scheduled notice failed", zap.Error(err))
				}
			}
			// Direct sends whose pod stopped mid-way carry on from where they were.
			stuck, err := d.Notices.StuckDirect(sys(ctx))
			if err != nil {
				return err
			}
			for _, n := range stuck {
				d.Notices.Resume(tenantguard.With(ctx, n.TenantID), n)
			}
			return nil
		}},
		// Yesterday's daily_stats rebuilt from the source tables, one statement per tenant, which
		// repairs anything an event missed (collections stay with the payment consumer).
		{"maskani:daily-stats-rebuild", 24 * time.Hour, func(ctx context.Context) error {
			if d.SQL == nil {
				return nil
			}
			n, err := reports.RebuildTenants(ctx, d.SQL, d.Loc)
			log.Info("daily stats rebuilt", zap.Int("tenants", n))
			return err
		}},
		{"maskani:outbox-prune-passes", 15 * time.Minute, func(ctx context.Context) error {
			_, err := PruneOutbox(sys(ctx), d.Client, true)
			return err
		}},
		{"maskani:outbox-prune", 24 * time.Hour, func(ctx context.Context) error {
			n, err := PruneOutbox(sys(ctx), d.Client, false)
			if n > 0 {
				log.Info("outbox pruned", zap.Int("rows", n))
			}
			return err
		}},
	}}
}

// Start runs until ctx ends. Each claimed job runs in its own goroutine with a timeout of its
// period, so an hour-long job never holds up the five-minute ones; the period claim (shared across
// pods) keeps one run of a job at a time.
func (r *Runner) Start(ctx context.Context) {
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				for _, j := range r.jobs {
					if !sharedcache.ClaimPeriod(ctx, j.Name, j.Period) {
						continue
					}
					go r.run(ctx, j)
				}
			}
		}
	}()
}

func (r *Runner) run(ctx context.Context, j Job) {
	defer func() {
		if p := recover(); p != nil {
			r.log.Error("job panicked", zap.String("job", j.Name), zap.Any("panic", p))
		}
	}()
	jctx, cancel := context.WithTimeout(ctx, j.Period)
	defer cancel()
	if err := j.Run(jctx); err != nil {
		r.log.Warn("job failed", zap.String("job", j.Name), zap.Error(err))
	}
}
