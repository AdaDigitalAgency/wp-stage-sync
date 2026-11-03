package sync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AdaDigitalAgency/wp-stage-sync/internal/progress"
)

func TestReplaceLivePath(t *testing.T) {
	in := "$cache_path = '/home/a.test/wp-content/cache/';\n$x = '/home/a.test';\n$y = '/home/a.tester/z';\n$z = '/home/stage.a.test/q';"
	got, n := replaceLivePath(in, "/home/a.test/", "/home/stage.a.test")
	want := "$cache_path = '/home/stage.a.test/wp-content/cache/';\n$x = '/home/stage.a.test';\n$y = '/home/a.tester/z';\n$z = '/home/stage.a.test/q';"
	if got != want || n != 2 {
		t.Errorf("got %d:\n%s", n, got)
	}
}

func TestFixLivePaths(t *testing.T) {
	root := t.TempDir()
	live, stage := filepath.Join(root, "a.test"), filepath.Join(root, "stage.a.test")
	cfg := filepath.Join(stage, "wp-content", "wp-cache-config.php")
	writeFile(t, cfg)
	os.WriteFile(cfg, []byte("<?php $cache_path = '"+live+"/wp-content/cache/';"), 0640)
	os.Chmod(cfg, 0640)
	if err := FixLivePaths(live, stage, "live.test", "stage.live.test", progress.NopLogger{}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(cfg)
	if !strings.Contains(string(data), stage+"/wp-content/cache/") || strings.Contains(string(data), "'"+live) {
		t.Errorf("not fixed: %s", data)
	}
	if info, _ := os.Stat(cfg); info.Mode().Perm() != 0640 {
		t.Errorf("mode changed to %v", info.Mode().Perm())
	}
	if err := FixLivePaths(root, filepath.Join(root, "stage"), "", "", progress.NopLogger{}); err == nil {
		t.Error("stage inside live: want error")
	}
}
