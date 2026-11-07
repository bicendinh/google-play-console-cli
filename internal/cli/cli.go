// Package cli defines the public command and output contracts.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"playops/internal/config"
	"playops/internal/fault"
	"playops/internal/googleapi"
	"playops/internal/model"
)

var Version = "dev"

const SchemaVersion = "1"

type Envelope struct {
	SchemaVersion string         `json:"schemaVersion"`
	Data          any            `json:"data"`
	Meta          map[string]any `json:"meta"`
	Error         *fault.Error   `json:"error,omitempty"`
}
type Factory func(context.Context, string) (*googleapi.Client, error)
type App struct {
	Factory                                       Factory
	Now                                           func() time.Time
	profile, app, credentials, configPath, output string
	timeout                                       time.Duration
	requestTimeout                                time.Duration
	out, errOut                                   io.Writer
}

func New(out, errOut io.Writer, factory Factory) (*cobra.Command, *App) {
	if factory == nil {
		factory = googleapi.New
	}
	a := &App{Factory: factory, Now: time.Now, out: out, errOut: errOut}
	root := &cobra.Command{Use: "playops", Short: "Google Play releases and metrics for AI agents", SilenceUsage: true, SilenceErrors: true, CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true}}
	root.SetOut(out)
	root.SetErr(errOut)
	profile := os.Getenv("PLAYOPS_PROFILE")
	if profile == "" {
		profile = "default"
	}
	f := root.PersistentFlags()
	f.StringVar(&a.profile, "profile", profile, "Credential profile")
	f.StringVar(&a.app, "app", "", "Android package name (overrides profile)")
	f.StringVar(&a.credentials, "credentials", "", "Credentials file (overrides profile; otherwise ADC)")
	f.StringVar(&a.configPath, "config", "", "Config path (or PLAYOPS_CONFIG)")
	f.StringVar(&a.output, "output", "json", "Output: json or table")
	f.DurationVar(&a.timeout, "timeout", 5*time.Minute, "Whole-command timeout")
	f.DurationVar(&a.requestTimeout, "request-timeout", 0, "Per-request timeout; 0 uses the whole-command deadline (uploads may span multiple requests)")
	root.SetFlagErrorFunc(func(_ *cobra.Command, e error) error { return fault.Input("%s", e.Error()) })
	root.PersistentPreRunE = func(_ *cobra.Command, _ []string) error {
		if a.output != "json" && a.output != "table" {
			return fault.Input("--output must be json or table")
		}
		if a.timeout <= 0 {
			return fault.Input("--timeout must be positive")
		}
		if a.requestTimeout < 0 {
			return fault.Input("--request-timeout cannot be negative")
		}
		return config.ValidateProfile(a.profile)
	}
	root.RunE = a.run(func(ctx context.Context, c *cobra.Command, args []string) (model.Result, error) {
		return a.schema(root), nil
	})
	root.AddCommand(a.auth(), a.configuration(), a.doctor(), a.apps(), a.releases(), a.rollout(), a.stats(), a.vitals(), a.listings(), a.catalog("subscriptions"), a.catalog("products"))
	root.AddCommand(&cobra.Command{Use: "version", Short: "Show CLI version", Args: exact(0), RunE: a.run(func(context.Context, *cobra.Command, []string) (model.Result, error) {
		return model.Result{Data: map[string]string{"version": Version}}, nil
	})})
	root.AddCommand(&cobra.Command{Use: "schema", Short: "Describe commands, flags, output and exit codes as JSON", Args: exact(0), RunE: a.run(func(context.Context, *cobra.Command, []string) (model.Result, error) { return a.schema(root), nil })})
	return root, a
}
func exact(n int) func(*cobra.Command, []string) error {
	return func(c *cobra.Command, args []string) error {
		if err := cobra.ExactArgs(n)(c, args); err != nil {
			return fault.Input("%s", err.Error())
		}
		return nil
	}
}
func (a *App) run(fn func(context.Context, *cobra.Command, []string) (model.Result, error)) func(*cobra.Command, []string) error {
	return func(c *cobra.Command, args []string) error {
		ctx, cancel := context.WithTimeout(c.Context(), a.timeout)
		defer cancel()
		r, err := fn(ctx, c, args)
		if err != nil {
			return err
		}
		if r.Meta == nil {
			r.Meta = map[string]any{}
		}
		r.Meta["profile"] = a.profile
		r.Meta["fetchedAt"] = a.Now().UTC().Format(time.RFC3339)
		return a.write(Envelope{SchemaVersion: SchemaVersion, Data: r.Data, Meta: r.Meta})
	}
}
func (a *App) write(e Envelope) error {
	if a.output == "table" && e.Error == nil {
		// Flatten the same versioned data without losing nested metrics or nulls.
		b, err := json.Marshal(e.Data)
		if err != nil {
			return err
		}
		var value any
		dec := json.NewDecoder(strings.NewReader(string(b)))
		dec.UseNumber()
		if err = dec.Decode(&value); err != nil {
			return err
		}
		w := tabwriter.NewWriter(a.out, 0, 4, 2, ' ', 0)
		rows, ok := value.([]any)
		if !ok {
			rows = []any{value}
		}
		flat := []map[string]string{}
		keys := map[string]bool{}
		for _, row := range rows {
			m := map[string]string{}
			flatten("", row, m)
			for key := range m {
				keys[key] = true
			}
			flat = append(flat, m)
		}
		headers := []string{}
		for key := range keys {
			headers = append(headers, key)
		}
		sort.Strings(headers)
		if len(headers) == 0 {
			fmt.Fprintln(w, "No rows")
		} else {
			fmt.Fprintln(w, strings.Join(headers, "\t"))
			for _, row := range flat {
				cells := []string{}
				for _, key := range headers {
					cells = append(cells, row[key])
				}
				fmt.Fprintln(w, strings.Join(cells, "\t"))
			}
		}
		if err = w.Flush(); err != nil {
			return err
		}
		meta, _ := json.Marshal(e.Meta)
		_, err = fmt.Fprintln(a.errOut, string(meta))
		return err
	}
	encoder := json.NewEncoder(a.out)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(e)
}
func flatten(prefix string, value any, out map[string]string) {
	if m, ok := value.(map[string]any); ok {
		for key, v := range m {
			path := key
			if prefix != "" {
				path = prefix + "." + key
			}
			flatten(path, v, out)
		}
		return
	}
	if prefix == "" {
		prefix = "value"
	}
	if s, ok := value.(string); ok {
		out[prefix] = strings.NewReplacer("\t", "\\t", "\n", "\\n", "\r", "\\r").Replace(s)
		return
	}
	b, _ := json.Marshal(value)
	out[prefix] = string(b)
}
func Execute(ctx context.Context, args []string, out, errOut io.Writer, factory Factory) int {
	root, a := New(out, errOut, factory)
	root.SetArgs(args)
	// Preserve diagnostic context when parsing aborts at an earlier invalid flag.
	for i, arg := range args {
		if arg == "--" {
			break
		}
		if strings.HasPrefix(arg, "--profile=") {
			a.profile = strings.TrimPrefix(arg, "--profile=")
		}
		if arg == "--profile" && i+1 < len(args) {
			a.profile = args[i+1]
		}
	}
	if err := root.ExecuteContext(ctx); err != nil {
		var typed *fault.Error
		if !errors.As(err, &typed) && (strings.Contains(err.Error(), "unknown command") || strings.Contains(err.Error(), "required flag(s)")) {
			err = fault.Input("%s", err.Error())
		}
		e := fault.From(err)
		if writeErr := a.write(Envelope{SchemaVersion: SchemaVersion, Data: nil, Meta: map[string]any{"profile": a.profile}, Error: e}); writeErr != nil {
			fmt.Fprintln(errOut, "Could not write command result")
			return 1
		}
		return e.Exit
	}
	return 0
}
func (a *App) settings() (string, config.Config, config.Profile, error) {
	path, err := config.Path(a.configPath)
	if err != nil {
		return "", config.Config{}, config.Profile{}, err
	}
	c, err := config.Load(path)
	if err != nil {
		return "", c, config.Profile{}, err
	}
	p := c.Profiles[a.profile]
	if a.app != "" {
		p.App = a.app
	}
	if a.credentials != "" {
		p.Credentials = a.credentials
		p.Auth = "service-account"
	}
	return path, c, p, nil
}
func (a *App) client(ctx context.Context, needApp bool) (*googleapi.Client, config.Profile, error) {
	_, _, p, err := a.settings()
	if err != nil {
		return nil, p, err
	}
	if needApp {
		if err = config.ValidateApp(p.App); err != nil {
			return nil, p, err
		}
	}
	c, err := a.Factory(ctx, p.Credentials)
	if err == nil && c.HTTP != nil {
		c.HTTP.Timeout = a.requestTimeout
	}
	return c, p, err
}
func (a *App) schema(root *cobra.Command) model.Result {
	type flag struct {
		Name        string `json:"name"`
		Type        string `json:"type"`
		Default     string `json:"default"`
		Description string `json:"description"`
		Required    bool   `json:"required"`
	}
	type command struct {
		Command     string `json:"command"`
		Description string `json:"description"`
		Flags       []flag `json:"flags"`
	}
	commands := []command{}
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		if c.Hidden {
			return
		}
		if c.Runnable() {
			entry := command{Command: c.CommandPath(), Description: c.Short, Flags: []flag{}}
			seen := map[string]bool{}
			visit := func(f *pflag.Flag) {
				if !seen[f.Name] {
					required := len(f.Annotations[cobra.BashCompOneRequiredFlag]) > 0
					entry.Flags = append(entry.Flags, flag{Name: f.Name, Type: f.Value.Type(), Default: f.DefValue, Description: f.Usage, Required: required})
					seen[f.Name] = true
				}
			}
			c.LocalFlags().VisitAll(visit)
			c.InheritedFlags().VisitAll(visit)
			commands = append(commands, entry)
		}
		for _, child := range c.Commands() {
			walk(child)
		}
	}
	walk(root)
	return model.Result{Data: map[string]any{"commands": commands, "output": map[string]string{"schemaVersion": "1", "data": "command-specific object or array; null on error", "meta": "source, period, dataStatus, availableThrough, profile and fetchedAt where applicable", "error": "code, message, retryable, optional details"}, "exitCodes": map[string]string{"0": "success (including no data)", "1": "unexpected error", "2": "invalid input or Google validation rejection", "3": "authentication, permissions or failed doctor", "4": "conflict or unknown commit outcome", "5": "network or service failure"}, "writePolicy": "Play writes require --confirm. --dry-run performs local validation only. Never automatically replay release writes."}}
}
