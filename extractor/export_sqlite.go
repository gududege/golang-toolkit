package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jedib0t/go-pretty/v6/table"
	_ "modernc.org/sqlite"
)

// runSQLiteExport writes output to SQLite.
func runSQLiteExport(cfg *Config, records []Record, groupNames []string) error {
	tableName := cfg.Export.SQLite.Table
	if tableName == "" {
		tableName = "data"
	}
	override, err := parseOverrideType(cfg.Export.SQLite.OverrideType)
	if err != nil {
		return execErr(err.Error())
	}

	types := inferColumnTypes(records, groupNames, override)

	if cfg.DryRun {
		printSQLitePreview(tableName, groupNames, types, records)
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(cfg.Export.SQLite.File), 0o755); err != nil {
		return execErr(fmt.Sprintf("create dir: %v", err))
	}

	if cfg.Export.Append {
		if err := validateSQLiteSchema(cfg.Export.SQLite.File, tableName, groupNames, types); err != nil {
			return execErr(err.Error())
		}
	}

	if err := writeSQLite(cfg.Export.SQLite.File, tableName, groupNames, types, records, cfg.Export.Append); err != nil {
		return execErr(err.Error())
	}
	return nil
}

// sqlTypeOf maps spec type names to SQLite column types.
func sqlTypeOf(t string) string {
	switch strings.ToLower(t) {
	case "int", "int32", "int64":
		return "INTEGER"
	case "float", "double":
		return "REAL"
	case "bool", "boolean":
		return "BOOLEAN"
	case "date", "datetime":
		return "DATETIME"
	default:
		return "TEXT"
	}
}

// overrideEntry holds the parsed override info for a column.
type overrideEntry struct {
	sqlType string
	layout  string // Go time layout, only set when sqlType is DATETIME
}

// parseOverrideType parses override specifications.
// Formats: "column:type" or "column:datetime:\"layout\"" or "column:datetime:'layout'"
func parseOverrideType(s string) (map[string]overrideEntry, error) {
	m := map[string]overrideEntry{}
	if s == "" {
		return m, nil
	}
	for _, pair := range strings.Split(s, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		// Find the first colon to split column from the rest.
		idx := strings.Index(pair, ":")
		if idx < 0 {
			continue
		}
		col := strings.TrimSpace(pair[:idx])
		rest := strings.TrimSpace(pair[idx+1:])

		parts := strings.SplitN(rest, ":", 2)
		typeName := strings.TrimSpace(parts[0])
		sqlType := sqlTypeOf(typeName)

		entry := overrideEntry{sqlType: sqlType}

		// Check for datetime layout in quotes.
		if len(parts) == 2 {
			layout, err := parseQuotedLayout(strings.TrimSpace(parts[1]))
			if err != nil {
				return nil, fmt.Errorf("override column %q: %w", col, err)
			}
			entry.layout = layout
		}

		m[col] = entry
	}
	return m, nil
}

// parseQuotedLayout extracts a Go time layout from a quoted string and validates it.
func parseQuotedLayout(s string) (string, error) {
	if len(s) < 2 {
		return "", fmt.Errorf("invalid datetime layout %q: must be quoted", s)
	}
	q := s[0]
	if q != '"' && q != '\'' {
		return "", fmt.Errorf("invalid datetime layout %q: must be quoted with single or double quotes", s)
	}
	if s[len(s)-1] != q {
		return "", fmt.Errorf("invalid datetime layout %q: unmatched quote", s)
	}
	layout := s[1 : len(s)-1]
	if layout == "" {
		return "", fmt.Errorf("invalid datetime layout %q: empty layout", s)
	}
	// Validate by parsing the reference time.
	ref := layoutToReference(layout)
	if _, err := time.Parse(layout, ref); err != nil {
		return "", fmt.Errorf("invalid datetime layout %q: %w", layout, err)
	}
	return layout, nil
}

// layoutToReference generates a reference time string matching the given Go layout.
// It uses the standard Go reference time: Mon Jan 2 15:04:05 MST 2006.
func layoutToReference(layout string) string {
	t := time.Date(2006, 1, 2, 15, 4, 5, 0, time.UTC)
	return t.Format(layout)
}

// inferColumnTypes infers each column type using the precedence Override > Auto Detect > TEXT.
func inferColumnTypes(records []Record, groupNames []string, override map[string]overrideEntry) []string {
	types := make([]string, len(groupNames))
	for i, g := range groupNames {
		if e, ok := override[g]; ok {
			types[i] = e.sqlType
			continue
		}
		types[i] = autoDetectType(records, g)
	}
	return types
}

// autoDetectType infers the type from column values; any incompatible value downgrades to TEXT.
func autoDetectType(records []Record, group string) string {
	hasInt, hasFloat, hasBool := true, true, true
	any := false
	for _, rec := range records {
		v := rec[group]
		if v == "" {
			continue
		}
		any = true
		if hasInt && !isInt(v) {
			hasInt = false
		}
		if hasFloat && !isFloat(v) {
			hasFloat = false
		}
		if hasBool && !isBool(v) {
			hasBool = false
		}
	}
	if !any {
		return "TEXT"
	}
	if hasInt {
		return "INTEGER"
	}
	if hasFloat {
		return "REAL"
	}
	if hasBool {
		return "BOOLEAN"
	}
	return "TEXT"
}

