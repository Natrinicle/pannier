package s3

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPStoreListPutNoHeadDelete(t *testing.T) {
	store := map[string]string{}
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		if r.Header.Get("Authorization") == "" {
			t.Error("missing Authorization")
		}
		switch r.Method {
		case http.MethodGet:
			var b strings.Builder
			b.WriteString(`<?xml version="1.0"?><ListBucketResult>`)
			for k := range store {
				b.WriteString("<Contents><Key>")
				b.WriteString(k)
				b.WriteString("</Key></Contents>")
			}
			b.WriteString(`<IsTruncated>false</IsTruncated></ListBucketResult>`)
			w.Write([]byte(b.String()))
		case http.MethodPut:
			body, _ := io.ReadAll(r.Body)
			key := strings.TrimPrefix(r.URL.Path, "/bag/")
			store[key] = string(body)
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected method %s (no Head/Delete)", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(srv.Close)

	st, err := OpenWith(Options{
		Endpoint:  srv.URL,
		Bucket:    "bag",
		Prefix:    "cell/",
		AccessKey: "AKIAEXAMPLE",
		SecretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		Region:    "us-east-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := st.Put(ctx, "p1", strings.NewReader("ab"), 2); err != nil {
		t.Fatal(err)
	}
	keys, err := st.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0] != "p1" {
		t.Fatalf("keys %v", keys)
	}
	for _, m := range methods {
		if m == http.MethodHead || m == http.MethodDelete {
			t.Fatalf("Locked Packs must not %s", m)
		}
	}
}

func TestOpenWithFileURL(t *testing.T) {
	root := t.TempDir()
	st, err := OpenWith(Options{Endpoint: "file://" + root, Bucket: "bag", Prefix: "cell/"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Put(context.Background(), "p1", strings.NewReader("ab"), 2); err != nil {
		t.Fatal(err)
	}
}
