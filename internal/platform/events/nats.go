package events

import (
	"fmt"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bengobox/maskani-api/internal/config"
)

// Connect opens a resilient NATS connection (infinite reconnect).
func Connect(cfg config.EventsConfig) (*nats.Conn, error) {
	return nats.Connect(cfg.NATSURL,
		nats.Name("maskani-api"),
		nats.Timeout(5*time.Second),
		nats.ReconnectWait(2*time.Second),
		nats.MaxReconnects(-1),
	)
}

// EnsureStream creates or updates the maskani JetStream stream carrying every maskani.* event.
func EnsureStream(nc *nats.Conn, cfg config.EventsConfig) error {
	if nc == nil {
		return fmt.Errorf("nats connection is nil")
	}
	js, err := nc.JetStream()
	if err != nil {
		return fmt.Errorf("jetstream init: %w", err)
	}
	desired := []string{"maskani.>"}
	info, err := js.StreamInfo(cfg.StreamName)
	if err == nil {
		if len(info.Config.Subjects) != 1 || info.Config.Subjects[0] != desired[0] {
			info.Config.Subjects = desired
			if _, err := js.UpdateStream(&info.Config); err != nil {
				return fmt.Errorf("update stream subjects: %w", err)
			}
		}
		return nil
	}
	_, err = js.AddStream(&nats.StreamConfig{
		Name:      cfg.StreamName,
		Subjects:  desired,
		Replicas:  1,
		Retention: nats.LimitsPolicy,
		MaxAge:    7 * 24 * time.Hour,
	})
	return err
}
