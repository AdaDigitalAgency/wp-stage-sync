package whatsnew

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

var sample = map[string][]string{
	"unreleased": {"not yet"},
	"0.9.0":      {"future"},
	"0.7.0":      {"seven a", "seven b"},
	"0.6.1":      {"six one"},
	"0.6.0":      {"six"},
	"0.5.0":      {},
}

func versions(rs []Release) []string {
	out := []string{}
	for _, r := range rs {
		out = append(out, r.Version)
	}
	return out
}

func TestFor(t *testing.T) {
	cases := []struct {
		name, seen, current string
		want                []string
	}{
		{"after an update, newest first", "0.6.0", "0.7.0", []string{"0.7.0", "0.6.1"}},
		{"never seen shows current only", "", "0.7.0", []string{"0.7.0"}},
		{"already seen", "0.7.0", "0.7.0", []string{}},
		{"future entries wait", "0.6.1", "0.8.0", []string{"0.7.0"}},
		{"pre-release counts as its release", "0.6.1", "0.7.0-dev", []string{"0.7.0"}},
		{"no entry for this version", "", "0.8.0", []string{}},
		{"dev build shows nothing", "0.6.0", "dev", []string{}},
		{"seen is higher after a downgrade", "0.7.0", "0.6.1", []string{}},
		{"unparsable seen shows nothing", "x", "0.7.0", []string{}},
	}
	for _, c := range cases {
		if got := versions(For(c.seen, c.current, sample)); !slices.Equal(got, c.want) {
			t.Errorf("%s: For(%q, %q) = %v, want %v", c.name, c.seen, c.current, got, c.want)
		}
	}
}

func TestLater(t *testing.T) {
	cases := [][3]string{
		{"0.7.0", "0.6.0", "0.7.0"},
		{"0.6.0", "0.7.0", "0.7.0"},
		{"0.9.0", "0.10.0", "0.10.0"},
		{"", "0.7.0", "0.7.0"},
		{"0.7.0", "dev", "0.7.0"},
	}
	for _, c := range cases {
		if got := Later(c[0], c[1]); got != c[2] {
			t.Errorf("Later(%q, %q) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
}

func TestFit(t *testing.T) {
	one := []Release{{"0.7.0", []string{"a", "b"}}}
	many := []Release{{"0.7.0", []string{"a", "b"}}, {"0.6.1", []string{"c", "d"}}}

	if b, ok := Fit(one, 10); !ok || b.Heading != "What's new in 0.7.0" || !slices.Equal(b.Lines, []string{"a", "b"}) {
		t.Errorf("one release: %+v %v", b, ok)
	}
	b, ok := Fit(many, 10)
	if !ok || b.Heading != "What's new" || len(b.Lines) != MaxLines {
		t.Fatalf("overflow: %+v %v", b, ok)
	}
	if b.Lines[0] != "0.7.0: a" || !strings.HasPrefix(b.Lines[2], "and 2 more: ") {
		t.Errorf("overflow lines: %v", b.Lines)
	}
	if b, ok := Fit(one, 3); !ok || len(b.Lines) != 1 || !strings.Contains(b.Lines[0], "2 updates") {
		t.Errorf("one row: %+v %v", b, ok)
	}
	if _, ok := Fit(one, 2); ok {
		t.Error("no row for an item: want no block")
	}
	if _, ok := Fit(nil, 10); ok {
		t.Error("no news: want no block")
	}
}

// TestEntries checks the shipped text, so a bad line fails before a release.
func TestEntries(t *testing.T) {
	version := regexp.MustCompile(`^\d+\.\d+\.\d+$`)
	for key, items := range Entries {
		if key != Unreleased && !version.MatchString(key) {
			t.Errorf("key %q is not a version or %q", key, Unreleased)
		}
		if len(items) > MaxLines {
			t.Errorf("%s has %d lines, max %d", key, len(items), MaxLines)
		}
		for _, item := range items {
			if len(item) > MaxLineWidth {
				t.Errorf("%s: %q is longer than %d characters", key, item, MaxLineWidth)
			}
		}
	}
}
