// Package mongo implements dbtui's DocumentStore capability interface
// against a real MongoDB server, via mongo-driver/v2.
package mongo

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"dbtui/internal/config"
	"dbtui/internal/db"
)

func init() {
	db.RegisterDriver(config.MongoDB, func(ctx context.Context, conn config.Connection, password string) (db.Client, error) {
		return NewClient(ctx, conn, password)
	})
}

// Client implements db.DocumentStore for MongoDB.
type Client struct {
	client  *mongo.Client
	timeout time.Duration
}

var _ db.DocumentStore = (*Client)(nil)

func uri(conn config.Connection, password string) string {
	authSource := conn.AuthSource
	if authSource == "" {
		authSource = "admin"
	}
	if conn.User == "" {
		return fmt.Sprintf("mongodb://%s:%d/?authSource=%s", conn.Host, conn.Port, authSource)
	}
	return fmt.Sprintf("mongodb://%s:%s@%s:%d/?authSource=%s", conn.User, password, conn.Host, conn.Port, authSource)
}

// NewClient connects to MongoDB and verifies reachability with a Ping.
func NewClient(ctx context.Context, conn config.Connection, password string) (*Client, error) {
	timeout := conn.QueryTimeout()

	connectCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	mc, err := mongo.Connect(options.Client().ApplyURI(uri(conn, password)))
	if err != nil {
		return nil, db.WrapConnError(fmt.Sprintf("mongo: connect %s:%d", conn.Host, conn.Port), err, password)
	}
	if err := mc.Ping(connectCtx, nil); err != nil {
		mc.Disconnect(ctx)
		return nil, db.WrapConnError(fmt.Sprintf("mongo: ping %s:%d", conn.Host, conn.Port), err, password)
	}

	return &Client{client: mc, timeout: timeout}, nil
}

func (c *Client) Kind() config.DBType { return config.MongoDB }

func (c *Client) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return c.client.Ping(ctx, nil)
}

func (c *Client) Close() error {
	return c.client.Disconnect(context.Background())
}

func (c *Client) ListDatabases(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	names, err := c.client.ListDatabaseNames(ctx, bson.D{})
	if err != nil {
		return nil, &db.QueryError{Err: err}
	}
	return names, nil
}

func (c *Client) ListCollections(ctx context.Context, database string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	names, err := c.client.Database(database).ListCollectionNames(ctx, bson.D{})
	if err != nil {
		return nil, &db.QueryError{Err: err}
	}
	return names, nil
}

// Find runs filterJSON (a JSON document, e.g. {"age": {"$gt": 30}}) against
// database.collection, applying db.EffectiveFindLimit to limit.
func (c *Client) Find(ctx context.Context, database, collection, filterJSON string, limit int) (*db.DocResult, error) {
	effectiveLimit := db.EffectiveFindLimit(limit)

	if filterJSON == "" {
		filterJSON = "{}"
	}
	var filter bson.M
	if err := bson.UnmarshalExtJSON([]byte(filterJSON), true, &filter); err != nil {
		return nil, &db.QueryError{Query: filterJSON, Err: fmt.Errorf("invalid filter JSON: %w", err)}
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	start := time.Now()
	cur, err := c.client.Database(database).Collection(collection).Find(ctx, filter, options.Find().SetLimit(int64(effectiveLimit)))
	if err != nil {
		return nil, &db.QueryError{Query: filterJSON, Err: err}
	}
	defer cur.Close(ctx)

	var docs []string
	for cur.Next(ctx) {
		var raw bson.M
		if err := cur.Decode(&raw); err != nil {
			return nil, &db.QueryError{Query: filterJSON, Err: err}
		}
		ext, err := bson.MarshalExtJSON(raw, false, false)
		if err != nil {
			return nil, &db.QueryError{Query: filterJSON, Err: err}
		}
		pretty, err := prettyJSON(ext)
		if err != nil {
			return nil, &db.QueryError{Query: filterJSON, Err: err}
		}
		docs = append(docs, pretty)
	}
	if err := cur.Err(); err != nil {
		return nil, &db.QueryError{Query: filterJSON, Err: err}
	}

	return &db.DocResult{Documents: docs, Duration: time.Since(start)}, nil
}

func prettyJSON(raw []byte) (string, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", err
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func (c *Client) CountDocuments(ctx context.Context, database, collection, filterJSON string) (int64, error) {
	if filterJSON == "" {
		filterJSON = "{}"
	}
	var filter bson.M
	if err := bson.UnmarshalExtJSON([]byte(filterJSON), true, &filter); err != nil {
		return 0, &db.QueryError{Query: filterJSON, Err: fmt.Errorf("invalid filter JSON: %w", err)}
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	n, err := c.client.Database(database).Collection(collection).CountDocuments(ctx, filter)
	if err != nil {
		return 0, &db.QueryError{Query: filterJSON, Err: err}
	}
	return n, nil
}

func (c *Client) IndexInfo(ctx context.Context, database, collection string) ([]db.IndexInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	cur, err := c.client.Database(database).Collection(collection).Indexes().List(ctx)
	if err != nil {
		return nil, &db.QueryError{Err: err}
	}
	defer cur.Close(ctx)

	var out []db.IndexInfo
	for cur.Next(ctx) {
		var raw bson.M
		if err := cur.Decode(&raw); err != nil {
			return nil, &db.QueryError{Err: err}
		}
		info := db.IndexInfo{}
		if name, ok := raw["name"].(string); ok {
			info.Name = name
		}
		if unique, ok := raw["unique"].(bool); ok {
			info.Unique = unique
		}
		if key, ok := raw["key"].(bson.M); ok {
			for k := range key {
				info.Columns = append(info.Columns, k)
			}
		}
		out = append(out, info)
	}
	if err := cur.Err(); err != nil {
		return nil, &db.QueryError{Err: err}
	}
	return out, nil
}
