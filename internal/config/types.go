package config

import "time"

// DBType identifies the kind of database a connection targets.
type DBType string

const (
	Postgres DBType = "postgres"
	MySQL    DBType = "mysql"
	Redis    DBType = "redis"
	MongoDB  DBType = "mongodb"
)

// StoreCategory groups DBTypes that share the same capability interface.
type StoreCategory string

const (
	CategorySQL      StoreCategory = "sql"
	CategoryKV       StoreCategory = "kv"
	CategoryDocument StoreCategory = "document"
)

// Category maps a DBType to its StoreCategory. Unknown types return "".
func (t DBType) Category() StoreCategory {
	switch t {
	case Postgres, MySQL:
		return CategorySQL
	case Redis:
		return CategoryKV
	case MongoDB:
		return CategoryDocument
	}
	return ""
}

// TunnelConfig describes a kubectl port-forward used to reach a database.
// Identical shape for every store type — tunneling is orthogonal to store category.
type TunnelConfig struct {
	KubeContext string `yaml:"kube_context"`
	Namespace   string `yaml:"namespace"`
	TargetType  string `yaml:"target_type"` // "pod" | "svc"
	TargetName  string `yaml:"target_name"`
	RemotePort  int    `yaml:"remote_port"`
	LocalPort   int    `yaml:"local_port,omitempty"` // 0 = auto-assign a free port
}

// Connection describes a single saved database connection. The password is
// deliberately NOT a field here — it lives in the OS keyring under key
// "dbtui:" + Name (see internal/secrets). This is a static guard enforced by
// TestSave_NeverWritesPassword: there is no password field to accidentally
// serialize.
type Connection struct {
	Name       string        `yaml:"name"`
	Type       DBType        `yaml:"type"`
	Host       string        `yaml:"host"`                  // "localhost" when using a tunnel
	Port       int           `yaml:"port"`                  // remote db port (5432/3306/6379/27017)
	User       string        `yaml:"user,omitempty"`        // unused for Redis
	DBName     string        `yaml:"dbname,omitempty"`      // SQL: database name; Redis: DB index as string e.g. "0"; Mongo: optional default database
	AuthSource string        `yaml:"auth_source,omitempty"` // Mongo only, defaults to "admin"
	Tunnel     *TunnelConfig `yaml:"tunnel,omitempty"`      // nil = direct connect, no k8s
	Group      string        `yaml:"group,omitempty"`       // for grouping dev/staging/prod

	// QueryTimeoutSec bounds every Query()/Find()/RunCommand() call via
	// context.WithTimeout. 0 means "use the default" (30s) — see
	// QueryTimeout().
	QueryTimeoutSec int `yaml:"query_timeout_sec,omitempty"`

	// ShowAllDatabases, for SQL types (postgres/mysql) only, lets the
	// browser list every database on the server rather than just DBName —
	// DBName is still used as the database dialed first on connect.
	// Meaningless for Redis (DBName is already the numeric index) and
	// MongoDB (DBName is already optional and Find already browses across
	// every database the credentials can see).
	ShowAllDatabases bool `yaml:"show_all_databases,omitempty"`
}

// DefaultQueryTimeoutSec is used when QueryTimeoutSec is unset (0).
const DefaultQueryTimeoutSec = 30

// QueryTimeout returns the configured per-query timeout, falling back to
// DefaultQueryTimeoutSec when unset.
func (c Connection) QueryTimeout() time.Duration {
	sec := c.QueryTimeoutSec
	if sec <= 0 {
		sec = DefaultQueryTimeoutSec
	}
	return time.Duration(sec) * time.Second
}

// ConfigFile is the top-level shape of ~/.config/dbtui/connections.yaml.
type ConfigFile struct {
	Connections []Connection `yaml:"connections"`
}
