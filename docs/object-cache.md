# Object cache

A persistent object cache drop-in (`wp-content/object-cache.php`) is copied to the staging site with the other files, so staging connects to the same Redis or Memcached server as live. This page explains how the sync makes sure staging never reads, writes or flushes live's cache keys, and what to change when the two sites share a database.

The code is in [`internal/sync/objectcache.go`](../internal/sync/objectcache.go) and [`internal/sync/stagecache.go`](../internal/sync/stagecache.go).

## Drop-ins the sync knows

The sync reads the first 4 KB of live's `object-cache.php` and looks for a marker:

| Kind | Marker in the drop-in |
|---|---|
| Redis Object Cache (Till Krüss) | `Redis Object Cache Drop-In` |
| LiteSpeed Cache object cache | `LSCWP_OBJECT_CACHE` |
| Object Cache Pro | `Object Cache Pro` |
| W3 Total Cache | `W3 Total Cache Object Cache` |
| Hummingbird Redis | `Hummingbird Redis Object Cache` |
| WP Redis (Pantheon) | `WP_REDIS_OBJECT_CACHE` |
| Docket Cache | `Docket Cache Drop-in` |
| SQLite Object Cache | `SQLite Object Cache (Drop-in)` |
| atec Cache APCu | `atec Object-Cache` |
| Memcached 4.x (wordpress.org drop-in) | `Plugin Name: Memcached`, its description, and `Version: 4.` or higher |
| Memcached Redux, Memcached before 4.0 | `Plugin Name: Memcached` |
| Any other drop-in | none of the above |

With no `object-cache.php` on live, none of this applies: post-processing runs a plain `wp cache flush`.

## The isolation probe

Before the export, when live has a drop-in and the stage object cache is on:

1. Live's drop-in, with live's paths replaced, is written to the staging `wp-content/object-cache.php`. For W3 Total Cache, live's `w3tc-config/*.php` files are also written to staging with the staging changes, including the new `instance_id`.
2. On staging: `wp cache set probe-<random> <random> wp-stage-sync 300`.
3. On live: `wp cache get probe-<random> wp-stage-sync`.
4. On staging: `wp cache delete` of the key.

All three commands run with `--skip-plugins --skip-themes` and with WP-Cron off (`DISABLE_WP_CRON` through `--exec`). The live command only reads.

| Live returns | Result |
|---|---|
| The value | The two sites share cache keys. The sync stops, and staging gets its old drop-in and W3TC files back. |
| "not found" | The keys are separate. The copied drop-in stays and the sync goes on. |
| Any other error or value | The sync stops with the error. Staging is restored the same way. |

Shared keys would make every staging WP-CLI command, and every staging page load, read and write live's cache. Fix it with a staging key salt; see [key salts and prefixes](#key-salts-and-prefixes).

atec Cache APCu is not probed. APCu memory belongs to one PHP process pool, which WP-CLI never shares. atec's keys include `DB_NAME`, so the two sites cannot share keys.

## Flush per drop-in

At the end of the sync, the staging object cache is flushed, but only in a way that cannot reach live's keys. A flush that would be skipped prints a warning that ends with "Flush the stage cache by hand". A flush that fails is a warning too. With `auto_cache_flush` off in [settings](configuration.md#settingsjson), no flush runs.

