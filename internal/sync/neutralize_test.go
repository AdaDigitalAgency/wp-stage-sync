package sync

import (
	"strings"
	"testing"
)

func TestNeutralizeQuery(t *testing.T) {
	q, args := neutralizeQuery("wp_", integration{Name: "x", Blank: []string{"a", "b"}})
	if q != "UPDATE `wp_options` SET option_value = '' WHERE option_name IN (?,?) AND option_value <> ''" {
		t.Errorf("query: %s", q)
	}
	if len(args) != 2 || args[0] != "a" || args[1] != "b" {
		t.Errorf("args: %v", args)
	}
}

func TestDropJSONEntries(t *testing.T) {
	drop := dropJSONEntries("id", "cloudflare")
	raw := `[{"id":"maxcdn","cdnurl":"cdn.example.com"},{"id":"cloudflare","cdnurl":"a@b.c","originurl":"KEY","zone_id":"Z"}]`
	got, err := drop(raw)
	if err != nil || strings.Contains(got, "cloudflare") || strings.Contains(got, "KEY") || !strings.Contains(got, "maxcdn") {
		t.Errorf("got %s, %v", got, err)
	}
	if got, _ := drop(`[{"id":"cloudflare","originurl":"KEY"}]`); got != "" {
		t.Errorf("only Cloudflare: got %q, want empty", got)
	}
	if got, _ := drop(`[{"id":"maxcdn"}]`); got != `[{"id":"maxcdn"}]` {
		t.Errorf("no Cloudflare: changed to %s", got)
	}
	if _, err := drop("not json"); err == nil {
		t.Error("bad JSON: want error")
	}
}

func TestRewriteBase64Host(t *testing.T) {
	in := "aHR0cHM6Ly9hLnRlc3Qv" // https://a.test/
	got, _ := rewriteBase64Host("a.test", "stage.a.test")(in)
	if got != "aHR0cHM6Ly9zdGFnZS5hLnRlc3Qv" {
		t.Errorf("got %s", got)
	}
	if got, _ := rewriteBase64Host("a.test", "stage.a.test")("%%%"); got != "%%%" {
		t.Errorf("bad base64 changed: %s", got)
	}
}

func TestIntegrationsCoverLiteSpeedCloudflare(t *testing.T) {
	var names []string
	for _, in := range integrationsFor("/home/a", "/home/stage.a", "a.test", "stage.a.test") {
		names = append(names, in.Blank...)
	}
	all := strings.Join(names, " ")
	for _, must := range []string{"litespeed.conf.cdn-cloudflare ", "litespeed.conf.cdn-cloudflare_key", "litespeed.conf.cdn-cloudflare_zone"} {
		if !strings.Contains(all+" ", must) {
			t.Errorf("missing %q", must)
		}
	}
}
