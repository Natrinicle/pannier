package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateCellRejectsMaintenanceCreds(t *testing.T) {
	ok := File{Remotes: []Remote{{Name: "off", IngestCreds: &Creds{AccessKeyFile: "/etc/pannier/ingest"}}}}
	if err := ok.ValidateCell(); err != nil {
		t.Fatal(err)
	}
	bad := File{Remotes: []Remote{{Name: "off", MaintenanceCreds: &Creds{AccessKeyFile: "/etc/pannier/gc"}}}}
	if err := bad.ValidateCell(); err == nil {
		t.Fatal("expected cell to reject maintenanceCreds")
	}
}

func TestRedactScrubsInlineSecretsKeepsPaths(t *testing.T) {
	in := File{Remotes: []Remote{{
		Name: "off",
		IngestCreds: &Creds{
			AccessKeyFile: "/etc/pannier/ingest-id",
			AccessKey:     "AKIAEXAMPLE",
			SecretKey:     "wJalr",
		},
	}}}
	out := Redact(in)
	if out.Remotes[0].IngestCreds.AccessKey != "***" || out.Remotes[0].IngestCreds.SecretKey != "***" {
		t.Fatalf("secrets not redacted: %+v", out.Remotes[0].IngestCreds)
	}
	if out.Remotes[0].IngestCreds.AccessKeyFile != "/etc/pannier/ingest-id" {
		t.Fatal("file path is not a secret and must remain for restore")
	}
	if in.Remotes[0].IngestCreds.AccessKey != "AKIAEXAMPLE" {
		t.Fatal("Redact must copy")
	}
}

func TestNormalizeOffsideExclusive(t *testing.T) {
	lockWins := Remote{ObjectLock: true, LiveRepo: true}
	lockWins.NormalizeOffside()
	if !lockWins.ObjectLock || lockWins.LiveRepo {
		t.Fatalf("both true must keep Object Lock: %+v", lockWins)
	}
	live := Remote{LiveRepo: true}
	live.NormalizeOffside()
	if live.ObjectLock || !live.LiveRepo {
		t.Fatalf("live must clear Object Lock: %+v", live)
	}
	zero := Remote{}
	zero.NormalizeOffside()
	if !zero.ObjectLock || zero.LiveRepo {
		t.Fatalf("zero value defaults to Locked Packs: %+v", zero)
	}
}

func TestHouseAllowsOpenSelection(t *testing.T) {
	house := File{
		Selection: Selection{Docker: true, K8s: true, BareMetal: true, ControlPlane: true},
	}.House()
	if err := house.Validate(); err != nil {
		t.Fatal(err)
	}
	if !house.Selection.IncludeControlPlane(RoleHouse) {
		t.Fatal("house with Control Plane includes etcd")
	}
}

func TestHouseNamespacesShareEngine(t *testing.T) {
	f := File{
		LocalRepo: LocalRepo{Path: "/var/lib/pannier/repo"},
		Selection: Selection{K8s: true, Namespaces: []string{"acme", "beta"}},
		Satchels: []Satchel{{
			Name:      "acme",
			Selection: Selection{K8s: true, Namespaces: []string{"acme"}},
			LocalRepo: LocalRepo{Path: "/var/lib/pannier/satchels/acme"},
		}},
	}
	all := f.AllSatchels()
	if len(all) != 2 || all[0].Name != HouseName || all[1].Name != "acme" {
		t.Fatalf("%+v", all)
	}
	if all[0].Apply(f).LocalRepo.Path != "/var/lib/pannier/repo" {
		t.Fatal("house must keep the shared engine path")
	}
	if all[1].Apply(f).LocalRepo.Path != "/var/lib/pannier/satchels/acme" {
		t.Fatal("tenant must use its own directory")
	}
	if all[0].Selection.IncludeControlPlane(RoleHouse) {
		t.Fatal("house with a namespace filter omits etcd unless ControlPlane is set")
	}
	if all[1].Selection.IncludeControlPlane(RoleTenant) {
		t.Fatal("tenant must never receive etcd")
	}
}

func TestTenantRejectsOpenDockerAndBareEtcd(t *testing.T) {
	open := Satchel{Name: "acme", Role: RoleTenant, Selection: Selection{Docker: true}}
	if err := open.Validate(); err == nil {
		t.Fatal("tenant docker without names must fail")
	}
	etcd := Satchel{Name: "acme", Role: RoleTenant, Selection: Selection{K8s: true}}
	if err := etcd.Validate(); err == nil {
		t.Fatal("tenant k8s without namespaces must fail")
	}
	ok := Satchel{Name: "acme", Role: RoleTenant, Selection: Selection{K8s: true, Namespaces: []string{"acme"}}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestLoadWriteRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "pannier.yaml")
	f := Default()
	f.LocalRepo.Path = "/var/lib/pannier/repo"
	if err := Write(p, f); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Listen != "127.0.0.1:3477" {
		t.Fatalf("listen %q", got.Listen)
	}
	b, _ := os.ReadFile(p)
	if strings.Contains(string(b), "maintenanceCreds") && strings.Contains(string(b), "secretKey: w") {
		t.Fatal("unexpected secret in written YAML")
	}
}
