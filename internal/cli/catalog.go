package cli

import (
	"context"
	"github.com/spf13/cobra"
	"playops/internal/fault"
	"playops/internal/model"
	"regexp"
)

func (a *App) catalog(kind string) *cobra.Command {
	group := &cobra.Command{Use: kind, Short: "Read Google Play " + kind + " without opening an edit"}
	for _, action := range []string{"list", "get"} {
		var id string
		var offers, legacy bool
		c := &cobra.Command{Use: action, Short: "Read " + kind + " with regional pricing, base plans/purchase options and offers", Args: exact(0)}
		c.Flags().StringVar(&id, "product-id", "", "Product identifier (required for get; optional filter for list)")
		c.Flags().BoolVar(&offers, "include-offers", true, "Read offers in addition to catalog entries; preserves regional prices and eligibility rules")
		if kind == "products" {
			c.Flags().BoolVar(&legacy, "legacy", false, "Read the older inappproducts API instead of one-time products; no offers endpoint")
		}
		if action == "get" {
			required(c, "product-id")
		}
		c.RunE = a.run(func(ctx context.Context, _ *cobra.Command, _ []string) (model.Result, error) {
			if id != "" && !regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,254}$`).MatchString(id) {
				return model.Result{}, fault.Input("Invalid product identifier")
			}
			client, p, err := a.client(ctx, true)
			if err != nil {
				return model.Result{}, err
			}
			var result map[string]any
			if kind == "subscriptions" {
				result, err = client.Subscriptions(ctx, p.App, id, offers)
			} else {
				result, err = client.Products(ctx, p.App, id, offers, legacy)
			}
			return model.Result{Data: result, Meta: map[string]any{"source": "android-publisher", "app": p.App, "legacy": legacy, "notice": "Catalog configuration only; offer/trial eligibility for an individual user is not determined. Regional currencies and prices are not aggregated."}}, err
		})
		group.AddCommand(c)
	}
	return group
}
