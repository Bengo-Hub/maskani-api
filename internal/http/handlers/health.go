package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"

	"github.com/bengobox/maskani-api/internal/http/httpx"
)

type pinger interface{ Ping(context.Context) error }

// Health serves liveness, readiness and metrics.
type Health struct {
	DB     pinger
	Cache  *redis.Client
	Events *nats.Conn
}

// Liveness reports the process is up.
func (h *Health) Liveness(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "maskani-api"})
}

// Readiness checks Postgres, Redis and NATS.
func (h *Health) Readiness(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	issues := map[string]string{}
	if h.DB != nil {
		if err := h.DB.Ping(ctx); err != nil {
			issues["postgres"] = err.Error()
		}
	}
	if h.Cache != nil {
		if err := h.Cache.Ping(ctx).Err(); err != nil {
			issues["redis"] = err.Error()
		}
	}
	if h.Events != nil && !h.Events.IsConnected() {
		issues["nats"] = "not connected"
	}
	status := http.StatusOK
	if issues["postgres"] != "" {
		status = http.StatusServiceUnavailable
	}
	httpx.JSON(w, status, map[string]any{"status": http.StatusText(status), "dependencies": issues})
}

// Metrics exposes Prometheus metrics.
func (h *Health) Metrics(w http.ResponseWriter, r *http.Request) { promhttp.Handler().ServeHTTP(w, r) }
