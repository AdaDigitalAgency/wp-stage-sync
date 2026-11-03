package sync

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
)

// W3 Total Cache keeps its settings in wp-content/w3tc-config/*.php (or in
// the w3tc_config_0 option): "<?php exit; ?>" followed by JSON. The file sync
// copies them, so stage would hold live's Cloudflare, CDN and message-bus
// credentials, and live's instance_id. Every W3TC cache key starts with
// w3tc_<instance_id>, so with a shared Redis or Memcached, stage's group
// flushes invalidate live's object, database and page cache groups.

const w3tcFilePrefix = "<?php exit; ?>"

// w3tcBlank lists the settings set to "" on stage: remote purge and push
// credentials (CDN, full-site CDN, Cloudflare extension, SNS message bus).
var w3tcBlank = []string{
	"cdn.s3.key", "cdn.s3.secret", "cdn.cf.key", "cdn.cf.secret", "cdn.cf2.key", "cdn.cf2.secret",
	"cdn.ftp.pass", "cdn.ftp.privkey", "cdn.rscf.key", "cdn.rackspace_cdn.api_key", "cdn.azure.key",
	"cdn.google_drive.refresh_token", "cdn.bunnycdn.account_api_key", "cdn.bunnycdn.storage_api_key",
	"cdnfsd.cloudfront.access_key", "cdnfsd.cloudfront.secret_key", "cdnfsd.cloudfront.distribution_id",
	"cdnfsd.transparentcdn.client_id", "cdnfsd.transparentcdn.client_secret", "cdnfsd.transparentcdn.company_id",
	"cdnfsd.bunnycdn.pull_zone_id",
	"cluster.messagebus.sns.api_key", "cluster.messagebus.sns.api_secret", "cluster.messagebus.sns.topic_arn",
}

// w3tcOff lists the switches turned off on stage. A push CDN deletes and
// uploads files in live's bucket; full-site CDN and the message bus purge or
// flush live.
var w3tcOff = []string{"cdn.enabled", "cdnfsd.enabled", "cluster.messagebus.enabled"}

// w3tcInstanceID returns the instance_id stage gets: stable for one stage
// path, never 0, and never live's value. A value this function already gave
// (cur) is kept, so a second rewrite changes nothing.
func w3tcInstanceID(stagePath string, cur json.Number) json.Number {
	base := int64(crc32.ChecksumIEEE([]byte("wp-stage-sync:"+strings.TrimRight(stagePath, "/"))) & 0x7fffffff)
	if base == 0 {
		base = 1
	}
	if c, err := cur.Int64(); err == nil && (c == base || c == base+1) {
		return cur
	}
	return json.Number(fmt.Sprint(base))
}

// w3tcStageJSON rewrites one W3TC config JSON document for stage.
func w3tcStageJSON(raw []byte, livePath, stagePath string) ([]byte, int, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var cfg map[string]any
	if err := dec.Decode(&cfg); err != nil {
		return nil, 0, fmt.Errorf("W3 Total Cache config: %w", err)
	}
	changed := 0
	set := func(key string, v any) {
		old, ok := cfg[key]
		if !ok {
			return
		}
		if fmt.Sprint(old) != fmt.Sprint(v) {
			cfg[key] = v
			changed++
		}
	}
	for _, k := range w3tcBlank {
		set(k, "")
	}
	for _, k := range w3tcOff {
		set(k, false)
	}
	if cf, ok := cfg["cloudflare"].(map[string]any); ok {
		for _, k := range []string{"email", "key", "zone_id", "zone_name"} {
			if v, ok := cf[k]; ok && v != "" {
				cf[k] = ""
				changed++
			}
		}
	}
	for _, k := range []string{"extensions.active", "extensions.active_frontend"} {
		if m, ok := cfg[k].(map[string]any); ok {
			if _, on := m["cloudflare"]; on {
				delete(m, "cloudflare")
				changed++
			}
		}
	}
	if live, ok := cfg["common.instance_id"].(json.Number); ok {
		set("common.instance_id", w3tcInstanceID(stagePath, live))
	}
	if p, ok := cfg["config.path"].(string); ok && p != "" {
		if fixed, n := replaceLivePath(p, livePath, stagePath); n > 0 {
			cfg["config.path"] = fixed
			changed++
		}
	}
	if changed == 0 {
		return raw, 0, nil
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(cfg); err != nil {
		return nil, 0, err
	}
	return bytes.TrimRight(out.Bytes(), "\n"), changed, nil
}

// w3tcStageOption is the Neutralize rewrite for the w3tc_config_* options.
func w3tcStageOption(livePath, stagePath string) func(string) (string, error) {
	return func(raw string) (string, error) {
		out, _, err := w3tcStageJSON([]byte(raw), livePath, stagePath)
		return string(out), err
	}
}

// w3tcConfigFiles lists the W3TC config files of a WordPress root.
func w3tcConfigFiles(root string) []string {
	files, _ := filepath.Glob(filepath.Join(root, "wp-content", "w3tc-config", "*.php"))
	return files
}

// fixW3TCConfig rewrites one W3TC config file for stage. src and dst can be
// the same file. It reports how many settings changed.
func fixW3TCConfig(src, dst, livePath, stagePath string) (int, error) {
	data, err := os.ReadFile(src)
	if err != nil {
		return 0, err
	}
	body, ok := bytes.CutPrefix(data, []byte(w3tcFilePrefix))
	if !ok {
		return 0, nil // not a W3TC JSON config (index.html guard, old PHP format)
	}
	fixed, n, err := w3tcStageJSON(body, livePath, stagePath)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", src, err)
	}
	if n == 0 && src == dst {
		return 0, nil
	}
	mode := os.FileMode(0644)
	if info, err := os.Stat(src); err == nil {
		mode = info.Mode().Perm()
	}
	return n, os.WriteFile(dst, append([]byte(w3tcFilePrefix), fixed...), mode)
}

// stageW3TCConfigForProbe writes live's W3TC config files to stage, rewritten
// for stage, before the object cache probe. It returns a function that puts
// stage's previous files back.
func stageW3TCConfigForProbe(livePath, stagePath string) (func(), error) {
	dir := filepath.Join(stagePath, "wp-content", "w3tc-config")
	type saved struct {
		path string
		data []byte
		had  bool
	}
	var backup []saved
	restore := func() {
		for _, b := range backup {
			if b.had {
				_ = os.WriteFile(b.path, b.data, 0644)
			} else {
				_ = os.Remove(b.path)
			}
		}
	}
	files := w3tcConfigFiles(livePath)
	if len(files) == 0 {
		return restore, nil
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return restore, err
	}
	uid, gid := detectOwnership(stagePath)
	for _, src := range files {
		dst := filepath.Join(dir, filepath.Base(src))
		old, err := os.ReadFile(dst)
		backup = append(backup, saved{dst, old, err == nil})
		if _, err := fixW3TCConfig(src, dst, livePath, stagePath); err != nil {
			restore()
			return func() {}, err
		}
		_ = os.Chown(dst, int(uid), int(gid))
	}
	return restore, nil
}
