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
		data, e := fetchOriginal(ctx, client, row.Url, expected[i].SHA256, i+1)
		if e != nil {
			return nil, e
		}
		h := sha256.Sum256(data)
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

// Google image transformations are best-effort candidates, never trusted as
// originals without a snapshot digest match. Preserve the URL's query string.
func originalCandidates(raw string) []string {
	result := []string{raw}
	u, e := url.Parse(raw)
	if e != nil {
		return result
	}
	if i := strings.LastIndex(u.Path, "="); i >= 0 {
		u.Path = u.Path[:i]
	}
	u.Path += "=s0"
	u.RawPath = ""
	candidate := u.String()
	if candidate != raw {
		result = append(result, candidate)
	}
	return result
}
func fetchOriginal(ctx context.Context, client *http.Client, raw, want string, position int) ([]byte, error) {
	actual := ""
	attempts := 0
	retrieved := false
	for _, candidate := range originalCandidates(raw) {
		if e := imageURL(candidate); e != nil {
			return nil, e
		}
		req, e := http.NewRequestWithContext(ctx, http.MethodGet, candidate, nil)
		if e != nil {
			return nil, fault.Input("Invalid image request")
		}
		attempts++
		response, e := client.Do(req)
		if e != nil {
			if ctx.Err() != nil {
				return nil, fault.From(ctx.Err())
			}
			continue
		}
		const limit = 32 << 20
		data, readErr := io.ReadAll(io.LimitReader(response.Body, limit+1))
		response.Body.Close()
		if ctx.Err() != nil {
			return nil, fault.From(ctx.Err())
		}
		if response.StatusCode != http.StatusOK || readErr != nil || len(data) > limit {
			continue
		}
		retrieved = true
		h := sha256.Sum256(data)
		actual = hex.EncodeToString(h[:])
		if strings.EqualFold(actual, want) {
			return data, nil
		}
	}
	if !retrieved {
		return nil, fault.New("IMAGE_DOWNLOAD_FAILED", "Image retrieval failed or exceeded 32 MiB for all candidates", 5)
	}
	e := fault.New("IMAGE_ORIGINAL_UNAVAILABLE", "Neither preview nor original-size candidate matches snapshot SHA-256; supply your saved originals", 4)
	e.Details = map[string]any{"position": position, "expectedSha256": want, "actualSha256": actual, "attempts": attempts}
	return nil, e
}
