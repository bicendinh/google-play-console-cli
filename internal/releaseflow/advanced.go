package releaseflow

import (
	"archive/zip"
	"context"
	"os"

	"playops/internal/fault"
	"playops/internal/publisher"
)

func BuildImagePlan(ctx context.Context, snapshotPath, locale, kind string, sources []string, replacements map[int]string) (Plan, error) {
	snapshot, err := LoadSnapshot(snapshotPath)
	if err != nil {
		return Plan{}, err
	}
	m := Manifest{App: snapshot.App, Profile: snapshot.Profile, Track: snapshot.Track.Track, Status: "draft", Images: []ImageChange{{Locale: locale, Type: kind, Mode: "replace", SourceFiles: sources, Replace: replacements}}}
	input, err := HashFile(snapshotPath)
	if err != nil {
		return Plan{}, err
	}
	p, err := prepare(ctx, m, snapshot, map[string]File{input.Path: input})
	if err == nil {
		p.Digest = p.digest()
	}
	return p, err
}

// SyncOnly mutates the caller's edit without validating, committing or deleting
// it. On error the caller must abort that edit, not continue to a commit.
func (s Service) SyncOnly(ctx context.Context, p Plan, edit string) error {
	if p.Digest == "" || p.Digest != p.digest() {
		return fault.Input("Modified image plan")
	}
	if p.Manifest.ChangesTrack() || len(p.Manifest.Listings) > 0 || len(p.Manifest.Symbols) > 0 || len(p.TargetImages) == 0 {
		return fault.Input("images sync requires an image-only plan")
	}
	if err := VerifyInputs(p); err != nil {
		return err
	}
	rebuilt, err := prepare(ctx, p.Manifest, p.Baseline, fileMap(p.Inputs))
	if err != nil {
		return err
	}
	if !publisher.Same(rebuilt.TargetImages, p.TargetImages) {
		return fault.Input("Image plan differs from its validated manifest")
	}
	staged, cleanup, err := stage(p)
	if err != nil {
		return err
	}
	defer cleanup()
	for _, g := range p.TargetImages {
		actual, err := s.API.Images(ctx, p.Manifest.App, edit, g.Locale, g.Type)
		if err != nil {
			return err
		}
		before := []Image{}
		for _, im := range actual {
			before = append(before, Image{ID: im.Id, SHA256: im.Sha256})
		}
		if !publisher.Same(before, p.Baseline.Images[key(g.Locale, g.Type)]) {
			return fault.Conflict("Image state changed after snapshot")
		}
	}
	open := func(path string) (*os.File, error) {
		local, ok := staged[path]
		if !ok {
			return nil, fault.Input("File not in plan")
		}
		return os.Open(local)
	}
	for _, g := range p.TargetImages {
		if err = s.SyncImages(ctx, p.Manifest.App, edit, g, open); err != nil {
			return err
		}
	}
	return nil
}
func MergeListing(locale string, before *Listing, patch ListingChange) (*Listing, error) {
	patch.Locale = locale
	if err := ValidLocale(locale); err != nil {
		return nil, err
	}
	if err := validateText(patch); err != nil {
		return nil, err
	}
	if patch.Title == nil && patch.ShortDescription == nil && patch.FullDescription == nil && patch.Video == nil {
		return nil, fault.Input("Listing patch is empty")
	}
	next := Listing{}
	if before != nil {
		next = *before
	}
	if patch.Title != nil {
		next.Title = *patch.Title
	}
	if patch.ShortDescription != nil {
		next.ShortDescription = *patch.ShortDescription
	}
	if patch.FullDescription != nil {
		next.FullDescription = *patch.FullDescription
	}
	if patch.Video != nil {
		next.Video = *patch.Video
	}
	if next.Title == "" || next.ShortDescription == "" || next.FullDescription == "" {
		return nil, fault.Input("New listing requires title, shortDescription and fullDescription")
	}
	return &next, nil
}
func (s Service) UpdateListing(ctx context.Context, app, edit, locale string, patch ListingChange) (Diff, error) {
	all, err := s.API.Listings(ctx, app, edit)
	if err != nil {
		return Diff{}, err
	}
	var before *Listing
	for _, l := range all {
		if l.Language == locale {
			before = localListing(l)
		}
	}
	next, err := MergeListing(locale, before, patch)
	if err != nil {
		return Diff{}, err
	}
	if err = s.API.PutListing(ctx, app, edit, locale, remoteListing(next, locale)); err != nil {
		return Diff{}, err
	}
	all, err = s.API.Listings(ctx, app, edit)
	if err != nil {
		return Diff{}, err
	}
	var actual *Listing
	for _, l := range all {
		if l.Language == locale {
			actual = localListing(l)
		}
	}
	if !publisher.Same(next, actual) {
		return Diff{}, fault.Conflict("Listing readback differs from requested fields")
	}
	return Diff{Resource: "listing/" + locale, Before: before, After: next}, nil
}
func ValidateSymbols(path, kind string) error {
	f, err := HashFile(path)
	if err != nil {
		return err
	}
	if f.Size == 0 {
		return fault.Input("Symbol/mapping file is empty")
	}
	switch kind {
	case "nativeCode":
		z, err := zip.OpenReader(path)
		if err != nil {
			return fault.Input("nativeCode symbols must be a ZIP archive")
		}
		defer z.Close()
		if len(z.File) == 0 {
			return fault.Input("Symbols ZIP is empty")
		}
	case "proguard":
	case "":
		return fault.Input("Symbol type is required")
	default:
		return fault.Input("Symbol type must be nativeCode or proguard")
	}
	return nil
}
