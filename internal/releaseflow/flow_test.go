package releaseflow_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	pub "google.golang.org/api/androidpublisher/v3"
	"gopkg.in/yaml.v3"
	"playops/internal/fault"
	"playops/internal/googleapi"
	"playops/internal/releaseflow"
	"playops/internal/testutil"
)

type state struct {
	Track    *pub.Track
	Source   *pub.Track
	Listings map[string]*pub.Listing
	Images   map[string][]*pub.Image
}
type play struct {
	mu                                                                                   sync.Mutex
	live                                                                                 state
	pending                                                                              state
	begins, commits, validates, discards, bundles, imageUploads, listingUpdates, symbols int
	writeEdits                                                                           map[string]bool
	failLocale                                                                           string
	reorder, noOp, dropCommit, expire                                                    bool
	symbolStatus                                                                         int
	commitBehavior                                                                       string
	notSent                                                                              string
}

func media(t *testing.T, r *http.Request) []byte {
	t.Helper()
	kind, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if strings.HasPrefix(kind, "multipart/") {
		reader := multipart.NewReader(r.Body, params["boundary"])
		var data []byte
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Error(err)
				return nil
			}
			b, _ := io.ReadAll(part)
			if part.Header.Get("Content-Type") != "application/json" {
				data = b
			}
		}
		return data
	}
	b, _ := io.ReadAll(r.Body)
	return b
}
func (p *play) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.Path
		if strings.HasSuffix(path, "/edits") && r.Method == "POST" {
			p.begins++
			b, _ := json.Marshal(p.live)
			_ = json.Unmarshal(b, &p.pending)
			fmt.Fprintf(w, `{"id":"e%d"}`, p.begins)
			return
		}
		parts := strings.Split(path, "/")
		edit := ""
		for i, part := range parts {
			if part == "edits" && i+1 < len(parts) {
				edit = strings.Split(parts[i+1], ":")[0]
			}
		}
		if r.Method != "GET" && r.Method != "DELETE" {
			p.writeEdits[edit] = true
		}
		if p.expire && r.Method == "GET" {
			w.WriteHeader(404)
			fmt.Fprint(w, `{"error":{"code":404,"message":"edit expired"}}`)
			return
		}
		switch {
		case r.Method == "DELETE" && strings.Contains(path, "/listings/"):
			locale, kind := parts[len(parts)-2], parts[len(parts)-1]
			p.pending.Images[locale+"/"+kind] = []*pub.Image{}
			fmt.Fprint(w, `{}`)
		case r.Method == "DELETE":
			p.discards++
			w.WriteHeader(204)
		case strings.HasSuffix(path, ":validate"):
			p.validates++
			fmt.Fprint(w, `{"id":"e"}`)
		case strings.HasSuffix(path, ":commit"):
			p.commits++
			p.commitBehavior = r.URL.Query().Get("changesInReviewBehavior")
			p.notSent = r.URL.Query().Get("changesNotSentForReview")
			p.live = p.pending
			if p.dropCommit {
				conn, _, _ := w.(http.Hijacker).Hijack()
				_ = conn.Close()
				return
			}
			fmt.Fprint(w, `{"id":"e"}`)
		case strings.Contains(path, "/tracks/"):
			if strings.HasSuffix(path, "/internal") && p.pending.Source != nil {
				if r.Method != "GET" {
					t.Error("promotion wrote source track")
				}
				_ = json.NewEncoder(w).Encode(p.pending.Source)
				return
			}
			if r.Method == "PUT" {
				var track pub.Track
				if err := json.NewDecoder(r.Body).Decode(&track); err != nil {
					t.Error(err)
				}
				p.pending.Track = &track
			}
			_ = json.NewEncoder(w).Encode(p.pending.Track)
		case strings.HasSuffix(path, "/bundles"):
			p.bundles++
			_ = media(t, r)
			fmt.Fprint(w, `{"versionCode":123}`)
		case strings.Contains(path, "/deobfuscationFiles/"):
			p.symbols++
			_ = media(t, r)
			if p.symbolStatus != 0 {
				w.WriteHeader(p.symbolStatus)
				fmt.Fprintf(w, `{"error":{"code":%d,"message":"mapping rejected"}}`, p.symbolStatus)
			} else {
				fmt.Fprint(w, `{}`)
			}
		case strings.HasSuffix(path, "/listings"):
			rows := []*pub.Listing{}
			for _, l := range p.pending.Listings {
				rows = append(rows, l)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"listings": rows})
		case strings.Contains(path, "/listings/"):
			index := 0
			for i, v := range parts {
				if v == "listings" {
					index = i
				}
			}
			locale := parts[index+1]
			if len(parts) == index+2 {
				var value pub.Listing
				if err := json.NewDecoder(r.Body).Decode(&value); err != nil {
					t.Error(err)
				}
				value.Language = locale
				p.pending.Listings[locale] = &value
				p.listingUpdates++
				_ = json.NewEncoder(w).Encode(value)
				return
			}
			kind := parts[index+2]
			group := locale + "/" + kind
			if r.Method == "GET" {
				rows := append([]*pub.Image{}, p.pending.Images[group]...)
				if p.reorder && p.imageUploads > 0 && len(rows) > 1 {
					rows[0], rows[1] = rows[1], rows[0]
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"images": rows})
				return
			}
			p.imageUploads++
			if locale == p.failLocale {
				w.WriteHeader(400)
				fmt.Fprint(w, `{"error":{"code":400,"message":"image rejected"}}`)
				return
			}
			b := media(t, r)
			h := sha256.Sum256(b)
			value := &pub.Image{Id: fmt.Sprintf("new-%d", p.imageUploads), Sha256: hex.EncodeToString(h[:])}
			if !p.noOp {
				p.pending.Images[group] = append(p.pending.Images[group], value)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"image": value})
		default:
			t.Errorf("unexpected %s %s", r.Method, path)
			http.Error(w, "unexpected", 500)
		}
	}
}
func asset(t *testing.T, dir string, index int) string {
	t.Helper()
	path := filepath.Join(dir, fmt.Sprintf("%02d.png", index))
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	im := image.NewRGBA(image.Rect(0, 0, 320, 640))
	for y := 0; y < 640; y++ {
		for x := 0; x < 320; x++ {
			im.SetRGBA(x, y, color.RGBA{R: uint8(index * 17), G: uint8(x % 255), B: uint8(y % 255), A: 255})
		}
	}
	if err = png.Encode(f, im); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	return path
}

