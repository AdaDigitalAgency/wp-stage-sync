package export

import (
	"database/sql"
	"fmt"

	"github.com/AdaDigitalAgency/wp-stage-sync/internal/config"
	"github.com/AdaDigitalAgency/wp-stage-sync/internal/progress"
)

// TablePlan describes how a single table would be exported during a dry-run.
type TablePlan struct {
	Name   string           // full table name (with prefix)
	Mode   config.TableMode // effective export mode
	Filter string           // human-readable filter description, "" for full data
	Rows   int64            // rows that would be exported (0 for schema-only/ignored)
}

// Plan summarizes what an export would produce, without dumping any data.
// It is built from the same classify() pass as Run, issuing only read-only
// COUNT queries instead of dumping.
type Plan struct {
	WooCommerce     bool // false → full export, no order/user filtering
	OrderPreference string
	OrderCount      int
	TargetOrders    int
	SafeUsers       int
	TotalTables     int
	Tables          []TablePlan
}

// ExportedRows returns the total number of rows that would be written.
func (p *Plan) ExportedRows() int64 {
	var total int64
	for _, t := range p.Tables {
		total += t.Rows
	}
	return total
}

// BuildPlan runs the shared classify() pass and, instead of dumping, counts the
// rows each table would contribute. It issues no writes.
func BuildPlan(liveDB *sql.DB, prefix string, cfg *config.Config, log progress.Logger) (*Plan, error) {
	c, err := classify(liveDB, prefix, cfg, log)
	if err != nil {
		return nil, err
	}

	p := &Plan{
		WooCommerce:     c.hasWoo,
		OrderPreference: c.orderPref,
		OrderCount:      c.orderCount,
		TargetOrders:    c.targetOrders,
		SafeUsers:       c.safeUsers,
		TotalTables:     c.totalTables,
	}

	for _, a := range c.actions {
		var rows int64
		if a.dump {
			rows, err = countWhere(liveDB, a.name, a.where, a.args)
			if err != nil {
				return nil, fmt.Errorf("counting %s: %w", a.name, err)
			}
		}
		p.Tables = append(p.Tables, TablePlan{Name: a.name, Mode: a.mode, Filter: a.filter, Rows: rows})
	}

	return p, nil
}

// countWhere counts a table's rows, optionally filtered by a WHERE clause; an
// empty where counts the whole table.
func countWhere(db *sql.DB, table, where string, args []interface{}) (int64, error) {
	query := fmt.Sprintf("SELECT COUNT(*) FROM `%s`", table)
	if where != "" {
		query += " WHERE " + where
	}
	var n int64
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}
