package releaseflow

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"strings"
	"testing"
	"time"
)

type fakeTools struct {
	missing, invalidSigner, wrongPackage bool
	cert                                 string
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
	case "keytool":
		return f.cert, nil
	case "bundletool":
		all := strings.Join(args, " ")
		switch {
		case strings.Contains(all, "@package"):
			if f.wrongPackage {
				return "com.other.app", nil
			}
			return "com.example.app", nil
		case strings.Contains(all, "@android:versionCode"):
			return "123", nil
		case strings.Contains(all, "targetSdkVersion"):
			return "36", nil
		}
		return "", nil
	}
	return "", errors.New("unexpected tool")
}
func TestPreflightCertificateAndRequiredChecks(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Upload"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(der)
	m := Manifest{App: "com.example.app", Bundle: "app.aab", VersionCode: 123, UploadCertificateSHA256: hex.EncodeToString(h[:]), Preflight: PreflightOptions{Required: true}}
	tools := fakeTools{cert: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))}
	checks, err := preflight(context.Background(), m, tools)
	if err != nil {
		t.Fatalf("%+v %v", checks, err)
	}
	for _, c := range checks {
		if c.Status != "passed" {
			t.Fatalf("unverified: %+v", c)
		}
	}
	for _, bad := range []fakeTools{{missing: true}, {invalidSigner: true, cert: tools.cert}, {wrongPackage: true, cert: tools.cert}} {
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
	if checks[1].Status != "not_run" {
		t.Fatal("missing tool reported as passed")
	}
}
