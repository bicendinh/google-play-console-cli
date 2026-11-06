package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/gofrs/flock"
	"github.com/spf13/cobra"
	"golang.org/x/text/language"
	pub "google.golang.org/api/androidpublisher/v3"

	"playops/internal/config"
	"playops/internal/fault"
	"playops/internal/model"
	"playops/internal/publisher"
)

func (a *App) lock(app string) (func(), error) {
	path, err := config.Path(a.configPath)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(filepath.Dir(path), "locks")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	hash := sha256.Sum256([]byte(app))
	f := flock.New(filepath.Join(dir, hex.EncodeToString(hash[:])+".lock"))
	ok, err := f.TryLock()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fault.Conflict("Another local edit operation is running for this app")
	}
	return func() { _ = f.Unlock() }, nil
}
func (a *App) releases() *cobra.Command {
	group := &cobra.Command{Use: "releases", Short: "Read, upload, publish and promote releases"}
	var track string
	list := &cobra.Command{Use: "list", Short: "Read release lifecycle states without opening an edit", Args: exact(0), RunE: a.run(func(ctx context.Context, _ *cobra.Command, _ []string) (model.Result, error) {
		if track == "" || strings.ContainsAny(track, "/\\ \t\n\r") {
			return model.Result{}, fault.Input("--track must be a track identifier")
		}
		client, p, err := a.client(ctx, true)
		if err != nil {
			return model.Result{}, err
		}
		rows, err := client.Releases(ctx, p.App, track)
		return model.Result{Data: rows, Meta: map[string]any{"source": "android-publisher", "app": p.App, "track": track, "notice": "Lifecycle and rollout state are distinct. Google returns at most 20 non-obsolete releases."}}, err
	})}
	list.Flags().StringVar(&track, "track", "", "Track identifier (required)")
	_ = list.MarkFlagRequired("track")
	group.AddCommand(list, a.releaseWrite("upload"), a.releaseWrite("publish"), a.releaseWrite("promote"), a.releaseSnapshot(), a.releasePlan(), a.releaseApply(), a.releasePreflight(), a.releaseStatus(), a.symbols())
	return group
}
func (a *App) rollout() *cobra.Command {
	group := &cobra.Command{Use: "rollout", Short: "Inspect and manage staged releases"}
	var track string
	var confirm bool
	get := &cobra.Command{Use: "get", Short: "Read rollout configuration through a temporary, discarded edit", Args: exact(0), RunE: a.run(func(ctx context.Context, _ *cobra.Command, _ []string) (model.Result, error) {
		if track == "" || strings.ContainsAny(track, "/\\ \t\n\r") {
			return model.Result{}, fault.Input("--track must be a track identifier")
		}
		if !confirm {
			return model.Result{}, fault.Input("rollout get requires --confirm because opening its temporary edit can invalidate another edit by the same identity; releases list is edit-free")
		}
		client, p, err := a.client(ctx, true)
		if err != nil {
			return model.Result{}, err
		}
		unlock, err := a.lock(p.App)
		if err != nil {
			return model.Result{}, err
		}
		defer unlock()
		rows, err := (publisher.Service{API: client}).ReadTrack(ctx, p.App, track)
		return model.Result{Data: rows, Meta: map[string]any{"source": "android-publisher", "app": p.App, "track": track, "notice": "Opened and discarded a temporary edit. An existing edit by the same identity can be invalidated; no release is committed."}}, err
	})}
	get.Flags().StringVar(&track, "track", "", "Track identifier (required)")
	get.Flags().BoolVar(&confirm, "confirm", false, "Consent to opening and discarding a temporary edit")
	_ = get.MarkFlagRequired("track")
	group.AddCommand(get, a.releaseWrite("set"), a.releaseWrite("halt"), a.releaseWrite("resume"))
	return group
}
func (a *App) releaseWrite(action string) *cobra.Command {
	r := publisher.Request{Action: action}
	var file, notesFile string
	var confirm, dry bool
	descriptions := map[string]string{"upload": "Upload a signed AAB and commit a draft", "publish": "Publish a draft by version code", "promote": "Copy a release to another track", "set": "Increase an active staged rollout", "halt": "Halt an active staged rollout", "resume": "Resume a halted staged rollout"}
	c := &cobra.Command{Use: action, Short: descriptions[action], Args: exact(0)}
	f := c.Flags()
	f.BoolVar(&confirm, "confirm", false, "Explicitly authorize a Google Play write")
	f.BoolVar(&dry, "dry-run", false, "Preview local inputs only; no Google requests or server validation")
	f.StringVar(&r.Review.InReview, "in-review", "error", "Behavior when changes are in review: error or explicit cancel")
	f.BoolVar(&r.Review.ChangesNotSentForReview, "changes-not-sent-for-review", false, "Request Google's rejection-specific not-sent-for-review behavior; not a general draft switch")
	if action == "promote" {
		f.StringVar(&r.From, "from", "", "Source track (required)")
		f.StringVar(&r.Track, "to", "", "Target track (required)")
	} else {
		f.StringVar(&r.Track, "track", "", "Track identifier (required)")
	}
	if action == "upload" {
		f.StringVar(&file, "file", "", "Signed .aab file (required)")
		f.StringVar(&r.Name, "name", "", "Release name")
		f.StringVar(&notesFile, "notes-file", "", "JSON array of {language,text} release notes")
	} else {
		f.Int64Var(&r.VersionCode, "version-code", 0, "Version code identifying the release (required)")
	}
	if action == "publish" || action == "promote" || action == "set" {
		f.Float64Var(&r.Rollout, "rollout", 0, "Rollout percentage 1–100 (required for production; 100 completes release)")
		f.BoolVar(&r.ReplaceExisting, "replace-existing", false, "Explicitly supersede conflicting active releases; inspect rollout get first")
	}
	if action == "resume" {
		f.BoolVar(&r.ReplaceExisting, "replace-existing", false, "Explicitly supersede another staged release")
	}
	if action == "promote" {
		_ = c.MarkFlagRequired("from")
		_ = c.MarkFlagRequired("to")
	} else {
		_ = c.MarkFlagRequired("track")
	}
	if action == "upload" {
		_ = c.MarkFlagRequired("file")
	} else {
		_ = c.MarkFlagRequired("version-code")
	}
	c.RunE = a.run(func(ctx context.Context, _ *cobra.Command, _ []string) (model.Result, error) {
		_, _, p, err := a.settings()
		if err != nil {
			return model.Result{}, err
		}
		r.App = p.App
		if err = config.ValidateApp(r.App); err != nil {
			return model.Result{}, err
		}
		if err = publisher.Check(r); err != nil {
			return model.Result{}, err
		}
		if !dry && !confirm {
			return model.Result{}, fault.Input("Google Play writes require --confirm; use --dry-run to preview local inputs")
		}
		if r.ReplaceExisting {
			return model.Result{}, fault.Input("Use releases snapshot/plan/apply for replacement: the exact artifact diff and remote baseline must be reviewed before removing active releases")
		}
		if action == "upload" {
			if err = publisher.ValidateBundle(file); err != nil {
				return model.Result{}, err
			}
			if notesFile != "" {
				r.Notes, err = readNotes(notesFile)
				if err != nil {
					return model.Result{}, err
				}
			}
		}
		if dry {
			return model.Result{Data: map[string]any{"request": r, "file": file, "dryRun": true, "serverValidated": false}, Meta: map[string]any{"source": "local-preview", "notice": "No Google API calls were made. Current track state, permissions and signing are not server-validated."}}, nil
		}
		client, _, err := a.client(ctx, true)
		if err != nil {
			return model.Result{}, err
		}
		unlock, err := a.lock(p.App)
		if err != nil {
			return model.Result{}, err
		}
		defer unlock()
		var reader io.Reader
		if action == "upload" {
			b, e := os.Open(file)
			if e != nil {
				return model.Result{}, fault.Input("Cannot open AAB")
			}
			defer b.Close()
			reader = b
		}
		out, err := (publisher.Service{API: client}).Execute(ctx, r, reader)
		return model.Result{Data: out, Meta: map[string]any{"source": "android-publisher", "notice": "Commit success does not mean the app is published. Query releases list for review status."}}, err
	})
	return c
}
func readNotes(path string) ([]*pub.LocalizedText, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fault.Input("Cannot read release notes file")
	}
	var notes []*pub.LocalizedText
	if json.Unmarshal(b, &notes) != nil || len(notes) == 0 {
		return nil, fault.Input("Release notes must be a non-empty JSON array of {language,text}")
	}
	seen := map[string]bool{}
	for _, n := range notes {
		if n == nil {
			return nil, fault.Input("Release notes cannot contain null")
		}
		tag, e := language.Parse(n.Language)
		if e != nil || n.Language == "" || tag == language.Und {
			return nil, fault.Input("Invalid release note language")
		}
		key := tag.String()
		if seen[key] || strings.TrimSpace(n.Text) == "" || utf8.RuneCountInString(n.Text) > 500 {
			return nil, fault.Input("Each language must be unique with 1–500 characters of release notes")
		}
		seen[key] = true
	}
	return notes, nil
}
