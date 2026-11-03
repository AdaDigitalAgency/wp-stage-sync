package sync

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/AdaDigitalAgency/wp-stage-sync/internal/wpcli"
	"github.com/AdaDigitalAgency/wp-stage-sync/internal/wpconfig"
)

// objectCache is the kind of persistent object cache drop-in a site has.
type objectCache int

const (
	cacheNone            objectCache = iota // no drop-in: the cache lives only for one request
	cacheRedis                              // Redis Object Cache by Till Krüss
	cacheLiteSpeed                          // LiteSpeed Cache object cache (Redis or Memcached)
	cacheOCP                                // Object Cache Pro
	cacheW3TC                               // W3 Total Cache object cache
	cacheHummingbird                        // Hummingbird Redis object cache
	cacheWPRedis                            // WP Redis (Pantheon)
	cacheMemcached                          // Memcached drop-in 4.x (flush rotates a key)
	cacheMemcachedServer                    // Memcached drop-in whose flush clears the whole server
	cacheDocket                             // Docket Cache (files in stage wp-content)
	cacheSQLite                             // SQLite Object Cache (file in stage wp-content)
	cacheAPCu                               // atec Cache APCu (web server memory)
	cacheOther                              // any other drop-in: flush scope is unknown
)

func (c objectCache) String() string {
	switch c {
	case cacheRedis:
		return "Redis Object Cache"
	case cacheLiteSpeed:
		return "LiteSpeed Cache object cache"
	case cacheOCP:
		return "Object Cache Pro"
	case cacheW3TC:
		return "W3 Total Cache object cache"
	case cacheHummingbird:
		return "Hummingbird Redis cache"
	case cacheWPRedis:
		return "WP Redis"
	case cacheMemcached, cacheMemcachedServer:
		return "Memcached object cache"
	case cacheDocket:
		return "Docket Cache"
	case cacheSQLite:
		return "SQLite Object Cache"
	case cacheAPCu:
		return "atec Cache APCu"
	case cacheOther:
		return "an object cache drop-in"
	}
	return "no persistent object cache"
}

// detectObjectCache reads the object-cache.php drop-in of a WordPress root.
func detectObjectCache(root string) objectCache {
	f, err := os.Open(filepath.Join(root, "wp-content", "object-cache.php"))
	if err != nil {
		return cacheNone
	}
	defer f.Close()
	head := make([]byte, 4096)
	n, _ := f.Read(head)
	switch text := string(head[:n]); {
	case strings.Contains(text, "Redis Object Cache Drop-In"):
		return cacheRedis
	case strings.Contains(text, "LSCWP_OBJECT_CACHE"):
		return cacheLiteSpeed
	case strings.Contains(text, "Object Cache Pro"):
		return cacheOCP
	case strings.Contains(text, "W3 Total Cache Object Cache"):
		return cacheW3TC
	case strings.Contains(text, "Hummingbird Redis Object Cache"):
		return cacheHummingbird
	case strings.Contains(text, "WP_REDIS_OBJECT_CACHE"):
		return cacheWPRedis
	case strings.Contains(text, "Docket Cache Drop-in"):
		return cacheDocket
	case strings.Contains(text, "SQLite Object Cache (Drop-in)"):
		return cacheSQLite
	case strings.Contains(text, "atec Object-Cache"):
		return cacheAPCu
	case memcachedV4Re.MatchString(text):
		return cacheMemcached
	case strings.Contains(text, "Plugin Name: Memcached"):
		// Memcached Redux and versions before 4.0 call flush_all.
		return cacheMemcachedServer
	}
	return cacheOther
}

// memcachedV4Re matches the wordpress.org Memcached drop-in from 4.0 on. Its
// flush() writes a new flush number under the salt and table prefix instead
// of clearing the server.
var memcachedV4Re = regexp.MustCompile(`Plugin Name: Memcached\s+Description: Memcached backend for the WP Object Cache\.\s+Version: ([4-9]|\d\d)\.`)

