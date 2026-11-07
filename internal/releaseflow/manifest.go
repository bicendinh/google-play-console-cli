package releaseflow

import (
	"encoding/hex"
	"math"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/language"
	"playops/internal/config"
	"playops/internal/fault"
	"playops/internal/publisher"
)

func LoadManifest(path, profile string) (Manifest, error) {
	m, err := ReadManifest(path, profile)
	if err != nil {
		return m, err
	}
	return m, ValidateManifest(m)
}

// ReadManifest resolves local references without stopping at the first semantic
// issue. Preflight uses it to report all independent input errors together.
func ReadManifest(path, profile string) (Manifest, error) {
	var m Manifest
	if err := Read(path, &m); err != nil {
		return m, err
	}
	if m.Profile == "" {
		m.Profile = profile
	}
	if m.Status == "" {
		m.Status = "draft"
	}
	if m.Review.InReview == "" {
		m.Review.InReview = "error"
	}
	base, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return m, err
	}
	abs := func(p string) string {
		if p == "" || filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(base, p)
	}
	m.Bundle = abs(m.Bundle)
	for i := range m.Listings {
		v := &m.Listings[i]
		if v.File != "" {
			if v.Title != nil || v.ShortDescription != nil || v.FullDescription != nil || v.Video != nil {
				return m, fault.Input("Use either a listing file or inline fields for %s", v.Locale)
			}
			v.File = abs(v.File)
			var patch ListingChange
			if err = Read(v.File, &patch); err != nil {
				return m, err
			}
			if patch.File != "" || (patch.Locale != "" && patch.Locale != v.Locale) {
				return m, fault.Input("Listing file locale must match and cannot reference another file")
			}
			v.Title = patch.Title
			v.ShortDescription = patch.ShortDescription
			v.FullDescription = patch.FullDescription
			v.Video = patch.Video
		}
	}
	for i := range m.Images {
		v := &m.Images[i]
		if v.Mode == "" {
			v.Mode = "replace"
		}
		for j := range v.Files {
			v.Files[j] = abs(v.Files[j])
		}
		for j := range v.SourceFiles {
			v.SourceFiles[j] = abs(v.SourceFiles[j])
		}
		for position, p := range v.Replace {
			v.Replace[position] = abs(p)
		}
	}
	for i := range m.Symbols {
		m.Symbols[i].File = abs(m.Symbols[i].File)
	}
	if strings.ContainsAny(m.Preflight.Bundletool, "/\\") || strings.HasSuffix(m.Preflight.Bundletool, ".jar") {
		m.Preflight.Bundletool = abs(m.Preflight.Bundletool)
	}
	for _, p := range []*string{&m.Preflight.Java, &m.Preflight.Jarsigner, &m.Preflight.Keytool} {
		if strings.ContainsAny(*p, "/\\") {
			*p = abs(*p)
		}
	}
	m.UploadCertificateSHA256 = strings.ToLower(strings.ReplaceAll(m.UploadCertificateSHA256, ":", ""))
	return m, nil
}
func ValidLocale(locale string) error {
	tag, err := language.Parse(locale)
	if err != nil || tag == language.Und || locale == "" || strings.ContainsAny(locale, "/\\ \t\r\n") {
		return fault.Input("Invalid locale: %s", locale)
	}
	return nil
}
func validateManifestFirst(m Manifest) error {
	if err := AlphaPolicy(m.FlattenAlpha); err != nil {
		return err
	}
	if m.MinSDK < 0 || m.TargetSDK < 0 {
		return fault.Input("minSdk and targetSdk must be positive when specified")
	}
	if math.IsNaN(m.Rollout) || math.IsInf(m.Rollout, 0) {
		return fault.Input("rollout must be a finite percentage")
	}
	if err := config.ValidateApp(m.App); err != nil {
		return err
	}
	if err := config.ValidateProfile(m.Profile); err != nil {
		return err
	}
	if m.Track == "" || strings.ContainsAny(m.Track, "/\\ \t\r\n") {
		return fault.Input("Manifest requires a track identifier")
	}
	if m.FromTrack != "" && (m.FromTrack == m.Track || strings.ContainsAny(m.FromTrack, "/\\ \t\r\n") || m.Bundle != "" || m.VersionCode <= 0) {
		return fault.Input("fromTrack requires a different source track, versionCode and no bundle re-upload")
	}
	if err := m.Review.Validate(); err != nil {
		return err
	}
	if m.Status != "draft" && m.Status != "inProgress" && m.Status != "completed" {
		return fault.Input("status must be draft, inProgress or completed")
	}
	if m.Bundle != "" {
		if strings.TrimSpace(m.Name) == "" {
			return fault.Input("Bundle releases require an explicit name for deterministic readback")
		}
		if m.VersionCode <= 0 {
			return fault.Input("Manifest requires the expected positive AAB versionCode")
		}
		if err := publisher.ValidateBundle(m.Bundle); err != nil {
			return err
		}
	} else if m.Status != "draft" && m.VersionCode <= 0 {
		return fault.Input("Existing release transitions require versionCode")
	} else if m.Status == "draft" && m.FromTrack == "" && (len(m.Notes) > 0 || m.Name != "") {
		return fault.Input("Existing release transitions require explicit inProgress/completed status; omit release fields for metadata-only changes")
	}
	if m.Status == "draft" && m.Rollout != 0 {
		return fault.Input("A draft cannot have a rollout percentage")
	}
	if m.Status != "draft" {
		if m.Rollout < 1 || m.Rollout > 100 {
			return fault.Input("Field rollout is required: set rollout: 100 for completed, or rollout: 1–99 for inProgress")
		}
		if (m.Status == "completed") != (m.Rollout == 100) {
			return fault.Input("Use completed with rollout 100, or inProgress with rollout below 100")
		}
	}
	if m.UploadCertificateSHA256 != "" {
		b, err := hex.DecodeString(m.UploadCertificateSHA256)
		if err != nil || len(b) != 32 {
			return fault.Input("uploadCertificateSha256 must be a SHA-256 certificate fingerprint")
		}
	}
	seen := map[string]bool{}
	for _, locale := range m.RequiredLocales {
		if err := ValidLocale(locale); err != nil {
			return err
		}
		if seen[locale] {
			return fault.Input("Duplicate required locale: %s", locale)
		}
		seen[locale] = true
	}
	notes := map[string]bool{}
	for _, n := range m.Notes {
		if err := ValidLocale(n.Language); err != nil {
			return err
		}
		if notes[n.Language] || strings.TrimSpace(n.Text) == "" || utf8.RuneCountInString(n.Text) > 500 {
			return fault.Input("Each note language must be unique with 1–500 characters")
		}
		notes[n.Language] = true
	}
	if m.Bundle != "" {
		for _, locale := range m.RequiredLocales {
			if !notes[locale] {
				return fault.Input("Missing release notes for required locale %s", locale)
			}
		}
	}
	seen = map[string]bool{}
	for _, v := range m.Listings {
		if err := ValidLocale(v.Locale); err != nil {
			return err
		}
		if seen[v.Locale] {
			return fault.Input("Duplicate listing locale: %s", v.Locale)
		}
		seen[v.Locale] = true
		if v.Title == nil && v.ShortDescription == nil && v.FullDescription == nil && v.Video == nil {
			return fault.Input("Listing patch is empty: %s", v.Locale)
		}
		if err := validateText(v); err != nil {
			return err
		}
	}
	seen = map[string]bool{}
	for _, v := range m.Images {
		if err := ValidLocale(v.Locale); err != nil {
			return err
		}
		if !ImageType(v.Type) {
			return fault.Input("Unsupported image type: %s", v.Type)
		}
		if seen[key(v.Locale, v.Type)] {
			return fault.Input("Duplicate image group")
		}
		seen[key(v.Locale, v.Type)] = true
		if v.Mode != "replace" && v.Mode != "preserve" && v.Mode != "inherit" {
			return fault.Input("Image mode must be replace, preserve or inherit")
		}
		if v.Mode == "replace" {
			if v.CopyFrom != "" {
				if v.CopyFrom == v.Locale || len(v.Files) > 0 || len(v.SourceFiles) > 0 || len(v.Replace) > 0 || v.InheritFrom != "" {
					return fault.Input("copyFrom requires a different locale and cannot also specify files, replacements or inheritance")
				}
				if err := ValidLocale(v.CopyFrom); err != nil {
					return err
				}
				continue
			}
			if (len(v.Files) == 0) == (len(v.Replace) == 0) {
				return fault.Input("Image replacement requires either final files, or sourceFiles plus positional replace")
			}
			if len(v.Replace) > 0 && len(v.SourceFiles) == 0 {
				return fault.Input("Positional replacements require all baseline sourceFiles")
			}
			if len(v.Files) > 0 && len(v.SourceFiles) > 0 {
				return fault.Input("sourceFiles is only used with positional replace")
			}
			if v.InheritFrom != "" {
				return fault.Input("Replacement cannot also inherit images")
			}
		} else if len(v.Files) > 0 || len(v.SourceFiles) > 0 || len(v.Replace) > 0 || v.CopyFrom != "" {
			return fault.Input("Preserve/inherit groups cannot upload files")
		}
		if v.Mode == "inherit" {
			if !v.InheritanceConfirmed || v.InheritFrom == v.Locale || v.InheritFrom == "" {
				return fault.Input("Inheritance requires a different inheritFrom locale and explicit inheritanceConfirmed: true")
			}
			if err := ValidLocale(v.InheritFrom); err != nil {
				return err
			}
		}
	}
	for _, kind := range m.RequiredImageTypes {
		if !ImageType(kind) {
			return fault.Input("Unsupported required image type")
		}
	}
	seen = map[string]bool{}
	for _, s := range m.Symbols {
		if s.Type != "nativeCode" && s.Type != "proguard" {
			return fault.Input("Symbol type must be nativeCode or proguard")
		}
		if seen[s.Type] {
			return fault.Input("Duplicate symbol type")
		}
		seen[s.Type] = true
		if s.File == "" || m.VersionCode <= 0 {
			return fault.Input("Symbols require a file and versionCode")
		}
	}
	return nil
}

