package publisher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	pub "google.golang.org/api/androidpublisher/v3"
	gapi "google.golang.org/api/googleapi"

	"playops/internal/fault"
)

type API interface {
	Begin(context.Context, string) (string, error)
	Track(context.Context, string, string, string) (*pub.Track, error)
	Update(context.Context, string, string, *pub.Track) error
	Upload(context.Context, string, string, io.Reader) (int64, error)
	Validate(context.Context, string, string) error
	Commit(context.Context, string, string) error
	Discard(context.Context, string, string) error
}
type Service struct{ API API }
type Request struct {
	Action          string               `json:"action"`
	App             string               `json:"app"`
	Track           string               `json:"track"`
	From            string               `json:"fromTrack,omitempty"`
	VersionCode     int64                `json:"versionCode,omitempty"`
	Rollout         float64              `json:"rolloutPercent,omitempty"`
	Name            string               `json:"name,omitempty"`
	Notes           []*pub.LocalizedText `json:"releaseNotes,omitempty"`
	ReplaceExisting bool                 `json:"replaceExisting,omitempty"`
	Review          CommitOptions        `json:"review"`
}
type Outcome struct {
	App          string  `json:"app"`
	Track        string  `json:"track"`
	VersionCode  int64   `json:"versionCode"`
	VersionCodes []int64 `json:"versionCodes"`
	Status       string  `json:"rolloutStatus"`
	Percent      float64 `json:"rolloutPercent,omitempty"`
	Committed    bool    `json:"committed"`
	// Publication and review status must be fetched separately after commit.
	ReviewStatus string `json:"reviewStatus"`
}

func Check(r Request) error {
	if err := r.Review.Validate(); err != nil {
		return err
	}
	if math.IsNaN(r.Rollout) || math.IsInf(r.Rollout, 0) {
		return fault.Input("--rollout must be a finite percentage")
	}
	if r.Track == "" || strings.ContainsAny(r.Track, "/\\\n\r\t ") {
		return fault.Input("Provide a track identifier with --track (or --to for promote)")
	}
	if r.Action != "upload" && r.VersionCode <= 0 {
		return fault.Input("--version-code must be a positive integer")
	}
	if r.Action == "promote" && (r.From == "" || r.From == r.Track || strings.ContainsAny(r.From, "/\\\n\r\t ")) {
		return fault.Input("--from and --to must be different track identifiers")
	}
	production := r.Track == "production" || strings.HasSuffix(r.Track, ":production")
	if r.Action == "publish" || r.Action == "promote" {
		if (production && r.Rollout == 0) || r.Rollout < 0 || r.Rollout > 100 {
			return fault.Input("Production requires --rollout between 1 and 100 percent")
		}
		if r.Rollout > 0 && r.Rollout < 1 {
			return fault.Input("--rollout must be between 1 and 100 percent")
		}
	}
	if r.Action == "set" && (r.Rollout < 1 || r.Rollout > 100) {
		return fault.Input("--rollout must be between 1 and 100 percent")
	}
	return nil
}

// ReadTrack creates a short-lived edit because Google has no edit-free API for
// rollout fractions. It never validates or commits and always attempts cleanup.
func (s Service) ReadTrack(ctx context.Context, app, track string) (result *pub.Track, err error) {
	id, err := s.API.Begin(ctx, app)
	if err != nil {
		return nil, err
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		e := s.API.Discard(cleanupCtx, app, id)
		if err == nil && e != nil {
			err = e
		}
	}()
	return s.API.Track(ctx, app, id, track)
}

