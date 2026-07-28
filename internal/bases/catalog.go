package bases

import (
	"database/sql"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	_ "modernc.org/sqlite"
)

// Catalog is the user's tabular data store (catalog.db): CSVs imported as
// tables, browsable and queryable with read-only SQL. It is a separate
// database from the notes index so user queries can never contend with
// indexing.
type Catalog struct {
	rw *sql.DB // import path only
	ro *sql.DB // all reads; opened mode=ro so writes are impossible
}

func OpenCatalog(dataDir string) (*Catalog, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dataDir, "catalog.db")
	rw, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	rw.SetMaxOpenConns(1)
	if err := rw.Ping(); err != nil { // creates the file
		rw.Close()
		return nil, err
	}
	ro, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(5000)&_pragma=query_only(1)")
	if err != nil {
		rw.Close()
		return nil, err
	}
	ro.SetMaxOpenConns(4)
	return &Catalog{rw: rw, ro: ro}, nil
}

func (c *Catalog) Close() error {
	rerr := c.ro.Close()
	if err := c.rw.Close(); err != nil {
		return err
	}
	return rerr
}

var identRe = regexp.MustCompile(`[^a-zA-Z0-9_]+`)

func sqlIdent(s string) string {
	s = identRe.ReplaceAllString(strings.TrimSpace(s), "_")
	s = strings.Trim(s, "_")
	if s == "" {
		s = "col"
	}
	if s[0] >= '0' && s[0] <= '9' {
		s = "c_" + s
	}
	return s
}

// ImportCSV loads a CSV file into a table (replacing it), with header-derived
// column names. All columns are TEXT — SQLite's dynamic typing still lets
// numeric expressions work in queries.
func (c *Catalog) ImportCSV(csvPath, table string) (rows int, err error) {
	if table == "" {
		table = strings.TrimSuffix(filepath.Base(csvPath), filepath.Ext(csvPath))
	}
	table = sqlIdent(table)

	f, err := os.Open(csvPath)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	header, err := r.Read()
	if err != nil {
		return 0, fmt.Errorf("read header: %w", err)
	}
	cols := make([]string, len(header))
	seen := map[string]bool{}
	for i, h := range header {
		name := sqlIdent(h)
		for seen[name] {
			name += "_2"
		}
		seen[name] = true
		cols[i] = name
	}

	tx, err := c.rw.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DROP TABLE IF EXISTS "` + table + `"`); err != nil {
		return 0, err
	}
	quoted := make([]string, len(cols))
	for i, col := range cols {
		quoted[i] = `"` + col + `" TEXT`
	}
	if _, err := tx.Exec(`CREATE TABLE "` + table + `"(` + strings.Join(quoted, ",") + `)`); err != nil {
		return 0, err
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(cols)), ",")
	stmt, err := tx.Prepare(`INSERT INTO "` + table + `" VALUES(` + placeholders + `)`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return rows, fmt.Errorf("row %d: %w", rows+1, err)
		}
		args := make([]any, len(cols))
		for i := range cols {
			if i < len(rec) {
				args[i] = rec[i]
			} else {
				args[i] = ""
			}
		}
		if _, err := stmt.Exec(args...); err != nil {
			return rows, err
		}
		rows++
	}
	return rows, tx.Commit()
}

// Tables lists catalog tables with row counts.
func (c *Catalog) Tables() (map[string]int, error) {
	rows, err := c.ro.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names = append(names, n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, n := range names {
		var count int
		if err := c.ro.QueryRow(`SELECT COUNT(*) FROM "` + n + `"`).Scan(&count); err == nil {
			out[n] = count
		}
	}
	return out, nil
}

var selectRe = regexp.MustCompile(`(?is)^\s*(select|with)\b`)

// Query runs a read-only SQL query. Defense in depth: the statement must be
// a single SELECT/WITH, and the connection itself is opened mode=ro with
// query_only, so even a clever bypass cannot write.
func (c *Catalog) Query(q string, maxRows int) (cols []string, rows [][]string, err error) {
	if !selectRe.MatchString(q) {
		return nil, nil, fmt.Errorf("only SELECT queries are allowed")
	}
	if strings.Contains(strings.TrimRight(strings.TrimSpace(q), ";"), ";") {
		return nil, nil, fmt.Errorf("multiple statements are not allowed")
	}
	if maxRows <= 0 || maxRows > 2000 {
		maxRows = 500
	}
	res, err := c.ro.Query(q)
	if err != nil {
		return nil, nil, err
	}
	defer res.Close()
	cols, err = res.Columns()
	if err != nil {
		return nil, nil, err
	}
	for res.Next() && len(rows) < maxRows {
		raw := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		if err := res.Scan(ptrs...); err != nil {
			return nil, nil, err
		}
		row := make([]string, len(cols))
		for i, v := range raw {
			switch t := v.(type) {
			case nil:
				row[i] = ""
			case []byte:
				row[i] = string(t)
			default:
				row[i] = fmt.Sprint(t)
			}
		}
		rows = append(rows, row)
	}
	return cols, rows, res.Err()
}
