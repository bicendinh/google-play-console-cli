package releaseflow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"os/exec"
	"strconv"
	"strings"

	"playops/internal/fault"
)

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
	checks := []Check{{Name: "manifest", Status: "passed", Message: "Local field, locale and asset validation completed; no server validation"}}
	if m.Bundle == "" {
		return checks, nil
	}
	failed := false
	add := func(name, status, message string) {
		checks = append(checks, Check{Name: name, Status: status, Message: message})
		if status == "failed" {
			failed = true
		}
	}
	find := func(value, fallback string) (string, error) {
		if value == "" {
			value = fallback
		}
		return tools.Find(value)
	}
	var executable string
	prefix := []string{}
	if strings.HasSuffix(m.Preflight.Bundletool, ".jar") {
		java, e := find(m.Preflight.Java, "java")
		if e == nil {
			executable = java
			prefix = []string{"-jar", m.Preflight.Bundletool}
		}
	} else {
		executable, _ = find(m.Preflight.Bundletool, "bundletool")
	}
	if executable == "" {
		add("bundleMetadata", "not_run", "bundletool/Java unavailable; package, versionCode and targetSdk were not inspected")
	} else {
		run := func(args ...string) (string, error) {
			all := append(append([]string{}, prefix...), args...)
			return tools.Run(ctx, executable, all...)
		}
		validationOutput, err := run("validate", "--bundle="+m.Bundle)
		pkg, e1 := run("dump", "manifest", "--bundle="+m.Bundle, "--xpath=/manifest/@package")
		version, e2 := run("dump", "manifest", "--bundle="+m.Bundle, "--xpath=/manifest/@android:versionCode")
		sdk, e3 := run("dump", "manifest", "--bundle="+m.Bundle, "--xpath=/manifest/uses-sdk/@android:targetSdkVersion")
		code, e4 := strconv.ParseInt(strings.TrimSpace(version), 10, 64)
		target, e5 := strconv.Atoi(strings.TrimSpace(sdk))
		if err != nil && runtimeUnavailable(validationOutput) {
			add("bundleMetadata", "not_run", "Java runtime unavailable; bundle metadata was not inspected")
		} else if err != nil || e1 != nil || e2 != nil || e3 != nil || e4 != nil || e5 != nil || strings.TrimSpace(pkg) != m.App || code != m.VersionCode || target <= 0 {
			add("bundleMetadata", "failed", "Bundle validation failed or package/versionCode/targetSdk differs from manifest")
		} else {
			add("bundleMetadata", "passed", "Package/versionCode verified; targetSdk="+strconv.Itoa(target)+" (not a guarantee of current Play policy compliance)")
		}
	}
	jarsigner, err := find(m.Preflight.Jarsigner, "jarsigner")
	if err != nil {
		add("signatureIntegrity", "not_run", "jarsigner unavailable; cryptographic signature verification was not performed")
	} else {
		// Summary output keeps verification usable for bundles with very many
		// entries; verbose listings can hide the final verdict beyond output caps.
		out, e := tools.Run(ctx, jarsigner, "-J-Duser.language=en", "-J-Duser.country=US", "-verify", m.Bundle)
		lower := strings.ToLower(out)
		if e != nil && runtimeUnavailable(out) {
			add("signatureIntegrity", "not_run", "Java runtime unavailable; JAR signature was not verified")
		} else if e != nil || !strings.Contains(lower, "jar verified.") || strings.Contains(lower, "unsigned entries") || strings.Contains(lower, "jar is unsigned") {
			add("signatureIntegrity", "failed", "JAR signature verification failed or bundle contains unsigned entries")
		} else {
			add("signatureIntegrity", "passed", "JAR integrity verified; self-signed upload certificates do not require a public PKI trust chain")
		}
	}
	if m.UploadCertificateSHA256 == "" {
		add("uploadCertificate", "not_run", "No expected upload certificate SHA-256 configured; signer identity was not compared")
	} else {
		keytool, e := find(m.Preflight.Keytool, "keytool")
		if e != nil {
			add("uploadCertificate", "not_run", "keytool unavailable; expected upload certificate was not verified")
		} else {
			out, e := tools.Run(ctx, keytool, "-J-Duser.language=en", "-printcert", "-jarfile", m.Bundle, "-rfc")
			var cert *x509.Certificate
			block, _ := pem.Decode([]byte(out))
			if block != nil {
				cert, _ = x509.ParseCertificate(block.Bytes)
			}
			if e != nil && runtimeUnavailable(out) {
				add("uploadCertificate", "not_run", "Java runtime unavailable; upload certificate was not inspected")
			} else if e != nil || cert == nil {
				add("uploadCertificate", "failed", "Cannot inspect bundle signer certificate")
			} else {
				h := sha256.Sum256(cert.Raw)
				if hex.EncodeToString(h[:]) != m.UploadCertificateSHA256 {
					add("uploadCertificate", "failed", "Bundle signer does not match the expected upload certificate")
				} else {
					add("uploadCertificate", "passed", "Bundle upload signer SHA-256 matches the configured certificate")
				}
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
	if m.UploadCertificateSHA256 != "" {
		for _, c := range checks {
			if c.Name == "uploadCertificate" && c.Status != "passed" {
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
