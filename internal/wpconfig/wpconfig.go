package wpconfig

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

type WPConfig struct {
	DBName      string
	DBUser      string
	DBPassword  string
	DBHost      string
	TablePrefix string
}

// The value groups match a full PHP single- or double-quoted string, allowing
// escaped characters inside (e.g. a password containing a quote).
var quoted = `('(?:\\.|[^'\\])*'|"(?:\\.|[^"\\])*")`
var defineRe = regexp.MustCompile(`define\s*\(\s*['"](\w+)['"]\s*,\s*` + quoted + `\s*\)`)
var prefixRe = regexp.MustCompile(`\$table_prefix\s*=\s*` + quoted + `\s*;`)

// unquote strips the surrounding quotes from a PHP string literal and resolves
// the escape sequences PHP recognises for that quote style.
func unquote(s string) string {
	if len(s) < 2 {
		return s
	}
	q := s[0]
	body := s[1 : len(s)-1]
	var b strings.Builder
	for i := 0; i < len(body); i++ {
		c := body[i]
		if c == '\\' && i+1 < len(body) {
			n := body[i+1]
			if q == '\'' {
				if n == '\'' || n == '\\' {
					b.WriteByte(n)
					i++
					continue
				}
			} else {
				switch n {
				case '"', '\\', '$':
					b.WriteByte(n)
					i++
					continue
				case 'n':
					b.WriteByte('\n')
					i++
					continue
				case 't':
					b.WriteByte('\t')
					i++
					continue
				case 'r':
					b.WriteByte('\r')
					i++
					continue
				}
			}
		}
		b.WriteByte(c)
	}
	return b.String()
}

func Parse(path string) (*WPConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	content := string(data)

	cfg := &WPConfig{
		TablePrefix: "wp_", // default
	}

	defines := make(map[string]string)
	for _, match := range defineRe.FindAllStringSubmatch(content, -1) {
		defines[match[1]] = unquote(match[2])
	}

	required := map[string]*string{
		"DB_NAME":     &cfg.DBName,
		"DB_USER":     &cfg.DBUser,
		"DB_PASSWORD": &cfg.DBPassword,
		"DB_HOST":     &cfg.DBHost,
	}
	var missing []string
	for key, target := range required {
		val, ok := defines[key]
		if !ok {
			missing = append(missing, key)
			continue
		}
		*target = val
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing defines in %s: %s", path, strings.Join(missing, ", "))
	}

	if m := prefixRe.FindStringSubmatch(content); len(m) > 1 {
		cfg.TablePrefix = unquote(m[1])
	}

	return cfg, nil
}
