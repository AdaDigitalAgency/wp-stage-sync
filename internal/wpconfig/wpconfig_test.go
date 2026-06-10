package wpconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "wp-config.php")
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParse(t *testing.T) {
	cfg, err := Parse(writeConfig(t, `<?php
define('DB_NAME', 'mydb');
define( "DB_USER" , "myuser" );
define('DB_PASSWORD', 'secret');
define('DB_HOST', 'localhost:3306');
$table_prefix = 'wp_custom_';
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DBName != "mydb" || cfg.DBUser != "myuser" || cfg.DBPassword != "secret" || cfg.DBHost != "localhost:3306" {
		t.Errorf("unexpected parse result: %+v", cfg)
	}
	if cfg.TablePrefix != "wp_custom_" {
		t.Errorf("TablePrefix = %q, want wp_custom_", cfg.TablePrefix)
	}
}

func TestParseDefaultPrefix(t *testing.T) {
	cfg, err := Parse(writeConfig(t, `<?php
define('DB_NAME', 'd');
define('DB_USER', 'u');
define('DB_PASSWORD', 'p');
define('DB_HOST', 'h');
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TablePrefix != "wp_" {
		t.Errorf("TablePrefix = %q, want default wp_", cfg.TablePrefix)
	}
}

func TestParseMissingDefine(t *testing.T) {
	_, err := Parse(writeConfig(t, `<?php
define('DB_NAME', 'd');
define('DB_USER', 'u');
`))
	if err == nil {
		t.Fatal("expected error when DB_PASSWORD/DB_HOST are missing")
	}
}

func TestParseFileNotFound(t *testing.T) {
	if _, err := Parse(filepath.Join(t.TempDir(), "nope.php")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestParseQuotedPasswords(t *testing.T) {
	cfg, err := Parse(writeConfig(t, `<?php
define('DB_NAME', 'd');
define('DB_USER', 'u');
define('DB_PASSWORD', 'p@ss\'w"ord');
define("DB_HOST", "loc\\alhost");
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DBPassword != `p@ss'w"ord` {
		t.Errorf("DBPassword = %q, want p@ss'w\"ord", cfg.DBPassword)
	}
	if cfg.DBHost != `loc\alhost` {
		t.Errorf("DBHost = %q, want loc\\alhost", cfg.DBHost)
	}
}
