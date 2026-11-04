package engine

import (
	"context"
	"io"
	"time"
)

type Kind string

const (
	Kopia  Kind = "kopia"
	Restic Kind = "restic"
	Borg   Kind = "borg"
)

type CredRole string

const (
	RoleIngest CredRole = "ingest"
	RoleGC     CredRole = "gc"
)

type Repo struct {
	Kind         Kind
	Path         string
	PasswordFile string
	KopiaConfig  string
	Binary       string
}

type Snapshot struct {
	ID        string
	Time      time.Time
	Host      string
	Paths     []string
	Tags      map[string]string
	EntryID   string
	SitesHold int
}

type SnapshotIn struct {
	Repo    Repo
	Paths   []string
	Tags    map[string]string
	Host    string
	Exclude []string
}

type StdinIn struct {
	Repo     Repo
	Filename string
	Command  []string
	Tags     map[string]string
	Host     string
}

type RestoreIn struct {
	Repo   Repo
	ID     string
	Target string
	Sub    string
}

type CheckIn struct {
	Repo           Repo
	ID             string
	ReadPercent    float64
	VerifyData     bool
	MaxDurationSec int
}

type Pack struct {
	RelPath string
	Size    int64
	Key     string
}

type ObjectDest struct {
	Endpoint, Bucket, Prefix, Region string
	DisableTLS                       bool
	ObjectLock                       bool
	LiveRepo                         bool
	AccessKey                        string
	SecretKey                        string
	Role                             CredRole
}

type PackCopyIn struct {
	Repo Repo
	Dest ObjectDest
}

type PackCopyResult struct {
	ListedSrc, ListedDst, Put, Skipped int
	Bytes                              int64
}

type ForgetIn struct {
	Repo        Repo
	KeepLast    int
	KeepDaily   int
	KeepWeekly  int
	KeepMonthly int
	KeepYearly  int
	KeepWithin  string
	Prune       bool
	Role        CredRole
}

type Engine interface {
	Kind() Kind
	Init(ctx context.Context, r Repo) error
	Snapshot(ctx context.Context, in SnapshotIn) (Snapshot, error)
	SnapshotStdin(ctx context.Context, in StdinIn, extraEnv []string) (Snapshot, error)
	List(ctx context.Context, r Repo) ([]Snapshot, error)
	Restore(ctx context.Context, in RestoreIn) error
	Check(ctx context.Context, in CheckIn) error
	ListPacks(ctx context.Context, r Repo) ([]Pack, error)
	PackCopy(ctx context.Context, in PackCopyIn) (PackCopyResult, error)
	ForgetPrune(ctx context.Context, in ForgetIn) error
}

type Runner interface {
	Run(ctx context.Context, stdin io.Reader, env []string, argv ...string) (stdout []byte, err error)
}
