// Package tenant keeps the local tenant and outlet projections in step with auth-api, which owns
// tenant identity. Ported from hospital-api's syncer (drift guard and self-heal included).
package tenant

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/maskani-api/internal/ent"
	enttenant "github.com/bengobox/maskani-api/internal/ent/tenant"
	"github.com/bengobox/maskani-api/internal/modules/rbac"
)

var s2sHTTPClient = &http.Client{Timeout: 15 * time.Second}

// driftProbeClient keeps the per-call drift check short; on timeout the local projection stands.
var driftProbeClient = &http.Client{Timeout: 5 * time.Second}

// driftCheckInterval throttles the auth-api drift probe per slug.
const driftCheckInterval = 10 * time.Minute

// FirstSeenFunc runs once per pod when a tenant is first resolved, to ensure its defaults exist
// (settings, modules, funds, charge catalogue). It must be idempotent.
type FirstSeenFunc func(ctx context.Context, tenantID uuid.UUID, slug string)

// Syncer resolves tenant slugs to auth-api UUIDs and maintains the local projections.
type Syncer struct {
	client  *ent.Client
	authURL string
	db      *sql.DB
	log     *zap.Logger

	onFirstSeen FirstSeenFunc
	seenMu      sync.Mutex
	seen        map[uuid.UUID]bool

	driftMu       sync.Mutex
	lastDriftScan map[string]time.Time
}

// NewSyncer creates a Syncer. authURL is auth-api's base URL.
func NewSyncer(client *ent.Client, authURL string, db *sql.DB, log *zap.Logger) *Syncer {
	if log == nil {
		log = zap.NewNop()
	}
	return &Syncer{client: client, authURL: strings.TrimRight(authURL, "/"), db: db, log: log.Named("tenant-sync")}
}

// OnFirstSeen registers the defaults initialiser.
func (s *Syncer) OnFirstSeen(fn FirstSeenFunc) { s.onFirstSeen = fn }

type authAPITenant struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Slug    string `json:"slug"`
	Status  string `json:"status"`
	UseCase string `json:"use_case,omitempty"`
}

// SyncTenant returns the auth-api UUID for slug, creating or refreshing the local projection.
func (s *Syncer) SyncTenant(ctx context.Context, slug string) (uuid.UUID, error) {
	id, err := s.syncTenantID(ctx, slug)
	if err == nil && id != uuid.Nil {
		s.firstSeen(ctx, id, slug)
	}
	return id, err
}

func (s *Syncer) firstSeen(ctx context.Context, id uuid.UUID, slug string) {
	if s.onFirstSeen == nil {
		return
	}
	s.seenMu.Lock()
	if s.seen == nil {
		s.seen = map[uuid.UUID]bool{}
	}
	if s.seen[id] {
		s.seenMu.Unlock()
		return
	}
	s.seen[id] = true
	s.seenMu.Unlock()
	s.onFirstSeen(context.WithoutCancel(ctx), id, slug)
}

func (s *Syncer) syncTenantID(ctx context.Context, slug string) (uuid.UUID, error) {
	local, localErr := s.client.Tenant.Query().Where(enttenant.SlugEQ(slug)).Only(ctx)
	hasLocal := localErr == nil && local != nil
	endpoint := s.authURL + "/api/v1/tenants/by-slug/" + slug

	// auth-api owns the UUID. A tenant deleted and recreated upstream gets a new one, so the cached
	// row is confirmed periodically and re-keyed when it drifted.
	if hasLocal {
		if !s.dueForDriftScan(slug) {
			return local.ID, nil
		}
		remoteID, err := s.probeAuthTenantID(ctx, endpoint)
		if err != nil || remoteID == local.ID {
			return local.ID, nil
		}
		s.log.Warn("tenant uuid drift, adopting auth-api uuid", zap.String("slug", slug),
			zap.String("local", local.ID.String()), zap.String("auth", remoteID.String()))
		return s.adoptAuthTenantID(ctx, local.ID, remoteID, slug)
	}

	remote, err := s.fetchTenant(ctx, endpoint)
	if err != nil {
		return uuid.Nil, err
	}
	realID, err := uuid.Parse(remote.ID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("tenant: invalid uuid %q: %w", remote.ID, err)
	}
	status := remote.Status
	if status == "" {
		status = "active"
	}
	create := s.client.Tenant.Create().
		SetID(realID).SetSlug(remote.Slug).SetName(remote.Name).SetStatus(status).
		SetSyncStatus("synced").SetLastSyncAt(time.Now())
	if remote.UseCase != "" {
		create.SetUseCase(remote.UseCase)
	}
	if err := create.OnConflictColumns(enttenant.FieldID).UpdateNewValues().Exec(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("tenant: upsert %s: %w", slug, err)
	}
	return realID, nil
}

