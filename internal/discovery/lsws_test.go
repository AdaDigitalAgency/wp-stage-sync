package discovery

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func withLSWSRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	old := lswsRoot
	lswsRoot = root
	t.Cleanup(func() { lswsRoot = old })
	return root
}

func docRoots(vhosts []VHost) []string {
	var out []string
	for _, v := range vhosts {
		out = append(out, v.DocumentRoot)
	}
	return out
}

func TestScanLiteSpeedEnterpriseXML(t *testing.T) {
	root := withLSWSRoot(t)
	home := t.TempDir()
	writeFile(t, filepath.Join(root, "conf", "httpd_config.xml"), `<?xml version="1.0" encoding="UTF-8"?>
<httpServerConfig>
  <listenerList><listener><name>Default</name><address>*:80</address>
    <vhostMapList>
      <vhostMap><vhost>live</vhost><domain>example.com, www.example.com</domain></vhostMap>
      <vhostMap><vhost>Example</vhost><domain>*</domain></vhostMap>
    </vhostMapList></listener></listenerList>
  <virtualHostList>
    <virtualHost><name>live</name><vhRoot>`+home+`/example.com/</vhRoot><configFile>$VH_ROOT/conf/vhconf.xml</configFile></virtualHost>
    <virtualHost><name>Example</name><vhRoot>$SERVER_ROOT/DEFAULT/</vhRoot><configFile>$VH_ROOT/conf/vhconf.xml</configFile></virtualHost>
  </virtualHostList>
  <vhTemplateList>
    <vhTemplate><name>ccl</name><templateFile>$SERVER_ROOT/conf/templates/ccl.xml</templateFile><listeners>Default</listeners>
      <member><vhName>stage.example.com</vhName><vhDomain>stage.example.com</vhDomain></member>
    </vhTemplate>
  </vhTemplateList>
</httpServerConfig>`)
	writeFile(t, filepath.Join(home, "example.com", "conf", "vhconf.xml"), `<?xml version="1.0"?><virtualHostConfig><docRoot>$VH_ROOT/public_html/</docRoot></virtualHostConfig>`)
	writeFile(t, filepath.Join(root, "DEFAULT", "conf", "vhconf.xml"), `<?xml version="1.0"?><virtualHostConfig><docRoot>$VH_ROOT/html/</docRoot></virtualHostConfig>`)
	writeFile(t, filepath.Join(root, "conf", "templates", "ccl.xml"), `<?xml version="1.0"?>
<virtualHostTemplate><vhRoot>`+home+`/$VH_NAME/</vhRoot><configFile>$SERVER_ROOT/conf/$VH_NAME.xml</configFile>
  <virtualHostConfig><docRoot>$VH_ROOT/html/</docRoot></virtualHostConfig></virtualHostTemplate>`)

	vhosts := ScanLiteSpeedVHosts()
	got := docRoots(vhosts)
	for _, want := range []string{
		filepath.Join(home, "example.com", "public_html"),
		filepath.Join(root, "DEFAULT", "html"),
		filepath.Join(home, "stage.example.com", "html"),
		filepath.Join(home, "stage.example.com"),
	} {
		if !slices.Contains(got, want) {
			t.Errorf("missing %s in %v", want, got)
		}
	}
	if vhosts[0].ServerName != "example.com" {
		t.Errorf("server name = %q, want example.com", vhosts[0].ServerName)
	}
}

func TestScanOpenLiteSpeedConf(t *testing.T) {
	root := withLSWSRoot(t)
	writeFile(t, filepath.Join(root, "conf", "httpd_config.conf"), `
serverName
user                      nobody
listener Default {
  address                 *:80
  secure                  0
  map                     example.com example.com, www.example.com
  map                     Example *
}

virtualhost example.com {
  vhRoot                  /var/www/example.com/
  configFile              conf/vhosts/$VH_NAME/vhconf.conf
  allowSymbolLink         1
}

vhTemplate docker {
  templateFile            conf/templates/docker.conf
  listeners               Default
  member stage.example.com {
    vhDomain              stage.example.com
  }
  member dev.example.com
}
`)
	writeFile(t, filepath.Join(root, "conf", "vhosts", "example.com", "vhconf.conf"), "docRoot                   $VH_ROOT/html/\nenableGzip 1\n")
	writeFile(t, filepath.Join(root, "conf", "templates", "docker.conf"), `
vhRoot                    /var/www/vhosts/$VH_NAME/
configFile                $SERVER_ROOT/conf/vhosts/$VH_NAME/vhconf.conf
virtualHostConfig  {
  docRoot                 $VH_ROOT/html/
  index  {
    indexFiles            index.php
  }
}
`)
	got := docRoots(ScanLiteSpeedVHosts())
	for _, want := range []string{
		"/var/www/example.com/html",
		"/var/www/vhosts/stage.example.com/html",
		"/var/www/vhosts/dev.example.com/html",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("missing %s in %v", want, got)
		}
	}
}

func TestScanLiteSpeedMissingConfig(t *testing.T) {
	withLSWSRoot(t)
	if got := ScanLiteSpeedVHosts(); len(got) != 0 {
		t.Errorf("got %v from an empty server root", got)
	}
}

func TestExtractDomainSkipsGenericFolders(t *testing.T) {
	for in, want := range map[string]string{
		"/home/example.com":                    "example.com",
		"/home/stage.example.com/":             "stage.example.com",
		"/home/user/example.com/public_html":   "example.com",
		"/var/www/vhosts/example.com/httpdocs": "example.com",
		"/var/www/example.com/current/public":  "example.com",
		"/home/user/public_html":               "user",
		"/public_html":                         "public_html",
	} {
		if got := ExtractDomain(in); got != want {
			t.Errorf("ExtractDomain(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPairSitesWithPublicHTML(t *testing.T) {
	pairs := PairSites([]string{"/home/u/example.com/public_html", "/home/u/stage.example.com/public_html"})
	if len(pairs) != 1 || pairs[0].Domain != "example.com" || pairs[0].StagePath != "/home/u/stage.example.com/public_html" {
		t.Errorf("pairs = %+v", pairs)
	}
}
