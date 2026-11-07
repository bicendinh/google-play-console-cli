package releaseflow

import (
	"context"
	"errors"
	"fmt"
	"os"
	"playops/internal/bundle"
	"playops/internal/testutil"
	"strings"
	"testing"
)

type fakeTools struct {
	missing, invalidSigner bool
}

func TestLocalToolSeparatesDiagnostics(t *testing.T) {
	if len(os.Args) > 1 && os.Args[len(os.Args)-1] == "PLAYOPS_TOOL_TEST" {
		fmt.Println("com.example.app")
		fmt.Fprintln(os.Stderr, "Runtime warning")
		os.Exit(0)
	}
	out, err := (localTools{}).Run(context.Background(), os.Args[0], "-test.run=^TestLocalToolSeparatesDiagnostics$", "--", "PLAYOPS_TOOL_TEST")
	if err != nil || strings.TrimSpace(out) != "com.example.app" {
		t.Fatalf("diagnostics polluted parsed stdout: %q %v", out, err)
	}
}

func (f fakeTools) Find(name string) (string, error) {
	if f.missing {
		return "", errors.New("missing")
	}
	return name, nil
}
func (f fakeTools) Run(_ context.Context, path string, args ...string) (string, error) {
	switch path {
	case "jarsigner":
		if f.invalidSigner {
			return "jar verified. This jar contains unsigned entries", nil
		}
		return "jar verified. The signer certificate is self-signed.", nil
	case "bundletool":
		return "", nil
	}
	return "", errors.New("unexpected tool")
}
func TestPreflightCertificateAndRequiredChecks(t *testing.T) {
	path := testutil.Bundle(t)
	signers, err := bundle.Signers(path)
	if err != nil {
		t.Fatal(err)
	}
	m := Manifest{App: "com.example.app", Profile: "default", Track: "internal", Name: "Test", Status: "draft", Bundle: path, VersionCode: 123, UploadCertificateSHA256: signers[0], Preflight: PreflightOptions{Required: true}}
	tools := fakeTools{}
	checks, err := preflight(context.Background(), m, tools)
	if err != nil {
		t.Fatalf("%+v %v", checks, err)
	}
	for _, c := range checks {
		if c.Status != "passed" {
			t.Fatalf("unverified: %+v", c)
		}
	}
	for _, bad := range []fakeTools{{missing: true}, {invalidSigner: true}} {
		if _, err = preflight(context.Background(), m, bad); err == nil {
			t.Fatal("accepted missing/invalid required check")
		}
	}
	m.UploadCertificateSHA256 = strings.Repeat("0", 64)
	if _, err = preflight(context.Background(), m, tools); err == nil {
		t.Fatal("wrong upload signer accepted")
	}
	m.Preflight.Required = false
	m.UploadCertificateSHA256 = ""
	checks, err = preflight(context.Background(), m, fakeTools{missing: true})
	if err != nil {
		t.Fatal(err)
	}
	if checks[1].Status != "passed" {
		t.Fatal("native metadata should run without Java")
	}
	m.VersionCode = 124
	if _, err = preflight(context.Background(), m, fakeTools{missing: true}); err == nil {
		t.Fatal("native version mismatch accepted")
	}
	m.VersionCode = 123
	m.App = "com.other.app"
	if _, err = preflight(context.Background(), m, fakeTools{missing: true}); err == nil {
		t.Fatal("native package mismatch accepted")
	}
}
