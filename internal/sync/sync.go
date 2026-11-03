package sync

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/AdaDigitalAgency/wp-stage-sync/internal/config"
	"github.com/AdaDigitalAgency/wp-stage-sync/internal/export"
	"github.com/AdaDigitalAgency/wp-stage-sync/internal/progress"
	"github.com/AdaDigitalAgency/wp-stage-sync/internal/wpcli"
)

// backupPrefix names tables moved aside during import so a failure can roll back.
const backupPrefix = "_wpss_bak_"

// Import executes Step 1: move existing staging tables aside, import the
// exported SQL, then drop the backups (or restore them if the import fails).
func Import(stageDB *sql.DB, exp *export.Result, log progress.Logger) error {
	log.Detail("Disabling foreign key checks")
	if _, err := stageDB.Exec("SET FOREIGN_KEY_CHECKS=0"); err != nil {
		return fmt.Errorf("disabling FK checks: %w", err)
	}
	// Raise the packet limit for this session so a large INSERT (e.g. a big
	// post_content) doesn't reset the connection. This is session-settable on
	// MariaDB; on MySQL it is read-only, so we surface the limitation rather
	// than SET GLOBAL it (which would affect every database on the server).
	if _, err := stageDB.Exec("SET SESSION max_allowed_packet=268435456"); err != nil {
		log.Detail("Note: could not raise session max_allowed_packet; raise the server's max_allowed_packet if a very large row fails to import")
	}

	tables, err := listTables(stageDB)
	if err != nil {
		return fmt.Errorf("listing tables: %w", err)
	}

	// Move existing tables aside (a RENAME is instant — no data copy) so a
	// failed import can be rolled back. Drop any stale backups left by a
	// previously crashed run first.
	var live []string
	for _, t := range tables {
		if strings.HasPrefix(t, backupPrefix) {
			if _, err := stageDB.Exec(fmt.Sprintf("DROP TABLE IF EXISTS `%s`", t)); err != nil {
				return fmt.Errorf("clearing stale backup %s: %w", t, err)
			}
			continue
		}
		live = append(live, t)
	}

	log.Detail(fmt.Sprintf("Backing up %d existing tables", len(live)))
	backups := make(map[string]string, len(live)) // backup name -> original name
	for i, t := range live {
		bak := fmt.Sprintf("%s%d", backupPrefix, i)
		if _, err := stageDB.Exec(fmt.Sprintf("RENAME TABLE `%s` TO `%s`", t, bak)); err != nil {
			restoreBackups(stageDB, backups, log)
			return fmt.Errorf("backing up %s: %w", t, err)
		}
		backups[bak] = t
	}

	// Import; restore the backups if anything fails partway through.
	if err := execImport(stageDB, exp, log); err != nil {
		log.Detail("Import failed — restoring previous staging tables")
		restoreBackups(stageDB, backups, log)
		return err
	}

	log.Detail("Removing backup tables")
	for bak := range backups {
		if _, err := stageDB.Exec(fmt.Sprintf("DROP TABLE IF EXISTS `%s`", bak)); err != nil {
			return fmt.Errorf("removing backup %s: %w", bak, err)
		}
	}

	log.Detail("Re-enabling foreign key checks")
	if _, err := stageDB.Exec("SET FOREIGN_KEY_CHECKS=1"); err != nil {
		return fmt.Errorf("re-enabling FK checks: %w", err)
	}
	return nil
}

// execImport runs the exported statements in order: schema-only, base, users, orders.
func execImport(stageDB *sql.DB, exp *export.Result, log progress.Logger) error {
	groups := []struct {
		name  string
		stmts []string
	}{
		{"schema-only", exp.SchemaOnly},
		{"base", exp.Base},
		{"users", exp.Users},
		{"orders", exp.Orders},
	}
	totalStmts := 0
	for _, g := range groups {
		totalStmts += len(g.stmts)
	}
	doneStmts := 0
	for _, g := range groups {
		log.Detail(fmt.Sprintf("Importing %s (%d statements)", g.name, len(g.stmts)))
		for _, stmt := range g.stmts {
			if _, err := stageDB.Exec(stmt); err != nil {
				// Include first 200 chars of statement in error for debugging
				preview := stmt
				if len(preview) > 200 {
					preview = preview[:200] + "..."
				}
				return fmt.Errorf("executing %s SQL: %w\n  stmt: %s", g.name, err, preview)
			}
			doneStmts++
			log.Progress(doneStmts, totalStmts)
		}
	}
	return nil
}

