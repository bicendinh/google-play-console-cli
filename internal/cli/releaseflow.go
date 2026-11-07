package cli

import (
	"context"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	pub "google.golang.org/api/androidpublisher/v3"
	"playops/internal/fault"
	"playops/internal/model"
	"playops/internal/releaseflow"
)

func (a *App) workflowIdentity(m releaseflow.Manifest) error {
	if m.Profile != a.profile {
		return fault.Input("Select manifest profile explicitly with --profile %s", m.Profile)
	}
	if a.app != "" && a.app != m.App {
		return fault.Input("--app differs from the reviewed manifest app")
	}
	return nil
}
func required(c *cobra.Command, names ...string) {
	for _, name := range names {
		_ = c.MarkFlagRequired(name)
	}
}
func (a *App) releaseSnapshot() *cobra.Command {
	var path, out string
	var confirm, noEdit bool
	c := &cobra.Command{Use: "snapshot", Short: "Capture track/listing/image state through one discarded edit", Args: exact(0)}
	c.Flags().StringVar(&path, "manifest", "", "Release YAML/JSON manifest")
	c.Flags().StringVar(&out, "out", "", "New snapshot JSON path")
	c.Flags().BoolVar(&confirm, "confirm", false, "Consent to a temporary edit; this can invalidate an existing edit by the same identity")
	c.Flags().BoolVar(&noEdit, "no-edit", false, "Save an incomplete lifecycle-only snapshot; cannot be used for plan")
	required(c, "manifest", "out")
	c.RunE = a.run(func(ctx context.Context, _ *cobra.Command, _ []string) (model.Result, error) {
		if !confirm && !noEdit {
			return model.Result{}, fault.Input("snapshot opens a temporary edit and requires --confirm")
		}
		if err := releaseflow.CheckOutput(out); err != nil {
			return model.Result{}, err
		}
		m, err := releaseflow.LoadManifest(path, a.profile)
		if err != nil {
			return model.Result{}, err
		}
		if err = a.workflowIdentity(m); err != nil {
			return model.Result{}, err
		}
		client, _, err := a.client(ctx, false)
		if err != nil {
			return model.Result{}, err
		}
		if noEdit {
			rows, e := client.Releases(ctx, m.App, m.Track)
			if e != nil {
				return model.Result{}, e
			}
			complete := false
			snapshot := releaseflow.Snapshot{FormatVersion: 1, App: m.App, Profile: m.Profile, CapturedAt: a.Now().UTC(), Complete: &complete, Lifecycle: rows, MissingFields: []string{"track configuration", "rollout fraction", "release notes", "listings", "images"}}
			if e = releaseflow.Write(out, snapshot); e != nil {
				return model.Result{}, e
			}
			return model.Result{Data: snapshot, Meta: map[string]any{"source": "android-publisher", "out": out, "editOpened": false, "usableForPlan": false, "notice": lifecycleNotice}}, nil
		}
		unlock, err := a.lock(m.App)
		if err != nil {
			return model.Result{}, err
		}
		defer unlock()
		snapshot, receipt, err := (releaseflow.Service{API: client, Now: a.Now}).CaptureWithReceipt(ctx, m)
		if err != nil {
			return model.Result{}, err
		}
		if err = releaseflow.Write(out, snapshot); err != nil {
			return model.Result{}, err
		}
		return model.Result{Data: snapshot, Meta: map[string]any{"source": "android-publisher", "out": out, "committed": false, "editOpened": receipt.EditOpened, "discarded": receipt.Discarded, "editId": receipt.EditID}}, nil
	})
	return c
}
func (a *App) releasePlan() *cobra.Command {
	var manifest, snapshot, out, alpha string
	var dry bool
	c := &cobra.Command{Use: "plan", Short: "Create an offline, hashed plan from local files and a saved snapshot", Args: exact(0)}
	c.Flags().StringVar(&manifest, "manifest", "", "Release YAML/JSON manifest")
	c.Flags().StringVar(&snapshot, "snapshot", "", "Previously captured snapshot JSON")
	c.Flags().StringVar(&out, "out", "", "New reviewed plan JSON path")
	c.Flags().BoolVar(&dry, "dry-run", true, "Offline preview only; this command never calls Google")
	c.Flags().StringVar(&alpha, "flatten-alpha", "", "PNG policy: auto (default), never or error; recorded in plan")
	required(c, "manifest", "snapshot", "out")
	c.RunE = a.run(func(ctx context.Context, _ *cobra.Command, _ []string) (model.Result, error) {
		if !dry {
			return model.Result{}, fault.Input("plan is always offline; use apply for Google writes")
		}
		p, err := releaseflow.BuildPlanWithAlpha(ctx, manifest, snapshot, a.profile, alpha)
		if err != nil {
			return model.Result{}, err
		}
		if err = a.workflowIdentity(p.Manifest); err != nil {
			return model.Result{}, err
		}
		if err = releaseflow.Write(out, p); err != nil {
			return model.Result{}, err
		}
		return model.Result{Data: p, Meta: map[string]any{"source": "local-preview", "out": out, "dryRun": true, "serverValidated": false}}, nil
	})
	return c
}
func (a *App) releaseApply() *cobra.Command {
	var path string
	var confirm, dry bool
	c := &cobra.Command{Use: "apply", Short: "Apply an unchanged reviewed plan in one validated, committed edit", Args: exact(0)}
	c.Flags().StringVar(&path, "plan", "", "Reviewed plan JSON")
	c.Flags().BoolVar(&confirm, "confirm", false, "Authorize exactly the changes and review behavior in the plan")
	c.Flags().BoolVar(&dry, "dry-run", false, "Verify local plan inputs without contacting Google")
	required(c, "plan")
	c.RunE = a.run(func(ctx context.Context, _ *cobra.Command, _ []string) (model.Result, error) {
		if !dry && !confirm {
			return model.Result{}, fault.Input("apply requires --confirm or --dry-run")
		}
		p, err := releaseflow.LoadPlan(path)
		if err != nil {
			return model.Result{}, err
		}
		if err = a.workflowIdentity(p.Manifest); err != nil {
			return model.Result{}, err
		}
		if err = releaseflow.VerifyInputs(p); err != nil {
			return model.Result{}, err
		}
		if dry {
			return model.Result{Data: p, Meta: map[string]any{"source": "local-preview", "serverValidated": false, "dryRun": true}}, nil
		}
		if len(p.Diff) == 0 && len(p.Manifest.Symbols) == 0 {
			return model.Result{}, fault.Input("Plan contains no changes; no edit is needed")
		}
		client, _, err := a.client(ctx, false)
		if err != nil {
			return model.Result{}, err
		}
		unlock, err := a.lock(p.Manifest.App)
		if err != nil {
			return model.Result{}, err
		}
		defer unlock()
		out, err := (releaseflow.Service{API: client, Now: a.Now}).Apply(ctx, p)
		return model.Result{Data: out, Meta: map[string]any{"source": "android-publisher", "app": p.Manifest.App, "track": p.Manifest.Track, "notice": "Commit acceptance is not proof of review approval or publication"}}, err
	})
	return c
}
func (a *App) releasePreflight() *cobra.Command {
	var path, snapshot, alpha string
	c := &cobra.Command{Use: "preflight", Short: "Check local AAB metadata, signer, assets and optional snapshot coverage", Args: exact(0)}
	c.Flags().StringVar(&path, "manifest", "", "Release YAML/JSON manifest")
	c.Flags().StringVar(&snapshot, "snapshot", "", "Saved snapshot for full asset coverage and diff checks")
	c.Flags().StringVar(&alpha, "flatten-alpha", "", "PNG policy: auto (default), never or error")
	required(c, "manifest")
	c.RunE = a.run(func(ctx context.Context, _ *cobra.Command, _ []string) (model.Result, error) {
		m, err := releaseflow.ReadManifest(path, a.profile)
		if err != nil {
			return model.Result{}, err
		}
		if err = a.workflowIdentity(m); err != nil {
			return model.Result{}, err
		}
		if alpha != "" {
			m.FlattenAlpha = alpha
		}
		checks, err := releaseflow.Preflight(ctx, m)
		if ctx.Err() != nil {
			return model.Result{}, fault.From(ctx.Err())
		}
		if snapshot == "" {
			status := "not_run"
			if m.Preflight.Required {
				status = "failed"
			}
			checks = append(checks, releaseflow.Check{Name: "snapshotCoverage", Status: status, Message: "No snapshot supplied; current listing/image coverage and source hash equality were not verified"})
			if err != nil || m.Preflight.Required {
				f := fault.New("PREFLIGHT_FAILED", "Preflight failed; inspect details.checks", 2)
				f.Details = map[string]any{"checks": checks}
				return model.Result{}, f
			}
		} else if err != nil {
			return model.Result{}, err
		}
		complete := true
		for _, check := range checks {
			if check.Status != "passed" {
				complete = false
			}
		}
		meta := map[string]any{"source": "local-preflight", "serverValidated": false, "verificationComplete": complete}
		if snapshot != "" {
			p, e := releaseflow.BuildPlanWithAlpha(ctx, path, snapshot, a.profile, alpha)
			if e != nil {
				return model.Result{}, e
			}
			return model.Result{Data: map[string]any{"checks": p.Preflight, "diff": p.Diff, "snapshotCoverage": "passed"}, Meta: meta}, nil
		}
		return model.Result{Data: checks, Meta: meta}, nil
	})
	return c
}
func (a *App) releaseStatus() *cobra.Command {
	var track string
	var version int64
	c := &cobra.Command{Use: "status", Short: "Find a version's release lifecycle without creating an edit", Args: exact(0)}
	c.Flags().StringVar(&track, "track", "", "Track identifier")
	c.Flags().Int64Var(&version, "version-code", 0, "Positive version code to find")
	required(c, "track", "version-code")
	c.RunE = a.run(func(ctx context.Context, _ *cobra.Command, _ []string) (model.Result, error) {
		if version <= 0 || strings.ContainsAny(track, "/\\ \n\r\t") || track == "" {
			return model.Result{}, fault.Input("Provide a track and positive version code")
		}
		client, p, err := a.client(ctx, true)
		if err != nil {
			return model.Result{}, err
		}
		rows, err := client.Releases(ctx, p.App, track)
		if err != nil {
			return model.Result{}, err
		}
		matches := []*pub.ReleaseSummary{}
		for _, r := range rows {
			if slices.ContainsFunc(r.ActiveArtifacts, func(a *pub.ArtifactSummary) bool { return a.VersionCode == version }) {
				matches = append(matches, r)
			}
		}
		return model.Result{Data: map[string]any{"found": len(matches) > 0, "releases": matches, "reviewState": reviewState(matches), "globalReviewState": "unknown"}, Meta: map[string]any{"source": "android-publisher", "app": p.App, "track": track, "notice": lifecycleNotice}}, nil
	})
	return c
}
