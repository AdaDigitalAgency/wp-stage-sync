package sync

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/AdaDigitalAgency/wp-stage-sync/internal/wpconfig"
)

// stageCredentialConstants are wp-config.php constants that give a plugin
// remote credentials. The sync never copies wp-config.php, so a constant on
// stage was set there by hand; the database cleanup cannot remove it.
var stageCredentialConstants = map[string]string{
	"CLOUDFLARE_API_KEY":    "Cloudflare plugin",
	"CLOUDFLARE_EMAIL":      "Cloudflare plugin",
	"SWCFPC_CF_API_KEY":     "Super Page Cache",
	"SWCFPC_CF_API_TOKEN":   "Super Page Cache",
	"SWCFPC_CF_API_ZONE_ID": "Super Page Cache",
	"CDN_SITE_TOKEN":        "Breeze (Cloudways Cloudflare)",
}

// nginxHelperDefaultPath is Nginx Helper's default fastcgi cache folder.
const nginxHelperDefaultPath = "/var/run/nginx-cache"

// PreflightIntegrations runs before anything on stage changes. It refuses a
// sync that would let stage write to live's database, and warns about live
// services that the database cleanup cannot switch off.
func PreflightIntegrations(livePath, stagePath string, log interface{ Detail(string) }) error {
	// FixLivePaths and the path search-replace cannot tell stage paths from
	// live paths when one site is inside the other. Refuse before any change.
	live, stage := strings.TrimRight(livePath, "/")+"/", strings.TrimRight(stagePath, "/")+"/"
	if strings.HasPrefix(stage, live) || strings.HasPrefix(live, stage) {
		return fmt.Errorf("one site is inside the other (live %s, stage %s): live paths in the copied files could not be "+
			"told apart from stage paths. Move stage out of live's folder", livePath, stagePath)
	}
	liveCfg, err := wpconfig.Parse(filepath.Join(livePath, "wp-config.php"))
	if err != nil {
		log.Detail("WARNING: live wp-config.php could not be read, so the credential and Nginx Helper checks were skipped: " + err.Error())
		return nil
	}
	stageCfg, err := wpconfig.Parse(filepath.Join(stagePath, "wp-config.php"))
	if err != nil {
		log.Detail("WARNING: stage wp-config.php could not be read, so the credential and Nginx Helper checks were skipped: " + err.Error())
		return nil
	}
	if err := checkDBClusterConfig(livePath, liveCfg.DBName); err != nil {
		return err
	}
	for _, w := range integrationWarnings(livePath, liveCfg, stageCfg) {
		log.Detail("WARNING: " + w)
	}
	return nil
}

// checkDBClusterConfig refuses W3 Total Cache's db-cluster-config.php when it
// names live's database: the file sync copies it, and stage's db.php drop-in
// would then send queries to live.
func checkDBClusterConfig(livePath, liveDB string) error {
	data, err := os.ReadFile(filepath.Join(livePath, "wp-content", "db-cluster-config.php"))
	if err != nil || liveDB == "" {
		return nil
	}
	text := string(data)
	if strings.Contains(text, "'"+liveDB+"'") || strings.Contains(text, `"`+liveDB+`"`) {
		return fmt.Errorf("live's wp-content/db-cluster-config.php (W3 Total Cache database cluster) names live's database %q. "+
			"The file sync would copy it, and stage would read and write live's database. "+
			"Make the file use the DB_NAME and DB_HOST constants instead of literal values", liveDB)
	}
	return nil
}

func integrationWarnings(livePath string, liveCfg, stageCfg *wpconfig.WPConfig) []string {
	var warnings []string
	names := make([]string, 0, len(stageCredentialConstants))
	for name := range stageCredentialConstants {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		plugin := stageCredentialConstants[name]
		if strings.TrimSpace(stageCfg.Defines[name]) != "" {
			warnings = append(warnings, fmt.Sprintf("stage wp-config.php defines %s (%s). If it holds live's credentials, "+
				"a purge on stage reaches live's zone. Remove it from stage's wp-config.php", name, plugin))
		}
	}
	if dirExists(filepath.Join(livePath, "wp-content", "plugins", "nginx-helper")) {
		lp, sp := liveCfg.Defines["RT_WP_NGINX_HELPER_CACHE_PATH"], stageCfg.Defines["RT_WP_NGINX_HELPER_CACHE_PATH"]
		if lp == "" {
			lp = nginxHelperDefaultPath
		}
		if sp == "" {
			sp = nginxHelperDefaultPath
		}
		if filepath.Clean(lp) == filepath.Clean(sp) {
			warnings = append(warnings, fmt.Sprintf("Nginx Helper: stage and live use the same cache folder %s. "+
				"\"Purge Cache\" on stage deletes the whole folder, live's pages included. "+
				"Give stage its own fastcgi_cache_path and RT_WP_NGINX_HELPER_CACHE_PATH", lp))
		}
	}
	return warnings
}
