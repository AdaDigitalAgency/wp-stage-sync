package release

import (
	"slices"
	"testing"
)

func TestWebsiteTarget(t *testing.T) {
	got, err := WebsiteTarget("# comment\nDEPLOY_USER=root\n\nOTHER=x y\nDEPLOY_HOST=example.com\n")
	if err != nil || got != "root@example.com" {
		t.Errorf("got %q, %v", got, err)
	}
	for _, env := range []string{
		"",
		"DEPLOY_USER=root\n",
		"DEPLOY_HOST=example.com\n",
		"DEPLOY_USER=root;reboot\nDEPLOY_HOST=example.com\n",
		"DEPLOY_USER=root\nDEPLOY_HOST=example.com:/tmp\n",
		"DEPLOY_USER=-oProxyCommand=x\nDEPLOY_HOST=example.com\n",
		"DEPLOY_USER=root\nDEPLOY_HOST=-oProxyCommand=x\n",
	} {
		if got, err := WebsiteTarget(env); err == nil {
			t.Errorf("WebsiteTarget(%q) = %q, want error", env, got)
		}
	}
}

func TestChangedFiles(t *testing.T) {
	out := "Transfer starting: 13 files\n" +
		".d..t.... ./\n" +
		"<fcst.... index.html\n" +
		".f..t.... install.sh\n" +
		"<f+++++++ images/new one.png\n" +
		"cd+++++++ images/sub/\n" +
		"\nsent 679 bytes  received 38 bytes\n"
	want := []string{"index.html", "images/new one.png"}
	if got := ChangedFiles(out); !slices.Equal(got, want) {
		t.Errorf("ChangedFiles = %q, want %q", got, want)
	}
	if got := ChangedFiles(".d..t.... ./\n"); len(got) != 0 {
		t.Errorf("no changes: got %q", got)
	}
}
