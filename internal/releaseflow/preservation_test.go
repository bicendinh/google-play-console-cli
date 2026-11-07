package releaseflow

import (
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	pub "google.golang.org/api/androidpublisher/v3"
	"playops/internal/testutil"
)

func TestBaselineRGBAStaysUnchangedAndNoOpSkipsImages(t *testing.T) {
	dir := t.TempDir()
	a := testutil.RGBAFile(t, filepath.Join(dir, "01.png"), 255)
	b := testutil.RGBAFile(t, filepath.Join(dir, "02.png"), 255)
	first, _ := HashFile(a)
	second, _ := HashFile(b)
	baseline := Snapshot{FormatVersion: 1, App: "com.example.app", Profile: "default", Track: &pub.Track{Track: "internal"}, Listings: map[string]*Listing{"en-US": {Title: "Example", ShortDescription: "Example", FullDescription: "Example"}}, Images: map[string][]Image{"en-US/phoneScreenshots": {{ID: "one", SHA256: first.SHA256}, {ID: "two", SHA256: second.SHA256}}}}
	m := Manifest{App: baseline.App, Profile: baseline.Profile, Track: "internal", Status: "draft", Images: []ImageChange{{Locale: "en-US", Type: "phoneScreenshots", Mode: "replace", SourceFiles: []string{a, b}, Replace: map[int]string{2: b}}}}
	p, e := prepare(context.Background(), m, baseline, map[string]File{})
	if e != nil {
		t.Fatal(e)
	}
	if len(p.TargetImages) != 0 || len(p.Diff) != 0 {
		t.Fatal("identical replacement must be a no-op")
	}
	p.Digest = p.digest()
	if _, e = (Service{}).Apply(context.Background(), p); e == nil {
		t.Fatal("no-op should not open an edit")
	}
	newFile := filepath.Join(dir, "new.png")
	f, e := os.Create(newFile)
	if e != nil {
		t.Fatal(e)
	}
	im := image.NewRGBA(image.Rect(0, 0, 320, 640))
	for i := 3; i < len(im.Pix); i += 4 {
		im.Pix[i] = 255
	}
	if e = png.Encode(f, im); e != nil {
		t.Fatal(e)
	}
	f.Close()
	m.Images[0].Replace[2] = newFile
	p, e = prepare(context.Background(), m, baseline, map[string]File{})
	if e != nil {
		t.Fatal(e)
	}
	if len(p.TargetImages) != 1 || p.TargetImages[0].Files[0].SHA256 != first.SHA256 || p.TargetImages[0].Files[0].Transform != "" || p.TargetImages[0].Files[1].SHA256 == second.SHA256 {
		t.Fatal("unchanged RGBA was normalized or replacement lost")
	}
	// An independent new locale needs normalized versions of the same source.
	m.Images = append(m.Images, ImageChange{Locale: "de-DE", Type: "phoneScreenshots", Mode: "replace", CopyFrom: "en-US"})
	baseline.Listings["de-DE"] = baseline.Listings["en-US"]
	baseline.Images["de-DE/phoneScreenshots"] = []Image{}
	p, e = prepare(context.Background(), m, baseline, map[string]File{})
	if e != nil {
		t.Fatal(e)
	}
	staged, cleanup, e := stage(p)
	if e != nil {
		t.Fatal(e)
	}
	defer cleanup()
	raw := p.TargetImages[0].Files[0]
	converted := p.TargetImages[1].Files[0]
	if raw.Transform != "" || converted.Transform == "" {
		t.Fatal("expected independent payload policies")
	}
	for _, want := range []File{raw, converted} {
		actual, e := HashFile(staged[imageUploadKey(want)])
		if e != nil || actual.SHA256 != want.SHA256 {
			t.Fatal("staging mixed raw and converted payloads")
		}
	}
}
