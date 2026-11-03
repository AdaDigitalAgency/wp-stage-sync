package discovery

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var scanDirs = []string{
	"/home",
	"/var/www",
	"/srv/www",
	"/var/www/vhosts", // Plesk
	"/usr/local/lsws", // LiteSpeed default vhost roots
}
var apacheConfDirs = []string{
	"/etc/apache2/sites-enabled",
	"/etc/apache2/sites-available",
	"/etc/httpd/conf.d",        // RHEL/AlmaLinux/CentOS
	"/etc/httpd/sites-enabled", // custom RHEL setups
	"/etc/apache2/conf",        // cPanel EasyApache 4 (httpd.conf holds every vhost)
	"/etc/apache2/conf.d",      // Debian/Ubuntu extra configs, cPanel includes
}
var nginxConfDirs = []string{
	"/etc/nginx/sites-enabled",
	"/etc/nginx/conf.d",
}

var documentRootRe = regexp.MustCompile(`(?i)^\s*DocumentRoot\s+["']?([^"'\s]+)["']?`)
var serverNameRe = regexp.MustCompile(`(?i)^\s*ServerName\s+(\S+)`)
var nginxRootRe = regexp.MustCompile(`(?i)^\s*root\s+([^;]+);`)
var nginxServerNameRe = regexp.MustCompile(`(?i)^\s*server_name\s+([^;]+);`)

// VHost represents a discovered Apache virtual host.
type VHost struct {
	ServerName   string
	DocumentRoot string
}

// Scan looks for WordPress installations via web server configs first,
// then falls back to filesystem scanning for wp-config.php files.
func Scan() []string {
	seen := make(map[string]bool)
	var roots []string

	addRoot := func(root string) {
		if seen[root] {
			return
		}
		if _, err := os.Stat(filepath.Join(root, "wp-config.php")); err != nil {
			return
		}
		seen[root] = true
		roots = append(roots, root)
	}

	// 1. Parse Apache vhost configs
	for _, vhost := range ScanApacheVHosts() {
		addRoot(vhost.DocumentRoot)
	}

	// 2. Parse Nginx server blocks
	for _, vhost := range ScanNginxServers() {
		addRoot(vhost.DocumentRoot)
	}

	// 3. Parse LiteSpeed vhosts (Enterprise native XML, OpenLiteSpeed)
	for _, vhost := range ScanLiteSpeedVHosts() {
		addRoot(vhost.DocumentRoot)
	}

	// 4. Filesystem scan as fallback
	for _, dir := range scanDirs {
		filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return filepath.SkipDir
			}
			if d.IsDir() {
				rel, _ := filepath.Rel(dir, path)
				if strings.Count(rel, string(filepath.Separator)) > 2 {
					return filepath.SkipDir
				}
				return nil
			}
			if d.Name() == "wp-config.php" {
				root := filepath.Dir(path)
				if !seen[root] {
					seen[root] = true
					roots = append(roots, root)
				}
			}
			return nil
		})
	}
	return roots
}

// ScanApacheVHosts parses Apache2 site configs and returns all vhosts
// with a DocumentRoot directive.
func ScanApacheVHosts() []VHost {
	var vhosts []VHost
	for _, dir := range apacheConfDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			parsed := parseApacheConf(path)
			vhosts = append(vhosts, parsed...)
		}
	}
	return vhosts
}

// parseApacheConf extracts VHost entries from a single Apache config file.
// Handles multiple <VirtualHost> blocks per file.
func parseApacheConf(path string) []VHost {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var vhosts []VHost
	var current *VHost

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()

		if strings.Contains(strings.ToLower(line), "<virtualhost") {
			current = &VHost{}
			continue
		}
		if strings.Contains(strings.ToLower(line), "</virtualhost") {
			if current != nil && current.DocumentRoot != "" {
				vhosts = append(vhosts, *current)
			}
			current = nil
			continue
		}
		if current == nil {
			continue
		}

		if m := documentRootRe.FindStringSubmatch(line); len(m) > 1 {
			current.DocumentRoot = m[1]
		}
		if m := serverNameRe.FindStringSubmatch(line); len(m) > 1 {
			current.ServerName = m[1]
		}
	}
	return vhosts
}

