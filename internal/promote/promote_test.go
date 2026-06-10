package promote

import (
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
