package promote

import (
	"github.com/AdaDigitalAgency/wp-stage-sync/internal/progress"
	"os"
	"path/filepath"
	"testing"
)

func TestSafeJoin(t *testing.T) {
	dest := t.TempDir()

	ok := []string{"theme/twentytwenty/style.css", "plugin/woo/woo.php", "."}
	for _, name := range ok {
		if _, err := safeJoin(dest, name); err != nil {
			t.Errorf("safeJoin(%q) unexpected error: %v", name, err)
		}
	}

	bad := []string{"../escape", "../../etc/passwd", "theme/../../escape"}
	for _, name := range bad {
		if _, err := safeJoin(dest, name); err == nil {
			t.Errorf("safeJoin(%q) should have been rejected", name)
		}
	}
}

func TestUnderWPContent(t *testing.T) {
	live := "/home/example.com"
	wpContent := filepath.Join(live, "wp-content")

	if !underWPContent(live, filepath.Join(wpContent, "plugins", "woo")) {
		t.Error("a plugin path should be inside wp-content")
	}
	// The wp-content root itself must not qualify (no wholesale removal).
	if underWPContent(live, wpContent) {
		t.Error("wp-content root should not qualify")
	}
	// Paths outside wp-content must be rejected.
	if underWPContent(live, "/home/example.com-other/wp-content/x") {
		t.Error("path outside live wp-content should be rejected")
	}
	if underWPContent(live, "/etc/passwd") {
		t.Error("unrelated path should be rejected")
	}
}

func TestBackupKeepsSymlinks(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	live := t.TempDir()
	plugin := filepath.Join(live, "wp-content", "plugins", "demo")
	os.MkdirAll(filepath.Join(plugin, "assets"), 0755)
	os.WriteFile(filepath.Join(plugin, "demo.php"), []byte("<?php // demo"), 0644)
	os.Symlink("demo.php", filepath.Join(plugin, "link.php"))            // link to a file
	os.Symlink("assets", filepath.Join(plugin, "assets-link"))           // link to a folder
	os.Symlink("/nonexistent/target", filepath.Join(plugin, "dangling")) // broken link

	archive, err := CreateBackup("demo.test", []SelectedItem{{Type: "plugin", Name: "demo", LivePath: plugin}}, progress.NopLogger{})
	if err != nil {
		t.Fatalf("backup with symlinks failed: %v", err)
	}
	out := t.TempDir()
	if err := extractTarGz(archive, out); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"link.php": "demo.php", "assets-link": "assets", "dangling": "/nonexistent/target"} {
		got, err := os.Readlink(filepath.Join(out, "plugin", "demo", name))
		if err != nil || got != want {
			t.Errorf("%s: link %q (%v), want %q", name, got, err, want)
		}
	}
	if data, _ := os.ReadFile(filepath.Join(out, "plugin", "demo", "demo.php")); string(data) != "<?php // demo" {
		t.Errorf("file content = %q", data)
	}
}
