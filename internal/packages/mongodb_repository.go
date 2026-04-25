package packages

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/villenneve/vil-core/internal/platform/config"
)

type mongoRepository struct{ coll *mongo.Collection }

// NewMongoRepository returns a Repository backed by MongoDB.
func NewMongoRepository(client *mongo.Client, cfg config.MongoConfig) Repository {
	return &mongoRepository{
		coll: client.Database(cfg.Database).Collection(cfg.CollectionPackages),
	}
}

func (r *mongoRepository) Create(ctx context.Context, pkg Package) error {
	_, err := r.coll.InsertOne(ctx, pkg)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return fmt.Errorf("create package: %w", ErrDuplicate)
		}
		return fmt.Errorf("create package: %w", err)
	}
	return nil
}

func (r *mongoRepository) FindByUUID(ctx context.Context, packageUUID string) (*Package, error) {
	var pkg Package
	err := r.coll.FindOne(ctx, bson.D{{Key: "package_uuid", Value: packageUUID}}).Decode(&pkg)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("find package by uuid: %w", err)
	}
	return &pkg, nil
}

func (r *mongoRepository) FindByTenantSlugVersion(ctx context.Context, tenantID, slug string, version int) (*Package, error) {
	filter := bson.D{
		{Key: "tenant_id", Value: tenantID},
		{Key: "slug", Value: slug},
		{Key: "version", Value: version},
	}
	var pkg Package
	err := r.coll.FindOne(ctx, filter).Decode(&pkg)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("find package by tenant+slug+version: %w", err)
	}
	return &pkg, nil
}

func (r *mongoRepository) FindLatestPublishedBySlug(ctx context.Context, tenantID, slug string) (*Package, error) {
	filter := bson.D{
		{Key: "tenant_id", Value: tenantID},
		{Key: "slug", Value: slug},
		{Key: "status", Value: StatusPublished},
	}
	opts := options.FindOne().SetSort(bson.D{{Key: "version", Value: -1}})
	var pkg Package
	err := r.coll.FindOne(ctx, filter, opts).Decode(&pkg)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("find latest published package: %w", err)
	}
	return &pkg, nil
}
