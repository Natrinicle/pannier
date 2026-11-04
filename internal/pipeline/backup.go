package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"pannier.dev/internal/config"
	"pannier.dev/internal/engine"
	"pannier.dev/internal/source"
)

type Logf func(string, ...any)

type Pipeline struct {
	Cfg     config.File
	Eng     engine.Engine
	Logf    Logf
	runID   string
	satchel string
}

type Result struct {
	ID        string
	Started   time.Time
	Snapshots []engine.Snapshot
	DumpErrs  []error
	PackCopy  engine.PackCopyResult
	Contents  []Item
	Satchel   string
	Node      string
}

func (res Result) Bag(engineKind engine.Kind, sites int) Bag {
	contents := res.Contents
	if contents == nil {
		contents = []Item{}
	}
	started := ""
	if !res.Started.IsZero() {
		started = res.Started.UTC().Format(time.RFC3339)
	}
	satchel := res.Satchel
	if satchel == "" {
		satchel = config.HouseName
	}
	return Bag{
		ID:       res.ID,
		Started:  started,
		Contents: contents,
		Engine:   string(engineKind),
		Sites:    sites,
		Parcels:  len(contents),
		Satchel:  satchel,
		Node:     res.Node,
	}
}

func (p *Pipeline) log(format string, args ...any) {
	if p.Logf != nil {
		p.Logf(format, args...)
	}
}

func (p *Pipeline) Backup(ctx context.Context) (Result, error) {
	unlock, err := lockDir(p.dataDir())
	if err != nil {
		return Result{}, err
	}
	defer unlock()
	return p.backupLocked(ctx, p.satchelName())
}

// BackupAll runs the shared house engine first, then each tenant
// satchel into its own repository. One lock. A tenant failure does
// not throw away a house bag that already finished.
func (p *Pipeline) BackupAll(ctx context.Context) ([]Result, error) {
	unlock, err := lockDir(p.dataDir())
	if err != nil {
		return nil, err
	}
	defer unlock()
	if p.Logf == nil {
		p.Logf = func(string, ...any) {}
	}
	var all []Result
	var firstErr error
	for _, s := range p.Cfg.AllSatchels() {
		if err := s.Validate(); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			p.log("satchel %s skipped: %v", s.Name, err)
			continue
		}
		sub := *p
		sub.Cfg = s.Apply(p.Cfg)
		sub.satchel = s.Name
		res, err := sub.backupLocked(ctx, s.Name)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("satchel %s: %w", s.Name, err)
			}
			p.log("satchel %s: %v", s.Name, err)
			continue
		}
		all = append(all, res)
	}
	if len(all) == 0 && firstErr != nil {
		return all, firstErr
	}
	return all, firstErr
}

func (p *Pipeline) satchelName() string {
	if p.satchel != "" {
		return p.satchel
	}
	if p.Cfg.Role == config.RoleTenant {
		return "tenant"
	}
	return config.HouseName
}

