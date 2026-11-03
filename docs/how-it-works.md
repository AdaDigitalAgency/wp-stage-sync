# How a sync works

A sync runs a fixed list of steps, the same in the TUI and in unattended mode: checks that change nothing, then the database, then the files, then WP-CLI on the staging site. This page lists them in order and says what each one reads and writes.

The code is `runUnattended` in [`cmd/wp-stage-sync/main.go`](../cmd/wp-stage-sync/main.go) and `runSync` in [`internal/tui/tui.go`](../internal/tui/tui.go). Both call the same functions in [`internal/sync/`](../internal/sync/).

## Step order

### 1. Guardrails and connection

1. Parse the live and staging `wp-config.php`: `DB_NAME`, `DB_USER`, `DB_PASSWORD`, `DB_HOST`, `$table_prefix`, and every `define()` with a string value.
2. Refuse when both paths resolve to the same folder, symlinks resolved.
3. Refuse when both configs name the same database on the same `DB_HOST`.
4. Connect to both databases.
5. Refuse when both connections reach the same server and schema, compared by `@@server_uuid` (or `@@hostname:@@port`) and `DATABASE()`.
6. Read the hosts. The live host comes from live's `home` option, then `siteurl`, then the live folder name. The staging host comes the same way from the staging database. If that equals the live host, because an earlier sync stopped before its search-replace, the staging folder name is used. If that is also the live host, the sync stops.

See [safety](safety.md) for each guardrail.

### 2. Preflight checks

Shown as **Checking object cache isolation**. All of it runs before the export.

1. **Integrations** ([`preflight.go`](../internal/sync/preflight.go)): refuse when one webroot is inside the other, and when live's `wp-content/db-cluster-config.php` names live's database. Warn when the staging `wp-config.php` defines Cloudflare or CDN credentials, and when Nginx Helper on both sites uses one cache folder.
2. **Settings check** ([`neutralize_check.go`](../internal/sync/neutralize_check.go)): read every live option and W3 Total Cache config file that step 4 will change, and run the change in memory. If one has a format the sync cannot parse, stop. Live is only read.
3. **Object cache** ([`objectcache.go`](../internal/sync/objectcache.go)): if live has a `wp-content/object-cache.php` drop-in, warn when staging would share live's Redis database or Memcached server, refuse an Object Cache Pro setup where both use one Redis database, then put live's drop-in (with paths fixed) on staging and run the isolation probe. With **Stage object cache: Off**, the staging drop-in is removed instead and no probe runs. See [object cache](object-cache.md).

If the probe refuses, the staging drop-in and W3 Total Cache config files are put back as they were. If it passes, the copied drop-in stays: it is the file the file sync would write anyway. Apart from that drop-in, nothing on staging has changed when a preflight check stops the sync.

### 3. Export

Read live's tables and build the SQL in memory: `CREATE TABLE` for every exported table, then `INSERT` statements in batches of up to 8 MB. Binary values are written as hex literals, and generated columns are left out. See [database export](#database-export) for the filters.

### 4. Import and switch off live integrations

1. Turn off foreign key checks for the session, and try to raise the session `max_allowed_packet` to 256 MB. On MySQL, where that variable is read-only per session, a note is printed instead; raise the server setting if a large row fails.
2. Rename every staging table to `_wpss_bak_<n>`. Backups left by a crashed run are dropped first.
3. Run the export: structure-only tables, then the others, then users, then orders.
4. On success, drop the `_wpss_bak_` tables. On failure, drop the new tables and rename the backups back, so the staging database is as before.
5. Right after the import, before any WordPress code runs on staging, clear the live integrations in the staging options table: Cloudflare, CDN, Sucuri and Varnish credentials and switches of the plugins in [cache plugins](cache-plugins.md), the W3 Total Cache CDN queue, and the Redis failback flush flag. A failure here stops the sync, because staging would keep live's API keys.

### 5. Anonymise

Only when `anonymize` is on. On the staging database:

- Users whose role list contains `customer`: email `customer_<ID>@example.com`, login and nicename `customer_<ID>`, display name `Customer <ID>`.
- Their usermeta: first and last names, billing email and phone, shipping phone, billing and shipping address lines, companies, city and postcode.
- `wc_order_addresses` (if present): every row gets placeholder name, company, address, email and phone.
- `wc_orders` (if present): billing email, IP address, user agent and customer note. In `wc_orders_meta`, the billing and shipping address search index is cleared.
- Orders stored as posts: the `_billing_*` and `_shipping_*` name, company, address, email and phone meta, the address search index, `_customer_ip_address` and `_customer_user_agent`; the customer note (`post_excerpt`) is cleared.
- `wc_customer_lookup` (if present): username, names, email, city and postcode.
- Order notes: author email and IP cleared, and the text of customer notes replaced with `[customer note removed]`.

