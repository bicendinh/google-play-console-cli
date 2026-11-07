package bundle_test

import (
	"archive/zip"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"github.com/smallstep/pkcs7"
	"google.golang.org/protobuf/encoding/protowire"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"playops/internal/bundle"
	"playops/internal/testutil"
)

func TestNativeMetadataAndSigner(t *testing.T) {
	path := testutil.Bundle(t)
	m, e := bundle.Inspect(path)
	if e != nil {
		t.Fatal(e)
	}
	if m.Package != "com.example.app" || m.VersionCode != 123 || m.VersionName != "1.2.3" || m.MinSDK != 23 || m.TargetSDK != 36 {
		t.Fatalf("bad metadata %+v", m)
	}
	s, e := bundle.Signers(path)
	if e != nil || len(s) != 1 || len(s[0]) != 64 {
		t.Fatalf("signer %v %v", s, e)
	}
}

func TestCompiledMetadataWithoutRawValues(t *testing.T) {
	attr := func(ns, name string, compiled []byte) []byte {
		b := append(testutil.ProtoBytes(1, []byte(ns)), testutil.ProtoBytes(2, []byte(name))...)
		return testutil.ProtoBytes(4, append(b, testutil.ProtoBytes(6, compiled)...))
	}
	integer := func(v uint64) []byte {
		return testutil.ProtoBytes(7, protowire.AppendVarint(protowire.AppendTag(nil, 6, protowire.VarintType), v))
	}
	str := func(v string) []byte { return testutil.ProtoBytes(2, testutil.ProtoBytes(1, []byte(v))) }
	ns := "http://schemas.android.com/apk/res/android"
	root := testutil.ProtoBytes(3, []byte("manifest"))
	root = append(root, attr("", "package", str("com.example.app"))...)
	root = append(root, attr(ns, "versionCode", integer(123))...)
	root = append(root, attr(ns, "versionName", str("1.2.3"))...)
	path := filepath.Join(t.TempDir(), "compiled.aab")
	f, _ := os.Create(path)
	z := zip.NewWriter(f)
	w, _ := z.Create("base/manifest/AndroidManifest.xml")
	_, _ = w.Write(testutil.ProtoBytes(1, root))
	_ = z.Close()
	_ = f.Close()
	m, e := bundle.Inspect(path)
	if e != nil || m.VersionCode != 123 || m.VersionName != "1.2.3" {
		t.Fatalf("compiled values %+v %v", m, e)
	}
}

func TestSignerSelectionIgnoresCertificateOrder(t *testing.T) {
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	makeCert := func(serial int64) *x509.Certificate {
		template := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "Example"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature}
		der, e := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
		if e != nil {
			t.Fatal(e)
		}
		cert, e := x509.ParseCertificate(der)
		if e != nil {
			t.Fatal(e)
		}
		return cert
	}
	other, signer := makeCert(1), makeCert(2)
	signed, e := pkcs7.NewSignedData([]byte("fixture"))
	if e != nil {
		t.Fatal(e)
	}
	signed.AddCertificate(other)
	if e = signed.AddSigner(signer, key, pkcs7.SignerInfoConfig{}); e != nil {
		t.Fatal(e)
	}
	b, e := signed.Finish()
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "signer.aab")
	f, _ := os.Create(path)
	z := zip.NewWriter(f)
	w, _ := z.Create("META-INF/UPLOAD.EC")
	_, _ = w.Write(b)
	_ = z.Close()
	_ = f.Close()
	got, e := bundle.Signers(path)
	want := sha256.Sum256(signer.Raw)
	if e != nil || len(got) != 1 || got[0] != hex.EncodeToString(want[:]) {
		t.Fatalf("wrong signer %v %v", got, e)
	}
}
func TestRejectMalformedAndDuplicateManifest(t *testing.T) {
	for _, data := range [][]byte{[]byte("<manifest/>"), {0xff}, testutil.ProtoManifest("com.example.app", 0, "1.2.3")} {
		path := filepath.Join(t.TempDir(), "bad.aab")
		f, e := os.Create(path)
		if e != nil {
			t.Fatal(e)
		}
		z := zip.NewWriter(f)
		w, _ := z.Create("base/manifest/AndroidManifest.xml")
		_, _ = w.Write(data)
		_ = z.Close()
		_ = f.Close()
		if _, e = bundle.Inspect(path); e == nil {
			t.Fatal("malformed manifest accepted")
		}
	}
	path := filepath.Join(t.TempDir(), "duplicate.aab")
	f, _ := os.Create(path)
	z := zip.NewWriter(f)
	for i := 0; i < 2; i++ {
		w, _ := z.Create("base/manifest/AndroidManifest.xml")
		_, _ = w.Write(testutil.ProtoManifest("com.example.app", 123, "1.2.3"))
	}
	_ = z.Close()
	_ = f.Close()
	if _, e := bundle.Inspect(path); e == nil {
		t.Fatal("duplicate manifest accepted")
	}
}
