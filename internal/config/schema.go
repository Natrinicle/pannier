package config

import (
	"errors"
	"fmt"

	"pannier.dev/internal/engine"
)

var ErrCellHasMaintenanceCreds = errors.New("cell config must not contain maintenanceCreds")

// File is the on-disk YAML. Cell processes load ingestCreds only.
// Steward / GC keys live on the maintenance host.
type File struct {
	Engine      engine.Kind `yaml:"engine" json:"engine"`
	DataDir     string      `yaml:"dataDir" json:"dataDir"`
	Listen      string      `yaml:"listen" json:"listen"`
	LocalRepo   LocalRepo   `yaml:"localRepo" json:"localRepo"`
	Remotes     []Remote    `yaml:"remotes" json:"remotes"`
	Schedule    Schedule    `yaml:"schedule" json:"schedule"`
	Selection   Selection   `yaml:"selection" json:"selection"`
	Dumps       []Dump      `yaml:"dumps" json:"dumps"`
	SelfInclude bool        `yaml:"selfInclude" json:"selfInclude"`
	Retention   Retention   `yaml:"retention" json:"retention"`
	// Satchels are extra isolated copies (customer off-side bags). The
	// house bag is the top-level LocalRepo + Selection + Remotes and
	// keeps using the shared engine.
	Satchels []Satchel `yaml:"satchels,omitempty" json:"satchels,omitempty"`
	// Role is set on an overlay during a run. Empty means house.
	Role string `yaml:"-" json:"-"`
}

type LocalRepo struct {
	Path         string `yaml:"path" json:"path"`
	PasswordFile string `yaml:"passwordFile" json:"passwordFile"`
	KopiaConfig  string `yaml:"kopiaConfig" json:"kopiaConfig"`
	Binary       string `yaml:"binary" json:"binary"`
}

type Remote struct {
	Name             string `yaml:"name" json:"name"`
	Endpoint         string `yaml:"endpoint" json:"endpoint"`
	Bucket           string `yaml:"bucket" json:"bucket"`
	Prefix           string `yaml:"prefix" json:"prefix"`
	Region           string `yaml:"region" json:"region"`
	DisableTLS       bool   `yaml:"disableTLS" json:"disableTLS"`
	ObjectLock       bool   `yaml:"objectLock" json:"objectLock"`
	LiveRepo         bool   `yaml:"liveRepo" json:"liveRepo"`
	IngestCreds      *Creds `yaml:"ingestCreds,omitempty" json:"ingestCreds,omitempty"`
	MaintenanceCreds *Creds `yaml:"maintenanceCreds,omitempty" json:"maintenanceCreds,omitempty"`
}

type Creds struct {
	AccessKeyFile string `yaml:"accessKeyFile,omitempty" json:"accessKeyFile,omitempty"`
	SecretKeyFile string `yaml:"secretKeyFile,omitempty" json:"secretKeyFile,omitempty"`
	// AccessKey / SecretKey are discouraged; present so Redact can scrub
	// a mistaken paste before self-include snapshots the YAML.
	AccessKey string `yaml:"accessKey,omitempty" json:"accessKey,omitempty"`
	SecretKey string `yaml:"secretKey,omitempty" json:"secretKey,omitempty"`
}

func (c *Creds) Empty() bool {
	if c == nil {
		return true
	}
	return c.AccessKeyFile == "" && c.SecretKeyFile == "" && c.AccessKey == "" && c.SecretKey == ""
}

type Schedule struct {
	Backup   string `yaml:"backup" json:"backup"`
	PackCopy string `yaml:"packcopy" json:"packcopy"`
	Check    string `yaml:"check" json:"check"`
	Maint    string `yaml:"maint" json:"maint"`
}

type Selection struct {
	Docker          bool     `yaml:"docker" json:"docker"`
	K8s             bool     `yaml:"k8s" json:"k8s"`
	LXC             bool     `yaml:"lxc" json:"lxc"`
	BareMetal       bool     `yaml:"bareMetal" json:"bareMetal"`
	ControlPlane    bool     `yaml:"controlPlane" json:"controlPlane"`
	Containers      []string `yaml:"containers,omitempty" json:"containers,omitempty"`
	SkipContainers  []string `yaml:"skipContainers,omitempty" json:"skipContainers,omitempty"`
	ComposeProjects []string `yaml:"composeProjects,omitempty" json:"composeProjects,omitempty"`
	Namespaces      []string `yaml:"namespaces,omitempty" json:"namespaces,omitempty"`
	LXCNames        []string `yaml:"lxcNames,omitempty" json:"lxcNames,omitempty"`
	HostPaths       []string `yaml:"hostPaths,omitempty" json:"hostPaths,omitempty"`
}

