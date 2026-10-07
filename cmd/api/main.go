package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"github.com/bengobox/maskani-api/internal/app"
)

// @title Maskani API
// @version 0.1.0
// @description Multi-tenant property management API for Maskani by Codevertex: properties, units, parties, billing runs, utilities, unit sales, works, vendors, gate and visitors. Money moves only through treasury-api.
// @BasePath /api/v1
// @schemes http https
// @securityDefinitions.apikey bearerAuth
// @in header
// @name Authorization
// @description JWT from auth-api. Format: Bearer {token}
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	a, err := app.New(ctx)
	if err != nil {
		log.Fatalf("failed to initialise app: %v", err)
	}
	defer a.Close()

	if err := a.Run(ctx); err != nil {
		log.Fatalf("runtime error: %v", err)
	}
}
