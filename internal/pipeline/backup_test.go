package pipeline

import (
	"context"
	"testing"

	"pannier.dev/internal/config"
	"pannier.dev/internal/engine"
)

func TestBackupFakeCell(t *testing.T) {
	dir := t.TempDir()
	p := Pipeline{
		Cfg: config.File{
			Engine:      engine.FakeKind,
			DataDir:     dir,
			SelfInclude: true,
			Selection:   config.Selection{},
			Retention:   config.Retention{KeepLast: 3},
		},
		Eng: engine.NewFake(),
	}
	res, err := p.Backup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.ID == "" || len(res.Contents) == 0 {
		t.Fatalf("bag should record contents: %+v", res)
	}
	if res.Contents[0].ID != "self" || !res.Contents[0].Restorable {
		t.Fatalf("self-include should be restorable: %+v", res.Contents[0])
	}
}

func TestBackupRefusesStewardOnCell(t *testing.T) {
	dir := t.TempDir()
	p := Pipeline{
		Cfg: config.File{
			Engine:  engine.FakeKind,
			DataDir: dir,
			Remotes: []config.Remote{{
				Name:             "off",
				MaintenanceCreds: &config.Creds{AccessKeyFile: "/nope"},
			}},
		},
		Eng: engine.NewFake(),
	}
	if _, err := p.Backup(context.Background()); err == nil {
		t.Fatal("cell with Steward keys must fail")
	}
}

func TestBackupAllHouseAndTenantSeparateRepos(t *testing.T) {
	t.Setenv("DOCKER_HOST", "tcp://127.0.0.1:1")
	dir := t.TempDir()
	fake := engine.NewFake()
	p := Pipeline{
		Cfg: config.File{
			Engine:      engine.FakeKind,
			DataDir:     dir,
			SelfInclude: true,
			LocalRepo:   config.LocalRepo{Path: dir + "/house"},
			Selection:   config.Selection{Docker: true, Containers: []string{"web"}},
			Retention:   config.Retention{KeepLast: 1},
			Satchels: []config.Satchel{{
				Name:      "acme",
				LocalRepo: config.LocalRepo{Path: dir + "/acme"},
				Selection: config.Selection{K8s: true, Namespaces: []string{"acme"}},
				Dumps:     []config.Dump{{Name: "acmedb", Command: []string{"true"}}},
			}},
		},
		Eng: fake,
	}
	results, err := p.BackupAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("want house+tenant got %d", len(results))
	}
	if results[0].Satchel != "house" || results[1].Satchel != "acme" {
		t.Fatalf("%q %q", results[0].Satchel, results[1].Satchel)
	}
	house, err := fake.List(context.Background(), engine.Repo{Path: dir + "/house"})
	if err != nil {
		t.Fatal(err)
	}
	acme, err := fake.List(context.Background(), engine.Repo{Path: dir + "/acme"})
	if err != nil {
		t.Fatal(err)
	}
	houseHasWeb := false
	for _, s := range house {
		if s.Tags["entry"] == "docker:web" {
			houseHasWeb = true
		}
		if s.Tags["entry"] == "acmedb" {
			t.Fatal("customer dump must not land in the shared engine")
		}
	}
	if !houseHasWeb {
		t.Fatalf("house snaps %+v", house)
	}
	acmeHasDump := false
	for _, s := range acme {
		if s.Tags["entry"] == "acmedb" {
			acmeHasDump = true
		}
		if s.Tags["entry"] == "docker:web" {
			t.Fatal("house container must not land in the tenant directory")
		}
		if s.Tags["lane"] == "k8s.etcd" {
			t.Fatal("tenant must not snapshot etcd")
		}
	}
	if !acmeHasDump {
		t.Fatalf("acme snaps %+v", acme)
	}
}

func TestBackupNamedContainerAndDump(t *testing.T) {
	t.Setenv("DOCKER_HOST", "tcp://127.0.0.1:1")
	dir := t.TempDir()
	p := Pipeline{
		Cfg: config.File{
			Engine:      engine.FakeKind,
			DataDir:     dir,
			SelfInclude: true,
			Selection: config.Selection{
				Docker:     true,
				Containers: []string{"web"},
			},
			Dumps:     []config.Dump{{Name: "appdb", Command: []string{"true"}}},
			Retention: config.Retention{KeepLast: 1},
		},
		Eng: engine.NewFake(),
	}
	res, err := p.Backup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Item{}
	for _, it := range res.Contents {
		by[it.ID] = it
	}
	if !by["appdb"].Restorable || by["appdb"].SnapshotID == "" {
		t.Fatalf("dump %+v", by["appdb"])
	}
	if !by["docker:web"].Restorable || by["docker:web"].SnapshotID == "" {
		t.Fatalf("container %+v contents=%+v", by["docker:web"], res.Contents)
	}
}
