package sync

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AdaDigitalAgency/wp-stage-sync/internal/progress"
)

func writeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// TestFileSyncExcludesOnlyTopLevel runs real rsync: excluding "cache" must skip
// wp-content/cache but still copy same-named folders deeper in the tree.
func TestFileSyncExcludesOnlyTopLevel(t *testing.T) {
	if _, err := exec.LookPath("rsync"); err != nil {
		t.Skip("rsync not installed")
	}
	live, stage := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(live, "wp-content/cache/page.html"))
	writeFile(t, filepath.Join(live, "wp-content/plugins/elementor/cache/asset.css"))
	writeFile(t, filepath.Join(live, "wp-content/uploads/cache/image.jpg"))
	// Stage-only data in an excluded folder must survive --delete.
	writeFile(t, filepath.Join(stage, "wp-content/cache/stage-only.html"))

	if err := FileSync(live, stage, []string{"cache"}, progress.NopLogger{}); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(stage, "wp-content")
	if exists(filepath.Join(dst, "cache/page.html")) {
		t.Error("top-level cache was copied, want excluded")
	}
	if !exists(filepath.Join(dst, "cache/stage-only.html")) {
		t.Error("top-level cache on stage was deleted, want preserved")
	}
	for _, p := range []string{"plugins/elementor/cache/asset.css", "uploads/cache/image.jpg"} {
		if !exists(filepath.Join(dst, p)) {
			t.Errorf("%s was not copied, want copied", p)
		}
	}
}

func TestExcludeArgs(t *testing.T) {
	got := excludeArgs([]string{"cache", "/ewww", "uploads/tmp", "dir/"})
	want := []string{"--exclude=/cache", "--exclude=/ewww", "--exclude=/uploads/tmp", "--exclude=/dir/"}
	if len(got) != len(want) {
		t.Fatalf("excludeArgs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("excludeArgs[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestResolveExcludes(t *testing.T) {
	if got := ResolveExcludes(nil); len(got) != len(DefaultExcludes) {
		t.Errorf("nil: got %v, want defaults", got)
	}
	if got := ResolveExcludes([]string{}); got == nil || len(got) != 0 {
		t.Errorf("empty: got %v, want explicit empty list", got)
	}
	if got := ResolveExcludes([]string{"a"}); len(got) != 1 || got[0] != "a" {
		t.Errorf("saved: got %v", got)
	}
}

func TestFileSyncErrorIncludesRsyncOutput(t *testing.T) {
	if _, err := exec.LookPath("rsync"); err != nil {
		t.Skip("rsync not installed")
	}
	live := filepath.Join(t.TempDir(), "missing")
	err := FileSync(live, t.TempDir(), nil, progress.NopLogger{})
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), live) {
		t.Errorf("error %q does not include rsync output", err)
	}
}

func TestCommandError(t *testing.T) {
	var out strings.Builder
	for i := 1; i <= 30; i++ {
		fmt.Fprintf(&out, "line %d\n", i)
	}
	base := errors.New("exit status 23")
	err := CommandError(base, []byte(out.String()))
	if !errors.Is(err, base) {
		t.Error("base error not wrapped")
	}
	if !strings.Contains(err.Error(), "line 30") || strings.Contains(err.Error(), "line 20\n") {
		t.Errorf("want only the last lines, got %q", err)
	}
	if got := CommandError(base, []byte("  \n")); got != base {
		t.Errorf("empty output: got %v, want base error", got)
	}
}
