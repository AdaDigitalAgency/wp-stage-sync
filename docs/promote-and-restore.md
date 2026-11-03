# Promote and restore

Promote copies selected themes, plugins and must-use plugins from the staging site to the live site, after it backs up the live copies. Restore puts such a backup back on live. Both write live files on purpose, and neither touches a database.

Both run in the TUI only, for a site that is already saved. The code is in [`internal/promote/promote.go`](../internal/promote/promote.go).

## Promote

Start it from the home screen with `p` on a saved site, or directly:

```
wp-stage-sync --promote
wp-stage-sync --promote --site example.com
```

Without `--site`, the only saved site is used; with more than one, the command lists the `--site` values. `--promote` cannot be combined with `-u` or `-n`: the selection always happens in the TUI.

### Select

The screen lists the staging site's assets in three groups:

- **Themes**: folders in `wp-content/themes/`.
- **Plugins**: folders in `wp-content/plugins/`.
- **MU-Plugins**: every file and folder in `wp-content/mu-plugins/`.

Hidden entries are not listed. Space checks an item, Enter continues; at least one item must be checked. Esc goes back to the home screen.

The confirm screen shows each item with its staging and live path. Enter starts.

### What a promote does

1. **Validate.** Refuse when the live and staging webroots, or their `wp-content` folders, resolve to the same folder. Refuse when a selected item no longer exists on staging.
2. **Back up.** Write the live copy of each selected item to `~/.config/wp-stage-sync/sites/<site>/backups/<timestamp>.tar.gz`, with a `<timestamp>.json` that lists the items and their live paths. An item that does not exist on live is listed in the JSON but not archived, so a restore knows to remove it. Symlinks are stored as links, never followed. The archive is closed and synced to disk before it counts as a backup; if the backup fails, nothing on live has changed.
3. **Retention.** Delete the oldest backups of the site beyond `backup_retention` (default 5).
4. **Copy.** For each item, `rsync -a --delete <staging item>/ <live item>/` for a folder, or `rsync -a <file> <live file>` for a single must-use plugin file. Then `chown -R` gives the copied files the owner and group of live's `wp-content/`, because `rsync -a` would keep staging's owner.
5. **Restore on failure.** If an item cannot be copied, the whole backup is restored at once, and the error says `— live restored from backup`. If the restore also fails, the error says `live may be inconsistent`.
6. **Flush.** With `auto_cache_flush` on, flush live's object cache with the same scope rules as the sync's staging flush: only live's keys, and a skip with a warning when the drop-in's flush would clear a whole shared server. See [object cache](object-cache.md#flush-per-drop-in). Cache and CDN plugins are skipped in this command. A failure is a warning.

Things a promote does not do:

- It does not change ownership. rsync `-a` keeps the staging owner and permissions when it runs as root. If the two sites run as different users, check the owner of the promoted folders on live.
- It does not activate, deactivate or update a plugin in the database, and does not run database migrations a new version may need.
- It does not copy `uploads`, other `wp-content` folders, or core files.
- The flush in step 6 loads live's plugins and clears live's whole object cache. If staging shares that Redis database or Memcached server, staging's cache is cleared too.

## Restore

Start it from the home screen with `r` (shown only when the site has backups), or directly:

```
wp-stage-sync --restore
```

`--restore` takes no `--site`. It uses the only saved site; with several saved sites, start the TUI and press `r` on the site.

The list shows each backup with its time and items, newest first, for example `2026-11-03 14-05-09 — plugin:my-plugin, theme:my-theme`. Enter shows the items with their live paths; Enter again restores.

A restore:

1. Extracts the archive to a temporary folder. An archive member whose path would leave that folder is refused.
2. For each item in the backup: if it was archived, `rsync -a --delete` it back to its live path and give it the owner of live's `wp-content/`. If it did not exist on live before the promote, delete it from live, but only when its path is inside live `wp-content/`.
3. With `auto_cache_flush` on, flushes live's object cache, scoped as above.

A restore does not make a new backup first. If you need the current live state, copy it by hand before you restore.

Backups hold regular files and folders only. A live item that contains a symbolic link usually cannot be archived: the backup step fails and the promote stops before anything on live changes.

## Backups

```
~/.config/wp-stage-sync/sites/example.com/backups/
├── 2026-11-03_14-05-09.tar.gz
└── 2026-11-03_14-05-09.json
```

The archive holds `theme/<name>/…`, `plugin/<name>/…` and `mu-plugin/<name>` entries. A backup without its JSON file, or a JSON file without its archive, is not listed. `wp-stage-sync --delete --site example.com` and `--reset` delete the backups with the configuration.
