package engine

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	objs "pannier.dev/internal/s3"
)

// KopiaEngine shells out to the kopia binary. We do not import
// github.com/kopia/kopia: that would pull the engine into this module.
type KopiaEngine struct {
	R Runner
}

func (KopiaEngine) Kind() Kind { return Kopia }

func (k KopiaEngine) Init(ctx context.Context, r Repo) error {
	bin := binary(r, "kopia")
	if _, err := exec.LookPath(bin); err != nil {
		return fmt.Errorf("kopia %q not found (install the kopia package, copy it into the image, or set localRepo.binary): %w", bin, err)
	}
	base, err := kopiaBase(r)
	if err != nil {
		return err
	}
	argv := append(base, "repository", "create", "filesystem", "--path", r.Path)
	if _, err := k.R.Run(ctx, nil, nil, argv...); err != nil {
		// Already-initialized repo: connect instead of failing the cell.
		argv = append(base, "repository", "connect", "filesystem", "--path", r.Path)
		_, err2 := k.R.Run(ctx, nil, nil, argv...)
		return err2
	}
	return nil
}

func (k KopiaEngine) Snapshot(ctx context.Context, in SnapshotIn) (Snapshot, error) {
	base, err := kopiaBase(in.Repo)
	if err != nil {
		return Snapshot{}, err
	}
	argv := append(base, "snapshot", "create", "--json")
	argv = append(argv, kopiaTagsHostExclude(in.Tags, in.Host, in.Exclude)...)
	argv = append(argv, in.Paths...)
	out, err := k.R.Run(ctx, nil, nil, argv...)
	if err != nil {
		return Snapshot{}, err
	}
	fb := Snapshot{Time: time.Now().UTC(), Host: in.Host, Paths: in.Paths, Tags: in.Tags}
	return parseSnapshotJSON(out, fb), nil
}

func (k KopiaEngine) SnapshotStdin(ctx context.Context, in StdinIn, extraEnv []string) (Snapshot, error) {
	base, err := kopiaBase(in.Repo)
	if err != nil {
		return Snapshot{}, err
	}
	name := in.Filename
	if name == "" {
		name = "stdin"
	}
	// --stdin-file names the blob inside the snapshot; kopia still wants a
	// source path so the snapshot has a stable identity.
	argv := append(base, "snapshot", "create", "--json", "--stdin-file", name, name)
	argv = append(argv, kopiaTagsHostExclude(in.Tags, in.Host, nil)...)
	stdin, closer, err := stdinFromCommand(ctx, in.Command, extraEnv)
	if err != nil {
		return Snapshot{}, err
	}
	if closer != nil {
		defer closer()
	}
	out, err := k.R.Run(ctx, stdin, extraEnv, argv...)
	if err != nil {
		return Snapshot{}, err
	}
	fb := Snapshot{Time: time.Now().UTC(), Host: in.Host, Paths: []string{name}, Tags: in.Tags}
	return parseSnapshotJSON(out, fb), nil
}

func (k KopiaEngine) List(ctx context.Context, r Repo) ([]Snapshot, error) {
	base, err := kopiaBase(r)
	if err != nil {
		return nil, err
	}
	out, err := k.R.Run(ctx, nil, nil, append(base, "snapshot", "list", "--json")...)
	if err != nil {
		return nil, err
	}
	return parseSnapshotListJSON(out), nil
}

func (k KopiaEngine) Restore(ctx context.Context, in RestoreIn) error {
	base, err := kopiaBase(in.Repo)
	if err != nil {
		return err
	}
	argv := append(base, "snapshot", "restore", in.ID, in.Target)
	if in.Sub != "" {
		argv = append(argv, "--snapshot-time-latest")
		argv = append(argv, in.Sub)
	}
	_, err = k.R.Run(ctx, nil, nil, argv...)
	return err
}

