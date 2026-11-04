package pipeline

import (
	"testing"

	"pannier.dev/internal/config"
	"pannier.dev/internal/engine"
)

func TestCatalogSelectionAndDumps(t *testing.T) {
	cfg := config.File{
		SelfInclude: true,
		Selection:   config.Selection{Docker: true, K8s: true, BareMetal: true, ControlPlane: true},
		Dumps:       []config.Dump{{Name: "appdb", Kind: "postgres"}},
	}
	cat := Catalog(cfg)
	got := map[string]Item{}
	for _, it := range cat {
		got[it.ID] = it
	}
	for _, id := range []string{"self", "appdb", "docker", "etcd", "controlplane", "pvc", "host"} {
		if _, ok := got[id]; !ok {
			t.Fatalf("missing %s in %+v", id, cat)
		}
	}
	if !got["etcd"].Sequence || RestorableKind("k8s.etcd") {
		t.Fatal("etcd is a sequence, not a restorable live-dir write")
	}
	if !RestorableKind("db.dump") || !RestorableKind("docker.container") {
		t.Fatal("dumps and containers should restore beside")
	}
}

func TestCatalogNamedContainers(t *testing.T) {
	cfg := config.File{
		Selection: config.Selection{
			Docker:     true,
			Containers: []string{"web", "docker:db"},
		},
		Dumps: []config.Dump{{Name: "appdb", Kind: "postgres"}},
	}
	cat := Catalog(cfg)
	got := map[string]Item{}
	for _, it := range cat {
		got[it.ID] = it
	}
	if _, ok := got["docker"]; ok {
		t.Fatal("named containers must replace the lumped docker row")
	}
	if got["docker:web"].Kind != "docker.container" || got["docker:db"].Name != "db" {
		t.Fatalf("%+v", cat)
	}
	if got["appdb"].Kind != "db.dump" {
		t.Fatalf("dump %+v", got["appdb"])
	}
}

func TestCatalogTenantOmitsEtcd(t *testing.T) {
	cfg := config.File{
		Role:      config.RoleTenant,
		Selection: config.Selection{K8s: true, Namespaces: []string{"acme"}},
	}
	for _, it := range Catalog(cfg) {
		if it.Kind == "k8s.etcd" || it.Kind == "k8s.controlplane" {
			t.Fatalf("tenant catalog leaked control plane: %+v", it)
		}
		if it.ID == "k8s.ns:acme" && it.Kind != "k8s.namespace" {
			t.Fatalf("%+v", it)
		}
	}
}

func TestCatalogHouseNamespacesShareCluster(t *testing.T) {
	cfg := config.File{
		Selection: config.Selection{
			K8s:          true,
			ControlPlane: true,
			Namespaces:   []string{"acme", "beta"},
		},
	}
	got := map[string]Item{}
	for _, it := range Catalog(cfg) {
		got[it.ID] = it
	}
	for _, id := range []string{"etcd", "k8s.ns:acme", "k8s.ns:beta"} {
		if _, ok := got[id]; !ok {
			t.Fatalf("missing %s in %+v", id, got)
		}
	}
}

func TestAttachSnapshotsMarksRestorable(t *testing.T) {
	items := Catalog(config.File{SelfInclude: true, Dumps: []config.Dump{{Name: "appdb"}}})
	snaps := []engine.Snapshot{
		{ID: "s-self", Tags: map[string]string{"lane": "pannier.self", "entry": "self"}},
		{ID: "s-db", Tags: map[string]string{"lane": "db.dump", "entry": "appdb"}},
	}
	out := AttachSnapshots(items, snaps)
	by := map[string]Item{}
	for _, it := range out {
		by[it.ID] = it
	}
	if !by["self"].Restorable || by["self"].SnapshotID != "s-self" {
		t.Fatalf("self %+v", by["self"])
	}
	if !by["appdb"].Restorable || by["appdb"].SnapshotID != "s-db" {
		t.Fatalf("appdb %+v", by["appdb"])
	}
}
