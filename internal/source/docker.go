package source

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DockerSock is the local engine socket. tcp:// DOCKER_HOST is refused
// (the cell must not be turned into an SSRF trampoline).
func DockerSock() string {
	h := strings.TrimSpace(os.Getenv("DOCKER_HOST"))
	if strings.HasPrefix(h, "unix://") {
		return strings.TrimPrefix(h, "unix://")
	}
	if h != "" {
		return ""
	}
	if xdg := os.Getenv("XDG_RUNTIME_DIR"); xdg != "" {
		p := filepath.Join(xdg, "docker.sock")
		if isSock(p) {
			return p
		}
	}
	if isSock("/var/run/docker.sock") {
		return "/var/run/docker.sock"
	}
	return ""
}

func isSock(p string) bool {
	fi, err := os.Stat(p)
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeSocket != 0
}

type dockerCtr struct {
	ID     string            `json:"Id"`
	Names  []string          `json:"Names"`
	Mounts []dockerMount     `json:"Mounts"`
	Labels map[string]string `json:"Labels"`
}

type dockerMount struct {
	Type   string `json:"Type"`
	Name   string `json:"Name"`
	Source string `json:"Source"`
}

// ListDocker talks to the local engine over a unix socket. Overlay2
// layers stay out of the bag; only bind mounts and named-volume data
// dirs that this process can stat.
func ListDocker(ctx context.Context) ([]Instance, error) {
	sock := DockerSock()
	if sock == "" {
		return nil, nil
	}
	client := &http.Client{
		Timeout: 8 * time.Second,
		Transport: &http.Transport{
			DisableKeepAlives: true,
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				d := net.Dialer{Timeout: 3 * time.Second}
				return d.DialContext(ctx, "unix", sock)
			},
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/v1.41/containers/json?all=true", nil)
	if err != nil {
		return nil, err
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("docker list: %w", err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("docker list: HTTP %d", res.StatusCode)
	}
	var raw []dockerCtr
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("docker list: %w", err)
	}
	out := make([]Instance, 0, len(raw))
	for _, c := range raw {
		name := containerName(c.Names)
		if name == "" {
			name = shortID(c.ID)
		}
		if name == "" {
			continue
		}
		paths := mountPaths(c.Mounts)
		note := ""
		if len(paths) == 0 {
			note = "No readable bind mounts; bag keeps a marker."
		}
		out = append(out, Instance{
			Kind:    KindDockerContainer,
			ID:      "docker:" + name,
			Name:    name,
			Paths:   paths,
			Note:    note,
			Project: composeProject(c.Labels),
		})
	}
	return out, nil
}

func composeProject(labels map[string]string) string {
	if labels == nil {
		return ""
	}
	return strings.TrimSpace(labels["com.docker.compose.project"])
}

func containerName(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return bareName(names[0])
}

func shortID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func mountPaths(ms []dockerMount) []string {
	var paths []string
	seen := map[string]bool{}
	for _, m := range ms {
		kind := strings.ToLower(m.Type)
		src := m.Source
		if kind == "volume" && src == "" && m.Name != "" {
			src = filepath.Join("/var/lib/docker/volumes", m.Name, "_data")
		}
		if kind != "bind" && kind != "volume" {
			continue
		}
		if src == "" || seen[src] || !readable(src) {
			continue
		}
		seen[src] = true
		paths = append(paths, src)
	}
	return paths
}

func readable(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
