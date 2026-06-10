package sync

import (
	"database/sql"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/AdaDigitalAgency/wp-stage-sync/internal/wpcli"
)

// ListStageTables returns the tables currently present in the staging database.
// In a real run these are all dropped before import; in a dry-run it reports
// how many tables would be replaced.
func ListStageTables(db *sql.DB) ([]string, error) {
	return listTables(db)
}

// RsyncPlan captures what a file sync would transfer, produced via rsync's own
// --dry-run mode so nothing is actually written.
type RsyncPlan struct {
	Source  string
	Dest    string
	Command string
	Output  string // transfer stats from rsync --dry-run
}

// FileSyncPlan runs rsync in --dry-run mode to report what FileSync would
// transfer and delete, without copying any files or changing ownership.
func FileSyncPlan(livePath, stagePath string, excludes []string) (*RsyncPlan, error) {
	src := filepath.Join(livePath, "wp-content") + "/"
	dst := filepath.Join(stagePath, "wp-content") + "/"

	args := []string{"-a", "--delete", "--dry-run", "--stats"}
	for _, ex := range excludes {
		args = append(args, "--exclude="+ex)
	}
	args = append(args, src, dst)

	plan := &RsyncPlan{
		Source:  src,
		Dest:    dst,
		Command: "rsync " + strings.Join(args, " "),
	}

	cmd := exec.Command("rsync", args...)
	out, err := cmd.CombinedOutput()
	plan.Output = strings.TrimRight(string(out), "\n")
	if err != nil {
		return plan, fmt.Errorf("rsync --dry-run: %w", err)
	}
	return plan, nil
}

// PostProcessReport describes the WP-CLI commands PostProcess would run.
type PostProcessReport struct {
	WPAvailable bool             // false if wp-cli is not installed locally
	WPBase      []string         // the wp-cli invocation that would be used
	Commands    []PlannedCommand // ordered commands with skip decisions
}

// PostProcessPlan reports which WP-CLI commands PostProcess would run, without
// executing them and without downloading wp-cli if it is missing.
func PostProcessPlan(stagePath, liveHost, stageHost string) *PostProcessReport {
	wpBase, ok := wpcli.ResolveExisting()
	display := wpBase
	if !ok {
		// wp-cli would be resolved (and downloaded if needed) at run time;
		// show a representative invocation for the plan.
		display = []string{"wp"}
	}
	return &PostProcessReport{
		WPAvailable: ok,
		WPBase:      display,
		Commands:    postProcessCommands(display, stagePath, liveHost, stageHost),
	}
}
