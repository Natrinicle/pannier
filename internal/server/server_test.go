package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"pannier.dev/internal/config"
	"pannier.dev/internal/engine"
	"pannier.dev/internal/pipeline"
)

func TestListenAddrRejectsWAN(t *testing.T) {
	if err := ListenAddr("127.0.0.1:3477"); err != nil {
		t.Fatal(err)
	}
	if err := ListenAddr("0.0.0.0:3477"); err == nil {
		t.Fatal("WAN bind must be refused")
	}
	if err := ListenAddr("[::]:3477"); err == nil {
		t.Fatal("IPv6 WAN bind must be refused")
	}
}

func TestListenAddrContainerBind(t *testing.T) {
	if err := ListenAddr("0.0.0.0:3477"); err == nil {
		t.Fatal("WAN bind must stay refused without PANNIER_CONTAINER_BIND")
	}
	t.Setenv("PANNIER_CONTAINER_BIND", "1")
	if err := ListenAddr("0.0.0.0:3477"); err != nil {
		t.Fatal(err)
	}
	if err := ListenAddr("8.8.8.8:3477"); err == nil {
		t.Fatal("public unicast must stay refused even in a container")
	}
}

func TestHealthzUnauthenticatedBoolean(t *testing.T) {
	s := &Server{Password: "x"}
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("code %d", rec.Code)
	}
	if rec.Body.String() != "true" {
		t.Fatalf("body %q", rec.Body.String())
	}
}

func TestStatusRequiresAuth(t *testing.T) {
	s := &Server{Password: "x"}
	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatalf("code %d", rec.Code)
	}
}

func TestStaticUnauthenticated(t *testing.T) {
	s := &Server{
		Password: "x",
		Web: fstest.MapFS{
			"index.html": &fstest.MapFile{Data: []byte("<!doctype html><title>Pannier</title>")},
			"style.css":  &fstest.MapFile{Data: []byte("body{color:black}")},
			"app.js":     &fstest.MapFile{Data: []byte("window.pannier=1")},
		},
	}
	h := s.Handler()
	cases := []struct {
		path string
		ct   string
	}{
		{"/", "text/html"},
		{"/style.css", "text/css"},
		{"/app.js", "text/javascript"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("%s code %d body %q", tc.path, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Content-Type"); !strings.Contains(got, tc.ct) {
			t.Fatalf("%s content-type %q want %s", tc.path, got, tc.ct)
		}
	}
}

func TestAnomaliesEmptyArrayNotNull(t *testing.T) {
	s := &Server{
		Password: "x",
		Cfg:      config.File{Remotes: []config.Remote{{Name: "off"}}},
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/anomalies", nil)
	req.Header.Set("Authorization", "Bearer x")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("code %d", rec.Code)
	}
	body := strings.TrimSpace(rec.Body.String())
	if body != "[]" {
		t.Fatalf("anomalies %q (nil slice becomes JSON null and breaks boot)", body)
	}
}

func TestStatusOmitsZeroLastAt(t *testing.T) {
	s := &Server{Password: "x"}
	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	req.Header.Set("Authorization", "Bearer x")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("code %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "0001-01-01") {
		t.Fatalf("zero time leaked: %s", rec.Body.String())
	}
}

func TestBagsEmptyArrayNotNull(t *testing.T) {
	s := &Server{Password: "x"}
	req := httptest.NewRequest(http.MethodGet, "/v1/bags", nil)
	req.Header.Set("Authorization", "Bearer x")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("code %d", rec.Code)
	}
	body := strings.TrimSpace(rec.Body.String())
	if body != "[]" {
		t.Fatalf("bags %q", body)
	}
}

func TestRestoreSelectedItems(t *testing.T) {
	dir := t.TempDir()
	fake := engine.NewFake()
	s := &Server{
		Password: "x",
		DataDir:  dir,
		Eng:      fake,
		Bags: []pipeline.Bag{{
			ID: "bag-demo",
			Contents: []pipeline.Item{
				{ID: "self", Kind: "pannier.self", SnapshotID: "s-self", Restorable: true},
				{ID: "appdb", Kind: "db.dump", SnapshotID: "s-db", Restorable: true},
				{ID: "etcd", Kind: "k8s.etcd", Sequence: true},
			},
		}},
	}
	body := `{"bagId":"bag-demo","items":["appdb"]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/restore", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer x")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("code %d body %s", rec.Code, rec.Body.String())
	}
	got := fake.Restored()
	if len(got) != 1 || got[0].ID != "s-db" {
		t.Fatalf("restored %+v", got)
	}
	if !strings.Contains(got[0].Target, filepath.Join("restore", "bag-demo", "appdb")) {
		t.Fatalf("target %q", got[0].Target)
	}
}

func TestRestoreEntireBagSkipsSequence(t *testing.T) {
	dir := t.TempDir()
	fake := engine.NewFake()
	s := &Server{
		Password: "x",
		DataDir:  dir,
		Eng:      fake,
		Bags: []pipeline.Bag{{
			ID: "bag-demo",
			Contents: []pipeline.Item{
				{ID: "self", Kind: "pannier.self", SnapshotID: "s-self", Restorable: true},
				{ID: "etcd", Kind: "k8s.etcd", Sequence: true},
			},
		}},
	}
	body := `{"bagId":"bag-demo","items":["all"]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/restore", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer x")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("code %d body %s", rec.Code, rec.Body.String())
	}
	var out restoreOut
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Restored) != 1 || out.Restored[0] != "self" {
		t.Fatalf("restored %+v", out.Restored)
	}
	if len(out.Sequence) == 0 {
		t.Fatal("etcd should surface the control-plane sequence")
	}
}

func TestRestoreSelectedContainer(t *testing.T) {
	dir := t.TempDir()
	fake := engine.NewFake()
	s := &Server{
		Password: "x",
		DataDir:  dir,
		Eng:      fake,
		Bags: []pipeline.Bag{{
			ID: "bag-demo",
			Contents: []pipeline.Item{
				{ID: "docker:web", Kind: "docker.container", SnapshotID: "s-web", Restorable: true},
				{ID: "appdb", Kind: "db.dump", SnapshotID: "s-db", Restorable: true},
			},
		}},
	}
	body := `{"bagId":"bag-demo","items":["docker:web"]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/restore", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer x")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("code %d body %s", rec.Code, rec.Body.String())
	}
	got := fake.Restored()
	if len(got) != 1 || got[0].ID != "s-web" {
		t.Fatalf("restored %+v", got)
	}
	marker := filepath.Join(dir, "restore", "bag-demo", "docker:web", "RESTORED")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("beside file: %v", err)
	}
}

func TestRestoreRejectsLiveEtcdPath(t *testing.T) {
	s := &Server{
		Password: "x",
		DataDir:  t.TempDir(),
		Eng:      engine.NewFake(),
		Bags: []pipeline.Bag{{
			ID: "bag-demo",
			Contents: []pipeline.Item{
				{ID: "self", Kind: "pannier.self", SnapshotID: "s-self", Restorable: true},
			},
		}},
	}
	body := `{"bagId":"bag-demo","items":["self"],"target":"/var/lib/etcd"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/restore", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer x")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatalf("code %d body %s", rec.Code, rec.Body.String())
	}
}

func TestConfigRequiresAuth(t *testing.T) {
	s := &Server{Password: "x"}
	req := httptest.NewRequest(http.MethodGet, "/v1/config", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatalf("code %d", rec.Code)
	}
}
