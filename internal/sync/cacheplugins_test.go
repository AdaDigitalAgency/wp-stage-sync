package sync

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AdaDigitalAgency/wp-stage-sync/internal/progress"
)

func TestW3TCStageJSON(t *testing.T) {
	raw := `{"common.instance_id":3732146603,"pgcache.enabled":true,"cdn.enabled":true,"cdnfsd.enabled":true,` +
		`"cdnfsd.cloudfront.secret_key":"LIVE","varnish.enabled":true,"config.path":"\/home\/live\/nginx.conf",` +
		`"cloudflare":{"email":"a@b.c","key":"LIVE","zone_id":"Z","pagecache":true},` +
		`"extensions.active":{"cloudflare":"w3-total-cache\/Extension_CloudFlare_Plugin.php","fragmentcache":"x"},` +
		`"extensions.active_frontend":{"cloudflare":"*"},"big":12345678901234567890}`
	out, n, err := w3tcStageJSON([]byte(raw), "/home/live", "/home/stage")
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("nothing changed")
	}
	if strings.Contains(string(out), "LIVE") {
		t.Errorf("credentials left: %s", out)
	}
	var cfg map[string]any
	dec := json.NewDecoder(strings.NewReader(string(out)))
	dec.UseNumber()
	if err := dec.Decode(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["cdn.enabled"] != false || cfg["cdnfsd.enabled"] != false {
		t.Errorf("CDN still on: %v %v", cfg["cdn.enabled"], cfg["cdnfsd.enabled"])
	}
	if cfg["varnish.enabled"] != true || cfg["pgcache.enabled"] != true {
		t.Error("unrelated settings changed")
	}
	if cfg["common.instance_id"] == json.Number("3732146603") {
		t.Error("instance_id not changed")
	}
	if cfg["config.path"] != "/home/stage/nginx.conf" {
		t.Errorf("config.path = %v", cfg["config.path"])
	}
	if _, on := cfg["extensions.active"].(map[string]any)["cloudflare"]; on {
		t.Error("cloudflare extension still active")
	}
	if cfg["big"] != json.Number("12345678901234567890") {
		t.Errorf("number lost precision: %v", cfg["big"])
	}
	// Idempotent: a second pass changes nothing.
	if _, n2, _ := w3tcStageJSON(out, "/home/live", "/home/stage"); n2 != 0 {
		t.Errorf("second pass changed %d settings", n2)
	}
}

func TestFixW3TCConfigFile(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "wp-content", "w3tc-config")
	os.MkdirAll(dir, 0755)
	f := filepath.Join(dir, "master.php")
	os.WriteFile(f, []byte(`<?php exit; ?>{"cloudflare":{"key":"LIVE"},"common.instance_id":1}`), 0640)
	os.WriteFile(filepath.Join(dir, "index.html"), nil, 0644)
	if err := FixLivePaths("/home/live", root, "live.test", "stage.live.test", progress.NopLogger{}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(f)
	if !strings.HasPrefix(string(data), "<?php exit; ?>{") || strings.Contains(string(data), "LIVE") {
		t.Errorf("master.php = %s", data)
	}
	if info, _ := os.Stat(f); info.Mode().Perm() != 0640 {
		t.Errorf("mode = %v", info.Mode().Perm())
	}
}

func TestFixLivePathsGlobsAndHostFiles(t *testing.T) {
	root := t.TempDir()
	stage := filepath.Join(root, "stage")
	write := func(rel, body string) {
		p := filepath.Join(stage, "wp-content", rel)
		os.MkdirAll(filepath.Dir(p), 0755)
		os.WriteFile(p, []byte(body), 0644)
	}
	write("breeze-config/breeze-config.php", "'config_path' => '/home/live/wp-content/x', 'homepage' => 'https://live.test', 'x' => 'https://live.test.example/'")
	write("mu-plugins/swift-performance-loader.php", "include '/home/live/wp-content/plugins/s/p.php';")
	write("wpo-cache/config/config-live.test.php", "{}")
	write("wpo-cache/config/config-live.test.example.php", "{}") // stage host starts with live's
	write("settings/cache-enabler/live.test.php", "<?php")
	if err := FixLivePaths("/home/live", stage, "live.test", "live.test.example", progress.NopLogger{}); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"breeze-config/breeze-config.php", "mu-plugins/swift-performance-loader.php"} {
		data, _ := os.ReadFile(filepath.Join(stage, "wp-content", rel))
		if strings.Contains(string(data), "/home/live/") {
			t.Errorf("%s still has live path: %s", rel, data)
		}
		if rel == "breeze-config/breeze-config.php" && !strings.Contains(string(data), "'homepage' => 'https://live.test.example'") {
			t.Errorf("breeze homepage not rewritten: %s", data)
		}
	}
	for rel, want := range map[string]bool{
		"wpo-cache/config/config-live.test.php":         false,
		"wpo-cache/config/config-live.test.example.php": true,
		"settings/cache-enabler/live.test.php":          false,
	} {
		_, err := os.Stat(filepath.Join(stage, "wp-content", rel))
		if got := err == nil; got != want {
			t.Errorf("%s exists = %v, want %v", rel, got, want)
		}
	}
}

