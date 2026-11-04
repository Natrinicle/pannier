package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"pannier.dev/internal/config"
	"pannier.dev/internal/engine"
	"pannier.dev/internal/pipeline"
	"pannier.dev/internal/server"
	"pannier.dev/web"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:3477", "loopback listen address")
	cfgPath := flag.String("config", "", "YAML config path")
	dataDir := flag.String("data", "", "state directory (default $HOME/.var/pannier)")
	run := flag.String("run", "", "oneshot job: backup or packcopy, then exit (no HTTP, no token on argv)")
	flag.Parse()

	dir := *dataDir
	if dir == "" {
		dir = os.Getenv("PANNIER_DATA_DIR")
	}
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".var", "pannier")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Fatal(err)
	}

	path := *cfgPath
	if path == "" {
		path = filepath.Join(dir, "pannier.yaml")
	}
	cfg, err := config.Load(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Fatal(err)
		}
		cfg = config.Default()
		cfg.DataDir = dir
		if err := config.Write(path, cfg); err != nil {
			log.Fatal(err)
		}
		log.Printf("wrote default config %s (engine=%s)", path, cfg.Engine)
	}
	if cfg.DataDir == "" {
		cfg.DataDir = dir
	}
	if *listen != "127.0.0.1:3477" {
		cfg.Listen = *listen
	}
	if cfg.Listen == "" {
		cfg.Listen = "127.0.0.1:3477"
	}
	if err := cfg.ValidateCell(); err != nil {
		log.Fatalf("cell process: %v", err)
	}

	eng, err := engine.New(cfg.Engine, engine.ExecRunner{})
	if err != nil {
		log.Fatal(err)
	}

	if *run != "" {
		if err := runOneshot(*run, cfg, eng); err != nil {
			log.Fatal(err)
		}
		return
	}

	if err := server.ListenAddr(cfg.Listen); err != nil {
		log.Fatal(err)
	}

	pw, generated, err := server.LoadOrCreatePassword(dir, os.Getenv("PANNIER_PASSWORD"))
	if err != nil {
		log.Fatal(err)
	}
	if generated {
		fmt.Fprintf(os.Stdout, "pannierd: generated console password (also %s/console.password): %s\n", dir, pw)
	}

	srv := &server.Server{
		Cfg:      cfg,
		CfgPath:  path,
		DataDir:  dir,
		Password: pw,
		Eng:      eng,
		Web:      web.FS,
		Bags:     server.LoadBags(dir),
	}
	hs := &http.Server{
		Addr:              cfg.Listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("pannierd loopback %s engine=%s", cfg.Listen, cfg.Engine)
	log.Fatal(hs.ListenAndServe())
}

func runOneshot(kind string, cfg config.File, eng engine.Engine) error {
	ctx := context.Background()
	switch kind {
	case "backup":
		pipe := pipeline.Pipeline{Cfg: cfg, Eng: eng, Logf: log.Printf}
		_, err := pipe.BackupAll(ctx)
		return err
	case "packcopy":
		for _, s := range cfg.AllSatchels() {
			sub := s.Apply(cfg)
			repo := sub.Repo()
			for _, rem := range sub.Remotes {
				if _, err := eng.PackCopy(ctx, engine.PackCopyIn{
					Repo: repo,
					Dest: engine.ObjectDest{
						Endpoint: rem.Endpoint, Bucket: rem.Bucket, Prefix: rem.Prefix,
						Region: rem.Region, DisableTLS: rem.DisableTLS,
						ObjectLock: rem.ObjectLock, LiveRepo: rem.LiveRepo,
						Role: engine.RoleIngest,
					},
				}); err != nil {
					return fmt.Errorf("pack-copy %s/%s: %w", s.Name, rem.Name, err)
				}
			}
		}
		return nil
	default:
		return fmt.Errorf("unknown -run %q (want backup or packcopy)", kind)
	}
}