type fixture struct {
	play                       *play
	client                     *googleapi.Client
	service                    releaseflow.Service
	manifest                   releaseflow.Manifest
	manifestPath, snapshotPath string
	sources                    []string
}

func TestPreparedRGBAAndCopiesVerifyAfterApply(t *testing.T) {
	f := setup(t, []string{"en-US", "de-DE"})
	image := testutil.RGBAFile(t, filepath.Join(t.TempDir(), "opaque.png"), 255)
	f.manifest.Images[0].Replace[5] = image
	f.manifest.Images[1] = releaseflow.ImageChange{Locale: "de-DE", Type: "phoneScreenshots", Mode: "replace", CopyFrom: "en-US"}
	b, e := yaml.Marshal(f.manifest)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(f.manifestPath, b, 0600); e != nil {
		t.Fatal(e)
	}
	p := f.plan(t)
	if p.TargetImages[0].Files[4].Transform == "" || p.TargetImages[1].Files[4].SHA256 != p.TargetImages[0].Files[4].SHA256 {
		t.Fatal("copy lost converted payload")
	}
	if _, e = f.service.Apply(context.Background(), p); e != nil {
		t.Fatal(e)
	}
	commits, uploads, validates := f.play.commits, f.play.imageUploads, f.play.validates
	// Verification is remote-only: removing the original assets must not prevent it.
	if e = os.Remove(image); e != nil {
		t.Fatal(e)
	}
	v, e := f.service.Verify(context.Background(), p, "")
	if e != nil || v.Status != "match" {
		t.Fatalf("verify %+v %v", v, e)
	}
	if f.play.commits != commits || f.play.imageUploads != uploads || f.play.validates != validates {
		t.Fatal("verification mutated release state")
	}
	f.play.live.Images["de-DE/phoneScreenshots"][0].Sha256 = "changed"
	v, e = f.service.Verify(context.Background(), p, "")
	if e != nil || v.Status != "drift" {
		t.Fatalf("drift %+v %v", v, e)
	}
	f.play.live.Track.Releases = nil
	v, e = f.service.Verify(context.Background(), p, "")
	if e != nil || v.Status != "unknown" {
		t.Fatalf("absence should be inconclusive: %+v %v", v, e)
	}
}

