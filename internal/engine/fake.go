package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Fake is an in-memory engine so the console and API can run without
// kopia/restic/borg binaries. It is not a backup.
type Fake struct {
	mu       sync.Mutex
	byRepo   map[string]*fakeRepo
	inited   bool
	lastCopy PackCopyResult
	forgetN  int
	restored []RestoreIn
}

type fakeRepo struct {
	snaps []Snapshot
	packs []Pack
}

func NewFake() *Fake {
	return &Fake{byRepo: map[string]*fakeRepo{}}
}

func repoKey(path string) string {
	if path == "" {
		return "_"
	}
	return path
}

func (f *Fake) bucketLocked(path string) *fakeRepo {
	if f.byRepo == nil {
		f.byRepo = map[string]*fakeRepo{}
	}
	k := repoKey(path)
	b := f.byRepo[k]
	if b == nil {
		b = &fakeRepo{packs: []Pack{{Key: "p-demo", RelPath: "p-demo", Size: 42}}}
		f.byRepo[k] = b
	}
	return b
}

func (*Fake) Kind() Kind { return FakeKind }

func (f *Fake) Init(_ context.Context, _ Repo) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inited = true
	return nil
}

func (f *Fake) Snapshot(_ context.Context, in SnapshotIn) (Snapshot, error) {
	s := Snapshot{
		ID:    fmt.Sprintf("fake-%d", time.Now().UnixNano()),
		Time:  time.Now().UTC(),
		Host:  in.Host,
		Paths: append([]string(nil), in.Paths...),
		Tags:  cloneTags(in.Tags),
	}
	f.mu.Lock()
	b := f.bucketLocked(in.Repo.Path)
	b.snaps = append(b.snaps, s)
	f.mu.Unlock()
	return s, nil
}

func (f *Fake) SnapshotStdin(_ context.Context, in StdinIn, _ []string) (Snapshot, error) {
	s := Snapshot{
		ID:    fmt.Sprintf("fake-stdin-%d", time.Now().UnixNano()),
		Time:  time.Now().UTC(),
		Host:  in.Host,
		Paths: []string{in.Filename},
		Tags:  cloneTags(in.Tags),
	}
	f.mu.Lock()
	b := f.bucketLocked(in.Repo.Path)
	b.snaps = append(b.snaps, s)
	f.mu.Unlock()
	return s, nil
}

func (f *Fake) List(_ context.Context, repo Repo) ([]Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b := f.bucketLocked(repo.Path)
	out := make([]Snapshot, len(b.snaps))
	copy(out, b.snaps)
	return out, nil
}

func (f *Fake) Restore(_ context.Context, in RestoreIn) error {
	if in.Target != "" {
		if err := os.MkdirAll(in.Target, 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(in.Target, "RESTORED"), []byte(in.ID+"\n"), 0o600); err != nil {
			return err
		}
	}
	f.mu.Lock()
	f.restored = append(f.restored, in)
	f.mu.Unlock()
	return nil
}

func (f *Fake) Restored() []RestoreIn {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]RestoreIn, len(f.restored))
	copy(out, f.restored)
	return out
}

func (f *Fake) Check(_ context.Context, _ CheckIn) error { return nil }

func (f *Fake) ListPacks(_ context.Context, repo Repo) ([]Pack, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b := f.bucketLocked(repo.Path)
	out := make([]Pack, len(b.packs))
	copy(out, b.packs)
	return out, nil
}

func (f *Fake) PackCopy(_ context.Context, in PackCopyIn) (PackCopyResult, error) {
	if in.Dest.ObjectLock || !in.Dest.LiveRepo {
		f.mu.Lock()
		defer f.mu.Unlock()
		b := f.bucketLocked(in.Repo.Path)
		res := PackCopyResult{ListedSrc: len(b.packs), Put: len(b.packs)}
		for _, p := range b.packs {
			res.Bytes += p.Size
		}
		f.lastCopy = res
		return res, nil
	}
	return PackCopyResult{}, nil
}

func (f *Fake) ForgetPrune(_ context.Context, in ForgetIn) error {
	if in.Role == RoleIngest {
		return ErrIngestMustNotMaintain
	}
	f.mu.Lock()
	f.forgetN++
	f.mu.Unlock()
	return nil
}

func cloneTags(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