type Issue struct {
	Field   string `json:"field"`
	Locale  string `json:"locale,omitempty"`
	File    string `json:"file,omitempty"`
	Index   int    `json:"index,omitempty"`
	Message string `json:"message"`
}

func inputIssues(m Manifest) []Issue {
	issues := []Issue{}
	if err := validateManifestFirst(m); err != nil {
		issues = append(issues, Issue{Field: "manifest", Message: err.Error()})
	}
	seen := map[string]bool{}
	for _, n := range m.Notes {
		if err := ValidLocale(n.Language); err != nil {
			issues = append(issues, Issue{Field: "notes", Locale: n.Language, Message: err.Error()})
		}
		if seen[n.Language] || strings.TrimSpace(n.Text) == "" || utf8.RuneCountInString(n.Text) > 500 {
			issues = append(issues, Issue{Field: "notes", Locale: n.Language, Message: "Release notes require a unique locale and 1–500 characters"})
		}
		seen[n.Language] = true
	}
	for _, l := range m.Listings {
		if err := ValidLocale(l.Locale); err != nil {
			issues = append(issues, Issue{Field: "listings.locale", Locale: l.Locale, File: l.File, Message: err.Error()})
		}
		if err := validateText(l); err != nil {
			issues = append(issues, Issue{Field: "listings", Locale: l.Locale, File: l.File, Message: err.Error()})
		}
	}
	// Validate every image/symbol group independently so one malformed locale
	// does not hide errors in the remaining release assets.
	for _, g := range m.Images {
		probe := Manifest{App: "com.example.app", Profile: "default", Track: "internal", Status: "draft", Images: []ImageChange{g}}
		if err := validateManifestFirst(probe); err != nil {
			issues = append(issues, Issue{Field: "images", Locale: g.Locale, Message: err.Error()})
		}
	}
	for _, symbol := range m.Symbols {
		probe := Manifest{App: "com.example.app", Profile: "default", Track: "internal", Status: "draft", VersionCode: m.VersionCode, Symbols: []Symbol{symbol}}
		if err := validateManifestFirst(probe); err != nil {
			issues = append(issues, Issue{Field: "symbols", File: symbol.File, Message: err.Error()})
		}
	}
	if _, err := expandedImages(m.Images); err != nil {
		issues = append(issues, Issue{Field: "images.copyFrom", Message: err.Error()})
	}
	return issues
}
func issuesError(issues []Issue) error {
	if len(issues) == 0 {
		return nil
	}
	e := fault.Input("%d input issue(s); inspect details.issues: %s", len(issues), issues[0].Message)
	e.Details = map[string]any{"issues": issues}
	return e
}
func ValidateManifest(m Manifest) error { return issuesError(inputIssues(m)) }

