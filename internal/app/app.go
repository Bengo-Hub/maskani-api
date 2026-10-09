// Package app wires maskani-api together.
package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	sharedcache "github.com/Bengo-Hub/cache"
	"github.com/Bengo-Hub/httpware"
	authclient "github.com/Bengo-Hub/shared-auth-client"
	eventslib "github.com/Bengo-Hub/shared-events"
	ratelimit "github.com/Bengo-Hub/shared-ratelimit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/bengobox/maskani-api/internal/config"
	"github.com/bengobox/maskani-api/internal/ent"
	_ "github.com/bengobox/maskani-api/internal/ent/runtime" // registers the tenant guard hooks and interceptors
	"github.com/bengobox/maskani-api/internal/http/handlers"
	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/http/router"
	"github.com/bengobox/maskani-api/internal/jobs"
	"github.com/bengobox/maskani-api/internal/modules/accounts"
	"github.com/bengobox/maskani-api/internal/modules/authapi"
	"github.com/bengobox/maskani-api/internal/modules/billing"
	"github.com/bengobox/maskani-api/internal/modules/collections"
	"github.com/bengobox/maskani-api/internal/modules/gate"
	"github.com/bengobox/maskani-api/internal/modules/imports"
	"github.com/bengobox/maskani-api/internal/modules/market"
	"github.com/bengobox/maskani-api/internal/modules/notices"
	"github.com/bengobox/maskani-api/internal/modules/notify"
	"github.com/bengobox/maskani-api/internal/modules/portal"
	"github.com/bengobox/maskani-api/internal/modules/rbac"
	"github.com/bengobox/maskani-api/internal/modules/register"
	"github.com/bengobox/maskani-api/internal/modules/reports"
	"github.com/bengobox/maskani-api/internal/modules/sales"
	"github.com/bengobox/maskani-api/internal/modules/sequence"
	"github.com/bengobox/maskani-api/internal/modules/settings"
	"github.com/bengobox/maskani-api/internal/modules/tenant"
	"github.com/bengobox/maskani-api/internal/modules/treasury"
	"github.com/bengobox/maskani-api/internal/modules/utilities"
	"github.com/bengobox/maskani-api/internal/modules/works"
	"github.com/bengobox/maskani-api/internal/platform/database"
	"github.com/bengobox/maskani-api/internal/platform/events"
	"github.com/bengobox/maskani-api/internal/platform/realtime"
	"github.com/bengobox/maskani-api/internal/shared/logger"
	"github.com/bengobox/maskani-api/internal/shared/secure"
)

// App holds the running service.
type App struct {
	cfg      *config.Config
	log      *zap.Logger
	server   *http.Server
	pool     *pgxpool.Pool
	cache    *redis.Client
	nc       *nats.Conn
	orm      *ent.Client
	roOrm    *ent.Client
	outbox   *eventslib.OutboxPoller
	consumer *collections.Consumer
	notices  *notices.Service
	jobs     *jobs.Runner
}

