package sync

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/AdaDigitalAgency/wp-stage-sync/internal/progress"
)

// integration is a live-only service whose settings the import copies to
// stage. Stage keeps the plugin but loses the credentials, so neither the
// sync nor a person in the stage admin can act on the live service.
type integration struct {
	Name string
	// Blank lists options set to an empty string. An empty value is "off"
	// for the plugin's boolean switches and removes stored credentials.
	Blank []string
	// Rewrite maps an option to a function that returns its stage value,
	// for settings stored as JSON or serialized PHP.
	Rewrite map[string]func(string) (string, error)
	// Empty lists tables (without the prefix) whose rows are deleted: queues
	// of remote jobs copied from live.
	Empty []string
}

// integrationsFor returns the list the import neutralizes on stage.
func integrationsFor(livePath, stagePath, liveHost, stageHost string) []integration {
	empty, zero := phpString(""), "i:0;"
	return []integration{
		{
			// The QUIC.cloud key is live's domain key: stage would use live's
			// QUIC.cloud account and credits (image optimization, CCSS) and its
			// CDN purge.
			Name: "LiteSpeed Cache Cloudflare and QUIC.cloud",
			Blank: []string{
				"litespeed.conf.cdn-cloudflare", "litespeed.conf.cdn-cloudflare_clear",
				"litespeed.conf.cdn-cloudflare_key", "litespeed.conf.cdn-cloudflare_email",
				"litespeed.conf.cdn-cloudflare_zone", "litespeed.conf.cdn-cloudflare_name",
				"litespeed.conf.api_key", "litespeed.conf.cdn-quic",
			},
			// Since v7 the QUIC.cloud site key pair lives here (JSON).
			Rewrite: map[string]func(string) (string, error){
				"litespeed.cloud._summary": setJSONOrPHPKeys(map[string]any{"sk_b64": "", "pk_b64": ""}),
			},
		},
		{
			// Every WPFC purge sends purge_everything to the zone cached in this
			// option, with no host check (inc/cdn.php). Varnish gets PURGE /.*.
			Name:    "WP Fastest Cache Cloudflare and Varnish",
			Blank:   []string{"WpFastestCacheVarnish"},
			Rewrite: map[string]func(string) (string, error){"WpFastestCacheCDN": dropJSONEntries("id", "cloudflare")},
		},
		{
			// Set by a failed Redis write on live; makes the first stage load run
			// FLUSHDB on the shared server (Powered Cache and wp-redis drop-ins).
			Name:  "Redis failback flush flag",
			Blank: []string{"wp_redis_do_redis_failback_flush"},
		},
		{
			Name:  "Cloudflare plugin",
			Blank: []string{"cloudflare_api_key", "cloudflare_api_email", "cloudflare_cached_domain_name"},
		},
		{
			// Without a capability check, an admin action on stage (menu, widget,
			// Elementor save) purges the live zone (Addon/Cloudflare/Subscriber).
			// Sucuri purges have no capability check at all. The RocketCDN token
			// lets the stage "Purge RocketCDN cache" button purge live's CDN zone
			// (CDN/RocketCDN/APIClient::purge_cache_request).
			Name:  "WP Rocket Cloudflare, Sucuri and RocketCDN",
			Blank: []string{"rocketcdn_user_token"},
			Rewrite: map[string]func(string) (string, error){
				"wp_rocket_settings": setPHPKeys(map[string]string{
					"do_cloudflare":         "i:0;",
					"cloudflare_api_key":    phpString(""),
					"cloudflare_email":      phpString(""),
					"cloudflare_zone_id":    phpString(""),
					"sucury_waf_cache_sync": "i:0;",
					"sucury_waf_api_key":    phpString(""),
				}),
				// The stored base URL is live's, so the stage admin would start
				// WP Rocket's domain-change flow.
				"wp_rocket_last_base_url": rewriteBase64Host(liveHost, stageHost),
			},
		},
		{
			// Every stage purge sends purge_everything to cf_zoneid. A stage
			// settings save or plugin update rewrites live's Cache Rule to the
			// stage host, or deletes it (Cloudflare_Integration).
			Name: "Super Page Cache Cloudflare",
			Rewrite: map[string]func(string) (string, error){
				"swcfpc_config": setPHPKeys(map[string]string{
					"cf_email": empty, "cf_apikey": empty, "cf_apikey_enc": empty, "cf_apitoken": empty,
					"cf_apitoken_enc": empty, "cf_zoneid": empty, "cf_zoneid_list": "a:0:{}", "cf_apitoken_domain": empty,
					"cf_cache_settings_ruleset_id": empty, "cf_cache_settings_ruleset_rule_id": empty,
					"cf_page_rule_id": empty, "cf_woker_id": empty, "enable_cache_rule": zero,
					"cf_object_cache_purge_on_flush": zero, "cf_opcache_purge_on_flush": zero,
				}),
			},
		},
		{
			// A full page cache clear (Elementor, settings save) sends
			// purge_everything to live's zone; page rule and APO calls change it.
			Name: "Hummingbird Cloudflare",
			Rewrite: map[string]func(string) (string, error){
				"wphb_settings": setPHPKeys(map[string]string{
					"cloudflare": `a:4:{s:7:"enabled";b:0;s:9:"connected";b:0;s:7:"api_key";s:0:"";s:4:"zone";s:0:"";}`,
				}),
			},
		},
		{
			// clear_all_cache() purges the Cloudflare zone and MaxCDN. In
			// memcached mode it runs flush on the whole Memcached server.
			Name: "Swift Performance Cloudflare and MaxCDN",
			Rewrite: map[string]func(string) (string, error){
				"swift_performance_options": chain(
					setPHPKeys(map[string]string{
						"cloudflare-auto-purge": zero, "cloudflare-token": empty, "cloudflare-email": empty,
						"cloudflare-api-key": empty, "cloudflare-host": empty,
						"enable-cdn": zero, "maxcdn-key": empty, "maxcdn-secret": empty,
					}),
					replacePHPValue("caching-mode", phpString("memcached_php"), phpString("disk_cache_php")),
				),
			},
		},
		{
			// powered_cache_flush() purges the zone in these settings (its
			// Cloudflare extension), so a stage purge reached live's zone.
			Name: "Powered Cache Cloudflare",
			Rewrite: map[string]func(string) (string, error){
				"powered_cache_settings": setPHPKeys(map[string]string{
					"enable_cloudflare": "b:0;", "cloudflare_api_token": empty, "cloudflare_email": empty,
					"cloudflare_api_key": empty, "cloudflare_zone": empty,
				}),
			},
		},
		{
			// Tag purges ban by tag with no host check, and stage has live's
			// post IDs: a stage save would ban live's pages.
			Name:  "Proxy Cache Purge tags",
			Blank: []string{"vhp_varnish_use_tags", "vhp_varnish_purge_queue"},
		},
		{
			// The config holds live's Cloudflare, CDN and SNS credentials and
			// live's instance_id when W3TC_CONFIG_DATABASE is on.
			Name: "W3 Total Cache CDN and Cloudflare",
			Rewrite: map[string]func(string) (string, error){
				"w3tc_config_0":         w3tcStageOption(livePath, stagePath),
				"w3tc_config_0_preview": w3tcStageOption(livePath, stagePath),
			},
			// Push CDN jobs: a stage delete would remove files in live's bucket.
			Empty: []string{"w3tc_cdn_queue"},
		},
	}
}

