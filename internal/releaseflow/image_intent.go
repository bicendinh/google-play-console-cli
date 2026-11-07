package releaseflow

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
)

var imagePositionPrefix = regexp.MustCompile(`^([0-9]+)[_.-]`)

// Filenames are advisory only. files always declares the complete ordered set;
// positional replacement requires sourceFiles and replace.
func imageIntentWarnings(m Manifest) []Issue {
	var warnings []Issue
	for _, group := range m.Images {
		if group.Mode != "replace" {
			continue
		}
		for i, path := range group.Files {
			match := imagePositionPrefix.FindStringSubmatch(filepath.Base(path))
			if match == nil {
				continue
			}
			n, err := strconv.Atoi(match[1])
			if err != nil || n != i+1 {
				warnings = append(warnings, Issue{Field: "images.files", Locale: group.Locale, File: path, Index: i + 1, Message: fmt.Sprintf("%s is upload position %d, regardless of its filename. files replaces the entire %s set; use sourceFiles plus replace for positional changes", filepath.Base(path), i+1, group.Type)})
			}
		}
	}
	return warnings
}
