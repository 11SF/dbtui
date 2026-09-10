// Package postgres implements dbtui's SQLStore capability interface against
// a real Postgres database, via pgx.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"dbtui/internal/config"
	"dbtui/internal/db"
)

func init() {
	db.RegisterDriver(config.Postgres, func(ctx context.Context, conn config.Connection, password string) (db.Client, error) {
		return NewClient(ctx, conn, password)
	})
}

// Client implements db.SQLStore for Postgres.
type Client struct {
	pool    *pgxpool.Pool
	timeout time.Duration
}

var _ db.SQLStore = (*Client)(nil)

func dsn(conn config.Connection, password string) string {
	dbname := conn.DBName
	if dbname == "" {
		dbname = "postgres"
	}
	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(conn.User, password),
		Host:   fmt.Sprintf("%s:%d", conn.Host, conn.Port),
		Path:   "/" + dbname,
	}
	return u.String()
}

// NewClient connects to Postgres and verifies reachability with a Ping.
func NewClient(ctx context.Context, conn config.Connection, password string) (*Client, error) {
	pool, err := pgxpool.New(ctx, dsn(conn, password))
	if err != nil {
		return nil, db.WrapConnError(fmt.Sprintf("postgres: parse config for %s:%d", conn.Host, conn.Port), err, password)
	}

	pingCtx, cancel := context.WithTimeout(ctx, conn.QueryTimeout())
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, &db.TimeoutError{Op: fmt.Sprintf("postgres: connect %s:%d", conn.Host, conn.Port), Err: db.ErrTunnelTimeout}
		}
		return nil, db.WrapConnError(fmt.Sprintf("postgres: connect %s:%d", conn.Host, conn.Port), err, password)
	}

	return &Client{pool: pool, timeout: conn.QueryTimeout()}, nil
}

func (c *Client) Kind() config.DBType { return config.Postgres }

func (c *Client) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return c.pool.Ping(ctx)
}

func (c *Client) Close() error {
	c.pool.Close()
	return nil
}

func (c *Client) Query(ctx context.Context, sql string) (*db.QueryResult, error) {
	effectiveSQL, capped := db.ApplyAutoLimit(sql)

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	start := time.Now()
	rows, err := c.pool.Query(ctx, effectiveSQL)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, &db.TimeoutError{Op: "postgres: query", Err: db.ErrQueryTimeout}
		}
		return nil, &db.QueryError{Query: effectiveSQL, Err: err}
	}
	defer rows.Close()

	fields := rows.FieldDescriptions()
	columns := make([]string, len(fields))
	for i, f := range fields {
		columns[i] = string(f.Name)
	}

	var resultRows [][]any
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return nil, &db.TimeoutError{Op: "postgres: query", Err: db.ErrQueryTimeout}
			}
			return nil, &db.QueryError{Query: effectiveSQL, Err: err}
		}
		formatted := make([]any, len(vals))
		for i, v := range vals {
			formatted[i] = db.FormatValue(v)
		}
		resultRows = append(resultRows, formatted)
	}
	if err := rows.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, &db.TimeoutError{Op: "postgres: query", Err: db.ErrQueryTimeout}
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

	rows, err := c.pool.Query(ctx, `
		SELECT schema_name FROM information_schema.schemata
		WHERE schema_name NOT IN ('pg_catalog', 'information_schema')
		  AND schema_name NOT LIKE 'pg_toast%'
		  AND schema_name NOT LIKE 'pg_temp%'
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

	rows, err := c.pool.Query(ctx, `
		SELECT table_name, table_type FROM information_schema.tables
		WHERE table_schema = $1
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

	// A nil Go slice marshals to JSON `null`, not `[]` — a table with no
	// foreign keys (the common case) would otherwise hand the frontend a
	// null it has to know to guard against. Normalize once here rather
	// than in every describe* helper.
	if cols == nil {
		cols = []db.ColumnInfo{}
	}
	if pks == nil {
		pks = []string{}
	}
	if fks == nil {
		fks = []db.ForeignKeyInfo{}
	}
	if idx == nil {
		idx = []db.IndexInfo{}
	}

	return &db.TableDescription{
		Columns:     cols,
		PrimaryKeys: pks,
		ForeignKeys: fks,
		Indexes:     idx,
	}, nil
}

func (c *Client) describeColumns(ctx context.Context, schema, table string) ([]db.ColumnInfo, error) {
	rows, err := c.pool.Query(ctx, `
		SELECT column_name, data_type, is_nullable, COALESCE(column_default, '')
		FROM information_schema.columns
		WHERE table_schema = $1 AND table_name = $2
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
	rows, err := c.pool.Query(ctx, `
		SELECT kcu.column_name
		FROM information_schema.table_constraints tc
		JOIN information_schema.key_column_usage kcu
		  ON tc.constraint_name = kcu.constraint_name AND tc.table_schema = kcu.table_schema
		WHERE tc.table_schema = $1 AND tc.table_name = $2 AND tc.constraint_type = $3
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
	rows, err := c.pool.Query(ctx, `
		SELECT
			kcu.column_name,
			ccu.table_schema AS ref_schema,
			ccu.table_name AS ref_table,
			ccu.column_name AS ref_column,
			tc.constraint_name
		FROM information_schema.table_constraints tc
		JOIN information_schema.key_column_usage kcu
		  ON tc.constraint_name = kcu.constraint_name AND tc.table_schema = kcu.table_schema
		JOIN information_schema.constraint_column_usage ccu
		  ON tc.constraint_name = ccu.constraint_name AND tc.table_schema = ccu.table_schema
		WHERE tc.table_schema = $1 AND tc.table_name = $2 AND tc.constraint_type = 'FOREIGN KEY'
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
	rows, err := c.pool.Query(ctx, `
		SELECT ic.relname AS index_name, i.indisunique AS is_unique, a.attname AS column_name
		FROM pg_index i
		JOIN pg_class ic ON ic.oid = i.indexrelid
		JOIN pg_class tc2 ON tc2.oid = i.indrelid
		JOIN pg_namespace n ON n.oid = tc2.relnamespace
		JOIN unnest(i.indkey) WITH ORDINALITY AS k(attnum, ord) ON true
		JOIN pg_attribute a ON a.attrelid = tc2.oid AND a.attnum = k.attnum
		WHERE n.nspname = $1 AND tc2.relname = $2
		ORDER BY ic.relname, k.ord`, schema, table)
	if err != nil {
		return nil, &db.QueryError{Err: err}
	}
	defer rows.Close()

	order := []string{}
	byName := map[string]*db.IndexInfo{}
	for rows.Next() {
		var name, col string
		var unique bool
		if err := rows.Scan(&name, &unique, &col); err != nil {
			return nil, &db.QueryError{Err: err}
		}
		info, ok := byName[name]
		if !ok {
			info = &db.IndexInfo{Name: name, Unique: unique}
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
