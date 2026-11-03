package sync

import (
	"strings"
	"testing"
)

func TestOCPDatabase(t *testing.T) {
	cases := []struct {
		cfg  string
		want int
		ok   bool
	}{
		{"define('WP_REDIS_CONFIG', [\n 'token' => 'x',\n 'scheme' => 'unix',\n 'host' => '/run/redis.sock',\n 'database' => 1,\n]);", 1, true},
		{"define( 'WP_REDIS_CONFIG', [ 'host' => '127.0.0.1', 'prefix' => 'live' ] );", 0, true},
		{"define('WP_REDIS_CONFIG', ['cluster' => ['tcp://a:6379'], 'database' => 2]);", 0, false},
		{"define('WP_REDIS_CONFIG', ['servers' => ['tcp://a'], 'database' => 2]);", 0, false},
		{"define('WP_REDIS_CONFIG', array_merge($base, ['database' => 3]));", 0, false},
		{"", 0, false},
	}
	for _, c := range cases {
		got, err := ocpDatabase(site(t, "", c.cfg))
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("%q: got %d, %v", c.cfg, got, err)
		}
	}
}

func TestCheckOCPDatabases(t *testing.T) {
	live := site(t, "", "define('WP_REDIS_CONFIG', ['database' => 0]);")
	if err := checkOCPDatabases(live, site(t, "", "define('WP_REDIS_CONFIG', ['database' => 1]);")); err != nil {
		t.Errorf("separate databases: %v", err)
	}
	for _, stageCfg := range []string{
		"define('WP_REDIS_CONFIG', ['database' => 0]);",
		"define('WP_REDIS_CONFIG', ['prefix' => 'stage']);", // default database 0
		"define('WP_REDIS_CONFIG', $config);",
	} {
		if err := checkOCPDatabases(live, site(t, "", stageCfg)); err == nil {
			t.Errorf("stage %q: want refusal", stageCfg)
		}
	}
}

func TestOCPFlushPlan(t *testing.T) {
	live := site(t, "", "define('WP_REDIS_CONFIG', ['database' => 0]);")
	eval, skip := ocpFlushPlan(live)
	if skip != "" || !strings.Contains(eval, "=== 0)") {
		t.Errorf("eval %q skip %q", eval, skip)
	}
	if eval, skip := ocpFlushPlan(site(t, "", "")); eval != "" || skip == "" {
		t.Error("unreadable live config: want a skip")
	}
	if detectObjectCache(site(t, "<?php\n/*\n * Plugin Name: Object Cache Pro (Drop-in)\n */\n", "")) != cacheOCP {
		t.Error("drop-in not detected")
	}
}
