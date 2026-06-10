package export

import (
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/AdaDigitalAgency/wp-stage-sync/internal/config"
	"github.com/AdaDigitalAgency/wp-stage-sync/internal/progress"
)

// Result holds all generated SQL grouped by category.
type Result struct {
	SchemaOnly []string // CREATE TABLE statements for structure-only tables
	Base       []string // Full schema+data for base tables
	Users      []string // Filtered users + usermeta
	Orders     []string // Filtered HPOS, order lookups, comments
}

// structureOnlyTables are always exported as schema-only.
var structureOnlyTables = []string{
	"woocommerce_sessions",
	"actionscheduler_actions",
	"actionscheduler_claims",
	"actionscheduler_groups",
	"actionscheduler_logs",
	"mainwp_child_changes_logs",
	"mainwp_child_changes_meta",
}

// customOrderTables are filtered by order ID.
var customOrderTables = map[string]string{
	"wc_orders":                 "id",
	"wc_order_addresses":        "order_id",
	"wc_order_operational_data": "order_id",
	"wc_orders_meta":            "order_id",
	"wc_order_stats":            "order_id",
	"wc_order_product_lookup":   "order_id",
	"wc_order_tax_lookup":       "order_id",
	"wc_order_coupon_lookup":    "order_id",
	"woocommerce_order_items":   "order_id",
}

// customRuleTables are handled with special filtering logic.
var customRuleTables = map[string]bool{
	"woocommerce_order_itemmeta": true,
	"yith_ywpar_points_log":      true,
	"comments":                   true,
	"commentmeta":                true,
	"users":                      true,
	"usermeta":                   true,
}

// DefaultTableMode returns the built-in mode for a table name (without prefix).
func DefaultTableMode(shortName string) config.TableMode {
	for _, t := range structureOnlyTables {
		if t == shortName {
			return config.TableModeStructureOnly
		}
	}
	if _, ok := customOrderTables[shortName]; ok {
		return config.TableModeCustomRule
	}
	if customRuleTables[shortName] {
		return config.TableModeCustomRule
	}
	return config.TableModeStructureAndData
}

// bucket identifies which Result group (and import phase) a table belongs to.
type bucket int

const (
	bucketSchema bucket = iota // schema only, no data
	bucketBase                 // full data
	bucketUsers
	bucketOrders
)

// tableAction is one classified table: which group it lands in, whether to dump
// its data, and the WHERE filter to apply. It is the single description both the
// real export (Run) and the dry-run (BuildPlan) consume.
type tableAction struct {
	name   string
	bucket bucket
	dump   bool   // false = schema only
	skip   bool   // true = ignore entirely (no DDL, no data)
	where  string // data filter, "" = full table
	args   []interface{}
	mode   config.TableMode // for the dry-run display
	filter string           // human description for the dry-run
}

// classification is the result of inspecting the live database: the discovery
// counts plus the ordered list of table actions.
type classification struct {
	hasWoo       bool
	orderCount   int
	orderPref    string
	targetOrders int
	safeUsers    int
	totalTables  int
	actions      []tableAction
}

