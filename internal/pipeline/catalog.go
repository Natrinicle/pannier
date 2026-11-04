package pipeline

import (
	"strings"

	"pannier.dev/internal/config"
	"pannier.dev/internal/engine"
	"pannier.dev/internal/source"
)

// Item is one thing inside a bag: a container, a database dump, etcd,
// host paths, or this pannier's own config.
type Item struct {
	ID         string   `json:"id"`
	Kind       string   `json:"kind"`
	Name       string   `json:"name"`
	SnapshotID string   `json:"snapshotId,omitempty"`
	Restorable bool     `json:"restorable"`
	Sequence   bool     `json:"sequence,omitempty"`
	Note       string   `json:"note,omitempty"`
	Paths      []string `json:"paths,omitempty"`
}

// Bag is one backup run, listed like a Kopia snapshot.
type Bag struct {
	ID       string `json:"id"`
	Started  string `json:"started"`
	Contents []Item `json:"contents"`
	Engine   string `json:"engine,omitempty"`
	Sites    int    `json:"sites"`
	Parcels  int    `json:"parcels"`
	Satchel  string `json:"satchel,omitempty"`
	Node     string `json:"node,omitempty"`
}

// Catalog is the planned contents of the next bag from current selection.
func Catalog(cfg config.File) []Item {
	return CatalogWith(cfg, nil)
}

// CatalogWith expands live docker/lxc instances into one parcel each.
func CatalogWith(cfg config.File, found []source.Instance) []Item {
	var out []Item
	if cfg.SelfInclude {
		out = append(out, Item{ID: "self", Kind: "pannier.self", Name: "This Pannier"})
	}
	for _, d := range cfg.Dumps {
		name := d.Name
		if name == "" {
			name = d.Filename
		}
		if name == "" {
			continue
		}
		note := d.Kind
		out = append(out, Item{ID: name, Kind: "db.dump", Name: name, Note: note})
	}
	if cfg.Selection.Docker {
		dock := itemsOf(found, source.KindDockerContainer)
		if len(dock) == 0 {
			skip := map[string]bool{}
			for _, raw := range cfg.Selection.SkipContainers {
				n := strings.TrimPrefix(strings.TrimSpace(raw), "docker:")
				if n != "" {
					skip[n] = true
				}
			}
			for _, raw := range cfg.Selection.Containers {
				name := strings.TrimPrefix(strings.TrimSpace(raw), "docker:")
				if name == "" || skip[name] {
					continue
				}
				dock = append(dock, Item{
					ID: "docker:" + name, Kind: source.KindDockerContainer, Name: name,
				})
			}
			for _, proj := range cfg.Selection.ComposeProjects {
				proj = strings.TrimSpace(proj)
				if proj == "" {
					continue
				}
				dock = append(dock, Item{
					ID: "compose:" + proj, Kind: source.KindDockerContainer, Name: proj,
					Note: "Compose project on this engine. Containers not listed live yet.",
				})
			}
		}
		if len(dock) == 0 {
			out = append(out, Item{
				ID: "docker", Kind: source.KindDockerContainer, Name: "Docker Containers",
				Note: "No containers listed yet. Add names under Containers to Bag, or run where Docker is listening.",
			})
		} else {
			out = append(out, dock...)
		}
	}
	if cfg.Selection.K8s {
		role := cfg.Role
		if role == "" {
			role = config.RoleHouse
		}
		if cfg.Selection.IncludeControlPlane(role) {
			out = append(out, Item{
				ID: "etcd", Kind: "k8s.etcd", Name: "etcd Keyspace", Sequence: true,
				Note: "Control plane restore is a sourced sequence, not a live-data-dir button. Whole-cluster keyspace — house bag only.",
			})
			out = append(out, Item{
				ID: "controlplane", Kind: "k8s.controlplane", Name: "Control Plane Files", Sequence: true,
				Note: "/etc/kubernetes, kubelet, kubeadm-config, and /etc/etcd if that tree is present. This node.",
			})
			out = append(out, Item{ID: "pvc", Kind: "k8s.pvc", Name: "Persistent Volumes",
				Note: "Workload discover is a stub. Namespace filters still record the intended scope.",
			})
		}
		for _, ns := range cfg.Selection.Namespaces {
			ns = strings.TrimSpace(ns)
			if ns == "" {
				continue
			}
			out = append(out, Item{
				ID: "k8s.ns:" + ns, Kind: "k8s.namespace", Name: ns,
				Note: "Namespace scope on the shared or tenant engine. PVC/workload discover is a stub; dumps still bag.",
			})
		}
	}
	if cfg.Selection.LXC {
		lx := itemsOf(found, source.KindLXCInstance)
		if len(lx) == 0 {
			out = append(out, Item{
				ID: "lxc", Kind: source.KindLXCInstance, Name: "LXC Instances",
				Note: "No instances listed yet.",
			})
		} else {
			out = append(out, lx...)
		}
	}
	if cfg.Selection.BareMetal {
		note := "This node's filesystem only. Other Swarm/Kubernetes nodes need their own cell."
		if len(cfg.Selection.HostPaths) > 0 {
			note = strings.Join(cfg.Selection.HostPaths, ", ") + ". " + note
		}
		out = append(out, Item{ID: "host", Kind: "host.path", Name: "Host Paths", Note: note, Paths: cfg.Selection.HostPaths})
	}
	return out
}

func itemsOf(found []source.Instance, kind string) []Item {
	var out []Item
	for _, in := range found {
		if in.Kind != kind {
			continue
		}
		out = append(out, Item{
			ID: in.ID, Kind: in.Kind, Name: in.Name, Note: in.Note, Paths: in.Paths,
		})
	}
	return out
}

// ControlPlaneSteps is the kubeadm-safe restore order. Never point the
// live etcd data-dir at a snapshot from the console button.
var ControlPlaneSteps = []string{
	"Stop every API server first.",
	"Restore etcd into a new data-dir (never the live one).",
	"Point etcd.yaml hostPath and kubeadm-config dataDir at that dir.",
	"Bring back /etc/etcd only if that tree existed on the node.",
}

// SequenceKind is a control-plane restore: write-beside is fine, pointing
// the live etcd data-dir at the snapshot is a sourced sequence, not a button.
func SequenceKind(kind string) bool {
	return kind == "k8s.etcd" || kind == "k8s.controlplane"
}

// RestorableKind can be written beside the live tree with engine.Restore.
func RestorableKind(kind string) bool {
	switch kind {
	case "pannier.self", "db.dump", "docker.container", "lxc.instance", "host.path", "k8s.pvc":
		return true
	default:
		return false
	}
}

// AttachSnapshots fills SnapshotID / Restorable from engine snapshots
// tagged lane=kind and optional entry=id.
func AttachSnapshots(items []Item, snaps []engine.Snapshot) []Item {
	idx := make(map[string]engine.Snapshot, len(snaps))
	for _, s := range snaps {
		if s.Tags == nil {
			continue
		}
		lane := s.Tags["lane"]
		if lane == "" {
			continue
		}
		idx[lane] = s
		if e := s.Tags["entry"]; e != "" {
			idx[lane+":"+e] = s
		}
	}
	out := append([]Item(nil), items...)
	for i := range out {
		s, ok := idx[out[i].Kind+":"+out[i].ID]
		if !ok {
			s, ok = idx[out[i].Kind]
		}
		if !ok {
			continue
		}
		out[i].SnapshotID = s.ID
		if RestorableKind(out[i].Kind) {
			out[i].Restorable = true
			if out[i].Note == "Discover is not wired yet." {
				out[i].Note = ""
			}
		}
	}
	return out
}
