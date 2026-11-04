package pipeline

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"pannier.dev/internal/config"
	"pannier.dev/internal/engine"
)

func (p *Pipeline) selfInclude(ctx context.Context, repo engine.Repo) (engine.Snapshot, error) {
	red := config.Redact(p.Cfg)
	body, err := yaml.Marshal(red)
	if err != nil {
		return engine.Snapshot{}, err
	}
	if bytes.Contains(bytes.ToLower(body), []byte("wjalr")) {
		return engine.Snapshot{}, fmt.Errorf("refusing self-include: secret-shaped bytes survived redact")
	}
	dir := filepath.Join(p.dataDir(), "self-include")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return engine.Snapshot{}, err
	}
	path := filepath.Join(dir, "pannier.config.yaml")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return engine.Snapshot{}, err
	}
	return p.Eng.Snapshot(ctx, engine.SnapshotIn{
		Repo:  repo,
		Paths: []string{path},
		Tags:  p.tags("pannier.self", "self"),
		Host:  hostname(),
	})
}
