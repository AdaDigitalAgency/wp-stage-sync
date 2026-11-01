// Package release holds the pure logic of the release tool: text changes to the
// changelog and release notes, and the plan for shipping a release commit.
package release

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/Masterminds/semver/v3"
)

const unreleasedHeading = "## [Unreleased]"

var (
	sectionRe    = regexp.MustCompile(`(?m)^## \[`)
	unreleasedRe = regexp.MustCompile(`(?m)^\t"unreleased":`)
)

// Bump returns the next version for size: patch, minor or major.
func Bump(current, size string) (string, error) {
	v, err := semver.NewVersion(strings.TrimSpace(current))
	if err != nil {
		return "", fmt.Errorf("current version %q: %w", current, err)
	}
	var next semver.Version
	switch size {
	case "patch":
		next = v.IncPatch()
	case "minor":
		next = v.IncMinor()
	case "major":
		next = v.IncMajor()
	default:
		return "", fmt.Errorf("unknown release size %q: use patch, minor or major", size)
	}
	return next.String(), nil
}

// sectionBody returns the text under heading, up to the next section.
func sectionBody(changelog, heading string) (string, bool) {
	start := strings.Index(changelog, heading+"\n")
	if start < 0 {
		return "", false
	}
	rest := changelog[start+len(heading)+1:]
	if loc := sectionRe.FindStringIndex(rest); loc != nil {
		rest = rest[:loc[0]]
	}
	return strings.TrimSpace(rest), true
}

// UnreleasedNotes returns the changelog text under [Unreleased]. It is an error
// when that section is missing or empty, because there is nothing to release.
func UnreleasedNotes(changelog string) (string, error) {
	body, ok := sectionBody(changelog, unreleasedHeading)
	if !ok {
		return "", fmt.Errorf("CHANGELOG.md has no %q section", unreleasedHeading)
	}
	if body == "" {
		return "", fmt.Errorf("the %q section of CHANGELOG.md is empty", unreleasedHeading)
	}
	return body, nil
}

// CutChangelog turns [Unreleased] into a dated version section and leaves a
// fresh, empty [Unreleased] above it.
func CutChangelog(changelog, version, date string) (string, error) {
	if _, err := UnreleasedNotes(changelog); err != nil {
		return "", err
	}
	heading := fmt.Sprintf("## [%s]", version)
	if strings.Contains(changelog, heading) {
		return "", fmt.Errorf("CHANGELOG.md already has a section for %s", version)
	}
	replacement := fmt.Sprintf("%s\n\n%s - %s", unreleasedHeading, heading, date)
	return strings.Replace(changelog, unreleasedHeading, replacement, 1), nil
}

// VersionNotes returns the changelog text for one released version.
func VersionNotes(changelog, version string) (string, error) {
	re := regexp.MustCompile(`(?m)^## \[` + regexp.QuoteMeta(version) + `\][^\n]*$`)
	heading := re.FindString(changelog)
	if heading == "" {
		return "", fmt.Errorf("CHANGELOG.md has no section for %s", version)
	}
	body, _ := sectionBody(changelog, heading)
	if body == "" {
		return "", fmt.Errorf("the section for %s in CHANGELOG.md is empty", version)
	}
	return body, nil
}

// CutWhatsNew renames the "unreleased" key of entries.go to version. It reports
// false when there is no such key. A second key for version would make the Go
// file fail to compile, so that case is an error here.
func CutWhatsNew(source, version string) (string, bool, error) {
	if !unreleasedRe.MatchString(source) {
		return source, false, nil
	}
	key := fmt.Sprintf("\t%q:", version)
	if strings.Contains(source, key) {
		return "", false, fmt.Errorf("entries.go already has a key for %s", version)
	}
	return unreleasedRe.ReplaceAllLiteralString(source, key), true, nil
}
