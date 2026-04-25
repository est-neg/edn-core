package tenants

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
		coll: client.Database(cfg.Database).Collection(cfg.CollectionTenants),
	}
}

func (r *mongoRepository) Create(ctx context.Context, tenant Tenant) error {
	_, err := r.coll.InsertOne(ctx, tenant)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return fmt.Errorf("create tenant: %w", ErrDuplicate)
		}
		return fmt.Errorf("create tenant: %w", err)
	}
	return nil
}

func (r *mongoRepository) FindByUUID(ctx context.Context, tenantUUID string) (*Tenant, error) {
	var t Tenant
	err := r.coll.FindOne(ctx, bson.D{{Key: "tenant_uuid", Value: tenantUUID}}).Decode(&t)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("find tenant by uuid: %w", err)
	}
	return &t, nil
}

func (r *mongoRepository) FindByOrgAndSlug(ctx context.Context, organizationID, slug string) (*Tenant, error) {
	filter := bson.D{
		{Key: "organization_id", Value: organizationID},
		{Key: "slug", Value: slug},
	}
	var t Tenant
	err := r.coll.FindOne(ctx, filter).Decode(&t)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("find tenant by org+slug: %w", err)
	}
	return &t, nil
}

func (r *mongoRepository) ListByOrg(ctx context.Context, organizationID string) ([]Tenant, error) {
	cursor, err := r.coll.Find(ctx, bson.D{{Key: "organization_id", Value: organizationID}})
	if err != nil {
		return nil, fmt.Errorf("list tenants by org: %w", err)
	}
	defer cursor.Close(ctx)
	var out []Tenant
	if err := cursor.All(ctx, &out); err != nil {
		return nil, fmt.Errorf("decode tenants: %w", err)
	}
	return out, nil
}
