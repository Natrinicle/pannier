package engine

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type recRunner struct {
	mu    sync.Mutex
	calls [][]string
	out   []byte
}

func (r *recRunner) Run(_ context.Context, _ io.Reader, _ []string, argv ...string) ([]byte, error) {
	cp := append([]string(nil), argv...)
	r.mu.Lock()
	r.calls = append(r.calls, cp)
	r.mu.Unlock()
	if r.out != nil {
		return r.out, nil
	}
	return []byte(`{"id":"snap1","startTime":"2026-01-02T03:04:05Z"}`), nil
}

func (r *recRunner) joined() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var b strings.Builder
	for _, c := range r.calls {
		b.WriteString(strings.Join(c, " "))
		b.WriteByte('\n')
	}
	return b.String()
}

func (r *recRunner) has(needle string) bool {
	return strings.Contains(r.joined(), needle)
}

func writePass(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "pw")
	if err := os.WriteFile(p, []byte("s3cret-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestKopiaSnapshotArgv(t *testing.T) {
	pw := writePass(t)
	rec := &recRunner{}
	k := KopiaEngine{R: rec}
	in := SnapshotIn{
		Repo:  Repo{Kind: Kopia, Path: "/repo", PasswordFile: pw, Binary: "kopia"},
		Paths: []string{"/data/app"},
		Tags:  map[string]string{"lane": "host.path"},
		Host:  "cell-a",
	}
	if _, err := k.Snapshot(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	got := rec.joined()
	for _, want := range []string{"kopia", "-p", "s3cret-value", "snapshot", "create", "/data/app", "--tags", "lane:host.path"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in argv %q", want, got)
		}
	}
}

func TestKopiaSnapshotStdinArgv(t *testing.T) {
	pw := writePass(t)
	rec := &recRunner{}
	k := KopiaEngine{R: rec}
	in := StdinIn{
		Repo:     Repo{Kind: Kopia, Path: "/repo", PasswordFile: pw, Binary: "kopia"},
		Filename: "pg.dump",
		Host:     "cell-a",
	}
	if _, err := k.SnapshotStdin(context.Background(), in, nil); err != nil {
		t.Fatal(err)
	}
	got := rec.joined()
	if !strings.Contains(got, "--stdin-file") {
		t.Fatalf("expected --stdin-file in %q", got)
	}
	if !strings.Contains(got, "pg.dump") {
		t.Fatalf("expected source path pg.dump in %q", got)
	}
	if !strings.Contains(got, "snapshot create") {
		t.Fatalf("expected snapshot create in %q", got)
	}
}

func TestResticStdinFromCommandArgv(t *testing.T) {
	pw := writePass(t)
	rec := &recRunner{}
	e := ResticEngine{R: rec}
	in := StdinIn{
		Repo:     Repo{Kind: Restic, Path: "/repo", PasswordFile: pw, Binary: "restic"},
		Filename: "pg.dump",
		Command:  []string{"pg_dump", "app"},
		Host:     "cell-a",
	}
	if _, err := e.SnapshotStdin(context.Background(), in, nil); err != nil {
		t.Fatal(err)
	}
	got := rec.joined()
	for _, want := range []string{"restic", "--password-file", "--stdin-from-command", "--stdin-filename", "pg.dump", "pg_dump", "app"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in argv %q", want, got)
		}
	}
	if strings.Contains(got, "s3cret-value") {
		t.Fatal("restic must not splice password onto argv")
	}
}

func TestForgetPruneIngestRejected(t *testing.T) {
	pw := writePass(t)
	rec := &recRunner{}
	in := ForgetIn{
		Repo:     Repo{Path: "/repo", PasswordFile: pw, Binary: "kopia"},
		KeepLast: 7,
		Prune:    true,
		Role:     RoleIngest,
	}
	engines := []Engine{
		KopiaEngine{R: rec},
		ResticEngine{R: rec},
		BorgEngine{R: rec},
		NewFake(),
	}
	for _, e := range engines {
		if err := e.ForgetPrune(context.Background(), in); err != ErrIngestMustNotMaintain {
			t.Fatalf("%s: want ErrIngestMustNotMaintain, got %v", e.Kind(), err)
		}
	}
	if rec.has("forget") || rec.has("prune") || rec.has("expire") || rec.has("maintenance") {
		t.Fatalf("ingest ForgetPrune must not exec maintenance argv: %q", rec.joined())
	}
}

func TestKopiaPackCopyNoSyncToWhenNotLive(t *testing.T) {
	pw := writePass(t)
	rec := &recRunner{out: []byte("pabc 12\n")}
	k := KopiaEngine{R: rec}
	destDir := t.TempDir()
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "pabc"), []byte("pack-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := k.PackCopy(context.Background(), PackCopyIn{
		Repo: Repo{Kind: Kopia, Path: srcDir, PasswordFile: pw, Binary: "kopia"},
		Dest: ObjectDest{
			Endpoint:   "file://" + destDir,
			Bucket:     "bag",
			Prefix:     "cell/",
			ObjectLock: true,
			LiveRepo:   false,
			Role:       RoleIngest,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rec.has("sync-to") {
		t.Fatalf("ObjectLock/LiveRepo=false must not invoke repository sync-to; argv=%q", rec.joined())
	}
	if !rec.has("blob list") {
		t.Fatalf("expected blob list for pack inventory, argv=%q", rec.joined())
	}
}

func TestRedactArgv(t *testing.T) {
	in := []string{"kopia", "-p", "s3cret-value", "--password=also", "snapshot", "create"}
	got := strings.Join(RedactArgv(in), " ")
	if strings.Contains(got, "s3cret-value") || strings.Contains(got, "also") {
		t.Fatalf("password leaked: %q", got)
	}
	if in[2] != "s3cret-value" {
		t.Fatal("RedactArgv must copy, not mutate caller argv")
	}
}
