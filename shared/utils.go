package shared

import (
	"path/filepath"
	"slices"
	"strings"
)

// ExtractHostname returns a lowercase host without protocol, path, or port for registry matching.
func ExtractHostname(url string) string {

	url, _ = strings.CutPrefix(url, "https://")
	url, _ = strings.CutPrefix(url, "http://")

	if index := strings.Index(url, "/"); index != -1 {
		url = url[:index]
	}

	if index := strings.Index(url, ":"); index != -1 {
		url = url[:index]
	}

	return strings.ToLower(url)
}

// SortFilesByDepth orders files in place by shallowest path first and then alphabetically.
func SortFilesByDepth(files []string) {
	slices.SortFunc(files, func(first, second string) int {
		firstDepth := strings.Count(first, string(filepath.Separator))
		secondDepth := strings.Count(second, string(filepath.Separator))
		if firstDepth != secondDepth {
			return firstDepth - secondDepth
		}
		return strings.Compare(first, second)
	})
}
