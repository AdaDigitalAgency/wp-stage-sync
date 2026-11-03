# Configuration

wp-stage-sync keeps one `config.json` per site pair and one global `settings.json`, both under `~/.config/wp-stage-sync/`. The interactive app writes them; you can also edit them by hand.

## File locations

```
~/.config/wp-stage-sync/
├── settings.json                  global settings
├── .internal                      update check state (last check, latest version)
├── wp-cli.phar                    only when wp is not installed; pinned and checksummed
└── sites/
    └── example.com/               the site identifier
        ├── config.json            the site pair
        └── backups/               promote backups
            ├── 2026-11-03_14-05-09.tar.gz
            └── 2026-11-03_14-05-09.json
```

`~` is the home folder of the user that runs the tool. A root cron job reads `/root/.config/wp-stage-sync/`.

The site identifier is the last folder name of the live webroot, past generic folders such as `public_html`: `/home/example.com` and `/home/user/example.com/public_html` both give `example.com`. A site saved under `public_html` by an older version keeps that identifier. See [servers](servers.md#pairing).

On every start, a file in the old layout `sites/<site>.json` is moved to `sites/<site>/config.json`.

## config.json

```
{
  "live_path": "/home/example.com",
  "stage_path": "/home/stage.example.com",
  "order_count": 100,
  "order_preference": "last",
  "anonymize": false,
  "table_modes": {
    "wp_options": "structure_and_data",
    "wp_wc_orders": "custom_rule",
    "wp_actionscheduler_actions": "structure_only"
  },
  "rsync_excludes": null,
  "stage_object_cache_off": true
}
```

| Field | Type | Meaning |
|---|---|---|
| `live_path` | path | The live webroot: the folder that holds `wp-config.php` and `wp-content/`. |
| `stage_path` | path | The staging webroot. |
| `order_count` | integer | How many WooCommerce orders to copy. The TUI starts at 100. |
| `order_preference` | `"last"` or `"first"` | `last` takes the highest order IDs (newest), `first` the lowest. Any other value counts as `last`. |
| `anonymize` | boolean | Mask customer data on staging after the import. See [how it works](how-it-works.md#5-anonymise). |
| `table_modes` | object | Mode per table. See below. |
| `rsync_excludes` | array or `null` | `wp-content/` paths not copied. See below. |
| `stage_object_cache_off` | boolean | Keep the persistent object cache off on staging. Left out of the file when false. See [object cache](object-cache.md#shared-database). |

### `table_modes`

Keys are full table names with prefix; a key without the prefix also matches. A table missing from the object is exported with structure and data.

| Value | Effect |
|---|---|
| `structure_and_data` | Table and all rows. |
| `structure_only` | Table, no rows. |
| `ignore` | Not exported. Because the import replaces every staging table, the table is missing on staging afterwards. |
| `custom_rule` | The built-in rule for the tables listed below. For any other table, the same as `structure_and_data`. |

Some tables have built-in modes that the saved value cannot change:

- Always `structure_only`: `woocommerce_sessions`, `actionscheduler_actions`, `actionscheduler_claims`, `actionscheduler_groups`, `actionscheduler_logs`, `mainwp_child_changes_logs`, `mainwp_child_changes_meta`. Staging gets no sessions and no scheduled actions, so it does not run live's queued jobs.
- With WooCommerce, filtered by the target orders: `wc_orders`, `wc_order_addresses`, `wc_order_operational_data`, `wc_orders_meta`, `wc_order_stats`, `wc_order_product_lookup`, `wc_order_tax_lookup`, `wc_order_coupon_lookup`, `woocommerce_order_items`, `woocommerce_order_itemmeta`, `woocommerce_downloadable_product_permissions`, and the order posts in `posts` and `postmeta` (other posts are kept).
- With WooCommerce, filtered by the kept users or comments: `users`, `usermeta`, `comments`, `commentmeta`, `wc_customer_lookup`, `yith_ywpar_points_log`.

Without WooCommerce (no `wc_orders` and no `woocommerce_order_items` table), the order and user tables have no filter and are exported in full. The filters are described in [how it works](how-it-works.md#database-export).

### `rsync_excludes`

Paths relative to `wp-content/`. Each one is anchored there: `cache` becomes `--exclude=/cache`, which skips `wp-content/cache` but not `wp-content/plugins/foo/cache`. A glob such as `*.log` matches only at the top level of `wp-content/`; add `**/*.log` as well to match at every depth.

| Value | Effect |
|---|---|
| `null` or missing | The [default excludes](#default-excludes) apply, including defaults added in later versions. |
| `[]` | Nothing is excluded. |
| a list | Exactly these paths are excluded. |

An excluded path is neither copied nor deleted on staging. rsync runs with `--delete` but not `--delete-excluded`, so the staging copy of an excluded folder stays as it was. The sync then empties the known cache folders on staging; see [cache plugins](cache-plugins.md).

When `stage_object_cache_off` is true, `object-cache.php` is added to the excludes for the run, and `db.php` too when live's `db.php` is the W3 Total Cache database module. The saved list does not change.

### Default excludes

| Path | Why it is excluded |
|---|---|
| `cache` | Page, minify and object cache files of many plugins. They hold live's pages and are rebuilt on staging. |
| `ewww` | EWWW Image Optimizer's tools folder. |
| `critical-css` | Generated critical CSS. |
| `litespeed` | LiteSpeed Cache's generated CSS, JS and optimisation data. |
| `updraft` | UpdraftPlus backup archives. |
| `archive-master-db` | An archive folder that staging does not need (default since 0.2.2). |
| `boost-cache/cache` | Jetpack Boost page cache. |
| `wphb-cache/cache` | Hummingbird page cache. |
| `wphb-logs` | Hummingbird logs. |
| `wp-cloudflare-super-page-cache` | Super Page Cache fallback cache and logs. |
| `.ht.object-cache*.sqlite*` | SQLite Object Cache database files: a copy would give staging live's cached objects. |

The list is `DefaultExcludes` in [`internal/sync/sync.go`](../internal/sync/sync.go).

## settings.json

```
{
  "backup_retention": 5,
  "auto_cache_flush": true,
  "whats_new_seen": "0.7.0"
}
```

| Field | Default | Meaning |
|---|---|---|
| `backup_retention` | `5` | Promote backups kept per site. After each new backup the oldest are deleted. A value of 0 or less counts as 5. |
| `auto_cache_flush` | `true` | Run `wp cache flush` on live after a promote or a restore, and flush the object cache on staging at the end of a sync. When false, the sync skips its object cache flush (the dry run shows `[skip] … (auto cache flush disabled in settings)`); the page cache clears still run. |
| `whats_new_seen` | empty | The newest version whose "What's new" lines the TUI has shown. |

A missing or unreadable `settings.json` gives the defaults. Edit it in the TUI with `s`; see [the interactive app](interactive.md#settings).
