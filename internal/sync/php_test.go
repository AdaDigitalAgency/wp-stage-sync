package sync

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestPHPSnippetsParse lints the PHP that runs through `wp eval` on stage.
func TestPHPSnippetsParse(t *testing.T) {
	if _, err := exec.LookPath("php"); err != nil {
		t.Skip("php not installed")
	}
	for name, code := range map[string]string{
		"redisPrefixFlush":     redisPrefixFlush,
		"wpRedisPrefixFlush":   wpRedisPrefixFlush,
		"liteSpeedPrefixFlush": liteSpeedPrefixFlush,
		"liteSpeedPagePurge":   liteSpeedPagePurge,
		"ocpFlush":             fmt.Sprintf(ocpFlush, 0),
	} {
		f := filepath.Join(t.TempDir(), name+".php")
		if err := os.WriteFile(f, []byte("<?php\n"+code+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("php", "-l", f).CombinedOutput(); err != nil {
			t.Errorf("%s: %s", name, out)
		}
	}
}
