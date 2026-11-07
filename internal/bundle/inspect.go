// Package bundle reads AAB metadata without a Java runtime. It does not verify
// JAR entry digests or cryptographic signatures; jarsigner remains a separate check.
package bundle

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/smallstep/pkcs7"
	"google.golang.org/protobuf/encoding/protowire"
	"playops/internal/fault"
)

const androidNS = "http://schemas.android.com/apk/res/android"

type Metadata struct {
	Package     string `json:"package"`
	VersionCode int64  `json:"versionCode"`
	VersionName string `json:"versionName,omitempty"`
	MinSDK      int64  `json:"minSdk,omitempty"`
	TargetSDK   int64  `json:"targetSdk,omitempty"`
}

// Wire fields follow AOSP tools/aapt2/Resources.proto (XmlNode, XmlElement,
// XmlAttribute and Item). Unknown fields are skipped for forward compatibility.
type field struct {
	number protowire.Number
	kind   protowire.Type
	data   []byte
	value  uint64
}

func fields(b []byte) ([]field, error) {
	var out []field
	for len(b) > 0 {
		n, t, k := protowire.ConsumeTag(b)
		if k < 0 {
			return nil, fmt.Errorf("invalid protobuf tag")
		}
		b = b[k:]
		f := field{number: n, kind: t}
		switch t {
		case protowire.BytesType:
			f.data, k = protowire.ConsumeBytes(b)
		case protowire.VarintType:
			f.value, k = protowire.ConsumeVarint(b)
		default:
			k = protowire.ConsumeFieldValue(n, t, b)
		}
		if k < 0 {
			return nil, fmt.Errorf("invalid protobuf value")
		}
		b = b[k:]
		out = append(out, f)
	}
	return out, nil
}
func bytesField(b []byte, n protowire.Number) ([]byte, error) {
	fs, err := fields(b)
	if err != nil {
		return nil, err
	}
	var result []byte
	found := false
	for _, f := range fs {
		if f.number == n {
			if f.kind != protowire.BytesType || found {
				return nil, fmt.Errorf("invalid/duplicate field %d", n)
			}
			result = f.data
			found = true
		}
	}
	return result, nil
}
func attribute(b []byte) (string, string, string, error) {
	ns, e := bytesField(b, 1)
	if e != nil {
		return "", "", "", e
	}
	name, e := bytesField(b, 2)
	if e != nil {
		return "", "", "", e
	}
	raw, e := bytesField(b, 3)
	if e != nil {
		return "", "", "", e
	}
	compiled, e := bytesField(b, 6)
	if e != nil {
		return "", "", "", e
	}
	value := string(raw)
	if value == "" && len(compiled) > 0 {
		items, e := fields(compiled)
		if e != nil {
			return "", "", "", e
		}
		for _, item := range items {
			if item.number == 2 || item.number == 3 {
				v, e := bytesField(item.data, 1)
				if e != nil {
					return "", "", "", e
				}
				value = string(v)
			}
			if item.number == 7 {
				ps, e := fields(item.data)
				if e != nil {
					return "", "", "", e
				}
				for _, p := range ps {
					if (p.number == 6 || p.number == 7) && p.kind == protowire.VarintType {
						value = strconv.FormatUint(p.value, 10)
					}
				}
			}
		}
	}
	return string(ns), string(name), value, nil
}
func element(node []byte) (string, map[string]string, [][]byte, error) {
	b, e := bytesField(node, 1)
	if e != nil || len(b) == 0 {
		return "", nil, nil, fmt.Errorf("missing XML element")
	}
	fs, e := fields(b)
	if e != nil {
		return "", nil, nil, e
	}
	attrs := map[string]string{}
	var children [][]byte
	name := ""
	for _, f := range fs {
		switch f.number {
		case 3:
			name = string(f.data)
		case 4:
			ns, n, v, e := attribute(f.data)
			if e != nil {
				return "", nil, nil, e
			}
			k := ns + "|" + n
			if _, exists := attrs[k]; exists {
				return "", nil, nil, fmt.Errorf("duplicate XML attribute %s", n)
			}
			attrs[k] = v
		case 5:
			children = append(children, f.data)
		}
	}
	return name, attrs, children, nil
}
func readEntry(z *zip.ReadCloser, name string) ([]byte, error) {
	var found *zip.File
	for _, f := range z.File {
		if f.Name == name {
			if found != nil {
				return nil, fmt.Errorf("duplicate ZIP entry: %s", name)
			}
			found = f
		}
	}
	if found == nil {
		return nil, fmt.Errorf("missing ZIP entry: %s", name)
	}
	const limit = 8 << 20
	if found.UncompressedSize64 > limit {
		return nil, fmt.Errorf("entry exceeds 8 MiB: %s", name)
	}
	r, e := found.Open()
	if e != nil {
		return nil, e
	}
	defer r.Close()
	b, e := io.ReadAll(io.LimitReader(r, limit+1))
	if len(b) > limit {
		return nil, fmt.Errorf("entry exceeds 8 MiB: %s", name)
	}
	return b, e
}
func Inspect(path string) (Metadata, error) {
	var m Metadata
	z, e := zip.OpenReader(path)
	if e != nil {
		return m, fault.Input("Cannot read AAB ZIP: %s", path)
	}
	defer z.Close()
	b, e := readEntry(z, "base/manifest/AndroidManifest.xml")
	if e != nil {
		return m, fault.Input("AAB manifest: %v", e)
	}
	name, a, children, e := element(b)
	if e != nil || name != "manifest" {
		return m, fault.Input("AAB base manifest is not valid AAPT2 protobuf XML")
	}
	m.Package = a["|package"]
	if m.Package == "" {
		return m, fault.Input("AAB manifest package is missing")
	}
	m.VersionCode, e = strconv.ParseInt(a[androidNS+"|versionCode"], 10, 64)
	if e != nil || m.VersionCode <= 0 || m.VersionCode > 2100000000 {
		return m, fault.Input("AAB manifest versionCode is missing or invalid")
	}
	m.VersionName = a[androidNS+"|versionName"]
	if strings.HasPrefix(m.VersionName, "@") {
		m.VersionName = ""
	} // unresolved resource reference, never invent a version name
	seenSDK := false
	for _, child := range children {
		fs, err := fields(child)
		if err != nil {
			return m, fault.Input("Malformed AAB manifest child")
		}
		hasElement, hasText := false, false
		for _, f := range fs {
			hasElement = hasElement || f.number == 1
			hasText = hasText || f.number == 2
		}
		if !hasElement && hasText {
			continue
		}
		n, a, _, err := element(child)
		if err != nil {
			return m, fault.Input("Malformed AAB manifest child: %v", err)
		}
		if n != "uses-sdk" {
			continue
		}
		if seenSDK {
			return m, fault.Input("Duplicate uses-sdk in AAB manifest")
		}
		seenSDK = true
		for key, target := range map[string]*int64{"minSdkVersion": &m.MinSDK, "targetSdkVersion": &m.TargetSDK} {
			v, exists := a[androidNS+"|"+key]
			if !exists {
				continue
			}
			*target, e = strconv.ParseInt(v, 10, 64)
			if e != nil || *target < 1 {
				return m, fault.Input("Cannot resolve AAB %s", key)
			}
		}
	}
	return m, nil
}

