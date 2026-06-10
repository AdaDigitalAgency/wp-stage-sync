package promote

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/AdaDigitalAgency/wp-stage-sync/internal/config"
	"github.com/AdaDigitalAgency/wp-stage-sync/internal/progress"
	"github.com/AdaDigitalAgency/wp-stage-sync/internal/wpcli"
)

type AssetType string

const (
	AssetTheme    AssetType = "theme"
	AssetPlugin   AssetType = "plugin"
	AssetMuPlugin AssetType = "mu-plugin"
)

type SelectedItem struct {
	Type      AssetType
	Name      string
	StagePath string
	LivePath  string
}

type BackupMeta struct {
	Domain    string       `json:"domain"`
	Timestamp string       `json:"timestamp"`
	Items     []BackupItem `json:"items"`
}

type BackupItem struct {
	Type     AssetType `json:"type"`
	Name     string    `json:"name"`
	LivePath string    `json:"live_path"`
}

// ListAssets scans the staging wp-content for themes, plugins, and mu-plugins.
func ListAssets(stagePath string) (themes, plugins, muPlugins []string) {
	wpContent := filepath.Join(stagePath, "wp-content")

	themes = listDirs(filepath.Join(wpContent, "themes"))
	plugins = listDirs(filepath.Join(wpContent, "plugins"))
	muPlugins = listAll(filepath.Join(wpContent, "mu-plugins"))

	return
}

func listDirs(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			names = append(names, e.Name())
		}
	}
	return names
}

func listAll(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".") {
			names = append(names, e.Name())
		}
	}
	return names
}

// ValidatePromote checks that all selected items exist on staging.
func ValidatePromote(items []SelectedItem) error {
	for _, item := range items {
		if _, err := os.Stat(item.StagePath); os.IsNotExist(err) {
			return fmt.Errorf("%s %q does not exist on staging at %s", item.Type, item.Name, item.StagePath)
		}
	}
	return nil
}

// CreateBackup creates a tar.gz of the live versions of all selected items.
func CreateBackup(domain string, items []SelectedItem, log progress.Logger) (string, error) {
	dir, err := config.BackupsDir(domain)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}

	ts := time.Now().Format("2006-01-02_15-04-05")
	archivePath := filepath.Join(dir, ts+".tar.gz")
	metaPath := filepath.Join(dir, ts+".json")

	// Create tar.gz
	f, err := os.Create(archivePath)
	if err != nil {
		return "", fmt.Errorf("creating archive: %w", err)
	}
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)

	// fail closes the writers and removes the partial archive.
	fail := func(format string, a ...interface{}) (string, error) {
		tw.Close()
		gw.Close()
		f.Close()
		os.Remove(archivePath)
		return "", fmt.Errorf(format, a...)
	}

	var backupItems []BackupItem
	for i, item := range items {
		log.Detail(fmt.Sprintf("Backing up %s: %s", item.Type, item.Name))
		log.Progress(i, len(items))

		// Check if the item exists on live — if not, record it but skip archiving
		if _, err := os.Stat(item.LivePath); os.IsNotExist(err) {
			backupItems = append(backupItems, BackupItem{
				Type:     item.Type,
				Name:     item.Name,
				LivePath: item.LivePath,
			})
			continue
		}

		// Use a relative prefix for the archive: type/name/...
		prefix := string(item.Type) + "/" + item.Name
		if err := addToTar(tw, item.LivePath, prefix); err != nil {
			return fail("archiving %s %s: %w", item.Type, item.Name, err)
		}

		backupItems = append(backupItems, BackupItem{
			Type:     item.Type,
			Name:     item.Name,
			LivePath: item.LivePath,
		})
	}
	log.Progress(len(items), len(items))

	// Finalize and flush the archive to disk before it is recorded as valid, so
	// a truncated archive is never treated as a usable backup.
	if err := tw.Close(); err != nil {
		return fail("finalizing archive: %w", err)
	}
	if err := gw.Close(); err != nil {
		return fail("finalizing gzip: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fail("flushing archive: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(archivePath)
		return "", fmt.Errorf("closing archive: %w", err)
	}

	// Write metadata sidecar
	meta := BackupMeta{
		Domain:    domain,
		Timestamp: ts,
		Items:     backupItems,
	}
	metaData, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		os.Remove(archivePath)
		return "", err
	}
	if err := os.WriteFile(metaPath, metaData, 0644); err != nil {
		os.Remove(archivePath)
		return "", err
	}

	return archivePath, nil
}

