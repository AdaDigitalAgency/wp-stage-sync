package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/AdaDigitalAgency/wp-stage-sync/internal/config"
	"github.com/AdaDigitalAgency/wp-stage-sync/internal/whatsnew"
)

const newsVersion = "0.7.0"

func withNews(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	saved := whatsnew.Entries
	whatsnew.Entries = map[string][]string{newsVersion: {"first line", "second line"}}
	t.Cleanup(func() { whatsnew.Entries = saved })
}

func homeModel(sites int, version string) model {
	m := model{step: stepStartup, currentVersion: version}
	for range sites {
		m.savedSites = append(m.savedSites, config.SavedSite{Domain: "example.com", Config: &config.Config{}})
	}
	return m.loadNews()
}

func resize(m model, height int) model {
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: height})
	return next.(model)
}

func seen() string { return config.LoadSettings().WhatsNewSeen }

func TestNewsShownOnceAfterUpdate(t *testing.T) {
	withNews(t)
	if err := config.SaveSettings(config.Settings{WhatsNewSeen: "0.6.0"}); err != nil {
		t.Fatal(err)
	}
	m := resize(homeModel(1, newsVersion), 50)
	if !strings.Contains(m.View(), "What's new in 0.7.0") || !strings.Contains(m.View(), "second line") {
		t.Fatalf("block missing:\n%s", m.View())
	}
	if seen() != newsVersion {
		t.Errorf("seen = %q", seen())
	}
	// The block stays for this session, and the next run shows nothing.
	if m = resize(m, 49); !strings.Contains(m.View(), "first line") {
		t.Error("block gone after a resize")
	}
	if strings.Contains(resize(homeModel(1, newsVersion), 50).View(), "What's new") {
		t.Error("block shown a second time")
	}
}

func TestExistingUserWithoutSeenGetsCurrentVersion(t *testing.T) {
	withNews(t)
	m := resize(homeModel(1, newsVersion), 50)
	if !strings.Contains(m.View(), "first line") {
		t.Errorf("block missing:\n%s", m.View())
	}
}

func TestFirstInstallShowsNothingButRecords(t *testing.T) {
	withNews(t)
	m := resize(homeModel(0, newsVersion), 50)
	if len(m.whatsNew) != 0 {
		t.Errorf("whatsNew = %v", m.whatsNew)
	}
	if seen() != newsVersion {
		t.Errorf("seen = %q, want the next update to count as news", seen())
	}
}

func TestShortTerminalKeepsNewsForLater(t *testing.T) {
	withNews(t)
	m := resize(homeModel(1, newsVersion), 8)
	if strings.Contains(m.View(), "What's new") {
		t.Errorf("block drawn without room:\n%s", m.View())
	}
	if seen() != "" {
		t.Errorf("seen = %q, want empty", seen())
	}
	if m = resize(m, 50); !strings.Contains(m.View(), "first line") || seen() != newsVersion {
		t.Errorf("block missing after growth, seen %q", seen())
	}
}

func TestNewsReplacesBannerAtDefaultTerminalSize(t *testing.T) {
	withNews(t)
	next, _ := homeModel(1, newsVersion).Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m := next.(model)
	view := m.View()
	if !strings.Contains(view, "first line") || !strings.Contains(view, "second line") {
		t.Fatalf("block missing at 80x24:\n%s", view)
	}
	if h := strings.Count(view, "\n") + 1; h > 24 {
		t.Errorf("view is %d rows, want at most 24", h)
	}
	if seen() != newsVersion {
		t.Errorf("seen = %q", seen())
	}
}

func TestBannerStaysWithRoomForNews(t *testing.T) {
	withNews(t)
	if m := resize(homeModel(1, newsVersion), 50); m.newsHidesBanner {
		t.Error("banner dropped on a tall terminal")
	}
	if m := resize(homeModel(1, "dev"), 24); m.newsHidesBanner {
		t.Error("banner dropped with no news")
	}
}

func TestDevBuildIgnoresNews(t *testing.T) {
	withNews(t)
	m := resize(homeModel(1, "dev"), 50)
	if strings.Contains(m.View(), "What's new") || seen() != "" {
		t.Errorf("dev build: seen %q, view:\n%s", seen(), m.View())
	}
}
