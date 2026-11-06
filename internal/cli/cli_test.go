package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"playops/internal/config"
	"playops/internal/googleapi"
	"playops/internal/testutil"
)

func invoke(t *testing.T, path string, factory Factory, args ...string) (int, Envelope, string) {
	t.Helper()
	var out, stderr bytes.Buffer
	full := append([]string{"--config", path}, args...)
	code := Execute(context.Background(), full, &out, &stderr, factory)
	var envelope Envelope
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("stdout must contain exactly one JSON object: %s (%v)", out.String(), err)
	}
	if envelope.SchemaVersion != "1" {
		t.Fatal("missing schema version")
	}
	return code, envelope, stderr.String()
}
func offline(t *testing.T) Factory {
	return func(context.Context, string) (*googleapi.Client, error) {
		t.Fatal("offline command contacted Google")
		return nil, nil
	}
}
func TestAllDryRunsAreOffline(t *testing.T) {
	file := testutil.Bundle(t)
	path := filepath.Join(t.TempDir(), "config.json")
	commands := [][]string{{"releases", "upload", "--track", "internal", "--file", file}, {"releases", "publish", "--track", "production", "--version-code", "123", "--rollout", "10"}, {"releases", "promote", "--from", "internal", "--to", "production", "--version-code", "123", "--rollout", "10"}, {"rollout", "set", "--track", "production", "--version-code", "123", "--rollout", "25"}, {"rollout", "halt", "--track", "production", "--version-code", "123"}, {"rollout", "resume", "--track", "production", "--version-code", "123"}}
	for _, args := range commands {
		t.Run(strings.Join(args[:2], "-"), func(t *testing.T) {
			args = append(args, "--app", "com.example.app", "--dry-run")
			code, e, stderr := invoke(t, path, offline(t), args...)
			if code != 0 || e.Error != nil || e.Data.(map[string]any)["serverValidated"] != false || stderr != "" {
				t.Fatalf("dry run: %+v %s", e, stderr)
			}
		})
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("dry-run created config")
	}
}
func TestInputErrorsAndNoSecretLeak(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	for _, args := range [][]string{{"missing-command"}, {"releases", "publish", "--track", "production", "--version-code", "123", "--confirm", "--app", "com.example.app"}, {"rollout", "set", "--track", "production", "--version-code", "123", "--rollout", "NaN", "--dry-run", "--app", "com.example.app"}, {"releases", "upload", "--track", "internal", "--file", "missing.aab", "--app", "com.example.app"}, {"stats", "installs", "--from", "bad", "--to", "2026-11-01"}, {"version", "extra"}, {"version", "--unknown"}} {
		code, e, _ := invoke(t, path, offline(t), args...)
		if code != 2 || e.Error == nil {
			t.Fatalf("%v: %d %+v", args, code, e)
		}
	}
	code, e, _ := invoke(t, path, nil, "auth", "status", "--credentials", "/missing/secret-key.json")
	if code != 3 || e.Error == nil || strings.Contains(e.Error.Message, "secret-key") {
		t.Fatalf("auth: %+v", e)
	}
}
func TestProfilesAndSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	for _, args := range [][]string{{"config", "set", "app", "com.example.one"}, {"--profile", "other", "config", "set", "app", "com.example.two"}, {"config", "set", "reports-bucket", "gs://pubsite_prod_rev_123/"}} {
		if code, e, _ := invoke(t, path, offline(t), args...); code != 0 {
			t.Fatalf("config: %+v", e)
		}
	}
	stored, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Profiles["default"].App != "com.example.one" || stored.Profiles["other"].App != "com.example.two" || stored.Profiles["default"].ReportsBucket != "pubsite_prod_rev_123" {
		t.Fatal("profiles mixed")
	}
	info, _ := os.Stat(path)
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("config mode %o", info.Mode().Perm())
	}
	code, e, _ := invoke(t, path, offline(t), "schema")
	if code != 0 {
		t.Fatal(e.Error)
	}
	commands := e.Data.(map[string]any)["commands"].([]any)
	if len(commands) < 20 {
		t.Fatal("incomplete schema")
	}
	for _, entry := range commands {
		c := entry.(map[string]any)
		if !strings.HasPrefix(c["command"].(string), "playops") {
			t.Fatal("invalid command path")
		}
	}
}
func TestDoctorProbesEverySource(t *testing.T) {
	publishing, vitals, reports := 0, 0, 0
	client := testutil.Google(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/tracks/"):
			publishing++
			w.WriteHeader(403)
			fmt.Fprint(w, `{"error":{"code":403,"message":"permission denied"}}`)
		case strings.Contains(r.URL.Path, "MetricSet"):
			vitals++
			fmt.Fprint(w, `{"freshnessInfo":{}}`)
		case strings.Contains(r.URL.Path, "/storage/"):
			reports++
			w.WriteHeader(403)
			fmt.Fprint(w, `{"error":{"code":403,"message":"denied"}}`)
		default:
			t.Errorf("unexpected %s", r.URL.Path)
			http.Error(w, "unexpected", 500)
		}
	})
	path := filepath.Join(t.TempDir(), "config.json")
	if err := config.Save(path, config.Config{Profiles: map[string]config.Profile{"default": {App: "com.example.app", ReportsBucket: "reports"}}}); err != nil {
		t.Fatal(err)
	}
	code, e, _ := invoke(t, path, func(context.Context, string) (*googleapi.Client, error) { return client, nil }, "doctor")
	if code != 3 || e.Error.Code != "DOCTOR_FAILED" || publishing != 1 || vitals != 2 || reports != 1 {
		t.Fatalf("doctor %d %+v, counts %d %d %d", code, e, publishing, vitals, reports)
	}
}
func TestLocksAndTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	a := &App{configPath: path}
	unlock, err := a.lock("com.example.app")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.lock("com.example.app"); err == nil {
		t.Fatal("concurrent edit was allowed")
	}
	unlock()
	u, err := a.lock("com.example.app")
	if err != nil {
		t.Fatal(err)
	}
	u()
	var out, stderr bytes.Buffer
	code := Execute(context.Background(), []string{"--config", path, "--output", "table", "version"}, &out, &stderr, offline(t))
	if code != 0 || !strings.Contains(out.String(), "version") || json.Valid(out.Bytes()) || !json.Valid(bytes.TrimSpace(stderr.Bytes())) {
		t.Fatalf("table: %s / %s", out.String(), stderr.String())
	}
}

func TestRolloutInspectionRequiresConsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	code, e, _ := invoke(t, path, offline(t), "rollout", "get", "--track", "production", "--app", "com.example.app")
	if code != 2 || e.Error == nil {
		t.Fatalf("temporary edit requires explicit consent: %+v", e)
	}
}
