# Troubleshooting

This page lists the errors and warnings a sync can print, grouped by the step that prints them, with the reason and the fix. Long messages are shortened with "…"; values in angle brackets change per site.

An error stops the run. In unattended mode it is the last line, `Error: <message>`, and the exit status is 1. In the TUI it is on the done screen. A warning starts with `WARNING:` and the run goes on. The TUI lists the run's warnings on the done screen.

Where a sync stops matters:

- **Before the export** (flags, guardrails, hosts, preflight): nothing on staging has changed, apart from the object cache drop-in described in [how it works](how-it-works.md#2-preflight-checks). Fix the cause and run again.
- **Import**: the staging tables are restored as they were.
- **After the import**: staging is partly synced. Fix the cause and run the sync again; it starts from the beginning and repairs staging, including a staging database that still holds live's URLs.

## Flags and saved sites

**`The --site flag requires unattended mode (-u), dry-run mode (-n), or promote mode (--promote).`**

`--site` alone does nothing. Add `-u`, `-n` or `--promote`.

**`no saved config found — run wp-stage-sync interactively first`**

`-u`, `-n`, `--promote` and `--restore` need a saved site. Run `wp-stage-sync` once and start a sync, or check that you run as the same user: the configuration is in that user's `~/.config/wp-stage-sync/`.

**`--site flag required when multiple sites are configured`**

The line before it lists the `--site` values. `wp-stage-sync --list` shows them too.

**`no saved config for site "<id>": reading config: …`**

No `sites/<id>/config.json`. The identifier is the live folder name; see [unattended runs](unattended.md#site-identifiers).

**`Promote mode is not available in unattended mode.` / `Restore mode is not available in unattended mode.` / `Restore mode does not support --site. Use the interactive TUI.` / `Dry-run mode is only available for the default stage sync, not promote/restore.`**

These modes run in the TUI only. See [promote and restore](promote-and-restore.md).

## Site selection and credentials (TUI)

**`no staging site found for <domain> — use Custom paths`**

No folder named `stage.`, `staging.`, `dev.` or `test.` plus the domain was found. Choose **Custom paths...**; see [servers](servers.md#pairing).

**`live wp-config: reading /home/example.com/wp-config.php: …` / `… missing defines in …: DB_NAME, DB_HOST`**

The selected folder has no readable `wp-config.php`, or one of `DB_NAME`, `DB_USER`, `DB_PASSWORD` and `DB_HOST` is not a quoted string in `define()`. A value from `getenv()` cannot be read. Select the folder that holds `wp-config.php`. In unattended mode the same error reads `parsing live wp-config.php: …`.

**`connecting to DB: live db: pinging: …`**

None of the connection methods worked; see [safety](safety.md#database-connections). Test the credentials with `mysql -u <user> -p -h <host> <name>`.

**`invalid order count`**

Enter a whole number above 0.

**`Cannot read wp-content: …`**

Shown on the excludes screen. The live `wp-content` cannot be listed, usually a permission problem. Enter keeps the saved excludes.

## Guardrails

**`SAFETY: live path and stage path are identical (<path>) — aborting to protect production`**

Both paths resolve to one folder, possibly through a symlink. Select the real staging webroot.

**`SAFETY: live and stage use the same database (<name>@<host>) — aborting to protect production`**

Both `wp-config.php` files name the same database on the same host. Give staging its own database.

**`SAFETY: live and stage resolve to the same database (<server>/<schema>) — aborting to protect production`**

The configs differ, but both connections reach the same schema on the same server, for example `localhost` and `127.0.0.1`. Give staging its own database.

**`database connection: live db: …` / `stage db: …`**

See the connection order in [safety](safety.md#database-connections).

**`cannot tell the stage host from the live host (<host>)`**

The staging database's `home` holds live's host (an earlier sync stopped before its search-replace), and the staging folder name is also the live host. Set the staging `home` and `siteurl` to the staging URL in the staging database, or use a staging folder named after the staging host.

## Before the sync

**`another wp-stage-sync run (pid <pid>) is using <path>. Wait for it to finish`**

A sync, promote or restore of the same live or staging webroot is running. The lock is released when that process ends, also when it crashes. Check the process with `ps -p <pid>`.

**`starting a read-only snapshot on live: …`**

The export could not open its read-only transaction on the live database. The detail is the database error.

## Preflight: integrations

**`live's wp-content/db-cluster-config.php (W3 Total Cache database cluster) names live's database "<name>". The file sync would copy it, and stage would read and write live's database. Make the file use the DB_NAME and DB_HOST constants instead of literal values`**

The check reads live's file for the database name in quotes. Change live's `db-cluster-config.php` to use the `DB_NAME`, `DB_HOST`, `DB_USER` and `DB_PASSWORD` constants instead of literal values.

**`WARNING: stage wp-config.php defines <CONSTANT> (<plugin>). If it holds live's credentials, a purge on stage reaches live's zone. Remove it from stage's wp-config.php`**

For `CLOUDFLARE_API_KEY`, `CLOUDFLARE_EMAIL` (Cloudflare plugin), `SWCFPC_CF_API_KEY`, `SWCFPC_CF_API_TOKEN`, `SWCFPC_CF_API_ZONE_ID` (Super Page Cache) and `CDN_SITE_TOKEN` (Breeze, Cloudways). The sync never changes `wp-config.php`. Remove the constant from the staging file.

**`WARNING: Nginx Helper: stage and live use the same cache folder <path>. "Purge Cache" on stage deletes the whole folder, live's pages included. …`**

See [servers](servers.md#nginx-helper-cache-folder).

If a `wp-config.php` cannot be parsed at this point, these checks are skipped without a message; the earlier parse step has normally caught it.

## Preflight: settings check

**`live option <option> (<integration>) has a format the sync cannot change: <reason>. The sync stopped before any change: it could not clear live's keys from stage`**

The option is one that the staging cleanup rewrites (see [cache plugins](cache-plugins.md)), and its value on live is not a valid serialized PHP array or JSON. Common causes are a serialized string with a wrong length after a manual edit or a bad search-replace, or an object (`O:`) where an array is expected. Re-save the plugin's settings on live, or repair the value, then run again. The dry run shows the same as `[check] FAILED: …`.

**`live W3 Total Cache config <file> has a format the sync cannot change: … The sync stopped before any change`**

A `wp-content/w3tc-config/*.php` file starts with `<?php exit; ?>` but is not valid JSON after it. Re-save W3 Total Cache's settings on live.

**`reading live option <option>: …` / `reading <path>: …`**

The live database or file could not be read. Check permissions and the connection.

## Preflight: object cache

**`WARNING: stage and live share <backend>. The sync flushes only stage keys, but a cache purge in the stage admin can clear live's cache. …`**

The sync goes on. Give staging its own database, or choose **Stage object cache: Off**. See [object cache](object-cache.md#shared-database).

**`Stage object cache is off: live's <kind> is not copied to stage`**

Information, with `stage_object_cache_off` set. See [what Off changes](object-cache.md#what-off-changes).

**`Object Cache Pro: stage and live both use Redis database <n>. A stage boot after the sync can run OCP's integrity FLUSHDB on it and clear live's cache. Give stage its own 'database' index in <path>. Nothing on stage was changed`**

Set a different `'database'` in the staging `WP_REDIS_CONFIG`; see [the rule](object-cache.md#object-cache-pro-database-rule).

**`Object Cache Pro: cannot read live's Redis database from WP_REDIS_CONFIG (<reason>)…` / `… cannot read stage's Redis database …`**

The reason is `no WP_REDIS_CONFIG array literal` (the config is missing, uses `array( … )`, or is computed) or `cluster, servers or sentinels setup`. Write `WP_REDIS_CONFIG` as a `[ … ]` literal with a `database` key in both files. Cluster and replication setups cannot be synced with the object cache on; use **Off**.

**`stage and live share object cache keys. Nothing on stage was changed: a sync would make stage read and write live's cache. Give stage its own WP_CACHE_KEY_SALT (or WP_REDIS_PREFIX) in <path>, then sync again`**

The probe key written through staging was readable through live. Set a staging salt; see [key salts and prefixes](object-cache.md#key-salts-and-prefixes). With a separate database the keys are separate too.

**`object cache check (nothing on stage was changed): writing the probe key on stage: …`**

`wp cache set` failed on staging with live's drop-in in place. The output follows the message: often the cache server refused the connection from staging's settings, or the staging `wp-config.php` is broken. Run `wp cache set test 1 --path=<staging path> --allow-root --skip-plugins --skip-themes` to see the error.

**`object cache check (nothing on stage was changed): reading the probe key on live: …` / `unexpected probe value on live: "<value>"`**

`wp cache get` on live failed with something other than "not found", or returned another value. Run `wp cache get test --path=<live path> --allow-root --skip-plugins --skip-themes` to see the error. The sync does not go on without a clear answer.

**`W3 Total Cache config (nothing on stage was changed): …`**

Live's `w3tc-config` files could not be written to staging for the probe. Check permissions on the staging `wp-content/w3tc-config/`.

**`copying the drop-in to stage: …`**

The staging `wp-content/` is not writable.

**`wp-cli: wp-cli not found and php not available to run the PHAR fallback; install wp-cli or php` / `wp-cli: downloading wp-cli.phar: …` / `… verifying wp-cli 2.12.0: checksum mismatch …`**

Install WP-CLI as `wp` in `PATH`. The fallback needs `php` and access to GitHub, and refuses a download whose SHA-256 does not match.

## Export and import

**`export: target orders: …` / `export: safe users: …`**

The live `wc_orders` or user tables could not be read. The detail is the database error.

**`Note: could not raise session max_allowed_packet; raise the server's max_allowed_packet if a very large row fails to import`**

Information. MySQL does not allow a session value. If the import then fails with a packet error, raise `max_allowed_packet` in the MySQL server config.

**`import: executing <group> SQL: <error> stmt: <first 200 characters>`**

A statement failed on staging; the old staging tables are restored. Typical causes are `max_allowed_packet` (above), a storage engine or collation that the staging server lacks, or a full disk.

**`WARNING: could not restore <table> from backup <_wpss_bak_n>: …`**

The rollback after a failed import could not rename a table back. The table is still in the staging database as `_wpss_bak_<n>`; rename it by hand. The next sync drops leftover `_wpss_bak_` tables.

**`switching off <integration> on stage: …` / `switching off <integration> on stage (<option>): …`**

The cleanup after the import failed. The sync stops so that staging does not run with live's keys. Do not open the staging site; fix the database error and run again.

**`anonymize: …`**

An anonymisation query failed. Staging holds unmasked customer data until a sync completes.

## File sync and paths

**`file sync: rsync: exit status <n>: <last lines>`**

The last ten lines of rsync's output follow. Status 23 is usually a permission problem on a source or destination file.

**`file sync: chown: …`**

The run is not allowed to `chown`. Run as root, or as a user that owns the staging files.

**`one site is inside the other (live <live>, stage <stage>): live paths in the copied files could not be told apart from stage paths. Move stage out of live's folder`**

One webroot is a subfolder of the other. The check runs before any change. Move the staging webroot out of the live folder and update `stage_path`.

**`removing the stage object cache: …` / `switching off <setting> on stage: …`**

With Off, the drop-in could not be deleted or a setting could not be changed. Check permissions on the staging `wp-content/`.

## Post-processing

These lines start with `Skipping:` (a command that does not run) or `WARNING: <step> failed, continuing:` (a cache step that failed). The sync still finishes.

**`Skipping: Cache flush (WARNING: stage has no WP_REDIS_PREFIX or WP_CACHE_KEY_SALT, so a flush would clear the whole Redis database, live included. Flush the stage cache by hand)`**

Redis Object Cache without a staging prefix. Set one; see [key salts and prefixes](object-cache.md#key-salts-and-prefixes). Other skip reasons name the drop-in: WP Redis without `WP_CACHE_KEY_SALT`, Hummingbird without `WP_REDIS_SALT`, a live prefix that starts with staging's, Memcached that flushes the whole server, atec Cache APCu, an unknown drop-in, or `cannot read live's Redis database from WP_REDIS_CONFIG` for Object Cache Pro.

**`Skipping: Cache flush (auto cache flush disabled in settings)`**

`auto_cache_flush` is off; see [configuration](configuration.md#settingsjson).

**`WARNING: Cache flush (LiteSpeed object cache, stage prefix only) failed, continuing: … the Memcached backend cannot delete keys by prefix`**

LiteSpeed's object cache on Memcached cannot be flushed by prefix. Flush it from the staging admin only if staging has its own Memcached server. With `this LiteSpeed Cache version has no 64-character key prefix; update the plugin`, update LiteSpeed Cache.

**`WARNING: Cache flush (…) failed, continuing: … not a Redis Object Cache connection` / `no WP_REDIS_PREFIX` / `unsupported Redis client` / `WP Redis is not connected on stage` / `Object Cache Pro is not connected on stage` / `stage uses the same Redis database as live …` / `the flush returned false`**

The flush code checks the staging connection again before it deletes. The staging site's cache could not be reached or did not match what the preflight read. Check the staging cache settings; flush by hand if needed.

**`WARNING: LiteSpeed page cache purge failed, continuing: … LiteSpeed Cache is not active or too old` / `Could not notify the web server to purge.`**

LiteSpeed Cache is installed but not active on staging, or too old for `kick_purge_queue()`, or the web server is not LiteSpeed. Purge the page cache from the staging admin.

**`WARNING: <plugin> files failed, continuing: refusing to clear <path>: not under a stage cache folder`**

A cache folder path is outside the allowed staging cache folders, for example through an unusual host name. Nothing was deleted. Clear it by hand.

**`post-processing: running <wp command>: exit status 1: …`**

A search-replace, Elementor URL replace or the Jetpack command failed, and the sync stopped. The last lines of WP-CLI's output follow. Run the command by hand on staging to see the full error; a fatal PHP error in a must-use plugin is a common cause.

**`post-processing: live and stage resolve to the same host (<host>): refusing to post-process`**

A search-replace from a host to itself would leave live's URLs on staging. Normally the host check before the export catches this; see [guardrails](#guardrails).

## Dry run

**`rsync preview unavailable: …`**

`rsync --dry-run` failed; the rest of the plan is still printed.

**`[check] FAILED: …`**

A real sync would stop in the [settings check](#preflight-settings-check).

The dry run does not run the probe, the Object Cache Pro check or the integration checks; see [unattended runs](unattended.md#dry-run).

## Promote and restore

**`select at least one item to promote`**

Check an item with Space.

**`<type> "<name>" does not exist on staging at <path>`**

The item was removed from staging after the list was shown.

**`SAFETY: live and stage wp-content paths are identical (<path>) — aborting to protect production`**

The two `wp-content` folders are the same folder, for example through a symlink.

**`backup: archiving <type> <name>: …`**

The live copy could not be archived, for example because it contains a symbolic link. Nothing on live changed.

**`promote failed for <type> <name>: … — live restored from backup`**

rsync failed. Every item in the backup was put back.

**`promote failed for <type> <name>: …; live may be inconsistent — automatic restore failed: …`**

The automatic restore also failed. Restore by hand from `~/.config/wp-stage-sync/sites/<site>/backups/<timestamp>.tar.gz`.

**`restore: extracting archive: unsafe path in archive: …`**

The archive has a member that would be written outside the temporary folder. It was not made by this tool, or was changed. Nothing was restored.

**`WARNING: skipping removal of "<path>" (outside live wp-content)`**

The backup's JSON names a path outside live `wp-content/`. It was not deleted.

**`Retention cleanup warning: …` / `Cache flush warning: …`**

Old backups could not be deleted, or `wp cache flush` on live failed. The promote itself succeeded.

**`WARNING: live object cache not flushed: …`**

After a promote or restore, live's object cache was not flushed, because the drop-in's flush would also clear other sites on the same server (for example Redis Object Cache without `WP_REDIS_PREFIX`). Flush live's cache by hand if the new code needs it, or give live a key prefix.

## Settings and updates

`settings.json` that cannot be read or parsed is replaced by the defaults without a message: backup retention 5, auto cache flush on. Open the settings screen and press Esc to write a valid file.

**`Update failed: …`**

`--update` could not reach GitHub, find a release for the platform, verify the checksum, or replace the binary. Run it as a user that can write the binary's folder.
