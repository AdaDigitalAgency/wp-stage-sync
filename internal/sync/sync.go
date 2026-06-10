package sync

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/AdaDigitalAgency/wp-stage-sync/internal/config"
	"github.com/AdaDigitalAgency/wp-stage-sync/internal/export"
	"github.com/AdaDigitalAgency/wp-stage-sync/internal/progress"
	"github.com/AdaDigitalAgency/wp-stage-sync/internal/wpcli"
)

// backupPrefix names tables moved aside during import so a failure can roll back.
const backupPrefix = "_wpss_bak_"

// Import executes Step 1: move existing staging tables aside, import the
// exported SQL, then drop the backups (or restore them if the import fails).
func Import(stageDB *sql.DB, exp *export.Result, log progress.Logger) error {
	log.Detail("Disabling foreign key checks")
	if _, err := stageDB.Exec("SET FOREIGN_KEY_CHECKS=0"); err != nil {
		return fmt.Errorf("disabling FK checks: %w", err)
	}
	// Raise the packet limit for this session so a large INSERT (e.g. a big
	// post_content) doesn't reset the connection. This is session-settable on
	// MariaDB; on MySQL it is read-only, so we surface the limitation rather
	// than SET GLOBAL it (which would affect every database on the server).
	if _, err := stageDB.Exec("SET SESSION max_allowed_packet=268435456"); err != nil {
		log.Detail("Note: could not raise session max_allowed_packet; raise the server's max_allowed_packet if a very large row fails to import")
	}

	tables, err := listTables(stageDB)
	if err != nil {
		return fmt.Errorf("listing tables: %w", err)
	}

	// Move existing tables aside (a RENAME is instant — no data copy) so a
	// failed import can be rolled back. Drop any stale backups left by a
	// previously crashed run first.
	var live []string
	for _, t := range tables {
		if strings.HasPrefix(t, backupPrefix) {
			if _, err := stageDB.Exec(fmt.Sprintf("DROP TABLE IF EXISTS `%s`", t)); err != nil {
				return fmt.Errorf("clearing stale backup %s: %w", t, err)
			}
			continue
		}
		live = append(live, t)
	}

	log.Detail(fmt.Sprintf("Backing up %d existing tables", len(live)))
	backups := make(map[string]string, len(live)) // backup name -> original name
	for i, t := range live {
		bak := fmt.Sprintf("%s%d", backupPrefix, i)
		if _, err := stageDB.Exec(fmt.Sprintf("RENAME TABLE `%s` TO `%s`", t, bak)); err != nil {
			restoreBackups(stageDB, backups, log)
			return fmt.Errorf("backing up %s: %w", t, err)
		}
		backups[bak] = t
	}

	// Import; restore the backups if anything fails partway through.
	if err := execImport(stageDB, exp, log); err != nil {
		log.Detail("Import failed — restoring previous staging tables")
		restoreBackups(stageDB, backups, log)
		return err
	}

	log.Detail("Removing backup tables")
	for bak := range backups {
		if _, err := stageDB.Exec(fmt.Sprintf("DROP TABLE IF EXISTS `%s`", bak)); err != nil {
			return fmt.Errorf("removing backup %s: %w", bak, err)
		}
	}

	log.Detail("Re-enabling foreign key checks")
	if _, err := stageDB.Exec("SET FOREIGN_KEY_CHECKS=1"); err != nil {
		return fmt.Errorf("re-enabling FK checks: %w", err)
	}
	return nil
}

// execImport runs the exported statements in order: schema-only, base, users, orders.
func execImport(stageDB *sql.DB, exp *export.Result, log progress.Logger) error {
	groups := []struct {
		name  string
		stmts []string
	}{
		{"schema-only", exp.SchemaOnly},
		{"base", exp.Base},
		{"users", exp.Users},
		{"orders", exp.Orders},
	}
	totalStmts := 0
	for _, g := range groups {
		totalStmts += len(g.stmts)
	}
	doneStmts := 0
	for _, g := range groups {
		log.Detail(fmt.Sprintf("Importing %s (%d statements)", g.name, len(g.stmts)))
		for _, stmt := range g.stmts {
			if _, err := stageDB.Exec(stmt); err != nil {
				// Include first 200 chars of statement in error for debugging
				preview := stmt
				if len(preview) > 200 {
					preview = preview[:200] + "..."
				}
				return fmt.Errorf("executing %s SQL: %w\n  stmt: %s", g.name, err, preview)
			}
			doneStmts++
			log.Progress(doneStmts, totalStmts)
		}
	}
	return nil
}