// cachePrefix returns the key prefix a wp-config.php sets for a drop-in kind.
// Redis Object Cache uses WP_REDIS_PREFIX, else WP_CACHE_KEY_SALT, which the
// drop-in maps to it. Hummingbird uses WP_REDIS_SALT; WP Redis uses
// WP_CACHE_KEY_SALT. It is empty when the constant is not a string literal.
func cachePrefix(root string, kind objectCache) string {
	cfg, err := wpconfig.Parse(filepath.Join(root, "wp-config.php"))
	if err != nil {
		return ""
	}
	switch kind {
	case cacheHummingbird:
		return strings.TrimSpace(cfg.Defines["WP_REDIS_SALT"])
	case cacheWPRedis:
		// WP Redis removes all whitespace from its keys.
		return strings.Join(strings.Fields(cfg.Defines["WP_CACHE_KEY_SALT"]), "")
	}
	if p := strings.TrimSpace(cfg.Defines["WP_REDIS_PREFIX"]); p != "" {
		return p
	}
	return strings.TrimSpace(cfg.Defines["WP_CACHE_KEY_SALT"])
}

// redisPrefixFlush deletes only the keys that start with the stage prefix,
// through the connection of the Redis Object Cache drop-in. It replaces the
// drop-in's own selective flush, whose glob_quote() breaks on "[" and "]" and
// which reports a failure when no key matches.
const redisPrefixFlush = `$c = $GLOBALS['wp_object_cache'];
if (!is_object($c) || !method_exists($c, 'redis_instance')) { WP_CLI::error('not a Redis Object Cache connection'); }
$p = defined('WP_REDIS_PREFIX') ? trim(WP_REDIS_PREFIX) : '';
if ($p === '') { WP_CLI::error('no WP_REDIS_PREFIX'); }
$r = $c->redis_instance();
$m = preg_replace('/([*?\[\]\\\\])/', '\\\\$1', $p) . '*';
$n = 0;
if ($r instanceof \Redis) {
	$r->setOption(\Redis::OPT_SCAN, \Redis::SCAN_RETRY);
	$it = null;
	while (($keys = $r->scan($it, $m, 1000)) !== false) { if ($keys) { $n += $r->del($keys); } }
} elseif ($r instanceof \Predis\ClientInterface) {
	$cur = '0';
	do { list($cur, $keys) = $r->scan($cur, ['match' => $m, 'count' => 1000]); if ($keys) { $n += $r->del($keys); } } while ((string) $cur !== '0');
} else { WP_CLI::error('unsupported Redis client'); }
WP_CLI::success("Deleted $n stage cache keys.");`

// wpRedisPrefixFlush deletes the keys that start with WP_CACHE_KEY_SALT through
// the WP Redis (Pantheon) connection. Its own flush() always runs FLUSHDB.
const wpRedisPrefixFlush = `$c = $GLOBALS['wp_object_cache'];
$r = is_object($c) && isset($c->redis) ? $c->redis : null;
if (!$r instanceof \Redis || !$c->is_redis_connected) { WP_CLI::error('WP Redis is not connected on stage'); }
$p = preg_replace('/\s+/', '', defined('WP_CACHE_KEY_SALT') ? WP_CACHE_KEY_SALT : ''); // WP Redis strips whitespace from keys
if ($p === '') { WP_CLI::error('no WP_CACHE_KEY_SALT'); }
$m = preg_replace('/([*?\[\]\\\\])/', '\\\\$1', $p) . '*';
$r->setOption(\Redis::OPT_SCAN, \Redis::SCAN_RETRY);
$n = 0; $it = null;
while (($keys = $r->scan($it, $m, 1000)) !== false) { if ($keys) { $n += $r->del($keys); } }
WP_CLI::success("Deleted $n stage cache keys.");`

// liteSpeedPrefixFlush deletes the stage keys of the LiteSpeed Cache object
// cache. Its own flush() always runs FLUSHDB (Redis) or flush (Memcached), which
// clears live's cache too, because stage gets live's .litespeed_conf.dat. Every
// key starts with LSOC_PREFIX, a SHA-256 of salt, DB_NAME and table prefix, so
// stage and live never share it. Memcached cannot list keys, and older plugin
// versions use a short prefix, so both stop with an error.
const liteSpeedPrefixFlush = `if (!defined('LSOC_PREFIX') || strlen(LSOC_PREFIX) !== 64 || !class_exists('\LiteSpeed\Object_Cache')) { WP_CLI::error('this LiteSpeed Cache version has no 64-character key prefix; update the plugin'); }
$oc = \LiteSpeed\Object_Cache::cls();
$driver = (function () { return $this->_oc_driver; })->call($oc);
if ($driver !== 'Redis') { WP_CLI::error("the $driver backend cannot delete keys by prefix"); }
$r = (function () { return $this->_connect() ? $this->_conn : null; })->call($oc);
if (!$r instanceof \Redis) { WP_CLI::error('no Redis connection'); }
$r->setOption(\Redis::OPT_SCAN, \Redis::SCAN_RETRY);
$n = 0; $it = null;
while (($keys = $r->scan($it, LSOC_PREFIX . ':*', 1000)) !== false) { if ($keys) { $n += $r->del($keys); } }
WP_CLI::success("Deleted $n stage cache keys.");`