func (s Service) Execute(ctx context.Context, r Request, bundle io.Reader) (out Outcome, err error) {
	if err = Check(r); err != nil {
		return out, err
	}
	id, err := s.API.Begin(ctx, r.App)
	if err != nil {
		return out, editError(err, "create")
	}
	committed, unknown := false, false
	defer func() {
		if committed || unknown {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		cleanupErr := s.API.Discard(cleanupCtx, r.App, id)
		if cleanupErr != nil && err != nil {
			e := fault.From(err)
			e.Details = map[string]any{"editId": id, "cleanup": "failed; edit may remain until expiry"}
			err = e
		}
	}()
	target, e := s.API.Track(ctx, r.App, id, r.Track)
	if e != nil {
		return out, editError(e, "read track")
	}
	if target.Releases == nil {
		target.Releases = []*pub.TrackRelease{}
	}
	version := r.VersionCode
	switch r.Action {
	case "upload":
		if bundle == nil {
			return out, fault.Input("A signed AAB file is required")
		}
		version, e = s.API.Upload(ctx, r.App, id, bundle)
		if e != nil {
			return out, editError(e, "upload")
		}
		if find(target, version) != nil {
			return out, fault.Conflict("Version code %d already exists in track", version)
		}
		target.Releases = append(target.Releases, &pub.TrackRelease{Name: r.Name, VersionCodes: []int64{version}, Status: "draft", ReleaseNotes: r.Notes})
	case "publish", "promote":
		var selected *pub.TrackRelease
		if r.Action == "promote" {
			source, e := s.API.Track(ctx, r.App, id, r.From)
			if e != nil {
				return out, editError(e, "read source track")
			}
			original := find(source, version)
			if original == nil {
				return out, fault.Conflict("Version code %d not found in source track", version)
			}
			if find(target, version) != nil {
				return out, fault.Conflict("Version code %d already exists in target track", version)
			}
			copy := *original
			copy.VersionCodes = slices.Clone(original.VersionCodes)
			copy.CountryTargeting = nil
			selected = &copy
			target.Releases = append(target.Releases, selected)
		} else {
			selected = find(target, version)
			if selected == nil {
				return out, fault.Conflict("Version code %d not found in track", version)
			}
			if selected.Status != "draft" {
				return out, fault.Conflict("publish only accepts a draft release; use rollout commands for active releases")
			}
		}
		percent := r.Rollout
		if percent == 0 {
			percent = 100
		}
		if percent == 100 {
			selected.Status = "completed"
			selected.UserFraction = 0
			selected.CountryTargeting = nil
		} else {
			selected.Status = "inProgress"
			selected.UserFraction = percent / 100
		}
		if e = resolveActive(target, selected, r.ReplaceExisting); e != nil {
			return out, e
		}
	case "set", "halt", "resume":
		selected := find(target, version)
		if selected == nil {
			return out, fault.Conflict("Version code %d not found in track", version)
		}
		switch r.Action {
		case "set":
			if selected.Status != "inProgress" {
				return out, fault.Conflict("Only an inProgress rollout can be increased")
			}
			if r.Rollout <= selected.UserFraction*100 {
				return out, fault.Input("New rollout must exceed the current percentage")
			}
			if r.Rollout == 100 {
				selected.Status = "completed"
				selected.UserFraction = 0
				selected.CountryTargeting = nil
			} else {
				selected.UserFraction = r.Rollout / 100
			}
			if e = resolveActive(target, selected, r.ReplaceExisting); e != nil {
				return out, e
			}
		case "halt":
			if selected.Status != "inProgress" {
				return out, fault.Conflict("Only an inProgress rollout can be halted")
			}
			selected.Status = "halted"
		case "resume":
			if selected.Status != "halted" || selected.UserFraction <= 0 || selected.UserFraction >= 1 {
				return out, fault.Conflict("Only a halted staged rollout can be resumed")
			}
			selected.Status = "inProgress"
			if e = resolveActive(target, selected, r.ReplaceExisting); e != nil {
				return out, e
			}
		}
	default:
		return out, fault.Input("Unknown release action")
	}
	selected := find(target, version)
	if e = s.API.Update(ctx, r.App, id, target); e != nil {
		return out, editError(e, "update track")
	}
	if e = s.API.Validate(ctx, r.App, id); e != nil {
		return out, editError(e, "validate")
	}
	commit := func() error {
		if r.Review.InReview == "cancel" || r.Review.ChangesNotSentForReview {
			api, ok := s.API.(interface {
				CommitWithOptions(context.Context, string, string, CommitOptions) error
			})
			if !ok {
				return fault.Input("API does not support explicit review options")
			}
			return api.CommitWithOptions(ctx, r.App, id, r.Review)
		}
		return s.API.Commit(ctx, r.App, id)
	}
	if e = commit(); e != nil {
		if ambiguous(e) {
			unknown = true
			fe := fault.New("COMMIT_OUTCOME_UNKNOWN", "Commit outcome is unknown. Check releases list before taking any further write action; do not replay automatically", 4)
			fe.Details = map[string]any{"app": r.App, "track": r.Track, "versionCode": version, "editId": id}
			return out, fe
		}
		return out, editError(e, "commit")
	}
	committed = true
	percent := selected.UserFraction * 100
	if selected.Status == "completed" {
		percent = 100
	}
	return Outcome{App: r.App, Track: r.Track, VersionCode: version, VersionCodes: slices.Clone(selected.VersionCodes), Status: selected.Status, Percent: percent, Committed: true, ReviewStatus: "unknown"}, nil
}

func find(t *pub.Track, version int64) *pub.TrackRelease {
	for _, r := range t.Releases {
		if slices.Contains(r.VersionCodes, version) {
			return r
		}
	}
	return nil
}

// Completing a release supersedes older active releases. Explicit consent is
// required so artifacts supporting other devices are never silently dropped.
func resolveActive(t *pub.Track, selected *pub.TrackRelease, replace bool) error {
	kept := make([]*pub.TrackRelease, 0, len(t.Releases))
	for _, r := range t.Releases {
		conflict := r != selected && ((selected.Status == "completed" && r.Status != "draft") || (selected.Status == "inProgress" && (r.Status == "inProgress" || r.Status == "halted")))
		if conflict && !replace {
			return fault.Conflict("Target has another active release (%s). Review a snapshot/plan and set replaceExisting in the manifest to supersede it", codes(r.VersionCodes))
		}
		if !conflict {
			kept = append(kept, r)
		}
	}
	t.Releases = kept
	return nil
}
func codes(v []int64) string {
	a := make([]string, len(v))
	for i, x := range v {
		a[i] = strconv.FormatInt(x, 10)
	}
	return strings.Join(a, ",")
}
func ambiguous(err error) bool {
	var api *gapi.Error
	if errors.As(err, &api) {
		return api.Code >= 500 || api.Code == 408
	}
	// Even a malformed successful response may follow a committed edit.
	return fault.From(err).Exit != 3
}
func editError(err error, stage string) error {
	var api *gapi.Error
	if errors.As(err, &api) && api.Code == 404 {
		return fault.Conflict("Edit expired or app/track is unavailable during %s; verify configuration and fetch fresh state", stage)
	}
	e := fault.From(err)
	// An agent must reconsider all state-changing failures instead of replaying.
	e.Retryable = false
	if e.Code == "INTERNAL_ERROR" {
		e.Message = fmt.Sprintf("Release operation failed during %s", stage)
	}
	return e
}