// Signers returns the actual signer certificates, not the first certificates in
// each PKCS#7 chain. Multiple signers inside one block are explicitly unsupported.
func Signers(path string) ([]string, error) {
	z, e := zip.OpenReader(path)
	if e != nil {
		return nil, fault.Input("Cannot read AAB signer ZIP")
	}
	defer z.Close()
	set := map[string]bool{}
	for _, f := range z.File {
		n := strings.ToUpper(f.Name)
		if !strings.HasPrefix(n, "META-INF/") || !(strings.HasSuffix(n, ".RSA") || strings.HasSuffix(n, ".EC") || strings.HasSuffix(n, ".DSA")) {
			continue
		}
		b, e := readEntry(z, f.Name)
		if e != nil {
			return nil, fault.Input("AAB signer block: %v", e)
		}
		p, e := pkcs7.Parse(b)
		if e != nil {
			return nil, fault.Input("Invalid PKCS#7 signer block: %s", f.Name)
		}
		cert := p.GetOnlySigner()
		if cert == nil {
			return nil, fault.Input("Signer block requires exactly one identifiable signer: %s", f.Name)
		}
		h := sha256.Sum256(cert.Raw)
		set[hex.EncodeToString(h[:])] = true
	}
	if len(set) == 0 {
		return nil, fault.Input("No AAB signer certificate found")
	}
	result := []string{}
	for h := range set {
		result = append(result, h)
	}
	sort.Strings(result)
	return result, nil
}
