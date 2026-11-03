package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLockPaths(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	release, err := LockPaths("/home/a.test", "/home/stage.a.test/")
	if err != nil {
		t.Fatal(err)
	}
	// flock locks belong to the open file, so a second lock in this process
	// on the same path also fails: it opens the file again.
	if _, err := LockPaths("/home/stage.a.test"); err == nil || !strings.Contains(err.Error(), "another wp-stage-sync run") {
		t.Errorf("second lock: %v", err)
	}
	// Another site is not blocked.
	other, err := LockPaths(filepath.Join("/home", "b.test"))
	if err != nil {
		t.Errorf("other site blocked: %v", err)
	} else {
		other()
	}
	release()
	again, err := LockPaths("/home/stage.a.test")
	if err != nil {
		t.Errorf("lock after release: %v", err)
	} else {
		again()
	}
}

func TestDomainFromPathKeepsLegacyName(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	webroot := "/home/u/example.com/public_html"
	if got := DomainFromPath(webroot); got != "example.com" {
		t.Fatalf("new site: %q", got)
	}
	// A site saved by an older version under "public_html" keeps that name.
	dir, _ := sitesDir()
	os.MkdirAll(filepath.Join(dir, "public_html"), 0755)
	os.WriteFile(filepath.Join(dir, "public_html", "config.json"), []byte(`{"live_path":"`+webroot+`"}`), 0644)
	if got := DomainFromPath(webroot); got != "public_html" {
		t.Errorf("legacy site: %q", got)
	}
	// Another public_html site does not take over that name.
	if got := DomainFromPath("/home/v/other.com/public_html"); got != "other.com" {
		t.Errorf("other site: %q", got)
	}
}
