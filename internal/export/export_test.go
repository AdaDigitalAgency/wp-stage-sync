package export

import (
	"strings"
	"testing"

	"github.com/AdaDigitalAgency/wp-stage-sync/internal/config"
)

func TestDefaultTableMode(t *testing.T) {
	cases := map[string]config.TableMode{
		"woocommerce_sessions":    config.TableModeStructureOnly,
		"actionscheduler_actions": config.TableModeStructureOnly,
		"wc_orders":               config.TableModeCustomRule,
		"woocommerce_order_items": config.TableModeCustomRule,
		"users":                   config.TableModeCustomRule,
		"comments":                config.TableModeCustomRule,
		"posts":                   config.TableModeStructureAndData,
		"options":                 config.TableModeStructureAndData,
	}
	for name, want := range cases {
		if got := DefaultTableMode(name); got != want {
			t.Errorf("DefaultTableMode(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestGetTableMode(t *testing.T) {
	// Full-name override wins.
	cfg := &config.Config{TableModes: map[string]config.TableMode{
		"wp_posts": config.TableModeIgnore,
	}}
	if got := getTableMode(cfg, "wp_posts", "wp_"); got != config.TableModeIgnore {
		t.Errorf("full-name override: got %q, want ignore", got)
	}

	// Short-name (prefix-stripped) override also matches.
	cfgShort := &config.Config{TableModes: map[string]config.TableMode{
		"posts": config.TableModeStructureOnly,
	}}
	if got := getTableMode(cfgShort, "wp_posts", "wp_"); got != config.TableModeStructureOnly {
		t.Errorf("short-name override: got %q, want structure_only", got)
	}

	// No override falls back to structure_and_data.
	if got := getTableMode(&config.Config{}, "wp_posts", "wp_"); got != config.TableModeStructureAndData {
		t.Errorf("default: got %q, want structure_and_data", got)
	}
}

func TestMakeInPlaceholders(t *testing.T) {
	cases := map[int]string{0: "", 1: "?", 3: "?,?,?"}
	for n, want := range cases {
		if got := makeInPlaceholders(n); got != want {
			t.Errorf("makeInPlaceholders(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestFormatValue(t *testing.T) {
	cases := []struct {
		name string
		in   interface{}
		want string
	}{
		{"nil", nil, "NULL"},
		{"int64", int64(42), "42"},
		{"single-quote", "O'Brien", `'O\'Brien'`},
		{"backslash bytes", []byte(`a\b`), `'a\\b'`},
		{"utf8 bytes", []byte("héllo"), "'héllo'"},
		{"binary bytes", []byte{0xff, 0x00, 0x1a}, "0xff001a"},
	}
	for _, c := range cases {
		if got := formatValue(c.in); got != c.want {
			t.Errorf("formatValue(%s) = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestPostsFilter(t *testing.T) {
	where, args := postsFilter([]int64{7, 9})
	want := "post_type NOT IN ('shop_order','shop_order_refund','shop_order_placehold','shop_subscription') OR ID IN (?,?)"
	if where != want || len(args) != 2 {
		t.Errorf("got %q %v", where, args)
	}
	// No target orders: every order post goes.
	if where, args := postsFilter(nil); strings.Contains(where, "OR") || len(args) != 0 {
		t.Errorf("empty targets: %q %v", where, args)
	}
}

func TestPostmetaFilter(t *testing.T) {
	where, args := postmetaFilter("wp_posts", []int64{7})
	want := "post_id NOT IN (SELECT ID FROM `wp_posts` WHERE post_type IN ('shop_order','shop_order_refund','shop_order_placehold','shop_subscription') AND ID NOT IN (?))"
	if where != want || len(args) != 1 {
		t.Errorf("got %q %v", where, args)
	}
	if where, _ := postmetaFilter("wp_posts", nil); strings.Contains(where, "AND ID NOT IN") {
		t.Errorf("empty targets: %q", where)
	}
}

func TestCustomerLookupFilter(t *testing.T) {
	where, args := customerLookupFilter("wp_", true, []int64{1, 2}, []int64{5})
	if !strings.Contains(where, "wp_wc_order_stats") || !strings.Contains(where, "user_id IN (?)") || len(args) != 3 {
		t.Errorf("got %q %v", where, args)
	}
	if where, _ := customerLookupFilter("wp_", false, nil, nil); where != "0" {
		t.Errorf("nothing to keep: %q", where)
	}
}