// restoreBackups drops any partially-imported tables and renames the backups
// back to their original names. Best-effort: warnings are logged, not returned.
func restoreBackups(stageDB *sql.DB, backups map[string]string, log progress.Logger) {
	if cur, err := listTables(stageDB); err == nil {
		for _, t := range cur {
			if strings.HasPrefix(t, backupPrefix) {
				continue
			}
			_, _ = stageDB.Exec(fmt.Sprintf("DROP TABLE IF EXISTS `%s`", t))
		}
	}
	for bak, orig := range backups {
		if _, err := stageDB.Exec(fmt.Sprintf("RENAME TABLE `%s` TO `%s`", bak, orig)); err != nil {
			log.Detail(fmt.Sprintf("WARNING: could not restore %s from backup %s: %v", orig, bak, err))
		}
	}
}

// DefaultExcludes are the default rsync exclude patterns.
var DefaultExcludes = []string{
	"cache",
	"ewww",
	"critical-css",
	"litespeed",
	"updraft",
	"archive-master-db",
}

// FileSync executes Step 2: rsync wp-content and fix ownership.
func FileSync(livePath, stagePath string, excludes []string, log progress.Logger) error {
	src := filepath.Join(livePath, "wp-content") + "/"
	dst := filepath.Join(stagePath, "wp-content") + "/"

	log.Detail(fmt.Sprintf("rsync %s → %s", src, dst))
	args := []string{"-a", "--delete"}
	for _, ex := range excludes {
		args = append(args, "--exclude="+ex)
	}
	args = append(args, src, dst)
	cmd := exec.Command("rsync", args...)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("rsync: %w", err)
	}

	// Detect owner:group from staging webroot
	uid, gid := detectOwnership(stagePath)
	log.Detail(fmt.Sprintf("Fixing ownership to %d:%d", uid, gid))
	chown := exec.Command("chown", "-R", fmt.Sprintf("%d:%d", uid, gid), dst)
	if err := chown.Run(); err != nil {
		return fmt.Errorf("chown: %w", err)
	}

	return nil
}

// PlannedCommand is a single WP-CLI command in the post-processing step.
type PlannedCommand struct {
	Label    string
	Args     []string
	Skip     bool
	SkipNote string
}

// hostFromURL returns the bare host (no scheme, no path) of a site URL such as
// "https://example.com/blog" → "example.com". If s is already a bare host it is
// returned cleaned.
func hostFromURL(s string) string {
	s = strings.TrimSpace(s)
	if u, err := url.Parse(s); err == nil && u.Host != "" {
		return u.Host
	}
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimPrefix(s, "//")
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	return s
}

// SiteHost returns the host of the site's home/siteurl from the options table,
// falling back to the provided default when it cannot be read.
func SiteHost(db *sql.DB, prefix, fallback string) string {
	for _, opt := range []string{"home", "siteurl"} {
		var val string
		q := fmt.Sprintf("SELECT option_value FROM `%soptions` WHERE option_name = ? LIMIT 1", prefix)
		if err := db.QueryRow(q, opt).Scan(&val); err == nil {
			if h := hostFromURL(val); h != "" {
				return h
			}
		}
	}
	return fallback
}