| Drop-in on staging | What the sync does |
|---|---|
| None | `wp cache flush`. |
| Redis Object Cache | Deletes only keys that start with the staging prefix (`WP_REDIS_PREFIX`, else `WP_CACHE_KEY_SALT`), with `SCAN` and `DEL` through the drop-in's connection (phpredis or Predis). Skipped when staging has no prefix, or when live's prefix starts with staging's. |
| LiteSpeed Cache | Deletes only keys that start with the staging `LSOC_PREFIX` (Redis). That prefix is a SHA-256 of the salt, `DB_NAME` and table prefix, so no salt is needed. Fails with a warning with Memcached, which cannot list keys, and with plugin versions whose prefix is not 64 characters. |
| Object Cache Pro | Flushes the staging Redis database (`FLUSHDB`) after it checks that the index differs from live's and that there is no cluster, `servers` or sentinel setup. See [below](#object-cache-pro-database-rule). |
| WP Redis | Deletes only keys that start with the staging `WP_CACHE_KEY_SALT` (whitespace removed, as WP Redis does). Skipped without a salt, or when live's salt starts with staging's. Its own flush always runs `FLUSHDB`. |
| Hummingbird Redis | `wp cache flush` when staging has its own `WP_REDIS_SALT`; with a salt, Hummingbird deletes only the salted keys. Skipped without a salt (its flush would run `FLUSHDB`) or when live's salt starts with staging's. |
| W3 Total Cache | `wp cache flush`. Staging has its own `instance_id`, so the flush changes staging keys only. |
| Memcached 4.x | `wp cache flush`, which writes a new flush number under the staging salt and table prefix instead of clearing the server. |
| SQLite Object Cache, Docket Cache | `wp cache flush`. Their data is in staging's own `wp-content`. |
| Memcached Redux, Memcached before 4.0 | Skipped: their flush clears the whole Memcached server. |
| atec Cache APCu | Skipped: WP-CLI cannot reach the web server's APCu memory. Clear the object cache in the staging admin (atec Cache APCu). |
| Any other drop-in | Skipped: the scope of its flush is unknown. |

Salts and prefixes are read from the `wp-config.php` files as string literals. A constant set from `getenv()` or another expression counts as missing.

## Object Cache Pro database rule

With Object Cache Pro, the sync refuses to start unless staging uses its own Redis database index. On a database that live also uses, the first staging boot after a config or version change runs OCP's integrity check, which can run `FLUSHDB` and clear live's cache. A prefix delete is not safe either: it removes OCP's metadata key and causes the same `FLUSHDB`.

The index is read from `define('WP_REDIS_CONFIG', [ ... ]);` in each `wp-config.php`, without WordPress:

- The value must be a short array literal (`[ ... ]`). `array( ... )` or a computed value cannot be read, and the sync stops.
- A config with `cluster`, `servers` or `sentinels` cannot be checked, and the sync stops.
- Without a `database` key, the index is 0.

The check runs before the probe. Example for the staging `wp-config.php`, with live on database 0:

```
define('WP_REDIS_CONFIG', [
    'token' => '...',
    'host' => '127.0.0.1',
    'port' => 6379,
    'database' => 1,
    'prefix' => 'stage_example_com:',
]);
```

## Shared database

The sync's own flush is limited to staging keys. A person in the staging admin is not limited that way. LiteSpeed Cache "Purge All", Redis Object Cache without selective flush, WP Redis, and plugins that call `wp_cache_flush()` (Breeze, WP-Optimize, Super Page Cache) clear the whole Redis database or Memcached server, live's keys included.

So the sync also checks whether staging would use the same backend as live after the sync:

| Drop-in | Counts as shared when |
|---|---|
| Redis Object Cache | Both resolve to the same `WP_REDIS_PATH` (or `WP_REDIS_HOST`:`WP_REDIS_PORT`, default `127.0.0.1:6379`) and `WP_REDIS_DATABASE` (default 0). Always, with `WP_REDIS_SERVERS` or `WP_REDIS_CLUSTER`. |
| Hummingbird Redis | Same `WPHB_REDIS_HOST`, `WPHB_REDIS_PORT` and `WPHB_REDIS_DB_ID`. |
| Object Cache Pro | Same `database` index (the sync refuses this case anyway). |
| WP Redis | Same `database` in `$redis_server`. |
| LiteSpeed Cache | Always: staging gets live's `.litespeed_conf.dat` with live's server and database. |
| W3 Total Cache | Unless live's object cache engine is `file` or `apc`. |
| Memcached (all versions) | Always. |
| Any other drop-in | Always: its backend is unknown. |
| SQLite, Docket Cache, APCu, none | Never. |

When a value cannot be read, the backend counts as shared.

If it is shared, the sync goes on and prints:

