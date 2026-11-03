# Unattended runs

The `-u` flag runs a sync without the interactive app, from a site configuration saved earlier, so it fits cron jobs and scripts. The `-n` flag runs the same planning steps and prints what a sync would do, without any change.

Both read the configuration that the [interactive app](interactive.md) saves. Set up each site there once.

## Flags

| Flag | What it does |
|---|---|
| `-u`, `--unattended` | Sync a saved site without the TUI. |
| `-n`, `--dry-run` | Plan a sync and print it. Writes nothing. |
| `-s`, `--site <id>` | The saved site to use. Works with `-u`, `-n` and `--promote`. |
| `-l`, `--list` | List the saved sites. |
| `--promote` | Open the TUI in promote mode; see [promote and restore](promote-and-restore.md). |
| `--restore` | Open the TUI in restore mode. |
| `--delete` | Delete a saved site and its backups. Needs `--site`. |
| `--reset` | Delete every saved site and all backups. |
| `--update` | Replace the binary with the latest release. |
| `-v`, `--version` | Print the version. |
| `-h`, `--help` | Print the flags and examples. |

Flags that do not combine are refused with exit status 1:

- `--promote` or `--restore` with `-u`
- `--restore` with `--site`
- `-n` with `--promote` or `--restore`
- `--site` without `-u`, `-n` or `--promote`
- `--delete` without `--site`

## Site identifiers

```
wp-stage-sync --list
```

```
example.com  →  stage.example.com
shop.example.org  →  staging.shop.example.org
```

The left column is the identifier: the name of the folder under `~/.config/wp-stage-sync/sites/`, which is the last folder name of the live webroot. The right column is the last folder name of the staging webroot.

Without `--site`, a run uses the only saved site. With more than one saved site, it stops and lists the `--site` values:

```
Multiple sites configured. Use --site to specify which one:
  --site example.com
  --site shop.example.org
Error: --site flag required when multiple sites are configured
```

## Sync

```
wp-stage-sync -u -s example.com
```

The run follows the same steps as the TUI, described in [how it works](how-it-works.md), with every setting from the saved `config.json`: order count and preference, anonymisation, table modes, excludes and `stage_object_cache_off`. There are no prompts. Each step and message goes to stderr with a time:

```
[03:00:01]   Site: example.com
[03:00:01]   Stage: /home/stage.example.com
[03:00:01] ► Parsing wp-config files
[03:00:01] ✓ Config parsed
[03:00:01] ► Validating paths and databases
...
[03:04:12] ✓ Sync completed at 2026-11-03 03:04:12 and it took 4m11s
```

In a terminal, progress bars update in place. When stderr is a file or pipe, as under cron, each bar prints only its final state, so the log stays short. Warnings start with `WARNING:`; grep for them.

If a newer release exists, the run first prints `Update available: vX → vY. Run 'wp-stage-sync --update' to upgrade.` The tool asks GitHub at most every eight hours and keeps the answer in `~/.config/wp-stage-sync/.internal`.

### Exit status

| Status | Meaning |
|---|---|
| 0 | The sync finished. Cache purges that failed were printed as warnings. |
| 1 | The run stopped. The last line is `Error: <reason>`. |
| 2 | An unknown flag. |

A run that stops in a preflight check has changed nothing on the staging site, except the object cache drop-in in the cases described in [how it works](how-it-works.md#2-preflight-checks). A run that stops later leaves the staging site partly synced; the next sync repairs it. See [troubleshooting](troubleshooting.md).

### Cron

Run the sync as the same user that set it up, because the configuration is read from that user's home folder (root in most setups). Send the output to a log file:

```
# /etc/cron.d/wp-stage-sync
30 3 * * * root /usr/local/bin/wp-stage-sync -u -s example.com >> /var/log/wp-stage-sync-example.com.log 2>&1
```

Give each site its own line with its own `--site`. Do not run two syncs of the same site at the same time: the tool takes no lock.

## Dry run

```
wp-stage-sync -n -s example.com
```

The dry run parses both `wp-config.php` files, runs the path and database guardrails, connects to both databases and reads them, and runs `rsync --dry-run`. It changes no database, no file and runs no WP-CLI command. If WP-CLI is not installed it is not downloaded. The plan has these sections:

- **Database export**: whether WooCommerce was found, the target order count, the number of users kept, then every table grouped as filtered (with its rule and row count), full data (with row count), schema only and ignored, and the total rows.
- **Database import**: how many staging tables would be dropped and how many recreated.
- **Anonymization**: enabled or disabled, from the site setting.
- **File sync**: source, destination, the excludes, the rsync command and the `rsync --dry-run --stats` summary.
- **Post-processing (WP-CLI)**: the object cache drop-in and how it would be handled (`[off]`, or `[check]` for the isolation probe, with a warning when staging would share live's backend), then each command as `[run]` or `[skip] (reason)`. For a folder clear it prints `clear <folders>`; for a flush through `wp eval` it prints the kind of flush.
- **Live integrations switched off on stage**: the [list of settings groups](cache-plugins.md) the import clears, then `[check]`: whether every live value can be changed. `[check] FAILED: …` means a real sync would stop before any change.

The dry run does not run every preflight check. It does not run the object cache probe, the W3 Total Cache `db-cluster-config.php` check, the Object Cache Pro database check, or the warnings about Cloudflare constants and the Nginx Helper cache folder. A real run can still stop where the dry run passed.

The staging host in the plan comes from the staging database as it is now. If an earlier sync stopped before its search-replace, that host can be live's; the real run detects this and uses the staging folder name instead.

## Config management

```
wp-stage-sync --delete --site example.com
wp-stage-sync --reset
```

Both ask for confirmation (`[y/N]`). `--delete` removes `~/.config/wp-stage-sync/sites/example.com/`, which holds the configuration and the promote backups of that site. `--reset` removes every site folder. Neither touches `settings.json` or a WordPress site.

## Update and version

```
wp-stage-sync --update
wp-stage-sync --version
```

`--update` downloads the latest GitHub release for the running OS and architecture, verifies it against the release `checksums.txt`, and replaces the running binary. It prints `Already up to date` when there is nothing newer. The user that runs it needs write access to the binary's folder.
