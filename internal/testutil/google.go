// Package testutil supplies HTTP fixtures; it never contacts Google.
package testutil

import (
	"archive/zip"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"playops/internal/googleapi"
)

type rewrite struct {
	target *url.URL
	base   http.RoundTripper
}

func (r rewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	copy := req.Clone(req.Context())
	u := *req.URL
	u.Scheme = r.target.Scheme
	u.Host = r.target.Host
	copy.URL = &u
	return r.base.RoundTrip(copy)
}
func Google(t *testing.T, handler http.HandlerFunc) *googleapi.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	target, _ := url.Parse(server.URL)
	client, err := googleapi.WithHTTP(context.Background(), &http.Client{Transport: rewrite{target: target, base: server.Client().Transport}})
	if err != nil {
		t.Fatal(err)
	}
	return client
}
func Bundle(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "app.aab")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	for _, name := range []string{"BundleConfig.pb", "base/manifest/AndroidManifest.xml", "META-INF/UPLOAD.SF", "META-INF/UPLOAD.RSA"} {
		w, e := z.Create(name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = w.Write([]byte("fixture, not a real signed artifact")); e != nil {
			t.Fatal(e)
		}
	}
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
