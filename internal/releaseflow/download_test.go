package releaseflow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pub "google.golang.org/api/androidpublisher/v3"
	"playops/internal/fault"
	"playops/internal/testutil"
)

type assetTransport func(*http.Request) (*http.Response, error)

func (f assetTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestDownloadsRequireExactOriginalBytes(t *testing.T) {
	dir := t.TempDir()
	path := testutil.RGBAFile(t, filepath.Join(dir, "source.png"), 255)
	data, _ := os.ReadFile(path)
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	expected := []Image{{ID: "one", SHA256: hash}, {ID: "two", SHA256: hash}}
	rows := []*pub.Image{{Id: "one", Sha256: hash, Url: "https://play-lh.googleusercontent.com/one"}, {Id: "two", Sha256: hash, Url: "https://play-lh.googleusercontent.com/two"}}
	calls := 0
	bad := false
	transport := assetTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("Authorization") != "" {
			t.Error("credentials leaked")
		}
		body := data
		if bad && r.URL.Path != "/one" && r.URL.Path != "/one=s0" {
			body = []byte("different preview")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{}}, nil
	})
	out := filepath.Join(dir, "verified")
	files, e := DownloadImages(context.Background(), expected, rows, out, transport)
	if e != nil || len(files) != 2 {
		t.Fatalf("download %v %v", files, e)
	}
	for _, f := range files {
		b, e := os.ReadFile(f.Path)
		if e != nil || !bytes.Equal(b, data) {
			t.Fatal("download changed original")
		}
	}
	if _, e = DownloadImages(context.Background(), expected, rows, out, transport); e == nil {
		t.Fatal("overwrote existing directory")
	}
	bad = true
	out = filepath.Join(dir, "mismatch")
	if _, e = DownloadImages(context.Background(), expected, rows, out, transport); e == nil || fault.From(e).Code != "IMAGE_ORIGINAL_UNAVAILABLE" {
		t.Fatalf("mismatch accepted %v", e)
	}
	if _, e = os.Stat(out); !os.IsNotExist(e) {
		t.Fatal("partial originals left behind")
	}
	before := calls
	rows[0].Sha256 = "changed"
	if _, e = DownloadImages(context.Background(), expected, rows, filepath.Join(dir, "drift"), transport); e == nil || calls != before {
		t.Fatal("fetched after snapshot drift")
	}
	for _, url := range []string{"http://play-lh.googleusercontent.com/a", "https://localhost/a", "https://googleusercontent.com.evil.test/a", "https://user:secret@play-lh.googleusercontent.com/a"} {
		if imageURL(url) == nil {
			t.Fatalf("unsafe URL accepted %s", url)
		}
	}
}

func TestDownloadFallsBackToOriginalSizeWithExactHash(t *testing.T) {
	dir := t.TempDir()
	path := testutil.RGBAFile(t, filepath.Join(dir, "source.png"), 255)
	data, _ := os.ReadFile(path)
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	calls := 0
	transport := assetTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		body := []byte("compressed preview")
		if strings.HasSuffix(r.URL.Path, "=s0") {
			body = data
		}
		if r.URL.RawQuery != "token=example" || r.Header.Get("Authorization") != "" {
			t.Fatal("query changed or credentials sent")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{}}, nil
	})
	expected := []Image{{SHA256: hash}}
	rows := []*pub.Image{{Sha256: hash, Url: "https://play-lh.googleusercontent.com/image=w400?token=example"}}
	files, e := DownloadImages(context.Background(), expected, rows, filepath.Join(dir, "originals"), transport)
	if e != nil || len(files) != 1 || calls != 2 {
		t.Fatalf("fallback failed %+v %v calls=%d", files, e, calls)
	}
}
