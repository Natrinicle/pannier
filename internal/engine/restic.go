package engine

import (
	"context"
	"fmt"
	"time"
)

// ResticEngine shells out to restic. Password stays in --password-file;
// we never splice it onto argv.
type ResticEngine struct {
	R Runner
}

func (ResticEngine) Kind() Kind { return Restic }

func (e ResticEngine) Init(ctx context.Context, r Repo) error {
	_, err := e.R.Run(ctx, nil, nil, resticBase(r, "init")...)
	return err
}

func (e ResticEngine) Snapshot(ctx context.Context, in SnapshotIn) (Snapshot, error) {
	argv := resticBase(in.Repo, "backup", "--json")
	argv = append(argv, resticTagsExclude(in.Tags, in.Exclude, in.Host)...)
	argv = append(argv, in.Paths...)
	out, err := e.R.Run(ctx, nil, nil, argv...)
	if err != nil {
		return Snapshot{}, err
	}
	fb := Snapshot{Time: time.Now().UTC(), Host: in.Host, Paths: in.Paths, Tags: in.Tags}
	return parseSnapshotJSON(out, fb), nil
}

func (e ResticEngine) SnapshotStdin(ctx context.Context, in StdinIn, extraEnv []string) (Snapshot, error) {
	name := in.Filename
	if name == "" {
		name = "stdin"
	}
	argv := resticBase(in.Repo, "backup", "--json", "--stdin-from-command", "--stdin-filename", name)
	argv = append(argv, resticTagsExclude(in.Tags, nil, in.Host)...)
	argv = append(argv, "--")
	argv = append(argv, in.Command...)
	out, err := e.R.Run(ctx, nil, extraEnv, argv...)
	if err != nil {
		return Snapshot{}, err
	}
	fb := Snapshot{Time: time.Now().UTC(), Host: in.Host, Paths: []string{name}, Tags: in.Tags}
	return parseSnapshotJSON(out, fb), nil
}

func (e ResticEngine) List(ctx context.Context, r Repo) ([]Snapshot, error) {
	out, err := e.R.Run(ctx, nil, nil, resticBase(r, "snapshots", "--json")...)
	if err != nil {
		return nil, err
	}
	return parseSnapshotListJSON(out), nil
}

func (e ResticEngine) Restore(ctx context.Context, in RestoreIn) error {
	argv := resticBase(in.Repo, "restore", in.ID, "--target", in.Target)
	if in.Sub != "" {
		argv = append(argv, "--include", in.Sub)
	}
	_, err := e.R.Run(ctx, nil, nil, argv...)
	return err
}

func (e ResticEngine) Check(ctx context.Context, in CheckIn) error {
	ctx, cancel := withMaxDuration(ctx, in.MaxDurationSec)
	defer cancel()
	argv := resticBase(in.Repo, "check")
	if in.ReadPercent > 0 {
		argv = append(argv, "--read-data-subset", fmt.Sprintf("%g%%", in.ReadPercent))
	} else if in.VerifyData {
		argv = append(argv, "--read-data")
	}
	_, err := e.R.Run(ctx, nil, nil, argv...)
	return err
}

func (e ResticEngine) ListPacks(ctx context.Context, r Repo) ([]Pack, error) {
	out, err := e.R.Run(ctx, nil, nil, resticBase(r, "list", "packs")...)
	if err != nil {
		return nil, err
	}
	return parseBlobList(out), nil
}

func (e ResticEngine) PackCopy(ctx context.Context, in PackCopyIn) (PackCopyResult, error) {
	if !in.Dest.LiveRepo {
		return PackCopyResult{}, ErrNotLiveRepo
	}
	// restic copy needs a live destination repo (connected via env).
	env := []string{
		"RESTIC_FROM_PASSWORD_FILE=" + in.Repo.PasswordFile,
		"RESTIC_FROM_REPOSITORY=" + in.Repo.Path,
	}
	argv := []string{binary(in.Repo, "restic"), "copy", "--password-file", in.Repo.PasswordFile}
	_, err := e.R.Run(ctx, nil, env, argv...)
	return PackCopyResult{}, err
}

func (e ResticEngine) ForgetPrune(ctx context.Context, in ForgetIn) error {
	if in.Role == RoleIngest {
		return ErrIngestMustNotMaintain
	}
	argv := resticBase(in.Repo, "forget")
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
	if in.Prune {
		argv = append(argv, "--prune")
	}
	_, err := e.R.Run(ctx, nil, nil, argv...)
	return err
}

func resticBase(r Repo, verb string, extra ...string) []string {
	argv := []string{binary(r, "restic"), "--password-file", r.PasswordFile, "-r", r.Path, verb}
	return append(argv, extra...)
}

func resticTagsExclude(tags map[string]string, exclude []string, host string) []string {
	var argv []string
	if host != "" {
		argv = append(argv, "--host", host)
	}
	for _, k := range sortedKeys(tags) {
		argv = append(argv, "--tag", k+"="+tags[k])
	}
	for _, e := range exclude {
		argv = append(argv, "--exclude", e)
	}
	return argv
}
