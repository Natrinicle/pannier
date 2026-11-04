package source

import (
	"context"
	"os"
	"path/filepath"
)

// ListLXC names directories under /var/lib/lxc. Missing root is empty,
// not an error.
func ListLXC(_ context.Context) ([]Instance, error) {
	root := "/var/lib/lxc"
	ents, err := os.ReadDir(root)
	if err != nil {
		return nil, nil
	}
	var out []Instance
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		name := bareName(e.Name())
		if name == "" {
			continue
		}
		path := filepath.Join(root, name)
		out = append(out, Instance{
			Kind:  KindLXCInstance,
			ID:    "lxc:" + name,
			Name:  name,
			Paths: []string{path},
		})
	}
	return out, nil
}
