package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// LockPaths takes an exclusive lock for each webroot, so two runs (sync,
// promote or restore) never write the same site at once. The locks are keyed
// by the cleaned path, not the site name, and are released by the returned
// function or when the process ends. It fails at once if another run holds a
// lock.
func LockPaths(paths ...string) (func(), error) {
	dir, err := configDir()
	if err != nil {
		return nil, err
	}
	dir = filepath.Join(dir, "locks")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	// A fixed order, so two runs that lock the same pair never deadlock.
	clean := make([]string, 0, len(paths))
	for _, p := range paths {
		if p = filepath.Clean(strings.TrimSpace(p)); p != "." && p != "" {
			clean = append(clean, p)
		}
	}
	sort.Strings(clean)

	var held []*os.File
	release := func() {
		for _, f := range held {
			_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
			f.Close()
		}
	}
	for _, p := range clean {
		sum := sha256.Sum256([]byte(p))
		name := filepath.Join(dir, hex.EncodeToString(sum[:8])+".lock")
		f, err := os.OpenFile(name, os.O_RDWR|os.O_CREATE, 0600)
		if err != nil {
			release()
			return nil, err
		}
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			owner, _ := os.ReadFile(name)
			f.Close()
			release()
			pid := strings.TrimSpace(string(owner))
			if pid == "" {
				pid = "unknown"
			}
			return nil, fmt.Errorf("another wp-stage-sync run (pid %s) is using %s. Wait for it to finish", pid, p)
		}
		_ = f.Truncate(0)
		_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
		held = append(held, f)
	}
	return release, nil
}
