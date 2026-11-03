# Cache plugins

The import copies live's plugin settings to the staging site, including CDN, Cloudflare and firewall credentials, and the file sync copies config files that hold live's paths. This page lists, per cache plugin, what the sync switches off, rewrites and clears on staging, and the problem each change prevents.

Without these changes, a person who clicks "Purge All" in the staging admin can purge the live Cloudflare zone, and a cache clear on staging can delete live's cache folder. The settings are cleared right after the import, before any WordPress code runs on staging; the files are fixed right after the file sync. Both happen on staging only. The order is in [how it works](how-it-works.md).

The behaviour described here comes from the code in [`internal/sync/`](../internal/sync/): [`neutralize.go`](../internal/sync/neutralize.go) (settings), [`livepaths.go`](../internal/sync/livepaths.go) (files), [`w3tc.go`](../internal/sync/w3tc.go), and `postProcessCommands` and `pageCaches` in [`sync.go`](../internal/sync/sync.go) (cache clears).

Each plugin below was installed on a test server with dummy credentials, synced, and then purged from the staging admin, with all outbound HTTP logged and blocked. The check was that no request reached a live service, no credential stayed on staging, and live's cache files and keys stayed. The real WP Rocket plugin and a LiteSpeed Enterprise server were part of the tests. Comet Cache, Jetpack Boost, SiteGround Optimizer and NitroPack were checked by reading their source only.

## Overview

"Settings" are options in the staging database. "Files" are copied files whose live paths are replaced. "Cleared" are staging cache folders, relative to `wp-content/cache/` unless they start with `wp-content/`; `<host>` is the staging host. A dash means the sync does nothing for that plugin in that column.

