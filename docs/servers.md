# Servers

The interactive app lists the WordPress sites on the server by reading the web server configs, then the file system, and pairs each live site with its staging site by folder name. This page says where it looks, how pairs are formed, and which server details matter for a sync.

Discovery only fills the site list in the TUI. Unattended runs use the saved paths and do not scan. The code is in [`internal/discovery/`](../internal/discovery/).

## Discovery sources

The sources are read in this order. A root is listed only if it holds `wp-config.php`, and each root is listed once.

### Apache

Every file (not subfolder) in:

| Folder | Used by |
|---|---|
| `/etc/apache2/sites-enabled`, `/etc/apache2/sites-available` | Debian, Ubuntu |
| `/etc/httpd/conf.d`, `/etc/httpd/sites-enabled` | RHEL, AlmaLinux, CentOS |
| `/etc/apache2/conf` | cPanel EasyApache 4: `httpd.conf` holds every vhost |
| `/etc/apache2/conf.d` | Debian extra configs, cPanel includes |

Each `<VirtualHost>` block gives its `DocumentRoot`. `Include` lines are not followed.

### Nginx

Every file in `/etc/nginx/sites-enabled` and `/etc/nginx/conf.d`. Each `server { }` block gives its `root`.

### LiteSpeed Enterprise

`/usr/local/lsws/conf/httpd_config.xml`, LiteSpeed's native XML config. When LiteSpeed Enterprise loads the Apache config instead, the Apache scan above finds the sites.

- Each `virtualHost` gives its `vhRoot` and `configFile`. The `docRoot` is read from that vhost config file (XML or plain text).
- Each `vhTemplate` is read from its `templateFile`. Every `member` gives a vhost, with its own `vhRoot` when set, else the template's, and the template's `docRoot`.
- `$SERVER_ROOT`, `$VH_NAME` and `$VH_ROOT` are expanded. A relative path is read from `/usr/local/lsws`, a relative `docRoot` from the vhost root. A path with another variable is skipped.

Both the `docRoot` and the vhost root are offered, because WordPress is often installed in the vhost root itself.

### OpenLiteSpeed

`/usr/local/lsws/conf/httpd_config.conf`. The same rules for `virtualhost` blocks and `vhTemplate` blocks with `member` entries, in OpenLiteSpeed's plain-text format.

### File system

Last, the tool walks these folders and lists every folder with a `wp-config.php`, down to three levels below the start folder:

```
/home
/var/www
/srv/www
/var/www/vhosts      (Plesk)
/usr/local/lsws      (LiteSpeed default vhost roots)
```

For example, `/home/user/sites/example.com/wp-config.php` is found, and `/home/user/sites/example.com/public/wp-config.php` is not. A site outside these folders is found only through a web server config, or chosen with **Custom paths...**.

## Pairing

Each root is named by its last folder name: `/home/example.com` is `example.com`, `/home/stage.example.com` is `stage.example.com`. Generic folder names (`public_html`, `htdocs`, `httpdocs`, `html`, `www`, `web`, `public`, `wordpress`, `current`) are skipped upward: `/home/user/example.com/public_html` is `example.com`, `/var/www/vhosts/example.com/httpdocs` is `example.com`. A name that starts with `stage.`, `staging.`, `dev.` or `test.` is a staging site. For every other name, the tool looks for a staging site with the same name and one of these prefixes, in that order, and the first match makes the pair:

```
/home/example.com          →  /home/stage.example.com
/var/www/shop.example.org  →  /var/www/staging.shop.example.org
```

The folders do not need the same parent. A live site without a match is listed with "(no staging found)". A staging folder without a live partner is not listed.

### Folder name limit

Pairing and the saved configuration use this name. A site saved by an older version under a generic name such as `public_html` keeps that name, with its configuration and backups. Two webroots that still get the same name, for example `/home/a/example.com` and `/srv/example.com`, hide each other in the list and share one configuration. For such sites, choose **Custom paths...**, and keep only one of them saved.

The folder name is also a fallback host. The sync takes each site's host from its `home` and `siteurl` options; only when those cannot be read does it use the folder name. See [how it works](how-it-works.md#1-guardrails-and-connection).

## Ownership

After rsync, the staging `wp-content/` is set with `chown -R` to the user and group that own the staging webroot folder. If that folder cannot be read, `33:33` is used (`www-data` on Debian and Ubuntu; another user on other systems). Make sure the staging webroot itself is owned by the site's user before the first sync.

rsync runs with `-a`, so files keep live's permissions. Promote does not change ownership; see [promote and restore](promote-and-restore.md).

## rsync

```
rsync -a --delete --exclude=/cache ... <live>/wp-content/ <staging>/wp-content/
```

- Only `wp-content/` is copied.
- Excludes are anchored to `wp-content/`. `cache` skips `wp-content/cache`, not `wp-content/plugins/foo/cache`. Before 0.7.0 an exclude matched at every depth, so the first sync after that update can copy more files.
- `--delete` removes staging files that live does not have, except under excluded paths: they are neither copied nor deleted.
- A failed rsync or chown stops the sync and shows the last ten lines of the command's output.

See [configuration](configuration.md#rsync_excludes) for the exclude list.

## Database connections

Both sites can be on one MySQL or MariaDB server or on different ones. The connection order is in [safety](safety.md#database-connections).

## Nginx Helper cache folder

Nginx Helper's "Purge Cache" deletes everything in its cache folder. If live has Nginx Helper and both sites use the same folder, a purge on staging deletes live's cached pages. The folder is `RT_WP_NGINX_HELPER_CACHE_PATH` in each `wp-config.php`, or `/var/run/nginx-cache` when it is not set. The sync warns before it starts:

```
WARNING: Nginx Helper: stage and live use the same cache folder /var/run/nginx-cache. "Purge Cache" on stage deletes the whole folder, live's pages included. Give stage its own fastcgi_cache_path and RT_WP_NGINX_HELPER_CACHE_PATH
```

Give staging its own folder in the Nginx config:

```
fastcgi_cache_path /var/run/nginx-cache-stage levels=1:2 keys_zone=STAGE:64m inactive=60m;
```

and point the staging site to it:

```
// staging wp-config.php
define('RT_WP_NGINX_HELPER_CACHE_PATH', '/var/run/nginx-cache-stage/');
```

The check compares the configured paths only. It does not read the Nginx config.
