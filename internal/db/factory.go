package db

import (
	"context"
	"fmt"
	"sync"

	"dbtui/internal/config"
)

// DriverConstructor builds a Client for a given connection + password.
type DriverConstructor func(ctx context.Context, conn config.Connection, password string) (Client, error)

var (
	registryMu sync.RWMutex
	registry   = map[config.DBType]DriverConstructor{}
)

// RegisterDriver registers a constructor for a DBType. Driver packages
// (postgres, mysql, redis, mongo) call this from an init() func; main.go
// blank-imports each driver package so its init() runs before NewClient is
// ever called.
//
// Why indirection instead of factory.go switching on type and calling
// postgres.NewClient/mysql.NewClient/... directly (as sketched in the spec):
// a driver package necessarily imports "dbtui/internal/db" for the
// capability interfaces (SQLStore etc. reference db.QueryResult and
// friends), so having db/factory.go import the driver packages back would
// be an import cycle (db -> postgres -> db). Registration is the standard
// Go answer to that exact shape — it's how database/sql itself wires up
// drivers.
func RegisterDriver(t config.DBType, ctor DriverConstructor) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[t] = ctor
}

// NewClient dispatches to the registered driver constructor for conn.Type.
func NewClient(ctx context.Context, conn config.Connection, password string) (Client, error) {
	registryMu.RLock()
	ctor, ok := registry[conn.Type]
	registryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unsupported db type: %s", conn.Type)
	}
	return ctor(ctx, conn, password)
}
