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
	envs  [][]string
	out   []byte
}

func (r *recRunner) Run(_ context.Context, _ io.Reader, env []string, argv ...string) ([]byte, error) {
	cp := append([]string(nil), argv...)
	r.mu.Lock()
	r.calls = append(r.calls, cp)
	r.envs = append(r.envs, append([]string(nil), env...))
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

func TestKopiaCheckDownloadPercent(t *testing.T) {
	pw := writePass(t)
	rec := &recRunner{}
	k := KopiaEngine{R: rec}
	if err := k.Check(context.Background(), CheckIn{
		Repo:        Repo{Kind: Kopia, Path: t.TempDir(), PasswordFile: pw, Binary: "kopia"},
		ReadPercent: 1,
	}); err != nil {
		t.Fatal(err)
	}
	got := rec.joined()
	if !strings.Contains(got, "content verify") || !strings.Contains(got, "--download-percent") {
		t.Fatalf("want content verify --download-percent, got %q", got)
	}
	if strings.Contains(got, "--percent ") && !strings.Contains(got, "--download-percent") {
		t.Fatalf("kopia 0.23 uses --download-percent, argv=%q", got)
	}
}

func TestKopiaPackCopyNoSyncToWhenNotLive(t *testing.T) {
	pw := writePass(t)
	rec := &recRunner{out: []byte("pabc 12\n")}
	k := KopiaEngine{R: rec}
	destDir := t.TempDir()
	srcDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(srcDir, "p"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "p", "pabc"), []byte("pack-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "kopia.repository"), []byte("repo"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "scratch.tmp"), []byte("nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := k.PackCopy(context.Background(), PackCopyIn{
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
	if rec.has("blob list") {
		t.Fatalf("Locked Packs walks the repo directory, not blob-list IDs; argv=%q", rec.joined())
	}
	if res.Put < 2 {
		t.Fatalf("expected repo files copied, got %+v", res)
	}
	want := filepath.Join(destDir, "bag", "cell", "p", "pabc")
	b, err := os.ReadFile(want)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "pack-bytes" {
		t.Fatalf("dest %s: %q", want, b)
	}
	if _, err := os.Stat(filepath.Join(destDir, "bag", "cell", "scratch.tmp")); err == nil {
		t.Fatal("must not copy .tmp files")
	}
}

func TestKopiaPackCopyLiveRepoUsesEnvNotArgv(t *testing.T) {
	pw := writePass(t)
	rec := &recRunner{}
	k := KopiaEngine{R: rec}
	_, err := k.PackCopy(context.Background(), PackCopyIn{
		Repo: Repo{Kind: Kopia, Path: t.TempDir(), PasswordFile: pw, Binary: "kopia"},
		Dest: ObjectDest{
			Endpoint:  "minio:9000",
			Bucket:    "bag",
			Prefix:    "cell/",
			Region:    "us-east-1",
			LiveRepo:  true,
			AccessKey: "AKIAEXAMPLE",
			SecretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			Role:      RoleIngest,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := rec.joined()
	if !strings.Contains(got, "repository sync-to s3") {
		t.Fatalf("live repo should sync-to, argv=%q", got)
	}
	if strings.Contains(got, "wJalrXUtnFEMI") || strings.Contains(got, "--secret-access-key") || strings.Contains(got, "--access-key") {
		t.Fatalf("ingest keys must not be on argv: %q", got)
	}
	found := false
	rec.mu.Lock()
	defer rec.mu.Unlock()
	for _, env := range rec.envs {
		joined := strings.Join(env, " ")
		if strings.Contains(joined, "AWS_ACCESS_KEY_ID=AKIAEXAMPLE") && strings.Contains(joined, "AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected AWS_* env, got %#v", rec.envs)
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

func TestKopiaCheckFull(t *testing.T) {
	pw := writePass(t)
	rec := &recRunner{}
	k := KopiaEngine{R: rec}
	if err := k.Check(context.Background(), CheckIn{
		Repo:       Repo{Kind: Kopia, Path: "/repo", PasswordFile: pw, Binary: "kopia"},
		VerifyData: true,
	}); err != nil {
		t.Fatal(err)
	}
	got := rec.joined()
	if !strings.Contains(got, "content verify --full") {
		t.Fatalf("VerifyData without percent should --full, argv=%q", got)
	}
}

func TestKopiaForgetPrunePolicyThenExpire(t *testing.T) {
	pw := writePass(t)
	rec := &recRunner{}
	k := KopiaEngine{R: rec}
	if err := k.ForgetPrune(context.Background(), ForgetIn{
		Repo:        Repo{Kind: Kopia, Path: "/repo", PasswordFile: pw, Binary: "kopia"},
		KeepLast:    7,
		KeepDaily:   14,
		KeepWeekly:  4,
		KeepMonthly: 6,
		KeepYearly:  2,
		Prune:       true,
		Role:        RoleGC,
	}); err != nil {
		t.Fatal(err)
	}
	got := rec.joined()
	for _, want := range []string{
		"policy set --global",
		"--keep-latest 7",
		"--keep-daily 14",
		"--keep-weekly 4",
		"--keep-monthly 6",
		"--keep-annual 2",
		"snapshot expire --all --delete",
		"maintenance run --full",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	var expire int
	for _, c := range rec.calls {
		joined := strings.Join(c, " ")
		if !strings.Contains(joined, "snapshot expire") {
			continue
		}
		expire++
		if strings.Contains(joined, "--keep-latest") || strings.Contains(joined, "--keep-daily") {
			t.Fatalf("kopia 0.23 expire has no keep flags: %q", joined)
		}
		if !strings.Contains(joined, "--all") || !strings.Contains(joined, "--delete") {
			t.Fatalf("expire needs --all --delete: %q", joined)
		}
	}
	if expire != 1 {
		t.Fatalf("expected one snapshot expire, got %d in %q", expire, got)
	}
}
