package releaseflow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	pub "google.golang.org/api/androidpublisher/v3"
	"playops/internal/fault"
	"playops/internal/publisher"
)

func imageGroups(m Manifest) [][2]string {
	groups := map[string][2]string{}
	add := func(locale, kind string) { groups[key(locale, kind)] = [2]string{locale, kind} }
	for _, v := range m.Images {
		add(v.Locale, v.Type)
		if v.Mode == "inherit" {
			add(v.InheritFrom, v.Type)
		}
	}
	for _, locale := range m.RequiredLocales {
		for _, kind := range m.RequiredImageTypes {
			add(locale, kind)
		}
	}
	keys := []string{}
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	result := [][2]string{}
	for _, k := range keys {
		result = append(result, groups[k])
	}
	return result
}
func listingLocales(m Manifest) []string {
	set := map[string]bool{}
	for _, locale := range m.RequiredLocales {
		set[locale] = true
	}
	for _, n := range m.Notes {
		set[n.Language] = true
	}
	for _, l := range m.Listings {
		set[l.Locale] = true
	}
	for _, g := range imageGroups(m) {
		set[g[0]] = true
	}
	locales := []string{}
	for l := range set {
		locales = append(locales, l)
	}
	sort.Strings(locales)
	return locales
}
func (s Service) Capture(ctx context.Context, m Manifest) (Snapshot, error) {
	var snapshot Snapshot
	_, err := publisher.Transaction(ctx, s.API, m.App, nil, func(id string) error { var e error; snapshot, e = s.captureInEdit(ctx, m, id); return e })
	return snapshot, err
}
func (s Service) captureInEdit(ctx context.Context, m Manifest, id string) (Snapshot, error) {
	snap := Snapshot{FormatVersion: 1, App: m.App, Profile: m.Profile, Listings: map[string]*Listing{}, Images: map[string][]Image{}}
	var err error
	snap.Track, err = s.API.Track(ctx, m.App, id, m.Track)
	if err != nil {
		return snap, err
	}
	if m.FromTrack != "" {
		snap.SourceTrack, err = s.API.Track(ctx, m.App, id, m.FromTrack)
		if err != nil {
			return snap, err
		}
	}
	listings, err := s.API.Listings(ctx, m.App, id)
	if err != nil {
		return snap, err
	}
	byLocale := map[string]*Listing{}
	for _, l := range listings {
		byLocale[l.Language] = localListing(l)
	}
	for _, locale := range listingLocales(m) {
		snap.Listings[locale] = byLocale[locale]
	}
	for _, g := range imageGroups(m) {
		images := []Image{}
		if byLocale[g[0]] != nil {
			remote, e := s.API.Images(ctx, m.App, id, g[0], g[1])
			if e != nil {
				return snap, e
			}
			for _, im := range remote {
				images = append(images, Image{ID: im.Id, SHA256: strings.ToLower(im.Sha256)})
			}
		}
		snap.Images[key(g[0], g[1])] = images
	}
	if s.Now != nil {
		snap.CapturedAt = s.Now().UTC()
	}
	return snap, nil
}
func BuildPlan(ctx context.Context, manifestPath, snapshotPath, profile string) (Plan, error) {
	return BuildPlanWithAlpha(ctx, manifestPath, snapshotPath, profile, "")
}
func BuildPlanWithAlpha(ctx context.Context, manifestPath, snapshotPath, profile, alpha string) (Plan, error) {
	m, err := LoadManifest(manifestPath, profile)
	if err != nil {
		return Plan{}, err
	}
	if alpha != "" {
		m.FlattenAlpha = alpha
	}
	snapshot, err := LoadSnapshot(snapshotPath)
	if err != nil {
		return Plan{}, err
	}
	inputs := map[string]File{}
	for _, path := range []string{manifestPath, snapshotPath} {
		f, e := HashFile(path)
		if e != nil {
			return Plan{}, e
		}
		inputs[f.Path] = f
	}
	plan, err := prepare(ctx, m, snapshot, inputs)
	if err != nil {
		return plan, err
	}
	plan.SourceManifest, _ = filepath.Abs(manifestPath)
	plan.Digest = plan.digest()
	return plan, nil
}
func prepare(ctx context.Context, m Manifest, baseline Snapshot, inputs map[string]File) (Plan, error) {
	p := Plan{Warnings: imageIntentWarnings(m), FormatVersion: 1, Manifest: m, Baseline: baseline, TargetListings: map[string]*Listing{}, TargetImages: []PlannedImages{}, Diff: []Diff{}, RemovedVersionCodes: []int64{}}
	if err := ValidateManifest(m); err != nil {
		return p, err
	}
	if baseline.FormatVersion != 1 || (baseline.Complete != nil && !*baseline.Complete) || baseline.Track == nil || baseline.App != m.App || baseline.Profile != m.Profile || baseline.Track.Track != m.Track {
		return p, fault.Input("Snapshot must match the manifest app, profile and track")
	}
	if m.FromTrack != "" && (baseline.SourceTrack == nil || baseline.SourceTrack.Track != m.FromTrack) {
		return p, fault.Input("Promotion snapshot is missing the expected source track")
	}
	for _, locale := range listingLocales(m) {
		if _, ok := baseline.Listings[locale]; !ok {
			return p, fault.Input("Snapshot is missing listing locale %s", locale)
		}
	}
	for _, g := range imageGroups(m) {
		if _, ok := baseline.Images[key(g[0], g[1])]; !ok {
			return p, fault.Input("Snapshot is missing image group %s/%s", g[0], g[1])
		}
	}
	selected := baseline
	if m.FromTrack == "" {
		selected.SourceTrack = nil
	}
	selected.Listings = map[string]*Listing{}
	selected.Images = map[string][]Image{}
	for _, locale := range listingLocales(m) {
		selected.Listings[locale] = baseline.Listings[locale]
	}
	for _, g := range imageGroups(m) {
		selected.Images[key(g[0], g[1])] = baseline.Images[key(g[0], g[1])]
	}
	baseline = selected
	p.Baseline = selected
	add := func(path string) (File, error) {
		f, e := HashFile(path)
		if e == nil {
			inputs[f.Path] = f
		}
		return f, e
	}
	if m.Bundle != "" {
		if _, err := add(m.Bundle); err != nil {
			return p, err
		}
	}
	for _, change := range m.Listings {
		if change.File != "" {
			if _, err := add(change.File); err != nil {
				return p, err
			}
		}
		old := baseline.Listings[change.Locale]
		next := Listing{}
		if old != nil {
			next = *old
		}
		if change.Title != nil {
			next.Title = *change.Title
		}
		if change.ShortDescription != nil {
			next.ShortDescription = *change.ShortDescription
		}
		if change.FullDescription != nil {
			next.FullDescription = *change.FullDescription
		}
		if change.Video != nil {
			next.Video = *change.Video
		}
		if next.Title == "" || next.ShortDescription == "" || next.FullDescription == "" {
			return p, fault.Input("New listing %s needs title, shortDescription and fullDescription", change.Locale)
		}
		p.TargetListings[change.Locale] = &next
		if !publisher.Same(old, &next) {
			p.Diff = append(p.Diff, Diff{Resource: "listing/" + change.Locale, Before: old, After: next})
		}
	}
	for _, locale := range m.RequiredLocales {
		if baseline.Listings[locale] == nil && p.TargetListings[locale] == nil {
			return p, fault.Input("Required listing locale %s is missing", locale)
		}
	}
	plannedCounts := map[string]int{}
	imageChanges, err := expandedImages(m.Images)
	if err != nil {
		return p, err
	}
	for _, change := range imageChanges {
		group := key(change.Locale, change.Type)
		before := baseline.Images[group]
		if baseline.Listings[change.Locale] == nil && p.TargetListings[change.Locale] == nil {
			return p, fault.Input("Image locale %s has no listing; provide its complete listing fields", change.Locale)
		}
		switch change.Mode {
		case "preserve":
			plannedCounts[group] = len(before)
			continue
		case "inherit":
			if len(before) != 0 {
				return p, fault.Input("Cannot assume inheritance while locale %s has explicit/effective images", change.Locale)
			}
			if len(baseline.Images[key(change.InheritFrom, change.Type)]) == 0 {
				return p, fault.Input("Confirmed inheritance source has no images")
			}
			plannedCounts[group] = len(baseline.Images[key(change.InheritFrom, change.Type)])
			continue
		}
		files := append([]string(nil), change.Files...)
		if len(change.Replace) > 0 {
			if len(change.SourceFiles) != len(before) {
				return p, fault.Input("sourceFiles must cover every baseline image in %s", group)
			}
			files = append([]string(nil), change.SourceFiles...)
			for i, path := range change.SourceFiles {
				f, e := add(path)
				if e != nil {
					return p, e
				}
				if before[i].SHA256 == "" || f.SHA256 != before[i].SHA256 {
					return p, fault.Conflict("Source image %d does not match snapshot hash in %s", i+1, group)
				}
			}
			for pos, path := range change.Replace {
				if pos < 1 || pos > len(files) {
					return p, fault.Input("Replacement position %d is outside %s", pos, group)
				}
				files[pos-1] = path
			}
		}
		if err := imageCount(change.Type, len(files)); err != nil {
			return p, err
		}
		if len(files) < len(before) {
			if !change.AllowImageCountDecrease {
				e := fault.New("IMAGE_COUNT_DECREASE", "Image replacement reduces the complete set; use positional replace with sourceFiles, or explicitly set allowImageCountDecrease: true for this locale/type", 2)
				e.Details = map[string]any{"locale": change.Locale, "type": change.Type, "beforeCount": len(before), "afterCount": len(files)}
				return p, e
			}
			p.Warnings = append(p.Warnings, Issue{Field: "images.files", Locale: change.Locale, Message: fmt.Sprintf("Explicitly approved image count decrease for %s: %d -> %d", group, len(before), len(files))})
		}
		target := PlannedImages{Locale: change.Locale, Type: change.Type, Files: []File{}}
		for _, path := range files {
			_, e := add(path)
			if e != nil {
				return p, e
			}
			f, e := ImageFile(path, change.Type, m.FlattenAlpha)
			if e != nil {
				return p, e
			}
			target.Files = append(target.Files, f)
		}
		p.TargetImages = append(p.TargetImages, target)
		plannedCounts[group] = len(files)
		p.Diff = append(p.Diff, Diff{Resource: "images/" + group, Before: before, After: target.Files})
	}
	for _, locale := range m.RequiredLocales {
		for _, kind := range m.RequiredImageTypes {
			group := key(locale, kind)
			count, ok := plannedCounts[group]
			if !ok {
				count = len(baseline.Images[group])
			}
			if err := imageCount(kind, count); err != nil {
				return p, fault.Input("Required image coverage %s: %s", group, err.Error())
			}
		}
	}
	for _, symbol := range m.Symbols {
		if err := ValidateSymbols(symbol.File, symbol.Type); err != nil {
			return p, err
		}
		if _, err := add(symbol.File); err != nil {
			return p, err
		}
	}
	if strings.HasSuffix(m.Preflight.Bundletool, ".jar") {
		if _, err := add(m.Preflight.Bundletool); err != nil {
			return p, err
		}
	}
	p.TargetTrack = publisher.CloneTrack(baseline.Track)
	if m.ChangesTrack() {
		var selected *pub.TrackRelease
		for _, r := range p.TargetTrack.Releases {
			for _, code := range r.VersionCodes {
				if code == m.VersionCode {
					selected = r
				}
			}
		}
		if m.FromTrack != "" {
			if selected != nil {
				return p, fault.Conflict("Version code already exists in target track")
			}
			for _, source := range publisher.CloneTrack(baseline.SourceTrack).Releases {
				for _, code := range source.VersionCodes {
					if code == m.VersionCode {
						selected = source
					}
				}
			}
			if selected == nil {
				return p, fault.Conflict("Version code not found in source track")
			}
			for _, existing := range p.TargetTrack.Releases {
				for _, code := range existing.VersionCodes {
					for _, incoming := range selected.VersionCodes {
						if code == incoming {
							return p, fault.Conflict("A source artifact already exists in the target track")
						}
					}
				}
			}
			selected.CountryTargeting = nil
			p.TargetTrack.Releases = append(p.TargetTrack.Releases, selected)
		} else if m.Bundle != "" {
			if selected != nil {
				return p, fault.Conflict("Version code already exists in target track")
			}
			selected = &pub.TrackRelease{Name: m.Name, VersionCodes: []int64{m.VersionCode}, Status: "draft"}
			p.TargetTrack.Releases = append(p.TargetTrack.Releases, selected)
		} else {
			if selected == nil {
				return p, fault.Conflict("Existing version code was not found in the target track")
			}
			if selected.Status != "draft" && selected.Status != "inProgress" && selected.Status != "halted" {
				return p, fault.Conflict("Only a draft or staged/ halted release can transition through a manifest")
			}
			if m.Status == "inProgress" && selected.UserFraction > m.Rollout/100 {
				return p, fault.Input("Manifest cannot decrease an existing staged rollout")
			}
		}
		if m.Name != "" {
			selected.Name = m.Name
		}
		if len(m.Notes) > 0 {
			selected.ReleaseNotes = []*pub.LocalizedText{}
			for _, n := range m.Notes {
				selected.ReleaseNotes = append(selected.ReleaseNotes, &pub.LocalizedText{Language: n.Language, Text: n.Text})
			}
		}
		for _, locale := range m.RequiredLocales {
			found := false
			for _, note := range selected.ReleaseNotes {
				if note.Language == locale {
					found = true
				}
			}
			if !found {
				return p, fault.Input("Missing release notes for required locale %s", locale)
			}
		}
		selected.Status = m.Status
		if m.Status == "inProgress" {
			selected.UserFraction = m.Rollout / 100
		} else {
			selected.UserFraction = 0
			selected.CountryTargeting = nil
		}
		if err := publisher.ResolveActive(p.TargetTrack, selected, m.ReplaceExisting); err != nil {
			return p, err
		}
		kept := map[int64]bool{}
		for _, r := range p.TargetTrack.Releases {
			for _, v := range r.VersionCodes {
				kept[v] = true
			}
		}
		for _, r := range baseline.Track.Releases {
			for _, v := range r.VersionCodes {
				if !kept[v] {
					p.RemovedVersionCodes = append(p.RemovedVersionCodes, v)
				}
			}
		}
		if !publisher.Same(baseline.Track, p.TargetTrack) {
			p.Diff = append(p.Diff, Diff{Resource: "track/" + m.Track, Before: baseline.Track, After: p.TargetTrack})
		}
	}
	checks, err := Preflight(ctx, m)
	p.Preflight = checks
	if err != nil {
		return p, err
	}
	p.Inputs = orderedFiles(inputs)
	return p, nil
}
func VerifyInputs(p Plan) error {
	for _, expected := range p.Inputs {
		actual, err := HashFile(expected.Path)
		if err != nil || actual != expected {
			return fault.Conflict("Plan input changed or disappeared: %s", expected.Path)
		}
	}
	return nil
}
func FileExists(path string) bool { _, err := os.Stat(path); return err == nil }
