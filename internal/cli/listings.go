package cli

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"playops/internal/fault"
	"playops/internal/model"
	"playops/internal/publisher"
	"playops/internal/releaseflow"
)

func editFailure(err error, edit string) error {
	if err == nil {
		return nil
	}
	e := fault.From(err)
	e.Retryable = false
	e.Details = map[string]any{"editId": edit, "committed": false, "transactionOwner": "caller", "requiredAction": "Abort this edit after any failed mutation; helpers never commit or delete it"}
	return e
}
func (a *App) listings() *cobra.Command {
	group := &cobra.Command{Use: "listings", Short: "Read listing text or modify a caller-owned edit"}
	var locale, edit string
	var confirm bool
	get := &cobra.Command{Use: "get", Short: "Read localized listing text; without --edit-id opens and discards a temporary edit", Args: exact(0)}
	get.Flags().StringVar(&locale, "locale", "", "Listing locale")
	get.Flags().StringVar(&edit, "edit-id", "", "Existing caller-owned edit; no temporary edit is opened")
	get.Flags().BoolVar(&confirm, "confirm", false, "Consent to opening a temporary edit when no --edit-id is supplied")
	required(get, "locale")
	get.RunE = a.run(func(ctx context.Context, _ *cobra.Command, _ []string) (model.Result, error) {
		if err := releaseflow.ValidLocale(locale); err != nil {
			return model.Result{}, err
		}
		if edit == "" && !confirm {
			return model.Result{}, fault.Input("listings get without --edit-id opens a temporary edit and requires --confirm")
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
		var value any
		read := func(id string) error {
			rows, e := client.Listings(ctx, p.App, id)
			if e != nil {
				return e
			}
			for _, l := range rows {
				if l.Language == locale {
					value = l
					return nil
				}
			}
			return fault.New("LISTING_NOT_FOUND", "Locale has no explicit listing in this edit", 2)
		}
		if edit != "" {
			err = client.CheckEdit(ctx, p.App, edit)
			if err == nil {
				err = read(edit)
			}
		} else {
			_, err = publisher.Transaction(ctx, client, p.App, nil, read)
		}
		return model.Result{Data: value, Meta: map[string]any{"source": "android-publisher", "committed": false}}, err
	})
	var updateLocale, updateEdit, file string
	var updateConfirm bool
	update := &cobra.Command{Use: "update", Short: "Patch listing fields in a caller-owned edit; never commits or deletes that edit", Args: exact(0)}
	update.Flags().StringVar(&updateLocale, "locale", "", "Listing locale")
	update.Flags().StringVar(&updateEdit, "edit-id", "", "Caller-owned edit ID")
	update.Flags().StringVar(&file, "file", "", "JSON/YAML patch with title/shortDescription/fullDescription/video")
	update.Flags().BoolVar(&updateConfirm, "confirm", false, "Authorize the uncommitted mutation; caller must abort the edit on failure")
	required(update, "locale", "edit-id", "file")
	update.RunE = a.run(func(ctx context.Context, _ *cobra.Command, _ []string) (model.Result, error) {
		if !updateConfirm {
			return model.Result{}, fault.Input("listings update requires --confirm")
		}
		var patch releaseflow.ListingChange
		if err := releaseflow.Read(file, &patch); err != nil {
			return model.Result{}, err
		}
		if patch.File != "" || (patch.Locale != "" && patch.Locale != updateLocale) {
			return model.Result{}, fault.Input("Patch locale must match; file references are not accepted here")
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
		if err = client.CheckEdit(ctx, p.App, updateEdit); err != nil {
			return model.Result{}, editFailure(err, updateEdit)
		}
		diff, err := (releaseflow.Service{API: client}).UpdateListing(ctx, p.App, updateEdit, updateLocale, patch)
		return model.Result{Data: diff, Meta: map[string]any{"source": "android-publisher", "editId": updateEdit, "committed": false, "transactionOwner": "caller"}}, editFailure(err, updateEdit)
	})
	group.AddCommand(get, update, a.listingImages())
	return group
}
func (a *App) listingImages() *cobra.Command {
	group := &cobra.Command{Use: "images", Short: "Plan ordered images offline and sync into a caller-owned edit"}
	var manifest, snapshot, out, locale, kind, alpha string
	var replace, sources []string
	var dry bool
	plan := &cobra.Command{Use: "plan", Short: "Create an image-only plan with a complete verified source set", Args: exact(0)}
	f := plan.Flags()
	f.StringVar(&manifest, "manifest", "", "Image-only release manifest (alternative to --locale/--type/--replace)")
	f.StringVar(&snapshot, "snapshot", "", "Saved snapshot")
	f.StringVar(&out, "out", "", "New plan JSON path")
	f.StringVar(&locale, "locale", "", "Listing locale")
	f.StringVar(&kind, "type", "phoneScreenshots", "Image type")
	f.StringArrayVar(&replace, "replace", nil, "One-based POSITION=FILE; repeat for multiple replacements")
	f.StringSliceVar(&sources, "source-files", nil, "Complete baseline image files in snapshot order (comma-separated)")
	f.BoolVar(&dry, "dry-run", true, "Offline only; never opens an edit")
	f.StringVar(&alpha, "flatten-alpha", "", "PNG policy: auto (default), never or error; stored in the plan")
	required(plan, "snapshot", "out")
	plan.RunE = a.run(func(ctx context.Context, _ *cobra.Command, _ []string) (model.Result, error) {
		if !dry {
			return model.Result{}, fault.Input("images plan is always offline")
		}
		var p releaseflow.Plan
		var err error
		if manifest != "" {
			if locale != "" || len(replace) > 0 || len(sources) > 0 {
				return model.Result{}, fault.Input("Use a manifest or positional replacement flags, not both")
			}
			p, err = releaseflow.BuildPlanWithAlpha(ctx, manifest, snapshot, a.profile, alpha)
		} else {
			replacements := map[int]string{}
			for _, value := range replace {
				parts := strings.SplitN(value, "=", 2)
				if len(parts) != 2 {
					return model.Result{}, fault.Input("--replace must be POSITION=FILE")
				}
				position, e := strconv.Atoi(parts[0])
				if e != nil || position < 1 || replacements[position] != "" {
					return model.Result{}, fault.Input("Replacement positions must be unique positive integers")
				}
				path, e := filepath.Abs(parts[1])
				if e != nil {
					return model.Result{}, e
				}
				replacements[position] = path
			}
			for i := range sources {
				sources[i], err = filepath.Abs(sources[i])
				if err != nil {
					return model.Result{}, err
				}
			}
			p, err = releaseflow.BuildImagePlanWithAlpha(ctx, snapshot, locale, kind, sources, replacements, alpha)
		}
		if err != nil {
			return model.Result{}, err
		}
		if err = a.workflowIdentity(p.Manifest); err != nil {
			return model.Result{}, err
		}
		if p.Manifest.ChangesTrack() || len(p.Manifest.Listings) > 0 || len(p.Manifest.Symbols) > 0 {
			return model.Result{}, fault.Input("images plan requires an image-only manifest")
		}
		if err = releaseflow.Write(out, p); err != nil {
			return model.Result{}, err
		}
		return model.Result{Data: p, Meta: map[string]any{"source": "local-preview", "serverValidated": false, "dryRun": true, "out": out}}, nil
	})
	var path, edit string
	var confirm bool
	sync := &cobra.Command{Use: "sync", Short: "Sync reviewed images inside a caller-owned edit; no commit or delete", Args: exact(0)}
	sync.Flags().StringVar(&path, "plan", "", "Reviewed image-only JSON plan")
	sync.Flags().StringVar(&edit, "edit-id", "", "Caller-owned edit ID")
	sync.Flags().BoolVar(&confirm, "confirm", false, "Authorize image mutation; caller must abort the edit on failure")
	required(sync, "plan", "edit-id")
	sync.RunE = a.run(func(ctx context.Context, _ *cobra.Command, _ []string) (model.Result, error) {
		if !confirm {
			return model.Result{}, fault.Input("images sync requires --confirm")
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
		unlock, err := a.lock(p.Manifest.App)
		if err != nil {
			return model.Result{}, err
		}
		defer unlock()
		if err = client.CheckEdit(ctx, p.Manifest.App, edit); err == nil {
			err = (releaseflow.Service{API: client}).SyncOnly(ctx, p, edit)
		}
		return model.Result{Data: map[string]any{"editId": edit, "committed": false, "planDigest": p.Digest, "transactionOwner": "caller"}}, editFailure(err, edit)
	})
	group.AddCommand(plan, sync, a.listingImagesList())
	return group
}