// New constructs the application.
func New(ctx context.Context) (*App, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	log, err := logger.New(cfg.App.Env)
	if err != nil {
		return nil, fmt.Errorf("logger: %w", err)
	}
	httpx.Log = log
	loc, err := time.LoadLocation(cfg.App.Timezone)
	if err != nil {
		loc = time.FixedZone("EAT", 3*3600)
	}

	pool, err := database.NewPool(ctx, cfg.Postgres)
	if err != nil {
		return nil, err
	}
	rdb, rerr := sharedcache.NewRedis(ctx, sharedcache.RedisConfig{Addr: cfg.Redis.Addr, Username: cfg.Redis.Username,
		Password: cfg.Redis.Password, DB: cfg.Redis.DB, TLS: cfg.Redis.TLSRequired, DialTimeout: cfg.Redis.DialTimeout})
	if rerr != nil {
		log.Warn("redis not reachable at startup", zap.Error(rerr))
	}
	sharedcache.SetLeaseClient(rdb)

	nc, nerr := events.Connect(cfg.Events)
	if nerr != nil {
		log.Warn("nats connection failed", zap.Error(nerr))
	}
	if nc != nil {
		if err := events.EnsureStream(nc, cfg.Events); err != nil {
			log.Warn("ensure maskani stream failed", zap.Error(err))
		}
		_ = eventslib.NewBroadcaster(log, nc, "auth").Subscribe("apikey.changed", func(m eventslib.BroadcastMessage) {
			authclient.InvalidateAPIKeyHash(string(m.Data))
		})
	}

	sqlDB, err := database.OpenSQL(cfg.Postgres.URL, cfg.Postgres)
	if err != nil {
		return nil, err
	}
	orm := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, sqlDB)))
	roOrm, roSQL := orm, sqlDB
	if cfg.Postgres.ReadOnlyURL != "" && cfg.Postgres.ReadOnlyURL != cfg.Postgres.URL {
		if roDB, err := database.OpenSQL(cfg.Postgres.ReadOnlyURL, cfg.Postgres); err == nil {
			roOrm, roSQL = ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, roDB))), roDB
		}
	}

	var outbox *eventslib.OutboxPoller
	if nc != nil && cfg.Events.OutboxEnabled {
		js, err := nc.JetStream()
		if err != nil {
			return nil, fmt.Errorf("jetstream: %w", err)
		}
		outbox = eventslib.NewOutboxPoller(eventslib.NewSQLOutboxRepository(sqlDB), eventslib.NewJetStreamAdapter(js, log), log,
			eventslib.PollerConfig{BatchSize: cfg.Events.OutboxBatchSize, PollPeriod: cfg.Events.OutboxPollPeriod})
		outbox.Start(ctx)
	}

	box, err := secure.NewBox(cfg.Security.FieldEncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("FIELD_ENCRYPTION_KEY: %w", err)
	}

	// Services.
	rbacSvc := rbac.NewService(orm, log)
	if err := rbacSvc.Seed(ctx); err != nil {
		log.Warn("rbac seed failed (seed binary retries on deploy)", zap.Error(err))
	}
	settingsSvc := settings.NewService(orm, log)
	syncer := tenant.NewSyncer(orm, cfg.Auth.APIURL, sqlDB, log)
	syncer.OnFirstSeen(func(ctx context.Context, id uuid.UUID, slug string) {
		if err := settingsSvc.EnsureTenantDefaults(ctx, id); err != nil {
			log.Warn("tenant defaults failed", zap.String("slug", slug), zap.Error(err))
		}
		// Mirror the tenant's property outlets (and HQ) so properties link to auth-api branches and
		// staff outlet scoping resolves; other products' outlets are filtered out.
		if err := syncer.SyncOutlets(ctx, id, slug); err != nil {
			log.Warn("outlet sync failed", zap.String("slug", slug), zap.Error(err))
		}
	})
	tc := treasury.NewClient(cfg.Services.TreasuryURL, cfg.Auth.APIKey, log)
	ac := authapi.NewClient(cfg.Auth.APIURL, cfg.Auth.APIKey, log)
	nt := notify.NewClient(cfg.Services.NotificationsURL, cfg.Auth.APIKey, log)
	seq := sequence.NewAllocator(orm)
	accSvc := accounts.NewService(orm, tc, log)
	regSvc := register.NewService(orm, ac, accSvc, box, log)
	billSvc := billing.NewService(orm, tc, accSvc, loc, log)
	if rdb != nil {
		billSvc.SetRedis(rdb)
	}
	collSvc := collections.NewService(orm, tc, accSvc, log)
	utilSvc := utilities.NewService(orm, log)
	salesSvc := sales.NewService(orm, tc, accSvc, seq, loc, log)
	worksSvc := works.NewService(orm, seq, log)
	gateSvc := gate.NewService(orm, box, seq, log)
	noticeSvc := notices.NewService(orm, nt, loc, log)
	reportSvc := reports.NewService(roOrm, roSQL, utilities.NewService(roOrm, log), loc, log)
	portalSvc := portal.NewService(orm, log)
	marketSvc := market.NewService(orm, box, log)

	consumer := collections.NewConsumer(orm, collSvc, salesSvc.SyncProgress, loc, log)
	consumer.OnApplied = reportSvc.Invalidate

	// Live change hints for SSE clients, relayed to every replica over core NATS.
	rt := realtime.NewHub(log, nc)
	billSvc.SetRealtime(rt)
	worksSvc.SetRealtime(rt)
	gateSvc.SetRealtime(rt)
	utilSvc.SetRealtime(rt)
	noticeSvc.SetRealtime(rt)
	consumer.RT = rt
	// A payment or billing run on any pod drops this pod's cached dashboard figures for the tenant.
	rt.OnEvent(func(tenantID uuid.UUID, ev realtime.Event) {
		if ev.Type == realtime.PaymentApplied || ev.Type == realtime.BillingRunProgress {
			reportSvc.Invalidate(tenantID)
		}
	})

	// Auth.
	authCfg := authclient.DefaultConfig(cfg.Auth.JWKSUrl, cfg.Auth.Issuer, cfg.Auth.Audience)
	authCfg.CacheTTL, authCfg.RefreshInterval = cfg.Auth.JWKSCacheTTL, cfg.Auth.JWKSRefreshInterval
	validator, err := authclient.NewValidator(authCfg)
	if err != nil {
		return nil, fmt.Errorf("auth validator: %w", err)
	}
	authMW := authclient.NewAuthMiddleware(validator)
	if cfg.Auth.EnableAPIKeyAuth {
		authMW = authclient.NewAuthMiddlewareWithAPIKey(validator, authclient.NewAPIKeyValidator(cfg.Auth.ServiceURL, nil))
	}

	signer := httpware.NewMediaSigner(cfg.Security.MediaSigningSecret, 12*time.Hour)
	importSvc := imports.NewService(orm, regSvc, log)
	h := &handlers.H{RBAC: rbacSvc, Settings: settingsSvc, Register: regSvc, Accounts: accSvc, Billing: billSvc,
		Collections: collSvc, Utilities: utilSvc, Sales: salesSvc, Works: worksSvc, Gate: gateSvc, Notices: noticeSvc,
		Reports: reportSvc, Portal: portalSvc, Market: marketSvc, Imports: importSvc, PortalURL: strings.TrimRight(cfg.HTTP.AppURL, "/"),
		Media: &handlers.Media{Root: cfg.Media.Root, URLBase: cfg.Media.URLBase, MaxMB: cfg.Media.MaxMB, Signer: signer, Log: log},
		RT:    rt}

	var limiter *ratelimit.Limiter
	if rdb != nil {
		limiter = ratelimit.NewLimiter(rdb, log, "maskani")
	}
	mux := router.New(router.Deps{Log: log, Limiter: limiter, Auth: authMW, AllowedOrigins: cfg.HTTP.AllowedOrigins,
		Ent: orm, RBAC: rbacSvc, Settings: settingsSvc, TenantSyncer: syncer, H: h,
		Health: &handlers.Health{DB: pool, Cache: rdb, Events: nc}, MediaRoot: cfg.Media.Root, MediaSigner: signer,
		InternalKey: cfg.Auth.APIKey})

	runner := jobs.New(jobs.Deps{Client: orm, SQL: sqlDB, Loc: loc, Accounts: accSvc, Billing: billSvc, Imports: importSvc, Sales: salesSvc, Works: worksSvc, Gate: gateSvc, Notices: noticeSvc, Settings: settingsSvc, Log: log})

	return &App{cfg: cfg, log: log, pool: pool, cache: rdb, nc: nc, orm: orm, roOrm: roOrm, outbox: outbox,
		consumer: consumer, notices: noticeSvc, jobs: runner,
		server: &http.Server{Addr: fmt.Sprintf("%s:%d", cfg.HTTP.Host, cfg.HTTP.Port), Handler: mux,
			ReadTimeout: cfg.HTTP.ReadTimeout, ReadHeaderTimeout: 5 * time.Second, WriteTimeout: cfg.HTTP.WriteTimeout,
			IdleTimeout: cfg.HTTP.IdleTimeout}}, nil
}

// Run serves until ctx ends.
func (a *App) Run(ctx context.Context) error {
	if a.nc != nil {
		if js, err := a.nc.JetStream(); err == nil {
			a.consumer.Start(js)
			a.notices.StartCompletedConsumer(js)
		}
	}
	a.jobs.Start(ctx)
	errCh := make(chan error, 1)
	go func() {
		a.log.Info("maskani-api listening", zap.String("addr", a.server.Addr))
		errCh <- a.server.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return a.server.Shutdown(sctx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// Close releases resources.
func (a *App) Close() {
	if a.outbox != nil {
		a.outbox.Stop()
	}
	if a.nc != nil {
		_ = a.nc.Drain()
	}
	if a.cache != nil {
		_ = a.cache.Close()
	}
	if a.roOrm != nil && a.roOrm != a.orm {
		_ = a.roOrm.Close()
	}
	if a.orm != nil {
		_ = a.orm.Close()
	}
	if a.pool != nil {
		a.pool.Close()
	}
	_ = a.log.Sync()
}
