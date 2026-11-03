package config

import (
	"encoding/json"
	"fmt"
	"github.com/AdaDigitalAgency/wp-stage-sync/internal/discovery"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type TableMode string

const (
	TableModeStructureAndData TableMode = "structure_and_data"
	TableModeStructureOnly    TableMode = "structure_only"
	TableModeIgnore           TableMode = "ignore"
	TableModeCustomRule       TableMode = "custom_rule"
)

type Config struct {
	LivePath        string               `json:"live_path"`
	StagePath       string               `json:"stage_path"`
	OrderCount      int                  `json:"order_count"`
	OrderPreference string               `json:"order_preference"` // "last" or "first"
	Anonymize       bool                 `json:"anonymize"`
	TableModes      map[string]TableMode `json:"table_modes"`
	// RsyncExcludes: nil means use the defaults, empty means exclude nothing.
	RsyncExcludes []string `json:"rsync_excludes"`
	// StageObjectCacheOff removes the persistent object cache from stage. It
	// is offered when stage and live would share one Redis database or
	// Memcached server.
	StageObjectCacheOff bool `json:"stage_object_cache_off,omitempty"`
}

type Settings struct {
	BackupRetention int  `json:"backup_retention"`
	AutoCacheFlush  bool `json:"auto_cache_flush"`
	// WhatsNewSeen is the newest version whose release notes the TUI has shown.
	WhatsNewSeen string `json:"whats_new_seen,omitempty"`
}

func DefaultSettings() Settings {
	return Settings{BackupRetention: 5, AutoCacheFlush: true}
}

func configDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "wp-stage-sync"), nil
}

func sitesDir() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "sites"), nil
}

// DomainFromPath is the site identifier of a webroot: the saved config and
// backups folder name. Generic webroot folders (public_html, httpdocs, …) are
// named after their parent, so two such sites no longer share one config. A
// site already saved under its plain folder name for this same webroot keeps
// that name, with its config and backups.
func DomainFromPath(webroot string) string {
	legacy := filepath.Base(strings.TrimRight(webroot, "/"))
	name := discovery.ExtractDomain(webroot)
	if name == legacy {
		return name
	}
	if cfg, err := Load(legacy); err == nil && filepath.Clean(cfg.LivePath) == filepath.Clean(webroot) {
		return legacy
	}
	return name
}

func siteConfigPath(domain string) (string, error) {
	dir, err := sitesDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, domain, "config.json"), nil
}

// BackupsDir returns the backups directory for a domain.
func BackupsDir(domain string) (string, error) {
	dir, err := sitesDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, domain, "backups"), nil
}

// Load reads the config for a specific domain.
func Load(domain string) (*Config, error) {
	p, err := siteConfigPath(domain)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	return &cfg, nil
}

// Save writes the config, deriving the domain from LivePath.
func Save(cfg *Config) error {
	domain := DomainFromPath(cfg.LivePath)
	if domain == "" || domain == "." {
		return fmt.Errorf("cannot derive domain from live path: %s", cfg.LivePath)
	}
	p, err := siteConfigPath(domain)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0644)
}

// SavedSite represents a saved per-site configuration.
type SavedSite struct {
	Domain string
	Config *Config
}

// ListSites returns all saved site configurations.
func ListSites() []SavedSite {
	dir, err := sitesDir()
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var sites []SavedSite
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		domain := e.Name()
		cfg, err := Load(domain)
		if err != nil {
			continue
		}
		sites = append(sites, SavedSite{Domain: domain, Config: cfg})
	}
	return sites
}

// DeleteSite removes a site's entire config directory (config + backups).
func DeleteSite(identifier string) error {
	dir, err := sitesDir()
	if err != nil {
		return err
	}
	target := filepath.Join(dir, identifier)
	if _, err := os.Stat(target); os.IsNotExist(err) {
		return fmt.Errorf("no site config found for %q", identifier)
	}
	return os.RemoveAll(target)
}

// ResetAllSites removes all site configs and backups.
func ResetAllSites() error {
	dir, err := sitesDir()
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	return os.MkdirAll(dir, 0755)
}

// MigrateLegacyConfigs moves flat config files from sites/{domain}.json
// to the new directory structure sites/{domain}/config.json.
func MigrateLegacyConfigs() {
	dir, err := sitesDir()
	if err != nil {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		domain := strings.TrimSuffix(e.Name(), ".json")
		oldPath := filepath.Join(dir, e.Name())
		newDir := filepath.Join(dir, domain)
		newPath := filepath.Join(newDir, "config.json")

		if err := os.MkdirAll(newDir, 0755); err != nil {
			continue
		}
		if err := os.Rename(oldPath, newPath); err != nil {
			continue
		}
		fmt.Printf("Migrated config for %s to new directory structure.\n", domain)
	}
}

// LoadSettings reads the global settings file.
func LoadSettings() Settings {
	dir, err := configDir()
	if err != nil {
		return DefaultSettings()
	}
	data, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		return DefaultSettings()
	}
	s := DefaultSettings()
	if err := json.Unmarshal(data, &s); err != nil {
		return DefaultSettings()
	}
	if s.BackupRetention <= 0 {
		s.BackupRetention = 5
	}
	// Note: AutoCacheFlush will be false if missing, which is a change from default true.
	// Let's ensure if it was just created (or missing key) it's true, but bool defaults to false.
	// Actually, json.Unmarshal only sets fields present. If we initialize with DefaultSettings(), missing fields retain default.
	return s
}

// SaveSettings writes the global settings file.
func SaveSettings(s Settings) error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "settings.json"), data, 0644)
}

// HasBackups returns true if at least one backup exists for the domain.
func HasBackups(domain string) bool {
	dir, err := BackupsDir(domain)
	if err != nil {
		return false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tar.gz") {
			return true
		}
	}
	return false
}

// ListBackupTimestamps returns backup timestamps sorted most-recent-first.
func ListBackupTimestamps(domain string) []string {
	dir, err := BackupsDir(domain)
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var timestamps []string
	seen := make(map[string]bool)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tar.gz") {
			ts := strings.TrimSuffix(e.Name(), ".tar.gz")
			if !seen[ts] {
				seen[ts] = true
				timestamps = append(timestamps, ts)
			}
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(timestamps)))
	return timestamps
}