// postProcessCommands builds the ordered WP-CLI command list (with skip
// decisions) shared by PostProcess and PostProcessPlan. wpBase is the resolved
// wp-cli invocation, e.g. ["wp"] or ["php", "/path/to/wp-cli.phar"].
func postProcessCommands(wpBase []string, stagePath, liveHost, stageHost string) []PlannedCommand {
	wpArgs := func(args ...string) []string {
		cmd := make([]string, len(wpBase), len(wpBase)+len(args))
		copy(cmd, wpBase)
		return append(cmd, args...)
	}

	hasElementor := dirExists(filepath.Join(stagePath, "wp-content", "plugins", "elementor"))
	hasJetpack := dirExists(filepath.Join(stagePath, "wp-content", "plugins", "jetpack"))

	var cmds []PlannedCommand

	// Replace https, then http, then protocol-relative. Doing the scheme-
	// prefixed forms first means the bare "//host" pass only hits genuine
	// protocol-relative URLs, and never a bare hostname (which would corrupt
	// email addresses and the like).
	for _, p := range []string{"https://", "http://", "//"} {
		from, to := p+liveHost, p+stageHost
		cmds = append(cmds, PlannedCommand{
			Label: fmt.Sprintf("Search-replace: %s → %s", from, to),
			Args: wpArgs("search-replace", from, to,
				"--all-tables", "--allow-root", "--path="+stagePath),
		})
	}

	for _, p := range []string{"https://", "http://"} {
		cmds = append(cmds, PlannedCommand{
			Label: fmt.Sprintf("Elementor URL replace: %s%s → %s%s", p, liveHost, p, stageHost),
			Args: wpArgs("elementor", "replace-urls",
				p+liveHost, p+stageHost, "--allow-root", "--path="+stagePath),
			Skip:     !hasElementor,
			SkipNote: "elementor plugin not installed",
		})
	}

	cmds = append(cmds,
		PlannedCommand{
			Label: "Jetpack safe mode",
			Args: wpArgs("option", "update", "jetpack_safe_mode_confirmed", "1",
				"--allow-root", "--quiet", "--path="+stagePath),
			Skip:     !hasJetpack,
			SkipNote: "jetpack plugin not installed",
		},
		PlannedCommand{
			Label:    "Cache flush",
			Args:     wpArgs("cache", "flush", "--allow-root", "--path="+stagePath),
			Skip:     !config.LoadSettings().AutoCacheFlush,
			SkipNote: "auto cache flush disabled in settings",
		},
	)
	return cmds
}

// PostProcess executes Step 3: WP-CLI search-replace and cache flush.
func PostProcess(stagePath, liveHost, stageHost string, log progress.Logger) error {
	wpBase, err := wpcli.Resolve()
	if err != nil {
		return fmt.Errorf("wp-cli: %w", err)
	}

	commands := postProcessCommands(wpBase, stagePath, liveHost, stageHost)
	for i, c := range commands {
		if c.Skip {
			log.Detail(fmt.Sprintf("Skipping: %s (%s)", c.Label, c.SkipNote))
			continue
		}
		log.Detail(c.Label)
		log.Progress(i, len(commands))
		cmd := exec.Command(c.Args[0], c.Args[1:]...)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("running %s: %w", strings.Join(c.Args, " "), err)
		}
	}
	log.Progress(len(commands), len(commands))
	return nil
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func detectOwnership(path string) (uint32, uint32) {
	info, err := os.Stat(path)
	if err != nil {
		// Fallback: www-data is typically uid/gid 33
		return 33, 33
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 33, 33
	}
	return stat.Uid, stat.Gid
}

func listTables(db *sql.DB) ([]string, error) {
	rows, err := db.Query("SHOW TABLES")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		tables = append(tables, t)
	}
	return tables, rows.Err()
}

