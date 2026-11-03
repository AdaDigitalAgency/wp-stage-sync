package sync

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/AdaDigitalAgency/wp-stage-sync/internal/progress"
)

// A stage that shares live's cache backend is safe for the sync itself: its
// flush deletes only stage keys. A person in the stage admin is not limited
// that way. LiteSpeed's "Purge All", Redis Object Cache without selective
// flush, WP Redis, and Breeze, WP-Optimize or Super Page Cache (through
// wp_cache_flush) clear the whole Redis database or Memcached server. The
// safe setup is a separate database per stage; the fallback is a stage with
// no persistent object cache.

var defineValueRe = regexp.MustCompile(`define\s*\(\s*['"](\w+)['"]\s*,\s*(?:'([^']*)'|"([^"]*)"|(-?\d+)|(true|false))\s*\)`)

// rawDefines reads string, integer and boolean define() values of a
// wp-config.php. wpconfig.Parse keeps string literals only.
func rawDefines(root string) map[string]string {
	data, err := os.ReadFile(filepath.Join(root, "wp-config.php"))
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for _, m := range defineValueRe.FindAllStringSubmatch(string(data), -1) {
		out[m[1]] = m[2] + m[3] + m[4] + m[5]
	}
	return out
}

func defineOr(d map[string]string, name, def string) string {
	if v := strings.TrimSpace(d[name]); v != "" {
		return v
	}
	return def
}

var wpRedisDatabaseRe = regexp.MustCompile(`(?s)\$redis_server\s*=\s*(?:array\s*\(|\[)(.*?)(?:\)|\])\s*;`)
var wpRedisDBKeyRe = regexp.MustCompile(`['"]database['"]\s*=>\s*['"]?(\d+)`)

// redisTarget describes which Redis server and database a site uses, or ""
// when the config cannot be read as plain values.
func redisTarget(kind objectCache, root string) string {
	d := rawDefines(root)
	switch kind {
	case cacheRedis:
		if defineOr(d, "WP_REDIS_SERVERS", "") != "" || defineOr(d, "WP_REDIS_CLUSTER", "") != "" {
			return ""
		}
		server := defineOr(d, "WP_REDIS_PATH", defineOr(d, "WP_REDIS_HOST", "127.0.0.1")+":"+defineOr(d, "WP_REDIS_PORT", "6379"))
		return server + " database " + defineOr(d, "WP_REDIS_DATABASE", "0")
	case cacheHummingbird:
		return defineOr(d, "WPHB_REDIS_HOST", "") + ":" + defineOr(d, "WPHB_REDIS_PORT", "") + " database " + defineOr(d, "WPHB_REDIS_DB_ID", "0")
	case cacheOCP:
		db, err := ocpDatabase(root)
		if err != nil {
			return ""
		}
		return fmt.Sprintf("database %d", db)
	case cacheWPRedis:
		data, _ := os.ReadFile(filepath.Join(root, "wp-config.php"))
		db := "0"
		if m := wpRedisDatabaseRe.FindSubmatch(data); m != nil {
			if k := wpRedisDBKeyRe.FindSubmatch(m[1]); k != nil {
				db = string(k[1])
			}
		}
		return "database " + db
	}
	return ""
}

// SharedObjectCache reports whether stage would use the same cache backend
// as live after the sync. It returns a short description of the shared
// backend, or "" when stage has its own backend or there is no network cache.
func SharedObjectCache(livePath, stagePath string) string {
	kind := detectObjectCache(livePath)
	switch kind {
	case cacheNone, cacheDocket, cacheSQLite:
		return "" // files in each site's own wp-content
	case cacheAPCu:
		return "" // per PHP pool; WP-CLI cannot reach it either way
	case cacheLiteSpeed:
		// Stage gets live's .litespeed_conf.dat, with live's server and database.
		return "LiteSpeed object cache (stage gets live's server and database)"
	case cacheW3TC:
		if w3tcObjectEngine(livePath) == "file" || w3tcObjectEngine(livePath) == "apc" {
			return ""
		}
		return "W3 Total Cache object cache (stage gets live's server and database)"
	case cacheMemcached, cacheMemcachedServer:
		return "Memcached (one server for both sites)"
	case cacheOther:
		return "an object cache drop-in with an unknown backend"
	}
	live, stage := redisTarget(kind, livePath), redisTarget(kind, stagePath)
	if live == "" || stage == "" {
		return kind.String() + " (cannot read the Redis database of both sites)"
	}
	if live == stage {
		return fmt.Sprintf("%s, Redis %s", kind, live)
	}
	return ""
}