func setup(t *testing.T, locales []string) fixture {
	t.Helper()
	dir := t.TempDir()
	sources := []string{}
	for i := 1; i <= 8; i++ {
		sources = append(sources, asset(t, dir, i))
	}
	fifth, seventh := asset(t, dir, 15), asset(t, dir, 17)
	p := &play{live: state{Track: &pub.Track{Track: "production", Releases: []*pub.TrackRelease{{Name: "previous", Status: "completed", VersionCodes: []int64{99}}}}, Listings: map[string]*pub.Listing{}, Images: map[string][]*pub.Image{}}, writeEdits: map[string]bool{}}
	m := releaseflow.Manifest{App: "com.example.app", Profile: "default", Bundle: testutil.Bundle(t), VersionCode: 123, Track: "production", Name: "1.2.3", Status: "inProgress", Rollout: 10, RequiredLocales: locales, RequiredImageTypes: []string{"phoneScreenshots"}, Preflight: releaseflow.PreflightOptions{Bundletool: filepath.Join(dir, "missing-bundletool"), Jarsigner: filepath.Join(dir, "missing-jarsigner")}}
	for _, locale := range locales {
		p.live.Listings[locale] = &pub.Listing{Language: locale, Title: "App", ShortDescription: "Old", FullDescription: "Keep this full description", Video: "https://youtube.com/watch?v=old"}
		rows := []*pub.Image{}
		for i, path := range sources {
			f, err := releaseflow.HashFile(path)
			if err != nil {
				t.Fatal(err)
			}
			rows = append(rows, &pub.Image{Id: fmt.Sprintf("old-%d", i), Sha256: f.SHA256})
		}
		p.live.Images[locale+"/phoneScreenshots"] = rows
		m.Notes = append(m.Notes, releaseflow.Note{Language: locale, Text: "Bug fixes"})
		short := "New"
		m.Listings = append(m.Listings, releaseflow.ListingChange{Locale: locale, ShortDescription: &short})
		m.Images = append(m.Images, releaseflow.ImageChange{Locale: locale, Type: "phoneScreenshots", Mode: "replace", SourceFiles: sources, Replace: map[int]string{5: fifth, 7: seventh}})
	}
	client := testutil.Google(t, p.handler(t))
	s := releaseflow.Service{API: client, Now: time.Now}
	snapshot, err := s.Capture(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath, snapshotPath := filepath.Join(dir, "release.yaml"), filepath.Join(dir, "snapshot.json")
	b, _ := yaml.Marshal(m)
	if err = os.WriteFile(manifestPath, b, 0600); err != nil {
		t.Fatal(err)
	}
	if err = releaseflow.Write(snapshotPath, snapshot); err != nil {
		t.Fatal(err)
	}
	return fixture{play: p, client: client, service: s, manifest: m, manifestPath: manifestPath, snapshotPath: snapshotPath, sources: sources}
}
func (f fixture) plan(t *testing.T) releaseflow.Plan {
	t.Helper()
	p, err := releaseflow.BuildPlan(context.Background(), f.manifestPath, f.snapshotPath, "default")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAtomicEightLocalesAndPreservedImageOrder(t *testing.T) {
	f := setup(t, []string{"en-US", "en-AU", "en-GB", "en-CA", "es-ES", "es-419", "pt-BR", "pt-PT"})
	p := f.plan(t)
	planPath := filepath.Join(t.TempDir(), "reviewed.plan.json")
	if err := releaseflow.Write(planPath, p); err != nil {
		t.Fatal(err)
	}
	var loadErr error
	p, loadErr = releaseflow.LoadPlan(planPath)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	out, err := f.service.Apply(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	f.play.mu.Lock()
	defer f.play.mu.Unlock()
	if !out.Committed || f.play.commits != 1 || f.play.validates != 1 || f.play.begins != 2 || f.play.bundles != 1 || len(f.play.writeEdits) != 1 {
		t.Fatalf("not one edit/commit: %+v %+v", out, f.play)
	}
	if f.play.commitBehavior != "ERROR_IF_IN_REVIEW" {
		t.Fatal("unsafe review default")
	}
	for _, g := range p.TargetImages {
		rows := f.play.live.Images[g.Locale+"/"+g.Type]
		if len(rows) != 8 {
			t.Fatal("image count changed")
		}
		for i, row := range rows {
			if row.Sha256 != g.Files[i].SHA256 {
				t.Fatalf("wrong position %d", i+1)
			}
			if i != 4 && i != 6 {
				source, _ := releaseflow.HashFile(f.sources[i])
				if row.Sha256 != source.SHA256 {
					t.Fatal("unchanged image content lost")
				}
			}
		}
	}
	for _, l := range f.play.live.Listings {
		if l.ShortDescription != "New" || l.FullDescription != "Keep this full description" || l.Video != "https://youtube.com/watch?v=old" {
			t.Fatal("unmodified listing fields lost")
		}
	}
}
func TestFailuresNeverCommit(t *testing.T) {
	for _, kind := range []string{"last-locale", "no-op", "order", "stale", "expired", "input-changed"} {
		t.Run(kind, func(t *testing.T) {
			f := setup(t, []string{"en-US", "es-419", "pt-PT"})
			p := f.plan(t)
			f.play.mu.Lock()
			switch kind {
			case "last-locale":
				f.play.failLocale = "pt-PT"
			case "no-op":
				f.play.noOp = true
			case "order":
				f.play.reorder = true
			case "stale":
				f.play.live.Listings["en-US"].Title = "Changed externally"
			case "expired":
				f.play.expire = true
			case "input-changed":
				file := p.Manifest.Bundle
				b, _ := os.ReadFile(file)
				_ = os.WriteFile(file, append(b, 'x'), 0600)
			}
			f.play.mu.Unlock()
			_, err := f.service.Apply(context.Background(), p)
			if err == nil {
				t.Fatal("expected failure")
			}
			f.play.mu.Lock()
			defer f.play.mu.Unlock()
			if f.play.commits != 0 || f.play.validates != 0 {
				t.Fatal("failure reached validation/commit")
			}
			if kind == "stale" && (f.play.bundles != 0 || f.play.imageUploads != 0 || f.play.listingUpdates != 0) {
				t.Fatal("wrote before checking drift")
			}
			if kind == "input-changed" && f.play.begins != 1 {
				t.Fatal("opened edit before checking local inputs")
			}
			if len(f.play.live.Track.Releases) != 1 {
				t.Fatal("live app changed without commit")
			}
		})
	}
}
func TestUnknownCommitAndExplicitReviewOptions(t *testing.T) {
	f := setup(t, []string{"en-US"})
	f.manifest.Review.InReview = "cancel"
	f.manifest.Review.ChangesNotSentForReview = true
	b, _ := yaml.Marshal(f.manifest)
	_ = os.WriteFile(f.manifestPath, b, 0600)
	p := f.plan(t)
	f.play.dropCommit = true
	_, err := f.service.Apply(context.Background(), p)
	f.play.mu.Lock()
	defer f.play.mu.Unlock()
	if fault.From(err).Code != "COMMIT_OUTCOME_UNKNOWN" || f.play.commits != 1 || f.play.discards != 1 {
		t.Fatalf("unknown commit replayed/cleaned: %v %+v", err, f.play)
	}
	if f.play.commitBehavior != "CANCEL_IN_REVIEW_AND_SUBMIT" || f.play.notSent != "true" {
		t.Fatal("review options not explicit in commit")
	}
}
func TestPlanInputsAndImageSourceValidation(t *testing.T) {
	f := setup(t, []string{"en-US"})
	p := f.plan(t)
	path := filepath.Join(t.TempDir(), "plan.json")
	if err := releaseflow.Write(path, p); err != nil {
		t.Fatal(err)
	}
	if _, err := releaseflow.LoadPlan(path); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	b = bytes.Replace(b, []byte(`"rollout": 10`), []byte(`"rollout": 50`), 1)
	_ = os.WriteFile(path, b, 0600)
	if _, err := releaseflow.LoadPlan(path); err == nil {
		t.Fatal("accepted modified plan")
	}
	_ = os.WriteFile(f.sources[0], []byte("different file"), 0600)
	if _, err := releaseflow.BuildPlan(context.Background(), f.manifestPath, f.snapshotPath, "default"); fault.From(err).Exit != 4 {
		t.Fatalf("expected source hash conflict, got %v", err)
	}
}
func TestSymbolsOptionalAndRequiredPolicy(t *testing.T) {
	for _, optional := range []bool{false, true} {
		t.Run(fmt.Sprint(optional), func(t *testing.T) {
			f := setup(t, []string{"en-US"})
			mapping := filepath.Join(t.TempDir(), "mapping.txt")
			_ = os.WriteFile(mapping, []byte("com.example.Class -> a:\n"), 0600)
			f.manifest.Symbols = []releaseflow.Symbol{{Type: "proguard", File: mapping, Optional: optional}}
			b, _ := yaml.Marshal(f.manifest)
			_ = os.WriteFile(f.manifestPath, b, 0600)
			p := f.plan(t)
			f.play.symbolStatus = 400
			out, err := f.service.Apply(context.Background(), p)
			f.play.mu.Lock()
			defer f.play.mu.Unlock()
			if optional {
				if err != nil || f.play.commits != 1 || len(out.Warnings) != 1 {
					t.Fatalf("optional: %+v %v", out, err)
				}
			} else if err == nil || f.play.commits != 0 {
				t.Fatal("required symbol rejection committed")
			}
		})
	}
}

func TestCallerOwnedImageSyncNeverCommitsOrDeletes(t *testing.T) {
	f := setup(t, []string{"en-US", "pt-PT"})
	p, err := releaseflow.BuildImagePlan(context.Background(), f.snapshotPath, "en-US", "phoneScreenshots", f.sources, f.manifest.Images[0].Replace)
	if err != nil {
		t.Fatal(err)
	}
	id, err := f.client.Begin(context.Background(), f.manifest.App)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.service.SyncOnly(context.Background(), p, id); err != nil {
		t.Fatal(err)
	}
	f.play.mu.Lock()
	if f.play.commits != 0 || f.play.validates != 0 || f.play.discards != 1 || f.play.bundles != 0 {
		t.Error("helper took ownership of caller edit")
	}
	f.play.mu.Unlock()
	if err = f.client.Discard(context.Background(), f.manifest.App, id); err != nil {
		t.Fatal(err)
	}
}
func TestInheritanceRequiresExplicitConfirmationAndSource(t *testing.T) {
	f := setup(t, []string{"en-US", "pt-PT"})
	snapshot, err := releaseflow.LoadSnapshot(f.snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Images["pt-PT/phoneScreenshots"] = []releaseflow.Image{}
	snapshotPath := filepath.Join(t.TempDir(), "inherited.json")
	if err = releaseflow.Write(snapshotPath, snapshot); err != nil {
		t.Fatal(err)
	}
	m := releaseflow.Manifest{App: f.manifest.App, Profile: "default", Track: "production", Images: []releaseflow.ImageChange{{Locale: "pt-PT", Type: "phoneScreenshots", Mode: "inherit", InheritFrom: "en-US"}}, Status: "draft"}
	path := filepath.Join(t.TempDir(), "inherit.yaml")
	b, _ := yaml.Marshal(m)
	_ = os.WriteFile(path, b, 0600)
	if _, err = releaseflow.BuildPlan(context.Background(), path, snapshotPath, "default"); err == nil {
		t.Fatal("silently assumed inheritance")
	}
	m.Images[0].InheritanceConfirmed = true
	b, _ = yaml.Marshal(m)
	_ = os.WriteFile(path, b, 0600)
	p, err := releaseflow.BuildPlan(context.Background(), path, snapshotPath, "default")
	if err != nil || len(p.TargetImages) != 0 {
		t.Fatalf("preserve confirmed inheritance: %+v %v", p, err)
	}
}

func TestPromoteExistingArtifactSetWithReviewedReplacement(t *testing.T) {
	f := setup(t, []string{"en-US"})
	m := releaseflow.Manifest{App: f.manifest.App, Profile: "default", Track: "production", FromTrack: "internal", VersionCode: 123, Status: "completed", Rollout: 100, ReplaceExisting: true}
	f.play.mu.Lock()
	f.play.live.Source = &pub.Track{Track: "internal", Releases: []*pub.TrackRelease{{Name: "Tested build", VersionCodes: []int64{123, 124}, Status: "completed", ReleaseNotes: []*pub.LocalizedText{{Language: "en-US", Text: "Keep tested notes"}}}}}
	f.play.mu.Unlock()
	snapshot, err := f.service.Capture(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	manifestPath, snapshotPath := filepath.Join(dir, "promote.yaml"), filepath.Join(dir, "snapshot.json")
	b, _ := yaml.Marshal(m)
	_ = os.WriteFile(manifestPath, b, 0600)
	if err = releaseflow.Write(snapshotPath, snapshot); err != nil {
		t.Fatal(err)
	}
	p, err := releaseflow.BuildPlan(context.Background(), manifestPath, snapshotPath, "default")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.RemovedVersionCodes) != 1 || p.RemovedVersionCodes[0] != 99 {
		t.Fatal("missing target artifact replacement diff")
	}
	out, err := f.service.Apply(context.Background(), p)
	if err != nil || !out.Committed {
		t.Fatalf("promotion: %+v %v", out, err)
	}
	f.play.mu.Lock()
	defer f.play.mu.Unlock()
	if f.play.bundles != 0 || f.play.commits != 1 || len(f.play.live.Track.Releases) != 1 {
		t.Fatal("promotion re-uploaded or committed incorrectly")
	}
	selected := f.play.live.Track.Releases[0]
	if len(selected.VersionCodes) != 2 || selected.Name != "Tested build" || selected.ReleaseNotes[0].Text != "Keep tested notes" {
		t.Fatal("lost promoted artifact set or metadata")
	}
	if f.play.live.Source.Track != "internal" || f.play.live.Source.Releases[0].Status != "completed" {
		t.Fatal("source track changed")
	}
	// A changed source is also a conflict, even if the target did not change.
	f.play.live.Track = snapshot.Track
	f.play.live.Source.Releases[0].Name = "Changed after snapshot"
	f.play.mu.Unlock()
	_, err = f.service.Apply(context.Background(), p)
	f.play.mu.Lock()
	if err == nil || f.play.commits != 1 {
		t.Fatal("promotion ignored source drift")
	}
}

func TestCompleteExistingRolloutWithoutUploadingAgain(t *testing.T) {
	f := setup(t, []string{"en-US"})
	m := releaseflow.Manifest{App: f.manifest.App, Profile: "default", Track: "production", VersionCode: 123, Status: "completed", Rollout: 100, ReplaceExisting: true}
	f.play.mu.Lock()
	f.play.live.Track.Releases = append(f.play.live.Track.Releases, &pub.TrackRelease{Name: "Already tested", VersionCodes: []int64{123}, Status: "inProgress", UserFraction: 0.25, ReleaseNotes: []*pub.LocalizedText{{Language: "en-US", Text: "Preserve these notes"}}})
	f.play.mu.Unlock()
	snapshot, err := f.service.Capture(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	manifestPath, snapshotPath := filepath.Join(dir, "complete.yaml"), filepath.Join(dir, "snapshot.json")
	b, _ := yaml.Marshal(m)
	_ = os.WriteFile(manifestPath, b, 0600)
	if err = releaseflow.Write(snapshotPath, snapshot); err != nil {
		t.Fatal(err)
	}
	p, err := releaseflow.BuildPlan(context.Background(), manifestPath, snapshotPath, "default")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.RemovedVersionCodes) != 1 || p.RemovedVersionCodes[0] != 99 {
		t.Fatal("missing fallback supersession diff")
	}
	out, err := f.service.Apply(context.Background(), p)
	if err != nil || !out.Committed {
		t.Fatalf("existing rollout: %+v %v", out, err)
	}
	f.play.mu.Lock()
	defer f.play.mu.Unlock()
	if f.play.bundles != 0 || f.play.commits != 1 || len(f.play.live.Track.Releases) != 1 {
		t.Fatal("existing rollout re-uploaded AAB or committed incorrectly")
	}
	selected := f.play.live.Track.Releases[0]
	if selected.Status != "completed" || selected.Name != "Already tested" || selected.ReleaseNotes[0].Text != "Preserve these notes" {
		t.Fatal("lost existing release metadata")
	}
	m.Status = "inProgress"
	m.Rollout = 10
	b, _ = yaml.Marshal(m)
	_ = os.WriteFile(manifestPath, b, 0600)
	if _, err = releaseflow.BuildPlan(context.Background(), manifestPath, snapshotPath, "default"); err == nil {
		t.Fatal("manifest decreased staged rollout")
	}
}