// classify inspects the live database and produces the ordered set of table
// actions — the one place that decides which tables are filtered, schema-only,
// or full. Run and BuildPlan both walk this list so they can never drift.
func classify(db *sql.DB, prefix string, cfg *config.Config, log progress.Logger) (*classification, error) {
	allTables, err := listTables(db)
	if err != nil {
		return nil, fmt.Errorf("listing tables: %w", err)
	}
	log.Detail(fmt.Sprintf("Found %d tables in live database", len(allTables)))

	tableSet := make(map[string]bool, len(allTables))
	for _, t := range allTables {
		tableSet[t] = true
	}

	c := &classification{
		orderCount:  cfg.OrderCount,
		orderPref:   cfg.OrderPreference,
		totalTables: len(allTables),
	}

	// No wc_orders means no WooCommerce: skip order/user/comment filtering and
	// export every table in full.
	c.hasWoo = tableSet[prefix+"wc_orders"]

	var orderIDs, userIDs []int64
	if c.hasWoo {
		log.Detail(fmt.Sprintf("Querying target orders (%s %d)", cfg.OrderPreference, cfg.OrderCount))
		orderIDs, err = getTargetOrderIDs(db, prefix, cfg.OrderCount, cfg.OrderPreference)
		if err != nil {
			return nil, fmt.Errorf("target orders: %w", err)
		}
		c.targetOrders = len(orderIDs)
		log.Detail(fmt.Sprintf("Found %d orders", len(orderIDs)))

		log.Detail("Resolving safe user set")
		userIDs, err = getSafeUserIDs(db, prefix, orderIDs)
		if err != nil {
			return nil, fmt.Errorf("safe users: %w", err)
		}
		c.safeUsers = len(userIDs)
		log.Detail(fmt.Sprintf("Found %d users to export", len(userIDs)))
	} else {
		log.Detail("WooCommerce not detected — full export (no order/user filtering)")
	}

	handled := make(map[string]bool)
	add := func(a tableAction) {
		c.actions = append(c.actions, a)
		handled[a.name] = true
	}

	// Structure-only tables (Action Scheduler, sessions, etc.).
	for _, t := range structureOnlyTables {
		full := prefix + t
		if !tableSet[full] {
			continue
		}
		add(tableAction{name: full, bucket: bucketSchema, mode: config.TableModeStructureOnly, filter: "schema only (no rows)"})
	}

	// Order- and user-scoped tables (WooCommerce only). On non-WooCommerce sites
	// these stay unhandled and the base loop exports them in full.
	if c.hasWoo {
		// HPOS & order tables, filtered by order ID.
		for t, col := range customOrderTables {
			full := prefix + t
			if !tableSet[full] {
				continue
			}
			add(tableAction{
				name: full, bucket: bucketOrders, dump: true, mode: config.TableModeCustomRule,
				where: filterIn(col, orderIDs), args: int64sToArgs(orderIDs),
				filter: fmt.Sprintf("%s IN (%d target orders)", col, len(orderIDs)),
			})
		}

		// woocommerce_order_itemmeta, filtered by order_item_id.
		if itemmeta := prefix + "woocommerce_order_itemmeta"; tableSet[itemmeta] {
			log.Detail("Resolving order item IDs")
			orderItemIDs, err := getOrderItemIDs(db, prefix, orderIDs)
			if err != nil {
				return nil, fmt.Errorf("order item IDs: %w", err)
			}
			add(tableAction{
				name: itemmeta, bucket: bucketOrders, dump: true, mode: config.TableModeCustomRule,
				where: filterIn("order_item_id", orderItemIDs), args: int64sToArgs(orderItemIDs),
				filter: fmt.Sprintf("order_item_id IN (%d items)", len(orderItemIDs)),
			})
		}

		// Comments/reviews in full, plus order notes for the target orders.
		commentWhere, commentArgs := commentFilter(orderIDs)
		commentsTable := prefix + "comments"
		add(tableAction{
			name: commentsTable, bucket: bucketOrders, dump: true, mode: config.TableModeCustomRule,
			where: commentWhere, args: commentArgs,
			filter: "post comments/reviews + order notes for target orders",
		})

		// commentmeta follows the same comment set (subquery, no giant IN list).
		add(tableAction{
			name: prefix + "commentmeta", bucket: bucketOrders, dump: true, mode: config.TableModeCustomRule,
			where:  fmt.Sprintf("comment_id IN (SELECT comment_ID FROM `%s` WHERE %s)", commentsTable, commentWhere),
			args:   commentArgs,
			filter: "meta for the above comments",
		})

		// Users & usermeta, filtered by the safe user set.
		add(tableAction{
			name: prefix + "users", bucket: bucketUsers, dump: true, mode: config.TableModeCustomRule,
			where: filterIn("ID", userIDs), args: int64sToArgs(userIDs),
			filter: fmt.Sprintf("ID IN (%d safe users)", len(userIDs)),
		})
		add(tableAction{
			name: prefix + "usermeta", bucket: bucketUsers, dump: true, mode: config.TableModeCustomRule,
			where: filterIn("user_id", userIDs), args: int64sToArgs(userIDs),
			filter: fmt.Sprintf("user_id IN (%d safe users)", len(userIDs)),
		})

		// YITH points log, filtered by order OR user.
		if yith := prefix + "yith_ywpar_points_log"; tableSet[yith] {
			where, args := orderOrUserFilter(orderIDs, userIDs)
			add(tableAction{
				name: yith, bucket: bucketOrders, dump: true, mode: config.TableModeCustomRule,
				where: where, args: args,
				filter: fmt.Sprintf("order_id (%d orders) OR user_id (%d users)", len(orderIDs), len(userIDs)),
			})
		}
	}

	// Base tables — everything else, per configured mode.
	for _, t := range allTables {
		if handled[t] {
			continue
		}
		switch getTableMode(cfg, t, prefix) {
		case config.TableModeIgnore:
			add(tableAction{name: t, bucket: bucketBase, skip: true, mode: config.TableModeIgnore, filter: "ignored (skipped)"})
		case config.TableModeStructureOnly:
			add(tableAction{name: t, bucket: bucketSchema, mode: config.TableModeStructureOnly, filter: "schema only (no rows)"})
		default:
			// StructureAndData, or an unhandled custom_rule table — full export
			// rather than silently dropping it.
			add(tableAction{name: t, bucket: bucketBase, dump: true, mode: config.TableModeStructureAndData})
		}
	}

	return c, nil
}

