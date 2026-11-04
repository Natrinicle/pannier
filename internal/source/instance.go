package source

import (
	"context"
	"path/filepath"
	"strings"

	"pannier.dev/internal/config"
)

// Instance is one restorable runtime: a Docker container or an LXC
// instance. ID is stable across bags (docker:name / lxc:name).
type Instance struct {
	Kind      string   `json:"kind"`
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Paths     []string `json:"paths,omitempty"`
	Note      string   `json:"note,omitempty"`
	Project   string   `json:"project,omitempty"`
	Namespace string   `json:"namespace,omitempty"`
	Node      string   `json:"node,omitempty"`
}

const (
	KindDockerContainer = "docker.container"
	KindLXCInstance     = "lxc.instance"
	KindHostPath        = "host.path"
)

// Pick is the live set for this selection. Missing sockets are empty
// lists, not errors: include names still become marker parcels.
func Pick(ctx context.Context, sel config.Selection) []Instance {
	var out []Instance
	if sel.Docker {
		live, err := ListDocker(ctx)
		if err != nil {
			live = nil
		}
		out = append(out, filterDocker(live, sel)...)
	}
	if sel.LXC {
		live, err := ListLXC(ctx)
		if err != nil {
			live = nil
		}
		include := sel.LXCNames
		out = append(out, filter(live, KindLXCInstance, "lxc:", include, nil)...)
	}
	return out
}

// filterDocker is a union of include names and compose projects, minus
// skip. Empty include and empty projects means every container this
// engine reports (house shared-engine behaviour).
func filterDocker(live []Instance, sel config.Selection) []Instance {
	skip := nameSet(sel.SkipContainers)
	wantNames := nameSet(sel.Containers)
	wantProj := nameSet(sel.ComposeProjects)
	open := len(wantNames) == 0 && len(wantProj) == 0
	seen := map[string]bool{}
	var out []Instance
	add := func(in Instance) {
		n := bareName(in.Name)
		if n == "" || skip[n] || seen[n] {
			return
		}
		seen[n] = true
		out = append(out, in)
	}
	for _, in := range live {
		n := bareName(in.Name)
		if open {
			add(in)
			continue
		}
		if wantNames[n] {
			add(in)
			continue
		}
		if in.Project != "" && wantProj[in.Project] {
			add(in)
		}
	}
	for _, raw := range sel.Containers {
		name := bareName(raw)
		if name == "" || skip[name] || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, Instance{
			Kind: KindDockerContainer,
			ID:   "docker:" + name,
			Name: name,
			Note: "Not running on this cell. Bag keeps a marker.",
		})
	}
	return out
}

func filter(live []Instance, kind, prefix string, include, skip []string) []Instance {
	skipSet := nameSet(skip)
	have := map[string]Instance{}
	for _, in := range live {
		have[bareName(in.Name)] = in
	}
	if len(include) == 0 {
		var out []Instance
		for _, in := range live {
			if skipSet[bareName(in.Name)] {
				continue
			}
			out = append(out, in)
		}
		return out
	}
	var out []Instance
	seen := map[string]bool{}
	for _, raw := range include {
		name := bareName(raw)
		if name == "" || skipSet[name] || seen[name] {
			continue
		}
		seen[name] = true
		if in, ok := have[name]; ok {
			out = append(out, in)
			continue
		}
		out = append(out, Instance{
			Kind: kind,
			ID:   prefix + name,
			Name: name,
			Note: "Not running on this cell. Bag keeps a marker.",
		})
	}
	return out
}

func nameSet(ss []string) map[string]bool {
	m := map[string]bool{}
	for _, s := range ss {
		if n := bareName(s); n != "" {
			m[n] = true
		}
	}
	return m
}

func bareName(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "docker:")
	s = strings.TrimPrefix(s, "lxc:")
	s = strings.TrimPrefix(s, "/")
	s = filepath.Base(s)
	if s == "." || s == ".." || s == "" {
		return ""
	}
	return s
}
