// Package redis implements dbtui's KVStore capability interface against a
// real Redis server, via go-redis/v9.
package redis

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"dbtui/internal/config"
	"dbtui/internal/db"
)

func init() {
	db.RegisterDriver(config.Redis, func(ctx context.Context, conn config.Connection, password string) (db.Client, error) {
		return NewClient(ctx, conn, password)
	})
}

// maxScanCount is the hard cap on keys returned per ScanKeys page (spec §4.1
// KV result cap: 500/page).
const maxScanCount = 500

// Client implements db.KVStore for Redis.
type Client struct {
	rdb     *goredis.Client
	timeout time.Duration
}

var _ db.KVStore = (*Client)(nil)

func defaultDBIndex(conn config.Connection) int {
	if conn.DBName == "" {
		return 0
	}
	n, err := strconv.Atoi(conn.DBName)
	if err != nil {
		return 0
	}
	return n
}

// NewClient connects to Redis and verifies reachability with a Ping.
func NewClient(ctx context.Context, conn config.Connection, password string) (*Client, error) {
	rdb := goredis.NewClient(&goredis.Options{
		Addr:     fmt.Sprintf("%s:%d", conn.Host, conn.Port),
		Password: password,
		DB:       defaultDBIndex(conn),
	})

	pingCtx, cancel := context.WithTimeout(ctx, conn.QueryTimeout())
	defer cancel()
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		rdb.Close()
		return nil, db.WrapConnError(fmt.Sprintf("redis: connect %s:%d", conn.Host, conn.Port), err, password)
	}

	return &Client{rdb: rdb, timeout: conn.QueryTimeout()}, nil
}

func (c *Client) Kind() config.DBType { return config.Redis }

func (c *Client) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return c.rdb.Ping(ctx).Err()
}

func (c *Client) Close() error {
	return c.rdb.Close()
}

// ListDatabases returns the conventional Redis DB indexes 0-15. Redis has
// no server-side "list databases" call — the index range is a fixed
// convention (databases 0 default) — so this is a static list, not a query.
func (c *Client) ListDatabases(ctx context.Context) ([]int, error) {
	out := make([]int, 16)
	for i := range out {
		out[i] = i
	}
	return out, nil
}

// ScanKeys returns up to count (capped at maxScanCount) keys matching
// pattern in the given db index, using Redis's cursor-based SCAN so the
// full keyspace is never loaded at once.
func (c *Client) ScanKeys(ctx context.Context, dbIndex int, pattern string, cursor uint64, count int) ([]string, uint64, error) {
	if count <= 0 || count > maxScanCount {
		count = maxScanCount
	}
	if pattern == "" {
		pattern = "*"
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	conn := c.rdb.Conn()
	defer conn.Close()
	if err := conn.Do(ctx, "SELECT", dbIndex).Err(); err != nil {
		return nil, 0, &db.QueryError{Err: err}
	}

	keys, next, err := conn.Scan(ctx, cursor, pattern, int64(count)).Result()
	if err != nil {
		return nil, 0, &db.QueryError{Err: err}
	}
	return keys, next, nil
}

func (c *Client) KeyType(ctx context.Context, dbIndex int, key string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	conn := c.rdb.Conn()
	defer conn.Close()
	if err := conn.Do(ctx, "SELECT", dbIndex).Err(); err != nil {
		return "", &db.QueryError{Err: err}
	}
	t, err := conn.Type(ctx, key).Result()
	if err != nil {
		return "", &db.QueryError{Err: err}
	}
	return t, nil
}

// GetValue fetches key's value, dispatching on its Redis type internally.
func (c *Client) GetValue(ctx context.Context, dbIndex int, key string) (*db.KVValue, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	conn := c.rdb.Conn()
	defer conn.Close()
	if err := conn.Do(ctx, "SELECT", dbIndex).Err(); err != nil {
		return nil, &db.QueryError{Err: err}
	}

	kind, err := conn.Type(ctx, key).Result()
	if err != nil {
		return nil, &db.QueryError{Err: err}
	}

	switch kind {
	case "string":
		s, err := conn.Get(ctx, key).Result()
		if err != nil {
			return nil, &db.QueryError{Err: err}
		}
		return &db.KVValue{Type: "string", String: s}, nil
	case "hash":
		h, err := conn.HGetAll(ctx, key).Result()
		if err != nil {
			return nil, &db.QueryError{Err: err}
		}
		return &db.KVValue{Type: "hash", Hash: h}, nil
	case "list":
		l, err := conn.LRange(ctx, key, 0, -1).Result()
		if err != nil {
			return nil, &db.QueryError{Err: err}
		}
		return &db.KVValue{Type: "list", List: l}, nil
	case "set":
		s, err := conn.SMembers(ctx, key).Result()
		if err != nil {
			return nil, &db.QueryError{Err: err}
		}
		return &db.KVValue{Type: "set", Set: s}, nil
	case "zset":
		z, err := conn.ZRangeWithScores(ctx, key, 0, -1).Result()
		if err != nil {
			return nil, &db.QueryError{Err: err}
		}
		members := make([]db.ZMember, len(z))
		for i, m := range z {
			members[i] = db.ZMember{Member: fmt.Sprintf("%v", m.Member), Score: m.Score}
		}
		return &db.KVValue{Type: "zset", ZSet: members}, nil
	default:
		return &db.KVValue{Type: kind}, nil
	}
}

func (c *Client) TTL(ctx context.Context, dbIndex int, key string) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	conn := c.rdb.Conn()
	defer conn.Close()
	if err := conn.Do(ctx, "SELECT", dbIndex).Err(); err != nil {
		return 0, &db.QueryError{Err: err}
	}
	ttl, err := conn.TTL(ctx, key).Result()
	if err != nil {
		return 0, &db.QueryError{Err: err}
	}
	if ttl < 0 {
		return -1, nil
	}
	return ttl, nil
}

// RunCommand executes a raw redis-cli style command line (args already
// tokenized, e.g. strings.Fields(input)).
func (c *Client) RunCommand(ctx context.Context, dbIndex int, args []string) (any, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("redis: empty command")
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	conn := c.rdb.Conn()
	defer conn.Close()
	if err := conn.Do(ctx, "SELECT", dbIndex).Err(); err != nil {
		return nil, &db.QueryError{Err: err}
	}

	cmdArgs := make([]any, len(args))
	for i, a := range args {
		cmdArgs[i] = a
	}
	res, err := conn.Do(ctx, cmdArgs...).Result()
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "timeout") {
			return nil, &db.TimeoutError{Op: "redis: command", Err: db.ErrQueryTimeout}
		}
		return nil, &db.QueryError{Query: strings.Join(args, " "), Err: err}
	}
	return res, nil
}
