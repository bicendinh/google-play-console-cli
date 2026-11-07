package releaseflow

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"playops/internal/bundle"
	"playops/internal/fault"
)

type MetadataMismatch struct {
	Field    string `json:"field"`
	Expected any    `json:"expected"`
	Actual   any    `json:"actual"`
}

type toolRunner interface {
	Find(string) (string, error)
	Run(context.Context, string, ...string) (string, error)
}
type localTools struct{}

func (localTools) Find(name string) (string, error) { return exec.LookPath(name) }

type cappedOutput struct{ bytes.Buffer }

func (b *cappedOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := 1024*1024 - b.Len()
	if remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = b.Buffer.Write(p)
	}
	return n, nil
}
func (localTools) Run(ctx context.Context, path string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	var out, diagnostics cappedOutput
	cmd.Stdout = &out
	cmd.Stderr = &diagnostics
	err := cmd.Run()
	if err != nil {
		return out.String() + "\n" + diagnostics.String(), err
	}
	return out.String(), err
}

func Preflight(ctx context.Context, m Manifest) ([]Check, error) {
	return preflight(ctx, m, localTools{})
}
func preflight(ctx context.Context, m Manifest, tools toolRunner) ([]Check, error) {
	checks := []Check{}
	failed := false
	add := func(name, status, message string, details any) {
		checks = append(checks, Check{Name: name, Status: status, Message: message, Details: details})
		if status == "failed" {
			failed = true
		}
	}
	issues := inputIssues(m)
	for _, group := range m.Images {
		for i, path := range group.Files {
			if _, err := ImageFile(path, group.Type, m.FlattenAlpha); err != nil {
				issues = append(issues, Issue{Field: "images.files", Locale: group.Locale, File: path, Index: i + 1, Message: err.Error()})
			}
		}
		for i, path := range group.SourceFiles {
			if _, err := ImageFile(path, group.Type, m.FlattenAlpha); err != nil {
				issues = append(issues, Issue{Field: "images.sourceFiles", Locale: group.Locale, File: path, Index: i + 1, Message: err.Error()})
			}
		}
		for index, path := range group.Replace {
			if _, err := ImageFile(path, group.Type, m.FlattenAlpha); err != nil {
				issues = append(issues, Issue{Field: "images.replace", Locale: group.Locale, File: path, Index: index, Message: err.Error()})
			}
		}
	}
	if len(issues) > 0 {
		add("manifest", "failed", "Local input validation failed", map[string]any{"issues": issues})
	} else {
		add("manifest", "passed", "Local fields, locales and assets checked; no server validation", nil)
	}
	for _, warning := range imageIntentWarnings(m) {
		add("imageIntent", "warning", warning.Message, warning)
	}
	if m.Bundle != "" {
		metadata, err := bundle.Inspect(m.Bundle)
		if err != nil {
			add("bundleMetadata", "failed", err.Error(), nil)
		} else {
			mismatches := []MetadataMismatch{}
			for _, pair := range []MetadataMismatch{{"package", m.App, metadata.Package}, {"versionCode", m.VersionCode, metadata.VersionCode}} {
				if pair.Expected != pair.Actual {
					mismatches = append(mismatches, pair)
				}
			}
			for _, pair := range []MetadataMismatch{{"versionName", m.VersionName, metadata.VersionName}, {"minSdk", m.MinSDK, metadata.MinSDK}, {"targetSdk", m.TargetSDK, metadata.TargetSDK}} {
				if pair.Expected != "" && pair.Expected != int64(0) && pair.Expected != pair.Actual {
					mismatches = append(mismatches, pair)
				}
			}
			if len(mismatches) > 0 {
				messages := []string{}
				for _, v := range mismatches {
					messages = append(messages, fmt.Sprintf("%s: expected %v, actual %v", v.Field, v.Expected, v.Actual))
				}
				add("bundleMetadata", "failed", strings.Join(messages, "; "), map[string]any{"observed": metadata, "mismatches": mismatches})
			} else {
				add("bundleMetadata", "passed", "AAB metadata inspected with Go; package/versionCode and configured expectations match", metadata)
			}
		}
		signers, signerErr := bundle.Signers(m.Bundle)
		if signerErr != nil {
			add("signerCertificate", "failed", signerErr.Error(), nil)
		} else {
			add("signerCertificate", "passed", "Signer fingerprints extracted; this does not verify the JAR signature", map[string]any{"sha256": signers, "cryptographicallyVerified": false})
		}
		if m.UploadCertificateSHA256 == "" {
			add("uploadCertificate", "not_run", "No expected upload certificate SHA-256 configured; signer identity was not compared", nil)
		} else if signerErr != nil || len(signers) != 1 || signers[0] != m.UploadCertificateSHA256 {
			add("uploadCertificate", "failed", "AAB signer does not uniquely match the expected upload certificate", nil)
		} else {
			add("uploadCertificate", "passed", "Signer SHA-256 matches the configured upload certificate; signature integrity is checked separately", nil)
		}
		find := func(value, fallback string) (string, error) {
			if value == "" {
				value = fallback
			}
			return tools.Find(value)
		}
		executable := ""
		prefix := []string{}
		if strings.HasSuffix(m.Preflight.Bundletool, ".jar") {
			if java, e := find(m.Preflight.Java, "java"); e == nil {
				executable = java
				prefix = []string{"-jar", m.Preflight.Bundletool}
			}
		} else {
			executable, _ = find(m.Preflight.Bundletool, "bundletool")
		}
		if executable == "" {
			add("bundleStructure", "not_run", "bundletool/Java unavailable; full bundletool structural validation was not performed", nil)
		} else {
			args := append(prefix, "validate", "--bundle="+m.Bundle)
			out, e := tools.Run(ctx, executable, args...)
			if e != nil && runtimeUnavailable(out) {
				add("bundleStructure", "not_run", "Java runtime unavailable", nil)
			} else if e != nil {
				add("bundleStructure", "failed", "bundletool structural validation failed", nil)
			} else {
				add("bundleStructure", "passed", "bundletool validate completed; this does not guarantee Play policy compliance", nil)
			}
		}
		jarsigner, e := find(m.Preflight.Jarsigner, "jarsigner")
		if e != nil {
			add("signatureIntegrity", "not_run", "jarsigner unavailable; cryptographic JAR verification was not performed", nil)
		} else {
			out, e := tools.Run(ctx, jarsigner, "-J-Duser.language=en", "-J-Duser.country=US", "-verify", m.Bundle)
			lower := strings.ToLower(out)
			if e != nil && runtimeUnavailable(out) {
				add("signatureIntegrity", "not_run", "Java runtime unavailable; JAR signature was not verified", nil)
			} else if e != nil || !strings.Contains(lower, "jar verified.") || strings.Contains(lower, "unsigned entries") || strings.Contains(lower, "jar is unsigned") {
				add("signatureIntegrity", "failed", "JAR signature verification failed or bundle contains unsigned entries", nil)
			} else {
				add("signatureIntegrity", "passed", "JAR integrity verified; public PKI trust is not required for upload certificates", nil)
			}
		}
	}
	if m.Preflight.Required {
		for _, c := range checks {
			if c.Status == "not_run" {
				failed = true
			}
		}
	}
	if ctx.Err() != nil {
		return checks, fault.From(ctx.Err())
	}
	if failed {
		e := fault.New("PREFLIGHT_FAILED", "Preflight failed or a required check could not run; inspect details.checks", 2)
		e.Details = map[string]any{"checks": checks}
		return checks, e
	}
	return checks, nil
}

func runtimeUnavailable(output string) bool {
	text := strings.ToLower(output)
	for _, message := range []string{"unable to locate a java runtime", "no java runtime present", "java_home is not set", "java: not found"} {
		if strings.Contains(text, message) {
			return true
		}
	}
	return false
}