func addToTar(tw *tar.Writer, srcPath, prefix string) error {
	info, err := os.Stat(srcPath)
	if err != nil {
		return err
	}

	if !info.IsDir() {
		// Single file (e.g. mu-plugin .php file)
		return addFileToTar(tw, srcPath, prefix)
	}

	return filepath.Walk(srcPath, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcPath, path)
		if err != nil {
			return err
		}
		archiveName := prefix
		if rel != "." {
			archiveName = prefix + "/" + rel
		}

		header, err := tar.FileInfoHeader(fi, "")
		if err != nil {
			return err
		}
		header.Name = archiveName

		if fi.IsDir() {
			header.Name += "/"
			return tw.WriteHeader(header)
		}

		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
}

func addFileToTar(tw *tar.Writer, path, name string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	header, err := tar.FileInfoHeader(fi, "")
	if err != nil {
		return err
	}
	header.Name = name
	if err := tw.WriteHeader(header); err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(tw, f)
	return err
}

// EnforceRetention keeps only the last maxKeep backups per domain.
func EnforceRetention(domain string, maxKeep int) error {
	dir, err := config.BackupsDir(domain)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil // no backups dir is fine
	}

	// Collect unique timestamps
	var timestamps []string
	seen := make(map[string]bool)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tar.gz") {
			ts := strings.TrimSuffix(e.Name(), ".tar.gz")
			if !seen[ts] {
				seen[ts] = true
				timestamps = append(timestamps, ts)
			}
		}
	}

	sort.Strings(timestamps)

	if len(timestamps) <= maxKeep {
		return nil
	}

	// Delete oldest
	toDelete := timestamps[:len(timestamps)-maxKeep]
	for _, ts := range toDelete {
		os.Remove(filepath.Join(dir, ts+".tar.gz"))
		os.Remove(filepath.Join(dir, ts+".json"))
	}
	return nil
}

// Execute rsyncs all items from staging to live. On failure, auto-restores.
func Execute(items []SelectedItem, livePath, archivePath string, log progress.Logger) error {
	for i, item := range items {
		log.Step(fmt.Sprintf("Promoting %s: %s", item.Type, item.Name))
		log.Progress(i, len(items))

		src := item.StagePath
		dst := item.LivePath

		// Determine if source is a file or directory
		info, err := os.Stat(src)
		if err != nil {
			log.Detail(fmt.Sprintf("Failed: %s not found on staging", item.Name))
			if rerr := autoRestore(archivePath, livePath, log); rerr != nil {
				return fmt.Errorf("promote failed for %s %s: %w; live may be inconsistent — %v", item.Type, item.Name, err, rerr)
			}
			return fmt.Errorf("promote failed for %s %s: %w — live restored from backup", item.Type, item.Name, err)
		}

		var args []string
		if info.IsDir() {
			args = []string{"-a", "--delete", src + "/", dst + "/"}
		} else {
			// Ensure parent dir exists
			os.MkdirAll(filepath.Dir(dst), 0755)
			args = []string{"-a", src, dst}
		}

		cmd := exec.Command("rsync", args...)
		if err := cmd.Run(); err != nil {
			log.Detail(fmt.Sprintf("rsync failed for %s %s: %v", item.Type, item.Name, err))
			if rerr := autoRestore(archivePath, livePath, log); rerr != nil {
				return fmt.Errorf("promote failed for %s %s: %w; live may be inconsistent — %v", item.Type, item.Name, err, rerr)
			}
			return fmt.Errorf("promote failed for %s %s: %w — live restored from backup", item.Type, item.Name, err)
		}
	}
	log.Progress(len(items), len(items))

	// Cache flush on live
	settings := config.LoadSettings()
	if settings.AutoCacheFlush {
		log.Step("Flushing cache on live site")
		if err := flushCache(livePath); err != nil {
			log.Detail(fmt.Sprintf("Cache flush warning: %v", err))
		}
		log.StepDone("Cache flushed")
	}

	return nil
}

func autoRestore(archivePath, livePath string, log progress.Logger) error {
	log.Step("Automatic restore triggered")
	meta, err := LoadBackupMeta(archivePath)
	if err != nil {
		return fmt.Errorf("could not load backup metadata: %w", err)
	}
	if err := RestoreFromBackup(archivePath, meta, livePath, log); err != nil {
		return fmt.Errorf("automatic restore failed: %w", err)
	}
	log.StepDone("Automatic restore completed successfully")
	return nil
}

// LoadBackupMeta reads the JSON sidecar for a backup archive.
func LoadBackupMeta(archivePath string) (*BackupMeta, error) {
	metaPath := strings.TrimSuffix(archivePath, ".tar.gz") + ".json"
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, err
	}
	var meta BackupMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