// expandedImages resolves copyFrom against the final local source set, not
// remote inheritance. A copy always produces its own independently uploaded set.
func expandedImages(groups []ImageChange) ([]ImageChange, error) {
	byKey := map[string]ImageChange{}
	for _, g := range groups {
		byKey[key(g.Locale, g.Type)] = g
	}
	resolved := map[string]ImageChange{}
	visiting := map[string]bool{}
	var resolve func(string) (ImageChange, error)
	resolve = func(k string) (ImageChange, error) {
		if g, ok := resolved[k]; ok {
			return g, nil
		}
		if visiting[k] {
			return ImageChange{}, fault.Input("copyFrom cycle at %s", k)
		}
		g, ok := byKey[k]
		if !ok {
			return g, fault.Input("copyFrom source group missing: %s", k)
		}
		visiting[k] = true
		if g.CopyFrom != "" {
			source, e := resolve(key(g.CopyFrom, g.Type))
			if e != nil {
				return g, e
			}
			if source.Mode != "replace" {
				return g, fault.Input("copyFrom source must declare a replacement file set: %s", k)
			}
			files := append([]string(nil), source.Files...)
			if len(source.Replace) > 0 {
				files = append([]string(nil), source.SourceFiles...)
				for pos, path := range source.Replace {
					if pos < 1 || pos > len(files) {
						return g, fault.Input("copyFrom source replacement position %d is out of range", pos)
					}
					files[pos-1] = path
				}
			}
			if len(files) == 0 {
				return g, fault.Input("copyFrom source has no files: %s", k)
			}
			g.Files = files
			g.CopyFrom = ""
		}
		visiting[k] = false
		resolved[k] = g
		return g, nil
	}
	out := []ImageChange{}
	for _, g := range groups {
		v, e := resolve(key(g.Locale, g.Type))
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, nil
}

func (m Manifest) ChangesTrack() bool {
	return m.Bundle != "" || m.FromTrack != "" || m.Status != "draft"
}
func validateText(v ListingChange) error {
	for _, f := range []struct {
		name  string
		value *string
		limit int
	}{{"title", v.Title, 30}, {"shortDescription", v.ShortDescription, 80}, {"fullDescription", v.FullDescription, 4000}} {
		if f.value != nil && (strings.TrimSpace(*f.value) == "" || utf8.RuneCountInString(*f.value) > f.limit) {
			return fault.Input("%s for %s requires 1–%d characters", f.name, v.Locale, f.limit)
		}
	}
	if v.Video != nil && *v.Video != "" && !regexp.MustCompile(`^https://(?:www\.)?(?:youtube\.com/|youtu\.be/)`).MatchString(*v.Video) {
		return fault.Input("video must be empty or an HTTPS YouTube URL")
	}
	return nil
}
func ImageType(kind string) bool {
	switch kind {
	case "phoneScreenshots", "sevenInchScreenshots", "tenInchScreenshots", "tvScreenshots", "wearScreenshots", "featureGraphic", "icon", "tvBanner":
		return true
	}
	return false
}