// filterIn builds a "`col` IN (?, ?, …)" clause, or "0" (matches nothing) when
// there are no ids — preserving the "schema present, no data" result for empty sets.
func filterIn(col string, ids []int64) string {
	if len(ids) == 0 {
		return "0"
	}
	return fmt.Sprintf("`%s` IN (%s)", col, makeInPlaceholders(len(ids)))
}

// orderOrUserFilter builds "`order_id` IN (…) OR `user_id` IN (…)" from whichever
// id sets are non-empty, or "0" when both are empty.
func orderOrUserFilter(orderIDs, userIDs []int64) (string, []interface{}) {
	var conds []string
	var args []interface{}
	if len(orderIDs) > 0 {
		conds = append(conds, fmt.Sprintf("`order_id` IN (%s)", makeInPlaceholders(len(orderIDs))))
		args = append(args, int64sToArgs(orderIDs)...)
	}
	if len(userIDs) > 0 {
		conds = append(conds, fmt.Sprintf("`user_id` IN (%s)", makeInPlaceholders(len(userIDs))))
		args = append(args, int64sToArgs(userIDs)...)
	}
	if len(conds) == 0 {
		return "0", nil
	}
	return strings.Join(conds, " OR "), args
}

func (res *Result) append(b bucket, stmts ...string) {
	switch b {
	case bucketSchema:
		res.SchemaOnly = append(res.SchemaOnly, stmts...)
	case bucketUsers:
		res.Users = append(res.Users, stmts...)
	case bucketOrders:
		res.Orders = append(res.Orders, stmts...)
	default:
		res.Base = append(res.Base, stmts...)
	}
}

func Run(liveDB *sql.DB, prefix string, cfg *config.Config, log progress.Logger) (*Result, error) {
	c, err := classify(liveDB, prefix, cfg, log)
	if err != nil {
		return nil, err
	}

	res := &Result{}
	for i, a := range c.actions {
		if a.skip {
			log.Detail(fmt.Sprintf("Skipping: %s", a.name))
			log.Progress(i+1, c.totalTables)
			continue
		}

		ddl, err := getCreateTable(liveDB, a.name)
		if err != nil {
			return nil, fmt.Errorf("schema for %s: %w", a.name, err)
		}
		res.append(a.bucket, ddl)

		if a.dump {
			log.Detail(fmt.Sprintf("Dumping: %s", a.name))
			inserts, err := dumpWhere(liveDB, a.name, a.where, a.args)
			if err != nil {
				return nil, fmt.Errorf("data for %s: %w", a.name, err)
			}
			res.append(a.bucket, inserts...)
		} else {
			log.Detail(fmt.Sprintf("Schema-only: %s", a.name))
		}
		log.Progress(i+1, c.totalTables)
	}

	return res, nil
}

