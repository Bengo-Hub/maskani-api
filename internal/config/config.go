package config

import (
	"fmt"
	"time"

	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
)

// Config aggregates runtime configuration. Variables carry no prefix, matching the fleet.
type Config struct {
	App      AppConfig
	HTTP     HTTPConfig
	Postgres PostgresConfig
	Redis    RedisConfig
	Events   EventsConfig
	Auth     AuthConfig
	Services ServicesConfig
	Media    MediaConfig
	Security SecurityConfig
}

type AppConfig struct {
	Name     string `envconfig:"APP_NAME" default:"maskani-api"`
	Env      string `envconfig:"APP_ENV" default:"development"`
	Version  string `envconfig:"APP_VERSION" default:"0.1.0"`
	Timezone string `envconfig:"APP_TIMEZONE" default:"Africa/Nairobi"`
}

type HTTPConfig struct {
	Host           string        `envconfig:"HTTP_HOST" default:"0.0.0.0"`
	Port           int           `envconfig:"HTTP_PORT" default:"4000"`
	ReadTimeout    time.Duration `envconfig:"HTTP_READ_TIMEOUT" default:"20s"`
	WriteTimeout   time.Duration `envconfig:"HTTP_WRITE_TIMEOUT" default:"60s"`
	IdleTimeout    time.Duration `envconfig:"HTTP_IDLE_TIMEOUT" default:"90s"`
	PublicBaseURL  string        `envconfig:"HTTP_PUBLIC_BASE_URL" default:"https://maskaniapi.codevertexafrica.com"`
	AllowedOrigins []string      `envconfig:"HTTP_ALLOWED_ORIGINS" default:"https://maskaniapp.codevertexafrica.com,https://maskani.codevertexafrica.com,https://accounts.codevertexafrica.com"`
}

type PostgresConfig struct {
	URL              string        `envconfig:"POSTGRES_URL" default:"postgres://postgres:postgres@localhost:5432/maskani?sslmode=disable"`
	MigrateURL       string        `envconfig:"POSTGRES_MIGRATE_URL" default:""`
	ReadOnlyURL      string        `envconfig:"POSTGRES_READONLY_URL" default:""`
	MaxOpenConns     int           `envconfig:"POSTGRES_MAX_OPEN_CONNS" default:"8"`
	MaxIdleConns     int           `envconfig:"POSTGRES_MAX_IDLE_CONNS" default:"4"`
	ConnMaxLifetime  time.Duration `envconfig:"POSTGRES_CONN_MAX_LIFETIME" default:"15m"`
	StatementTimeout time.Duration `envconfig:"POSTGRES_STATEMENT_TIMEOUT" default:"30s"`
}

type RedisConfig struct {
	Addr        string        `envconfig:"REDIS_ADDR" default:"localhost:6379"`
	Username    string        `envconfig:"REDIS_USERNAME"`
	Password    string        `envconfig:"REDIS_PASSWORD"`
	DB          int           `envconfig:"REDIS_DB" default:"0"`
	TLSRequired bool          `envconfig:"REDIS_TLS_REQUIRED" default:"false"`
	DialTimeout time.Duration `envconfig:"REDIS_DIAL_TIMEOUT" default:"5s"`
}

// EventsConfig wires the maskani JetStream stream; subjects are maskani.{event_type}.
type EventsConfig struct {
	NATSURL          string        `envconfig:"EVENTS_NATS_URL" default:"nats://localhost:4222"`
	StreamName       string        `envconfig:"NATS_STREAM" default:"maskani"`
	DeliverGroup     string        `envconfig:"NATS_DELIVER_GROUP" default:"maskani-workers"`
	OutboxEnabled    bool          `envconfig:"EVENTS_OUTBOX_ENABLED" default:"true"`
	OutboxBatchSize  int           `envconfig:"EVENTS_OUTBOX_BATCH_SIZE" default:"100"`
	OutboxPollPeriod time.Duration `envconfig:"EVENTS_OUTBOX_POLL_PERIOD" default:"2s"`
}

// AuthConfig validates SSO JWTs through auth-api JWKS and identifies S2S callers by API key.
type AuthConfig struct {
	ServiceURL          string        `envconfig:"AUTH_SERVICE_URL" default:"https://sso.codevertexafrica.com"`
	APIURL              string        `envconfig:"AUTH_API_URL" default:"https://sso.codevertexafrica.com"`
	Issuer              string        `envconfig:"AUTH_ISSUER" default:"https://sso.codevertexafrica.com"`
	Audience            string        `envconfig:"AUTH_AUDIENCE" default:"codevertex"`
	JWKSUrl             string        `envconfig:"AUTH_JWKS_URL" default:"https://sso.codevertexafrica.com/api/v1/.well-known/jwks.json"`
	JWKSCacheTTL        time.Duration `envconfig:"AUTH_JWKS_CACHE_TTL" default:"3600s"`
	JWKSRefreshInterval time.Duration `envconfig:"AUTH_JWKS_REFRESH_INTERVAL" default:"300s"`
	EnableAPIKeyAuth    bool          `envconfig:"AUTH_ENABLE_API_KEY_AUTH" default:"true"`
	APIKey              string        `envconfig:"INTERNAL_SERVICE_KEY" default:""`
}

// ServicesConfig holds S2S base URLs of the services maskani references by ID.
type ServicesConfig struct {
	TreasuryURL      string `envconfig:"TREASURY_SERVICE_URL" default:"https://booksapi.codevertexafrica.com"`
	ERPURL           string `envconfig:"ERP_SERVICE_URL" default:"https://erpapi.codevertexafrica.com"`
	NotificationsURL string `envconfig:"NOTIFICATIONS_SERVICE_URL" default:"https://notificationsapi.codevertexafrica.com"`
	SubscriptionsURL string `envconfig:"SUBSCRIPTION_BASE_URL" default:"https://pricingapi.codevertexafrica.com"`
}

type MediaConfig struct {
	Root    string `envconfig:"MEDIA_ROOT" default:"./media"`
	URLBase string `envconfig:"MEDIA_URL_BASE" default:""`
	MaxMB   int    `envconfig:"MEDIA_MAX_MB" default:"8"`
}

// SecurityConfig carries keys for field encryption, phone hashing and signed media links. Each
// falls back to INTERNAL_SERVICE_KEY only in development so a local run starts without extra setup.
type SecurityConfig struct {
	FieldEncryptionKey string `envconfig:"FIELD_ENCRYPTION_KEY" default:""`
	MediaSigningSecret string `envconfig:"MEDIA_SIGNING_SECRET" default:""`
	GateTokenSecret    string `envconfig:"GATE_TOKEN_SECRET" default:""`
}

// Load reads configuration from the environment and an optional .env file.
func Load() (*Config, error) {
	_ = godotenv.Load()
	var cfg Config
	if err := envconfig.Process("", &cfg); err != nil {
		return nil, fmt.Errorf("config: failed to load environment variables: %w", err)
	}
	if cfg.Postgres.ReadOnlyURL == "" {
		cfg.Postgres.ReadOnlyURL = cfg.Postgres.URL
	}
	if cfg.Security.MediaSigningSecret == "" {
		cfg.Security.MediaSigningSecret = cfg.Auth.APIKey
	}
	if cfg.Security.GateTokenSecret == "" {
		cfg.Security.GateTokenSecret = cfg.Auth.APIKey
	}
	if cfg.Security.FieldEncryptionKey == "" && cfg.App.Env != "production" {
		cfg.Security.FieldEncryptionKey = cfg.Auth.APIKey
	}
	return &cfg, nil
}