func isInt(s string) bool {
	if s == "" {
		return false
	}
	_, err := strconv.ParseInt(s, 10, 64)
	return err == nil
}

func isFloat(s string) bool {
	if s == "" {
		return false
	}
	_, err := strconv.ParseFloat(s, 64)
	return err == nil && strings.ContainsAny(s, ".eE")
}

func isBool(s string) bool {
	return s == "true" || s == "false"
}

// quoteIdent wraps an SQLite identifier in double quotes.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// writeSQLite creates (or appends to) the table and inserts records.
func writeSQLite(file, table string, groupNames, types []string, records []Record, append bool) error {
	db, err := sql.Open("sqlite", file)
	if err != nil {
		return err
	}
	defer db.Close()

	if append {
		exists, err := tableExists(db, table)
		if err != nil {
			return err
		}
		if !exists {
			if err := createTable(db, table, groupNames, types); err != nil {
				return err
			}
		}
	} else {
		if _, err := db.Exec(fmt.Sprintf("DROP TABLE IF EXISTS %s", quoteIdent(table))); err != nil {
			return err
		}
		if err := createTable(db, table, groupNames, types); err != nil {
			return err
		}
	}

	return insertRecords(db, table, groupNames, records)
}

// tableExists reports whether the given table exists in the SQLite database.
func tableExists(db *sql.DB, table string) (bool, error) {
	var name string
	err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// createTable creates the table with the given columns and types.
func createTable(db *sql.DB, table string, groupNames, types []string) error {
	cols := make([]string, len(groupNames))
	for i, g := range groupNames {
		cols[i] = fmt.Sprintf("%s %s", quoteIdent(g), types[i])
	}
	create := fmt.Sprintf("CREATE TABLE %s (%s)", quoteIdent(table), strings.Join(cols, ", "))
	_, err := db.Exec(create)
	return err
}

// insertRecords inserts records into the table using a prepared statement.
func insertRecords(db *sql.DB, table string, groupNames []string, records []Record) error {
	qNames := make([]string, len(groupNames))
	for i, g := range groupNames {
		qNames[i] = quoteIdent(g)
	}
	placeholders := make([]string, len(groupNames))
	for i := range placeholders {
		placeholders[i] = "?"
	}
	insert := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
		quoteIdent(table),
		strings.Join(qNames, ", "),
		strings.Join(placeholders, ", "))

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() // no-op after Commit
	stmt, err := tx.Prepare(insert)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, rec := range records {
		args := make([]any, len(groupNames))
		for i, g := range groupNames {
			args[i] = rec[g]
		}
		if _, err := stmt.Exec(args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// validateSQLiteSchema compares the existing table schema in append mode.
func validateSQLiteSchema(file, table string, groupNames, types []string) error {
	if _, err := os.Stat(file); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	db, err := sql.Open("sqlite", file)
	if err != nil {
		return err
	}
	defer db.Close()
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", quoteIdent(table)))
	if err != nil {
		return err
	}
	defer rows.Close()
	var existing []string
	var existingTypes []string
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return err
		}
		existing = append(existing, name)
		existingTypes = append(existingTypes, strings.ToUpper(ctype))
	}
	if len(existing) == 0 {
		return nil
	}
	if !sameStringSlice(existing, groupNames) {
		return fmt.Errorf("SQLite append: schema mismatch (columns %v vs new %v)", existing, groupNames)
	}
	for i := range types {
		if i < len(existingTypes) && existingTypes[i] != "" && strings.ToUpper(types[i]) != existingTypes[i] {
			return fmt.Errorf("SQLite append: type mismatch on column %s (%s vs %s)", groupNames[i], existingTypes[i], types[i])
		}
	}
	return nil
}

// printSQLitePreview prints the schema and a data preview in table form during dry-run.
func printSQLitePreview(tableName string, groupNames, types []string, records []Record) {
	fmt.Printf("Table: %s\n", tableName)
	fmt.Println("Schema:")
	schema := table.NewWriter()
	schema.AppendHeader(table.Row{"Column", "Type"})
	for i, g := range groupNames {
		schema.AppendRow(table.Row{g, types[i]})
	}
	fmt.Println(schema.Render())

	fmt.Println("Preview (first 10 rows):")
	preview := table.NewWriter()
	header := make(table.Row, len(groupNames))
	for i, g := range groupNames {
		header[i] = g
	}
	preview.AppendHeader(header)
	for i, rec := range records {
		if i >= 10 {
			break
		}
		row := make(table.Row, len(groupNames))
		for j, g := range groupNames {
			row[j] = rec[g]
		}
		preview.AppendRow(row)
	}
	fmt.Println(preview.Render())
}
