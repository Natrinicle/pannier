package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

func Load(path string) (File, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return File{}, err
	}
	var f File
	if err := yaml.Unmarshal(b, &f); err != nil {
		return File{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if f.Listen == "" {
		f.Listen = "127.0.0.1:3477"
	}
	if f.Engine == "" {
		f.Engine = Default().Engine
	}
	NormalizeFile(&f)
	return f, nil
}

func NormalizeFile(f *File) {
	if f == nil {
		return
	}
	NormalizeRemotes(f.Remotes)
	for i := range f.Satchels {
		NormalizeRemotes(f.Satchels[i].Remotes)
	}
}

func Write(path string, f File) error {
	NormalizeFile(&f)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := yaml.Marshal(f)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