var (
	ocpConfigRe   = regexp.MustCompile(`(?s)WP_REDIS_CONFIG['"]\s*,\s*\[(.*?)\]\s*\)\s*;`)
	ocpDatabaseRe = regexp.MustCompile(`['"]database['"]\s*=>\s*(\d+)`)
	ocpMultiRe    = regexp.MustCompile(`['"](cluster|servers|sentinels)['"]\s*=>`)
)

// ocpDatabase reads the Redis database index from the WP_REDIS_CONFIG array
// in a wp-config.php, without loading WordPress. It fails for a config it
// cannot read as a plain array, and for cluster or replication setups.
func ocpDatabase(root string) (int, error) {
	data, err := os.ReadFile(filepath.Join(root, "wp-config.php"))
	if err != nil {
		return 0, err
	}
	m := ocpConfigRe.FindSubmatch(data)
	if m == nil {
		return 0, errors.New("no WP_REDIS_CONFIG array literal")
	}
	body := m[1]
	if ocpMultiRe.Match(body) {
		return 0, errors.New("cluster, servers or sentinels setup")
	}
	d := ocpDatabaseRe.FindSubmatch(body)
	if d == nil {
		return 0, nil // Object Cache Pro defaults to database 0
	}
	return strconv.Atoi(string(d[1]))
}

// checkOCPDatabases refuses a stage that would use live's Redis database.
func checkOCPDatabases(livePath, stagePath string) error {
	live, err := ocpDatabase(livePath)
	if err != nil {
		return fmt.Errorf("Object Cache Pro: cannot read live's Redis database from WP_REDIS_CONFIG (%v)", err)
	}
	stage, err := ocpDatabase(stagePath)
	if err != nil {
		return fmt.Errorf("Object Cache Pro: cannot read stage's Redis database from WP_REDIS_CONFIG (%v). "+
			"Give stage its own 'database' index in %s", err, filepath.Join(stagePath, "wp-config.php"))
	}
	if live == stage {
		return fmt.Errorf("Object Cache Pro: stage and live both use Redis database %d. A stage boot after the sync "+
			"can run OCP's integrity FLUSHDB on it and clear live's cache. Give stage its own 'database' index in %s",
			live, filepath.Join(stagePath, "wp-config.php"))
	}
	return nil
}

// ocpFlush flushes stage's own Redis database. FLUSHDB clears only the selected
// database, so it is safe when stage and live use different indexes. A prefix
// delete is not safe with a shared database: it removes OCP's metadata key,
// and the next stage boot runs an integrity FLUSHDB on the shared database.
const ocpFlush = `$oc = $GLOBALS['wp_object_cache'] ?? null;
if (!is_object($oc) || !method_exists($oc, 'config') || !method_exists($oc, 'connection') || !$oc->connection()) { WP_CLI::error('Object Cache Pro is not connected on stage'); }
$c = $oc->config();
if ($c->cluster || $c->servers || $c->sentinels) { WP_CLI::error('cluster or replication setup: a flush could reach other databases'); }
if ((int) $c->database === %d) { WP_CLI::error('stage uses the same Redis database as live (' . (int) $c->database . '); give stage its own database in WP_REDIS_CONFIG'); }
if (!$oc->flush()) { WP_CLI::error('the flush returned false'); }
WP_CLI::success('Flushed stage Redis database ' . (int) $c->database . '.');`

// ocpFlushPlan returns the flush code for Object Cache Pro, or a reason to skip.
func ocpFlushPlan(livePath string) (string, string) {
	db, err := ocpDatabase(livePath)
	if err != nil {
		return "", "cannot read live's Redis database from WP_REDIS_CONFIG (" + err.Error() + "), so a stage FLUSHDB could clear live's cache"
	}
	return fmt.Sprintf(ocpFlush, db), ""
}

