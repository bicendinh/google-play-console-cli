package releaseflow

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
	"playops/internal/bundle"
	"playops/internal/fault"
)

var numberedImage = regexp.MustCompile(`^([0-9]+)-.+\.(?i:png|jpe?g)$`)

func numberedFiles(dir string) (map[int]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fault.Input("Cannot read image directory: %s", dir)
	}
	files := map[int]string{}
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		match := numberedImage.FindStringSubmatch(entry.Name())
		if match == nil {
			return nil, fault.Input("Image filename must be NN-name.png or NN-name.jpg: %s", entry.Name())
		}
		pos, e := strconv.Atoi(match[1])
		if e != nil || pos < 1 || pos > 8 || files[pos] != "" {
			return nil, fault.Input("Image positions must be unique and between 1 and 8: %s", dir)
		}
		files[pos], e = filepath.Abs(filepath.Join(dir, entry.Name()))
		if e != nil {
			return nil, e
		}
	}
	return files, nil
}
func contiguous(files map[int]string) ([]string, bool) {
	result := []string{}
	for i := 1; i <= len(files); i++ {
		if files[i] == "" {
			return nil, false
		}
		result = append(result, files[i])
	}
	return result, true
}

// InitRelease is entirely local. A partial screenshot set requires all original
// assets in sourceDir/<locale>/NN-name.png; their hashes are checked by plan.
func InitRelease(dir, sourceDir, track, profile, status string, rollout float64) (Manifest, bundle.Metadata, error) {
	var m Manifest
	var metadata bundle.Metadata
	matches, err := filepath.Glob(filepath.Join(dir, "app-release-*.aab"))
	if err != nil || len(matches) != 1 {
		return m, metadata, fault.Input("Release directory must contain exactly one app-release-*.aab")
	}
	path, err := filepath.Abs(matches[0])
	if err != nil {
		return m, metadata, err
	}
	metadata, err = bundle.Inspect(path)
	if err != nil {
		return m, metadata, err
	}
	if metadata.VersionName == "" {
		return m, metadata, fault.Input("AAB versionName is missing or an unresolved resource reference; provide a manifest explicitly")
	}
	m = Manifest{App: metadata.Package, Profile: profile, Bundle: path, VersionCode: metadata.VersionCode, VersionName: metadata.VersionName, MinSDK: metadata.MinSDK, TargetSDK: metadata.TargetSDK, Track: track, Status: status, Rollout: rollout, Name: fmt.Sprintf("%d (%s)", metadata.VersionCode, metadata.VersionName), FlattenAlpha: "auto"}
	m.Review.InReview = "error"
	entries, err := os.ReadDir(filepath.Join(dir, "notes"))
	if err != nil {
		return m, metadata, fault.Input("Release directory requires notes/<locale>.txt")
	}
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if filepath.Ext(entry.Name()) != ".txt" {
			return m, metadata, fault.Input("Notes filename must be <locale>.txt: %s", entry.Name())
		}
		locale := strings.TrimSuffix(entry.Name(), ".txt")
		if err = ValidLocale(locale); err != nil {
			return m, metadata, err
		}
		text, e := os.ReadFile(filepath.Join(dir, "notes", entry.Name()))
		if e != nil {
			return m, metadata, e
		}
		m.Notes = append(m.Notes, Note{Language: locale, Text: strings.TrimRight(strings.TrimPrefix(string(text), "\ufeff"), "\r\n")})
		m.RequiredLocales = append(m.RequiredLocales, locale)
	}
	if len(m.Notes) == 0 {
		return m, metadata, fault.Input("At least one notes/<locale>.txt is required")
	}
	sort.Strings(m.RequiredLocales)
	shotDir := filepath.Join(dir, "shots")
	locales, err := os.ReadDir(shotDir)
	if err != nil && !os.IsNotExist(err) {
		return m, metadata, err
	}
	for _, entry := range locales {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if !entry.IsDir() {
			return m, metadata, fault.Input("shots must contain locale directories")
		}
		locale := entry.Name()
		if err = ValidLocale(locale); err != nil {
			return m, metadata, err
		}
		files, e := numberedFiles(filepath.Join(shotDir, locale))
		if e != nil {
			return m, metadata, e
		}
		if len(files) == 0 {
			return m, metadata, fault.Input("Screenshot set is empty: %s", locale)
		}
		group := ImageChange{Locale: locale, Type: "phoneScreenshots", Mode: "replace"}
		if sourceDir != "" {
			originals, e := numberedFiles(filepath.Join(sourceDir, locale))
			if e != nil {
				return m, metadata, e
			}
			var ok bool
			group.SourceFiles, ok = contiguous(originals)
			if !ok || len(group.SourceFiles) == 0 {
				return m, metadata, fault.Input("Baseline images must start at 01 with no gaps: %s", locale)
			}
			for pos := range files {
				if pos > len(group.SourceFiles) {
					return m, metadata, fault.Input("Replacement position %d exceeds the baseline set for %s", pos, locale)
				}
			}
			group.Replace = files
		} else {
			var ok bool
			group.Files, ok = contiguous(files)
			if !ok {
				return m, metadata, fault.Input("Partial screenshots for %s require --source-dir with every original image in snapshot order", locale)
			}
		}
		m.Images = append(m.Images, group)
	}
	return m, metadata, ValidateManifest(m)
}

func WriteManifest(path string, m Manifest) error {
	if err := CheckOutput(path); err != nil {
		return err
	}
	base, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return err
	}
	rel := func(p string) string {
		if p == "" {
			return p
		}
		r, e := filepath.Rel(base, p)
		if e == nil {
			return r
		}
		return p
	}
	m.Bundle = rel(m.Bundle)
	m.Images = append([]ImageChange(nil), m.Images...)
	for i := range m.Images {
		g := &m.Images[i]
		g.Files = append([]string(nil), g.Files...)
		g.SourceFiles = append([]string(nil), g.SourceFiles...)
		replacements := map[int]string{}
		for pos, p := range g.Replace {
			replacements[pos] = p
		}
		g.Replace = replacements
		for j := range g.Files {
			g.Files[j] = rel(g.Files[j])
		}
		for j := range g.SourceFiles {
			g.SourceFiles[j] = rel(g.SourceFiles[j])
		}
		for pos, p := range g.Replace {
			g.Replace[pos] = rel(p)
		}
	}
	b, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(b)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}
