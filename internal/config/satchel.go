package config

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"pannier.dev/internal/engine"
)

const (
	RoleHouse  = "house"
	RoleTenant = "tenant"
	HouseName  = "house"
)

var (
	ErrTenantNeedsScope = errors.New("tenant satchel needs a namespace, compose project, container set, host path, or dump")
)

// Satchel is one packing: a selection plus the engine repo it writes
// into plus the off-side locations that receive those packs.
//
// House (the top-level File fields) uses the shared engine. It can bag
// the whole cluster, several namespaces, or several containers in one
// repository so they share dedup.
//
// A tenant satchel is a separate directory. Use it when a customer
// wants their own copy on their own systems. Packs are never mixed
// with house or other tenants.
type Satchel struct {
	Name        string    `yaml:"name" json:"name"`
	Role        string    `yaml:"role,omitempty" json:"role,omitempty"`
	LocalRepo   LocalRepo `yaml:"localRepo" json:"localRepo"`
	Remotes     []Remote  `yaml:"remotes,omitempty" json:"remotes,omitempty"`
	Selection   Selection `yaml:"selection" json:"selection"`
	Dumps       []Dump    `yaml:"dumps,omitempty" json:"dumps,omitempty"`
	SelfInclude *bool     `yaml:"selfInclude,omitempty" json:"selfInclude,omitempty"`
}

func (s Satchel) role() string {
	if s.Role != "" {
		return s.Role
	}
	if s.Name == HouseName {
		return RoleHouse
	}
	return RoleTenant
}

func (s Satchel) IsHouse() bool {
	return s.role() == RoleHouse
}

// IncludeControlPlane is etcd + kubeadm files. Tenant copies never get
// it: the etcd snapshot is the whole cluster keyspace. House gets it
// when Kubernetes is on and either no namespace filter is set (whole
// cluster) or ControlPlane is checked.
func (s Selection) IncludeControlPlane(role string) bool {
	if role == RoleTenant || !s.K8s {
		return false
	}
	return s.ControlPlane
}

func (f File) House() Satchel {
	return Satchel{
		Name:      HouseName,
		Role:      RoleHouse,
		LocalRepo: f.LocalRepo,
		Remotes:   f.Remotes,
		Selection: f.Selection,
		Dumps:     f.Dumps,
	}
}

// AllSatchels is house first, then each named tenant copy. House is
// always the shared engine so a whole-cluster bag still runs when
// tenant copies exist.
func (f File) AllSatchels() []Satchel {
	out := []Satchel{f.House()}
	seen := map[string]bool{HouseName: true}
	for _, s := range f.Satchels {
		name := strings.TrimSpace(s.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		if s.Role == "" {
			s.Role = RoleTenant
		}
		s.Name = name
		out = append(out, s)
	}
	return out
}

func (f File) SatchelNamed(name string) (Satchel, bool) {
	if name == "" {
		name = HouseName
	}
	for _, s := range f.AllSatchels() {
		if s.Name == name {
			return s, true
		}
	}
	return Satchel{}, false
}

func (f File) RepoFor(name string) engine.Repo {
	s, ok := f.SatchelNamed(name)
	if !ok {
		return f.Repo()
	}
	return s.Apply(f).Repo()
}

func (s Satchel) Apply(base File) File {
	cfg := base
	cfg.Role = s.role()
	if !s.IsHouse() {
		cfg.Satchels = nil
	}
	if s.LocalRepo.Path != "" {
		cfg.LocalRepo = s.LocalRepo
	} else if !s.IsHouse() {
		root := strings.TrimSpace(base.DataDir)
		if root == "" {
			root = "satchels"
		}
		cfg.LocalRepo.Path = filepath.Join(root, "satchels", s.Name, "repo")
		if cfg.LocalRepo.PasswordFile == "" {
			cfg.LocalRepo.PasswordFile = filepath.Join(root, "satchels", s.Name, "repo.password")
		}
	}
	if s.IsHouse() {
		if s.Remotes != nil {
			cfg.Remotes = s.Remotes
		}
	} else {
		cfg.Remotes = s.Remotes
	}
	NormalizeRemotes(cfg.Remotes)
	cfg.Selection = s.Selection
	cfg.Dumps = s.Dumps
	if s.SelfInclude != nil {
		cfg.SelfInclude = *s.SelfInclude
	}
	return cfg
}

func (s Satchel) Validate() error {
	if strings.TrimSpace(s.Name) == "" {
		return errors.New("satchel name required")
	}
	if s.IsHouse() {
		return nil
	}
	sel := s.Selection
	if sel.K8s && len(trimList(sel.Namespaces)) == 0 {
		return fmt.Errorf("tenant %q: Kubernetes needs Namespaces; etcd is the whole cluster and stays in the house bag", s.Name)
	}
	if sel.Docker && len(trimList(sel.Containers)) == 0 && len(trimList(sel.ComposeProjects)) == 0 {
		return fmt.Errorf("tenant %q: Docker needs Include Names or Compose Projects so this copy does not bag every container", s.Name)
	}
	if sel.LXC && len(trimList(sel.LXCNames)) == 0 {
		return fmt.Errorf("tenant %q: LXC needs instance names", s.Name)
	}
	if sel.BareMetal && len(trimList(sel.HostPaths)) == 0 {
		return fmt.Errorf("tenant %q: Host Paths required; a node tree is this cell's, not a customer copy", s.Name)
	}
	scoped := len(trimList(sel.Namespaces)) > 0 ||
		len(trimList(sel.ComposeProjects)) > 0 ||
		len(trimList(sel.Containers)) > 0 ||
		len(trimList(sel.LXCNames)) > 0 ||
		len(trimList(sel.HostPaths)) > 0 ||
		len(s.Dumps) > 0
	if !scoped {
		return fmt.Errorf("%w (%s)", ErrTenantNeedsScope, s.Name)
	}
	return nil
}

func (f File) ValidateSatchels() error {
	seen := map[string]bool{}
	for _, s := range f.Satchels {
		name := strings.TrimSpace(s.Name)
		if name == "" || name == HouseName {
			return errors.New("tenant satchel needs its own name (house is the shared engine)")
		}
		if seen[name] {
			return fmt.Errorf("duplicate satchel %q", name)
		}
		seen[name] = true
		if s.Role == RoleHouse {
			return fmt.Errorf("satchel %q: extra copies are tenant role; house is the top-level shared engine", name)
		}
		cp := s
		if cp.Role == "" {
			cp.Role = RoleTenant
		}
		if err := cp.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func trimList(ss []string) []string {
	var out []string
	for _, s := range ss {
		if t := strings.TrimSpace(s); t != "" {
			out = append(out, t)
		}
	}
	return out
}
