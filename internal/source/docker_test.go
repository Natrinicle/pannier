package source

import (
	"context"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"pannier.dev/internal/config"
)

func TestListDockerUnix(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "docker.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.41/containers/json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[{"Id":"abc123def456","Names":["/web"],"Mounts":[{"Type":"bind","Source":"`+dir+`","Destination":"/data"}]}]`)
	})
	go func() { _ = http.Serve(ln, mux) }()

	t.Setenv("DOCKER_HOST", "unix://"+sock)
	var got []Instance
	for i := 0; i < 25; i++ {
		got, err = ListDocker(context.Background())
		if err == nil && len(got) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "docker:web" || got[0].Name != "web" {
		t.Fatalf("%+v", got)
	}
	if len(got[0].Paths) != 1 || got[0].Paths[0] != dir {
		t.Fatalf("paths %+v", got[0].Paths)
	}
}

func TestDockerSockRefusesTCP(t *testing.T) {
	t.Setenv("DOCKER_HOST", "tcp://127.0.0.1:2375")
	if DockerSock() != "" {
		t.Fatal("tcp DOCKER_HOST must not be dialed")
	}
	got, err := ListDocker(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestPickIncludeWithoutSocket(t *testing.T) {
	t.Setenv("DOCKER_HOST", "tcp://127.0.0.1:1")
	got := Pick(context.Background(), config.Selection{
		Docker:         true,
		Containers:     []string{"web"},
		SkipContainers: []string{"skip-me"},
	})
	if len(got) != 1 || got[0].ID != "docker:web" {
		t.Fatalf("%+v", got)
	}
	if got[0].Note == "" {
		t.Fatal("missing-not-running should keep a marker note")
	}
}

func TestPickComposeProject(t *testing.T) {
	live := []Instance{
		{Kind: KindDockerContainer, ID: "docker:web", Name: "web", Project: "shop"},
		{Kind: KindDockerContainer, ID: "docker:db", Name: "db", Project: "shop"},
		{Kind: KindDockerContainer, ID: "docker:other", Name: "other", Project: "internal"},
	}
	got := filterDocker(live, config.Selection{
		Docker:          true,
		ComposeProjects: []string{"shop"},
		SkipContainers:  []string{"db"},
	})
	if len(got) != 1 || got[0].Name != "web" {
		t.Fatalf("%+v", got)
	}
}

func TestPickSkip(t *testing.T) {
	live := []Instance{
		{Kind: KindDockerContainer, ID: "docker:web", Name: "web"},
		{Kind: KindDockerContainer, ID: "docker:cache", Name: "cache"},
	}
	got := filter(live, KindDockerContainer, "docker:", nil, []string{"cache"})
	if len(got) != 1 || got[0].Name != "web" {
		t.Fatalf("%+v", got)
	}
}
