package cli

import (
	"context"
	"github.com/spf13/cobra"
	"os"
	"playops/internal/fault"
	"playops/internal/model"
	"playops/internal/releaseflow"
)

func (a *App) symbols() *cobra.Command {
	group := &cobra.Command{Use: "symbols", Short: "Upload native symbols or ProGuard mapping inside a caller-owned edit"}
	var edit, kind, file string
	var version int64
	var confirm, dry bool
	c := &cobra.Command{Use: "upload", Short: "Upload without committing or deleting the edit", Args: exact(0)}
	f := c.Flags()
	f.StringVar(&edit, "edit-id", "", "Caller-owned edit ID")
	f.StringVar(&kind, "type", "", "nativeCode or proguard")
	f.StringVar(&file, "file", "", "Native symbols ZIP or ProGuard mapping")
	f.Int64Var(&version, "version-code", 0, "Version code corresponding to these symbols")
	f.BoolVar(&confirm, "confirm", false, "Authorize upload; caller must abort edit on failure")
	f.BoolVar(&dry, "dry-run", false, "Validate the local file without contacting Google")
	required(c, "edit-id", "type", "file", "version-code")
	c.RunE = a.run(func(ctx context.Context, _ *cobra.Command, _ []string) (model.Result, error) {
		if !dry && !confirm {
			return model.Result{}, fault.Input("symbols upload requires --confirm or --dry-run")
		}
		if version <= 0 {
			return model.Result{}, fault.Input("--version-code must be positive")
		}
		if err := releaseflow.ValidateSymbols(file, kind); err != nil {
			return model.Result{}, err
		}
		hash, err := releaseflow.HashFile(file)
		if err != nil {
			return model.Result{}, err
		}
		if dry {
			return model.Result{Data: hash, Meta: map[string]any{"source": "local-preview", "serverValidated": false}}, nil
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
		if err = client.CheckEdit(ctx, p.App, edit); err != nil {
			return model.Result{}, editFailure(err, edit)
		}
		reader, err := os.Open(file)
		if err != nil {
			return model.Result{}, err
		}
		defer reader.Close()
		err = client.Symbols(ctx, p.App, edit, version, kind, reader)
		return model.Result{Data: map[string]any{"editId": edit, "versionCode": version, "type": kind, "committed": false, "transactionOwner": "caller"}}, editFailure(err, edit)
	})
	group.AddCommand(c)
	return group
}