// chain applies rewrites in order.
func chain(fns ...func(string) (string, error)) func(string) (string, error) {
	return func(raw string) (string, error) {
		var err error
		for _, fn := range fns {
			if raw, err = fn(raw); err != nil {
				return "", err
			}
		}
		return raw, nil
	}
}

// rewriteBase64Host swaps the host in a base64-encoded URL.
func rewriteBase64Host(liveHost, stageHost string) func(string) (string, error) {
	return func(raw string) (string, error) {
		decoded, err := base64.StdEncoding.DecodeString(raw)
		if err != nil || liveHost == "" || stageHost == "" {
			return raw, nil
		}
		url := strings.Replace(string(decoded), "://"+liveHost, "://"+stageHost, 1)
		return base64.StdEncoding.EncodeToString([]byte(url)), nil
	}
}

// IntegrationNames lists the integrations Neutralize switches off.
func IntegrationNames() []string {
	list := integrationsFor("", "", "", "")
	names := make([]string, len(list))
	for i, in := range list {
		names[i] = in.Name
	}
	return names
}

// neutralizeQuery builds the UPDATE for one integration. Option names come
// from the list above, never from input, but they are still passed as
// parameters.
func neutralizeQuery(prefix string, in integration) (string, []any) {
	marks := strings.TrimSuffix(strings.Repeat("?,", len(in.Blank)), ",")
	q := fmt.Sprintf("UPDATE `%soptions` SET option_value = '' WHERE option_name IN (%s) AND option_value <> ''", prefix, marks)
	args := make([]any, len(in.Blank))
	for i, name := range in.Blank {
		args[i] = name
	}
	return q, args
}

