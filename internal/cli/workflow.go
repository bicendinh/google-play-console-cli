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

const lifecycleNotice = "Lifecycle describes the returned release, not all pending Console/listing changes. PUBLISHED means available to users on this track; a successful commit alone does not prove publication. Up to 20 non-obsolete releases are returned; absence is inconclusive. Rollout fraction is unavailable here."

func reviewState(rows []*pub.ReleaseSummary) string {
	state := ""
	for _, r := range rows {
		value := "unknown"
		switch strings.TrimPrefix(r.ReleaseLifecycleState, "RELEASE_LIFECYCLE_STATE_") {
		case "DRAFT":
			value = "draft"
		case "NOT_SENT_FOR_REVIEW":
			value = "not_sent_for_review"
		case "IN_REVIEW":
			value = "in_review"
		case "APPROVED_NOT_PUBLISHED", "PUBLISHED":
			value = "approved"
		case "NOT_APPROVED":
			value = "rejected"
		}
		if state != "" && state != value {
			return "unknown"
		}
		state = value
	}
	if state == "" {
		return "unknown"
	}
	return state
}

func (a *App) releaseInit() *cobra.Command {
	var dir, sources, track, out, status string
	var rollout float64
	c := &cobra.Command{Use: "init", Short: "Create a release manifest from a local AAB, notes and numbered screenshots", Args: exact(0)}
	c.Flags().StringVar(&dir, "dir", "", "Release directory: app-release-*.aab, notes/<locale>.txt, shots/<locale>/NN-name.png")
	c.Flags().StringVar(&sources, "source-dir", "", "Baseline image directory <locale>/NN-name.png; required for partial replacements")
	c.Flags().StringVar(&track, "track", "", "Target track (required)")
	c.Flags().StringVar(&out, "out", "", "New YAML manifest path")
	c.Flags().StringVar(&status, "status", "", "draft by default; inferred from an explicitly supplied --rollout")
	c.Flags().Float64Var(&rollout, "rollout", 0, "Explicit rollout percentage 1–100; 100 selects completed")
	required(c, "dir", "track", "out")
	c.RunE = a.run(func(_ context.Context, c *cobra.Command, _ []string) (model.Result, error) {
		if c.Flags().Changed("rollout") && (rollout < 1 || rollout > 100) {
			return model.Result{}, fault.Input("--rollout must be explicitly between 1 and 100")
		}
		if err := releaseflow.CheckOutput(out); err != nil {
			return model.Result{}, err
		}
		if status == "" {
			status = "draft"
			if c.Flags().Changed("rollout") {
				if rollout == 100 {
					status = "completed"
				} else {
					status = "inProgress"
				}
			}
		}
		m, metadata, err := releaseflow.InitRelease(dir, sources, track, a.profile, status, rollout)
		if err != nil {
			return model.Result{}, err
		}
		if err = a.workflowIdentity(m); err != nil {
			return model.Result{}, err
		}
		if err = releaseflow.WriteManifest(out, m); err != nil {
			return model.Result{}, err
		}
		return model.Result{Data: map[string]any{"manifest": m, "bundleMetadata": metadata}, Meta: map[string]any{"source": "local", "out": out, "serverValidated": false, "next": "Review the manifest, run preflight, capture a snapshot, then generate a plan"}}, nil
	})
	return c
}

func (a *App) releaseVerify() *cobra.Command {
	var path, edit string
	var confirm, noEdit, uncertain bool
	c := &cobra.Command{Use: "verify", Short: "Compare current release, notes and ordered image hashes with a reviewed plan; never commits", Args: exact(0)}
	c.Flags().StringVar(&path, "plan", "", "Reviewed plan JSON; original assets are not needed for remote verification")
	c.Flags().StringVar(&edit, "edit-id", "", "Inspect this caller-owned edit without creating, deleting or committing it")
	c.Flags().BoolVar(&confirm, "confirm", false, "Consent to opening/discarding a temporary edit; can invalidate another edit")
	c.Flags().BoolVar(&noEdit, "no-edit", false, "Check lifecycle evidence only; cannot prove full plan agreement")
	c.Flags().BoolVar(&uncertain, "commit-outcome-unknown", false, "Protect a possibly open edit after a commit timeout; requires --no-edit or --edit-id")
	required(c, "plan")
	c.RunE = a.run(func(ctx context.Context, _ *cobra.Command, _ []string) (model.Result, error) {
		if noEdit && edit != "" {
			return model.Result{}, fault.Input("Use --no-edit or --edit-id, not both")
		}
		if uncertain && !noEdit && edit == "" {
			return model.Result{}, fault.Input("After an uncertain commit use --no-edit first, or inspect the original --edit-id; opening a new edit could invalidate it")
		}
		if !noEdit && edit == "" && !confirm {
			return model.Result{}, fault.Input("Full verify opens a temporary edit and requires --confirm; use --no-edit for lifecycle only")
		}
		p, err := releaseflow.LoadPlan(path)
		if err != nil {
			return model.Result{}, err
		}
		if err = a.workflowIdentity(p.Manifest); err != nil {
			return model.Result{}, err
		}
		client, _, err := a.client(ctx, false)
		if err != nil {
			return model.Result{}, err
		}
		meta := map[string]any{"source": "android-publisher", "app": p.Manifest.App, "track": p.Manifest.Track, "publicationVerified": false, "notice": "Match means stored state agrees with the checked plan fields; it does not prove review approval or Store visibility. Symbols cannot be read back."}
		if noEdit {
			rows, e := client.Releases(ctx, p.Manifest.App, p.Manifest.Track)
			if e != nil {
				return model.Result{}, e
			}
			matches := []*pub.ReleaseSummary{}
			for _, r := range rows {
				if slices.ContainsFunc(r.ActiveArtifacts, func(v *pub.ArtifactSummary) bool { return v.VersionCode == p.Manifest.VersionCode }) {
					matches = append(matches, r)
				}
			}
			v := releaseflow.Verification{Status: "unknown", Scope: "release-lifecycle", PlanDigest: p.Digest, CheckedAt: a.Now().UTC(), Differences: []releaseflow.Diff{}, MissingEvidence: []string{"Track configuration, notes, listings and images are unavailable without an edit"}}
			for _, r := range matches {
				if r.ReleaseLifecycleState == "RELEASE_LIFECYCLE_STATE_IN_REVIEW" || r.ReleaseLifecycleState == "RELEASE_LIFECYCLE_STATE_APPROVED_NOT_PUBLISHED" {
					v.Status = "pending"
				}
			}
			meta["editOpened"] = false
			meta["notice"] = lifecycleNotice
			return model.Result{Data: map[string]any{"verification": v, "releases": matches, "reviewState": reviewState(matches), "globalReviewState": "unknown"}, Meta: meta}, nil
		}
		unlock, err := a.lock(p.Manifest.App)
		if err != nil {
			return model.Result{}, err
		}
		defer unlock()
		if edit != "" {
			if err = client.CheckEdit(ctx, p.Manifest.App, edit); err != nil {
				return model.Result{}, err
			}
		}
		v, err := (releaseflow.Service{API: client, Now: a.Now}).Verify(ctx, p, edit)
		meta["editOpened"] = edit == ""
		meta["committedByCommand"] = false
		return model.Result{Data: v, Meta: meta}, err
	})
	return c
}
