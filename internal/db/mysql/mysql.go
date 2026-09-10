// Package mysql implements dbtui's SQLStore capability interface against a
// real MySQL database, via database/sql + go-sql-driver/mysql.
package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"

	"dbtui/internal/config"
	"dbtui/internal/db"
)

func init() {
	db.RegisterDriver(config.MySQL, func(ctx context.Context, conn config.Connection, password string) (db.Client, error) {
		return NewClient(ctx, conn, password)
	})
}

// Client implements db.SQLStore for MySQL.
type Client struct {
	conn    *sql.DB
	timeout time.Duration
}

var _ db.SQLStore = (*Client)(nil)

func dsn(conn config.Connection, password string) string {
	cfg := mysqldriver.NewConfig()
	cfg.User = conn.User
	cfg.Passwd = password
	cfg.Net = "tcp"
	cfg.Addr = fmt.Sprintf("%s:%d", conn.Host, conn.Port)
	cfg.DBName = conn.DBName
	cfg.ParseTime = true
	return cfg.FormatDSN()
}

// NewClient connects to MySQL and verifies reachability with a Ping.
func NewClient(ctx context.Context, conn config.Connection, password string) (*Client, error) {
	sqlDB, err := sql.Open("mysql", dsn(conn, password))
	if err != nil {
		return nil, db.WrapConnError(fmt.Sprintf("mysql: open %s:%d", conn.Host, conn.Port), err, password)
	}

	pingCtx, cancel := context.WithTimeout(ctx, conn.QueryTimeout())
	defer cancel()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		sqlDB.Close()
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, &db.TimeoutError{Op: fmt.Sprintf("mysql: connect %s:%d", conn.Host, conn.Port), Err: db.ErrTunnelTimeout}
		}
		return nil, db.WrapConnError(fmt.Sprintf("mysql: connect %s:%d", conn.Host, conn.Port), err, password)
	}

	return &Client{conn: sqlDB, timeout: conn.QueryTimeout()}, nil
}

func (c *Client) Kind() config.DBType { return config.MySQL }

func (c *Client) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return c.conn.PingContext(ctx)
}

func (c *Client) Close() error {
	return c.conn.Close()
}

func (c *Client) Query(ctx context.Context, sqlText string) (*db.QueryResult, error) {
	effectiveSQL, capped := db.ApplyAutoLimit(sqlText)

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	start := time.Now()
	rows, err := c.conn.QueryContext(ctx, effectiveSQL)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, &db.TimeoutError{Op: "mysql: query", Err: db.ErrQueryTimeout}
		}
		return nil, &db.QueryError{Query: effectiveSQL, Err: err}
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return nil, &db.QueryError{Query: effectiveSQL, Err: err}
	}

	var resultRows [][]any
	for rows.Next() {
		raw := make([]any, len(columns))
		ptrs := make([]any, len(columns))
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return nil, &db.TimeoutError{Op: "mysql: query", Err: db.ErrQueryTimeout}
			}
			return nil, &db.QueryError{Query: effectiveSQL, Err: err}
		}
		formatted := make([]any, len(raw))
		for i, v := range raw {
			formatted[i] = db.FormatValue(v)
		}
		resultRows = append(resultRows, formatted)
	}
	if err := rows.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, &db.TimeoutError{Op: "mysql: query", Err: db.ErrQueryTimeout}
		}
		return nil, &db.QueryError{Query: effectiveSQL, Err: err}
	}

	return &db.QueryResult{
		Columns:  columns,
		Rows:     resultRows,
		Duration: time.Since(start),
		Capped:   capped,
	}, nil
}

func (c *Client) ListSchemas(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	rows, err := c.conn.QueryContext(ctx, `
		SELECT schema_name FROM information_schema.schemata
		WHERE schema_name NOT IN ('information_schema', 'mysql', 'performance_schema', 'sys')
		ORDER BY schema_name`)
	if err != nil {
		return nil, &db.QueryError{Err: err}
	}
	defer rows.Close()

	var schemas []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, &db.QueryError{Err: err}
		}
		schemas = append(schemas, s)
	}
	return schemas, rows.Err()
}

func (c *Client) ListTables(ctx context.Context, schema string) ([]db.TableRef, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	rows, err := c.conn.QueryContext(ctx, `
		SELECT table_name, table_type FROM information_schema.tables
		WHERE table_schema = ?
		ORDER BY table_name`, schema)
	if err != nil {
		return nil, &db.QueryError{Err: err}
	}
	defer rows.Close()

	var refs []db.TableRef
	for rows.Next() {
		var name, tableType string
		if err := rows.Scan(&name, &tableType); err != nil {
			return nil, &db.QueryError{Err: err}
		}
		kind := "table"
		if tableType == "VIEW" {
			kind = "view"
		}
		refs = append(refs, db.TableRef{Schema: schema, Name: name, Kind: kind})
	}
	return refs, rows.Err()
}

