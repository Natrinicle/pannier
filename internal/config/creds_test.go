package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCredsMaterializeFromFiles(t *testing.T) {
	dir := t.TempDir()
	access := filepath.Join(dir, "access")
	secret := filepath.Join(dir, "secret")
	if err := os.WriteFile(access, []byte(" AKIA \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secret, []byte("s3cret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := &Creds{AccessKeyFile: access, SecretKeyFile: secret}
	a, s, err := c.Materialize()
	if err != nil {
		t.Fatal(err)
	}
	if a != "AKIA" || s != "s3cret" {
		t.Fatalf("got %q %q", a, s)
	}
}

func TestNilCredsMaterialize(t *testing.T) {
	var c *Creds
	a, s, err := c.Materialize()
	if err != nil || a != "" || s != "" {
		t.Fatalf("nil creds: %q %q %v", a, s, err)
	}
}

func TestRemoteObjectDestUsesIngestFiles(t *testing.T) {
	dir := t.TempDir()
	access := filepath.Join(dir, "access")
	secret := filepath.Join(dir, "secret")
	if err := os.WriteFile(access, []byte("AKIA\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secret, []byte("s3cret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := Remote{
		Endpoint:    "minio:9000",
		Bucket:      "bag",
		Prefix:      "cell/",
		DisableTLS:  true,
		ObjectLock:  true,
		IngestCreds: &Creds{AccessKeyFile: access, SecretKeyFile: secret},
	}
	d, err := r.ObjectDest()
	if err != nil {
		t.Fatal(err)
	}
	if d.AccessKey != "AKIA" || d.SecretKey != "s3cret" {
		t.Fatalf("dest keys %q %q", d.AccessKey, d.SecretKey)
	}
	if d.Endpoint != "minio:9000" || d.Bucket != "bag" || !d.DisableTLS {
		t.Fatalf("dest %+v", d)
	}
}
