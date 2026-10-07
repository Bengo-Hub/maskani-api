// Command hashmigrations recomputes atlas.sum for the versioned migration directory offline (no
// database, no atlas CLI). Run after hand-adding a migration: go run ./cmd/hashmigrations
package main

import (
	"log"

	atlasmigrate "ariga.io/atlas/sql/migrate"
)

func main() {
	dir, err := atlasmigrate.NewLocalDir("internal/ent/migrate/migrations")
	if err != nil {
		log.Fatalf("open migrations dir: %v", err)
	}
	sum, err := dir.Checksum()
	if err != nil {
		log.Fatalf("compute checksum: %v", err)
	}
	if err := atlasmigrate.WriteSumFile(dir, sum); err != nil {
		log.Fatalf("write atlas.sum: %v", err)
	}
	log.Println("atlas.sum regenerated")
}
