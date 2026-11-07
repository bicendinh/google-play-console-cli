package cli

import (
	"context"

	"github.com/spf13/cobra"
	"playops/internal/fault"
	"playops/internal/model"
	"playops/internal/publisher"
	"playops/internal/releaseflow"
)

func (a *App) listingImagesList() *cobra.Command {
	var locale, kind, edit string
	var confirm bool
	c := &cobra.Command{Use: "list", Short: "List ordered image hashes and preview URLs through a temporary or caller-owned edit", Args: exact(0)}
	c.Flags().StringVar(&locale, "locale", "", "Listing locale")
	c.Flags().StringVar(&kind, "type", "", "Image type, such as phoneScreenshots")
	c.Flags().StringVar(&edit, "edit-id", "", "Caller-owned edit; no edit is created or deleted")
	c.Flags().BoolVar(&confirm, "confirm", false, "Consent to a temporary edit when --edit-id is not supplied")
	required(c, "locale", "type")
	c.RunE = a.run(func(ctx context.Context, _ *cobra.Command, _ []string) (model.Result, error) {
		if err := releaseflow.ValidLocale(locale); err != nil {
			return model.Result{}, err
		}
		if !releaseflow.ImageType(kind) {
			return model.Result{}, fault.Input("Unsupported image type: %s", kind)
		}
		if edit == "" && !confirm {
			return model.Result{}, fault.Input("images list requires --confirm to open a temporary edit, or an existing --edit-id")
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
		images := []map[string]any{}
		read := func(id string) error {
			listings, e := client.Listings(ctx, p.App, id)
			if e != nil {
				return e
			}
			found := false
			for _, l := range listings {
				if l.Language == locale {
					found = true
				}
			}
			if !found {
				return fault.New("LISTING_NOT_FOUND", "Locale has no explicit listing in this edit; inheritance is unknown", 2)
			}
			rows, e := client.Images(ctx, p.App, id, locale, kind)
			if e != nil {
				return e
			}
			for i, im := range rows {
				images = append(images, map[string]any{"position": i + 1, "id": im.Id, "sha256": im.Sha256, "url": im.Url})
			}
			return nil
		}
		if edit != "" {
			err = client.CheckEdit(ctx, p.App, edit)
			if err == nil {
				err = read(edit)
			}
		} else {
			_, err = publisher.Transaction(ctx, client, p.App, nil, read)
		}
		return model.Result{Data: map[string]any{"locale": locale, "type": kind, "images": images, "inheritance": "unknown", "usesDefaultImages": nil}, Meta: map[string]any{"source": "android-publisher", "editOpened": edit == "", "committed": false, "notice": "API image lists do not identify inheritance; empty images do not prove default-locale fallback. Preview URLs are not proof of public Store visibility."}}, err
	})
	return c
}
