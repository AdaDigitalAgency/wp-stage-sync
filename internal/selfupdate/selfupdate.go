package selfupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/creativeprojects/go-selfupdate"
)

const (
	repoOwner = "AdaDigitalAgency"
	repoName  = "wp-stage-sync"
)

func Update(currentVersion string) error {
	source, err := selfupdate.NewGitHubSource(selfupdate.GitHubConfig{})
	if err != nil {
		return fmt.Errorf("creating GitHub source: %w", err)
	}

	// Verify the downloaded binary against the release checksums.txt before it
	// replaces the running executable.
	updater, err := selfupdate.NewUpdater(selfupdate.Config{
		Source:    source,
		Validator: &selfupdate.ChecksumValidator{UniqueFilename: "checksums.txt"},
	})
	if err != nil {
		return fmt.Errorf("creating updater: %w", err)
	}

	latest, found, err := updater.DetectLatest(context.Background(), selfupdate.NewRepositorySlug(repoOwner, repoName))
	if err != nil {
		return fmt.Errorf("checking for updates: %w", err)
	}
	if !found {
		fmt.Println("No releases found.")
		return nil
	}

	if currentVersion != "dev" && latest.LessOrEqual(currentVersion) {
		fmt.Printf("Already up to date (v%s).\n", currentVersion)
		return nil
	}

	fmt.Printf("Updating from %s to %s (%s/%s)...\n",
		currentVersion, latest.Version(), runtime.GOOS, runtime.GOARCH)

	exe, err := selfupdate.ExecutablePath()
	if err != nil {
		return fmt.Errorf("finding executable: %w", err)
	}

	if err := updater.UpdateTo(context.Background(), latest, exe); err != nil {
		return fmt.Errorf("applying update: %w", err)
	}

	fmt.Printf("Updated to v%s.\n", latest.Version())
	saveState(&internalState{}) // clear cached version so update notice disappears
	return nil
}

// CheckVersion returns the latest version string if a newer version is available.
// Checks GitHub at most every 8 hours, but returns the cached result on every call
// so the update notice persists until the user actually upgrades.
func CheckVersion(currentVersion string) string {
	if currentVersion == "dev" {
		return ""
	}

	state := loadState()

	if shouldCheck(state) {
		if v := fetchLatest(); v != "" {
			state.LatestVersion = v
		}
		state.LastCheck = time.Now().Unix()
		saveState(state)
	}

	if state.LatestVersion == "" {
		return ""
	}

	// Normalize and compare: strip "v" prefix from both
	cached := strings.TrimPrefix(state.LatestVersion, "v")
	current := strings.TrimPrefix(currentVersion, "v")
	if cached == current {
		return ""
	}

	return state.LatestVersion
}

type internalState struct {
	LastCheck     int64  `json:"last_check"`
	LatestVersion string `json:"latest_version"`
}

func statePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "wp-stage-sync", ".internal")
}

func loadState() *internalState {
	p := statePath()
	if p == "" {
		return &internalState{}
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return &internalState{}
	}
	var s internalState
	if err := json.Unmarshal(data, &s); err != nil {
		return &internalState{}
	}
	return &s
}

func saveState(s *internalState) {
	p := statePath()
	if p == "" {
		return
	}
	data, _ := json.Marshal(s)
	_ = os.WriteFile(p, data, 0644)
}

func shouldCheck(s *internalState) bool {
	if s.LastCheck == 0 {
		return true
	}
	return time.Since(time.Unix(s.LastCheck, 0)) >= 8*time.Hour
}

func fetchLatest() string {
	source, err := selfupdate.NewGitHubSource(selfupdate.GitHubConfig{})
	if err != nil {
		return ""
	}
	updater, err := selfupdate.NewUpdater(selfupdate.Config{Source: source})
	if err != nil {
		return ""
	}
	latest, found, err := updater.DetectLatest(context.Background(), selfupdate.NewRepositorySlug(repoOwner, repoName))
	if err != nil || !found {
		return ""
	}
	return latest.Version()
}
