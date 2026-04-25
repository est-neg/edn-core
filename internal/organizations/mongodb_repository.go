package organizations

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/villenneve/vil-core/internal/platform/config"
)

type mongoRepository struct{ coll *mongo.Collection }

// NewMongoRepository returns a Repository backed by MongoDB.
func NewMongoRepository(client *mongo.Client, cfg config.MongoConfig) Repository {
	return &mongoRepository{
		coll: client.Database(cfg.Database).Collection(cfg.CollectionOrganizations),
	}
}

func (r *mongoRepository) Create(ctx context.Context, org Organization) error {
	_, err := r.coll.InsertOne(ctx, org)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return fmt.Errorf("create organization: %w", ErrDuplicate)
		}
		return fmt.Errorf("create organization: %w", err)
	}
	return nil
}

func (r *mongoRepository) FindByUUID(ctx context.Context, orgUUID string) (*Organization, error) {
	var org Organization
	err := r.coll.FindOne(ctx, bson.D{{Key: "org_uuid", Value: orgUUID}}).Decode(&org)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("find organization by uuid: %w", err)
	}
	return &org, nil
}

func (r *mongoRepository) FindBySlug(ctx context.Context, slug string) (*Organization, error) {
	var org Organization
	err := r.coll.FindOne(ctx, bson.D{{Key: "slug", Value: slug}}).Decode(&org)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("find organization by slug: %w", err)
	}
	return &org, nil
}
