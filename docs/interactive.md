# The interactive app

Run `wp-stage-sync` with no arguments to open the interactive app, a full-screen terminal wizard that sets up a site pair and runs a sync. This page lists every screen in order and the keys each one takes.

The app is built for a terminal of 80 by 24 or larger. At 60 columns by 24 rows or more, the home and site screens show the large banner; smaller, they show a one-line title. The footer always lists the global keys on the left and the version on the right, plus an update notice when a newer release exists.

## Global keys

| Key | What it does |
|---|---|
| `q`, Ctrl+C | Quit. While a sync, promote or restore runs, it asks first: `y` quits, any other key continues. On the settings screen, `q` saves and goes back. |
| `Esc` | Go back one screen. It does nothing on the running and done screens. |
| `h` | Show or hide the help screen. Not available while a sync, promote or restore runs. |
| `s` | Open the settings screen. Only on the home screen and the site pair list. |

Quitting during a run ends the program in the middle of its step: a sync leaves the staging site half synced, and a promote or restore can leave live half copied. Run it again to finish.

## Home screen

Shown when at least one site is saved. It lists the saved pairs as `example.com → stage.example.com`, then **Configure new site...**.

| Key | What it does |
|---|---|
| `↑` `↓` | Move. |
| `Enter` | On a saved site: read both `wp-config.php` files and go to the credentials screen with the saved settings. On **Configure new site...**: go to site selection. |
| `p` | [Promote](promote-and-restore.md#promote) themes and plugins of the selected site from staging to live. |
| `r` | [Restore](promote-and-restore.md#restore) a backup of the selected site. Shown in the footer only when the site has backups. |
| `d` | Delete saved site configurations. |

### What's new

After an update, the home screen shows up to three lines about the new release under the list, once. The version you have seen is saved as `whats_new_seen` in `settings.json`. A fresh install with no saved sites shows nothing. If the window is too short, the banner is left out to make room; if it is still too short, the notes wait for the next run.

### Delete configurations

`d` opens a checklist of the saved sites. Space checks one, `a` checks all and goes straight to the confirm screen, Enter continues. The confirm screen lists the sites; Enter deletes each site's folder under `~/.config/wp-stage-sync/sites/`, including its promote backups. Esc goes back to the checklist.

## Site selection

Shown first on a server with no saved sites, and after **Configure new site...**.

### Site pairs

The sites found on the server, paired by folder name. A live site with no staging partner is listed with "(no staging found)"; Enter on it shows an error that points to **Custom paths...**. The last entry is **Custom paths...**. How sites are found and paired is in [servers](servers.md).

The list shows the name of each side, for example `example.com  →  staging.example.com`. The credentials screen shows the database of each.

If a saved configuration exists under the live site's folder name, Enter loads it, so the order count, table modes and excludes come back.

### Custom paths

A folder browser, used when no pairs were found or you choose **Custom paths...**. It starts in your home folder and lists folders only, without hidden ones.

| Key | What it does |
|---|---|
| `↑` `↓` | Move. |
| `Enter` | Open the folder under the cursor (`..` goes up). |
| `Backspace` | Go up to the parent folder. |
| `Space` | Select the folder under the cursor, or the current folder when the cursor is on `..`. |
| `Esc` | Back to the site pairs, or from the staging step back to the live step. |

The first Space selects the **live webroot**, the second the **staging webroot**. Each webroot is the folder that holds `wp-config.php`. If either file cannot be read or lacks `DB_NAME`, `DB_USER`, `DB_PASSWORD` or `DB_HOST`, the error is shown and you start again at the live webroot.

## Credentials

Shows what was read from the two `wp-config.php` files:

```
Live DB: example_db @ localhost (prefix: wp_)
Stage DB: example_stage_db @ localhost (prefix: wp_)
```

Enter connects to the live database and lists its tables. A connection error is shown on this screen. The tool tries the socket from `DB_HOST`, then the common MySQL sockets with the `wp-config.php` user, then passwordless `root` on those sockets, then TCP; see [safety](safety.md#database-connections).

WooCommerce counts as installed when `wp-content/plugins/woocommerce` exists on live, or the live database has a `wc_orders` or `woocommerce_order_items` table. With WooCommerce the next screen is sync parameters; without it, table modes.

## Sync parameters

Only with WooCommerce.

| Field | Values | Default |
|---|---|---|
| Order Count | A whole number above 0. | 100 |
| Order Preference | **Last N** (newest orders) or **First N** (oldest). | Last N |
| Anonymize Customers | Yes or No. | No |

Type the count with the digit keys and Backspace. `Tab` or `Enter` moves to the next field; `↑` and `↓` toggle the preference and the anonymise choice. Enter on the last field continues. What is kept and masked is described in [how it works](how-it-works.md#database-export).

Order filtering works with HPOS and with the legacy order store. If the WooCommerce plugin folder exists but its tables do not (WooCommerce was never activated), these settings are saved and the export copies every table.

## Table modes

Every live table with its mode.

| Key | What it does |
|---|---|
| `↑` `↓` | Move. |
| `PgUp` `PgDn`, `Home` `End` | Jump. |
| `Space` | Cycle the mode: Structure & Data → Structure Only → Ignore → Custom Rule. |
| `Enter` | Continue. |

| Mode | Effect |
|---|---|
| Structure & Data | Table and all rows. |
| Structure Only | Table, no rows. |
| Ignore | Not exported. The import drops every staging table first, so an ignored table is missing on staging afterwards. |
| Custom Rule | For a table without a built-in rule, the same as Structure & Data. |

Tables with a built-in rule cannot be changed; Space does nothing on them. They are listed in [configuration](configuration.md#table_modes).

## Rsync excludes

The folders of live `wp-content/`. A checked folder is not copied and keeps its staging contents.

| Key | What it does |
|---|---|
| `↑` `↓`, `PgUp` `PgDn`, `Home` `End` | Move. |
| `Space` | Check or uncheck. |
| `Enter` | Continue. |

With no saved list, the [default excludes](configuration.md#default-excludes) that exist on live are checked. A nested default such as `boost-cache/cache` is listed when its top folder exists, and a file pattern when a file matches. If you press Enter without a change, nothing is saved, so the defaults keep applying, also to folders that appear later. After one change, the checked list is saved; unchecking every folder saves an empty list, which means "exclude nothing".

A saved exclude that is not a `wp-content` folder, such as a nested path added by hand, stays in the list. If `wp-content` cannot be read, the error is shown, and Enter keeps the saved excludes.

## Confirm

```
Ready to sync?

  Live:  /home/example.com
  Stage: /home/stage.example.com
  Orders: 100 (last, anonymize: no)
```

Without WooCommerce tables, the third line reads `WooCommerce: not installed (full WordPress sync)`.

### Stage object cache

The line **▸ Stage object cache: On (shared with live)** appears when the staging site would share live's Redis database or Memcached server after the sync, and while Off is saved for the site. The text under it names the shared backend. `↑` or `↓` toggles it to **Off**. Off removes the object cache drop-in from the staging site and keeps it off; [object cache](object-cache.md#shared-database) explains what it changes and why a separate database is the better fix.

Enter saves the site's configuration, including this choice as `stage_object_cache_off`, and starts the sync. Esc goes back to the excludes.

## Progress

The steps appear as they run, with a spinner, the current detail line and a progress bar:

1. Validating paths and databases
2. Connecting to databases
3. Checking object cache isolation
4. Exporting from live database
5. Importing to staging database
6. Anonymizing customer data (when enabled)
7. Syncing files
8. Post-processing

Messages appear on the detail line and are replaced by the next message. Warnings are also kept: the done screen lists every warning of the run. Unattended runs (`wp-stage-sync -u -s <site>`) print every line with a timestamp. The full order of steps is in [how it works](how-it-works.md).

## Done

Lists the finished steps and either `✓ Sync complete! (took 2m5s)` or `✗ Sync failed: <error>`. Enter or `q` exits. After a successful sync the terminal also prints `Sync completed at <time> and it took <duration>`.

## Settings

Press `s` on the home screen or the site pair list.

| Setting | Keys | Default |
|---|---|---|
| Backup retention (max per site) | `←` `→`; the lowest value is 1. | 5 |
| Auto cache flush after sync/promote | `Space` or `Enter` toggles. | ON |

Esc or `s` saves to `~/.config/wp-stage-sync/settings.json` and goes back. The screen says "Press Esc or q to save and exit", but `q` quits before the settings are saved; use Esc. What each setting does is in [configuration](configuration.md#settingsjson).

## Help

`h` shows the keys of the global, home screen and list contexts. `h` or Esc closes it.