// RestoreFromBackup extracts items from the archive and rsyncs them to live.
func RestoreFromBackup(archivePath string, meta *BackupMeta, livePath string, log progress.Logger) error {
	// Extract to temp dir
	tmpDir, err := os.MkdirTemp("", "wpss-restore-*")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	log.Detail("Extracting backup archive")
	if err := extractTarGz(archivePath, tmpDir); err != nil {
		return fmt.Errorf("extracting archive: %w", err)
	}

	for i, item := range meta.Items {
		log.Detail(fmt.Sprintf("Restoring %s: %s", item.Type, item.Name))
		log.Progress(i, len(meta.Items))

		extractedPath := filepath.Join(tmpDir, string(item.Type), item.Name)

		// Check if the item was in the backup
		info, err := os.Stat(extractedPath)
		if os.IsNotExist(err) {
			// Item didn't exist on live before promote — remove what the promote
			// added, but only if the path is safely inside live wp-content (guard
			// against a bad metadata entry triggering a destructive removal).
			if underWPContent(livePath, item.LivePath) {
				os.RemoveAll(item.LivePath)
			} else {
				log.Detail(fmt.Sprintf("WARNING: skipping removal of %q (outside live wp-content)", item.LivePath))
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("checking extracted %s %s: %w", item.Type, item.Name, err)
		}

		var args []string
		if info.IsDir() {
			args = []string{"-a", "--delete", extractedPath + "/", item.LivePath + "/"}
		} else {
			os.MkdirAll(filepath.Dir(item.LivePath), 0755)
			args = []string{"-a", extractedPath, item.LivePath}
		}

		cmd := exec.Command("rsync", args...)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("restoring %s %s: %w", item.Type, item.Name, err)
		}
	}
	log.Progress(len(meta.Items), len(meta.Items))

	// Cache flush
	settings := config.LoadSettings()
	if settings.AutoCacheFlush {
		log.Detail("Flushing cache after restore")
		if err := flushCache(livePath); err != nil {
			log.Detail(fmt.Sprintf("Cache flush warning: %v", err))
		}
	}

	return nil
}

// safeJoin joins destDir and an archive member name, rejecting names that would
// escape destDir (path traversal from a crafted archive).
func safeJoin(destDir, name string) (string, error) {
	target := filepath.Join(destDir, name)
	clean := filepath.Clean(destDir)
	if target != clean && !strings.HasPrefix(target, clean+string(os.PathSeparator)) {
		return "", fmt.Errorf("unsafe path in archive: %q", name)
	}
	return target, nil
}

// underWPContent reports whether target is a path strictly inside the live
// wp-content directory — a guard before any destructive removal.
func underWPContent(livePath, target string) bool {
	base := filepath.Clean(filepath.Join(livePath, "wp-content"))
	t := filepath.Clean(target)
	return t != base && strings.HasPrefix(t, base+string(os.PathSeparator))
}

func extractTarGz(archivePath, destDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		target, err := safeJoin(destDir, header.Name)
		if err != nil {
			return err
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(header.Mode)); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			outFile, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(outFile, tr); err != nil {
				outFile.Close()
				return err
			}
			outFile.Close()
		}
	}
	return nil
}

// ListBackups returns backup metadata sorted most-recent-first.
func ListBackups(domain string) ([]BackupMeta, error) {
	dir, err := config.BackupsDir(domain)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil
	}

	var metas []BackupMeta
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var meta BackupMeta
		if err := json.Unmarshal(data, &meta); err != nil {
			continue
		}
		// Verify the archive exists
		archiveName := strings.TrimSuffix(e.Name(), ".json") + ".tar.gz"
		if _, err := os.Stat(filepath.Join(dir, archiveName)); err != nil {
			continue
		}
		metas = append(metas, meta)
	}

	sort.Slice(metas, func(i, j int) bool {
		return metas[i].Timestamp > metas[j].Timestamp
	})

	return metas, nil
}

// BackupArchivePath returns the full path to a backup archive for a domain and timestamp.
func BackupArchivePath(domain, timestamp string) (string, error) {
	dir, err := config.BackupsDir(domain)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, timestamp+".tar.gz"), nil
}

func flushCache(wpPath string) error {
	wpBase, err := wpcli.Resolve()
	if err != nil {
		return err
	}
	args := make([]string, len(wpBase), len(wpBase)+3)
	copy(args, wpBase)
	args = append(args, "cache", "flush", "--allow-root", "--path="+wpPath)
	cmd := exec.Command(args[0], args[1:]...)
	return cmd.Run()
}