// flushPlan decides how the stage object cache is flushed. A flush can reach a
// cache server that live also uses, so it runs only when its scope is stage.
// It returns the eval code for a Redis prefix flush, or a reason to skip.
func flushPlan(kind objectCache, stagePrefix, livePrefix string) (eval string, skipNote string) {
	switch {
	case kind == cacheNone:
		return "", ""
	case kind == cacheRedis && stagePrefix == "":
		return "", "stage has no WP_REDIS_PREFIX or WP_CACHE_KEY_SALT, so a flush would clear the whole Redis database, live included"
	case kind == cacheRedis && strings.HasPrefix(livePrefix, stagePrefix):
		return "", "live's cache prefix starts with stage's prefix, so a stage flush would also delete live keys"
	case kind == cacheRedis:
		return redisPrefixFlush, ""
	case kind == cacheLiteSpeed:
		return liteSpeedPrefixFlush, ""
	case kind == cacheWPRedis && stagePrefix == "":
		return "", "stage has no WP_CACHE_KEY_SALT, and WP Redis flushes the whole Redis database, live included"
	case kind == cacheWPRedis && strings.HasPrefix(livePrefix, stagePrefix):
		return "", "live's WP_CACHE_KEY_SALT starts with stage's, so a stage flush would also delete live keys"
	case kind == cacheWPRedis:
		return wpRedisPrefixFlush, ""
	case kind == cacheHummingbird && stagePrefix == "":
		return "", "stage has no WP_REDIS_SALT, so Hummingbird's flush runs FLUSHDB on the database live can share"
	case kind == cacheHummingbird && strings.HasPrefix(livePrefix, stagePrefix):
		return "", "live's WP_REDIS_SALT starts with stage's, so a stage flush would also delete live keys"
	case kind == cacheHummingbird:
		return "", "" // with a salt, its flush deletes only keys that start with it
	case kind == cacheW3TC, kind == cacheMemcached, kind == cacheSQLite, kind == cacheDocket:
		// W3TC keys carry stage's own instance_id; Memcached 4.x rotates a
		// key under stage's salt and prefix; Docket and SQLite are files in
		// stage's wp-content. The isolation probe has passed for all four.
		return "", ""
	case kind == cacheMemcachedServer:
		return "", "this Memcached drop-in flushes the whole Memcached server, live included"
	case kind == cacheAPCu:
		return "", "WP-CLI cannot reach the web server's APCu memory. Clear the object cache in the stage admin (atec Cache APCu)"
	}
	return "", "the flush scope of this object cache drop-in is unknown, so it could clear live's cache too"
}

// PreflightObjectCache runs before the database import. If live has a
// persistent object cache drop-in, the file sync will copy it to stage, so it
// is put on stage now and the isolation probe runs. On refusal, stage gets its
// previous drop-in back and nothing else on stage has changed.
func PreflightObjectCache(livePath, stagePath string, cacheOff bool, log interface{ Detail(string) }) error {
	kind := detectObjectCache(livePath)
	if kind == cacheNone {
		return nil
	}
	if cacheOff {
		// The file sync will not copy the drop-in. Remove stage's own now, so
		// stage never uses the shared backend during or after the sync.
		log.Detail(fmt.Sprintf("Stage object cache is off: live's %s is not copied to stage", kind))
		return removeStageDropIn(stagePath, log)
	}
	if kind == cacheAPCu {
		// APCu is memory of one PHP process pool, which WP-CLI never shares, so
		// the probe cannot pass or prove anything. atec keys include DB_NAME,
		// so stage and live keys differ even in one pool.
		log.Detail("atec Cache APCu: keys include the database name, so stage and live do not share keys (no WP-CLI probe)")
		return nil
	}
	if shared := SharedObjectCache(livePath, stagePath); shared != "" {
		log.Detail("WARNING: stage and live share " + shared + ". The sync flushes only stage keys, " +
			"but a cache purge in the stage admin can clear live's cache. Give stage its own database, " +
			"or switch the stage object cache off for this site")
	}
	// Object Cache Pro must be checked without loading stage: on a database
	// that live also uses, stage's first boot after a config or version change
	// runs an integrity FLUSHDB that clears live's cache.
	if kind == cacheOCP {
		if err := checkOCPDatabases(livePath, stagePath); err != nil {
			return fmt.Errorf("%w. Nothing on stage was changed", err)
		}
	}
	wpBase, err := wpcli.Resolve()
	if err != nil {
		return fmt.Errorf("wp-cli: %w", err)
	}
	livePHP := filepath.Join(livePath, "wp-content", "object-cache.php")
	stagePHP := filepath.Join(stagePath, "wp-content", "object-cache.php")
	liveCopy, err := os.ReadFile(livePHP)
	if err != nil {
		return err
	}
	// The file sync and FixLivePaths will produce exactly this file.
	fixed, _ := replaceLivePath(string(liveCopy), livePath, stagePath)
	want := []byte(fixed)
	old, readErr := os.ReadFile(stagePHP)
	hadOld := readErr == nil
	replaced := !hadOld || !bytes.Equal(old, want)
	if replaced {
		if err := os.WriteFile(stagePHP, want, 0644); err != nil {
			return fmt.Errorf("copying the drop-in to stage: %w", err)
		}
		uid, gid := detectOwnership(stagePath)
		_ = os.Chown(stagePHP, int(uid), int(gid))
	}
	restore := func() {
		if !replaced {
			return
		}
		if hadOld {
			_ = os.WriteFile(stagePHP, old, 0644)
		} else {
			_ = os.Remove(stagePHP)
		}
	}

	// The W3TC drop-in reads w3tc-config/master.php. Stage gets live's file
	// with its own instance_id, as FixLivePaths will write it after the sync.
	restoreW3TC := func() {}
	if kind == cacheW3TC {
		var err error
		if restoreW3TC, err = stageW3TCConfigForProbe(livePath, stagePath); err != nil {
			restore()
			return fmt.Errorf("W3 Total Cache config (nothing on stage was changed): %w", err)
		}
	}

	log.Detail(fmt.Sprintf("Checking that stage and live do not share %s", kind))
	if err := checkCacheIsolation(wpBase, livePath, stagePath); err != nil {
		restore()
		restoreW3TC()
		if errors.Is(err, ErrSharedObjectCache) {
			return fmt.Errorf("%w. Nothing on stage was changed: a sync would make stage read and write live's cache. "+
				"Give stage its own WP_CACHE_KEY_SALT (or WP_REDIS_PREFIX) in %s, then sync again",
				err, filepath.Join(stagePath, "wp-config.php"))
		}
		return fmt.Errorf("object cache check (nothing on stage was changed): %w", err)
	}
	return nil
}

