package sync

import (
	"strings"
	"testing"
)

func TestSetPHPKeys(t *testing.T) {
	raw := `a:5:{s:13:"do_cloudflare";i:1;s:18:"cloudflare_api_key";s:6:"SECRET";s:10:"cdn_cnames";a:1:{i:0;s:15:"cdn.example.com";}s:4:"note";s:9:"a;b:"c"};";i:7;b:1;}`
	got, err := setPHPKeys(map[string]string{
		"do_cloudflare":      "i:0;",
		"cloudflare_api_key": phpString(""),
		"absent":             "i:1;",
	})(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := `a:5:{s:13:"do_cloudflare";i:0;s:18:"cloudflare_api_key";s:0:"";s:10:"cdn_cnames";a:1:{i:0;s:15:"cdn.example.com";}s:4:"note";s:9:"a;b:"c"};";i:7;b:1;}`
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if strings.Contains(got, "SECRET") {
		t.Error("key kept")
	}
}

func TestParsePHPArrayRejects(t *testing.T) {
	for _, bad := range []string{"", "s:3:\"abc\";", "a:1:{s:1:\"a\";i:1;", `a:1:{s:9:"short";i:1;}`, `a:1:{s:1:"a";O:8:"stdClass":0:{}}`, "a:0:{}x"} {
		if _, err := parsePHPArray(bad); err == nil {
			t.Errorf("parsePHPArray(%q): want error", bad)
		}
	}
	if e, err := parsePHPArray("a:0:{}"); err != nil || len(e) != 0 {
		t.Errorf("empty array: %v %v", e, err)
	}
}
