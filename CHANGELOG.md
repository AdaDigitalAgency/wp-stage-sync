# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Elementor CSS is regenerated, the LiteSpeed page cache is purged, and the WP Rocket cache folders are emptied on staging after a sync, when those plugins are installed. A failed purge is a warning.
- The LiteSpeed Cache object cache is flushed on staging by deleting only the staging keys (Redis). With Memcached the flush is skipped with a warning.
- Object Cache Pro is flushed on staging when staging uses its own Redis database index.
- WP Super Cache, Powered Cache and WP Fastest Cache stage page caches are emptied after a sync.
- After the import, staging gets live integrations switched off: Cloudflare purge in LiteSpeed Cache, WP Rocket, WP Fastest Cache and the Cloudflare plugin, WP Rocket Sucuri sync, WP Fastest Cache Varnish, and the Redis failback flush flag. A "Purge All" in the staging admin no longer purges the live Cloudflare zone.
- More cache plugins are switched off on staging: Super Page Cache (Cloudflare keys and rule IDs), Hummingbird (Cloudflare), Swift Performance (Cloudflare and MaxCDN; Memcached mode becomes disk cache), Proxy Cache Purge (tag purges), the WP Rocket RocketCDN token, and W3 Total Cache (Cloudflare, CDN, full-site CDN and message bus, in `w3tc-config/*.php` or the database config, plus the CDN push queue).
- W3 Total Cache on staging gets its own `instance_id`, so its object, database and page cache keys never match live's on a shared Redis or Memcached server.
- Stage cache folders of W3 Total Cache, WP-Optimize, Breeze, Cache Enabler, Comet Cache, Swift Performance, Autoptimize, SiteGround Optimizer, Jetpack Boost, Hummingbird and Super Page Cache are emptied after a sync.
- Object cache flush support for WP Redis (staging salt only), Hummingbird Redis (with a staging salt), W3 Total Cache, Memcached 4.x, SQLite Object Cache and Docket Cache. Memcached Redux, Memcached before 4.0 and atec Cache APCu are skipped with a warning.
- **Stage object cache: Off.** When staging would share live's Redis database or Memcached server, the TUI confirm screen offers to switch the staging object cache off for the site. The drop-in is then not copied, and the plugin settings that would put it back are switched off on staging. Without it, the sync warns that a purge in the staging admin can clear live's cache.
- Before the sync starts, every live setting that the staging cleanup changes is read and test-changed in memory (live is only read). If one has a format the sync cannot change, the sync stops before any change, instead of after the import. The dry run shows the same check.
- Warnings before the sync for Cloudflare credentials in the staging `wp-config.php` and for an Nginx Helper cache folder that both sites share.
- Powered Cache's Cloudflare settings are switched off on staging. Before, a purge in the staging admin purged the live zone.
- The TUI done screen lists every warning of the run. Before, a warning was shown only until the next message replaced it.
- **Stage object cache: Off** stays visible on the confirm screen once saved, so it can be set back to On. With W3 Total Cache in database config mode, Off also changes the `w3tc_config_0` options.
- Documentation: a new `docs/` folder, published as docs pages on the website (now an Astro site), with a page per topic and a reference of every cache plugin, refusal and warning.
- Site discovery reads LiteSpeed Enterprise's own XML config (`/usr/local/lsws/conf/httpd_config.xml`, including vhost templates) and OpenLiteSpeed's `httpd_config.conf`, and cPanel's `/etc/apache2/conf/httpd.conf`. Before, a site outside `/home`, `/var/www` and `/srv/www` on these servers was not listed.
- The default rsync excludes also skip `boost-cache/cache`, `wphb-cache/cache`, `wphb-logs`, `wp-cloudflare-super-page-cache` and SQLite Object Cache database files.

### Fixed

- **Customer data reached staging on shops without HPOS.** Order filtering read only the HPOS `wc_orders` table. On a shop that stores orders in `wp_posts`, every order and its billing and shipping meta was copied, and anonymisation did not touch them. Orders are now filtered in both stores, including the copies HPOS compatibility sync keeps in `wp_posts`, and anonymisation covers order postmeta, customer notes and the HPOS address search index. `wc_customer_lookup` and `woocommerce_downloadable_product_permissions` are filtered and anonymised too.

