package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"

	"playops/internal/config"
	"playops/internal/fault"
	"playops/internal/googleapi"
	"playops/internal/model"
)

func (a *App) auth() *cobra.Command {
	group := &cobra.Command{Use: "auth", Short: "Configure and inspect authentication"}
	var file string
	var adc bool
	login := &cobra.Command{Use: "login", Short: "Store a credentials file path or select ADC (no browser login)", Args: exact(0), RunE: a.run(func(ctx context.Context, _ *cobra.Command, _ []string) (model.Result, error) {
		if (file == "" && !adc) || (file != "" && adc) {
			return model.Result{}, fault.Input("Choose exactly one of --service-account FILE or --adc")
		}
		path, c, p, err := a.settings()
		if err != nil {
			return model.Result{}, err
		}
		if adc {
			p.Auth = "adc"
			p.Credentials = ""
		} else {
			b, e := os.ReadFile(file)
			if e != nil {
				return model.Result{}, fault.Input("Cannot read service account file")
			}
			var header struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(b, &header) != nil || header.Type != "service_account" {
				return model.Result{}, fault.Input("--service-account requires a service_account JSON key; use --adc for workload identity")
			}
			if _, e = googleapi.Credentials(ctx, file); e != nil {
				return model.Result{}, e
			}
			p.Credentials, err = filepath.Abs(file)
			if err != nil {
				return model.Result{}, err
			}
			p.Auth = "service-account"
		}
		c.Profiles[a.profile] = p
		if err = config.Save(path, c); err != nil {
			return model.Result{}, err
		}
		return model.Result{Data: map[string]any{"profile": a.profile, "auth": p.Auth, "credentialsPath": p.Credentials, "configured": true, "networkValidated": false}}, nil
	})}
	login.Flags().StringVar(&file, "service-account", "", "Path to your service account JSON key (stored as a path, never copied)")
	login.Flags().BoolVar(&adc, "adc", false, "Use Application Default Credentials, including CI workload identity")
	group.AddCommand(login, &cobra.Command{Use: "status", Short: "Check local credentials configuration without requesting an access token", Args: exact(0), RunE: a.run(func(ctx context.Context, _ *cobra.Command, _ []string) (model.Result, error) {
		_, _, p, err := a.settings()
		if err != nil {
			return model.Result{}, err
		}
		if _, err = googleapi.Credentials(ctx, p.Credentials); err != nil {
			return model.Result{}, err
		}
		mode := p.Auth
		if mode == "" {
			mode = "adc"
		}
		return model.Result{Data: map[string]any{"profile": a.profile, "auth": mode, "configured": true, "networkValidated": false, "next": "Run doctor --app PACKAGE to test access"}}, nil
	})})
	return group
}
func (a *App) configuration() *cobra.Command {
	group := &cobra.Command{Use: "config", Short: "Manage local credential profiles"}
	group.AddCommand(&cobra.Command{Use: "show", Short: "Show the selected profile (never shows credential contents)", Args: exact(0), RunE: a.run(func(context.Context, *cobra.Command, []string) (model.Result, error) {
		path, _, p, e := a.settings()
		return model.Result{Data: map[string]any{"path": path, "profile": a.profile, "settings": p}}, e
	})})
	group.AddCommand(&cobra.Command{Use: "profiles", Short: "List configured profile names", Args: exact(0), RunE: a.run(func(context.Context, *cobra.Command, []string) (model.Result, error) {
		_, c, _, e := a.settings()
		names := []string{}
		for n := range c.Profiles {
			names = append(names, n)
		}
		sort.Strings(names)
		return model.Result{Data: names}, e
	})})
	group.AddCommand(&cobra.Command{Use: "set KEY VALUE", Short: "Set app or reports-bucket in the selected profile", Args: exact(2), RunE: a.run(func(_ context.Context, _ *cobra.Command, args []string) (model.Result, error) {
		path, c, p, err := a.settings()
		if err != nil {
			return model.Result{}, err
		}
		switch args[0] {
		case "app":
			if err = config.ValidateApp(args[1]); err != nil {
				return model.Result{}, err
			}
			p.App = args[1]
		case "reports-bucket":
			p.ReportsBucket, err = config.Bucket(args[1])
			if err != nil {
				return model.Result{}, err
			}
		default:
			return model.Result{}, fault.Input("KEY must be app or reports-bucket")
		}
		c.Profiles[a.profile] = p
		err = config.Save(path, c)
		return model.Result{Data: map[string]any{"profile": a.profile, "settings": p}}, err
	})})
	return group
}