func TestReplacePHPValue(t *testing.T) {
	raw := `a:2:{s:12:"caching-mode";s:13:"memcached_php";s:1:"x";i:1;}`
	got, err := replacePHPValue("caching-mode", phpString("memcached_php"), phpString("disk_cache_php"))(raw)
	if err != nil {
		t.Fatal(err)
	}
	if want := `a:2:{s:12:"caching-mode";s:14:"disk_cache_php";s:1:"x";i:1;}`; got != want {
		t.Errorf("got %s", got)
	}
	other := `a:1:{s:12:"caching-mode";s:18:"disk_cache_rewrite";}`
	if got, _ := replacePHPValue("caching-mode", phpString("memcached_php"), "x")(other); got != other {
		t.Errorf("changed a different value: %s", got)
	}
}

func TestDetectObjectCacheKinds(t *testing.T) {
	for head, want := range map[string]objectCache{
		"<?php\n/**\n * W3 Total Cache Object Cache\n":                                                                   cacheW3TC,
		"<?php\n/* Hummingbird Redis Object Cache */":                                                                    cacheHummingbird,
		"<?php\n * WP Redis\ndefine( 'WP_REDIS_OBJECT_CACHE', true );":                                                   cacheWPRedis,
		"<?php\n/*\nPlugin Name:         Docket Cache Drop-in\n":                                                         cacheDocket,
		"<?php\n/*\nPlugin Name: SQLite Object Cache (Drop-in)\n":                                                        cacheSQLite,
		"<?php\n/*\nOC Name:  atec Object-Cache\n":                                                                       cacheAPCu,
		"<?php\n\n/*\nPlugin Name: Memcached Redux\nDescription: x\n":                                                    cacheMemcachedServer,
		"<?php\n\n/*\nPlugin Name: Memcached\nDescription: Memcached backend for the WP Object Cache.\nVersion: 4.0.0\n": cacheMemcached,
		"<?php\n\n/*\nPlugin Name: Memcached\nDescription: Memcached backend for the WP Object Cache.\nVersion: 3.2.2\n": cacheMemcachedServer,
		"<?php\n// something else":                                                                                       cacheOther,
	} {
		root := t.TempDir()
		os.MkdirAll(filepath.Join(root, "wp-content"), 0755)
		os.WriteFile(filepath.Join(root, "wp-content", "object-cache.php"), []byte(head), 0644)
		if got := detectObjectCache(root); got != want {
			t.Errorf("%q: got %v, want %v", head, got, want)
		}
	}
}

func TestFlushPlanNewKinds(t *testing.T) {
	cases := []struct {
		kind        objectCache
		stage, live string
		eval, skips bool
	}{
		{cacheWPRedis, "", "", false, true},
		{cacheWPRedis, "stg", "stg-live", false, true},
		{cacheWPRedis, "stg", "live", true, false},
		{cacheHummingbird, "", "", false, true},
		{cacheHummingbird, "stg", "live", false, false},
		{cacheW3TC, "", "", false, false},
		{cacheMemcached, "", "", false, false},
		{cacheMemcachedServer, "x", "y", false, true},
		{cacheAPCu, "", "", false, true},
		{cacheSQLite, "", "", false, false},
		{cacheDocket, "", "", false, false},
	}
	for _, c := range cases {
		eval, skip := flushPlan(c.kind, c.stage, c.live)
		if (eval != "") != c.eval || (skip != "") != c.skips {
			t.Errorf("%v %q/%q: eval=%v skip=%q", c.kind, c.stage, c.live, eval != "", skip)
		}
	}
}

