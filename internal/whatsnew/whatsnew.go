// Package whatsnew selects and formats the release notes shown once in the TUI
// after an update.
package whatsnew

import (
	"fmt"
	"sort"

	"github.com/Masterminds/semver/v3"
)

const (
	// Unreleased is the Entries key for lines that wait for the next release.
	Unreleased = "unreleased"
	// MaxLineWidth keeps a line on one row of an 80-column terminal.
	MaxLineWidth = 60
	// MaxLines is the largest block the home screen shows.
	MaxLines = 3
	// ReleasesURL is where the overflow line sends the reader.
	ReleasesURL = "https://github.com/AdaDigitalAgency/wp-stage-sync/releases"
)

// Release is the notes for one version.
type Release struct {
	Version string
	Items   []string
}

// release returns v without a pre-release or metadata part.
func release(v string) (*semver.Version, error) {
	parsed, err := semver.NewVersion(v)
	if err != nil {
		return nil, err
	}
	stripped := semver.New(parsed.Major(), parsed.Minor(), parsed.Patch(), "", "")
	return stripped, nil
}

// IsRelease reports whether v is a real version, not a build such as "dev".
func IsRelease(v string) bool {
	_, err := semver.NewVersion(v)
	return err == nil
}

// For returns the releases to show, newest first: each one after seen, up to
// current. With no seen version, only current is shown. A current version that
// does not parse, such as "dev", shows nothing.
func For(seen, current string, entries map[string][]string) []Release {
	now, err := release(current)
	if err != nil {
		return nil
	}
	var last *semver.Version
	if seen != "" {
		if last, err = release(seen); err != nil {
			return nil
		}
	}

	type candidate struct {
		version *semver.Version
		release Release
	}
	var picked []candidate
	for key, items := range entries {
		v, err := release(key)
		if err != nil || v.GreaterThan(now) || len(items) == 0 {
			continue
		}
		if last == nil && !v.Equal(now) || last != nil && !v.GreaterThan(last) {
			continue
		}
		picked = append(picked, candidate{v, Release{Version: key, Items: items}})
	}
	sort.Slice(picked, func(i, j int) bool { return picked[i].version.GreaterThan(picked[j].version) })

	out := make([]Release, 0, len(picked))
	for _, c := range picked {
		out = append(out, c.release)
	}
	return out
}

// Later returns the higher of two versions, so a downgrade followed by an
// upgrade does not repeat lines. A value that does not parse loses.
func Later(a, b string) string {
	va, errA := semver.NewVersion(a)
	vb, errB := semver.NewVersion(b)
	switch {
	case errA != nil:
		return b
	case errB != nil:
		return a
	case vb.GreaterThan(va):
		return b
	}
	return a
}

// Block is the text to draw: a heading and up to MaxLines lines.
type Block struct {
	Heading string
	Lines   []string
}

// Fit builds a block for the rows available. It returns false when not even
// one item fits, so the caller can keep the news for a taller terminal.
func Fit(releases []Release, spare int) (Block, bool) {
	if len(releases) == 0 {
		return Block{}, false
	}
	const headingRows = 2 // the heading and a blank line above it
	room := min(spare-headingRows, MaxLines)
	if room < 1 {
		return Block{}, false
	}

	var lines []string
	for _, r := range releases {
		for _, item := range r.Items {
			if len(releases) > 1 {
				item = r.Version + ": " + item
			}
			lines = append(lines, item)
		}
	}

	block := Block{Heading: "What's new"}
	if len(releases) == 1 {
		block.Heading = "What's new in " + releases[0].Version
	}
	if len(lines) <= room {
		block.Lines = lines
		return block, true
	}
	if room == 1 {
		block.Lines = []string{fmt.Sprintf("%d updates: %s", len(lines), ReleasesURL)}
		return block, true
	}
	shown := lines[:room-1]
	block.Lines = append(append([]string{}, shown...),
		fmt.Sprintf("and %d more: %s", len(lines)-len(shown), ReleasesURL))
	return block, true
}