// ScanNginxServers parses Nginx site configs and returns all server blocks
// with a root directive.
func ScanNginxServers() []VHost {
	var vhosts []VHost
	for _, dir := range nginxConfDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			parsed := parseNginxConf(path)
			vhosts = append(vhosts, parsed...)
		}
	}
	return vhosts
}

// parseNginxConf extracts VHost entries from a single Nginx config file.
func parseNginxConf(path string) []VHost {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var vhosts []VHost
	var current *VHost
	braceDepth := 0

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if strings.HasPrefix(line, "server") && strings.Contains(line, "{") {
			current = &VHost{}
			braceDepth = 1
			continue
		}
		if current == nil {
			continue
		}

		braceDepth += strings.Count(line, "{") - strings.Count(line, "}")
		if braceDepth <= 0 {
			if current.DocumentRoot != "" {
				vhosts = append(vhosts, *current)
			}
			current = nil
			continue
		}

		if m := nginxRootRe.FindStringSubmatch(line); len(m) > 1 {
			current.DocumentRoot = strings.TrimSpace(m[1])
		}
		if m := nginxServerNameRe.FindStringSubmatch(line); len(m) > 1 {
			// Take the first server_name (ignore _ and extras)
			names := strings.Fields(m[1])
			for _, n := range names {
				if n != "_" {
					current.ServerName = n
					break
				}
			}
		}
	}
	return vhosts
}

// genericWebroots are folder names that say nothing about the site. A
// webroot with one of these names is named after its parent folder.
var genericWebroots = map[string]bool{
	"public_html": true, "htdocs": true, "httpdocs": true, "html": true, "www": true,
	"web": true, "public": true, "wordpress": true, "current": true,
}

// ExtractDomain names a site after its webroot path:
// /home/example.com/ → example.com, /home/stage.example.com/ → stage.example.com,
// /var/www/vhosts/example.com/httpdocs → example.com (generic folder names
// are skipped upward).
func ExtractDomain(webroot string) string {
	p := filepath.Clean(strings.TrimRight(webroot, "/"))
	name := filepath.Base(p)
	for genericWebroots[strings.ToLower(name)] {
		parent := filepath.Dir(p)
		if parent == p || parent == "/" || parent == "." {
			break
		}
		p = parent
		name = filepath.Base(p)
	}
	return name
}

// SitePair is an auto-detected live+stage pair.
type SitePair struct {
	LivePath  string
	StagePath string
	Domain    string // base domain without "stage." prefix
}

// stagePrefixes are the naming conventions checked when pairing sites.
var stagePrefixes = []string{"stage.", "staging.", "dev.", "test."}

// PairSites groups discovered paths into live/stage pairs.
// Convention: "stage.example.com" (or staging./dev./test.) is the staging counterpart.
func PairSites(paths []string) []SitePair {
	byDomain := make(map[string]string) // domain → path
	for _, p := range paths {
		d := ExtractDomain(p)
		byDomain[d] = p
	}

	seen := make(map[string]bool)
	var pairs []SitePair

	for domain, path := range byDomain {
		// Skip domains that are themselves staging
		isStage := false
		for _, prefix := range stagePrefixes {
			if strings.HasPrefix(domain, prefix) {
				isStage = true
				break
			}
		}
		if isStage {
			continue // handled from the live side
		}

		// Try each prefix to find a staging counterpart
		for _, prefix := range stagePrefixes {
			stageDomain := prefix + domain
			if stagePath, ok := byDomain[stageDomain]; ok {
				pairs = append(pairs, SitePair{
					LivePath:  path,
					StagePath: stagePath,
					Domain:    domain,
				})
				seen[domain] = true
				seen[stageDomain] = true
				break // first match wins
			}
		}
	}

	// Include unpaired paths as live-only entries
	for domain, path := range byDomain {
		if !seen[domain] {
			pairs = append(pairs, SitePair{
				LivePath: path,
				Domain:   domain,
			})
		}
	}

	return pairs
}
