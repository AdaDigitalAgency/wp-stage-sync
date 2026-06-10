package sync

import "testing"

func TestHostFromURL(t *testing.T) {
	cases := map[string]string{
		"https://example.com":          "example.com",
		"http://example.com/":          "example.com",
		"https://example.com/blog":     "example.com",
		"//example.com":                "example.com",
		"example.com":                  "example.com",
		"  https://example.com  ":      "example.com",
		"https://sub.example.com:8080": "sub.example.com:8080",
	}
	for in, want := range cases {
		if got := hostFromURL(in); got != want {
			t.Errorf("hostFromURL(%q) = %q, want %q", in, got, want)
		}
	}
}