type Dump struct {
	Name     string   `yaml:"name" json:"name"`
	Kind     string   `yaml:"kind" json:"kind"`
	Command  []string `yaml:"command" json:"command"`
	Filename string   `yaml:"filename" json:"filename"`
}

type Retention struct {
	KeepLast    int    `yaml:"keepLast" json:"keepLast"`
	KeepDaily   int    `yaml:"keepDaily" json:"keepDaily"`
	KeepWeekly  int    `yaml:"keepWeekly" json:"keepWeekly"`
	KeepMonthly int    `yaml:"keepMonthly" json:"keepMonthly"`
	KeepYearly  int    `yaml:"keepYearly" json:"keepYearly"`
	KeepWithin  string `yaml:"keepWithin" json:"keepWithin"`
	Prune       bool   `yaml:"prune" json:"prune"`
}

// ValidateCell errors if maintenanceCreds are present. The backup daemon
// on a cell must not be able to delete object-lock packs.
// NormalizeOffside makes Locked Packs and Live Engine Repo exclusive.
// Both true: Object Lock wins (the B2-safe default). Both false: Locked
// Packs. LiveRepo true: Object Lock off.
func (r *Remote) NormalizeOffside() {
	if r == nil {
		return
	}
	if r.LiveRepo && r.ObjectLock {
		r.LiveRepo = false
		return
	}
	if r.LiveRepo {
		r.ObjectLock = false
		return
	}
	r.ObjectLock = true
	r.LiveRepo = false
}

func NormalizeRemotes(rs []Remote) {
	for i := range rs {
		rs[i].NormalizeOffside()
	}
}

func (f File) ValidateCell() error {
	if err := f.ValidateSatchels(); err != nil {
		return err
	}
	for _, s := range f.AllSatchels() {
		for _, r := range s.Remotes {
			if !r.MaintenanceCreds.Empty() {
				return fmt.Errorf("%w (remote %q satchel %q)", ErrCellHasMaintenanceCreds, r.Name, s.Name)
			}
		}
	}
	return nil
}

// ValidateIngestOnly checks this overlay's remotes. Use during a satchel
// run so a tenant copy does not re-validate sibling satchels.
func (f File) ValidateIngestOnly() error {
	for _, r := range f.Remotes {
		if !r.MaintenanceCreds.Empty() {
			return fmt.Errorf("%w (remote %q)", ErrCellHasMaintenanceCreds, r.Name)
		}
	}
	return nil
}

func (f File) HasMaintenanceCreds() bool {
	for _, s := range f.AllSatchels() {
		for _, r := range s.Remotes {
			if !r.MaintenanceCreds.Empty() {
				return true
			}
		}
	}
	return false
}

func (f File) Repo() engine.Repo {
	bin := f.LocalRepo.Binary
	kind := f.Engine
	if kind == "" {
		kind = engine.Kopia
	}
	if bin == "" {
		bin = string(kind)
	}
	return engine.Repo{
		Kind:         kind,
		Path:         f.LocalRepo.Path,
		PasswordFile: f.LocalRepo.PasswordFile,
		KopiaConfig:  f.LocalRepo.KopiaConfig,
		Binary:       bin,
	}
}

func Default() File {
	return File{
		Engine:      engine.FakeKind,
		Listen:      "127.0.0.1:3477",
		SelfInclude: true,
		Schedule: Schedule{
			Backup:   "0 2 * * *",
			PackCopy: "30 2 * * *",
			Check:    "0 4 * * 0",
			Maint:    "0 5 * * 0",
		},
		Selection: Selection{Docker: true, K8s: true, BareMetal: true, ControlPlane: true},
		Retention: Retention{KeepLast: 7, KeepDaily: 14, KeepWeekly: 8, KeepMonthly: 12, Prune: true},
	}
}
