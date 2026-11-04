package s3

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ObjectStore is the ingest-side object API: list and put. There is no
// Head and no Delete. Membership is decided from List alone so a
// write-only Object-Lock prefix never needs GetObject/HeadObject.
type ObjectStore interface {
	List(ctx context.Context, prefix string) ([]string, error)
	Put(ctx context.Context, key string, r io.Reader, size int64) error
}

type Object struct {
	Key     string
	Size    int64
	RelPath string
}

type Result struct {
	ListedSrc, ListedDst, Put, Skipped int
	Bytes                              int64
}

// CopyMissing lists destination keys once, then Put each source object
// whose key is absent. Extra destination keys are left alone (never
// delete). Do not Head — Object-Lock write-only creds often deny it.
func CopyMissing(ctx context.Context, src []Object, open func(context.Context, Object) (io.Reader, int64, error), dst ObjectStore) (Result, error) {
	var res Result
	res.ListedSrc = len(src)
	destKeys, err := dst.List(ctx, "")
	if err != nil {
		return res, err
	}
	res.ListedDst = len(destKeys)
	have := make(map[string]struct{}, len(destKeys))
	for _, k := range destKeys {
		have[k] = struct{}{}
	}
	for _, o := range src {
		key := o.Key
		if key == "" {
			key = o.RelPath
		}
		if _, ok := have[key]; ok {
			res.Skipped++
			continue
		}
		r, size, err := open(ctx, o)
		if err != nil {
			return res, fmt.Errorf("open %s: %w", key, err)
		}
		if size <= 0 {
			size = o.Size
		}
		if err := dst.Put(ctx, key, r, size); err != nil {
			if c, ok := r.(io.Closer); ok {
				_ = c.Close()
			}
			return res, fmt.Errorf("put %s: %w", key, err)
		}
		if c, ok := r.(io.Closer); ok {
			_ = c.Close()
		}
		res.Put++
		res.Bytes += size
	}
	return res, nil
}

// Open returns a filesystem store for file:// endpoints. Real S3 is a
// later drop-in behind the same interface; v0 copies packs locally for
// tests and for a dry berth.
func Open(endpoint, bucket, prefix string) (ObjectStore, error) {
	if strings.HasPrefix(endpoint, "file://") {
		root := strings.TrimPrefix(endpoint, "file://")
		root = filepath.Join(root, bucket, filepath.FromSlash(prefix))
		if err := os.MkdirAll(root, 0o700); err != nil {
			return nil, err
		}
		return NewFileStore(root), nil
	}
	return nil, fmt.Errorf("object store %s: HTTP S3 client is not in this draft; use file:// for tests", endpoint)
}

type FileStore struct {
	Root string
}

func NewFileStore(root string) *FileStore {
	return &FileStore{Root: root}
}

func (f *FileStore) List(_ context.Context, prefix string) ([]string, error) {
	if err := os.MkdirAll(f.Root, 0o700); err != nil {
		return nil, err
	}
	var keys []string
	err := filepath.Walk(f.Root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(f.Root, path)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(rel)
		if prefix == "" || strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
		return nil
	})
	return keys, err
}

func (f *FileStore) Put(_ context.Context, key string, r io.Reader, size int64) error {
	clean, err := safeJoin(f.Root, key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(clean), 0o700); err != nil {
		return err
	}
	tmp := clean + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	n, err := io.Copy(out, r)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if size > 0 && n != size {
		_ = os.Remove(tmp)
		return fmt.Errorf("put %s: wrote %d, expected %d", key, n, size)
	}
	return os.Rename(tmp, clean)
}

func safeJoin(root, key string) (string, error) {
	if key == "" || strings.Contains(key, "..") {
		return "", fmt.Errorf("refusing object key %q", key)
	}
	clean := filepath.Join(root, filepath.FromSlash(key))
	rel, err := filepath.Rel(root, clean)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("refusing object key %q", key)
	}
	return clean, nil
}
