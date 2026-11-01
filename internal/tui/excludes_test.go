package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/AdaDigitalAgency/wp-stage-sync/internal/config"
)

func liveWith(t *testing.T, dirs ...string) string {
	t.Helper()
	live := t.TempDir()
	for _, d := range append(dirs, "") {
		if err := os.MkdirAll(filepath.Join(live, "wp-content", d), 0755); err != nil {
			t.Fatal(err)
		}
	}
	return live
}

func pressKey(t *testing.T, m model, k tea.KeyType) model {
	t.Helper()
	next, _ := m.updateExcludes(tea.KeyMsg{Type: k})
	return next.(model)
}

func TestBuildExcludeListKeepsSavedPathsNotInWPContent(t *testing.T) {
	cfg := &config.Config{
		LivePath:      liveWith(t, "cache", "plugins"),
		RsyncExcludes: []string{"/cache", "uploads/tmp"},
	}
	items, checked, err := buildExcludeList(cfg)
	if err != nil {
		t.Fatal(err)
	}
	m := model{cfg: cfg, excludeItems: items, excludeChecked: checked}
	m = pressKey(t, m, tea.KeyEnter)

	want := []string{"cache", "uploads/tmp"}
	if !slices.Equal(cfg.RsyncExcludes, want) {
		t.Errorf("RsyncExcludes = %v, want %v", cfg.RsyncExcludes, want)
	}
}

func TestUncheckAllPersistsAsEmptyNotDefaults(t *testing.T) {
	cfg := &config.Config{LivePath: liveWith(t, "cache")}
	items, checked, _ := buildExcludeList(cfg)
	m := model{cfg: cfg, excludeItems: items, excludeChecked: checked}
	m = pressKey(t, m, tea.KeySpace) // uncheck the default "cache"
	m = pressKey(t, m, tea.KeyEnter)

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var loaded config.Config
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatal(err)
	}
	if loaded.RsyncExcludes == nil || len(loaded.RsyncExcludes) != 0 {
		t.Errorf("after save/load RsyncExcludes = %#v, want empty non-nil", loaded.RsyncExcludes)
	}
	items, checked, _ = buildExcludeList(&loaded)
	if checked["cache"] {
		t.Errorf("cache re-checked after reload, items %v", items)
	}
}

func TestUntouchedExcludeStepKeepsDefaults(t *testing.T) {
	// No default folder exists yet, so the list opens with nothing checked.
	cfg := &config.Config{LivePath: liveWith(t, "plugins")}
	items, checked, _ := buildExcludeList(cfg)
	m := model{cfg: cfg, excludeItems: items, excludeChecked: checked}
	m = pressKey(t, m, tea.KeyEnter)
	if cfg.RsyncExcludes != nil {
		t.Errorf("RsyncExcludes = %#v, want nil so defaults apply later", cfg.RsyncExcludes)
	}
	if m.step != stepConfirm {
		t.Errorf("step = %v, want confirm", m.step)
	}
}

func TestEmptyExcludeListKeysDoNotPanicOrClearConfig(t *testing.T) {
	cfg := &config.Config{LivePath: filepath.Join(t.TempDir(), "missing")}
	items, checked, err := buildExcludeList(cfg)
	if err == nil {
		t.Fatal("want read error")
	}
	m := model{cfg: cfg, excludeItems: items, excludeChecked: checked, height: 30}
	for _, k := range []tea.KeyType{tea.KeySpace, tea.KeyEnd, tea.KeyPgDown, tea.KeyEnter} {
		m = pressKey(t, m, k)
	}
	if m.cursor != 0 {
		t.Errorf("cursor = %d, want 0", m.cursor)
	}
	if cfg.RsyncExcludes != nil {
		t.Errorf("RsyncExcludes = %#v, want nil so defaults still apply", cfg.RsyncExcludes)
	}
}
