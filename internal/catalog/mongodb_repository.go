package catalog

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
		coll: client.Database(cfg.Database).Collection(cfg.CollectionCatalogItems),
	}
}

func (r *mongoRepository) Create(ctx context.Context, item Item) error {
	_, err := r.coll.InsertOne(ctx, item)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return fmt.Errorf("create catalog item: %w", ErrDuplicate)
		}
		return fmt.Errorf("create catalog item: %w", err)
	}
	return nil
}

func (r *mongoRepository) FindByUUID(ctx context.Context, itemUUID string) (*Item, error) {
	var item Item
	err := r.coll.FindOne(ctx, bson.D{{Key: "item_uuid", Value: itemUUID}}).Decode(&item)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("find catalog item by uuid: %w", err)
	}
	return &item, nil
}

func (r *mongoRepository) FindByTenantAndSlug(ctx context.Context, tenantID, slug string) (*Item, error) {
	filter := bson.D{
		{Key: "tenant_id", Value: tenantID},
		{Key: "slug", Value: slug},
	}
	var item Item
	err := r.coll.FindOne(ctx, filter).Decode(&item)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("find catalog item by tenant+slug: %w", err)
	}
	return &item, nil
}

func (r *mongoRepository) ListByTenant(ctx context.Context, tenantID, itemType string) ([]Item, error) {
	filter := bson.D{
		{Key: "tenant_id", Value: tenantID},
		{Key: "active", Value: true},
	}
	if itemType != "" {
		filter = append(filter, bson.E{Key: "type", Value: itemType})
	}
	cursor, err := r.coll.Find(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("list catalog items: %w", err)
	}
	defer cursor.Close(ctx)
	var items []Item
	if err := cursor.All(ctx, &items); err != nil {
		return nil, fmt.Errorf("decode catalog items: %w", err)
	}
	return items, nil
}

func (r *mongoRepository) Deactivate(ctx context.Context, itemUUID string, updatedAt time.Time) error {
	update := bson.D{{Key: "$set", Value: bson.D{
		{Key: "active", Value: false},
		{Key: "updated_at", Value: updatedAt},
	}}}
	res, err := r.coll.UpdateOne(ctx, bson.D{{Key: "item_uuid", Value: itemUUID}}, update)
	if err != nil {
		return fmt.Errorf("deactivate catalog item: %w", err)
	}
	if res.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}
