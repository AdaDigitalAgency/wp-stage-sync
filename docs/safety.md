# Safety

The default sync reads the live site and writes only to the staging site. This page lists the guardrails that enforce this, what each one refuses, and exactly what the tool writes and where.

Promote and restore are the exception: they write live files on purpose, after a backup. See [promote and restore](promote-and-restore.md).

## Guardrails

Each guardrail stops the run with an error. The exact messages and fixes are in [troubleshooting](troubleshooting.md).

| Check | When | Refuses |
|---|---|---|
| Same path | Before connecting | Live and staging webroots that resolve to the same folder, after symlinks are resolved. |
| Same database (config) | Before connecting | The same `DB_NAME` on the same `DB_HOST` in both `wp-config.php` files. |
| Same database (server) | After connecting | Two connections that reach the same server (`@@server_uuid`, else `@@hostname:@@port`) and schema. This catches `localhost` against `127.0.0.1`, and a socket against TCP. |
| Stage host | After connecting | A staging host that cannot be told apart from the live host. |
| W3TC database cluster | Preflight | A live `db-cluster-config.php` that names live's database. |
| Settings check | Preflight | A live option or W3TC config file that the staging cleanup could not parse. |
| Object Cache Pro database | Preflight | Staging and live on the same Redis database index, or an index that cannot be read. |
| Object cache probe | Preflight | Staging and live that share object cache keys. |
| Stage inside live | Preflight | One webroot inside the other (live paths in copied files could not be told apart from staging paths). |
| Same `wp-content` | Promote | Live and staging `wp-content` folders that resolve to the same folder. |
| Restore path | Restore | Archive members outside the extraction folder; removals outside live `wp-content/`. |

The preflight checks run before the export, so nothing on staging has changed when they stop the sync, except the object cache drop-in in the case described in [how it works](how-it-works.md#2-preflight-checks).

## The live side is read-only

In the default sync:

- **Database.** The live connection runs only `SELECT`, `SHOW` and session queries such as `SELECT DATABASE()`. The export runs inside one `READ ONLY`, `REPEATABLE READ` transaction: the server refuses any write in it, and every table is read from the same snapshot (InnoDB), so orders, items and customers match on a busy shop. The checks before the export use the plain connection, with the user from live's `wp-config.php` (or `root`, see below), which can write; their read-only behaviour is a property of the code.
- **One run per site.** A sync, promote or restore locks both webroots. A second run on either path stops at once with the process ID of the first.
- **Files.** Live files are read: the `wp-config.php`, the drop-in, `w3tc-config/`, `db-cluster-config.php`, and `wp-content/` as the rsync source.
- **WP-CLI.** On live, only `wp cache get` of the probe key runs, when live has an object cache drop-in. It runs with `--skip-plugins --skip-themes` and WP-Cron off. WP-CLI still loads live's `wp-config.php`, must-use plugins and drop-ins, which can run their own code.
- **Cache servers.** Staging writes one probe key with a 300-second lifetime and deletes it. If the keys turn out to be shared, that key was in live's cache for that short time. The flushes at the end of a sync are limited to staging keys; see [object cache](object-cache.md#flush-per-drop-in).

## What is written where

| Where | What | Step |
|---|---|---|
| Staging database | `RENAME` of all tables to `_wpss_bak_<n>`, `CREATE`/`INSERT` from the export, `DROP` of the backups (or of the new tables on failure) | Import |
| Staging database | `UPDATE` of integration options, `DELETE FROM <prefix>w3tc_cdn_queue` | After the import |
| Staging database | `UPDATE` of customer rows | Anonymise (when on) |
| Staging database | search-replace of URLs and paths, Jetpack safe mode option | Post-processing |
| Staging `wp-content/object-cache.php`, `w3tc-config/*.php` | Live's copy, or removed with Off | Preflight |
| Staging `wp-content/` | rsync with `--delete`, `chown -R` | File sync |
| Staging config files | Live paths replaced, live per-host configs removed | After the file sync |
| Staging cache folders | Contents deleted | Post-processing |
| Staging object cache | Probe key; prefix delete or flush | Preflight, post-processing |
| `~/.config/wp-stage-sync/` | `config.json` (when the TUI starts a sync), `settings.json`, `.internal`, `wp-cli.phar` | Any run |
| Live `wp-content/themes`, `plugins`, `mu-plugins` | Selected items | Promote, restore |
| `~/.config/wp-stage-sync/sites/<site>/backups/` | Backup archives | Promote |
| Live object cache | Scoped flush (live's keys only) | After promote and restore, with `auto_cache_flush` |

`wp-config.php`, `.htaccess` and WordPress core are never copied or changed on either site.

## Live integrations on staging

The import copies live's CDN, Cloudflare, Sucuri and Varnish credentials to the staging database. Right after the import, before any WordPress code runs on staging, the sync clears them on staging, and every staging WP-CLI command skips cache and CDN plugins. Copied config files and stored paths are rewritten, so staging never loads live's plugin code or deletes live's cache and minified files. The full list is in [cache plugins](cache-plugins.md), with the gaps: Powered Cache's Cloudflare settings and NitroPack's connection are not changed, and credentials in the staging `wp-config.php` only cause a warning.

## Database connections

For each site, the tool tries, in order:

1. The socket in `DB_HOST`, when it has the form `localhost:/path/to/socket`, with the `wp-config.php` user.
2. For a `DB_HOST` of `localhost`, `127.0.0.1` or empty: the common sockets (`/var/run/mysqld/mysqld.sock`, `/run/mysqld/mysqld.sock`, `/var/lib/mysql/mysql.sock`, `/tmp/mysql.sock`, `/var/run/mysql/mysql.sock`) with the `wp-config.php` user.
3. The same sockets as `root` without a password, as on a default Debian or Ubuntu install where root logs in through the socket.
4. TCP to `DB_HOST` and its port (default 3306), with `127.0.0.1` for `localhost`.

Passwords are passed to the driver without parsing, so `@`, `:`, `/`, `?`, quotes and backslashes work. The import raises `max_allowed_packet` for its own session only, never globally.

## WP-CLI and root

The tool runs WP-CLI with `--allow-root`. When you run it as root, the staging site's PHP code runs as root during post-processing. Plugins are skipped as described in [cache plugins](cache-plugins.md#plugins-skipped-in-wp-cli); must-use plugins and drop-ins are not. Treat the staging site's code as code that runs with the tool's privileges.

If `wp` is not in `PATH`, the tool downloads WP-CLI 2.12.0 from GitHub, rejects it unless its SHA-256 matches the value in the code, and keeps it as `~/.config/wp-stage-sync/wp-cli.phar` with mode `0700`. `--update` verifies the new binary against the release `checksums.txt`.

## Anonymisation scope

Anonymisation masks the standard WordPress and WooCommerce customer fields: customer users and their usermeta names, email, phone and address, order addresses and order records in both order stores (names, email, phone, address, IP, user agent, customer note, address search index), the analytics customer table, and order notes. The exact fields are in [how it works](how-it-works.md#5-anonymise).

It does not guarantee that every plugin's stored personal data or secret is removed: API keys, payment and SMTP credentials, form entries, custom tables and logs stay as on live, apart from the cache and CDN credentials listed in [cache plugins](cache-plugins.md). Treat the staging site as sensitive: restrict access to it and keep it out of search engines.
