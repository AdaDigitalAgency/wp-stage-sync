package sync

import (
	"bytes"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
)

// CheckNeutralize runs every stage settings rewrite on live's values, in
// memory, before the sync changes anything. The import copies live's options
// table to stage unchanged, so a value that cannot be read here would stop
// Neutralize after the import, with stage half synced. Live is only read.
func CheckNeutralize(liveDB *sql.DB, prefix, livePath, stagePath, liveHost, stageHost string, cacheOff bool) error {
	q := fmt.Sprintf("SELECT option_value FROM `%soptions` WHERE option_name = ?", prefix)
	get := func(option string) (string, error) {
		var raw string
		err := liveDB.QueryRow(q, option).Scan(&raw)
		if err == sql.ErrNoRows {
			return "", nil
		}
		return raw, err
	}
	return checkNeutralize(get, livePath, stagePath, liveHost, stageHost, cacheOff)
}

// checkNeutralize is CheckNeutralize with the option lookup passed in. get
// returns "" for a missing option.
func checkNeutralize(get func(string) (string, error), livePath, stagePath, liveHost, stageHost string, cacheOff bool) error {
	type check struct {
		option, label string
		rewrite       func(string) (string, error)
	}
	var checks []check
	for _, in := range integrationsFor(livePath, stagePath, liveHost, stageHost) {
		for option, rewrite := range in.Rewrite {
			checks = append(checks, check{option, in.Name, rewrite})
		}
	}
	if cacheOff {
		for _, s := range cacheOffSettings {
			checks = append(checks, check{s.option, s.label, s.rewrite})
		}
	}
	for _, c := range checks {
		raw, err := get(c.option)
		if err != nil {
			return fmt.Errorf("reading live option %s: %w", c.option, err)
		}
		if raw == "" {
			continue
		}
		if _, err := c.rewrite(raw); err != nil {
			return fmt.Errorf("live option %s (%s) has a format the sync cannot change: %v. "+
				"The sync stopped before any change: it could not clear live's keys from stage", c.option, c.label, err)
		}
	}
	for _, path := range w3tcConfigFiles(livePath) {
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}
		body, ok := bytes.CutPrefix(data, []byte(w3tcFilePrefix))
		if !ok {
			continue
		}
		if _, _, err := w3tcStageJSON(body, livePath, stagePath); err != nil {
			return fmt.Errorf("live W3 Total Cache config %s has a format the sync cannot change: %v. "+
				"The sync stopped before any change", filepath.Base(path), err)
		}
	}
	return nil
}
