package publisher

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"

	"playops/internal/fault"
)

// ValidateBundle checks AAB structure and the presence of JAR signature files.
// Google remains authoritative for cryptographic signature and package checks.
func ValidateBundle(path string) error {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return fault.Input("--file must point to a readable AAB file")
	}
	if !strings.EqualFold(filepath.Ext(path), ".aab") {
		return fault.Input("--file must have the .aab extension")
	}
	z, err := zip.OpenReader(path)
	if err != nil {
		return fault.Input("AAB must be a valid ZIP archive")
	}
	defer z.Close()
	manifest, config, sf, sig := false, false, false, false
	for _, f := range z.File {
		name := strings.ToUpper(f.Name)
		if f.Name == "base/manifest/AndroidManifest.xml" {
			manifest = true
		}
		if f.Name == "BundleConfig.pb" {
			config = true
		}
		if strings.HasPrefix(name, "META-INF/") {
			if strings.HasSuffix(name, ".SF") {
				sf = true
			}
			if strings.HasSuffix(name, ".RSA") || strings.HasSuffix(name, ".DSA") || strings.HasSuffix(name, ".EC") {
				sig = true
			}
		}
	}
	if !manifest || !config || !sf || !sig {
		return fault.Input("AAB must include bundle configuration, base manifest and JAR signature files; build and sign it before uploading")
	}
	return nil
}
