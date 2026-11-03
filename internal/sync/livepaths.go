package sync

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// livePathFiles are wp-content files (glob patterns) that the file sync
// copies from live and that can hold live's absolute path. Stage loads most of
// them before WordPress starts, so a live path makes stage run live's plugin
// code, or write and clear live's cache folders:
//   - WP Super Cache writes $cache_path into wp-cache-config.php.
//   - advanced-cache.php of Breeze, Cache Enabler, Jetpack Boost, WP-Optimize,
//     NitroPack and Powered Cache include live's plugin files.
//   - SiteGround Optimizer's sgo-config.php holds live's cache output_dir.
//   - Swift Performance's mu-plugin loader includes live's plugin.
//   - Breeze's config and Autoptimize's 404 handler hold live paths.
var livePathFiles = []string{
	"wp-cache-config.php", "advanced-cache.php", "object-cache.php",
	"sgo-config.php", "autoptimize_404_handler.php",
	"mu-plugins/swift-performance-loader.php",
	"breeze-config/*.php", "docket-cache-data/runtime.php",
}

// replaceLivePath swaps live's webroot for stage's in text. Only whole path
// prefixes ("<live>/" or "<live>" before a quote) change.
func replaceLivePath(text, livePath, stagePath string) (string, int) {
	live := strings.TrimRight(livePath, "/")
	stage := strings.TrimRight(stagePath, "/")
	n := 0
	for _, end := range []string{"/", "'", `"`} {
		c := strings.Count(text, live+end)
		if c > 0 {
			text = strings.ReplaceAll(text, live+end, stage+end)
			n += c
		}
	}
	return text, n
}

// FixLivePaths rewrites live paths in the copied config files. It runs right
// after the file sync, before WordPress loads on stage.
func FixLivePaths(livePath, stagePath, liveHost, stageHost string, log interface{ Detail(string) }) error {
	live := strings.TrimRight(livePath, "/")
	stage := strings.TrimRight(stagePath, "/")
	// A stage inside live would make the replacement ambiguous.
	if strings.HasPrefix(stage+"/", live+"/") {
		return fmt.Errorf("stage path %s is inside live path %s: cannot fix copied config paths", stage, live)
	}
	var paths []string
	for _, pattern := range livePathFiles {
		m, _ := filepath.Glob(filepath.Join(stage, "wp-content", pattern))
		paths = append(paths, m...)
	}
	for _, path := range paths {
		name, _ := filepath.Rel(filepath.Join(stage, "wp-content"), path)
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		fixed, n := replaceLivePath(string(data), live, stage)
		if strings.HasPrefix(name, "breeze-config"+string(filepath.Separator)) {
			// Breeze keeps live's home URL ('homepage') for its link rules.
			var m int
			fixed, m = replaceLiveURL(fixed, liveHost, stageHost)
			n += m
		}
		if n == 0 {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(fixed), info.Mode().Perm()); err != nil {
			return err
		}
		log.Detail(fmt.Sprintf("Replaced %d live paths in stage wp-content/%s", n, name))
	}
	for _, path := range w3tcConfigFiles(stage) {
		n, err := fixW3TCConfig(path, path, live, stage)
		if err != nil {
			return err
		}
		if n > 0 {
			log.Detail(fmt.Sprintf("Switched off live integrations in stage W3 Total Cache config %s (%d settings changed)", filepath.Base(path), n))
		}
	}
	return removeLiveHostFiles(stage, liveHost, stageHost, log)
}

// liveHostFiles are per-host config copies that stage must not read. Each
// plugin rebuilds the stage file from the database. Cache Enabler's early
// engine falls back to the first file in its folder, so it could load live's.
var liveHostFiles = []string{
	"wpo-cache/config/config-%s.php", "wpo-cache/config/config-%s-port*.php",
	"settings/cache-enabler/%s.php", "settings/cache-enabler/%s.*.php",
}

// removeLiveHostFiles deletes stage copies of live's per-host config files.
func removeLiveHostFiles(stage, liveHost, stageHost string, log interface{ Detail(string) }) error {
	if liveHost == "" || strings.ContainsAny(liveHost, "/*?[") {
		return nil
	}
	for _, pattern := range liveHostFiles {
		matches, _ := filepath.Glob(filepath.Join(stage, "wp-content", fmt.Sprintf(pattern, liveHost)))
		for _, m := range matches {
			base := filepath.Base(m)
			// "<live>.*" also matches a stage host that starts with live's.
			if stageHost != "" && strings.Contains(base, stageHost) {
				continue
			}
			if info, err := os.Lstat(m); err != nil || !info.Mode().IsRegular() {
				continue
			}
			if err := os.Remove(m); err != nil {
				return err
			}
			rel, _ := filepath.Rel(filepath.Join(stage, "wp-content"), m)
			log.Detail("Removed live's config copy from stage: wp-content/" + rel)
		}
	}
	return nil
}

// replaceLiveURL swaps "://<live host>" for "://<stage host>" when the host
// ends there (quote, slash, colon or backslash), so a longer host is kept.
func replaceLiveURL(text, liveHost, stageHost string) (string, int) {
	if liveHost == "" || stageHost == "" {
		return text, 0
	}
	n := 0
	for _, end := range []string{"'", `"`, "/", ":", `\`} {
		from := "://" + liveHost + end
		if c := strings.Count(text, from); c > 0 {
			text = strings.ReplaceAll(text, from, "://"+stageHost+end)
			n += c
		}
	}
	return text, n
}