func (s *Syncer) fetchTenant(ctx context.Context, endpoint string) (*authAPITenant, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s2sHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tenant: GET %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("tenant: not found in auth-api")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tenant: auth-api HTTP %d", resp.StatusCode)
	}
	var t authAPITenant
	if err := json.NewDecoder(resp.Body).Decode(&t); err != nil {
		return nil, fmt.Errorf("tenant: decode: %w", err)
	}
	return &t, nil
}

func (s *Syncer) dueForDriftScan(slug string) bool {
	s.driftMu.Lock()
	defer s.driftMu.Unlock()
	if s.lastDriftScan == nil {
		s.lastDriftScan = map[string]time.Time{}
	}
	if last, ok := s.lastDriftScan[slug]; ok && time.Since(last) < driftCheckInterval {
		return false
	}
	s.lastDriftScan[slug] = time.Now()
	return true
}

func (s *Syncer) probeAuthTenantID(ctx context.Context, endpoint string) (uuid.UUID, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return uuid.Nil, err
	}
	resp, err := driftProbeClient.Do(req)
	if err != nil {
		return uuid.Nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return uuid.Nil, fmt.Errorf("auth-api HTTP %d", resp.StatusCode)
	}
	var t authAPITenant
	if err := json.NewDecoder(resp.Body).Decode(&t); err != nil {
		return uuid.Nil, err
	}
	return uuid.Parse(t.ID)
}

func sqlLiteral(v string) string { return "'" + strings.ReplaceAll(v, "'", "''") + "'" }

// adoptTenantIDSQL re-keys every tenant-scoped row from the old UUID onto the new one in one
// statement, discovering tenant_id columns and FKs to tenants from the catalog.
const adoptTenantIDSQL = `
DO $$
DECLARE
  old_id text := %s;
  new_id text := %s;
  slug_v text := %s;
  r record;
  cols text;
  vals text;
BEGIN
  IF NOT EXISTS (SELECT 1 FROM tenants WHERE id::text = old_id) THEN RETURN; END IF;
  IF NOT EXISTS (SELECT 1 FROM tenants WHERE id::text = new_id) THEN
    SELECT string_agg(quote_ident(column_name), ', ' ORDER BY ordinal_position) INTO cols
      FROM information_schema.columns WHERE table_schema='public' AND table_name='tenants';
    SELECT string_agg(CASE WHEN column_name='id' THEN quote_literal(new_id)
                           WHEN column_name='slug' THEN quote_literal('__drift_migrating__')
                           ELSE quote_ident(column_name) END, ', ' ORDER BY ordinal_position) INTO vals
      FROM information_schema.columns WHERE table_schema='public' AND table_name='tenants';
    EXECUTE format('INSERT INTO tenants (%%s) SELECT %%s FROM tenants WHERE id::text = %%L', cols, vals, old_id);
  END IF;
  FOR r IN
    SELECT c.table_name AS tbl, c.column_name AS col
      FROM information_schema.columns c
      JOIN information_schema.tables t
        ON t.table_schema=c.table_schema AND t.table_name=c.table_name AND t.table_type='BASE TABLE'
     WHERE c.table_schema='public' AND c.column_name='tenant_id'
    UNION
    SELECT con.conrelid::regclass::text, a.attname
      FROM pg_constraint con
      JOIN unnest(con.conkey) k ON true
      JOIN pg_attribute a ON a.attrelid=con.conrelid AND a.attnum=k
     WHERE con.confrelid='tenants'::regclass AND con.contype='f'
  LOOP
    BEGIN
      EXECUTE format('UPDATE %%I SET %%I = %%L WHERE %%I = %%L', r.tbl, r.col, new_id, r.col, old_id);
    EXCEPTION WHEN unique_violation OR foreign_key_violation THEN
      EXECUTE format('DELETE FROM %%I WHERE %%I = %%L', r.tbl, r.col, old_id);
      RAISE WARNING 'tenant drift: dropped stale rows from %% (collided under new UUID)', r.tbl;
    END;
  END LOOP;
  EXECUTE format('DELETE FROM tenants WHERE id::text = %%L', old_id);
  EXECUTE format('UPDATE tenants SET slug = %%L WHERE id::text = %%L', slug_v, new_id);
END $$;`

