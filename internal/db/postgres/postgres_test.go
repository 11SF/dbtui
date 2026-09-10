package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"

	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"dbtui/internal/config"
	"dbtui/internal/db"
)

// startPostgres boots a real Postgres container via testcontainers-go and
// returns a config.Connection + password ready to hand to NewClient. Spec
// §3.4-3.6 require a live Postgres instance; this sandbox has docker and
// network egress available (verified before writing this test), so option
// (a) from the test spec is used instead of t.Skip.
func startPostgres(t *testing.T) (config.Connection, string) {
	t.Helper()
	ctx := context.Background()

	const user = "dbtui"
	const password = "dbtui-test-pw"
	const dbname = "dbtui_test"

	container, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase(dbname),
		tcpostgres.WithUsername(user),
		tcpostgres.WithPassword(password),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() {
		if err := container.Terminate(context.Background()); err != nil {
			t.Logf("terminate postgres container: %v", err)
		}
	})

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("container host: %v", err)
	}
	port, err := container.MappedPort(ctx, "5432/tcp")
	if err != nil {
		t.Fatalf("container port: %v", err)
	}

	conn := config.Connection{
		Name:   "it-postgres",
		Type:   config.Postgres,
		Host:   host,
		Port:   int(port.Num()),
		User:   user,
		DBName: dbname,
	}
	return conn, password
}

func TestPostgres_Query_Success(t *testing.T) {
	conn, password := startPostgres(t)
	ctx := context.Background()

	client, err := NewClient(ctx, conn, password)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer client.Close()

	if _, err := client.Query(ctx, "CREATE TABLE widgets (id serial PRIMARY KEY, name text NOT NULL)"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := client.Query(ctx, "INSERT INTO widgets (name) VALUES ('sprocket'), ('gizmo')"); err != nil {
		t.Fatalf("insert: %v", err)
	}

	res, err := client.Query(ctx, "SELECT id, name FROM widgets ORDER BY id")
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if len(res.Columns) != 2 || res.Columns[0] != "id" || res.Columns[1] != "name" {
		t.Fatalf("Columns = %v, want [id name]", res.Columns)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("Rows = %v, want 2 rows", res.Rows)
	}
	if res.Rows[0][1] != "sprocket" || res.Rows[1][1] != "gizmo" {
		t.Fatalf("Rows = %v, want name column sprocket/gizmo in order", res.Rows)
	}
	if res.Duration <= 0 {
		t.Fatalf("Duration = %v, want > 0", res.Duration)
	}
}

// TestPostgres_ConnError_NoPasswordLeak is the driver-level half of spec
// §7/test spec §8.1: connecting with a wrong password against a real
// Postgres must return an error that does not contain the raw password.
func TestPostgres_ConnError_NoPasswordLeak(t *testing.T) {
	conn, _ := startPostgres(t)
	const wrongPassword = "definitely-not-the-real-password"
	ctx := context.Background()

	_, err := NewClient(ctx, conn, wrongPassword)
	if err == nil {
		t.Fatalf("NewClient() with wrong password error = nil, want an auth error")
	}
	if strings.Contains(err.Error(), wrongPassword) {
		t.Fatalf("NewClient() error leaked the password: %v", err)
	}
}

func TestPostgres_Query_Timeout(t *testing.T) {
	conn, password := startPostgres(t)
	conn.QueryTimeoutSec = 1 // short timeout so pg_sleep(5) reliably trips it
	ctx := context.Background()

	client, err := NewClient(ctx, conn, password)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer client.Close()

	_, err = client.Query(ctx, "SELECT pg_sleep(5)")
	if err == nil {
		t.Fatalf("Query() error = nil, want timeout error")
	}

	var te *db.TimeoutError
	if !errors.As(err, &te) {
		t.Fatalf("Query() error = %v (%T), want *db.TimeoutError", err, err)
	}
	if !errors.Is(err, db.ErrQueryTimeout) {
		t.Fatalf("Query() error does not wrap db.ErrQueryTimeout: %v", err)
	}
}

func TestPostgres_ListSchemas_ListTables_DescribeTable(t *testing.T) {
	conn, password := startPostgres(t)
	ctx := context.Background()

	client, err := NewClient(ctx, conn, password)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer client.Close()

	setup := []string{
		"CREATE SCHEMA shop",
		"CREATE TABLE shop.customers (id serial PRIMARY KEY, email text NOT NULL)",
		"CREATE TABLE shop.orders (id serial PRIMARY KEY, customer_id int NOT NULL REFERENCES shop.customers(id), total numeric DEFAULT 0)",
		"CREATE INDEX idx_orders_customer_id ON shop.orders (customer_id)",
		"CREATE UNIQUE INDEX idx_customers_email ON shop.customers (email)",
	}
	for _, stmt := range setup {
		if _, err := client.Query(ctx, stmt); err != nil {
			t.Fatalf("setup %q: %v", stmt, err)
		}
	}

	schemas, err := client.ListSchemas(ctx)
	if err != nil {
		t.Fatalf("ListSchemas() error = %v", err)
	}
	if !containsStr(schemas, "shop") {
		t.Fatalf("ListSchemas() = %v, want it to contain %q", schemas, "shop")
	}

	tables, err := client.ListTables(ctx, "shop")
	if err != nil {
		t.Fatalf("ListTables() error = %v", err)
	}
	names := map[string]string{}
	for _, tr := range tables {
		names[tr.Name] = tr.Kind
	}
	if names["customers"] != "table" || names["orders"] != "table" {
		t.Fatalf("ListTables(shop) = %v, want customers/orders tables", tables)
	}

	desc, err := client.DescribeTable(ctx, "shop", "orders")
	if err != nil {
		t.Fatalf("DescribeTable() error = %v", err)
	}
	colNames := map[string]db.ColumnInfo{}
	for _, c := range desc.Columns {
		colNames[c.Name] = c
	}
	if _, ok := colNames["customer_id"]; !ok {
		t.Fatalf("DescribeTable(orders).Columns = %v, want customer_id present", desc.Columns)
	}
	if len(desc.PrimaryKeys) != 1 || desc.PrimaryKeys[0] != "id" {
		t.Fatalf("DescribeTable(orders).PrimaryKeys = %v, want [id]", desc.PrimaryKeys)
	}
	if len(desc.ForeignKeys) != 1 || desc.ForeignKeys[0].RefTable != "customers" {
		t.Fatalf("DescribeTable(orders).ForeignKeys = %+v, want 1 FK referencing customers", desc.ForeignKeys)
	}
	foundIdx := false
	for _, idx := range desc.Indexes {
		if idx.Name == "idx_orders_customer_id" {
			foundIdx = true
			if idx.Unique {
				t.Fatalf("idx_orders_customer_id.Unique = true, want false")
			}
		}
	}
	if !foundIdx {
		t.Fatalf("DescribeTable(orders).Indexes = %+v, want idx_orders_customer_id present", desc.Indexes)
	}
}

func containsStr(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
