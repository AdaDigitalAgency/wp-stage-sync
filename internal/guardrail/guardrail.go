package guardrail

import (
	"database/sql"
	"fmt"
	"path/filepath"

	"github.com/AdaDigitalAgency/wp-stage-sync/internal/wpconfig"
)

// resolvePath returns the absolute, symlink-resolved form of p so two paths
// pointing at the same directory compare equal (lexical fallback if missing).
func resolvePath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved, nil
	}
	return abs, nil
}

// ValidatePaths ensures live and stage paths are different.
func ValidatePaths(livePath, stagePath string) error {
	absLive, err := resolvePath(livePath)
	if err != nil {
		return fmt.Errorf("resolving live path: %w", err)
	}
	absStage, err := resolvePath(stagePath)
	if err != nil {
		return fmt.Errorf("resolving stage path: %w", err)
	}
	if absLive == absStage {
		return fmt.Errorf("SAFETY: live path and stage path are identical (%s) — aborting to protect production", absLive)
	}
	return nil
}

// ValidateDBs is a cheap pre-connect check that aborts when the configs point
// at the same database. ValidateConnectedDBs is the authoritative guard.
func ValidateDBs(liveWP, stageWP *wpconfig.WPConfig) error {
	if liveWP.DBName == stageWP.DBName && liveWP.DBHost == stageWP.DBHost {
		return fmt.Errorf("SAFETY: live and stage use the same database (%s@%s) — aborting to protect production", liveWP.DBName, liveWP.DBHost)
	}
	return nil
}

// ValidateConnectedDBs aborts if the two connections resolve to the same server
// instance and schema — the authoritative same-database guard.
func ValidateConnectedDBs(liveDB, stageDB *sql.DB) error {
	liveID, err := serverIdentity(liveDB)
	if err != nil {
		return fmt.Errorf("checking live database identity: %w", err)
	}
	stageID, err := serverIdentity(stageDB)
	if err != nil {
		return fmt.Errorf("checking stage database identity: %w", err)
	}
	if liveID == stageID {
		return fmt.Errorf("SAFETY: live and stage resolve to the same database (%s) — aborting to protect production", liveID)
	}
	return nil
}

// serverIdentity returns "<server>/<schema>" for a connection. Server prefers
// @@server_uuid, falling back to hostname:port when unavailable.
func serverIdentity(db *sql.DB) (string, error) {
	var schema sql.NullString
	if err := db.QueryRow("SELECT DATABASE()").Scan(&schema); err != nil {
		return "", err
	}

	var server string
	if err := db.QueryRow("SELECT @@server_uuid").Scan(&server); err != nil || server == "" {
		var host string
		var port int
		if err := db.QueryRow("SELECT @@hostname, @@port").Scan(&host, &port); err != nil {
			return "", err
		}
		server = fmt.Sprintf("%s:%d", host, port)
	}
	return server + "/" + schema.String, nil
}

// ValidateWPContentPaths ensures live and stage wp-content directories are different.
func ValidateWPContentPaths(livePath, stagePath string) error {
	absLive, err := resolvePath(filepath.Join(livePath, "wp-content"))
	if err != nil {
		return fmt.Errorf("resolving live wp-content path: %w", err)
	}
	absStage, err := resolvePath(filepath.Join(stagePath, "wp-content"))
	if err != nil {
		return fmt.Errorf("resolving stage wp-content path: %w", err)
	}
	if absLive == absStage {
		return fmt.Errorf("SAFETY: live and stage wp-content paths are identical (%s) — aborting to protect production", absLive)
	}
	return nil
}
