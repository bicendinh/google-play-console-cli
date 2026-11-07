package cli

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"playops/internal/googleapi"
	"playops/internal/testutil"
)

func TestSnapshotReportsAcknowledgedCleanup(t *testing.T) {
	manifest, _ := localPlan(t)
	client := testutil.Google(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "DELETE":
			w.WriteHeader(204)
		case r.Method == "POST":
			fmt.Fprint(w, `{"id":"temporary"}`)
		case strings.HasSuffix(r.URL.Path, "/listings"):
			fmt.Fprint(w, `{"listings":[{"language":"en-US","title":"Example","shortDescription":"Before","fullDescription":"Description"}]}`)
		case strings.Contains(r.URL.Path, "/tracks/"):
			fmt.Fprint(w, `{"track":"production"}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})
	factory := func(context.Context, string) (*googleapi.Client, error) { return client, nil }
	code, e, stderr := invoke(t, filepath.Join(t.TempDir(), "config.json"), factory, "releases", "snapshot", "--manifest", manifest, "--out", filepath.Join(t.TempDir(), "snapshot.json"), "--confirm")
	if code != 0 || stderr != "" || e.Meta["discarded"] != true || e.Meta["editOpened"] != true || e.Meta["editId"] != "temporary" || e.Meta["committed"] != false {
		t.Fatalf("wrong cleanup output %+v", e)
	}
}
func TestImageDownloadRequiresConsent(t *testing.T) {
	code, e, _ := invoke(t, filepath.Join(t.TempDir(), "config.json"), offline(t), "listings", "images", "download", "--snapshot", "missing.json", "--locale", "en-US", "--type", "phoneScreenshots", "--out", "originals")
	if code != 2 || e.Error == nil {
		t.Fatal("download opened an edit without consent")
	}
}
