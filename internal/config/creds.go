package config

import (
	"fmt"
	"os"
	"strings"

	"pannier.dev/internal/engine"
)

// Materialize reads ingest keys from files when set. Inline keys are a
// last resort for tests; self-include redacts them.
func (c *Creds) Materialize() (access, secret string, err error) {
	if c == nil {
		return "", "", nil
	}
	access = strings.TrimSpace(c.AccessKey)
	secret = strings.TrimSpace(c.SecretKey)
	if c.AccessKeyFile != "" {
		b, err := os.ReadFile(c.AccessKeyFile)
		if err != nil {
			return "", "", fmt.Errorf("access key file: %w", err)
		}
		access = strings.TrimSpace(string(b))
	}
	if c.SecretKeyFile != "" {
		b, err := os.ReadFile(c.SecretKeyFile)
		if err != nil {
			return "", "", fmt.Errorf("secret key file: %w", err)
		}
		secret = strings.TrimSpace(string(b))
	}
	return access, secret, nil
}

// ObjectDest is the ingest-side destination for pack-copy. Secrets come
// from ingestCreds files, never from maintenanceCreds.
func (r Remote) ObjectDest() (engine.ObjectDest, error) {
	access, secret, err := r.IngestCreds.Materialize()
	if err != nil {
		return engine.ObjectDest{}, err
	}
	return engine.ObjectDest{
		Endpoint:   r.Endpoint,
		Bucket:     r.Bucket,
		Prefix:     r.Prefix,
		Region:     r.Region,
		DisableTLS: r.DisableTLS,
		ObjectLock: r.ObjectLock,
		LiveRepo:   r.LiveRepo,
		AccessKey:  access,
		SecretKey:  secret,
		Role:       engine.RoleIngest,
	}, nil
}