func (s *Syncer) adoptAuthTenantID(ctx context.Context, localID, remoteID uuid.UUID, slug string) (uuid.UUID, error) {
	if s.db == nil {
		return localID, nil
	}
	stmt := fmt.Sprintf(adoptTenantIDSQL, sqlLiteral(localID.String()), sqlLiteral(remoteID.String()), sqlLiteral(slug))
	if _, err := s.db.ExecContext(ctx, stmt); err != nil {
		s.log.Error("tenant drift self-heal failed", zap.String("slug", slug), zap.Error(err))
		return localID, nil
	}
	_ = s.client.Tenant.UpdateOneID(remoteID).SetSyncStatus("synced").SetLastSyncAt(time.Now()).Exec(ctx)
	return remoteID, nil
}

type authOutlet struct {
	ID       string         `json:"id"`
	Code     string         `json:"code"`
	Name     string         `json:"name"`
	UseCase  string         `json:"use_case"`
	// ApplicableServices is auth-api's own list of services an outlet serves.
	ApplicableServices []string `json:"applicable_services,omitempty"`
	IsHQ               bool     `json:"is_hq"`
	Status   string         `json:"status"`
	Address  string         `json:"address,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// isMaskaniOutlet is a property outlet by use case, or one auth-api lists as served by maskani-api.
func isMaskaniOutlet(o authOutlet) bool {
	if rbac.IsPropertyUseCase(o.UseCase) {
		return true
	}
	for _, svc := range o.ApplicableServices {
		if svc == "maskani-api" {
			return true
		}
	}
	return false
}

// SyncOutlets pulls the tenant's outlets from auth-api and upserts the property ones (and the HQ,
// which grants a tenant-wide view).
func (s *Syncer) SyncOutlets(ctx context.Context, tenantID uuid.UUID, tenantSlug string) error {
	url := s.authURL + "/api/v1/tenants/" + tenantSlug + "/outlets"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := s2sHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("tenant: GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("tenant: outlets HTTP %d", resp.StatusCode)
	}
	var items []authOutlet
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return fmt.Errorf("tenant: decode outlets: %w", err)
	}
	for _, it := range items {
		// Only property outlets and the HQ, which grants a tenant-wide view. A shop or restaurant
		// outlet in the same tenant is never projected here.
		if it.Status == "archived" || (!it.IsHQ && !isMaskaniOutlet(it)) {
			continue
		}
		id, err := uuid.Parse(it.ID)
		if err != nil {
			continue
		}
		status := it.Status
		if status == "" {
			status = "active"
		}
		q := s.client.Outlet.Create().SetID(id).SetTenantID(tenantID).SetTenantSlug(tenantSlug).
			SetCode(it.Code).SetName(it.Name).SetIsHq(it.IsHQ).SetStatus(status)
		if it.UseCase != "" {
			q.SetUseCase(it.UseCase)
		}
		if it.Address != "" {
			q.SetAddressJSON(map[string]any{"street": it.Address})
		}
		if err := q.OnConflict().UpdateNewValues().Exec(ctx); err != nil {
			s.log.Warn("outlet upsert failed", zap.String("code", it.Code), zap.Error(err))
		}
	}
	return nil
}