// restoreBackups drops any partially-imported tables and renames the backups
// back to their original names. Best-effort: warnings are logged, not returned.
func restoreBackups(stageDB *sql.DB, backups map[string]string, log progress.Logger) {
	if cur, err := listTables(stageDB); err == nil {
		for _, t := range cur {
			if strings.HasPrefix(t, backupPrefix) {
				continue
			}
			_, _ = stageDB.Exec(fmt.Sprintf("DROP TABLE IF EXISTS `%s`", t))
		}
	}
	for bak, orig := range backups {
		if _, err := stageDB.Exec(fmt.Sprintf("RENAME TABLE `%s` TO `%s`", bak, orig)); err != nil {
			log.Detail(fmt.Sprintf("WARNING: could not restore %s from backup %s: %v", orig, bak, err))
		}
	}
}

// DefaultExcludes are the default rsync excludes, relative to wp-content.
var DefaultExcludes = []string{
	"cache",
	"ewww",
	"critical-css",
	"litespeed",
	"updraft",
	"archive-master-db",
	"boost-cache/cache",
	"wphb-cache/cache",
	"wphb-logs",
	"wp-cloudflare-super-page-cache",
	".ht.object-cache*.sqlite*",
}

// maxOutputLines limits how much command output CommandError keeps. A failing
// rsync or chown can print one line per file.
const maxOutputLines = 10

// CommandError adds the last lines of a command's output to err, because the
// exit status alone does not say why the command failed.
func CommandError(err error, output []byte) error {
	if err == nil {
		return nil
	}
	text := strings.TrimSpace(string(output))
	if text == "" {
		return err
	}
	lines := strings.Split(text, "\n")
	if len(lines) > maxOutputLines {
		lines = lines[len(lines)-maxOutputLines:]
	}
	return fmt.Errorf("%w: %s", err, strings.Join(lines, "\n"))
}

// ResolveExcludes returns the excludes to use for a sync. Nil means none were
// ever chosen, so the defaults apply. An empty list means the user unchecked
// every folder, so nothing is excluded.
func ResolveExcludes(saved []string) []string {
	if saved == nil {
		return DefaultExcludes
	}
	return saved
}

// excludeArgs builds rsync --exclude flags. Each exclude is anchored to the
// transfer root (wp-content), because rsync matches a pattern with no leading
// slash at every depth: "cache" would also skip plugins/foo/cache.
func excludeArgs(excludes []string) []string {
	args := make([]string, 0, len(excludes))
	for _, ex := range excludes {
		args = append(args, "--exclude=/"+strings.TrimPrefix(ex, "/"))
	}
	return args
}

// FileSync executes Step 2: rsync wp-content and fix ownership.
func FileSync(livePath, stagePath string, excludes []string, log progress.Logger) error {
	src := filepath.Join(livePath, "wp-content") + "/"
	dst := filepath.Join(stagePath, "wp-content") + "/"

	log.Detail(fmt.Sprintf("rsync %s → %s", src, dst))
	args := []string{"-a", "--delete"}
	args = append(args, excludeArgs(excludes)...)
	args = append(args, src, dst)
	cmd := exec.Command("rsync", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("rsync: %w", CommandError(err, out))
	}

	// Detect owner:group from staging webroot
	uid, gid := detectOwnership(stagePath)
	log.Detail(fmt.Sprintf("Fixing ownership to %d:%d", uid, gid))
	chown := exec.Command("chown", "-R", fmt.Sprintf("%d:%d", uid, gid), dst)
	if out, err := chown.CombinedOutput(); err != nil {
		return fmt.Errorf("chown: %w", CommandError(err, out))
	}

	return nil
}

// PlannedCommand is a single WP-CLI command in the post-processing step.
type PlannedCommand struct {
	Label    string
	Args     []string
	Skip     bool
	SkipNote string
	// Optional commands only clear caches. A failure is a warning, because
	// the stage data is already correct.
	Optional bool
	// Remove lists stage folders whose contents are deleted instead of
	// running Args.
	Remove []string
	// RemoveFiles lists glob patterns of stage files to delete.
	RemoveFiles []string
}

// hostFromURL returns the bare host (no scheme, no path) of a site URL such as
// "https://example.com/blog" → "example.com". If s is already a bare host it is
// returned cleaned.
func hostFromURL(s string) string {
	s = strings.TrimSpace(s)
	if u, err := url.Parse(s); err == nil && u.Host != "" {
		return u.Host
	}
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimPrefix(s, "//")
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	return s
}

// StageHost returns the stage host. The stage database holds live's URLs when
// an earlier sync stopped before its search-replace, so a host equal to live's
// is replaced by the stage directory name.
func StageHost(db *sql.DB, prefix, stagePath, liveHost string) (string, error) {
	dirHost := filepath.Base(strings.TrimRight(stagePath, "/"))
	return pickStageHost(SiteHost(db, prefix, dirHost), dirHost, liveHost)
}