// dropJSONEntries returns a rewrite that removes the objects whose key has
// the given value from a JSON array.
func dropJSONEntries(key, value string) func(string) (string, error) {
	return func(raw string) (string, error) {
		var entries []map[string]any
		if err := json.Unmarshal([]byte(raw), &entries); err != nil {
			return "", err
		}
		kept := entries[:0]
		for _, e := range entries {
			if v, _ := e[key].(string); v != value {
				kept = append(kept, e)
			}
		}
		if len(kept) == len(entries) {
			return raw, nil
		}
		if len(kept) == 0 {
			return "", nil
		}
		out, err := json.Marshal(kept)
		return string(out), err
	}
}

// Neutralize switches off live integrations in the stage database. It runs
// right after the import, before any WordPress code loads on stage. A failure
// stops the sync: stage would hold live's API keys.
func Neutralize(stageDB *sql.DB, prefix, livePath, stagePath, liveHost, stageHost string, log progress.Logger) error {
	for _, in := range integrationsFor(livePath, stagePath, liveHost, stageHost) {
		var n int64
		if len(in.Blank) > 0 {
			q, args := neutralizeQuery(prefix, in)
			res, err := stageDB.Exec(q, args...)
			if err != nil {
				return fmt.Errorf("switching off %s on stage: %w", in.Name, err)
			}
			n, _ = res.RowsAffected()
		}
		for option, rewrite := range in.Rewrite {
			changed, err := rewriteOption(stageDB, prefix, option, rewrite)
			if err != nil {
				return fmt.Errorf("switching off %s on stage (%s): %w", in.Name, option, err)
			}
			if changed {
				n++
			}
		}
		for _, table := range in.Empty {
			res, err := stageDB.Exec(fmt.Sprintf("DELETE FROM `%s%s`", prefix, table))
			if isMissingTable(err) {
				continue
			}
			if err != nil {
				return fmt.Errorf("switching off %s on stage (%s): %w", in.Name, table, err)
			}
			if rows, _ := res.RowsAffected(); rows > 0 {
				n++
			}
		}
		if n > 0 {
			log.Detail(fmt.Sprintf("Switched off %s on stage (%d settings changed)", in.Name, n))
		}
	}
	return nil
}

func rewriteOption(db *sql.DB, prefix, option string, rewrite func(string) (string, error)) (bool, error) {
	var raw string
	q := fmt.Sprintf("SELECT option_value FROM `%soptions` WHERE option_name = ?", prefix)
	if err := db.QueryRow(q, option).Scan(&raw); err == sql.ErrNoRows {
		return false, nil
	} else if err != nil {
		return false, err
	}
	if raw == "" {
		return false, nil
	}
	value, err := rewrite(raw)
	if err != nil {
		return false, err
	}
	if value == raw {
		return false, nil
	}
	_, err = db.Exec(fmt.Sprintf("UPDATE `%soptions` SET option_value = ? WHERE option_name = ?", prefix), value, option)
	return err == nil, err
}

// isMissingTable reports MySQL error 1146 (table does not exist).
func isMissingTable(err error) bool {
	return err != nil && strings.Contains(err.Error(), "Error 1146")
}

// setJSONOrPHPKeys sets existing top-level keys of a JSON object, or of a
// serialized PHP array (string values only), whichever the option holds.
func setJSONOrPHPKeys(values map[string]any) func(string) (string, error) {
	return func(raw string) (string, error) {
		if strings.HasPrefix(strings.TrimSpace(raw), "{") {
			out, _, err := setJSONKeys([]byte(raw), values)
			return string(out), err
		}
		php := map[string]string{}
		for k, v := range values {
			php[k] = phpString(fmt.Sprint(v))
		}
		return setPHPKeys(php)(raw)
	}
}
