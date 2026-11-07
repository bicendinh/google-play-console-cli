package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pub "google.golang.org/api/androidpublisher/v3"
	"playops/internal/config"
	"playops/internal/googleapi"
	"playops/internal/releaseflow"
	"playops/internal/testutil"
)

func localPlan(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	manifest := filepath.Join(dir, "release.yaml")
	snapshot := filepath.Join(dir, "snapshot.json")
	plan := filepath.Join(dir, "plan.json")
	if e := os.WriteFile(manifest, []byte("app: com.example.app\nprofile: default\ntrack: production\nversionCode: 123\nlistings:\n  - locale: en-US\n    shortDescription: Updated\n"), 0600); e != nil {
		t.Fatal(e)
	}
	s := releaseflow.Snapshot{FormatVersion: 1, App: "com.example.app", Profile: "default", Track: &pub.Track{Track: "production"}, Listings: map[string]*releaseflow.Listing{"en-US": {Title: "Example", ShortDescription: "Before", FullDescription: "Description"}}, Images: map[string][]releaseflow.Image{}}
	if e := releaseflow.Write(snapshot, s); e != nil {
		t.Fatal(e)
	}
	p, e := releaseflow.BuildPlan(context.Background(), manifest, snapshot, "default")
	if e != nil {
		t.Fatal(e)
	}
	if e = releaseflow.Write(plan, p); e != nil {
		t.Fatal(e)
	}
	return manifest, plan
}