func pickStageHost(dbHost, dirHost, liveHost string) (string, error) {
	if dbHost != liveHost {
		return dbHost, nil
	}
	if dirHost != liveHost && dirHost != "" && dirHost != "." && dirHost != "/" {
		return dirHost, nil
	}
	return "", fmt.Errorf("cannot tell the stage host from the live host (%s)", liveHost)
}

// SiteHost returns the host of the site's home/siteurl from the options table,
// falling back to the provided default when it cannot be read.
func SiteHost(db *sql.DB, prefix, fallback string) string {
	for _, opt := range []string{"home", "siteurl"} {
		var val string
		q := fmt.Sprintf("SELECT option_value FROM `%soptions` WHERE option_name = ? LIMIT 1", prefix)
		if err := db.QueryRow(q, opt).Scan(&val); err == nil {
			if h := hostFromURL(val); h != "" {
				return h
			}
		}
	}
	return fallback
}

// postProcessCommands builds the ordered WP-CLI command list (with skip
// decisions) shared by PostProcess and PostProcessPlan. wpBase is the resolved
// wp-cli invocation, e.g. ["wp"] or ["php", "/path/to/wp-cli.phar"].
func postProcessCommands(wpBase []string, livePath, stagePath, liveHost, stageHost string, cache objectCache) []PlannedCommand {
	// Every stage command skips cache and CDN plugins. Their hooks can call a
	// full purge: LiteSpeed Cache runs purge_all() when Elementor clears its
	// files cache, which flushes the shared object cache and can purge the
	// live Cloudflare zone with the credentials copied from live.
	wpArgs := func(args ...string) []string {
		cmd := make([]string, len(wpBase), len(wpBase)+len(args)+1)
		copy(cmd, wpBase)
		return append(append(cmd, args...), "--skip-plugins="+strings.Join(sideEffectPlugins, ","))
	}
	// The LiteSpeed page purge needs LiteSpeed Cache itself loaded.
	wpWithLiteSpeed := func(args ...string) []string {
		var others []string
		for _, p := range sideEffectPlugins {
			if p != "litespeed-cache" {
				others = append(others, p)
			}
		}
		cmd := append(append([]string{}, wpBase...), args...)
		return append(cmd, "--skip-plugins="+strings.Join(others, ","))
	}

	hasElementor := dirExists(filepath.Join(stagePath, "wp-content", "plugins", "elementor"))
	hasJetpack := dirExists(filepath.Join(stagePath, "wp-content", "plugins", "jetpack"))
	hasLiteSpeed := dirExists(filepath.Join(stagePath, "wp-content", "plugins", "litespeed-cache"))
	hasRocket := dirExists(filepath.Join(stagePath, "wp-content", "plugins", "wp-rocket"))
	hasWPFC := dirExists(filepath.Join(stagePath, "wp-content", "plugins", "wp-fastest-cache"))
	hasSuperCache := dirExists(filepath.Join(stagePath, "wp-content", "plugins", "wp-super-cache"))
	hasPowered := dirExists(filepath.Join(stagePath, "wp-content", "plugins", "powered-cache"))

	var cmds []PlannedCommand

	// Replace https, then http, then protocol-relative. Doing the scheme-
	// prefixed forms first means the bare "//host" pass only hits genuine
	// protocol-relative URLs, and never a bare hostname (which would corrupt
	// email addresses and the like).
	for _, p := range []string{"https://", "http://", "//"} {
		from, to := p+liveHost, p+stageHost
		cmds = append(cmds, PlannedCommand{
			Label: fmt.Sprintf("Search-replace: %s → %s", from, to),
			Args: wpArgs("search-replace", from, to,
				"--all-tables", "--allow-root", "--path="+stagePath),
		})
	}

	// Plugins store absolute file paths: Hummingbird deletes the files in
	// its minify groups' _path meta, and Swift Performance writes to its
	// stored cache-path. With live's paths, stage deletes and writes live files.
	live, stage := strings.TrimRight(livePath, "/")+"/", strings.TrimRight(stagePath, "/")+"/"
	cmds = append(cmds, PlannedCommand{
		Label: fmt.Sprintf("Search-replace: %s → %s", live, stage),
		Args: wpArgs("search-replace", live, stage,
			"--all-tables", "--allow-root", "--path="+stagePath),
		Skip:     strings.HasPrefix(stage, live) || strings.HasPrefix(live, stage),
		SkipNote: "WARNING: one site path is inside the other, so live paths in the stage database stay",
	})

	for _, p := range []string{"https://", "http://"} {
		cmds = append(cmds, PlannedCommand{
			Label: fmt.Sprintf("Elementor URL replace: %s%s → %s%s", p, liveHost, p, stageHost),
			Args: wpArgs("elementor", "replace-urls",
				p+liveHost, p+stageHost, "--allow-root", "--path="+stagePath),
			Skip:     !hasElementor,
			SkipNote: "elementor plugin not installed",
		})
	}

	cmds = append(cmds, PlannedCommand{
		Label:    "Elementor CSS regenerate",
		Args:     wpArgs("elementor", "flush-css", "--allow-root", "--path="+stagePath),
		Skip:     !hasElementor,
		SkipNote: "elementor plugin not installed",
		Optional: true,
	})

	flushEval, flushSkip := flushPlan(cache, cachePrefix(stagePath, cache), cachePrefix(livePath, cache))
	if cache == cacheOCP {
		flushEval, flushSkip = ocpFlushPlan(livePath)
	}
	flushArgs := wpArgs("cache", "flush", "--allow-root", "--path="+stagePath)
	flushLabel := "Cache flush"
	if flushEval != "" {
		flushArgs = wpArgs("eval", flushEval, "--allow-root", "--path="+stagePath)
		flushLabel = "Cache flush (Redis, stage prefix only)"
		if cache == cacheLiteSpeed {
			flushLabel = "Cache flush (LiteSpeed object cache, stage prefix only)"
		}
		if cache == cacheOCP {
			flushLabel = "Cache flush (Object Cache Pro, stage Redis database only)"
		}
		if cache == cacheWPRedis {
			flushLabel = "Cache flush (WP Redis, stage salt only)"
		}
	} else if cache != cacheNone && flushSkip == "" {
		flushLabel = "Cache flush (" + cache.String() + ")"
	}

	cmds = append(cmds,
		PlannedCommand{
			Label: "Jetpack safe mode",
			Args: wpArgs("option", "update", "jetpack_safe_mode_confirmed", "1",
				"--allow-root", "--quiet", "--path="+stagePath),
			Skip:     !hasJetpack,
			SkipNote: "jetpack plugin not installed",
		},
	)

	switch {
	case !config.LoadSettings().AutoCacheFlush:
		cmds = append(cmds, PlannedCommand{Label: flushLabel, Args: flushArgs, Skip: true,
			SkipNote: "auto cache flush disabled in settings"})
	case flushSkip != "":
		cmds = append(cmds, PlannedCommand{Label: flushLabel, Args: flushArgs, Skip: true,
			SkipNote: "WARNING: " + flushSkip + ". Flush the stage cache by hand"})
	default:
		// The stage data is already correct, so a failed flush is a warning.
		cmds = append(cmds, PlannedCommand{Label: flushLabel, Args: flushArgs, Optional: true})
	}

	cmds = append(cmds,
		PlannedCommand{
			// Pages only. `wp litespeed-purge all` also flushes the object
			// cache, resets OPcache and purges Cloudflare with live's keys.
			Label: "LiteSpeed page cache purge",
			Args: wpWithLiteSpeed("eval", liteSpeedPagePurge,
				"--allow-root", "--path="+stagePath),
			Skip:     !hasLiteSpeed,
			SkipNote: "litespeed-cache plugin not installed",
			Optional: true,
		},
		PlannedCommand{
			Label: "WP Rocket cache files",
			Remove: []string{
				filepath.Join(stagePath, "wp-content", "cache", "wp-rocket"),
				filepath.Join(stagePath, "wp-content", "cache", "min"),
				filepath.Join(stagePath, "wp-content", "cache", "busting"),
				filepath.Join(stagePath, "wp-content", "cache", "critical-css"),
				filepath.Join(stagePath, "wp-content", "cache", "used-css"),
				filepath.Join(stagePath, "wp-content", "cache", "background-css"),
				filepath.Join(stagePath, "wp-content", "cache", "fonts"),
			},
			Skip:     !hasRocket,
			SkipNote: "wp-rocket plugin not installed",
			Optional: true,
		},
		PlannedCommand{
			// Paths built from the stage webroot. wp_cache_clear_cache()
			// trusts $cache_path from the config copied from live.
			Label:       "WP Super Cache files",
			Remove:      cacheDirs(stagePath, filepath.Join("supercache", stageHost), "meta", "blogs"),
			RemoveFiles: []string{filepath.Join(stagePath, "wp-content", "cache", "wp-cache-*")},
			Skip:        !hasSuperCache,
			SkipNote:    "wp-super-cache plugin not installed",
			Optional:    true,
		},
		PlannedCommand{
			// powered_cache_flush() fires a Cloudflare zone purge hook.
			Label:    "Powered Cache files",
			Remove:   cacheDirs(stagePath, filepath.Join("powered-cache", stageHost)),
			Skip:     !hasPowered,
			SkipNote: "powered-cache plugin not installed",
			Optional: true,
		},
		PlannedCommand{
			// Folders only: every WPFC purge function also purges Cloudflare.
			Label:    "WP Fastest Cache files",
			Remove:   cacheDirs(stagePath, "all", "wpfc-minified", "wpfc-mobile-cache", "wpfc-widget-cache", "tmpWpfc"),
			Skip:     !hasWPFC,
			SkipNote: "wp-fastest-cache plugin not installed",
			Optional: true,
		},
	)
	// Folders only, for the same reason: the purge functions of these plugins
	// chain into Cloudflare, CDN, Varnish or object cache flushes.
	for _, pc := range pageCaches(stagePath, stageHost) {
		installed := false
		for _, slug := range pc.slugs {
			installed = installed || dirExists(filepath.Join(stagePath, "wp-content", "plugins", slug))
		}
		cmds = append(cmds, PlannedCommand{
			Label: pc.label, Remove: pc.dirs, Skip: !installed,
			SkipNote: pc.slugs[0] + " plugin not installed", Optional: true,
		})
	}
	return cmds
}

