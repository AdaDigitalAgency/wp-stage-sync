package sync

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// phpEntry is one top-level element of a serialized PHP array. Values stay in
// their serialized form, so nested arrays pass through unchanged.
type phpEntry struct {
	key   string // serialized key, e.g. `s:13:"do_cloudflare";`
	name  string // key as text
	value string // serialized value
}

var errPHPSerial = errors.New("not a serialized PHP array")

// skipPHPValue returns the end offset of the serialized value at i.
func skipPHPValue(s string, i int) (int, error) {
	if i >= len(s) {
		return 0, errPHPSerial
	}
	switch s[i] {
	case 'N':
		if strings.HasPrefix(s[i:], "N;") {
			return i + 2, nil
		}
	case 'b', 'i', 'd':
		if i+1 < len(s) && s[i+1] == ':' {
			if end := strings.IndexByte(s[i:], ';'); end > 0 {
				return i + end + 1, nil
			}
		}
	case 's':
		colon := strings.IndexByte(s[i+2:], ':')
		if s[i+1] != ':' || colon < 0 {
			break
		}
		n, err := strconv.Atoi(s[i+2 : i+2+colon])
		start := i + 2 + colon + 2 // past `:"`
		end := start + n
		if err != nil || n < 0 || end+2 > len(s) || s[end:end+2] != `";` {
			break
		}
		return end + 2, nil
	case 'a':
		colon := strings.IndexByte(s[i+2:], ':')
		if s[i+1] != ':' || colon < 0 {
			break
		}
		n, err := strconv.Atoi(s[i+2 : i+2+colon])
		j := i + 2 + colon + 1
		if err != nil || j >= len(s) || s[j] != '{' {
			break
		}
		j++
		for k := 0; k < 2*n; k++ {
			if j, err = skipPHPValue(s, j); err != nil {
				return 0, err
			}
		}
		if j < len(s) && s[j] == '}' {
			return j + 1, nil
		}
	}
	return 0, fmt.Errorf("%w at offset %d", errPHPSerial, i)
}

// parsePHPArray splits a serialized top-level array into its entries.
func parsePHPArray(s string) ([]phpEntry, error) {
	end, err := skipPHPValue(s, 0)
	if err != nil || s[0] != 'a' || end != len(s) {
		return nil, errPHPSerial
	}
	j := strings.IndexByte(s, '{') + 1
	var entries []phpEntry
	for s[j] != '}' {
		kEnd, err := skipPHPValue(s, j)
		if err != nil {
			return nil, err
		}
		vEnd, err := skipPHPValue(s, kEnd)
		if err != nil {
			return nil, err
		}
		key := s[j:kEnd]
		name := strings.TrimSuffix(key[2:], ";")
		if key[0] == 's' {
			name = key[strings.IndexByte(key, '"')+1 : len(key)-2]
		}
		entries = append(entries, phpEntry{key: key, name: name, value: s[kEnd:vEnd]})
		j = vEnd
	}
	return entries, nil
}

func buildPHPArray(entries []phpEntry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "a:%d:{", len(entries))
	for _, e := range entries {
		b.WriteString(e.key)
		b.WriteString(e.value)
	}
	b.WriteString("}")
	return b.String()
}

// phpString serializes a PHP string. The length counts bytes.
func phpString(v string) string { return fmt.Sprintf("s:%d:\"%s\";", len(v), v) }

// setPHPKeys returns a rewrite that sets existing top-level keys of a
// serialized array. Missing keys are not added.
func setPHPKeys(values map[string]string) func(string) (string, error) {
	return func(raw string) (string, error) {
		entries, err := parsePHPArray(raw)
		if err != nil {
			return "", err
		}
		for i, e := range entries {
			if v, ok := values[e.name]; ok {
				entries[i].value = v
			}
		}
		return buildPHPArray(entries), nil
	}
}

// replacePHPValue returns a rewrite that sets a top-level key to `to` only
// when its serialized value is exactly `from`.
func replacePHPValue(key, from, to string) func(string) (string, error) {
	return func(raw string) (string, error) {
		entries, err := parsePHPArray(raw)
		if err != nil {
			return "", err
		}
		for i, e := range entries {
			if e.name == key && e.value == from {
				entries[i].value = to
			}
		}
		return buildPHPArray(entries), nil
	}
}