func TestNoOpApplyDoesNotCreateGoogleClient(t *testing.T) {
	manifest, _ := localPlan(t)
	b, e := os.ReadFile(manifest)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(manifest, []byte(strings.ReplaceAll(string(b), "Updated", "Before")), 0600); e != nil {
		t.Fatal(e)
	}
	p, e := releaseflow.BuildPlan(context.Background(), manifest, filepath.Join(filepath.Dir(manifest), "snapshot.json"), "default")
	if e != nil {
		t.Fatal(e)
	}
	out := filepath.Join(filepath.Dir(manifest), "noop.json")
	if e = releaseflow.Write(out, p); e != nil {
		t.Fatal(e)
	}
	code, env, _ := invoke(t, filepath.Join(t.TempDir(), "config.json"), offline(t), "releases", "apply", "--plan", out, "--confirm")
	if code != 2 || env.Error == nil || !strings.Contains(env.Error.Message, "no changes") {
		t.Fatalf("wrong no-op result %+v", env)
	}
}
func TestLifecycleOnlyCommandsNeverOpenEdits(t *testing.T) {
	manifest, plan := localPlan(t)
	cfg := filepath.Join(t.TempDir(), "config.json")
	calls := 0
	client := testutil.Google(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || strings.Contains(r.URL.Path, "/edits") {
			t.Errorf("unexpected edit operation %s %s", r.Method, r.URL.Path)
		}
		fmt.Fprint(w, `{"releases":[{"releaseName":"Example","activeArtifacts":[{"versionCode":123}],"releaseLifecycleState":"RELEASE_LIFECYCLE_STATE_IN_REVIEW"}]}`)
	})
	factory := func(context.Context, string) (*googleapi.Client, error) { return client, nil }
	path := filepath.Join(t.TempDir(), "lifecycle.json")
	for _, args := range [][]string{{"rollout", "get", "--track", "production", "--no-edit", "--app", "com.example.app"}, {"releases", "snapshot", "--manifest", manifest, "--out", path, "--no-edit"}, {"releases", "verify", "--plan", plan, "--no-edit", "--commit-outcome-unknown"}, {"releases", "status", "--track", "production", "--version-code", "123", "--app", "com.example.app"}} {
		code, e, stderr := invoke(t, cfg, factory, args...)
		if code != 0 || e.Error != nil || stderr != "" {
			t.Fatalf("%v: %+v stderr=%s", args, e, stderr)
		}
		if args[1] == "verify" {
			data := e.Data.(map[string]any)
			v := data["verification"].(map[string]any)
			if v["status"] != "pending" || data["reviewState"] != "in_review" || data["globalReviewState"] != "unknown" {
				t.Fatalf("lost lifecycle evidence %+v", data)
			}
		}
	}
	if calls != 4 {
		t.Fatal("unexpected network calls")
	}
	if _, e := releaseflow.LoadSnapshot(path); e == nil {
		t.Fatal("incomplete snapshot accepted for plan")
	}
	for _, args := range [][]string{{"releases", "verify", "--plan", plan}, {"releases", "verify", "--plan", plan, "--confirm", "--commit-outcome-unknown"}} {
		if code, _, _ := invoke(t, cfg, offline(t), args...); code != 2 {
			t.Fatal("missing verify guard")
		}
	}
}
func TestProfileOnFlagErrorAndReviewScope(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.json")
	code, e, stderr := invoke(t, cfg, offline(t), "releases", "upload", "--bundle", "example.aab", "--profile", "team")
	if code != 2 || e.Meta["profile"] != "team" || stderr != "" {
		t.Fatalf("lost error context %+v", e)
	}
	for raw, want := range map[string]string{"IN_REVIEW": "in_review", "PUBLISHED": "approved", "APPROVED_NOT_PUBLISHED": "approved", "NOT_APPROVED": "rejected", "FUTURE": "unknown"} {
		if got := reviewState([]*pub.ReleaseSummary{{ReleaseLifecycleState: "RELEASE_LIFECYCLE_STATE_" + raw}}); got != want {
			t.Fatalf("%s: %s", raw, got)
		}
	}
}
func TestReleaseInitIsOfflineAndExplicit(t *testing.T) {
	dir := t.TempDir()
	b, e := os.ReadFile(testutil.Bundle(t))
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(dir, "app-release-example.aab"), b, 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.Mkdir(filepath.Join(dir, "notes"), 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(dir, "notes", "en-US.txt"), []byte("Bug fixes\n"), 0600); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(dir, "release.yaml")
	cfg := filepath.Join(dir, "config.json")
	code, env, _ := invoke(t, cfg, offline(t), "releases", "init", "--dir", dir, "--track", "production", "--rollout", "100", "--out", path)
	if code != 0 {
		t.Fatalf("init %+v", env)
	}
	m, e := releaseflow.LoadManifest(path, "default")
	if e != nil || m.Name != "123 (1.2.3)" || m.Status != "completed" || m.Rollout != 100 || m.Notes[0].Text != "Bug fixes" {
		t.Fatalf("init %+v %v", m, e)
	}
	if code, _, _ := invoke(t, cfg, offline(t), "releases", "init", "--dir", dir, "--track", "production", "--out", path); code != 2 {
		t.Fatal("overwrote existing manifest")
	}
	partial := filepath.Join(dir, "shots", "en-US")
	if e = os.MkdirAll(partial, 0700); e != nil {
		t.Fatal(e)
	}
	testutil.RGBAFile(t, filepath.Join(partial, "05-example.png"), 255)
	if code, _, _ := invoke(t, cfg, offline(t), "releases", "init", "--dir", dir, "--track", "production", "--out", filepath.Join(dir, "partial.yaml")); code != 2 {
		t.Fatal("partial screenshots accepted without baseline assets")
	}
	originals := filepath.Join(dir, "originals", "en-US")
	if e = os.MkdirAll(originals, 0700); e != nil {
		t.Fatal(e)
	}
	for i := 1; i <= 8; i++ {
		testutil.RGBAFile(t, filepath.Join(originals, fmt.Sprintf("%02d-original.png", i)), 255)
	}
	partialManifest := filepath.Join(dir, "partial-with-baseline.yaml")
	if code, e, _ := invoke(t, cfg, offline(t), "releases", "init", "--dir", dir, "--track", "internal", "--source-dir", filepath.Dir(originals), "--out", partialManifest); code != 0 {
		t.Fatalf("partial init %+v", e)
	}
	m, e = releaseflow.LoadManifest(partialManifest, "default")
	if e != nil || len(m.Images) != 1 || len(m.Images[0].SourceFiles) != 8 || m.Images[0].Replace[5] == "" {
		t.Fatalf("partial manifest %+v %v", m, e)
	}
}
func TestImagesListCallerOwnedEdit(t *testing.T) {
	client := testutil.Google(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("mutated edit: %s", r.Method)
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/phoneScreenshots"):
			fmt.Fprint(w, `{"images":[{"id":"image","sha256":"abc","url":"https://example.com/preview"}]}`)
		case strings.HasSuffix(r.URL.Path, "/listings"):
			fmt.Fprint(w, `{"listings":[{"language":"en-US"}]}`)
		default:
			fmt.Fprint(w, `{"id":"existing"}`)
		}
	})
	cfg := filepath.Join(t.TempDir(), "config.json")
	factory := func(context.Context, string) (*googleapi.Client, error) { return client, nil }
	code, e, stderr := invoke(t, cfg, factory, "listings", "images", "list", "--locale", "en-US", "--type", "phoneScreenshots", "--edit-id", "existing", "--app", "com.example.app")
	if code != 0 || stderr != "" {
		t.Fatalf("images %+v", e)
	}
	data := e.Data.(map[string]any)
	if data["inheritance"] != "unknown" || data["usesDefaultImages"] != nil {
		t.Fatal("invented inheritance")
	}
	row := data["images"].([]any)[0].(map[string]any)
	if row["position"] != float64(1) || row["sha256"] != "abc" || row["url"] != "https://example.com/preview" {
		t.Fatal("lost image data")
	}
}
func TestReportsBucketVerifyBeforeSave(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.json")
	var denied atomic.Bool
	client := testutil.Google(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("unexpected mutation")
		}
		if denied.Load() {
			w.WriteHeader(403)
			fmt.Fprint(w, `{"error":{"code":403,"message":"forbidden"}}`)
		} else {
			fmt.Fprint(w, `{"items":[]}`)
		}
	})
	factory := func(context.Context, string) (*googleapi.Client, error) { return client, nil }
	args := []string{"config", "set", "reports-bucket", "gs://pubsite_prod_rev_example", "--verify", "--app", "com.example.app"}
	if code, e, _ := invoke(t, cfg, factory, args...); code != 0 {
		t.Fatalf("verify bucket %+v", e)
	}
	denied.Store(true)
	args[3] = "gs://pubsite_prod_rev_other"
	if code, _, _ := invoke(t, cfg, factory, args...); code != 3 {
		t.Fatal("permission failure accepted")
	}
	stored, e := config.Load(cfg)
	if e != nil || stored.Profiles["default"].ReportsBucket != "pubsite_prod_rev_example" {
		t.Fatal("failed verification changed configuration")
	}
}
func TestVitalsDaysFiltersAndConflicts(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.json")
	var queried atomic.Bool
	client := testutil.Google(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			fmt.Fprint(w, `{}`)
			return
		}
		var body map[string]any
		if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
			t.Fatal(e)
		}
		if body["filter"] != "versionCode = 123" {
			t.Errorf("bad filter %+v", body)
		}
		queried.Store(true)
		fmt.Fprint(w, `{"rows":[]}`)
	})
	factory := func(context.Context, string) (*googleapi.Client, error) { return client, nil }
	if code, e, _ := invoke(t, cfg, factory, "vitals", "crashes", "--version-code", "123", "--days", "7", "--app", "com.example.app"); code != 0 {
		t.Fatalf("vitals %+v", e)
	}
	if !queried.Load() {
		t.Fatal("vitals query not executed")
	}
	for _, extra := range [][]string{{"--days", "0"}, {"--days", "7", "--from", time.Now().Format(time.DateOnly)}, {"--days", "7", "--to", "2026-11-01"}} {
		args := append([]string{"vitals", "anrs"}, extra...)
		if code, _, _ := invoke(t, cfg, offline(t), args...); code != 2 {
			t.Fatal("ambiguous date range accepted")
		}
	}
}
