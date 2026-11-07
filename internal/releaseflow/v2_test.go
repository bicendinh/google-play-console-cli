package releaseflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"playops/internal/fault"
	"playops/internal/testutil"
)

func TestOpaqueRGBAPlanAndStaging(t *testing.T) {
	path := testutil.RGBAFile(t, filepath.Join(t.TempDir(), "opaque.png"), 255)
	input, e := HashFile(path)
	if e != nil {
		t.Fatal(e)
	}
	f, e := ImageFile(path, "phoneScreenshots", "auto")
	if e != nil {
		t.Fatal(e)
	}
	if f.SourceSHA256 != input.SHA256 || f.SHA256 == input.SHA256 || f.Transform == "" {
		t.Fatal("missing source/output provenance")
	}
	p := Plan{Inputs: []File{input}, TargetImages: []PlannedImages{{Locale: "en-US", Type: "phoneScreenshots", Files: []File{f}}}}
	staged, cleanup, e := stage(p)
	if e != nil {
		t.Fatal(e)
	}
	defer cleanup()
	actual, e := HashFile(staged[imageUploadKey(f)])
	if e != nil || actual.SHA256 != f.SHA256 {
		t.Fatal("staged bytes differ from planned output")
	}
	b, _ := os.ReadFile(path)
	converted, _ := os.ReadFile(staged[imageUploadKey(f)])
	if b[25] != 6 || converted[25] != 2 {
		t.Fatal("original overwritten or conversion missing")
	}
	for _, policy := range []string{"never", "error"} {
		if _, e = ImageFile(path, "phoneScreenshots", policy); e == nil {
			t.Fatal("strict policy accepted alpha channel")
		}
	}
	transparent := testutil.RGBAFile(t, filepath.Join(t.TempDir(), "transparent.png"), 128)
	if _, e = ImageFile(transparent, "phoneScreenshots", "auto"); e == nil {
		t.Fatal("transparency accepted")
	}
	f.SHA256 = strings.Repeat("0", 64)
	p.TargetImages[0].Files[0] = f
	if _, cleanup, e = stage(p); e == nil {
		cleanup()
		t.Fatal("unreviewed conversion hash accepted")
	}
}
func TestCopyFromCyclesAndFinalSet(t *testing.T) {
	groups := []ImageChange{{Locale: "en-US", Type: "phoneScreenshots", Mode: "replace", SourceFiles: []string{"a", "b", "c"}, Replace: map[int]string{2: "new"}}, {Locale: "de-DE", Type: "phoneScreenshots", Mode: "replace", CopyFrom: "en-US"}}
	expanded, e := expandedImages(groups)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Join(expanded[1].Files, ",") != "a,new,c" || groups[1].CopyFrom == "" {
		t.Fatal("copy did not resolve source final set or mutated source")
	}
	groups[0].CopyFrom = "de-DE"
	if _, e = expandedImages(groups); e == nil {
		t.Fatal("cycle accepted")
	}
	groups[0].CopyFrom = "fr-FR"
	if _, e = expandedImages(groups); e == nil {
		t.Fatal("missing source accepted")
	}
}
func TestPreflightAggregatesLocaleAndFileIssues(t *testing.T) {
	m := Manifest{App: "com.example.app", Profile: "default", Track: "internal", Status: "draft", Notes: []Note{{Language: "en-US", Text: strings.Repeat("x", 501)}, {Language: "de-DE", Text: strings.Repeat("y", 501)}}, Images: []ImageChange{{Locale: "en-US", Type: "phoneScreenshots", Mode: "replace", Files: []string{"missing-a.png", "missing-b.png"}}}}
	_, e := Preflight(context.Background(), m)
	if e == nil {
		t.Fatal("accepted invalid assets and notes")
	}
	f := fault.From(e)
	details := f.Details.(map[string]any)
	checks := details["checks"].([]Check)
	issues := checks[0].Details.(map[string]any)["issues"].([]Issue)
	if len(issues) < 4 {
		t.Fatalf("issues lost: %+v", issues)
	}
	for _, file := range []string{"missing-a.png", "missing-b.png"} {
		found := false
		for _, i := range issues {
			if i.File == file && i.Locale == "en-US" && i.Index > 0 {
				found = true
			}
		}
		if !found {
			t.Fatal("missing structured file/locale/index")
		}
	}
}
