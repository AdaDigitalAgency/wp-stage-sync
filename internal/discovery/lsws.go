package discovery

import (
	"bufio"
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
)

// lswsRoot is LiteSpeed's $SERVER_ROOT. Enterprise keeps its own vhosts in
// conf/httpd_config.xml (unless it loads the Apache config, which the Apache
// scan reads); OpenLiteSpeed uses conf/httpd_config.conf.
var lswsRoot = "/usr/local/lsws"

// lswsVHost is a LiteSpeed virtual host before its variables are expanded.
type lswsVHost struct {
	name, domain, vhRoot, configFile, docRoot string
}

// ScanLiteSpeedVHosts returns the vhosts of LiteSpeed Enterprise (native XML
// config) and OpenLiteSpeed. DocumentRoot is the expanded docRoot, or the
// vhost root when the docRoot cannot be read.
func ScanLiteSpeedVHosts() []VHost {
	var raw []lswsVHost
	raw = append(raw, readLSWSXML(filepath.Join(lswsRoot, "conf", "httpd_config.xml"))...)
	raw = append(raw, readOLSConf(filepath.Join(lswsRoot, "conf", "httpd_config.conf"))...)

	var out []VHost
	for _, v := range raw {
		vhRoot := lswsPath(lswsExpand(v.vhRoot, v.name, ""), "")
		docRoot := v.docRoot
		if docRoot == "" && v.configFile != "" {
			cfg := lswsPath(lswsExpand(v.configFile, v.name, vhRoot), "")
			docRoot = readLSWSDocRoot(cfg)
		}
		if docRoot != "" {
			out = append(out, VHost{ServerName: v.domain, DocumentRoot: lswsPath(lswsExpand(docRoot, v.name, vhRoot), vhRoot)})
		}
		// A WordPress install in the vhost root itself is common too; Scan
		// keeps only roots that hold wp-config.php.
		if vhRoot != "" {
			out = append(out, VHost{ServerName: v.domain, DocumentRoot: vhRoot})
		}
	}
	return out
}

// lswsExpand replaces LiteSpeed's path variables.
func lswsExpand(s, vhName, vhRoot string) string {
	s = strings.ReplaceAll(s, "$SERVER_ROOT", lswsRoot)
	s = strings.ReplaceAll(s, "$VH_NAME", vhName)
	if vhRoot != "" {
		s = strings.ReplaceAll(s, "$VH_ROOT", strings.TrimRight(vhRoot, "/"))
	}
	return s
}

// lswsPath makes a path absolute: LiteSpeed reads a relative path from
// $SERVER_ROOT, and a docRoot from the vhost root when one is given.
func lswsPath(p, base string) string {
	p = strings.TrimSpace(p)
	if p == "" || strings.Contains(p, "$") {
		return ""
	}
	if !filepath.IsAbs(p) {
		if base == "" {
			base = lswsRoot
		}
		p = filepath.Join(base, p)
	}
	return filepath.Clean(p)
}

// --- LiteSpeed Enterprise (XML) ---

type lswsServerXML struct {
	VirtualHosts []struct {
		Name       string `xml:"name"`
		VhRoot     string `xml:"vhRoot"`
		ConfigFile string `xml:"configFile"`
	} `xml:"virtualHostList>virtualHost"`
	Templates []struct {
		TemplateFile string `xml:"templateFile"`
		Members      []struct {
			VhName   string `xml:"vhName"`
			VhDomain string `xml:"vhDomain"`
			VhRoot   string `xml:"vhRoot"`
		} `xml:"member"`
	} `xml:"vhTemplateList>vhTemplate"`
	Listeners []struct {
		Maps []struct {
			Vhost  string `xml:"vhost"`
			Domain string `xml:"domain"`
		} `xml:"vhostMapList>vhostMap"`
	} `xml:"listenerList>listener"`
}

type lswsTemplateXML struct {
	VhRoot     string `xml:"vhRoot"`
	ConfigFile string `xml:"configFile"`
	DocRoot    string `xml:"virtualHostConfig>docRoot"`
}

