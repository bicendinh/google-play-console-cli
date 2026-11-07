package releaseflow

import (
	"context"
	"sort"
	"strings"
	"time"

	pub "google.golang.org/api/androidpublisher/v3"
	"playops/internal/fault"
	"playops/internal/publisher"
)

type Verification struct {
	Status          string    `json:"status"`
	Scope           string    `json:"scope"`
	PlanDigest      string    `json:"planDigest"`
	CheckedAt       time.Time `json:"checkedAt"`
	Differences     []Diff    `json:"differences"`
	MissingEvidence []string  `json:"missingEvidence"`
	Committed       *bool     `json:"committed"`
}

// Verify reads a caller-owned edit, or creates and discards a temporary edit.
// It never validates, commits, uploads or replays the reviewed plan.
func (s Service) Verify(ctx context.Context, p Plan, edit string) (Verification, error) {
	v := Verification{Status: "unknown", Scope: "edit-state", PlanDigest: p.Digest, Differences: []Diff{}, MissingEvidence: []string{}}
	if p.Digest == "" || p.Digest != p.digest() {
		return v, fault.Input("Modified plan")
	}
	if s.Now != nil {
		v.CheckedAt = s.Now().UTC()
	}
	read := func(id string) error {
		actual, err := s.captureInEdit(ctx, p.Manifest, id)
		if err != nil {
			return err
		}
		// Compare the release selected by this plan, not historical releases that
		// Google may retire after publication. Unrelated releases can evolve later.
		if p.Manifest.ChangesTrack() {
			want := selectedRelease(p.TargetTrack, p.Manifest.VersionCode)
			got := selectedRelease(actual.Track, p.Manifest.VersionCode)
			if got == nil {
				v.MissingEvidence = append(v.MissingEvidence, "Selected version is absent; this does not prove commit failure")
			} else if !publisher.Same(want, got) {
				v.Differences = append(v.Differences, Diff{Resource: "release", Before: want, After: got})
			}
		}
		locales := []string{}
		for locale := range p.TargetListings {
			locales = append(locales, locale)
		}
		sort.Strings(locales)
		for _, locale := range locales {
			want := p.TargetListings[locale]
			if !publisher.Same(want, actual.Listings[locale]) {
				v.Differences = append(v.Differences, Diff{Resource: "listing/" + locale, Before: want, After: actual.Listings[locale]})
			}
		}
		for _, group := range p.TargetImages {
			images := actual.Images[key(group.Locale, group.Type)]
			hashes := []string{}
			want := []string{}
			for _, im := range images {
				hashes = append(hashes, strings.ToLower(im.SHA256))
				if im.SHA256 == "" {
					v.MissingEvidence = append(v.MissingEvidence, "Image SHA-256 missing for "+key(group.Locale, group.Type))
				}
			}
			for _, file := range group.Files {
				want = append(want, file.SHA256)
			}
			if !publisher.Same(want, hashes) {
				v.Differences = append(v.Differences, Diff{Resource: "images/" + key(group.Locale, group.Type), Before: want, After: hashes})
			}
		}
		// Preserved groups are deliberate plan requirements too. Compare content,
		// not transient image IDs. Console fallback behavior is not exposed by API.
		for _, group := range p.Manifest.Images {
			if group.Mode != "preserve" && group.Mode != "inherit" {
				continue
			}
			k := key(group.Locale, group.Type)
			want := []string{}
			got := []string{}
			for _, im := range p.Baseline.Images[k] {
				want = append(want, strings.ToLower(im.SHA256))
			}
			for _, im := range actual.Images[k] {
				got = append(got, strings.ToLower(im.SHA256))
				if im.SHA256 == "" {
					v.MissingEvidence = append(v.MissingEvidence, "Preserved image hash unavailable for "+k)
				}
			}
			if !publisher.Same(want, got) {
				v.Differences = append(v.Differences, Diff{Resource: "images/" + k, Before: want, After: got})
			}
			if group.Mode == "inherit" {
				v.MissingEvidence = append(v.MissingEvidence, "Default-locale inheritance cannot be verified through the image API: "+k)
			}
		}
		if len(p.Manifest.Symbols) > 0 {
			v.MissingEvidence = append(v.MissingEvidence, "Symbol uploads cannot be verified through readback")
		}
		return nil
	}
	var err error
	if edit != "" {
		err = read(edit)
	} else {
		_, err = publisher.Transaction(ctx, s.API, p.Manifest.App, nil, read)
		v.Scope = "current-state"
	}
	if err != nil {
		return v, err
	}
	if len(v.MissingEvidence) == 0 {
		if len(v.Differences) > 0 {
			v.Status = "drift"
		} else {
			v.Status = "match"
		}
	}
	return v, nil
}
func selectedRelease(track *pub.Track, code int64) *pub.TrackRelease {
	if track != nil {
		for _, r := range track.Releases {
			for _, v := range r.VersionCodes {
				if v == code {
					return r
				}
			}
		}
	}
	return nil
}