func (p *Pipeline) backupLocked(ctx context.Context, satchel string) (Result, error) {
	var res Result
	if p.Logf == nil {
		p.Logf = func(string, ...any) {}
	}
	res.Started = time.Now().UTC()
	res.Satchel = satchel
	if res.Satchel == "" {
		res.Satchel = config.HouseName
	}
	res.Node = hostname()
	res.ID = "bag-" + slug(res.Satchel) + "-" + res.Started.Format("20060102T150405Z")
	p.runID = res.ID
	p.satchel = res.Satchel

	if err := p.Cfg.ValidateIngestOnly(); err != nil {
		return res, fmt.Errorf("cell must not hold Steward keys: %w", err)
	}

	repo := p.Cfg.Repo()
	if err := p.Eng.Init(ctx, repo); err != nil {
		return res, fmt.Errorf("engine init: %w", err)
	}

	// 2 discover — docker/lxc live list, plus include names as markers.
	parcels, err := p.discover(ctx)
	if err != nil {
		return res, err
	}
	p.log("discover: %d parcels", len(parcels))

	// 3 self-include first — restore of pannier itself must be in the bag
	// before the rest of the tree is snapshotted.
	if p.Cfg.SelfInclude {
		s, err := p.selfInclude(ctx, repo)
		if err != nil {
			return res, fmt.Errorf("self-include: %w", err)
		}
		res.Snapshots = append(res.Snapshots, s)
	}

	// 4 dump-first SQL — failure fails that db.* entry only, not the run.
	dumpSnaps, dumpErrs := p.dumpAll(ctx, repo)
	res.Snapshots = append(res.Snapshots, dumpSnaps...)
	res.DumpErrs = dumpErrs

	// 5 docker / lxc — one snapshot per instance. Failure is that entry.
	res.Snapshots = append(res.Snapshots, p.snapshotInstances(ctx, repo, parcels)...)

	role := p.Cfg.Role
	if role == "" {
		role = config.RoleHouse
	}
	// 6–7 k8s pvc / etcd — house cluster scope only. Tenant copies never
	// receive the etcd keyspace.
	if p.Cfg.Selection.IncludeControlPlane(role) {
		if err := p.stubK8sPVC(ctx); err != nil {
			p.log("k8s.pvc stub: %v", err)
		}
		if err := p.stubK8sEtcd(ctx, repo); err != nil {
			p.log("k8s.etcd stub: %v", err)
		}
		_ = source.ControlPlanePlan()
	}

	// 8 filesystem — bare metal / selected host paths.
	if paths := hostPaths(parcels); p.Cfg.Selection.BareMetal && len(paths) > 0 {
		s, err := p.Eng.Snapshot(ctx, engine.SnapshotIn{
			Repo:  repo,
			Paths: paths,
			Host:  hostname(),
			Tags:  p.tags("host.path", "host"),
		})
		if err != nil {
			return res, fmt.Errorf("filesystem snapshot: %w", err)
		}
		res.Snapshots = append(res.Snapshots, s)
	}

	// 9 local snapshot + first verify
	if err := p.Eng.Check(ctx, engine.CheckIn{Repo: repo, ReadPercent: 1, MaxDurationSec: 300}); err != nil {
		return res, fmt.Errorf("first verify: %w", err)
	}

	// 10 pack-copy ingest — Porter keys, list+put, never delete.
	for _, rem := range p.Cfg.Remotes {
		if rem.LiveRepo && !rem.ObjectLock {
			continue
		}
		dest, err := rem.ObjectDest()
		if err != nil {
			return res, fmt.Errorf("pack-copy %s creds: %w", rem.Name, err)
		}
		pc, err := p.Eng.PackCopy(ctx, engine.PackCopyIn{Repo: repo, Dest: dest})
		if err != nil {
			return res, fmt.Errorf("pack-copy %s: %w", rem.Name, err)
		}
		res.PackCopy = pc
	}

	// 11 optional live remote — only when the dest is a real engine repo.
	for _, rem := range p.Cfg.Remotes {
		if !rem.LiveRepo || rem.ObjectLock {
			continue
		}
		dest, err := rem.ObjectDest()
		if err != nil {
			return res, fmt.Errorf("live remote %s creds: %w", rem.Name, err)
		}
		if _, err := p.Eng.PackCopy(ctx, engine.PackCopyIn{Repo: repo, Dest: dest}); err != nil {
			return res, fmt.Errorf("live remote %s: %w", rem.Name, err)
		}
	}

	// 12 local retention — local repo only; RoleIngest still forbids GC of
	// the object-lock prefix. Expire on the local filesystem repo is OK.
	if err := p.Eng.ForgetPrune(ctx, engine.ForgetIn{
		Repo:        repo,
		KeepLast:    p.Cfg.Retention.KeepLast,
		KeepDaily:   p.Cfg.Retention.KeepDaily,
		KeepWeekly:  p.Cfg.Retention.KeepWeekly,
		KeepMonthly: p.Cfg.Retention.KeepMonthly,
		KeepYearly:  p.Cfg.Retention.KeepYearly,
		KeepWithin:  p.Cfg.Retention.KeepWithin,
		Prune:       p.Cfg.Retention.Prune,
		Role:        engine.RoleGC, // local repo, not the off-side bag
	}); err != nil {
		return res, fmt.Errorf("local retention: %w", err)
	}

	// 13 scheduled check — deeper than the first-verify percent.
	if err := p.Eng.Check(ctx, engine.CheckIn{Repo: repo, ReadPercent: 10, MaxDurationSec: 3600}); err != nil {
		return res, fmt.Errorf("scheduled check: %w", err)
	}

	// 14 start-test — restore into an isolated namespace (deny egress).
	if err := p.stubStartTest(ctx); err != nil {
		p.log("start-test stub: %v", err)
	}

	// 15 maint GC never on cell — even if YAML was edited by hand.
	if err := p.Cfg.ValidateIngestOnly(); err != nil {
		return res, fmt.Errorf("cell must not hold Steward keys: %w", err)
	}

	res.Contents = AttachSnapshots(CatalogWith(p.Cfg, parcels), res.Snapshots)
	return res, nil
}

