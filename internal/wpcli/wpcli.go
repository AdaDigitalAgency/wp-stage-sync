package wpcli

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
)

// Pinned wp-cli build. Verifying against a hardcoded hash means a tampered or
// swapped download is rejected even if the origin is compromised. Bumping the
// version also invalidates any cached phar (the hash no longer matches).
const (
	wpcliVersion = "2.12.0"
	wpcliSHA256  = "ce34ddd838f7351d6759068d09793f26755463b4a4610a5a5c0a97b68220d85c"
)

func pharURL() string {
	return fmt.Sprintf("https://github.com/wp-cli/wp-cli/releases/download/v%s/wp-cli-%s.phar", wpcliVersion, wpcliVersion)
}

// Resolve returns the wp-cli command parts: either ["wp"] if wp is in PATH,
// or ["php", "/path/to/wp-cli.phar"], downloading and verifying the PHAR if needed.
func Resolve() ([]string, error) {
	if path, err := exec.LookPath("wp"); err == nil {
		return []string{path}, nil
	}

	pharPath := localPharPath()
	if verifyPhar(pharPath) == nil {
		return pharCmd(pharPath)
	}

	// wp not found and no valid cached phar — download the pinned build.
	if _, err := exec.LookPath("php"); err != nil {
		return nil, fmt.Errorf("wp-cli not found and php not available to run the PHAR fallback; install wp-cli or php")
	}

	if err := downloadPhar(pharPath); err != nil {
		return nil, fmt.Errorf("downloading wp-cli.phar: %w", err)
	}

	return pharCmd(pharPath)
}

// ResolveExisting behaves like Resolve but never downloads the PHAR. It returns
// the wp-cli command parts and ok=true when wp-cli is available locally, or
// ok=false when it is not (so callers can plan without side effects).
func ResolveExisting() ([]string, bool) {
	if path, err := exec.LookPath("wp"); err == nil {
		return []string{path}, true
	}
	pharPath := localPharPath()
	if verifyPhar(pharPath) == nil {
		if cmd, err := pharCmd(pharPath); err == nil {
			return cmd, true
		}
	}
	return nil, false
}

func localPharPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "wp-stage-sync", "wp-cli.phar")
}

func pharCmd(pharPath string) ([]string, error) {
	phpPath, err := exec.LookPath("php")
	if err != nil {
		return nil, fmt.Errorf("wp-cli.phar exists but php not found in PATH")
	}
	return []string{phpPath, pharPath}, nil
}

// verifyPhar returns nil only when the file at path matches the pinned SHA256.
func verifyPhar(path string) error {
	if path == "" {
		return fmt.Errorf("no phar path")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != wpcliSHA256 {
		return fmt.Errorf("checksum mismatch: got %s, want %s", got, wpcliSHA256)
	}
	return nil
}

func downloadPhar(dest string) error {
	if dest == "" {
		return fmt.Errorf("cannot resolve wp-cli.phar path")
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
		return err
	}

	resp, err := http.Get(pharURL())
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// Download to a temp file, verify, then move into place — never expose an
	// unverified or partial phar at the final path.
	tmp := dest + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0700)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	f.Close()

	if err := verifyPhar(tmp); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("verifying wp-cli %s: %w", wpcliVersion, err)
	}
	return os.Rename(tmp, dest)
}
