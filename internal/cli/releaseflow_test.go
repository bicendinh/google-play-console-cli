package cli

import (
	pub "google.golang.org/api/androidpublisher/v3"
	"os"
	"path/filepath"
	"playops/internal/releaseflow"
	"testing"
	"time"
)

func TestOfflinePlanApplyAndPreflight(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "config.json")
	manifest, snapshot, out := filepath.Join(dir, "release.yaml"), filepath.Join(dir, "snapshot.json"), filepath.Join(dir, "plan.json")
	if err := os.WriteFile(manifest, []byte("app: com.example.app\nprofile: default\ntrack: production\nlistings:\n  - locale: en-US\n    shortDescription: New text\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s := releaseflow.Snapshot{FormatVersion: 1, App: "com.example.app", Profile: "default", CapturedAt: time.Now(), Track: &pub.Track{Track: "production"}, Listings: map[string]*releaseflow.Listing{"en-US": {Title: "App", ShortDescription: "Old", FullDescription: "Keep full"}}, Images: map[string][]releaseflow.Image{}}
	if err := releaseflow.Write(snapshot, s); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"releases", "plan", "--manifest", manifest, "--snapshot", snapshot, "--out", out, "--dry-run"}, {"releases", "apply", "--plan", out, "--dry-run"}, {"releases", "preflight", "--manifest", manifest, "--snapshot", snapshot}} {
		code, e, _ := invoke(t, config, offline(t), args...)
		if code != 0 {
			t.Fatalf("%v: %+v", args, e.Error)
		}
	}
	if err := os.WriteFile(manifest, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	code, e, _ := invoke(t, config, offline(t), "releases", "apply", "--plan", out, "--dry-run")
	if code != 4 || e.Error == nil {
		t.Fatal("modified manifest accepted")
	}
	if _, err := os.Stat(config); !os.IsNotExist(err) {
		t.Fatal("offline planning wrote credential config")
	}
}
func TestNewCommandGuards(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	for _, args := range [][]string{{"releases", "snapshot", "--manifest", "missing.yaml", "--out", "snapshot.json"}, {"releases", "apply", "--plan", "missing.json"}, {"listings", "update", "--locale", "en-US", "--edit-id", "e1", "--file", "listing.json"}, {"listings", "images", "sync", "--plan", "p.json", "--edit-id", "e1"}, {"releases", "symbols", "upload", "--edit-id", "e1", "--type", "proguard", "--file", "mapping.txt", "--version-code", "123"}} {
		code, e, _ := invoke(t, path, offline(t), args...)
		if code != 2 || e.Error == nil {
			t.Fatalf("unguarded mutation %v %+v", args, e)
		}
	}
	strict := filepath.Join(dir, "strict.yaml")
	if err := os.WriteFile(strict, []byte("app: com.example.app\nprofile: default\ntrack: production\npreflight:\n  required: true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if code, e, _ := invoke(t, path, offline(t), "releases", "preflight", "--manifest", strict); code != 2 {
		t.Fatalf("strict preflight skipped snapshot coverage: %+v", e)
	}
}
