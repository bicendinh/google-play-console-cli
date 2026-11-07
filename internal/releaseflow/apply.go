package releaseflow

import (
	"context"
	"errors"
	gapi "google.golang.org/api/googleapi"
	"os"
	"sort"
	"strings"

	"playops/internal/fault"
	"playops/internal/publisher"
)

func (s Service) Apply(ctx context.Context, p Plan) (Outcome, error) {
	if p.FormatVersion != 1 || p.Digest == "" || p.Digest != p.digest() {
		return Outcome{}, fault.Input("Plan digest does not match")
	}
	if err := VerifyInputs(p); err != nil {
		return Outcome{}, err
	}
	if p.SourceManifest != "" {
		m, e := LoadManifest(p.SourceManifest, p.Manifest.Profile)
		if e != nil {
			return Outcome{}, e
		}
		// CLI alpha policy overrides are stored in the reviewed plan, while the
		// original manifest bytes remain a hashed input.
		m.FlattenAlpha = p.Manifest.FlattenAlpha
		if !publisher.Same(m, p.Manifest) {
			return Outcome{}, fault.Conflict("Manifest contents no longer match the reviewed plan")
		}
	}
	if len(p.Diff) == 0 && len(p.Manifest.Symbols) == 0 {
		return Outcome{}, fault.Input("Plan contains no changes; no edit is needed")
	}
	// Recompute the reviewed result locally; a caller cannot bypass validation by
	// changing desired state and updating only the digest in a JSON plan.
	rebuilt, err := prepare(ctx, p.Manifest, p.Baseline, fileMap(p.Inputs))
	if err != nil {
		return Outcome{}, err
	}
	if !publisher.Same(rebuilt.TargetTrack, p.TargetTrack) || !publisher.Same(rebuilt.TargetListings, p.TargetListings) || !publisher.Same(rebuilt.TargetImages, p.TargetImages) || !publisher.Same(rebuilt.Inputs, p.Inputs) {
		return Outcome{}, fault.Input("Plan state does not match its validated manifest")
	}
	staged, cleanup, err := stage(p)
	if err != nil {
		return Outcome{}, err
	}
	defer cleanup()
	warnings := []string{}
	open := func(path string) (*os.File, error) {
		local, ok := staged[path]
		if !ok {
			return nil, fault.Input("Upload file is not present in reviewed plan")
		}
		return os.Open(local)
	}
	commit := func(ctx context.Context, id string) error {
		return s.API.CommitWithOptions(ctx, p.Manifest.App, id, p.Manifest.Review)
	}
	id, err := publisher.Transaction(ctx, s.API, p.Manifest.App, commit, func(edit string) error {
		current, e := s.captureInEdit(ctx, p.Manifest, edit)
		if e != nil {
			return e
		}
		if !sameBaseline(current, p.Baseline) {
			return fault.Conflict("Remote state changed after snapshot; capture and review a new plan")
		}
		m := p.Manifest
		if m.Bundle != "" {
			f, e := open(m.Bundle)
			if e != nil {
				return e
			}
			code, e := s.API.Upload(ctx, m.App, edit, f)
			_ = f.Close()
			if e != nil {
				return e
			}
			if code != m.VersionCode {
				return fault.Conflict("Uploaded AAB versionCode does not match the reviewed manifest")
			}
		}
		locales := []string{}
		for locale := range p.TargetListings {
			locales = append(locales, locale)
		}
		sort.Strings(locales)
		for _, locale := range locales {
			if e = s.API.PutListing(ctx, m.App, edit, locale, remoteListing(p.TargetListings[locale], locale)); e != nil {
				return e
			}
		}
		for _, group := range p.TargetImages {
			if e = s.SyncImages(ctx, m.App, edit, group, open); e != nil {
				return e
			}
		}
		for _, symbol := range m.Symbols {
			f, e := open(symbol.File)
			if e != nil {
				return e
			}
			e = s.API.Symbols(ctx, m.App, edit, m.VersionCode, symbol.Type, f)
			_ = f.Close()
			if e != nil {
				// Optional validation failures are explicit warnings. Transport, permission
				// and edit conflicts abort because the edit's state may be uncertain.
				var rejection *gapi.Error
				if symbol.Optional && errors.As(e, &rejection) && rejection.Code == 400 {
					warnings = append(warnings, symbol.Type+": "+fault.From(e).Message)
					continue
				}
				return e
			}
		}
		if m.ChangesTrack() {
			if e = s.API.Update(ctx, m.App, edit, p.TargetTrack); e != nil {
				return e
			}
		}
		// Verify after all mutations, while the edit is still uncommitted.
		readback, e := s.captureInEdit(ctx, m, edit)
		if e != nil {
			return e
		}
		if !publisher.Same(readback.Track, p.TargetTrack) {
			return fault.Conflict("Track readback does not match the plan")
		}
		if !publisher.Same(readback.SourceTrack, p.Baseline.SourceTrack) {
			return fault.Conflict("Promotion source track changed during the transaction")
		}
		for locale, before := range p.Baseline.Listings {
			expected := before
			if target, ok := p.TargetListings[locale]; ok {
				expected = target
			}
			if !publisher.Same(readback.Listings[locale], expected) {
				return fault.Conflict("Listing readback mismatch for %s", locale)
			}
		}
		changed := map[string]bool{}
		for _, g := range p.TargetImages {
			changed[key(g.Locale, g.Type)] = true
			if e = checkImages(readback.Images[key(g.Locale, g.Type)], g); e != nil {
				return e
			}
		}
		for group, before := range p.Baseline.Images {
			if !changed[group] && !publisher.Same(before, readback.Images[group]) {
				return fault.Conflict("Unmodified image group changed: %s", group)
			}
		}
		return nil
	})
	if err != nil {
		f := fault.From(err)
		if f.Code == "COMMIT_OUTCOME_UNKNOWN" {
			f.Details = map[string]any{"app": p.Manifest.App, "track": p.Manifest.Track, "versionCode": p.Manifest.VersionCode, "editId": id, "planDigest": p.Digest}
			err = f
		}
	}
	return Outcome{EditID: id, Committed: err == nil, ReviewStatus: "unknown", PlanDigest: p.Digest, Warnings: warnings}, err
}
func sameBaseline(a, b Snapshot) bool {
	return a.App == b.App && a.Profile == b.Profile && publisher.Same(a.Track, b.Track) && publisher.Same(a.SourceTrack, b.SourceTrack) && publisher.Same(a.Listings, b.Listings) && publisher.Same(a.Images, b.Images)
}
func checkImages(actual []Image, group PlannedImages) error {
	if len(actual) != len(group.Files) {
		return fault.Conflict("Image count readback mismatch for %s/%s", group.Locale, group.Type)
	}
	for i, f := range group.Files {
		if actual[i].SHA256 == "" || strings.ToLower(actual[i].SHA256) != f.SHA256 {
			return fault.Conflict("Image order/hash mismatch at position %d for %s/%s", i+1, group.Locale, group.Type)
		}
	}
	return nil
}
func (s Service) SyncImages(ctx context.Context, app, edit string, group PlannedImages, open func(string) (*os.File, error)) error {
	// Verify listing existence before deleting or uploading assets (unsupported
	// locale uploads can otherwise silently succeed without making a change).
	listings, err := s.API.Listings(ctx, app, edit)
	if err != nil {
		return err
	}
	exists := false
	for _, l := range listings {
		if l.Language == group.Locale {
			exists = true
		}
	}
	if !exists {
		return fault.Input("Image locale does not exist in this edit: %s", group.Locale)
	}
	if err = s.API.DeleteImages(ctx, app, edit, group.Locale, group.Type); err != nil {
		return err
	}
	for _, file := range group.Files {
		f, e := open(imageUploadKey(file))
		if e != nil {
			return e
		}
		uploaded, e := s.API.UploadImage(ctx, app, edit, group.Locale, group.Type, f)
		_ = f.Close()
		if e != nil {
			return e
		}
		if uploaded == nil || strings.ToLower(uploaded.Sha256) != file.SHA256 {
			return fault.Conflict("Upload returned no matching image hash for %s/%s", group.Locale, group.Type)
		}
	}
	remote, err := s.API.Images(ctx, app, edit, group.Locale, group.Type)
	if err != nil {
		return err
	}
	actual := []Image{}
	for _, im := range remote {
		actual = append(actual, Image{ID: im.Id, SHA256: im.Sha256})
	}
	return checkImages(actual, group)
}
