package selfupdate

import "testing"

func TestIsNewer(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"0.7.0", "0.6.0", true},
		{"v0.7.0", "0.6.0", true},
		{"0.10.0", "0.9.0", true}, // numeric, not string, comparison
		{"0.7.0", "0.7.0", false},
		{"v0.7.0", "0.7.0", false},
		{"0.6.0", "0.7.0", false}, // cached version is older after an upgrade
		{"0.6.0", "0.7.0-dev", false},
		{"0.7.0", "0.7.0-dev", true}, // the release is newer than its pre-release
		{"garbage", "0.7.0", false},
		{"0.7.0", "garbage", false},
		{"", "0.7.0", false},
	}
	for _, c := range cases {
		if got := isNewer(c.latest, c.current); got != c.want {
			t.Errorf("isNewer(%q, %q) = %v, want %v", c.latest, c.current, got, c.want)
		}
	}
}
