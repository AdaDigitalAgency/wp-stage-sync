# Getting started

wp-stage-sync copies a live WordPress site to its staging site on the same Linux server. It runs as an interactive terminal app or unattended from cron, and it never writes to the live database.

A sync exports the live database, with WooCommerce orders cut down to the number you choose and customer data optionally anonymised, imports it into the staging database, copies `wp-content/` with rsync, and runs WP-CLI on the staging site to replace URLs and paths and to clear caches. Before the staging site loads any WordPress code, the sync switches off the live CDN, Cloudflare and firewall credentials that the import copied; see [cache plugins](cache-plugins.md). Two more modes work in the other direction: promote copies selected themes and plugins from staging to live, after a backup, and restore puts such a backup back. See [promote and restore](promote-and-restore.md).

## Requirements

- A Linux server that hosts both sites, with:
  - MySQL or MariaDB
  - `rsync`
  - WP-CLI (`wp` in `PATH`). Without it, the tool downloads a pinned WP-CLI 2.12.0 PHAR, checks its SHA-256 and runs it with `php`, so `php` must then be in `PATH`.
  - Optionally Apache, Nginx, LiteSpeed Enterprise or OpenLiteSpeed, whose configs are read to find sites; see [servers](servers.md).
- Access to both `wp-config.php` files. Run the tool as a user that can read both sites and `chown` the staging files; in practice that is root. Every WP-CLI command runs with `--allow-root`.
- WooCommerce order filtering works with HPOS and with the legacy order store; see [how it works](how-it-works.md#database-export).
- Go 1.26 or later, only to build from source (`go.mod` sets `go 1.26.3`).

## Install

### Quick install

Download the release binary for the server's architecture (amd64 or arm64):

```
curl -sL "https://github.com/AdaDigitalAgency/wp-stage-sync/releases/latest/download/wp-stage-sync_linux_$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')" -o /tmp/wp-stage-sync && chmod +x /tmp/wp-stage-sync && sudo mv /tmp/wp-stage-sync /usr/local/bin/wp-stage-sync
```

Release binaries are static Linux builds. Later updates come from the tool itself: `wp-stage-sync --update` downloads the latest release and checks it against the release's `checksums.txt` before it replaces the binary.

### From source

```
go install github.com/AdaDigitalAgency/wp-stage-sync/cmd/wp-stage-sync@latest
```

A binary built this way reports its version as `dev`, and it shows no update notice.

## The first sync

Start the app with no arguments:

```
wp-stage-sync
```

1. **Choose the sites.** On a server with no saved sites, the app opens on the list of sites it found, paired as `example.com → stage.example.com`. Pick a pair and press Enter. If your sites are not listed or not paired, choose **Custom paths...** and select the live webroot, then the staging webroot, with Space. When no site is found at all, the app opens on that folder browser directly. See [servers](servers.md#pairing) for how pairs are formed.
2. **Check the credentials.** The app reads both `wp-config.php` files and shows the database name, host and table prefix of each. Press Enter. It connects to the live database and lists its tables.
3. **WooCommerce options** (only when WooCommerce is found): the number of orders, whether to take the newest or oldest, and whether to anonymise customers.
4. **Table modes.** Every live table with its mode. Defaults are safe for most sites; press Enter.
5. **Rsync excludes.** The `wp-content/` folders that are not copied. The defaults are checked; press Enter.
6. **Confirm.** The app shows both paths and the order settings. If the staging site would share live's Redis database or Memcached server, it also offers **Stage object cache: Off**; see [object cache](object-cache.md#shared-database). Press Enter to start.

The progress screen lists each step as it finishes. Before anything on the staging site changes, the sync runs its safety checks: it refuses to start if, for example, both sites share a database or object cache keys. The messages and their fixes are in [troubleshooting](troubleshooting.md).

Every screen is described in [the interactive app](interactive.md).

## Where the settings are saved

When you press Enter on the confirm screen, the site's choices are saved to:

```
~/.config/wp-stage-sync/sites/<site>/config.json
```

`<site>` is the last folder name of the live webroot, for example `example.com` for `/home/example.com`. The next run lists the site on its home screen, and an unattended run uses the same file. Global settings are in `~/.config/wp-stage-sync/settings.json`. Both files are described in [configuration](configuration.md).

## Preview before a real run

A dry run reads both sites and prints what a sync would do, without any change:

```
wp-stage-sync -n
```

It needs a saved site, so run the app once first. See [unattended runs](unattended.md#dry-run).

## Next steps

- [Unattended runs](unattended.md): cron, `--site`, `--list`, dry runs.
- [How a sync works](how-it-works.md): the exact order of steps and what each one touches.
- [Cache plugins](cache-plugins.md) and [object cache](object-cache.md): what the sync changes on the staging site and why.
- [Safety](safety.md): the guardrails and what is written where.
