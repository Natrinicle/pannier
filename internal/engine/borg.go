package engine

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"
)

// BorgEngine shells out to borg. Passphrase is BORG_PASSPHRASE_FD (extra
// file descriptor 3 on ExecRunner). There is no pack-level transfer.
type BorgEngine struct {
	R Runner
}

func (BorgEngine) Kind() Kind { return Borg }

func (e BorgEngine) Init(ctx context.Context, r Repo) error {
	return e.withPass(ctx, r, nil, nil, binary(r, "borg"), "init", "--encryption=repokey-blake2", r.Path)
}

func (e BorgEngine) Snapshot(ctx context.Context, in SnapshotIn) (Snapshot, error) {
	arch := borgArchive(in.Host, "fs")
	argv := []string{binary(in.Repo, "borg"), "create", "--json"}
	argv = append(argv, borgExclude(in.Exclude)...)
	argv = append(argv, in.Repo.Path+"::"+arch)
	argv = append(argv, in.Paths...)
	out, err := e.runPass(ctx, in.Repo, nil, nil, argv...)
	if err != nil {
		return Snapshot{}, err
	}
	fb := Snapshot{ID: arch, Time: time.Now().UTC(), Host: in.Host, Paths: in.Paths, Tags: in.Tags}
	return parseSnapshotJSON(out, fb), nil
}

func (e BorgEngine) SnapshotStdin(ctx context.Context, in StdinIn, extraEnv []string) (Snapshot, error) {
	name := in.Filename
	if name == "" {
		name = "stdin"
	}
	arch := borgArchive(in.Host, name)
	argv := []string{
		binary(in.Repo, "borg"), "create", "--json",
		"--content-from-command", "--stdin-name", name,
		in.Repo.Path + "::" + arch, "--",
	}
	argv = append(argv, in.Command...)
	out, err := e.runPass(ctx, in.Repo, nil, extraEnv, argv...)
	if err != nil {
		return Snapshot{}, err
	}
	fb := Snapshot{ID: arch, Time: time.Now().UTC(), Host: in.Host, Paths: []string{name}, Tags: in.Tags}
	return parseSnapshotJSON(out, fb), nil
}

func (e BorgEngine) List(ctx context.Context, r Repo) ([]Snapshot, error) {
	out, err := e.runPass(ctx, r, nil, nil, binary(r, "borg"), "list", "--json", r.Path)
	if err != nil {
		return nil, err
	}
	return parseSnapshotListJSON(out), nil
}

func (e BorgEngine) Restore(ctx context.Context, in RestoreIn) error {
	argv := []string{binary(in.Repo, "borg"), "extract", in.Repo.Path + "::" + in.ID}
	if in.Sub != "" {
		argv = append(argv, in.Sub)
	}
	return e.withPass(ctx, in.Repo, nil, nil, argv...)
}

func (e BorgEngine) Check(ctx context.Context, in CheckIn) error {
	ctx, cancel := withMaxDuration(ctx, in.MaxDurationSec)
	defer cancel()
	argv := []string{binary(in.Repo, "borg"), "check", in.Repo.Path}
	if in.VerifyData {
		argv = append(argv, "--verify-data")
	}
	return e.withPass(ctx, in.Repo, nil, nil, argv...)
}

func (e BorgEngine) ListPacks(ctx context.Context, r Repo) ([]Pack, error) {
	return nil, ErrNoPackTransfer
}

func (e BorgEngine) PackCopy(ctx context.Context, in PackCopyIn) (PackCopyResult, error) {
	return PackCopyResult{}, ErrNoPackTransfer
}

func (e BorgEngine) ForgetPrune(ctx context.Context, in ForgetIn) error {
	if in.Role == RoleIngest {
		return ErrIngestMustNotMaintain
	}
	argv := []string{binary(in.Repo, "borg"), "prune", in.Repo.Path}
	if in.KeepLast > 0 {
		argv = append(argv, "--keep-last", fmt.Sprintf("%d", in.KeepLast))
	}
	if in.KeepDaily > 0 {
		argv = append(argv, "--keep-daily", fmt.Sprintf("%d", in.KeepDaily))
	}
	if in.KeepWeekly > 0 {
		argv = append(argv, "--keep-weekly", fmt.Sprintf("%d", in.KeepWeekly))
	}
	if in.KeepMonthly > 0 {
		argv = append(argv, "--keep-monthly", fmt.Sprintf("%d", in.KeepMonthly))
	}
	if in.KeepYearly > 0 {
		argv = append(argv, "--keep-yearly", fmt.Sprintf("%d", in.KeepYearly))
	}
	if in.KeepWithin != "" {
		argv = append(argv, "--keep-within", in.KeepWithin)
	}
	return e.withPass(ctx, in.Repo, nil, nil, argv...)
}

func (e BorgEngine) withPass(ctx context.Context, r Repo, stdin io.Reader, extraEnv []string, argv ...string) error {
	_, err := e.runPass(ctx, r, stdin, extraEnv, argv...)
	return err
}

func (e BorgEngine) runPass(ctx context.Context, r Repo, stdin io.Reader, extraEnv []string, argv ...string) ([]byte, error) {
	env := append([]string{"BORG_PASSPHRASE_FD=3", "BORG_REPO=" + r.Path}, extraEnv...)
	if xf, ok := e.R.(extraFiler); ok && r.PasswordFile != "" {
		f, err := os.Open(r.PasswordFile)
		if err != nil {
			return nil, fmt.Errorf("borg password file: %w", err)
		}
		defer f.Close()
		return xf.RunWithExtraFiles(ctx, stdin, env, []*os.File{f}, argv...)
	}
	// Recording runners: advertise the FD in env. Never put the passphrase
	// on argv or in extraEnv.
	return e.R.Run(ctx, stdin, env, argv...)
}

func borgArchive(host, name string) string {
	if host == "" {
		host = "host"
	}
	stamp := time.Now().UTC().Format("20060102T150405")
	return host + "-" + name + "-" + stamp
}

func borgExclude(exclude []string) []string {
	var argv []string
	for _, e := range exclude {
		argv = append(argv, "--exclude", e)
	}
	return argv
}