// w3tcObjectEngine reads objectcache.engine from live's W3TC master config.
func w3tcObjectEngine(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "wp-content", "w3tc-config", "master.php"))
	if err != nil {
		return ""
	}
	var cfg map[string]any
	if json.Unmarshal(bytes.TrimPrefix(data, []byte(w3tcFilePrefix)), &cfg) != nil {
		return ""
	}
	engine, _ := cfg["objectcache.engine"].(string)
	return engine
}

// StageObjectCacheExcludes returns the rsync excludes that keep live's
// drop-ins off stage when the stage object cache is off: object-cache.php,
// and W3TC's db.php, which uses the same backend.
func StageObjectCacheExcludes(excludes []string, off bool, livePath string) []string {
	if !off {
		return excludes
	}
	out := append(append([]string{}, excludes...), "object-cache.php")
	if head, err := readHead(filepath.Join(livePath, "wp-content", "db.php")); err == nil && strings.Contains(head, "W3 Total Cache Database module") {
		out = append(out, "db.php")
	}
	return out
}

// removeStageDropIn deletes stage's object-cache.php, and W3TC's db.php,
// which uses the same backend for its database cache.
func removeStageDropIn(stagePath string, log interface{ Detail(string) }) error {
	content := filepath.Join(stagePath, "wp-content")
	if err := os.Remove(filepath.Join(content, "object-cache.php")); err == nil {
		log.Detail("Removed the stage object cache drop-in (stage object cache is off)")
	} else if !os.IsNotExist(err) {
		return err
	}
	db := filepath.Join(content, "db.php")
	if head, err := readHead(db); err == nil && strings.Contains(head, "W3 Total Cache Database module") {
		if err := os.Remove(db); err != nil {
			return err
		}
		log.Detail("Removed the W3 Total Cache database cache drop-in from stage")
	}
	return nil
}

func readHead(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	buf := make([]byte, 4096)
	n, _ := f.Read(buf)
	return string(buf[:n]), nil
}

// DisableStageObjectCache switches the persistent object cache off on stage
// after the file sync, and turns off the settings with which a plugin would
// put its drop-in back.
func DisableStageObjectCache(stagePath string, stageDB *sql.DB, prefix string, log progress.Logger) error {
	if err := removeStageDropIn(stagePath, log); err != nil {
		return fmt.Errorf("removing the stage object cache: %w", err)
	}
	for _, path := range w3tcConfigFiles(stagePath) {
		if err := setW3TCKeys(path, w3tcCacheOff); err != nil {
			return err
		}
	}
	for _, s := range cacheOffSettings {
		changed, err := rewriteOption(stageDB, prefix, s.option, s.rewrite)
		if err != nil {
			return fmt.Errorf("switching off %s on stage: %w", s.label, err)
		}
		if changed {
			log.Detail("Switched off " + s.label + " on stage")
		}
	}
	return nil
}

// cacheOffSettings are the stage options that would put a drop-in back.
var cacheOffSettings = []struct {
	option, label string
	rewrite       func(string) (string, error)
}{
	{"litespeed.conf.object", "LiteSpeed Cache object cache", func(string) (string, error) { return "", nil }},
	{"wphb_settings", "Hummingbird Redis", setPHPKeys(map[string]string{"redis": `a:1:{s:7:"enabled";b:0;}`})},
	{"w3tc_config_0", "W3 Total Cache object and database cache", w3tcCacheOffOption},
	{"w3tc_config_0_preview", "W3 Total Cache object and database cache (preview)", w3tcCacheOffOption},
}

// setW3TCKeys sets existing keys in one W3TC config file.
func setW3TCKeys(path string, values map[string]any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	body, ok := bytes.CutPrefix(data, []byte(w3tcFilePrefix))
	if !ok {
		return nil
	}
	out, changed, err := setJSONKeys(body, values)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if !changed {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	return os.WriteFile(path, append([]byte(w3tcFilePrefix), out...), info.Mode().Perm())
}

// setJSONKeys sets existing top-level keys of a JSON object.
func setJSONKeys(raw []byte, values map[string]any) ([]byte, bool, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var cfg map[string]any
	if err := dec.Decode(&cfg); err != nil {
		return nil, false, err
	}
	changed := false
	for k, v := range values {
		if old, ok := cfg[k]; ok && old != v {
			cfg[k] = v
			changed = true
		}
	}
	if !changed {
		return raw, false, nil
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(cfg); err != nil {
		return nil, false, err
	}
	return bytes.TrimRight(out.Bytes(), "\n"), true, nil
}

// w3tcCacheOff turns W3TC's object and database cache off.
var w3tcCacheOff = map[string]any{"objectcache.enabled": false, "dbcache.enabled": false}

// w3tcCacheOffOption is the cache-off rewrite for the w3tc_config_* options
// (W3TC_CONFIG_DATABASE).
func w3tcCacheOffOption(raw string) (string, error) {
	out, _, err := setJSONKeys([]byte(raw), w3tcCacheOff)
	return string(out), err
}
