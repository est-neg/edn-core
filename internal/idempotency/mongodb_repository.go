package idempotency

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/villenneve/vil-core/internal/platform/config"
)

type mongoRepository struct{ coll *mongo.Collection }

// NewMongoRepository returns a Repository backed by MongoDB.
func NewMongoRepository(client *mongo.Client, cfg config.MongoConfig) Repository {
	return &mongoRepository{
		coll: client.Database(cfg.Database).Collection(cfg.CollectionIdempotencyKeys),
	}
}

func (r *mongoRepository) Reserve(ctx context.Context, key Key) error {
	_, err := r.coll.InsertOne(ctx, key)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return fmt.Errorf("reserve idempotency key: %w", ErrDuplicate)
		}
		return fmt.Errorf("reserve idempotency key: %w", err)
	}
	return nil
}

func (r *mongoRepository) FindByTenantOpKey(ctx context.Context, tenantID, operation, idempotencyKey string) (*Key, error) {
	filter := bson.D{
		{Key: "tenant_id", Value: tenantID},
		{Key: "operation", Value: operation},
		{Key: "idempotency_key", Value: idempotencyKey},
	}
	var key Key
	err := r.coll.FindOne(ctx, filter).Decode(&key)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("find idempotency key: %w", err)
	}
	return &key, nil
}

func (r *mongoRepository) Commit(ctx context.Context, tenantID, operation, idempotencyKey, resourceID, resourceStatus, resourceURL, externalRef string, updatedAt time.Time) error {
	filter := bson.D{
		{Key: "tenant_id", Value: tenantID},
		{Key: "operation", Value: operation},
		{Key: "idempotency_key", Value: idempotencyKey},
	}
	update := bson.D{{Key: "$set", Value: bson.D{
		{Key: "status", Value: StatusCommitted},
		{Key: "resource_id", Value: resourceID},
		{Key: "resource_status", Value: resourceStatus},
		{Key: "resource_url", Value: resourceURL},
		{Key: "external_ref", Value: externalRef},
		{Key: "updated_at", Value: updatedAt},
	}}}
	res, err := r.coll.UpdateOne(ctx, filter, update)
	if err != nil {
		return fmt.Errorf("commit idempotency key: %w", err)
	}
	if res.MatchedCount == 0 {
		return fmt.Errorf("commit idempotency key: %w", ErrNotFound)
	}
	return nil
}

func (r *mongoRepository) Fail(ctx context.Context, tenantID, operation, idempotencyKey string, updatedAt time.Time) error {
	filter := bson.D{
		{Key: "tenant_id", Value: tenantID},
		{Key: "operation", Value: operation},
		{Key: "idempotency_key", Value: idempotencyKey},
	}
	update := bson.D{{Key: "$set", Value: bson.D{
		{Key: "status", Value: StatusFailed},
		{Key: "updated_at", Value: updatedAt},
	}}}
	res, err := r.coll.UpdateOne(ctx, filter, update)
	if err != nil {
		return fmt.Errorf("fail idempotency key: %w", err)
	}
	if res.MatchedCount == 0 {
		return fmt.Errorf("fail idempotency key: %w", ErrNotFound)
	}
	return nil
}
