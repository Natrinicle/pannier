package pipeline

import (
	"context"
	"fmt"

	"pannier.dev/internal/engine"
)

func (p *Pipeline) dumpAll(ctx context.Context, repo engine.Repo) ([]engine.Snapshot, []error) {
	var snaps []engine.Snapshot
	var errs []error
	for _, d := range p.Cfg.Dumps {
		s, err := p.dumpOne(ctx, repo, d.Name, d.Filename, d.Command)
		if err != nil {
			errs = append(errs, fmt.Errorf("db.dump %s: %w", d.Name, err))
			p.log("dump %s failed (entry only): %v", d.Name, err)
			continue
		}
		snaps = append(snaps, s)
	}
	return snaps, errs
}

func (p *Pipeline) dumpOne(ctx context.Context, repo engine.Repo, name, filename string, command []string) (engine.Snapshot, error) {
	if filename == "" {
		filename = name
	}
	if len(command) == 0 {
		return engine.Snapshot{}, fmt.Errorf("empty dump command")
	}
	return p.Eng.SnapshotStdin(ctx, engine.StdinIn{
		Repo:     repo,
		Filename: filename,
		Command:  command,
		Tags:     p.tags("db.dump", name),
		Host:     hostname(),
	}, nil)
}