```
WARNING: stage and live share <backend>. The sync flushes only stage keys, but a cache purge in the stage admin can clear live's cache. Give stage its own database, or switch the stage object cache off for this site
```

The dry run prints the same warning. In the TUI, the confirm screen shows **Stage object cache: On (shared with live)**, which `↑` or `↓` turns to **Off**.

### Why a separate database is better

A separate Redis database (or a separate Redis or Memcached instance) keeps the staging object cache, so staging behaves like live, and nothing in the staging admin can clear live's cache. **Off** is the fallback: staging works without a persistent object cache, which is slower and can hide bugs that only show with one.

Separate databases on one Redis server are fine for Redis Object Cache, Object Cache Pro, WP Redis and Hummingbird, because they read the database from the staging `wp-config.php`, which the sync never copies. Memcached has no databases: use a second instance on another port, set in the staging `wp-config.php`. The sync does not read Memcached server lists, so it keeps the warning and the toggle for Memcached even then.

LiteSpeed Cache and W3 Total Cache keep their server and database in plugin settings, and every sync copies live's (the database options, `.litespeed_conf.dat`, `w3tc-config/`). A staging-only change there does not survive the next sync, so the sync always counts them as shared. For these two, use **Off**, or keep the object cache on and accept that a purge in the staging admin can clear live's cache.

Examples for the staging `wp-config.php`:

```
// Redis Object Cache
define('WP_REDIS_DATABASE', 1);

// Hummingbird Redis
define('WPHB_REDIS_DB_ID', 1);
```

```
// WP Redis
$redis_server = [
    'host' => '127.0.0.1',
    'port' => 6379,
    'database' => 1,
];
```

Run a sync again after the change. The warning disappears when the sync can read both values and they differ.

### What Off changes

**Stage object cache: Off** is saved with the site as `"stage_object_cache_off": true` and applies to every later sync, including unattended runs. Each sync then:

1. Removes the staging `object-cache.php` before the export, and `db.php` when it is W3 Total Cache's database module. No probe runs.
2. Excludes `object-cache.php` (and that `db.php`) from the file sync.
3. After the file sync, removes them again if present, and switches off the settings that would put a drop-in back on staging: `objectcache.enabled` and `dbcache.enabled` in `w3tc-config/*.php` and in the `w3tc_config_0` options (database config mode), the `litespeed.conf.object` option, and Redis in `wphb_settings`.
4. Runs a plain `wp cache flush` in post-processing, which only clears the request cache.

To turn the object cache back on, set the toggle to **On** on the TUI confirm screen. Once Off is saved, the toggle stays visible, also after you give staging its own database. In unattended runs, set `stage_object_cache_off` to false in the site's `config.json`.

## Key salts and prefixes

The probe needs the two sites to use different keys. Set a salt or prefix in the staging `wp-config.php`. The sync never copies `wp-config.php`, so the value stays.

| Drop-in | Constant | Note |
|---|---|---|
| Redis Object Cache | `WP_REDIS_PREFIX`, or `WP_CACHE_KEY_SALT` | The drop-in uses `WP_CACHE_KEY_SALT` as the prefix when `WP_REDIS_PREFIX` is not set. |
| WP Redis | `WP_CACHE_KEY_SALT` | |
| Hummingbird Redis | `WP_REDIS_SALT` | Without it, the flush is skipped. |
| Memcached 4.x | `WP_CACHE_KEY_SALT` | |
| LiteSpeed Cache | none | The prefix includes `DB_NAME` and the table prefix. |
| W3 Total Cache | none | The sync gives staging its own `instance_id`. |
| Object Cache Pro | `'prefix'` in `WP_REDIS_CONFIG` | A separate `database` is required anyway. |

```
// staging wp-config.php
define('WP_CACHE_KEY_SALT', 'stage.example.com:');
define('WP_REDIS_PREFIX', 'stage.example.com:');
define('WP_REDIS_SALT', 'stage.example.com:');
```

Use a value that live's prefix does not start with. If live's prefix starts with staging's, for example live `example` and staging `ex`, a staging prefix delete would also match live's keys, and the flush is skipped.
