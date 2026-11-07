// Package testutil supplies HTTP fixtures; it never contacts Google.
package testutil

import (
	"archive/zip"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"github.com/smallstep/pkcs7"
	"google.golang.org/protobuf/encoding/protowire"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"playops/internal/googleapi"
)

type rewrite struct {
	target *url.URL
	base   http.RoundTripper
}

func (r rewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	copy := req.Clone(req.Context())
	u := *req.URL
	u.Scheme = r.target.Scheme
	u.Host = r.target.Host
	copy.URL = &u
	return r.base.RoundTrip(copy)
}
func Google(t *testing.T, handler http.HandlerFunc) *googleapi.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	target, _ := url.Parse(server.URL)
	client, err := googleapi.WithHTTP(context.Background(), &http.Client{Transport: rewrite{target: target, base: server.Client().Transport}})
	if err != nil {
		t.Fatal(err)
	}
	return client
}
func Bundle(t *testing.T) string {
	return BundleWithMetadata(t, "com.example.app", 123, "1.2.3")
}
func ProtoBytes(n protowire.Number, b []byte) []byte {
	return protowire.AppendBytes(protowire.AppendTag(nil, n, protowire.BytesType), b)
}
func ProtoManifest(app string, code int64, version string) []byte {
	attribute := func(ns, name, value string) []byte {
		b := append(ProtoBytes(1, []byte(ns)), ProtoBytes(2, []byte(name))...)
		return ProtoBytes(4, append(b, ProtoBytes(3, []byte(value))...))
	}
	ns := "http://schemas.android.com/apk/res/android"
	root := ProtoBytes(3, []byte("manifest"))
	root = append(root, attribute("", "package", app)...)
	root = append(root, attribute(ns, "versionCode", strconv.FormatInt(code, 10))...)
	root = append(root, attribute(ns, "versionName", version)...)
	sdk := ProtoBytes(3, []byte("uses-sdk"))
	sdk = append(sdk, attribute(ns, "minSdkVersion", "23")...)
	sdk = append(sdk, attribute(ns, "targetSdkVersion", "36")...)
	root = append(root, ProtoBytes(5, ProtoBytes(1, sdk))...)
	return ProtoBytes(1, root)
}
func BundleWithMetadata(t *testing.T, app string, code int64, version string) string {
	t.Helper()
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Example upload test certificate"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature}
	der, e := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	cert, e := x509.ParseCertificate(der)
	if e != nil {
		t.Fatal(e)
	}
	signed, e := pkcs7.NewSignedData([]byte("test signature file"))
	if e != nil {
		t.Fatal(e)
	}
	if e = signed.AddSigner(cert, key, pkcs7.SignerInfoConfig{}); e != nil {
		t.Fatal(e)
	}
	signed.Detach()
	signature, e := signed.Finish()
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "app.aab")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	for name, data := range map[string][]byte{"BundleConfig.pb": {0}, "base/manifest/AndroidManifest.xml": ProtoManifest(app, code, version), "META-INF/UPLOAD.SF": []byte("test signature file"), "META-INF/UPLOAD.EC": signature} {
		w, e := z.Create(name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = w.Write(data); e != nil {
			t.Fatal(e)
		}
	}
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
