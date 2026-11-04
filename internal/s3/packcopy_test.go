package s3

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCopyMissingPutsOnlyAbsentNeverDeletes(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dst := NewFileStore(dir)
	if err := dst.Put(ctx, "keep", strings.NewReader("aaa"), 3); err != nil {
		t.Fatal(err)
	}
	if err := dst.Put(ctx, "extra-dest-only", strings.NewReader("zzz"), 3); err != nil {
		t.Fatal(err)
	}
	src := []Object{
		{Key: "keep", Size: 3},
		{Key: "new", Size: 2},
	}
	open := func(_ context.Context, o Object) (io.Reader, int64, error) {
		if o.Key == "keep" {
			t.Fatal("open called for a key that List already showed; would have been a Head-shaped extra read")
		}
		return strings.NewReader("xx"), 2, nil
	}
	res, err := CopyMissing(ctx, src, open, dst)
	if err != nil {
		t.Fatal(err)
	}
	if res.ListedSrc != 2 || res.ListedDst != 2 || res.Put != 1 || res.Skipped != 1 {
		t.Fatalf("result %+v", res)
	}
	keys, err := dst.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, k := range keys {
		have[k] = true
	}
	if !have["keep"] || !have["new"] || !have["extra-dest-only"] {
		t.Fatalf("keys %v: dest-only object must survive (never delete)", keys)
	}
}

func TestFileStoreRejectsDotDot(t *testing.T) {
	dst := NewFileStore(t.TempDir())
	err := dst.Put(context.Background(), "../escape", strings.NewReader("x"), 1)
	if err == nil {
		t.Fatal("expected path-traversal reject")
	}
}

func TestOpenFileURL(t *testing.T) {
	root := t.TempDir()
	st, err := Open("file://"+root, "bag", "cell/")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Put(context.Background(), "p1", strings.NewReader("ab"), 2); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "bag", "cell", "p1"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "ab" {
		t.Fatalf("got %q", b)
	}
}