func getTableMode(cfg *config.Config, table, prefix string) config.TableMode {
	if mode, ok := cfg.TableModes[table]; ok {
		return mode
	}
	// Strip prefix for lookup
	short := strings.TrimPrefix(table, prefix)
	if mode, ok := cfg.TableModes[short]; ok {
		return mode
	}
	return config.TableModeStructureAndData
}

func getTargetOrderIDs(db *sql.DB, prefix string, count int, pref string) ([]int64, error) {
	order := "DESC"
	if pref == "first" {
		order = "ASC"
	}
	query := fmt.Sprintf("SELECT id FROM %swc_orders ORDER BY id %s LIMIT ?", prefix, order)
	rows, err := db.Query(query, count)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func getSafeUserIDs(db *sql.DB, prefix string, orderIDs []int64) ([]int64, error) {
	userSet := make(map[int64]bool)

	// Set A: all non-customer users
	query := fmt.Sprintf(`
		SELECT DISTINCT u.ID FROM %susers u
		INNER JOIN %susermeta um ON u.ID = um.user_id
		WHERE um.meta_key = '%scapabilities'
		AND um.meta_value NOT LIKE '%%\"customer\"%%'
	`, prefix, prefix, prefix)
	rows, err := db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("non-customer users: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		userSet[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Also get users with multiple roles (one of which might be customer)
	queryMulti := fmt.Sprintf(`
		SELECT DISTINCT u.ID FROM %susers u
		INNER JOIN %susermeta um ON u.ID = um.user_id
		WHERE um.meta_key = '%scapabilities'
		AND um.meta_value LIKE '%%\"customer\"%%'
		AND um.meta_value LIKE '%%"%%"%%"%%"%%'
	`, prefix, prefix, prefix)
	rows2, err := db.Query(queryMulti)
	if err == nil {
		defer rows2.Close()
		for rows2.Next() {
			var id int64
			if err := rows2.Scan(&id); err != nil {
				return nil, err
			}
			userSet[id] = true
		}
	}

	// Set B: customers linked to target orders
	if len(orderIDs) > 0 {
		placeholders := makeInPlaceholders(len(orderIDs))
		query := fmt.Sprintf(
			"SELECT DISTINCT customer_id FROM %swc_orders WHERE id IN (%s) AND customer_id > 0",
			prefix, placeholders,
		)
		args := int64sToArgs(orderIDs)
		rows, err := db.Query(query, args...)
		if err != nil {
			return nil, fmt.Errorf("order customers: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				return nil, err
			}
			userSet[id] = true
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	ids := make([]int64, 0, len(userSet))
	for id := range userSet {
		ids = append(ids, id)
	}
	return ids, nil
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

func getCreateTable(db *sql.DB, table string) (string, error) {
	var name, ddl string
	err := db.QueryRow(fmt.Sprintf("SHOW CREATE TABLE `%s`", table)).Scan(&name, &ddl)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("DROP TABLE IF EXISTS `%s`;\n%s;\n", table, ddl), nil
}

// selectExpr returns the column list to SELECT for a table, excluding generated
// (virtual/stored) columns — those can't be inserted into. Falls back to "*" if
// the columns can't be determined (e.g. on a server without generated-column
// support, which also can't have any).
func selectExpr(db *sql.DB, table string) string {
	rows, err := db.Query(`
		SELECT COLUMN_NAME FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?
		AND (GENERATION_EXPRESSION IS NULL OR GENERATION_EXPRESSION = '')
		ORDER BY ORDINAL_POSITION`, table)
	if err != nil {
		return "*"
	}
	defer rows.Close()

	var cols []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return "*"
		}
		cols = append(cols, "`"+c+"`")
	}
	if rows.Err() != nil || len(cols) == 0 {
		return "*"
	}
	return strings.Join(cols, ",")
}

// dumpWhere dumps a table's rows (non-generated columns only), optionally
// filtered by a WHERE clause; an empty where dumps the whole table.
func dumpWhere(db *sql.DB, table, where string, args []interface{}) ([]string, error) {
	query := fmt.Sprintf("SELECT %s FROM `%s`", selectExpr(db, table), table)
	if where != "" {
		query += " WHERE " + where
	}
	return dumpRows(db, table, query, args)
}

func dumpRows(db *sql.DB, table, query string, args []interface{}) ([]string, error) {
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	const maxBatchBytes = 8 * 1024 * 1024 // 8MB per INSERT statement

	var stmts []string
	batch := make([]string, 0, 500)
	batchBytes := 0

	for rows.Next() {
		values := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}

		vals := make([]string, len(values))
		for i, v := range values {
			vals[i] = formatValue(v)
		}
		row := "(" + strings.Join(vals, ",") + ")"
		batch = append(batch, row)
		batchBytes += len(row)

		if batchBytes >= maxBatchBytes {
			stmts = append(stmts, buildInsert(table, cols, batch))
			batch = batch[:0]
			batchBytes = 0
		}
	}
	if len(batch) > 0 {
		stmts = append(stmts, buildInsert(table, cols, batch))
	}
	return stmts, rows.Err()
}

func buildInsert(table string, cols []string, rows []string) string {
	quotedCols := make([]string, len(cols))
	for i, c := range cols {
		quotedCols[i] = "`" + c + "`"
	}
	return fmt.Sprintf("INSERT INTO `%s` (%s) VALUES\n%s;\n",
		table, strings.Join(quotedCols, ","), strings.Join(rows, ",\n"))
}

func formatValue(v interface{}) string {
	if v == nil {
		return "NULL"
	}
	switch val := v.(type) {
	case []byte:
		// Hex-encode true binary (blobs) as 0x… so NUL/Ctrl-Z bytes and
		// NO_BACKSLASH_ESCAPES mode can't corrupt it; keep text readable.
		if utf8.Valid(val) {
			return quoteString(string(val))
		}
		return "0x" + hex.EncodeToString(val)
	case string:
		return quoteString(val)
	case int64:
		return fmt.Sprintf("%d", val)
	case float64:
		return fmt.Sprintf("%g", val)
	default:
		return fmt.Sprintf("'%v'", val)
	}
}

func quoteString(s string) string {
	return "'" + strings.ReplaceAll(strings.ReplaceAll(s, "\\", "\\\\"), "'", "\\'") + "'"
}

// commentFilter selects all post comments and product reviews (their posts are
// synced in full) plus order notes for the target orders only.
func commentFilter(orderIDs []int64) (string, []interface{}) {
	where := "comment_type <> 'order_note'"
	if len(orderIDs) == 0 {
		return where, nil
	}
	where = fmt.Sprintf("%s OR comment_post_ID IN (%s)", where, makeInPlaceholders(len(orderIDs)))
	return where, int64sToArgs(orderIDs)
}

func makeInPlaceholders(n int) string {
	if n == 0 {
		return ""
	}
	return strings.TrimRight(strings.Repeat("?,", n), ",")
}

func int64sToArgs(ids []int64) []interface{} {
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return args
}

func getOrderItemIDs(db *sql.DB, prefix string, orderIDs []int64) ([]int64, error) {
	if len(orderIDs) == 0 {
		return nil, nil
	}
	placeholders := makeInPlaceholders(len(orderIDs))
	query := fmt.Sprintf(
		"SELECT order_item_id FROM `%swoocommerce_order_items` WHERE order_id IN (%s)",
		prefix, placeholders,
	)
	args := int64sToArgs(orderIDs)
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