func readLSWSXML(path string) []lswsVHost {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cfg lswsServerXML
	if xml.Unmarshal(data, &cfg) != nil {
		return nil
	}
	domains := map[string]string{}
	for _, l := range cfg.Listeners {
		for _, m := range l.Maps {
			if _, ok := domains[m.Vhost]; !ok {
				domains[m.Vhost] = firstDomain(m.Domain)
			}
		}
	}
	var out []lswsVHost
	for _, v := range cfg.VirtualHosts {
		out = append(out, lswsVHost{name: v.Name, domain: domains[v.Name], vhRoot: v.VhRoot, configFile: v.ConfigFile})
	}
	for _, t := range cfg.Templates {
		tdata, err := os.ReadFile(lswsPath(lswsExpand(t.TemplateFile, "", ""), ""))
		if err != nil {
			continue
		}
		var tpl lswsTemplateXML
		if xml.Unmarshal(tdata, &tpl) != nil {
			continue
		}
		for _, m := range t.Members {
			root := tpl.VhRoot
			if m.VhRoot != "" {
				root = m.VhRoot
			}
			out = append(out, lswsVHost{name: m.VhName, domain: firstDomain(m.VhDomain), vhRoot: root, configFile: tpl.ConfigFile, docRoot: tpl.DocRoot})
		}
	}
	return out
}

// readLSWSDocRoot reads docRoot from a vhost config file, XML or OLS text.
func readLSWSDocRoot(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if strings.HasSuffix(path, ".xml") {
		var v struct {
			DocRoot string `xml:"docRoot"`
		}
		if xml.Unmarshal(data, &v) == nil {
			return v.DocRoot
		}
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if f := strings.Fields(line); len(f) >= 2 && f[0] == "docRoot" {
			return strings.Join(f[1:], " ")
		}
	}
	return ""
}

func firstDomain(list string) string {
	for _, d := range strings.FieldsFunc(list, func(r rune) bool { return r == ',' || r == ' ' }) {
		if d != "*" && d != "" {
			return d
		}
	}
	return ""
}

// --- OpenLiteSpeed (plain text) ---

// olsBlock is one "name arg { ... }" block with its "key value" lines.
type olsBlock struct {
	kind, arg string
	values    map[string]string
	children  []*olsBlock
}

// parseOLS reads the nested block format of httpd_config.conf.
func parseOLS(path string) *olsBlock {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	root := &olsBlock{values: map[string]string{}}
	stack := []*olsBlock{root}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		cur := stack[len(stack)-1]
		switch {
		case strings.HasSuffix(line, "{"):
			f := strings.Fields(strings.TrimSuffix(line, "{"))
			b := &olsBlock{values: map[string]string{}}
			if len(f) > 0 {
				b.kind = f[0]
			}
			if len(f) > 1 {
				b.arg = f[1]
			}
			cur.children = append(cur.children, b)
			stack = append(stack, b)
		case line == "}":
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
		default:
			k, v, ok := strings.Cut(line, " ")
			switch {
			case !ok:
				// a bare word; nothing to keep
			case k == "member":
				// "member example.com" without a block
				cur.children = append(cur.children, &olsBlock{kind: "member", arg: strings.TrimSpace(v), values: map[string]string{}})
			default:
				cur.values[k] = strings.TrimSpace(v)
			}
		}
	}
	return root
}

func readOLSConf(path string) []lswsVHost {
	root := parseOLS(path)
	if root == nil {
		return nil
	}
	// "map <vhost> <domains>" lines of the listeners.
	domains := map[string]string{}
	for _, line := range olsMapLines(path) {
		if f := strings.Fields(line); len(f) >= 3 {
			if _, ok := domains[f[1]]; !ok {
				domains[f[1]] = firstDomain(strings.Join(f[2:], " "))
			}
		}
	}
	var out []lswsVHost
	for _, b := range root.children {
		switch b.kind {
		case "virtualhost", "virtualHost":
			out = append(out, lswsVHost{name: b.arg, domain: domains[b.arg], vhRoot: b.values["vhRoot"], configFile: b.values["configFile"]})
		case "vhTemplate":
			tpl := parseOLS(lswsPath(lswsExpand(b.values["templateFile"], "", ""), ""))
			if tpl == nil {
				continue
			}
			docRoot := ""
			for _, c := range tpl.children {
				if c.kind == "virtualHostConfig" {
					docRoot = c.values["docRoot"]
				}
			}
			for _, m := range b.children {
				if m.kind != "member" || m.arg == "" {
					continue
				}
				root := tpl.values["vhRoot"]
				if m.values["vhRoot"] != "" {
					root = m.values["vhRoot"]
				}
				domain := firstDomain(m.values["vhDomain"])
				if domain == "" {
					domain = m.arg
				}
				out = append(out, lswsVHost{name: m.arg, domain: domain, vhRoot: root, configFile: tpl.values["configFile"], docRoot: docRoot})
			}
		}
	}
	return out
}

func olsMapLines(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "map ") {
			out = append(out, t)
		}
	}
	return out
}