// pageCache is a plugin whose stage cache folders the sync empties.
type pageCache struct {
	label string
	slugs []string
	dirs  []string
}

// pageCaches lists the cache folders of more page cache plugins. The file sync
// excludes them (or they are keyed by host), so they hold stage's own pages
// built from the old stage database.
func pageCaches(stagePath, stageHost string) []pageCache {
	content := filepath.Join(stagePath, "wp-content")
	return []pageCache{
		{"W3 Total Cache files", []string{"w3-total-cache"},
			cacheDirs(stagePath, filepath.Join("page_enhanced", stageHost), "page", "minify", "db", "object")},
		{"WP-Optimize files", []string{"wp-optimize"},
			cacheDirs(stagePath, filepath.Join("wpo-cache", stageHost), "wpo-minify")},
		{"Breeze files", []string{"breeze"}, cacheDirs(stagePath, "breeze", "breeze-minification", "breeze-extra")},
		{"Cache Enabler files", []string{"cache-enabler"}, cacheDirs(stagePath, filepath.Join("cache-enabler", stageHost))},
		{"Comet Cache files", []string{"comet-cache"}, cacheDirs(stagePath,
			filepath.Join("comet-cache", "cache", "https", stageHost), filepath.Join("comet-cache", "cache", "http", stageHost))},
		{"Swift Performance files", []string{"swift-performance-lite", "swift-performance"},
			cacheDirs(stagePath, filepath.Join("swift-performance", stageHost))},
		{"Autoptimize files", []string{"autoptimize"}, cacheDirs(stagePath, "autoptimize")},
		{"SiteGround Optimizer files", []string{"sg-cachepress"}, cacheDirs(stagePath, "sgo-cache")},
		{"Jetpack Boost files", []string{"jetpack-boost"}, append(cacheDirs(stagePath, "page_optimize"),
			filepath.Join(content, "boost-cache", "cache", stageHost))},
		{"Hummingbird files", []string{"hummingbird-performance", "wp-hummingbird"}, []string{
			filepath.Join(content, "wphb-cache", "cache", stageHost),
			filepath.Join(content, "wphb-cache", "cache", "mobile", stageHost)}},
		{"Super Page Cache files", []string{"wp-cloudflare-page-cache"}, []string{
			filepath.Join(content, "wp-cloudflare-super-page-cache", stageHost, "fallback_cache")}},
	}
}

