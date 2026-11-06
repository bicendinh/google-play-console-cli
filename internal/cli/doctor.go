package cli

import (
	"context"

	"github.com/spf13/cobra"

	"playops/internal/fault"
	"playops/internal/model"
)

type check struct {
	Capability string `json:"capability"`
	Status     string `json:"status"`
	Message    string `json:"message"`
	Code       string `json:"code,omitempty"`
}

func (a *App) doctor() *cobra.Command {
	var track string
	c := &cobra.Command{Use: "doctor", Short: "Test release reads, Android vitals and reports independently", Args: exact(0), RunE: a.run(func(ctx context.Context, _ *cobra.Command, _ []string) (model.Result, error) {
		client, p, err := a.client(ctx, true)
		if err != nil {
			return model.Result{}, err
		}
		checks := []check{}
		failed := false
		probe := func(name, hint string, fn func() error) {
			e := fn()
			if e != nil {
				failed = true
				f := fault.From(e)
				checks = append(checks, check{Capability: name, Status: "failed", Message: f.Message + ". " + hint, Code: f.Code})
			} else {
				checks = append(checks, check{Capability: name, Status: "ok", Message: hint})
			}
		}
		probe("publishing", "Enable Android Publisher API and grant this identity app access plus release permissions. This probe verifies release reads only; write permission is validated on an actual edit.", func() error { _, e := client.Releases(ctx, p.App, track); return e })
		probe("vitals", "Enable Play Developer Reporting API and grant View app information (read-only).", func() error {
			_, e := client.Freshness(ctx, p.App, "crashes")
			if e != nil {
				return e
			}
			_, e = client.Freshness(ctx, p.App, "anrs")
			return e
		})
		probe("reports", "Set reports-bucket from Play Console > Play apps reports; grant the service account access to app reports.", func() error {
			if p.ReportsBucket == "" {
				return fault.Input("reports-bucket is not configured")
			}
			return client.CheckReports(ctx, p.ReportsBucket, p.App)
		})
		if failed {
			e := fault.New("DOCTOR_FAILED", "One or more capabilities are unavailable; inspect details.checks", 3)
			e.Details = map[string]any{"checks": checks}
			return model.Result{}, e
		}
		return model.Result{Data: checks, Meta: map[string]any{"app": p.App, "track": track}}, nil
	})}
	c.Flags().StringVar(&track, "track", "internal", "Track used for the release read probe")
	return c
}