Users with any other role keep their data. Countries, states, order totals, line items and plugin tables are not changed. See [safety](safety.md#anonymisation-scope).

### 6. File sync

```
rsync -a --delete --exclude=/cache --exclude=/ewww ... /home/example.com/wp-content/ /home/stage.example.com/wp-content/
```

Only `wp-content/` is copied. `wp-config.php`, `.htaccess` and WordPress core are not. Excludes are anchored to `wp-content/` and excluded paths are neither copied nor deleted; see [configuration](configuration.md#rsync_excludes). Then `chown -R <uid>:<gid>` sets the staging `wp-content/` to the owner of the staging webroot folder, or `33:33` (`www-data` on Debian and Ubuntu) if that cannot be read.

### 7. Fix live paths

Before WordPress loads the new files, the copied configs that hold live's absolute path get the staging path ([`livepaths.go`](../internal/sync/livepaths.go)):

- `advanced-cache.php`, `object-cache.php`, `wp-cache-config.php`, `sgo-config.php`, `autoptimize_404_handler.php`, `mu-plugins/swift-performance-loader.php`, `breeze-config/*.php` (also live's home URL) and `docket-cache-data/runtime.php`.
- `w3tc-config/*.php`: the same changes as the database config of W3 Total Cache, plus `config.path`.
- Live's per-host config copies are deleted: `wpo-cache/config/config-<live host>.php` (WP-Optimize) and `settings/cache-enabler/<live host>.php` (Cache Enabler). The plugins rebuild them for the staging host.

Only whole path prefixes change: `/home/example.com/` and `/home/example.com` before a quote. The sync stops here if the staging path is inside the live path.

### 8. Stage object cache off

Only with `stage_object_cache_off`. Remove `object-cache.php`, and `db.php` when it is the W3 Total Cache database module; set `objectcache.enabled` and `dbcache.enabled` to false in `w3tc-config/*.php`; clear `litespeed.conf.object`; and switch off Redis in `wphb_settings`. See [object cache](object-cache.md#what-off-changes).

### 9. Post-processing

WP-CLI runs on the staging site, in this order. Every command has `--allow-root --path=<staging path>` and `--skip-plugins=<list>`; see [skipped plugins](cache-plugins.md#plugins-skipped-in-wp-cli).

1. `wp search-replace https://<live host> https://<stage host> --all-tables`
2. The same for `http://`.
3. The same for `//` (protocol-relative). The scheme forms run first, so this pass touches only real protocol-relative URLs, not a bare host name in an email address.
4. `wp search-replace /home/example.com/ /home/stage.example.com/ --all-tables`: live's absolute path. Skipped with a warning when one path is inside the other.
5. `wp elementor replace-urls` for `https://` and `http://`, when `plugins/elementor` exists on staging.
6. `wp elementor flush-css`, same condition.
7. `wp option update jetpack_safe_mode_confirmed 1`, when `plugins/jetpack` exists.
8. The object cache flush for the staging drop-in kind; see [object cache](object-cache.md#flush-per-drop-in). Skipped when `auto_cache_flush` is off, or with a warning when the flush could reach live.
9. LiteSpeed Cache page purge, when `plugins/litespeed-cache` exists. This command loads LiteSpeed Cache and skips the other plugins.
10. Empty the staging cache folders of WP Rocket, WP Super Cache, Powered Cache, WP Fastest Cache, W3 Total Cache, WP-Optimize, Breeze, Cache Enabler, Comet Cache, Swift Performance, Autoptimize, SiteGround Optimizer, Jetpack Boost, Hummingbird and Super Page Cache, each when its plugin folder exists. The folders are in [cache plugins](cache-plugins.md#overview).

Steps 6 and 8 to 10 only clear caches: a failure is printed as a warning and the sync goes on, because the staging data is already correct. A failure in the other commands stops the sync.

The folder clears delete the contents of a folder and keep the folder. They refuse any path outside the staging `wp-content/cache`, `boost-cache/cache`, `wphb-cache/cache` and `wp-cloudflare-super-page-cache` folders.

## Database export

Without WooCommerce (no `wc_orders` and no `woocommerce_order_items` table), every table is exported by its [table mode](configuration.md#table_modes).

With WooCommerce, in either order store:

1. **Target orders**: the newest (`last`) or oldest (`first`) `order_count` orders by ID, plus their refunds. With HPOS (`woocommerce_custom_orders_table_enabled` is `yes`) they come from `wc_orders`; otherwise from `wp_posts` (the legacy order store).
2. **Kept users**: every user whose capabilities do not contain `customer`, every user with more than one role, and the customers of the target orders. Other customers are left out.
3. **Order tables**: the HPOS and lookup tables, `woocommerce_order_items` and `woocommerce_downloadable_product_permissions` are filtered by order ID, `woocommerce_order_itemmeta` by the order item IDs of those orders.
4. **Orders stored as posts**: `wp_posts` keeps every post except orders (`shop_order`, `shop_order_refund`, `shop_order_placehold`, `shop_subscription`), plus the target orders; `wp_postmeta` drops the meta of the other orders. This covers the legacy store and the copies HPOS keeps when compatibility sync is on. A `posts` or `postmeta` table mode of `ignore` or `structure_only` is respected.
5. **Analytics customers** (`wc_customer_lookup`): the customers of the target orders and the kept users.
6. **Comments**: every comment that is not an order note, plus the order notes of the target orders; `commentmeta` follows the same set.
7. **Users**: `users` and `usermeta` for the kept users.
8. **YITH points log** (`yith_ywpar_points_log`): rows of the target orders or kept users.
9. **Everything else**: by its table mode.

Subscriptions (`shop_subscription`) are not target orders, so staging gets none.

## What touches live

The default sync only reads live:

| Live resource | Access |
|---|---|
| Database | `SELECT` and `SHOW` statements only. |
| `wp-config.php`, `wp-content/` files | Read: configs, the drop-in, `w3tc-config/`, `db-cluster-config.php`, and rsync as the source. |
| WP-CLI | Only `wp cache get` of the probe key, with `--skip-plugins --skip-themes` and WP-Cron off, when live has an object cache drop-in. |

Everything else (`DROP`, `RENAME`, `INSERT`, `UPDATE`, rsync `--delete`, `chown`, file edits, WP-CLI commands) targets the staging site. The probe and the staging flushes reach a cache server only through the staging site's keys; see [object cache](object-cache.md). Promote and restore write live files on purpose; see [promote and restore](promote-and-restore.md).