// sideEffectPlugins are skipped in every stage WP-CLI command. They hook
// content and cache events to purge caches or a CDN, and the stage database
// holds live's settings and API keys for those purges.
var sideEffectPlugins = []string{
	"litespeed-cache", "wp-rocket", "w3-total-cache", "wp-super-cache",
	"wp-fastest-cache", "sg-cachepress", "breeze", "cloudflare",
	"hummingbird-performance", "nitropack", "wp-optimize", "autoptimize",
	"swift-performance", "swift-performance-lite", "cache-enabler", "comet-cache",
	"powered-cache", "object-cache-pro", "wp-cloudflare-page-cache", "wp-hummingbird",
	"nginx-helper", "varnish-http-purge", "jetpack-boost", "atec-cache-apcu",
	"docket-cache", "sqlite-object-cache", "wp-redis", "redis-cache", "flying-press",
}

// liteSpeedPagePurge queues a purge of the stage pages and sends it to the web
// server, without the object cache, OPcache or Cloudflare steps of purge_all().
const liteSpeedPagePurge = `if (!class_exists('\LiteSpeed\Purge') || !method_exists('\LiteSpeed\Core', 'kick_purge_queue')) { WP_CLI::error('LiteSpeed Cache is not active or too old'); }
\LiteSpeed\Purge::purge_all_lscache('wp-stage-sync');
if (!\LiteSpeed\Core::kick_purge_queue()) { WP_CLI::error('Could not notify the web server to purge.'); }
WP_CLI::success('Stage page cache purge sent to the web server.');`