// ErrSharedObjectCache means stage and live read and write the same cache keys.
var ErrSharedObjectCache = errors.New("stage and live share object cache keys")

// noCron stops WP-CLI from spawning WP-Cron while it loads live.
const noCron = "defined('DISABLE_WP_CRON') || define('DISABLE_WP_CRON', true);"

// checkCacheIsolation writes a probe key through stage and reads it through
// live. If live can read it, any stage WP-CLI command would write into live's
// cache, so the caller must stop. Live is only read, with plugins and themes
// skipped and WP-Cron off.
func checkCacheIsolation(wpBase []string, livePath, stagePath string) error {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return err
	}
	key, value := "probe-"+hex.EncodeToString(buf), hex.EncodeToString(buf)
	const group = "wp-stage-sync"
	common := []string{"--allow-root", "--skip-plugins", "--skip-themes"}
	run := func(path string, args ...string) (string, error) {
		full := append(append(append([]string{}, wpBase...), args...), common...)
		full = append(full, "--path="+path, "--exec="+noCron)
		cmd := exec.Command(full[0], full[1:]...)
		out, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(out)), CommandError(err, out)
	}

	if _, err := run(stagePath, "cache", "set", key, value, group, "300"); err != nil {
		return fmt.Errorf("writing the probe key on stage: %w", err)
	}
	defer run(stagePath, "cache", "delete", key, group)

	out, err := run(livePath, "cache", "get", key, group)
	switch {
	case err == nil && out == value:
		return ErrSharedObjectCache
	case err != nil && strings.Contains(out, "not found"):
		return nil
	case err != nil:
		return fmt.Errorf("reading the probe key on live: %w", err)
	}
	return fmt.Errorf("unexpected probe value on live: %q", out)
}

// ScopedFlushArgs returns the WP-CLI command that flushes the object cache of
// the site at path without clearing other sites on a shared backend, or a
// reason to skip. Promote uses it on live. Cache and CDN plugins are skipped,
// so no purge hook runs.
func ScopedFlushArgs(wpBase []string, path string) ([]string, string) {
	kind := detectObjectCache(path)
	base := append([]string{}, wpBase...)
	tail := []string{"--allow-root", "--path=" + path, "--skip-plugins=" + strings.Join(sideEffectPlugins, ",")}
	plain := append(append(base, "cache", "flush"), tail...)
	switch kind {
	case cacheNone, cacheOCP:
		// OCP flushes only the site's own database index.
		return plain, ""
	}
	eval, skip := flushPlan(kind, cachePrefix(path, kind), "")
	if skip != "" {
		return nil, skip
	}
	if eval == "" {
		return plain, ""
	}
	return append(append(base, "eval", eval), tail...), ""
}