func (p *Pipeline) tags(lane, entry string) map[string]string {
	m := map[string]string{"lane": lane, "satchel": p.satchelName(), "node": hostname()}
	if entry != "" {
		m["entry"] = entry
	}
	if p.runID != "" {
		m["run"] = p.runID
	}
	return m
}

func (p *Pipeline) discover(ctx context.Context) ([]source.Instance, error) {
	found := source.Pick(ctx, p.Cfg.Selection)
	if p.Cfg.Selection.BareMetal {
		paths := append([]string(nil), p.Cfg.Selection.HostPaths...)
		if len(paths) == 0 {
			paths = []string{"/etc"}
		}
		found = append(found, source.Instance{
			Kind:  source.KindHostPath,
			ID:    "host",
			Name:  "Host Paths",
			Paths: paths,
			Note:  "This node only (" + hostname() + ").",
			Node:  hostname(),
		})
	}
	if p.Cfg.Selection.K8s {
		// Workloads/PVCs stay a compile stub; etcd is a sequence, not discover.
		_ = ctx
	}
	return found, nil
}

func (p *Pipeline) snapshotInstances(ctx context.Context, repo engine.Repo, found []source.Instance) []engine.Snapshot {
	var snaps []engine.Snapshot
	for _, in := range found {
		if in.Kind != source.KindDockerContainer && in.Kind != source.KindLXCInstance {
			continue
		}
		paths := append([]string(nil), in.Paths...)
		if len(paths) == 0 {
			mp, err := p.marker(in)
			if err != nil {
				p.log("%s %s marker: %v", in.Kind, in.ID, err)
				continue
			}
			paths = []string{mp}
		}
		s, err := p.Eng.Snapshot(ctx, engine.SnapshotIn{
			Repo:  repo,
			Paths: paths,
			Host:  hostname(),
			Tags:  p.tags(in.Kind, in.ID),
		})
		if err != nil {
			p.log("%s %s: %v", in.Kind, in.ID, err)
			continue
		}
		snaps = append(snaps, s)
	}
	return snaps
}

func (p *Pipeline) marker(in source.Instance) (string, error) {
	dir := filepath.Join(p.dataDir(), "markers")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	name := strings.ReplaceAll(in.ID, ":", "_")
	path := filepath.Join(dir, name)
	body := in.Name + "\n" + in.Kind + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func (p *Pipeline) stubK8sPVC(ctx context.Context) error {
	// TODO: snapshot each k8s.pvc (volume snapshot or mount + engine).
	_ = ctx
	return nil
}

func (p *Pipeline) stubK8sEtcd(ctx context.Context, repo engine.Repo) error {
	// TODO: exec ControlPlanePlan steps on the control-plane node only.
	_ = ctx
	_ = repo
	return nil
}

func (p *Pipeline) stubStartTest(ctx context.Context) error {
	// TODO: restore a canary into namespace restore-start-test which has
	// default-deny egress (see deploy/k8s/networkpolicy-deny-egress.yaml).
	_ = ctx
	return nil
}

func (p *Pipeline) dataDir() string {
	if p.Cfg.DataDir != "" {
		return p.Cfg.DataDir
	}
	return os.TempDir()
}

func hostPaths(found []source.Instance) []string {
	var paths []string
	for _, in := range found {
		if in.Kind == source.KindHostPath {
			paths = append(paths, in.Paths...)
		}
	}
	return paths
}

func hostname() string {
	h, _ := os.Hostname()
	return h
}

func slug(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return config.HouseName
	}
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('-')
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return config.HouseName
	}
	return out
}

func lockDir(dir string) (func(), error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "backup.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("backup already running: %w", err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

// LastRun is a tiny stamp the API can show. Not a database.
func Stamp(dir, name string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name), []byte(time.Now().UTC().Format(time.RFC3339)), 0o600)
}
