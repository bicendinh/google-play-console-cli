package releaseflow

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	pub "google.golang.org/api/androidpublisher/v3"
	"playops/internal/fault"
	"playops/internal/testutil"
)

func TestImageReductionRequiresReviewedConsent(t *testing.T) {
	dir := t.TempDir()
	first := testutil.RGBAFile(t, filepath.Join(dir, "05-example.png"), 255)
	second := testutil.RGBAFile(t, filepath.Join(dir, "07-example.png"), 255)
	m := Manifest{App: "com.example.app", Profile: "default", Track: "internal", Status: "draft", Images: []ImageChange{{Locale: "en-US", Type: "phoneScreenshots", Mode: "replace", Files: []string{first, second}}}}
	baseline := Snapshot{FormatVersion: 1, App: m.App, Profile: m.Profile, Track: &pub.Track{Track: m.Track}, Listings: map[string]*Listing{"en-US": {Title: "Example", ShortDescription: "Example", FullDescription: "Example"}}, Images: map[string][]Image{"en-US/phoneScreenshots": make([]Image, 8)}}
	checks, e := Preflight(context.Background(), m)
	if e != nil {
		t.Fatal(e)
	}
	warnings := 0
	for _, c := range checks {
		if c.Status == "warning" {
			warnings++
		}
	}
	if warnings != 2 {
		t.Fatalf("missing filename warnings %+v", checks)
	}
	_, e = prepare(context.Background(), m, baseline, map[string]File{})
	if e == nil || fault.From(e).Code != "IMAGE_COUNT_DECREASE" {
		t.Fatalf("unapproved 8 -> 2 accepted: %v", e)
	}
	m.Images[0].AllowImageCountDecrease = true
	p, e := prepare(context.Background(), m, baseline, map[string]File{})
	if e != nil {
		t.Fatal(e)
	}
	if len(p.TargetImages[0].Files) != 2 || len(p.Warnings) != 3 {
		t.Fatalf("missing reviewed intent %+v", p.Warnings)
	}
	// Even a recomputed digest cannot bypass the semantic guard. A nil API
	// ensures failure occurs before any edit or content mutation is attempted.
	p.Manifest.Images = append([]ImageChange(nil), p.Manifest.Images...)
	p.Manifest.Images[0].AllowImageCountDecrease = false
	p.Digest = p.digest()
	if _, e = (Service{}).Apply(context.Background(), p); e == nil || fault.From(e).Code != "IMAGE_COUNT_DECREASE" {
		t.Fatalf("apply bypassed consent: %v", e)
	}
	// copyFrom must get its own consent; source consent cannot authorize a
	// reduction of the destination locale's independent set.
	m.Images = append(m.Images, ImageChange{Locale: "de-DE", Type: "phoneScreenshots", Mode: "replace", CopyFrom: "en-US"})
	baseline.Listings["de-DE"] = baseline.Listings["en-US"]
	baseline.Images["de-DE/phoneScreenshots"] = make([]Image, 8)
	if _, e = prepare(context.Background(), m, baseline, map[string]File{}); e == nil || fault.From(e).Code != "IMAGE_COUNT_DECREASE" {
		t.Fatal("copyFrom inherited deletion consent")
	}
}

func TestNoteLengthHasOnePreciseIssue(t *testing.T) {
	m := Manifest{App: "com.example.app", Profile: "default", Track: "internal", Status: "completed", Rollout: 100, VersionCode: 123, Notes: []Note{{Language: "en-US", Text: strings.Repeat("界", 501)}}}
	issues := inputIssues(m)
	if len(issues) != 1 || issues[0].Locale != "en-US" || issues[0].ActualLength == nil || *issues[0].ActualLength != 501 || issues[0].MaxLength != 500 {
		t.Fatalf("duplicate or imprecise note issue %+v", issues)
	}
	if !strings.Contains(issues[0].Message, "501") {
		t.Fatal("message omits actual length")
	}
	m.Notes[0].Text = strings.Repeat("界", 500)
	if issues = inputIssues(m); len(issues) != 0 {
		t.Fatalf("500 characters rejected %+v", issues)
	}
}

func TestMetadataMismatchIncludesBothValues(t *testing.T) {
	m := Manifest{App: "com.other.app", Profile: "default", Track: "internal", Name: "Example", Status: "draft", VersionCode: 124, Bundle: testutil.Bundle(t), VersionName: "2.0", MinSDK: 24, TargetSDK: 35}
	checks, e := preflight(context.Background(), m, fakeTools{missing: true})
	if e == nil {
		t.Fatal("mismatch accepted")
	}
	for _, c := range checks {
		if c.Name == "bundleMetadata" {
			if c.Status != "failed" || !strings.Contains(c.Message, "versionCode: expected 124, actual 123") {
				t.Fatalf("imprecise message %+v", c)
			}
			mismatches := c.Details.(map[string]any)["mismatches"].([]MetadataMismatch)
			if len(mismatches) != 5 {
				t.Fatalf("expected each mismatch, got %+v", mismatches)
			}
			return
		}
	}
	t.Fatal("missing metadata check")
}