func (c *Client) DescribeTable(ctx context.Context, schema, table string) (*db.TableDescription, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	cols, err := c.describeColumns(ctx, schema, table)
	if err != nil {
		return nil, err
	}
	pks, err := c.describeConstraintColumns(ctx, schema, table, "PRIMARY KEY")
	if err != nil {
		return nil, err
	}
	fks, err := c.describeForeignKeys(ctx, schema, table)
	if err != nil {
		return nil, err
	}
	idx, err := c.describeIndexes(ctx, schema, table)
	if err != nil {
		return nil, err
	}

	return &db.TableDescription{
		Columns:     cols,
		PrimaryKeys: pks,
		ForeignKeys: fks,
		Indexes:     idx,
	}, nil
}

func (c *Client) describeColumns(ctx context.Context, schema, table string) ([]db.ColumnInfo, error) {
	rows, err := c.conn.QueryContext(ctx, `
		SELECT column_name, data_type, is_nullable, COALESCE(column_default, '')
		FROM information_schema.columns
		WHERE table_schema = ? AND table_name = ?
		ORDER BY ordinal_position`, schema, table)
	if err != nil {
		return nil, &db.QueryError{Err: err}
	}
	defer rows.Close()

	var cols []db.ColumnInfo
	for rows.Next() {
		var name, dataType, nullable, def string
		if err := rows.Scan(&name, &dataType, &nullable, &def); err != nil {
			return nil, &db.QueryError{Err: err}
		}
		cols = append(cols, db.ColumnInfo{
			Name:     name,
			DataType: dataType,
			Nullable: nullable == "YES",
			Default:  def,
		})
	}
	return cols, rows.Err()
}

func (c *Client) describeConstraintColumns(ctx context.Context, schema, table, constraintType string) ([]string, error) {
	rows, err := c.conn.QueryContext(ctx, `
		SELECT kcu.column_name
		FROM information_schema.table_constraints tc
		JOIN information_schema.key_column_usage kcu
		  ON tc.constraint_name = kcu.constraint_name AND tc.table_schema = kcu.table_schema
		WHERE tc.table_schema = ? AND tc.table_name = ? AND tc.constraint_type = ?
		ORDER BY kcu.ordinal_position`, schema, table, constraintType)
	if err != nil {
		return nil, &db.QueryError{Err: err}
	}
	defer rows.Close()

	var cols []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, &db.QueryError{Err: err}
		}
		cols = append(cols, name)
	}
	return cols, rows.Err()
}

func (c *Client) describeForeignKeys(ctx context.Context, schema, table string) ([]db.ForeignKeyInfo, error) {
	rows, err := c.conn.QueryContext(ctx, `
		SELECT
			kcu.column_name,
			kcu.referenced_table_schema,
			kcu.referenced_table_name,
			kcu.referenced_column_name,
			kcu.constraint_name
		FROM information_schema.key_column_usage kcu
		WHERE kcu.table_schema = ? AND kcu.table_name = ? AND kcu.referenced_table_name IS NOT NULL
		ORDER BY kcu.ordinal_position`, schema, table)
	if err != nil {
		return nil, &db.QueryError{Err: err}
	}
	defer rows.Close()

	var fks []db.ForeignKeyInfo
	for rows.Next() {
		var fk db.ForeignKeyInfo
		if err := rows.Scan(&fk.Column, &fk.RefSchema, &fk.RefTable, &fk.RefColumn, &fk.ConstraintName); err != nil {
			return nil, &db.QueryError{Err: err}
		}
		fks = append(fks, fk)
	}
	return fks, rows.Err()
}

func (c *Client) describeIndexes(ctx context.Context, schema, table string) ([]db.IndexInfo, error) {
	rows, err := c.conn.QueryContext(ctx, `
		SELECT index_name, non_unique, column_name
		FROM information_schema.statistics
		WHERE table_schema = ? AND table_name = ?
		ORDER BY index_name, seq_in_index`, schema, table)
	if err != nil {
		return nil, &db.QueryError{Err: err}
	}
	defer rows.Close()

	order := []string{}
	byName := map[string]*db.IndexInfo{}
	for rows.Next() {
		var name, col string
		var nonUnique int
		if err := rows.Scan(&name, &nonUnique, &col); err != nil {
			return nil, &db.QueryError{Err: err}
		}
		info, ok := byName[name]
		if !ok {
			info = &db.IndexInfo{Name: name, Unique: nonUnique == 0}
			byName[name] = info
			order = append(order, name)
		}
		info.Columns = append(info.Columns, col)
	}
	if err := rows.Err(); err != nil {
		return nil, &db.QueryError{Err: err}
	}

	var out []db.IndexInfo
	for _, name := range order {
		out = append(out, *byName[name])
	}
	return out, nil
}
