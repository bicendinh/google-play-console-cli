//go:build integration

package integration_test

import (
	"context"
	"os"
	"testing"
	"time"

	"playops/internal/config"
	"playops/internal/googleapi"
	"playops/internal/publisher"
)

func TestInternalDraftOnly(t *testing.T) {
	if os.Getenv("PLAYOPS_LIVE_TEST") != "1" {
		t.Skip("set PLAYOPS_LIVE_TEST=1 to opt in to a real internal draft upload")
	}
	app, path := os.Getenv("PLAYOPS_LIVE_APP"), os.Getenv("PLAYOPS_LIVE_AAB")
	if err := config.ValidateApp(app); err != nil {
		t.Fatal(err)
	}
	if path == "" {
		t.Fatal("PLAYOPS_LIVE_AAB is required")
	}
	if err := publisher.ValidateBundle(path); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	client, err := googleapi.New(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	out, err := (publisher.Service{API: client}).Execute(ctx, publisher.Request{Action: "upload", App: app, Track: "internal", Name: "playops integration draft"}, file)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "draft" || !out.Committed {
		t.Fatalf("expected committed draft: %+v", out)
	}
	if _, err = client.Releases(ctx, app, "internal"); err != nil {
		t.Fatal(err)
	}
	t.Logf("Created internal draft version %d. Inspect it in Play Console; no publish operation was issued.", out.VersionCode)
}
