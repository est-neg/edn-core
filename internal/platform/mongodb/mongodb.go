package mongodb

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/villenneve/vil-core/internal/platform/config"
)

// New opens a MongoDB connection, performs a ping, and returns the client.
// The caller owns the client lifecycle and must call Disconnect when done.
func New(ctx context.Context, cfg config.MongoConfig) (*mongo.Client, error) {
	timeout := time.Duration(cfg.ConnectTimeoutSec) * time.Second
	if timeout == 0 {
		timeout = 10 * time.Second
	}

	connectCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	opts := options.Client().ApplyURI(cfg.URI)
	client, err := mongo.Connect(connectCtx, opts)
	if err != nil {
		return nil, fmt.Errorf("connect mongodb: %w", err)
	}

	pingCtx, pingCancel := context.WithTimeout(ctx, timeout)
	defer pingCancel()

	if err := client.Ping(pingCtx, nil); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("ping mongodb: %w", err)
	}

	return client, nil
}

// BootstrapLeadsStorage materializes the configured database and collection,
// then ensures the required indexes exist. Safe to call on every startup.
func BootstrapLeadsStorage(ctx context.Context, client *mongo.Client, cfg config.MongoConfig) error {
	db := client.Database(cfg.Database)

	if err := ensureCollection(ctx, db, cfg.CollectionLeads); err != nil {
		return fmt.Errorf("bootstrap leads collection: %w", err)
	}

	if err := EnsureLeadsIndexes(ctx, client, cfg); err != nil {
		return err
	}

	return nil
}

// EnsureLeadsIndexes creates the required indexes for the leads collection
// idempotently. Safe to call on every startup.
func EnsureLeadsIndexes(ctx context.Context, client *mongo.Client, cfg config.MongoConfig) error {
	coll := client.Database(cfg.Database).Collection(cfg.CollectionLeads)

	// Drop idx_dedup if it exists with a stale key set (e.g. from a previous schema).
	// DropOne ignores "index not found" so this is safe on a fresh database.
	if _, err := coll.Indexes().DropOne(ctx, "idx_dedup"); err != nil {
		var cmdErr mongo.CommandError
		if !errors.As(err, &cmdErr) || (cmdErr.Code != 27 && cmdErr.Name != "IndexNotFound") {
			return fmt.Errorf("drop stale idx_dedup: %w", err)
		}
	}

	indexes := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "id", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("idx_id_unique"),
		},
		{
			Keys:    bson.D{{Key: "dedup_key", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("idx_dedup_key_unique"),
		},
		{
			Keys:    bson.D{{Key: "email", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("idx_email_unique"),
		},
		{
			Keys:    bson.D{{Key: "phone", Value: 1}},
			Options: options.Index().SetUnique(true).SetSparse(true).SetName("idx_phone_unique"),
		},
		{
			Keys: bson.D{
				{Key: "status", Value: 1},
				{Key: "received_at", Value: -1},
			},
			Options: options.Index().SetName("idx_status_received"),
		},
		{
			Keys: bson.D{
				{Key: "notification_status", Value: 1},
				{Key: "received_at", Value: 1},
			},
			Options: options.Index().SetName("idx_notification_status_received"),
		},
	}

	_, err := coll.Indexes().CreateMany(ctx, indexes)
	if err != nil {
		return fmt.Errorf("ensure leads indexes: %w", err)
	}

	return nil
}

func ensureCollection(ctx context.Context, db *mongo.Database, name string) error {
	collections, err := db.ListCollectionNames(ctx, bson.D{{Key: "name", Value: name}})
	if err != nil {
		return fmt.Errorf("list collections: %w", err)
	}
	if len(collections) > 0 {
		return nil
	}

	if err := db.CreateCollection(ctx, name); err != nil {
		var cmdErr mongo.CommandError
		if errors.As(err, &cmdErr) && cmdErr.Code == 48 {
			return nil
		}
		return fmt.Errorf("create collection %q: %w", name, err)
	}

	return nil
}
