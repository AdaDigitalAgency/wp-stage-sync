package sync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func site(t *testing.T, dropin, config string, plugins ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, p := range append(plugins, "") {
		if err := os.MkdirAll(filepath.Join(root, "wp-content", "plugins", p), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if dropin != "" {
		writeFile(t, filepath.Join(root, "wp-content", "object-cache.php"))
		if err := os.WriteFile(filepath.Join(root, "wp-content", "object-cache.php"), []byte(dropin), 0644); err != nil {
			t.Fatal(err)
		}
	}
	base := "<?php\ndefine('DB_NAME','a');define('DB_USER','b');define('DB_PASSWORD','c');define('DB_HOST','d');\n"
	if err := os.WriteFile(filepath.Join(root, "wp-config.php"), []byte(base+config), 0644); err != nil {
		t.Fatal(err)
	}
	return root
}

const redisDropin = "<?php\n/*\nPlugin Name: Redis Object Cache Drop-In\n*/\n"

func TestDetectObjectCache(t *testing.T) {
	cases := map[string]objectCache{"": cacheNone, redisDropin: cacheRedis, "<?php // Memcached drop-in": cacheOther}
	for dropin, want := range cases {
		if got := detectObjectCache(site(t, dropin, "")); got != want {
			t.Errorf("dropin %q: got %v, want %v", dropin, got, want)
		}
	}
}

func TestCachePrefix(t *testing.T) {
	cases := map[string]string{
		"": "",
		"define('WP_CACHE_KEY_SALT', 'stage-salt');":                             "stage-salt",
		"define( 'WP_REDIS_PREFIX', 'stage' );":                                  "stage",
		"define('WP_CACHE_KEY_SALT','salt');define('WP_REDIS_PREFIX','prefix');": "prefix",
		"define('WP_CACHE_KEY_SALT', '');":                                       "",
		"define('WP_CACHE_KEY_SALT', getenv('SALT'));":                           "", // cannot prove it
	}
	for cfg, want := range cases {
		if got := cachePrefix(site(t, "", cfg), cacheRedis); got != want {
			t.Errorf("config %q: got %q, want %q", cfg, got, want)
		}
	}
}

func TestFlushPlan(t *testing.T) {
	if eval, skip := flushPlan(cacheNone, "", ""); eval != "" || skip != "" {
		t.Errorf("none: %q %q", eval, skip)
	}
	if eval, skip := flushPlan(cacheRedis, "stage[1]", "live"); eval != redisPrefixFlush || skip != "" {
		t.Errorf("redis with prefix: %q", skip)
	}
	skips := []struct {
		kind        objectCache
		stage, live string
	}{
		{cacheRedis, "", "live"},          // whole-database flush
		{cacheRedis, "site", "site-live"}, // pattern would match live keys
		{cacheOther, "stage", "live"},     // unknown scope
	}
	for _, c := range skips {
		if eval, skip := flushPlan(c.kind, c.stage, c.live); eval != "" || skip == "" {
			t.Errorf("%v %q %q: want a skip", c.kind, c.stage, c.live)
		}
	}
}

func findCmd(cmds []PlannedCommand, prefix string) *PlannedCommand {
	for i := range cmds {
		if strings.HasPrefix(cmds[i].Label, prefix) {
			return &cmds[i]
		}
	}
	return nil
}

func TestPostProcessCommandsCaches(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	live := site(t, redisDropin, "define('WP_CACHE_KEY_SALT','live');")
	plain := postProcessCommands([]string{"wp"}, live, site(t, "", ""), "a.test", "stage.a.test", cacheNone)
	if c := findCmd(plain, "Cache flush"); c == nil || c.Skip || strings.Contains(strings.Join(c.Args, " "), "--exec") {
		t.Errorf("no drop-in: want a plain flush, got %+v", c)
	}
	for _, label := range []string{"Elementor CSS", "LiteSpeed", "WP Rocket"} {
		if c := findCmd(plain, label); c == nil || !c.Skip || !c.Optional {
			t.Errorf("%s without its plugin: want an optional skip, got %+v", label, c)
		}
	}

	stage := site(t, redisDropin, "define('WP_CACHE_KEY_SALT','s');", "elementor", "litespeed-cache", "wp-rocket")
	cmds := postProcessCommands([]string{"wp"}, live, stage, "a.test", "stage.a.test", cacheRedis)
	if c := findCmd(cmds, "Cache flush"); c == nil || c.Skip || !c.Optional || c.Args[1] != "eval" {
		t.Errorf("redis with salt: want an optional prefix flush, got %+v", c)
	}
	for _, label := range []string{"Elementor CSS", "LiteSpeed", "WP Rocket"} {
		if c := findCmd(cmds, label); c == nil || c.Skip {
			t.Errorf("%s with its plugin: want it to run, got %+v", label, c)
		}
	}

	unsalted := postProcessCommands([]string{"wp"}, live, site(t, redisDropin, ""), "a.test", "stage.a.test", cacheRedis)
	if c := findCmd(unsalted, "Cache flush"); c == nil || !c.Skip || !strings.Contains(c.SkipNote, "WARNING") {
		t.Errorf("redis without salt: want a warned skip, got %+v", c)
	}
}

func TestClearDir(t *testing.T) {
	stage := t.TempDir()
	dir := filepath.Join(stage, "wp-content", "cache", "wp-rocket")
	writeFile(t, filepath.Join(dir, "stage.test", "index.html"))
	if err := clearDir(stage, dir); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 || !exists(dir) {
		t.Errorf("want an empty folder that still exists, got %d entries", len(entries))
	}
	if err := clearDir(stage, filepath.Join(stage, "wp-content", "cache", "missing")); err != nil {
		t.Errorf("missing folder: %v", err)
	}
	for _, bad := range []string{
		filepath.Join(stage, "wp-content", "uploads"),
		filepath.Join(stage, "wp-content", "cache"),
		filepath.Join(stage, "wp-content", "cache", "..", "uploads"),
		"/",
	} {
		if err := clearDir(stage, bad); err == nil {
			t.Errorf("clearDir(%q): want refusal", bad)
		}
	}
}

func TestCommandErrorNil(t *testing.T) {
	if err := CommandError(nil, []byte("Success: done")); err != nil {
		t.Errorf("CommandError(nil) = %v, want nil", err)
	}
}

func TestPickStageHost(t *testing.T) {
	cases := []struct{ db, dir, live, want string }{
		{"stage.a.test", "stage.a.test", "a.test", "stage.a.test"},
		{"staging.a.test", "stage.a.test", "a.test", "staging.a.test"},
		{"a.test", "stage.a.test", "a.test", "stage.a.test"}, // an earlier sync stopped early
	}
	for _, c := range cases {
		if got, err := pickStageHost(c.db, c.dir, c.live); err != nil || got != c.want {
			t.Errorf("pickStageHost(%q, %q, %q) = %q, %v", c.db, c.dir, c.live, got, err)
		}
	}
	if _, err := pickStageHost("a.test", "a.test", "a.test"); err == nil {
		t.Error("no way to tell them apart: want error")
	}
}

const liteSpeedDropin = "<?php\ndefined( 'WPINC' ) || exit;\n! defined( 'LSCWP_OBJECT_CACHE' ) && define( 'LSCWP_OBJECT_CACHE', true );\n"

func TestLiteSpeedObjectCache(t *testing.T) {
	if got := detectObjectCache(site(t, liteSpeedDropin, "")); got != cacheLiteSpeed {
		t.Fatalf("detect = %v", got)
	}
	// LSOC_PREFIX includes DB_NAME, so no salt is needed for a scoped flush.
	if eval, skip := flushPlan(cacheLiteSpeed, "", ""); eval != liteSpeedPrefixFlush || skip != "" {
		t.Errorf("flushPlan = %q", skip)
	}
}

func TestStageCommandsSkipCachePlugins(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stage := site(t, liteSpeedDropin, "", "elementor", "litespeed-cache")
	cmds := postProcessCommands([]string{"wp"}, site(t, "", ""), stage, "a.test", "stage.a.test", cacheLiteSpeed)
	for _, c := range cmds {
		if len(c.Args) == 0 {
			continue
		}
		args := strings.Join(c.Args, " ")
		if strings.Contains(args, "litespeed-purge all") || strings.HasSuffix(strings.Fields(args)[1], "flush") {
			t.Errorf("%s: a full purge or flush must not run: %s", c.Label, args)
		}
		skip := c.Args[len(c.Args)-1]
		if !strings.HasPrefix(skip, "--skip-plugins=") {
			t.Errorf("%s: no --skip-plugins", c.Label)
			continue
		}
		hasLS := strings.Contains(skip, "litespeed-cache")
		if c.Label == "LiteSpeed page cache purge" {
			if hasLS || !strings.Contains(skip, "wp-rocket") {
				t.Errorf("page purge must load LiteSpeed Cache only: %s", skip)
			}
		} else if !hasLS {
			t.Errorf("%s: LiteSpeed Cache not skipped", c.Label)
		}
	}
}
