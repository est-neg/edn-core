package organizations

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

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

func (r *mongoRepository) List(ctx context.Context) ([]Organization, error) {
	cursor, err := r.coll.Find(ctx, bson.D{}, options.Find().SetSort(bson.D{{Key: "slug", Value: 1}}))
	if err != nil {
		return nil, fmt.Errorf("list organizations: %w", err)
	}
	defer cursor.Close(ctx)
	var out []Organization
	if err := cursor.All(ctx, &out); err != nil {
		return nil, fmt.Errorf("decode organizations: %w", err)
	}
	return out, nil
}

func (r *mongoRepository) Update(ctx context.Context, orgUUID string, org Organization) error {
	filter := bson.D{{Key: "org_uuid", Value: orgUUID}}
	update := bson.D{{Key: "$set", Value: bson.D{
		{Key: "name", Value: org.Name},
		{Key: "legal_name", Value: org.LegalName},
		{Key: "cnpj", Value: org.CNPJ},
		{Key: "billing_email", Value: org.BillingEmail},
		{Key: "phone", Value: org.Phone},
		{Key: "address", Value: org.Address},
		{Key: "active", Value: org.Active},
		{Key: "updated_at", Value: time.Now().UTC()},
	}}}
	res, err := r.coll.UpdateOne(ctx, filter, update)
	if err != nil {
		return fmt.Errorf("update organization: %w", err)
	}
	if res.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *mongoRepository) Deactivate(ctx context.Context, orgUUID string) error {
	filter := bson.D{{Key: "org_uuid", Value: orgUUID}}
	update := bson.D{{Key: "$set", Value: bson.D{
		{Key: "active", Value: false},
		{Key: "updated_at", Value: time.Now().UTC()},
	}}}
	res, err := r.coll.UpdateOne(ctx, filter, update)
	if err != nil {
		return fmt.Errorf("deactivate organization: %w", err)
	}
	if res.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}