- **A sync could purge the live Cloudflare zone and flush the live object cache on LiteSpeed Cache sites.** LiteSpeed Cache answers Elementor's URL replace with a full purge, which runs with live's settings and Cloudflare credentials from the copied database. Staging WP-CLI commands now skip cache and CDN plugins.
- **The staging cache flush could clear the live object cache.** The object cache drop-in is copied to staging, so staging used live's Redis server, and `wp cache flush` cleared the whole database. With Redis Object Cache, only keys with the staging prefix are deleted now. Without a staging prefix, or with any other drop-in, the flush is skipped with a warning.
- The sync now refuses to start when Object Cache Pro on staging uses live's Redis database: OCP's integrity check can run `FLUSHDB` on it from a staging boot.
- Live absolute paths in the copied `advanced-cache.php`, `object-cache.php` and `wp-cache-config.php` are replaced with the staging path. Before, a WP Super Cache clear on staging could delete live's cache folder, and staging loaded live's plugin files.
- Live paths are also replaced in `sgo-config.php`, `autoptimize_404_handler.php`, the Swift Performance mu-plugin loader, Breeze and Docket Cache configs, and in the staging database. Before, SiteGround Optimizer on staging wrote pages into live's cache folder, Swift Performance loaded live's plugin, and Hummingbird's daily cleanup on staging deleted live's minified CSS and JS.
- **A sync could purge live's Sucuri firewall cache with WP Rocket.** WP Rocket's Sucuri sync ran during the post-processing with live's API key.
- The sync refuses to start when live's W3 Total Cache `db-cluster-config.php` names live's database: staging would read and write it.
- **Promote backups failed on plugins with symlinks**, and a restore lost them. Symlinks are now stored and restored as links.
- Promote and restore now give the copied files the owner of live's `wp-content/`. Before, live got staging's owner after a promote, and root after a restore.
- Promote and restore flush live's object cache with the sync's scope rules (live's keys only), instead of a full `wp cache flush` that could clear other sites on a shared Redis or Memcached server.
- The export reads live inside one read-only `REPEATABLE READ` transaction, so orders, order items and customers come from one moment on a busy shop.
- Two runs (sync, promote or restore) on the same webroot can no longer run at once: the second one stops with the process ID of the first.
- `q` and Ctrl+C during a sync, promote or restore ask for confirmation. `q` on the settings screen saves, as the screen says.
- Sites whose webroot is a generic folder (`public_html`, `httpdocs`, `htdocs`, …) are named and paired after their parent folder, so two of them no longer share one saved config. Sites already saved under the old name keep it.
- The pair list shows the real staging name (`staging.`, `dev.`, `test.`) instead of always `stage.`.
- LiteSpeed Cache's QUIC.cloud key and site key pair are cleared on staging, so staging is not linked to live's QUIC.cloud account.
- The `--help` text described the sync direction in reverse, and promote as unattended.
- A failed save of the site config in the TUI is shown as a warning, and an unreadable `wp-config.php` in the pre-sync checks is reported instead of skipped silently.
- The TUI closed only one of the two database connections it opens on the credentials screen.
- A staging webroot inside the live webroot (or the reverse) is refused before any change. Before, the check ran after the import and the file sync.
- The sync now stops before any change if staging and live share object cache keys, because staging would read and write live's cache. Set a separate `WP_CACHE_KEY_SALT` in the staging `wp-config.php`.
- A staging site whose database held live's URL after an interrupted sync is repaired by the next sync. Before, the staging host was read from that database, so the search-replace went from the live host to itself.
- A sync refuses to post-process when live and staging resolve to the same host.

## [0.7.0] - 2026-11-01

### Added

- The TUI home screen shows a short "What's new" block once after an update.

### Changed

- **Check your excludes before you update.** Nested folders that have the same name as an excluded folder now sync. The first sync after the update can transfer more files.
- `--delete` no longer protects those nested folders, so a sync removes a nested folder that exists only on staging, such as `wp-content/uploads/cache`.
- The release process now runs checks before and after the version bump, keeps commits instead of squashing them, and tags only after CI passes. CI now also runs on `main` and pull requests, and release notes come from this changelog.
- A glob in a hand-edited `rsync_excludes`, such as `*.log`, now matches only at the top level of `wp-content/`. To match at every depth, add both `*.log` and `**/*.log`.

### Fixed

- Rsync excludes now apply only to the selected `wp-content/` folder. Before, excluding `cache` also skipped every nested folder of that name, such as `wp-content/plugins/foo/cache`.
- Unchecking every folder in the exclude step now means "exclude nothing". Before, unattended runs silently fell back to the default excludes.
- The exclude step no longer crashes when `wp-content` has no subfolders or cannot be read, and it shows the read error.
- Saved excludes that are not `wp-content` subfolders, such as nested paths added by hand, stay in the exclude list instead of being dropped on the next run.
- A TUI sync with no saved excludes now applies the default excludes, like unattended mode.
- The update notice now appears only when a higher version exists. Before, any difference counted, so a newer or development build was told to "update" to an older release.
- `rsync` and `chown` failures in sync and promote now report the command's own error output, not only the exit status.

## [0.6.0] - 2026-06-11

### Added

- Unit tests for guardrail path/DB checks, export table classification, `wp-config.php` parsing, and site-host extraction.

### Changed

- Domain search-replace now uses the real site host from the database (`home`/`siteurl`) instead of the webroot directory name, and rewrites `http://`, `https://`, and protocol-relative (`//host`) URLs.
- Staging import now moves existing tables aside and restores them if the import fails, instead of dropping everything up front (no more half-wiped staging on a failed sync).
- Import raises `max_allowed_packet` for its own session instead of globally, so it no longer changes the setting for every database on a shared MySQL server; if the server won't allow it, a note is printed.
- Database connections are now built with the driver's DSN builder, so DB passwords containing `@ : / ?` no longer break the connection.
- `wp-config.php` parser now reads quoted values with escapes, so DB passwords containing quotes or backslashes are parsed correctly instead of being truncated.
- Promote backups are now finalized and flushed to disk before being recorded, so a truncated archive can't be treated as a valid backup. A failed automatic restore is now reported in the error, and restore no longer removes paths outside live `wp-content`.
- Binary/blob column values are exported as hexadecimal literals (`0x…`) instead of escaped text, preventing corruption of binary data on import.
- Generated/virtual columns are omitted from exported `INSERT`s (the database recomputes them on import), so tables that use them no longer fail to import.
- Unattended progress now updates a single in-place line per step in a terminal (the bar and its current item share one line), while standalone status lines are kept. Redirected output (cron/logs) prints only the final state of each bar.
- Clarified README and website safety wording: the default Live → Staging sync never writes to production; promote writes production files (always after a backup, with auto-restore) but never the production database.
- Documented anonymization scope: it masks standard WooCommerce/WordPress customer fields and is not a guarantee that every plugin's PII or secret is scrubbed.

### Fixed

- Sync no longer aborts on non-WooCommerce sites — order/user/comment filtering is skipped when WooCommerce is absent and all tables (users, comments, etc.) are exported in full.
- Blog comments and product reviews are now synced on WooCommerce sites; previously only order notes were exported. Order notes stay limited to the synced orders.
- Tables left as `custom_rule` but not specially handled are now exported in full instead of being silently dropped.
- Anonymization now masks PII in `wc_orders` (order email, IP, user-agent, customer note) and order notes; previously these were left intact.
- Path guardrails now resolve symlinks, so a stage path symlinked into the live tree is correctly rejected.
- Added an authoritative same-database guard after connecting (compares server identity + schema), catching cases string comparison missed (`localhost` vs `127.0.0.1`, socket vs TCP).

### Security

- `--update` now verifies the downloaded binary against the release `checksums.txt` (SHA256) before replacing the running executable.
- WP-CLI PHAR fallback now downloads a pinned version and verifies its SHA256 against a hardcoded value before use, storing it `0700`. A bad or tampered download is rejected.
- Backup restore now rejects archive entries whose paths would escape the extraction directory (path traversal).

## [0.5.1] - 2026-06-10

### Added

- **Dry-run mode** (`-n` / `--dry-run`): preview a sync without writing anything — prints what would be exported, dropped/recreated, transferred (`rsync --dry-run`), and which WP-CLI commands would run. Supports `-s` / `--site`.

### Fixed

- Anonymization now runs in unattended mode (`-u`); previously only the TUI applied it.
- Post-processing skip messages now report the actual reason instead of always "plugin not installed".

## [0.5.0] - 2026-05-27

### Added

- **Promote mode** (`--promote`): surgically promote specific themes, plugins, and mu-plugins from staging to production with automated backup and atomic restore on failure.
- **Restore mode** (`--restore`): restore any previous backup from the TUI with full item-level recovery.
- Backup engine with tar.gz archives, JSON metadata sidecars, and configurable retention (default: 5 backups per site).
- `p` (promote) and `r` (restore) keyboard shortcuts on the startup screen.
- `--promote` and `--restore` CLI flags for direct mode entry.
- `-s` / `--site` flag to target a specific site in unattended or promote mode. Use `--list` to see configured identifiers.
- `-l` / `--list` flag to list all configured sites with their live → staging mapping.
- `--delete --site <id>` to remove a saved site config and its backups (prompts for confirmation).
- `--reset` to wipe all saved site configs and start fresh (prompts for confirmation).
- `wp-content/` path guardrail for promote mode — aborts if live and stage resolve to the same directory.
- Global `settings.json` for backup retention configuration.
- Legacy config migration: flat `sites/{domain}.json` files are automatically migrated to `sites/{domain}/config.json` directory structure on startup.
- Landing page and promotional assets for wp-stage-sync website.
- Feature logo and interactive image lightbox to documentation website.
- `CHANGELOG.md` following [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) conventions.
- **Help System**: Added `-h` / `--help` CLI flag to print available commands, flags, and brief usage examples. Added an interactive help screen to the TUI (toggle with `h`).
- **Settings Screen**: Added a global settings screen to the TUI (accessible via `s` from the startup screen) to configure backup retention limits and automatic cache flushing. Settings are persisted to `~/.config/wp-stage-sync/settings.json`.
- `CONTRIBUTING.md` with development setup, project structure, and PR guidelines.

### Changed

- Config storage moved from `sites/{domain}.json` to `sites/{domain}/config.json` (directory-based, backward-compatible via auto-migration).

### Fixed

- Install script now works without root/sudo access.

## [0.4.2] - 2026-05-25

### Added

- Global navigation shortcuts: `q` to quit, `Esc` to go back, `h` for help.
- Dual-sided status bar footer showing shortcuts and version info.
- Backspace support for navigating to parent directory in file picker.
- Recursive back-navigation across all TUI steps via `Esc`.

### Changed

- Updated directory navigator legend to reflect new keybindings.

## [0.4.1] - 2026-05-25

### Fixed

- Directory selection ambiguity resolved by checking cursor position on Space key.

## [0.4.0] - 2026-05-25

### Added

- Interactive directory picker for selecting live/stage webroots (replaces manual text input).

### Changed

- Replaced manual path text inputs with a custom directories-only file browser.

### Removed

- Unused `bubbles/filepicker` dependency from `go.mod`.

## [0.3.1] - 2026-05-25

### Added

- WooCommerce auto-detection (checks `wp-content/plugins/woocommerce` folder first, then DB tables).
- Customer data anonymization option in sync parameters.
- Anonymize WooCommerce order addresses (HPOS `wc_order_addresses` table).

### Changed

- WooCommerce-specific sync parameters are now hidden when WooCommerce is not detected.

### Fixed

- Non-existent default exclude directories no longer shown in the TUI exclude list.

## [0.3.0] - 2026-05-25

### Added

- Nginx vhost discovery alongside Apache.
- WP-CLI PHAR auto-download fallback when `wp` is not in PATH.
- Support for `DB_HOST` formats with port and socket.
- HPOS (High-Performance Order Storage) detection.
- RHEL/AlmaLinux Apache config paths (`/etc/httpd`).
- Per-site configuration (multiple saved site configs).
- Rsync exclude selection step in TUI.

### Changed

- Renamed binary from `wp-sync` to `wp-stage-sync`.
- Standardized TUI formatting and updated ASCII banner art.
- ASCII banner now shown on the paths step too.

## [0.2.2] - 2026-05-25

### Added

- `archive-master-db` directory to default rsync exclusions.

## [0.2.1] - 2026-05-24

### Added

- Sync duration displayed on TUI done screen and terminal exit message.

## [0.2.0] - 2026-05-24

### Added

- PgUp/PgDn/Home/End keyboard navigation for table selection list.

## [0.1.9] - 2026-05-24

### Fixed

- Built-in table modes now always take precedence over saved config.
- `export.DefaultTableMode` is the single source of truth for table modes.

## [0.1.8] - 2026-05-24

### Fixed

- Correct default modes now displayed for built-in custom/structure-only tables.

## [0.1.7] - 2026-05-24

### Added

- Filter `woocommerce_order_items` and `woocommerce_order_itemmeta` by order IDs.
- `mainwp_child_changes_logs` and `mainwp_child_changes_logs_meta` as structure-only defaults.
- Filter `yith_ywpar_points_log` by `order_id` or `user_id`.

## [0.1.6] - 2026-05-24

### Added

- Completed steps summary on the done screen.
- Sync completion timestamp printed after exiting TUI.
- Sync completion timestamp in unattended mode output.

### Fixed

- Silenced rsync, chown, and WP-CLI subprocess output.

## [0.1.5] - 2026-05-24

### Added

- Jetpack safe mode automatically enabled on staging if Jetpack is installed.

## [0.1.4] - 2026-05-24

### Fixed

- Skip Elementor URL replace if plugin is not installed.
- Clear cached version state after successful `--update`.

## [0.1.3] - 2026-05-24

### Added

- Update notice in TUI footer when a new version is available.

### Changed

- Replaced `.last-update-check` file with `.internal` JSON state file.

## [0.1.2] - 2026-05-24

### Changed

- Improved release script: squash commits, combine messages, auto-push.

## [0.1.1] - 2026-05-24

### Added

- Auto-migrate legacy `~/.wp-sync.json` config to new `~/.config/wp-sync/config.json` location.

### Changed

- Consolidated all config files under `~/.config/wp-sync/` directory.

## [0.1.0] - 2026-05-24

### Added

- Progress reporting for the full sync process.
- `progress.Logger` interface with `CLILogger` (timestamped stderr) and `NopLogger`.
- TUI mode: animated spinner, step checklist with checkmarks, detail line, progress bar.
- Unattended mode: timestamped step/detail/progress output to stderr.
- Per-table progress reporting during export with row counts.
- Per-group statement progress during import.
- File sync and post-processing substep reporting.

## [0.0.8] - 2026-05-23

### Added

- Prioritize wp-config unix socket for database connections.
- Force IPv4 (`127.0.0.1`) for localhost TCP connections.

## [0.0.7] - 2026-05-23

### Changed

- Disabled CGO for fully static Linux builds.
- Added quick one-liner install command to README.

## [0.0.6] - 2026-05-23

### Fixed

- Self-update: use `go-selfupdate` naming convention for release assets (`{binary}_{GOOS}_{GOARCH}`).

## [0.0.5] - 2026-05-23

### Added

- `--allow-root` flag passed to all WP-CLI commands.

### Fixed

- Connection reset on large `wp_posts` INSERTs: batch by byte size (8 MB cap) instead of 1000-row count.
- Set `GLOBAL max_allowed_packet=256MB` before import to handle large `post_content`.

## [0.0.4] - 2026-05-23

### Added

- Auto-pair live/stage sites via `discovery.PairSites()` (matches `example.com` ↔ `stage.example.com`).
- Selectable site pair list with arrow key navigation.
- "Custom paths…" option falls back to manual text input.

### Changed

- Updated Go version to 1.26 in release workflow.

## [0.0.3] - 2026-05-23

### Changed

- Updated workflow dependencies (`setup-go` v6, `action-gh-release` v3).
- Standardized release binary naming convention.

### Fixed

- Node.js 24 deprecation warnings in CI.

## [0.0.2] - 2026-05-23

### Added

- Makefile with `release-patch`, `release-minor`, `release-major` targets.

### Fixed

- Release script.

## [0.0.1] - 2026-05-23

### Added

- Initial release: WP/WooCommerce staging sync TUI application.
- Go CLI with bubbletea TUI and unattended mode (`-u` flag).
- Apache2 vhost and filesystem auto-discovery for WordPress installs.
- `wp-config.php` parser for DB credentials and table prefix.
- DB connection with root socket fallback to wp-config credentials.
- Export engine: filtered HPOS orders, safe user calculation, batched INSERTs.
- Import with foreign key check handling.
- Rsync file sync.
- WP-CLI post-processing (search-replace, flush).
- Production safety guardrails (path and DB identity validation).
- JSON config persistence (`~/.wp-sync.json`).
- Self-update mechanism via `--update` flag using `go-selfupdate`.
- `--version` / `-v` flag.
- GitHub Actions release workflow (linux/amd64 + arm64, checksums).