func TestCheckDBClusterConfig(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "wp-content"), 0755)
	f := filepath.Join(root, "wp-content", "db-cluster-config.php")
	os.WriteFile(f, []byte(`<?php return array('databases' => array('master' => array('name' => DB_NAME)));`), 0644)
	if err := checkDBClusterConfig(root, "live_db"); err != nil {
		t.Errorf("constants-only config refused: %v", err)
	}
	os.WriteFile(f, []byte(`<?php return array('databases' => array('master' => array('name' => 'live_db')));`), 0644)
	if err := checkDBClusterConfig(root, "live_db"); err == nil {
		t.Error("config naming live's database was accepted")
	}
}

func TestClearDirRoots(t *testing.T) {
	stage := t.TempDir()
	for _, dir := range []string{
		filepath.Join(stage, "wp-content", "boost-cache", "cache", "s.test"),
		filepath.Join(stage, "wp-content", "wphb-cache", "cache", "s.test"),
	} {
		os.MkdirAll(dir, 0755)
		os.WriteFile(filepath.Join(dir, "x.html"), nil, 0644)
		if err := clearDir(stage, dir); err != nil {
			t.Errorf("%s: %v", dir, err)
		}
	}
	for _, bad := range []string{
		filepath.Join(stage, "wp-content", "boost-cache"),
		filepath.Join(stage, "wp-content", "boost-cache", "cache"),
		filepath.Join(stage, "wp-content", "uploads"),
	} {
		if err := clearDir(stage, bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestSharedObjectCache(t *testing.T) {
	mk := func(dropin, cfg string) string {
		root := t.TempDir()
		os.MkdirAll(filepath.Join(root, "wp-content"), 0755)
		os.WriteFile(filepath.Join(root, "wp-config.php"), []byte("<?php\n"+cfg), 0644)
		if dropin != "" {
			os.WriteFile(filepath.Join(root, "wp-content", "object-cache.php"), []byte(dropin), 0644)
		}
		return root
	}
	redis := "<?php /* Redis Object Cache Drop-In */"
	live := mk(redis, "define('WP_REDIS_DATABASE', 0);")
	if got := SharedObjectCache(live, mk("", "")); !strings.Contains(got, "database 0") {
		t.Errorf("default database: got %q", got)
	}
	if got := SharedObjectCache(live, mk("", "define( 'WP_REDIS_DATABASE', 1 );")); got != "" {
		t.Errorf("separate database reported shared: %q", got)
	}
	if got := SharedObjectCache(mk("", ""), mk("", "")); got != "" {
		t.Errorf("no drop-in reported shared: %q", got)
	}
	wpRedis := "<?php\n * WP Redis\ndefine( 'WP_REDIS_OBJECT_CACHE', true );"
	if got := SharedObjectCache(mk(wpRedis, "$redis_server = array( 'host' => '127.0.0.1', 'database' => 2 );"),
		mk("", "$redis_server = array( 'host' => '127.0.0.1', 'database' => 3 );")); got != "" {
		t.Errorf("WP Redis separate databases reported shared: %q", got)
	}
	if got := SharedObjectCache(mk("<?php /* Plugin Name: Memcached Redux */", ""), mk("", "")); got == "" {
		t.Error("Memcached not reported shared")
	}
}

func TestRemoveStageDropIn(t *testing.T) {
	root := t.TempDir()
	content := filepath.Join(root, "wp-content")
	os.MkdirAll(content, 0755)
	os.WriteFile(filepath.Join(content, "object-cache.php"), []byte("<?php"), 0644)
	os.WriteFile(filepath.Join(content, "db.php"), []byte("<?php /* Query Monitor Database Class */"), 0644)
	if err := removeStageDropIn(root, progress.NopLogger{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(content, "object-cache.php")); !os.IsNotExist(err) {
		t.Error("object-cache.php kept")
	}
	if _, err := os.Stat(filepath.Join(content, "db.php")); err != nil {
		t.Error("another plugin's db.php was removed")
	}
	os.WriteFile(filepath.Join(content, "db.php"), []byte("<?php\n/**\n * W3 Total Cache Database module\n"), 0644)
	removeStageDropIn(root, progress.NopLogger{})
	if _, err := os.Stat(filepath.Join(content, "db.php")); !os.IsNotExist(err) {
		t.Error("W3TC db.php kept")
	}
}

func TestCheckNeutralize(t *testing.T) {
	good := map[string]string{
		"wp_rocket_settings": `a:1:{s:13:"do_cloudflare";i:1;}`,
		"swcfpc_config":      `a:1:{s:9:"cf_zoneid";s:4:"ZONE";}`,
		"w3tc_config_0":      `{"cdn.enabled":true}`,
	}
	get := func(m map[string]string) func(string) (string, error) {
		return func(o string) (string, error) { return m[o], nil }
	}
	root := t.TempDir()
	if err := checkNeutralize(get(good), root, root+"-stage", "a.test", "stage.a.test", true); err != nil {
		t.Fatalf("readable settings refused: %v", err)
	}
	for option, raw := range map[string]string{
		"swcfpc_config":      `a:1:{s:9:"cf_zoneid";O:8:"stdClass":0:{}}`, // object: not supported
		"wphb_settings":      `a:1:{s:5:"redis";`,                         // truncated
		"w3tc_config_0":      `{"cdn.enabled":`,                           // broken JSON
		"wp_rocket_settings": `s:3:"abc";`,                                // not an array
	} {
		bad := map[string]string{option: raw}
		err := checkNeutralize(get(bad), root, root+"-stage", "a.test", "stage.a.test", true)
		if err == nil || !strings.Contains(err.Error(), option) {
			t.Errorf("%s: err = %v, want a refusal that names the option", option, err)
		}
	}

	// A broken live W3TC config file is refused too.
	dir := filepath.Join(root, "wp-content", "w3tc-config")
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "master.php"), []byte(`<?php exit; ?>{"cdn.enabled":`), 0644)
	if err := checkNeutralize(get(good), root, root+"-stage", "a.test", "stage.a.test", false); err == nil {
		t.Error("broken master.php accepted")
	}
}

func TestPoweredCacheCloudflareCleared(t *testing.T) {
	raw := `a:3:{s:17:"enable_cloudflare";b:1;s:15:"cloudflare_zone";s:4:"ZONE";s:12:"enable_cache";b:1;}`
	var rewrite func(string) (string, error)
	for _, in := range integrationsFor("/home/a", "/home/stage.a", "a.test", "stage.a.test") {
		if r, ok := in.Rewrite["powered_cache_settings"]; ok {
			rewrite = r
		}
	}
	if rewrite == nil {
		t.Fatal("powered_cache_settings is not rewritten")
	}
	got, err := rewrite(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := `a:3:{s:17:"enable_cloudflare";b:0;s:15:"cloudflare_zone";s:0:"";s:12:"enable_cache";b:1;}`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestPreflightRefusesNestedSites(t *testing.T) {
	root := t.TempDir()
	if err := PreflightIntegrations(root, filepath.Join(root, "stage"), progress.NopLogger{}); err == nil {
		t.Error("stage inside live accepted")
	}
	if err := PreflightIntegrations(filepath.Join(root, "live"), filepath.Join(root, "live-stage"), progress.NopLogger{}); err != nil {
		t.Errorf("sibling folders refused: %v", err)
	}
}

func TestW3TCCacheOffOption(t *testing.T) {
	got, err := w3tcCacheOffOption(`{"objectcache.enabled":true,"dbcache.enabled":true,"pgcache.enabled":true}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `"objectcache.enabled":false`) || !strings.Contains(got, `"dbcache.enabled":false`) || !strings.Contains(got, `"pgcache.enabled":true`) {
		t.Errorf("got %s", got)
	}
}

func TestLiteSpeedQUICCloudKeysCleared(t *testing.T) {
	f := setJSONOrPHPKeys(map[string]any{"sk_b64": "", "pk_b64": ""})
	got, err := f(`{"sk_b64":"LIVESECRET","pk_b64":"LIVEPUB","is_linked":1}`)
	if err != nil || strings.Contains(got, "LIVE") || !strings.Contains(got, `"is_linked":1`) {
		t.Errorf("json: %s %v", got, err)
	}
	got, err = f(`a:2:{s:6:"sk_b64";s:10:"LIVESECRET";s:4:"keep";i:1;}`)
	if err != nil || strings.Contains(got, "LIVE") || !strings.Contains(got, `s:4:"keep";i:1;`) {
		t.Errorf("php: %s %v", got, err)
	}
}