// Anonymize masks personal customer details in the staging database.
func Anonymize(db *sql.DB, prefix string, log progress.Logger) error {
	log.Detail("Anonymizing users and usermeta")

	// 1. Update customer users in users table
	usersQuery := fmt.Sprintf(`
		UPDATE %susers u
		INNER JOIN %susermeta um ON u.ID = um.user_id AND um.meta_key = '%scapabilities'
		SET 
			u.user_email = CONCAT('customer_', u.ID, '@example.com'), 
			u.user_login = CONCAT('customer_', u.ID),
			u.user_nicename = CONCAT('customer_', u.ID),
			u.display_name = CONCAT('Customer ', u.ID)
		WHERE um.meta_value LIKE '%%"customer"%%'
	`, prefix, prefix, prefix)

	if _, err := db.Exec(usersQuery); err != nil {
		return fmt.Errorf("anonymizing users: %w", err)
	}

	// 2. Update customer usermeta billing/shipping
	metaQuery := fmt.Sprintf(`
		UPDATE %susermeta um
		INNER JOIN %susermeta uc ON um.user_id = uc.user_id AND uc.meta_key = '%scapabilities'
		SET um.meta_value = CASE 
			WHEN um.meta_key IN ('first_name', 'billing_first_name', 'shipping_first_name') THEN 'Customer'
			WHEN um.meta_key IN ('last_name', 'billing_last_name', 'shipping_last_name') THEN CONCAT('LN_', um.user_id)
			WHEN um.meta_key = 'billing_email' THEN CONCAT('customer_', um.user_id, '@example.com')
			WHEN um.meta_key = 'billing_phone' THEN '555-0000'
			WHEN um.meta_key IN ('billing_address_1', 'shipping_address_1') THEN '123 Staging Lane'
			WHEN um.meta_key IN ('billing_address_2', 'shipping_address_2') THEN ''
			WHEN um.meta_key IN ('billing_city', 'shipping_city') THEN 'Staging City'
			WHEN um.meta_key IN ('billing_postcode', 'shipping_postcode') THEN '12345'
			ELSE um.meta_value
		END
		WHERE uc.meta_value LIKE '%%"customer"%%'
		AND um.meta_key IN (
			'first_name', 'billing_first_name', 'shipping_first_name',
			'last_name', 'billing_last_name', 'shipping_last_name',
			'billing_email', 'billing_phone',
			'billing_address_1', 'shipping_address_1',
			'billing_address_2', 'shipping_address_2',
			'billing_city', 'shipping_city',
			'billing_postcode', 'shipping_postcode'
		)
	`, prefix, prefix, prefix)

	if _, err := db.Exec(metaQuery); err != nil {
		return fmt.Errorf("anonymizing usermeta: %w", err)
	}

	// 3. Update WooCommerce order addresses (HPOS) if table exists
	addressTable := prefix + "wc_order_addresses"
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?", addressTable).Scan(&count)
	if err == nil && count > 0 {
		log.Detail("Anonymizing order addresses")
		addressQuery := fmt.Sprintf(`
			UPDATE %swc_order_addresses
			SET 
				first_name = 'Customer',
				last_name = CONCAT('LN_', order_id),
				company = '',
				address_1 = '123 Staging Lane',
				address_2 = '',
				city = 'Staging City',
				postcode = '12345',
				email = CONCAT('order_', order_id, '@example.com'),
				phone = '555-0000'
		`, prefix)
		if _, err := db.Exec(addressQuery); err != nil {
			return fmt.Errorf("anonymizing order addresses: %w", err)
		}
	}

	// 4. wc_orders (HPOS) holds PII not in wc_order_addresses: order email,
	// customer IP/user-agent, and the free-text customer note.
	ordersTable := prefix + "wc_orders"
	var ordersCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?", ordersTable).Scan(&ordersCount); err == nil && ordersCount > 0 {
		log.Detail("Anonymizing order records")
		ordersQuery := fmt.Sprintf(`
			UPDATE %swc_orders
			SET
				billing_email = CONCAT('order_', id, '@example.com'),
				ip_address = '',
				user_agent = '',
				customer_note = ''
		`, prefix)
		if _, err := db.Exec(ordersQuery); err != nil {
			return fmt.Errorf("anonymizing orders: %w", err)
		}
	}

	// 5. Scrub order notes (comments): clear author email/IP, and replace the
	// body of customer notes.
	log.Detail("Anonymizing order notes")
	notesQuery := fmt.Sprintf(`
		UPDATE %scomments
		SET comment_author_email = '', comment_author_IP = ''
		WHERE comment_type = 'order_note'
	`, prefix)
	if _, err := db.Exec(notesQuery); err != nil {
		return fmt.Errorf("anonymizing order notes: %w", err)
	}

	customerNoteQuery := fmt.Sprintf(`
		UPDATE %scomments c
		INNER JOIN %scommentmeta cm
			ON c.comment_ID = cm.comment_id AND cm.meta_key = 'is_customer_note'
		SET c.comment_content = '[customer note removed]'
		WHERE c.comment_type = 'order_note' AND cm.meta_value = '1'
	`, prefix, prefix)
	if _, err := db.Exec(customerNoteQuery); err != nil {
		return fmt.Errorf("anonymizing customer notes: %w", err)
	}

	return nil
}
