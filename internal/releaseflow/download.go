package releaseflow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	pub "google.golang.org/api/androidpublisher/v3"
	"playops/internal/fault"
)

func imageURL(raw string) error {
	u, e := url.Parse(raw)
	if e != nil {
		return fault.Input("Invalid image preview URL")
	}
	h := strings.ToLower(u.Hostname())
	allowed := false
	for _, domain := range []string{"googleusercontent.com", "ggpht.com"} {
		if h == domain || strings.HasSuffix(h, "."+domain) {
			allowed = true
		}
	}
	if u.Scheme != "https" || u.User != nil || u.Fragment != "" || (u.Port() != "" && u.Port() != "443") || !allowed {
		return fault.New("UNSUPPORTED_IMAGE_URL", "Image URL must use HTTPS on a supported Google image host", 2)
	}
	return nil
}

func CheckDownloadOutput(out string) error {
	if e := CheckOutput(out); e != nil {
		return e
	}
	info, e := os.Stat(filepath.Dir(out))
	if e != nil || !info.IsDir() {
		return fault.Input("Output parent directory must already exist")
	}
	return nil
}

// DownloadImages never transforms previews or sends Google API credentials to
// them. Only byte-for-byte matches of the current snapshot hashes are accepted.
func DownloadImages(ctx context.Context, expected []Image, rows []*pub.Image, out string, transport http.RoundTripper) ([]File, error) {
	if err := CheckDownloadOutput(out); err != nil {
		return nil, err
	}
	if len(expected) == 0 {
		return nil, fault.New("NO_SOURCE_IMAGES", "Snapshot has no image originals to download; inheritance is unknown", 2)
	}
	if len(expected) != len(rows) {
		return nil, fault.Conflict("Image count changed since snapshot")
	}
	for i, want := range expected {
		if len(want.SHA256) != 64 {
			return nil, fault.Input("Snapshot image %d has no valid SHA-256", i+1)
		}
		if _, e := hex.DecodeString(want.SHA256); e != nil {
			return nil, fault.Input("Snapshot image %d has invalid SHA-256", i+1)
		}
		if rows[i] == nil || !strings.EqualFold(want.SHA256, rows[i].Sha256) {
			return nil, fault.Conflict("Image hash/order changed at snapshot position %d", i+1)
		}
		if e := imageURL(rows[i].Url); e != nil {
			return nil, e
		}
	}
	parent := filepath.Dir(out)
	dir, e := os.MkdirTemp(parent, ".playops-download-*")
	if e != nil {
		return nil, fault.Input("Output parent directory must exist and be writable")
	}
	defer os.RemoveAll(dir)
	if transport == nil {
		transport = http.DefaultTransport
	}
	client := &http.Client{Transport: transport, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fault.Input("Too many image redirects")
		}
		return imageURL(req.URL.String())
	}}
	files := []File{}
	for i, row := range rows {
		req, e := http.NewRequestWithContext(ctx, http.MethodGet, row.Url, nil)
		if e != nil {
			return nil, fault.Input("Invalid image request")
		}
		response, e := client.Do(req)
		if e != nil {
			if ctx.Err() != nil {
				return nil, fault.From(ctx.Err())
			}
			return nil, fault.New("IMAGE_DOWNLOAD_FAILED", "Cannot fetch preview image; original bytes have not been recovered", 5)
		}
		const limit = 32 << 20
		data, readErr := io.ReadAll(io.LimitReader(response.Body, limit+1))
		response.Body.Close()
		if response.StatusCode != http.StatusOK || readErr != nil || len(data) > limit {
			return nil, fault.New("IMAGE_DOWNLOAD_FAILED", "Image download failed or exceeds 32 MiB; no original accepted", 5)
		}
		h := sha256.Sum256(data)
		if !strings.EqualFold(hex.EncodeToString(h[:]), expected[i].SHA256) {
			e := fault.New("IMAGE_ORIGINAL_UNAVAILABLE", "Preview bytes do not match the snapshot SHA-256; supply your saved original assets", 4)
			e.Details = map[string]any{"position": i + 1, "expectedSha256": expected[i].SHA256, "actualSha256": hex.EncodeToString(h[:])}
			return nil, e
		}
		_, format, e := image.DecodeConfig(bytes.NewReader(data))
		if e != nil || (format != "png" && format != "jpeg") {
			return nil, fault.Input("Verified image %d is not a supported PNG/JPEG", i+1)
		}
		ext := "png"
		if format == "jpeg" {
			ext = "jpg"
		}
		name := fmt.Sprintf("%02d-original.%s", i+1, ext)
		if e = os.WriteFile(filepath.Join(dir, name), data, 0600); e != nil {
			return nil, e
		}
		path, e := filepath.Abs(filepath.Join(out, name))
		if e != nil {
			return nil, e
		}
		files = append(files, File{Path: path, SHA256: hex.EncodeToString(h[:]), Size: int64(len(data))})
	}
	// Reserve a new destination only after every image has passed verification.
	if e = os.Mkdir(out, 0700); e != nil {
		return nil, fault.Input("Output directory already exists or cannot be created")
	}
	success := false
	defer func() {
		if !success {
			os.RemoveAll(out)
		}
	}()
	for _, file := range files {
		name := filepath.Base(file.Path)
		if e = os.Rename(filepath.Join(dir, name), filepath.Join(out, name)); e != nil {
			return nil, e
		}
	}
	success = true
	return files, nil
}