func (k KopiaEngine) Check(ctx context.Context, in CheckIn) error {
	ctx, cancel := withMaxDuration(ctx, in.MaxDurationSec)
	defer cancel()
	base, err := kopiaBase(in.Repo)
	if err != nil {
		return err
	}
	pct := in.ReadPercent
	if pct <= 0 && in.VerifyData {
		pct = 100
	}
	argv := append(base, "content", "verify")
	if pct >= 100 {
		argv = append(argv, "--full")
	} else if pct > 0 {
		argv = append(argv, "--download-percent", fmt.Sprintf("%g", pct))
	}
	_, err = k.R.Run(ctx, nil, nil, argv...)
	return err
}

func (k KopiaEngine) ListPacks(ctx context.Context, r Repo) ([]Pack, error) {
	base, err := kopiaBase(r)
	if err != nil {
		return nil, err
	}
	// blob list is the pack inventory. Do not use `repository sync-to` for
	// this: sync-to wants a live repo and will try to delete extras.
	out, err := k.R.Run(ctx, nil, nil, append(base, "blob", "list")...)
	if err != nil {
		return nil, err
	}
	return parseBlobList(out), nil
}

func (k KopiaEngine) PackCopy(ctx context.Context, in PackCopyIn) (PackCopyResult, error) {
	// Object-lock / write-only prefixes are not live Kopia repositories.
	// `repository sync-to` onto them hits kopia#3759 (blob-retention) and
	// would also want to delete. Cell ingest is list+put of missing packs.
	// Walk the local repo directory: `kopia blob list` IDs are not
	// filesystem RelPaths (pxxx vs p/pxxx, plus kopia.repository).
	if in.Dest.ObjectLock || !in.Dest.LiveRepo {
		items, err := listKopiaRepoFiles(in.Repo.Path)
		if err != nil {
			return PackCopyResult{}, err
		}
		store, err := objs.OpenWith(objs.Options{
			Endpoint:   in.Dest.Endpoint,
			Bucket:     in.Dest.Bucket,
			Prefix:     in.Dest.Prefix,
			Region:     in.Dest.Region,
			AccessKey:  in.Dest.AccessKey,
			SecretKey:  in.Dest.SecretKey,
			DisableTLS: in.Dest.DisableTLS,
		})
		if err != nil {
			return PackCopyResult{}, err
		}
		open := func(_ context.Context, o objs.Object) (io.Reader, int64, error) {
			path := packFilePath(in.Repo.Path, o)
			f, err := os.Open(path)
			if err != nil {
				return nil, 0, err
			}
			st, err := f.Stat()
			if err != nil {
				_ = f.Close()
				return nil, 0, err
			}
			return struct {
				io.Reader
				io.Closer
			}{f, f}, st.Size(), nil
		}
		res, err := objs.CopyMissing(ctx, items, open, store)
		return PackCopyResult{
			ListedSrc: res.ListedSrc,
			ListedDst: res.ListedDst,
			Put:       res.Put,
			Skipped:   res.Skipped,
			Bytes:     res.Bytes,
		}, err
	}
	base, err := kopiaBase(in.Repo)
	if err != nil {
		return PackCopyResult{}, err
	}
	argv := append(base, "repository", "sync-to", "s3",
		"--bucket", in.Dest.Bucket,
		"--endpoint", in.Dest.Endpoint,
		"--prefix", in.Dest.Prefix,
	)
	if in.Dest.Region != "" {
		argv = append(argv, "--region", in.Dest.Region)
	}
	if in.Dest.DisableTLS {
		argv = append(argv, "--disable-tls")
	}
	_, err = k.R.Run(ctx, nil, s3Env(in.Dest), argv...)
	return PackCopyResult{}, err
}

