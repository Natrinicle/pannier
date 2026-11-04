package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"pannier.dev/internal/config"
	"pannier.dev/internal/engine"
)

func main() {
	cfgPath := flag.String("config", "", "YAML config path (must include maintenanceCreds)")
	dataDir := flag.String("data", "", "state directory")
	flag.Parse()

	dir := *dataDir
	if dir == "" {
		dir = os.Getenv("PANNIER_DATA_DIR")
	}
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".var", "pannier-maint")
	}
	path := *cfgPath
	if path == "" {
		path = filepath.Join(dir, "pannier.yaml")
	}
	cfg, err := config.Load(path)
	if err != nil {
		log.Fatal(err)
	}

	// Refuses cell ingest-only YAML: that file has no Steward keys on
	// purpose, and this binary must not invent them.
	if err := cfg.ValidateCell(); err == nil {
		log.Fatal("refusing: config looks like a cell ingest-only file (no maintenanceCreds)")
	}
	if !cfg.HasMaintenanceCreds() {
		log.Fatal("refusing: maintenance credentials are required")
	}

	eng, err := engine.New(cfg.Engine, engine.ExecRunner{})
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()
	if err := eng.ForgetPrune(ctx, engine.ForgetIn{
		Repo:        cfg.Repo(),
		KeepLast:    cfg.Retention.KeepLast,
		KeepDaily:   cfg.Retention.KeepDaily,
		KeepWeekly:  cfg.Retention.KeepWeekly,
		KeepMonthly: cfg.Retention.KeepMonthly,
		KeepYearly:  cfg.Retention.KeepYearly,
		KeepWithin:  cfg.Retention.KeepWithin,
		Prune:       true,
		Role:        engine.RoleGC,
	}); err != nil {
		log.Fatal(err)
	}
	fmt.Fprintln(os.Stdout, "pannier-maint: forget/prune finished")
}
