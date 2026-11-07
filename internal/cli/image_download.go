package cli

import (
	"context"

	"github.com/spf13/cobra"
	pub "google.golang.org/api/androidpublisher/v3"
	"playops/internal/fault"
	"playops/internal/model"
	"playops/internal/publisher"
	"playops/internal/releaseflow"
)

func (a *App) listingImagesDownload() *cobra.Command {
	var snapshot, locale, kind, out, edit string
	var confirm bool
	c := &cobra.Command{Use: "download", Short: "Recover ordered original image bytes only when SHA-256 matches a saved snapshot", Args: exact(0)}
	c.Flags().StringVar(&snapshot, "snapshot", "", "Complete saved snapshot; must contain this locale/type")
	c.Flags().StringVar(&locale, "locale", "", "Listing locale")
	c.Flags().StringVar(&kind, "type", "", "Image type")
	c.Flags().StringVar(&out, "out", "", "New output directory, for example originals/en-US; parent must exist")
	c.Flags().StringVar(&edit, "edit-id", "", "Caller-owned edit; never created, committed or discarded")
	c.Flags().BoolVar(&confirm, "confirm", false, "Consent to opening/discarding a temporary edit to obtain current image URLs")
	required(c, "snapshot", "locale", "type", "out")
	c.RunE = a.run(func(ctx context.Context, _ *cobra.Command, _ []string) (model.Result, error) {
		if err := releaseflow.ValidLocale(locale); err != nil {
			return model.Result{}, err
		}
		if !releaseflow.ImageType(kind) {
			return model.Result{}, fault.Input("Unsupported image type")
		}
		if edit == "" && !confirm {
			return model.Result{}, fault.Input("images download requires --confirm for a temporary edit or an existing --edit-id")
		}
		if err := releaseflow.CheckDownloadOutput(out); err != nil {
			return model.Result{}, err
		}
		s, err := releaseflow.LoadSnapshot(snapshot)
		if err != nil {
			return model.Result{}, err
		}
		if s.Profile != a.profile || (a.app != "" && a.app != s.App) {
			return model.Result{}, fault.Input("Select the snapshot profile and app explicitly")
		}
		expected, ok := s.Images[locale+"/"+kind]
		if !ok || len(expected) == 0 {
			return model.Result{}, fault.Input("Snapshot must contain nonempty images for the selected locale/type; inheritance is unknown")
		}
		client, _, err := a.client(ctx, false)
		if err != nil {
			return model.Result{}, err
		}
		unlock, err := a.lock(s.App)
		if err != nil {
			return model.Result{}, err
		}
		defer unlock()
		var rows []*pub.Image
		read := func(id string) error { var e error; rows, e = client.Images(ctx, s.App, id, locale, kind); return e }
		receipt := publisher.EditReceipt{EditID: edit}
		if edit != "" {
			err = client.CheckEdit(ctx, s.App, edit)
			if err == nil {
				err = read(edit)
			}
		} else {
			receipt, err = publisher.Inspect(ctx, client, s.App, read)
		}
		if err != nil {
			return model.Result{}, err
		}
		// Cleanup completes before downloading. Asset requests use an anonymous
		// client, never the authenticated Publisher transport.
		files, err := releaseflow.DownloadImages(ctx, expected, rows, out, nil)
		if err != nil {
			f := fault.From(err)
			f.Details = map[string]any{"inspection": receipt, "cause": f.Details}
			return model.Result{}, f
		}
		return model.Result{Data: map[string]any{"locale": locale, "type": kind, "files": files, "originalBytesVerified": true}, Meta: map[string]any{"source": "google-play-image-preview", "editOpened": receipt.EditOpened, "editId": receipt.EditID, "discarded": receipt.Discarded, "committed": false, "notice": "All downloaded bytes match snapshot SHA-256. Preview availability is not guaranteed; no URL variants or image conversions are attempted."}}, nil
	})
	return c
}