| Plugin | Settings switched off on staging | Files fixed | Staging cache cleared | Notes |
|---|---|---|---|---|
| [LiteSpeed Cache](#litespeed-cache) | Cloudflare switch, key, email, zone, name; QUIC.cloud key and key pair | `advanced-cache.php`, `object-cache.php` | Page cache purge through LiteSpeed | Excluded: `litespeed`. Object cache: [prefix flush](object-cache.md#flush-per-drop-in). |
| [WP Rocket](#wp-rocket) | Cloudflare, Sucuri sync, RocketCDN token; base URL rewritten | `advanced-cache.php` | `wp-rocket`, `min`, `busting`, `critical-css`, `used-css`, `background-css`, `fonts` | |
| [W3 Total Cache](#w3-total-cache) | Cloudflare, CDN, full-site CDN, message bus; new `instance_id`; CDN queue emptied | `w3tc-config/*.php` (same changes), `advanced-cache.php`, `object-cache.php` | `page_enhanced/<host>`, `page`, `minify`, `db`, `object` | Refuses a `db-cluster-config.php` that names live's database. |
| [WP Super Cache](#wp-super-cache) | – | `wp-cache-config.php`, `advanced-cache.php` | `supercache/<host>`, `meta`, `blogs`, files `wp-cache-*` | |
| [WP Fastest Cache](#wp-fastest-cache) | Cloudflare CDN entry, Varnish | – | `all`, `wpfc-minified`, `wpfc-mobile-cache`, `wpfc-widget-cache`, `tmpWpfc` | |
| [Powered Cache](#powered-cache) | Cloudflare switch, token, key, email, zone; Redis failback flush flag | `advanced-cache.php` | `powered-cache/<host>` | |
| [WP-Optimize](#wp-optimize) | – | `advanced-cache.php`; live's per-host config deleted | `wpo-cache/<host>`, `wpo-minify` | Its purge can clear a shared object cache. |
| [Breeze](#breeze) | – | `breeze-config/*.php` (paths and home URL), `advanced-cache.php` | `breeze`, `breeze-minification`, `breeze-extra` | Warns on `CDN_SITE_TOKEN` in staging `wp-config.php`. |
| [Cache Enabler](#cache-enabler) | – | `advanced-cache.php`; live's per-host settings deleted | `cache-enabler/<host>` | |
| [Comet Cache](#comet-cache) | – | – | `comet-cache/cache/https/<host>`, `comet-cache/cache/http/<host>` | |
| [Swift Performance](#swift-performance) | Cloudflare, MaxCDN; Memcached mode becomes disk cache | `mu-plugins/swift-performance-loader.php`; stored paths in the database | `swift-performance/<host>` | Lite and Pro. |
| [Autoptimize](#autoptimize) | – | `autoptimize_404_handler.php` | `autoptimize` | |
| [SiteGround Optimizer](#siteground-optimizer) | – | `sgo-config.php` | `sgo-cache` | |
| [Jetpack Boost](#jetpack-boost) | – | `advanced-cache.php` | `page_optimize`, `wp-content/boost-cache/cache/<host>` | Excluded: `boost-cache/cache`. |
| [Hummingbird](#hummingbird) | Cloudflare | Stored minify paths in the database | `wp-content/wphb-cache/cache/<host>`, `wp-content/wphb-cache/cache/mobile/<host>` | Excluded: `wphb-cache/cache`, `wphb-logs`. Redis: [salt rule](object-cache.md#flush-per-drop-in). |
| [Super Page Cache](#super-page-cache) | Cloudflare keys, token, zone, rule and worker IDs | – | `wp-content/wp-cloudflare-super-page-cache/<host>/fallback_cache` | Excluded: `wp-cloudflare-super-page-cache`. Warns on its constants. |
| [Cloudflare plugin](#cloudflare-plugin) | API key, email, cached domain name | – | – | Warns on `CLOUDFLARE_API_KEY`, `CLOUDFLARE_EMAIL`. |
| [Proxy Cache Purge](#proxy-cache-purge) | Tag purges, purge queue | – | – | |
| [Nginx Helper](#nginx-helper) | – | – | – | Warns when both sites use one cache folder. |
| [NitroPack](#nitropack) | – | `advanced-cache.php` | – | |

Every plugin in this table is also skipped in the staging WP-CLI commands; see [below](#plugins-skipped-in-wp-cli).

## How the cleanup works

**Settings.** An option is either set to an empty string (an empty value is "off" for these plugins' switches and removes a stored credential), or rewritten key by key. Serialized PHP arrays are parsed and only the listed top-level keys change; a key that does not exist is not added. JSON values are parsed the same way. Options that are missing or empty are left alone.

**Check before the sync.** Before the export, every option and W3 Total Cache config file that will be rewritten is read from live and rewritten in memory. If one cannot be parsed, for example a serialized value with a broken length, the sync stops before any change on staging. The dry run shows this check as `[check]`. See [troubleshooting](troubleshooting.md#preflight-settings-check).

**Files.** Live's webroot is replaced by the staging webroot where it appears as a whole path prefix: followed by `/`, `'` or `"`. `advanced-cache.php` and `object-cache.php` are fixed whichever plugin wrote them; the table lists them under the plugins that write one.

**Cleared folders.** The sync deletes the contents of each folder and keeps the folder. It never calls the plugin's own purge function, because those functions also purge Cloudflare, a CDN, Varnish or the object cache. Each clear runs only when the plugin's folder exists in the staging `wp-content/plugins/`. A failed clear is a warning.

Why the folders need clearing: most of them are under the excluded `wp-content/cache`, or keyed by the staging host, so rsync neither copies nor deletes them. They hold pages that staging built from its old database.

## LiteSpeed Cache

- **Settings:** `litespeed.conf.cdn-cloudflare`, `…-cloudflare_clear`, `…-cloudflare_key`, `…-cloudflare_email`, `…-cloudflare_zone` and `…-cloudflare_name` are emptied.
- **Cache:** the page cache is purged with `\LiteSpeed\Purge::purge_all_lscache()` and `\LiteSpeed\Core::kick_purge_queue()`, through `wp eval` with LiteSpeed Cache loaded and the other cache plugins skipped. This sends a page purge to the web server and does nothing else.
- **Object cache:** the LiteSpeed object cache drop-in is flushed by deleting only keys with the staging `LSOC_PREFIX` (Redis). With Memcached it fails with a warning. See [object cache](object-cache.md).

The risk: LiteSpeed Cache answers a content event such as Elementor's URL replace or CSS flush with a full purge. A full purge flushes the whole object cache, resets OPcache and purges the Cloudflare zone with the credentials in the database, which were live's. The sync therefore never runs `wp litespeed-purge all`, and skips LiteSpeed Cache in every other WP-CLI command.

- **QUIC.cloud:** the domain key `litespeed.conf.api_key`, the `cdn-quic` switch, and the site key pair (`sk_b64`, `pk_b64`) in `litespeed.cloud._summary` are cleared, so staging is not linked to live's QUIC.cloud account. Link staging to its own account if you need QUIC.cloud there.

## WP Rocket

- **Settings:** in `wp_rocket_settings`, `do_cloudflare` and `sucury_waf_cache_sync` become 0, and `cloudflare_api_key`, `cloudflare_email`, `cloudflare_zone_id` and `sucury_waf_api_key` become empty. `rocketcdn_user_token` is emptied. `wp_rocket_last_base_url` (base64) gets the staging host.
- **Cache:** `wp-content/cache/wp-rocket`, `min`, `busting`, `critical-css`, `used-css`, `background-css` and `fonts` are emptied.

The risks: an admin action on staging (a menu, widget or Elementor save) purges the Cloudflare zone, with no capability check. Sucuri purges have no capability check at all; before the fix, the post-processing purged live's Sucuri firewall cache with live's API key. The RocketCDN token lets the staging "Purge RocketCDN cache" button purge live's CDN zone. Live's base URL would start WP Rocket's domain-change flow in the staging admin.

## W3 Total Cache

W3 Total Cache keeps its settings in `wp-content/w3tc-config/*.php` (`<?php exit; ?>` followed by JSON), or in the `w3tc_config_0` option when `W3TC_CONFIG_DATABASE` is on. The sync changes both: the options `w3tc_config_0` and `w3tc_config_0_preview` after the import, and the files after the file sync.

- **Emptied:** S3, CloudFront, FTP, Rackspace, Azure, Google Drive and BunnyCDN credentials; full-site CDN (CloudFront, TransparentCDN, BunnyCDN pull zone); the SNS message bus key, secret and topic; the `cloudflare` email, key, zone ID and zone name.
- **Switched off:** `cdn.enabled`, `cdnfsd.enabled`, `cluster.messagebus.enabled`, and the Cloudflare extension in `extensions.active` and `extensions.active_frontend`.
- **New `common.instance_id`:** derived from the staging path, never 0, stable from one sync to the next. Every W3TC cache key starts with `w3tc_<instance_id>`, so staging's object, database and page cache keys never match live's on a shared Redis or Memcached server.
- **`config.path`:** live's path replaced.
- **CDN queue:** the `w3tc_cdn_queue` table is emptied. A queued push job on staging would upload to or delete from live's bucket.
- **Cache:** `page_enhanced/<host>`, `page`, `minify`, `db` and `object` under `wp-content/cache/` are emptied.

Before the sync starts, it refuses when live's `wp-content/db-cluster-config.php` names live's database in quotes: the file sync would copy it, and staging's `db.php` would send queries to live. See [troubleshooting](troubleshooting.md#preflight-integrations).

Varnish and other page cache settings are not changed.

## WP Super Cache

- **Files:** `wp-cache-config.php` holds `$cache_path`, which is live's path in the copy. It is replaced, and so are paths in `advanced-cache.php`.
- **Cache:** `supercache/<host>`, `meta` and `blogs` under `wp-content/cache/`, and the files `wp-content/cache/wp-cache-*`, are deleted. The paths come from the staging webroot, not from the config.

The risk: `wp_cache_clear_cache()` trusts `$cache_path`. With live's path, a clear on staging deleted live's cache folder.

## WP Fastest Cache

- **Settings:** `WpFastestCacheVarnish` is emptied. In `WpFastestCacheCDN` (JSON), the entry with `"id": "cloudflare"` is removed and other CDN entries stay.
- **Cache:** `all`, `wpfc-minified`, `wpfc-mobile-cache`, `wpfc-widget-cache` and `tmpWpfc` under `wp-content/cache/`.

The risk: every WP Fastest Cache purge sends `purge_everything` to the zone stored in the option, with no host check, and sends `PURGE /.*` to the Varnish server. The sync never calls a WPFC purge function for the same reason.

## Powered Cache

- **Settings:** in `powered_cache_settings`, `enable_cloudflare` becomes false and `cloudflare_api_token`, `cloudflare_api_key`, `cloudflare_email` and `cloudflare_zone` become empty. The shared Redis failback flag, `wp_redis_do_redis_failback_flush`, is emptied too. A failed Redis write on live sets it, and the first load of the staging site would then run `FLUSHDB` on the shared server.
- **Files:** `advanced-cache.php`.
- **Cache:** `powered-cache/<host>`.

The risk: `powered_cache_flush()` purges the Cloudflare zone stored in its settings, so a purge in the staging admin purged live's zone. The sync also never calls it, and only deletes the folder.

## WP-Optimize

- **Files:** `advanced-cache.php`. Live's per-host config copies, `wpo-cache/config/config-<live host>.php` and `config-<live host>-port*.php`, are deleted; WP-Optimize writes one for the staging host.
- **Cache:** `wpo-cache/<host>` and `wpo-minify`.

WP-Optimize's purge calls `wp_cache_flush()`, which clears the whole Redis database or Memcached server when staging shares live's. See [object cache](object-cache.md#shared-database).

## Breeze

- **Files:** `breeze-config/*.php` gets the staging path, and its `homepage` URL the staging host. `advanced-cache.php` too.
- **Cache:** `breeze`, `breeze-minification` and `breeze-extra`.
- **Warning:** if the staging `wp-config.php` defines `CDN_SITE_TOKEN` (Cloudways Cloudflare).

Like WP-Optimize, Breeze's purge calls `wp_cache_flush()`.

## Cache Enabler

- **Files:** `advanced-cache.php`. Live's settings files, `wp-content/settings/cache-enabler/<live host>.php` and `<live host>.*.php`, are deleted; a file that also contains the staging host in its name is kept. Cache Enabler rebuilds the staging file.
- **Cache:** `cache-enabler/<host>`.

The risk: Cache Enabler's early engine falls back to the first file in its settings folder, so it could load live's.

## Comet Cache

- **Cache:** `comet-cache/cache/https/<host>` and `comet-cache/cache/http/<host>`.

Comet Cache is skipped in the WP-CLI commands. Its settings are not changed.

## Swift Performance

Both `swift-performance` and `swift-performance-lite`.

- **Settings:** in `swift_performance_options`, `cloudflare-auto-purge` and `enable-cdn` become 0, and `cloudflare-token`, `cloudflare-email`, `cloudflare-api-key`, `cloudflare-host`, `maxcdn-key` and `maxcdn-secret` become empty. A `caching-mode` of `memcached_php` becomes `disk_cache_php`.
- **Files:** `mu-plugins/swift-performance-loader.php` includes the plugin by absolute path; the path is replaced. The stored cache path in the database is fixed by the path search-replace.
- **Cache:** `swift-performance/<host>`.

The risks: `clear_all_cache()` purges the Cloudflare zone and MaxCDN, and in Memcached mode flushes the whole Memcached server. Without the fix, the loader made staging run live's plugin, and staging wrote into live's cache path.

## Autoptimize

- **Files:** `autoptimize_404_handler.php`.
- **Cache:** `autoptimize`.

## SiteGround Optimizer

- **Files:** `sgo-config.php` holds the cache `output_dir`; live's path is replaced.
- **Cache:** `sgo-cache`.

The risk: with live's path, SiteGround Optimizer on staging wrote pages into live's cache folder.

## Jetpack Boost

- **Files:** `advanced-cache.php`, which includes the plugin by path.
- **Cache:** `wp-content/cache/page_optimize` and `wp-content/boost-cache/cache/<host>`. The default excludes skip `boost-cache/cache`.

## Hummingbird

Both `hummingbird-performance` and `wp-hummingbird`.

- **Settings:** in `wphb_settings`, the `cloudflare` entry becomes disabled and disconnected, with an empty API key and zone.
- **Database paths:** the minify groups store absolute file paths, which the path search-replace fixes.
- **Cache:** `wp-content/wphb-cache/cache/<host>` and `wp-content/wphb-cache/cache/mobile/<host>`. The default excludes skip `wphb-cache/cache` and `wphb-logs`.
- **Redis object cache:** flushed only when staging has its own `WP_REDIS_SALT`. With **Stage object cache: Off**, Redis is switched off in `wphb_settings`.

The risks: a full page cache clear (Elementor, a settings save) sends `purge_everything` to live's zone, and page rule and APO calls change it. Without the fix, Hummingbird's daily cleanup on staging deleted live's minified CSS and JS by stored path.

## Super Page Cache

Plugin folder `wp-cloudflare-page-cache`.

- **Settings:** in `swcfpc_config`: `cf_email`, `cf_apikey`, `cf_apikey_enc`, `cf_apitoken`, `cf_apitoken_enc`, `cf_zoneid`, `cf_apitoken_domain`, `cf_cache_settings_ruleset_id`, `cf_cache_settings_ruleset_rule_id`, `cf_page_rule_id` and `cf_woker_id` become empty, `cf_zoneid_list` an empty array, and `enable_cache_rule`, `cf_object_cache_purge_on_flush` and `cf_opcache_purge_on_flush` 0.
- **Cache:** `wp-content/wp-cloudflare-super-page-cache/<host>/fallback_cache`.
- **Warning:** if the staging `wp-config.php` defines `SWCFPC_CF_API_KEY`, `SWCFPC_CF_API_TOKEN` or `SWCFPC_CF_API_ZONE_ID`.

The risks: every staging purge sends `purge_everything` to `cf_zoneid`. A staging settings save or plugin update rewrites live's Cloudflare Cache Rule to the staging host, or deletes it. Its purge also calls `wp_cache_flush()`.

## Cloudflare plugin

- **Settings:** `cloudflare_api_key`, `cloudflare_api_email` and `cloudflare_cached_domain_name` are emptied.
- **Warning:** if the staging `wp-config.php` defines `CLOUDFLARE_API_KEY` or `CLOUDFLARE_EMAIL`.

The sync never copies `wp-config.php`, so a constant there was set on staging by hand, and the database cleanup cannot remove it. Remove it, or make sure it holds credentials for a staging zone only.

## Proxy Cache Purge

Plugin folder `varnish-http-purge`.

- **Settings:** `vhp_varnish_use_tags` and `vhp_varnish_purge_queue` are emptied.

The risk: tag purges ban by tag with no host check, and staging has live's post IDs, so a save on staging would ban live's pages. URL purges are by host and are left on.

## Nginx Helper

- **Warning only:** when Nginx Helper is installed on live and both sites resolve to the same `RT_WP_NGINX_HELPER_CACHE_PATH` (default `/var/run/nginx-cache`).

"Purge Cache" in the staging admin deletes the whole folder, live's pages included. Give staging its own `fastcgi_cache_path` in the Nginx config and its own `RT_WP_NGINX_HELPER_CACHE_PATH` in the staging `wp-config.php`. See [servers](servers.md#nginx-helper-cache-folder).

## NitroPack

- **Files:** `advanced-cache.php`, which includes the plugin by path.

NitroPack is skipped in the WP-CLI commands. Its connection settings are not changed, so the staging site keeps live's NitroPack site ID and secret. Disconnect NitroPack on staging by hand.

## Other changes

- **Redis failback flush flag** (`wp_redis_do_redis_failback_flush`): emptied for every site, see [Powered Cache](#powered-cache).
- **Docket Cache:** `docket-cache-data/runtime.php` gets the staging path.
- **Jetpack:** `jetpack_safe_mode_confirmed` is set to 1, so Jetpack treats the staging site as a copy of live (safe mode).

## Plugins skipped in WP-CLI

Every WP-CLI command on the staging site runs with `--skip-plugins=` and this list:

```
litespeed-cache, wp-rocket, w3-total-cache, wp-super-cache, wp-fastest-cache,
sg-cachepress, breeze, cloudflare, hummingbird-performance, nitropack,
wp-optimize, autoptimize, swift-performance, swift-performance-lite,
cache-enabler, comet-cache, powered-cache, object-cache-pro,
wp-cloudflare-page-cache, wp-hummingbird, nginx-helper, varnish-http-purge,
jetpack-boost, atec-cache-apcu, docket-cache, sqlite-object-cache, wp-redis,
redis-cache, flying-press
```

These plugins hook content and cache events to purge caches or a CDN, and the staging database holds live's settings for those purges. A search-replace or an Elementor CSS flush would otherwise run them. The one exception is the LiteSpeed page purge, which needs LiteSpeed Cache loaded and skips the others.

`--skip-plugins` does not skip must-use plugins or drop-ins: `object-cache.php` and `advanced-cache.php` still load. That is why the drop-in is probed and its paths are fixed before post-processing.

The list is `sideEffectPlugins` in [`internal/sync/sync.go`](../internal/sync/sync.go). When the staging admin is used later, the plugins load normally, with the cleaned settings.