func cacheDirs(stagePath string, names ...string) []string {
	dirs := make([]string, len(names))
	for i, n := range names {
		dirs[i] = filepath.Join(stagePath, "wp-content", "cache", n)
	}
	return dirs
}

// removeFiles deletes the regular files that match pattern. The pattern must
// be inside the stage wp-content/cache folder.
func removeFiles(stagePath, pattern string) error {
	base := filepath.Join(stagePath, "wp-content", "cache") + string(filepath.Separator)
	if !strings.HasPrefix(filepath.Clean(pattern), base) {
		return fmt.Errorf("refusing to delete %s: not under %s", pattern, base)
	}
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return err
	}
	for _, m := range matches {
		if info, err := os.Lstat(m); err == nil && info.Mode().IsRegular() {
			if err := os.Remove(m); err != nil {
				return err
			}
		}
	}
	return nil
}

// clearRoots are the wp-content folders that hold only cache data.
var clearRoots = []string{"cache", filepath.Join("boost-cache", "cache"), filepath.Join("wphb-cache", "cache"), "wp-cloudflare-super-page-cache"}

// clearDir deletes the contents of dir and keeps dir. It refuses any path
// outside the stage cache folders.
func clearDir(stagePath, dir string) error {
	// Strictly under a cache root: a root itself is not a valid target.
	under := false
	for _, root := range clearRoots {
		base := filepath.Join(stagePath, "wp-content", root) + string(filepath.Separator)
		under = under || strings.HasPrefix(filepath.Clean(dir), base)
	}
	if !under {
		return fmt.Errorf("refusing to clear %s: not under a stage cache folder", dir)
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// PostProcess executes Step 3: WP-CLI search-replace and cache flush.
func PostProcess(livePath, stagePath, liveHost, stageHost string, log progress.Logger) error {
	wpBase, err := wpcli.Resolve()
	if err != nil {
		return fmt.Errorf("wp-cli: %w", err)
	}

	// A search-replace from a host to itself does nothing, and stage would
	// keep live's URLs. PreflightObjectCache already proved cache isolation.
	if liveHost == stageHost {
		return fmt.Errorf("live and stage resolve to the same host (%s): refusing to post-process", liveHost)
	}
	cache := detectObjectCache(stagePath)

	commands := postProcessCommands(wpBase, livePath, stagePath, liveHost, stageHost, cache)
	for i, c := range commands {
		if c.Skip {
			log.Detail(fmt.Sprintf("Skipping: %s (%s)", c.Label, c.SkipNote))
			continue
		}
		log.Detail(c.Label)
		log.Progress(i, len(commands))
		var runErr error
		if len(c.Remove) > 0 || len(c.RemoveFiles) > 0 {
			for _, dir := range c.Remove {
				if runErr = clearDir(stagePath, dir); runErr != nil {
					break
				}
			}
			for _, pattern := range c.RemoveFiles {
				if runErr != nil {
					break
				}
				runErr = removeFiles(stagePath, pattern)
			}
		} else {
			cmd := exec.Command(c.Args[0], c.Args[1:]...)
			out, err := cmd.CombinedOutput()
			if err != nil {
				runErr = fmt.Errorf("running %s: %w", strings.Join(c.Args, " "), CommandError(err, out))
			}
		}
		if runErr != nil {
			if c.Optional {
				log.Detail(fmt.Sprintf("WARNING: %s failed, continuing: %v", c.Label, runErr))
				continue
			}
			return runErr
		}
	}
	log.Progress(len(commands), len(commands))
	return nil
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func detectOwnership(path string) (uint32, uint32) {
	info, err := os.Stat(path)
	if err != nil {
		// Fallback: www-data is typically uid/gid 33
		return 33, 33
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 33, 33
	}
	return stat.Uid, stat.Gid
}

func listTables(db *sql.DB) ([]string, error) {
	rows, err := db.Query("SHOW TABLES")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		tables = append(tables, t)
	}
	return tables, rows.Err()
}

// Anonymize masks personal customer details in the staging database.
func Anonymize(db *sql.DB, prefix string, log progress.Logger) error {
	log.Detail("Anonymizing users and usermeta")

	// 1. Update customer users in users table
	usersQuery := fmt.Sprintf(`
		UPDATE %susers u
		INNER JOIN %susermeta um ON u.ID = um.user_id AND um.meta_key = '%scapabilities'
		SET 
			u.user_email = CONCAT('customer_', u.ID, '@example.com'), 
			u.user_login = CONCAT('customer_', u.ID),
			u.user_nicename = CONCAT('customer_', u.ID),
			u.display_name = CONCAT('Customer ', u.ID)
		WHERE um.meta_value LIKE '%%"customer"%%'
	`, prefix, prefix, prefix)

	if _, err := db.Exec(usersQuery); err != nil {
		return fmt.Errorf("anonymizing users: %w", err)
	}

	// 2. Update customer usermeta billing/shipping
	metaQuery := fmt.Sprintf(`
		UPDATE %susermeta um
		INNER JOIN %susermeta uc ON um.user_id = uc.user_id AND uc.meta_key = '%scapabilities'
		SET um.meta_value = CASE 
			WHEN um.meta_key IN ('first_name', 'billing_first_name', 'shipping_first_name') THEN 'Customer'
			WHEN um.meta_key IN ('last_name', 'billing_last_name', 'shipping_last_name') THEN CONCAT('LN_', um.user_id)
			WHEN um.meta_key = 'billing_email' THEN CONCAT('customer_', um.user_id, '@example.com')
			WHEN um.meta_key = 'billing_phone' THEN '555-0000'
			WHEN um.meta_key IN ('billing_address_1', 'shipping_address_1') THEN '123 Staging Lane'
			WHEN um.meta_key IN ('billing_address_2', 'shipping_address_2', 'billing_company', 'shipping_company') THEN ''
			WHEN um.meta_key = 'shipping_phone' THEN '555-0000'
			WHEN um.meta_key IN ('billing_city', 'shipping_city') THEN 'Staging City'
			WHEN um.meta_key IN ('billing_postcode', 'shipping_postcode') THEN '12345'
			ELSE um.meta_value
		END
		WHERE uc.meta_value LIKE '%%"customer"%%'
		AND um.meta_key IN (
			'first_name', 'billing_first_name', 'shipping_first_name',
			'last_name', 'billing_last_name', 'shipping_last_name',
			'billing_email', 'billing_phone',
			'billing_address_1', 'shipping_address_1',
			'billing_address_2', 'shipping_address_2',
			'billing_city', 'shipping_city',
			'billing_postcode', 'shipping_postcode',
			'billing_company', 'shipping_company', 'shipping_phone'
		)
	`, prefix, prefix, prefix)

	if _, err := db.Exec(metaQuery); err != nil {
		return fmt.Errorf("anonymizing usermeta: %w", err)
	}

	// 3. Update WooCommerce order addresses (HPOS) if table exists
	addressTable := prefix + "wc_order_addresses"
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?", addressTable).Scan(&count)
	if err == nil && count > 0 {
		log.Detail("Anonymizing order addresses")
		addressQuery := fmt.Sprintf(`
			UPDATE %swc_order_addresses
			SET 
				first_name = 'Customer',
				last_name = CONCAT('LN_', order_id),
				company = '',
				address_1 = '123 Staging Lane',
				address_2 = '',
				city = 'Staging City',
				postcode = '12345',
				email = CONCAT('order_', order_id, '@example.com'),
				phone = '555-0000'
		`, prefix)
		if _, err := db.Exec(addressQuery); err != nil {
			return fmt.Errorf("anonymizing order addresses: %w", err)
		}
	}

	// 4. wc_orders (HPOS) holds PII not in wc_order_addresses: order email,
	// customer IP/user-agent, and the free-text customer note.
	ordersTable := prefix + "wc_orders"
	var ordersCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?", ordersTable).Scan(&ordersCount); err == nil && ordersCount > 0 {
		log.Detail("Anonymizing order records")
		ordersQuery := fmt.Sprintf(`
			UPDATE %swc_orders
			SET
				billing_email = CONCAT('order_', id, '@example.com'),
				ip_address = '',
				user_agent = '',
				customer_note = ''
		`, prefix)
		if _, err := db.Exec(ordersQuery); err != nil {
			return fmt.Errorf("anonymizing orders: %w", err)
		}
	}

	// HPOS keeps the full address search index (names, street, e-mail,
	// phone) in wc_orders_meta.
	ordersMeta := prefix + "wc_orders_meta"
	var ordersMetaCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?", ordersMeta).Scan(&ordersMetaCount); err == nil && ordersMetaCount > 0 {
		indexQuery := fmt.Sprintf(`
			UPDATE %swc_orders_meta SET meta_value = ''
			WHERE meta_key IN ('_billing_address_index', '_shipping_address_index')
		`, prefix)
		if _, err := db.Exec(indexQuery); err != nil {
			return fmt.Errorf("anonymizing order address index: %w", err)
		}
	}

	// 5. Orders stored as posts (legacy order store, or HPOS sync copies):
	// billing and shipping meta, IP and user agent, the address search index,
	// and the customer note, which is the post excerpt.
	log.Detail("Anonymizing order meta")
	orderMetaQuery := fmt.Sprintf(`
		UPDATE %spostmeta pm
		INNER JOIN %sposts p ON p.ID = pm.post_id
		SET pm.meta_value = CASE
			WHEN pm.meta_key IN ('_billing_first_name', '_shipping_first_name') THEN 'Customer'
			WHEN pm.meta_key IN ('_billing_last_name', '_shipping_last_name') THEN CONCAT('LN_', pm.post_id)
			WHEN pm.meta_key = '_billing_email' THEN CONCAT('order_', pm.post_id, '@example.com')
			WHEN pm.meta_key IN ('_billing_phone', '_shipping_phone') THEN '555-0000'
			WHEN pm.meta_key IN ('_billing_address_1', '_shipping_address_1') THEN '123 Staging Lane'
			WHEN pm.meta_key IN ('_billing_city', '_shipping_city') THEN 'Staging City'
			WHEN pm.meta_key IN ('_billing_postcode', '_shipping_postcode') THEN '12345'
			ELSE ''
		END
		WHERE p.post_type IN ('shop_order', 'shop_order_refund', 'shop_subscription')
		AND pm.meta_key IN (
			'_billing_first_name', '_shipping_first_name', '_billing_last_name', '_shipping_last_name',
			'_billing_email', '_billing_phone', '_shipping_phone',
			'_billing_address_1', '_shipping_address_1', '_billing_address_2', '_shipping_address_2',
			'_billing_company', '_shipping_company', '_billing_city', '_shipping_city',
			'_billing_postcode', '_shipping_postcode',
			'_billing_address_index', '_shipping_address_index',
			'_customer_ip_address', '_customer_user_agent'
		)
	`, prefix, prefix)
	if _, err := db.Exec(orderMetaQuery); err != nil {
		return fmt.Errorf("anonymizing order meta: %w", err)
	}
	orderPostQuery := fmt.Sprintf(`
		UPDATE %sposts SET post_excerpt = ''
		WHERE post_type IN ('shop_order', 'shop_subscription') AND post_excerpt <> ''
	`, prefix)
	if _, err := db.Exec(orderPostQuery); err != nil {
		return fmt.Errorf("anonymizing order customer notes: %w", err)
	}

	// 6. Analytics customer table (wc_customer_lookup).
	lookupTable := prefix + "wc_customer_lookup"
	var lookupCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?", lookupTable).Scan(&lookupCount); err == nil && lookupCount > 0 {
		log.Detail("Anonymizing analytics customers")
		lookupQuery := fmt.Sprintf(`
			UPDATE %swc_customer_lookup
			SET
				username = IF(username = '', '', CONCAT('customer_', customer_id)),
				first_name = 'Customer',
				last_name = CONCAT('LN_', customer_id),
				email = CONCAT('customer_', customer_id, '@example.com'),
				city = 'Staging City',
				postcode = '12345'
		`, prefix)
		if _, err := db.Exec(lookupQuery); err != nil {
			return fmt.Errorf("anonymizing analytics customers: %w", err)
		}
	}

	// 7. Scrub order notes (comments): clear author email/IP, and replace the
	// body of customer notes.
	log.Detail("Anonymizing order notes")
	notesQuery := fmt.Sprintf(`
		UPDATE %scomments
		SET comment_author_email = '', comment_author_IP = ''
		WHERE comment_type = 'order_note'
	`, prefix)
	if _, err := db.Exec(notesQuery); err != nil {
		return fmt.Errorf("anonymizing order notes: %w", err)
	}

	customerNoteQuery := fmt.Sprintf(`
		UPDATE %scomments c
		INNER JOIN %scommentmeta cm
			ON c.comment_ID = cm.comment_id AND cm.meta_key = 'is_customer_note'
		SET c.comment_content = '[customer note removed]'
		WHERE c.comment_type = 'order_note' AND cm.meta_value = '1'
	`, prefix, prefix)
	if _, err := db.Exec(customerNoteQuery); err != nil {
		return fmt.Errorf("anonymizing customer notes: %w", err)
	}

	return nil
}
