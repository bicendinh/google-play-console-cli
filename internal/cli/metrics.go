package cli

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"playops/internal/config"
	"playops/internal/fault"
	"playops/internal/metrics"
	"playops/internal/model"
)

func (a *App) apps() *cobra.Command {
	group := &cobra.Command{Use: "apps", Short: "Discover apps available to the Reporting API"}
	group.AddCommand(&cobra.Command{Use: "list", Short: "List accessible apps, following every page", Args: exact(0), RunE: a.run(func(ctx context.Context, _ *cobra.Command, _ []string) (model.Result, error) {
		client, _, err := a.client(ctx, false)
		if err != nil {
			return model.Result{}, err
		}
		return (metrics.Service{Reporting: client}).Apps(ctx)
	})})
	return group
}
func (a *App) stats() *cobra.Command {
	group := &cobra.Command{Use: "stats", Short: "Read daily statistics from monthly Cloud Storage exports"}
	for _, kind := range []string{"installs", "ratings"} {
		var from, to, dimension, bucket string
		c := &cobra.Command{Use: kind, Short: "Read daily " + kind + " without aggregating rows", Args: exact(0)}
		c.Flags().StringVar(&from, "from", "", "First report date, inclusive (YYYY-MM-DD)")
		c.Flags().StringVar(&to, "to", "", "Last report date, inclusive (YYYY-MM-DD)")
		c.Flags().StringVar(&dimension, "group-by", "country", "Dimension: country or app-version")
		c.Flags().StringVar(&bucket, "bucket", "", "Reports bucket (overrides profile)")
		c.RunE = a.run(func(ctx context.Context, _ *cobra.Command, _ []string) (model.Result, error) {
			if dimension != "country" && dimension != "app-version" {
				return model.Result{}, fault.Input("--group-by must be country or app-version")
			}
			p, err := model.DateRange(from, to, "America/Los_Angeles", a.Now())
			if err != nil {
				return model.Result{}, err
			}
			client, settings, err := a.client(ctx, true)
			if err != nil {
				return model.Result{}, err
			}
			b := bucket
			if b == "" {
				b = settings.ReportsBucket
			}
			if b == "" {
				return model.Result{}, fault.Input("Set reports-bucket in the profile or pass --bucket")
			}
			b, err = config.Bucket(b)
			if err != nil {
				return model.Result{}, err
			}
			return (metrics.Service{Reports: client}).Stats(ctx, settings.App, b, kind, dimension, p)
		})
		group.AddCommand(c)
	}
	return group
}
func (a *App) vitals() *cobra.Command {
	group := &cobra.Command{Use: "vitals", Short: "Read daily crash and ANR rates from Android vitals"}
	for _, kind := range []string{"crashes", "anrs"} {
		var from, to, country, dimension string
		var version int64
		var days int
		c := &cobra.Command{Use: kind, Short: "Query daily " + kind + " with data freshness", Args: exact(0)}
		c.Flags().StringVar(&from, "from", "", "First date, inclusive (America/Los_Angeles)")
		c.Flags().StringVar(&to, "to", "", "Last date, inclusive (America/Los_Angeles)")
		c.Flags().StringVar(&country, "country", "", "Two-letter country filter")
		c.Flags().Int64Var(&version, "version-code", 0, "Positive app version code filter")
		c.Flags().StringVar(&dimension, "group-by", "", "Dimension: country or app-version (default: app-wide)")
		c.Flags().IntVar(&days, "days", 0, "Last N complete source-calendar days (1–1095); cannot combine with --from/--to")
		c.RunE = a.run(func(ctx context.Context, cmd *cobra.Command, _ []string) (model.Result, error) {
			if cmd.Flags().Changed("days") {
				if days < 1 || days > 1095 || cmd.Flags().Changed("from") || cmd.Flags().Changed("to") {
					return model.Result{}, fault.Input("--days requires 1–1095 and cannot be combined with --from/--to")
				}
				loc, e := time.LoadLocation("America/Los_Angeles")
				if e != nil {
					return model.Result{}, e
				}
				today := a.Now().In(loc)
				from = today.AddDate(0, 0, -days).Format(time.DateOnly)
				to = today.AddDate(0, 0, -1).Format(time.DateOnly)
			}
			if version < 0 {
				return model.Result{}, fault.Input("--version-code must be positive")
			}
			country = strings.ToUpper(country)
			if country != "" && !regexp.MustCompile(`^[A-Z]{2}$`).MatchString(country) {
				return model.Result{}, fault.Input("--country must be a two-letter country code")
			}
			if dimension != "" && dimension != "country" && dimension != "app-version" {
				return model.Result{}, fault.Input("--group-by must be country or app-version")
			}
			p, err := model.DateRange(from, to, "America/Los_Angeles", a.Now())
			if err != nil {
				return model.Result{}, err
			}
			client, settings, err := a.client(ctx, true)
			if err != nil {
				return model.Result{}, err
			}
			return (metrics.Service{Reporting: client}).Vitals(ctx, settings.App, kind, p, version, country, dimension)
		})
		group.AddCommand(c)
	}
	return group
}
