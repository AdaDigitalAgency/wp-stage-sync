package guardrail

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/AdaDigitalAgency/wp-stage-sync/internal/wpconfig"
)

func TestValidatePathsDistinct(t *testing.T) {
	if err := ValidatePaths("/home/example.com", "/home/stage.example.com"); err != nil {
		t.Fatalf("distinct paths should pass, got %v", err)
	}
}

func TestValidatePathsIdentical(t *testing.T) {
	dir := t.TempDir()
	if err := ValidatePaths(dir, dir); err == nil {
		t.Fatal("identical paths should be rejected")
	}
}

// A stage path symlinked into the live tree must be caught.
func TestValidatePathsSymlink(t *testing.T) {
	root := t.TempDir()
	live := filepath.Join(root, "live")
	if err := os.Mkdir(live, 0755); err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(root, "stage")
	if err := os.Symlink(live, stage); err != nil {
		t.Skipf("symlinks unsupported on this platform: %v", err)
	}
	if err := ValidatePaths(live, stage); err == nil {
		t.Fatal("stage symlinked into live should be rejected")
	}
}

func TestValidateWPContentPathsSymlink(t *testing.T) {
	root := t.TempDir()
	live := filepath.Join(root, "live")
	if err := os.MkdirAll(filepath.Join(live, "wp-content"), 0755); err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(root, "stage")
	if err := os.Symlink(live, stage); err != nil {
		t.Skipf("symlinks unsupported on this platform: %v", err)
	}
	if err := ValidateWPContentPaths(live, stage); err == nil {
		t.Fatal("identical wp-content (via symlink) should be rejected")
	}
}

func TestValidateDBsIdentical(t *testing.T) {
	same := &wpconfig.WPConfig{DBName: "wp", DBHost: "localhost"}
	if err := ValidateDBs(same, same); err == nil {
		t.Fatal("identical db name+host should be rejected")
	}
}

func TestValidateDBsDistinct(t *testing.T) {
	live := &wpconfig.WPConfig{DBName: "wp_live", DBHost: "localhost"}
	stage := &wpconfig.WPConfig{DBName: "wp_stage", DBHost: "localhost"}
	if err := ValidateDBs(live, stage); err != nil {
		t.Fatalf("distinct db names should pass, got %v", err)
	}
}

// ValidateDBs is intentionally lenient about host spelling — the authoritative
// guard is ValidateConnectedDBs at connect time. Documents that the pre-check
// does not trip on host-string differences.
func TestValidateDBsHostStringLimitation(t *testing.T) {
	live := &wpconfig.WPConfig{DBName: "wp", DBHost: "localhost"}
	stage := &wpconfig.WPConfig{DBName: "wp", DBHost: "127.0.0.1"}
	if err := ValidateDBs(live, stage); err != nil {
		t.Fatalf("pre-connect check should not trip on host spelling, got %v", err)
	}
}