func (k KopiaEngine) ForgetPrune(ctx context.Context, in ForgetIn) error {
	if in.Role == RoleIngest {
		return ErrIngestMustNotMaintain
	}
	base, err := kopiaBase(in.Repo)
	if err != nil {
		return err
	}
	// kopia 0.23: keep-* flags live on `policy set`. `snapshot expire`
	// only has --all --delete. KeepWithin is restic/borg; kopia has no
	// duration equivalent here.
	policy := append(base, "policy", "set", "--global")
	set := false
	if in.KeepLast > 0 {
		policy = append(policy, "--keep-latest", fmt.Sprintf("%d", in.KeepLast))
		set = true
	}
	if in.KeepDaily > 0 {
		policy = append(policy, "--keep-daily", fmt.Sprintf("%d", in.KeepDaily))
		set = true
	}
	if in.KeepWeekly > 0 {
		policy = append(policy, "--keep-weekly", fmt.Sprintf("%d", in.KeepWeekly))
		set = true
	}
	if in.KeepMonthly > 0 {
		policy = append(policy, "--keep-monthly", fmt.Sprintf("%d", in.KeepMonthly))
		set = true
	}
	if in.KeepYearly > 0 {
		policy = append(policy, "--keep-annual", fmt.Sprintf("%d", in.KeepYearly))
		set = true
	}
	if set {
		if _, err := k.R.Run(ctx, nil, nil, policy...); err != nil {
			return err
		}
	}
	if _, err := k.R.Run(ctx, nil, nil, append(base, "snapshot", "expire", "--all", "--delete")...); err != nil {
		return err
	}
	if in.Prune {
		_, err = k.R.Run(ctx, nil, nil, append(base, "maintenance", "run", "--full")...)
		return err
	}
	return nil
}

func kopiaBase(r Repo) ([]string, error) {
	argv := []string{binary(r, "kopia")}
	if r.KopiaConfig != "" {
		argv = append(argv, "--config-file", r.KopiaConfig)
	}
	if r.PasswordFile != "" {
		b, err := os.ReadFile(r.PasswordFile)
		if err != nil {
			return nil, fmt.Errorf("kopia password file: %w", err)
		}
		// Read in-process and pass -p. Do not log this argv unredacted.
		argv = append(argv, "-p", strings.TrimSpace(string(b)))
	}
	return argv, nil
}

func kopiaTagsHostExclude(tags map[string]string, host string, exclude []string) []string {
	var argv []string
	for _, k := range sortedKeys(tags) {
		argv = append(argv, "--tags", k+":"+tags[k])
	}
	if host != "" {
		argv = append(argv, "--override-source", host)
	}
	for _, e := range exclude {
		// kopia snapshot create has no --exclude; ignore-file is the
		// supported knob. Pass a path only when it looks like a file.
		argv = append(argv, "--ignore", e)
	}
	return argv
}

func packFilePath(repo string, o objs.Object) string {
	rel := o.RelPath
	if rel == "" {
		rel = o.Key
	}
	return filepath.Join(repo, filepath.FromSlash(rel))
}

func listKopiaRepoFiles(root string) ([]objs.Object, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf("kopia repo path is empty")
	}
	st, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("kopia repo %s: %w", root, err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("kopia repo %s: not a directory", root)
	}
	var out []objs.Object
	err = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		name := info.Name()
		if strings.HasSuffix(name, ".tmp") || strings.HasSuffix(name, ".lock") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." || strings.Contains(rel, "..") {
			return nil
		}
		out = append(out, objs.Object{Key: rel, RelPath: rel, Size: info.Size()})
		return nil
	})
	return out, err
}

func s3Env(d ObjectDest) []string {
	region := strings.TrimSpace(d.Region)
	if region == "" {
		region = "us-east-1"
	}
	env := []string{
		"AWS_REGION=" + region,
		"AWS_DEFAULT_REGION=" + region,
	}
	if d.AccessKey != "" {
		env = append(env, "AWS_ACCESS_KEY_ID="+d.AccessKey)
	}
	if d.SecretKey != "" {
		env = append(env, "AWS_SECRET_ACCESS_KEY="+d.SecretKey)
	}
	return env
}

func withMaxDuration(ctx context.Context, sec int) (context.Context, context.CancelFunc) {
	if sec <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, time.Duration(sec)*time.Second)
}

func stdinFromCommand(ctx context.Context, command, extraEnv []string) (io.Reader, func(), error) {
	if len(command) == 0 {
		return nil, nil, nil
	}
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, nil, fmt.Errorf("stdin command: %w: %s", err, redactBytes(stderr.Bytes()))
	}
	return &buf, nil, nil
}
