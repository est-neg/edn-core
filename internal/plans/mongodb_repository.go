package plans

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/villenneve/vil-core/internal/platform/config"
)

type mongoPlans struct{ coll *mongo.Collection }

// NewMongoRepository returns a Repository backed by the versioned_plans collection.
// It uses cfg.CollectionVersionedPlans so it is completely isolated from the legacy
// checkout plans collection (cfg.CollectionPlans / "plans").
func NewMongoRepository(client *mongo.Client, cfg config.MongoConfig) Repository {
	return &mongoPlans{
		coll: client.Database(cfg.Database).Collection(cfg.CollectionVersionedPlans),
	}
}

func (r *mongoPlans) Create(ctx context.Context, plan Plan) error {
	_, err := r.coll.InsertOne(ctx, plan)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return ErrDuplicate
		}
		return fmt.Errorf("plans: create: %w", err)
	}
	return nil
}

func (r *mongoPlans) FindByUUID(ctx context.Context, planUUID string) (*Plan, error) {
	var p Plan
	err := r.coll.FindOne(ctx, bson.D{{Key: "plan_uuid", Value: planUUID}}).Decode(&p)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("plans: find by uuid: %w", err)
	}
	return &p, nil
}

func (r *mongoPlans) FindActiveByTenantSlug(ctx context.Context, tenantID, slug string) (*Plan, error) {
	var p Plan
	filter := buildActivePlanFilter(tenantID, slug, "", "", time.Now().UTC())
	// Return the highest version if multiple active versions exist (migration guard).
	opts := options.FindOne().SetSort(bson.D{{Key: "version", Value: -1}})
	err := r.coll.FindOne(ctx, filter, opts).Decode(&p)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("plans: find active by tenant slug: %w", err)
	}
	return &p, nil
}

func (r *mongoPlans) FindSellableByTenantSlug(ctx context.Context, tenantID, slug, billingCycle, channel string, at time.Time) (*Plan, error) {
	var p Plan
	filter := buildActivePlanFilter(tenantID, slug, billingCycle, channel, at)
	opts := options.FindOne().SetSort(bson.D{{Key: "version", Value: -1}})
	err := r.coll.FindOne(ctx, filter, opts).Decode(&p)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("plans: find sellable by tenant slug: %w", err)
	}
	return &p, nil
}

func (r *mongoPlans) ListActiveByTenant(ctx context.Context, tenantID, channel string) ([]Plan, error) {
	filter := buildActivePlanFilter(tenantID, "", "", channel, time.Now().UTC())
	opts := options.Find().SetSort(bson.D{
		{Key: "slug", Value: 1},
		{Key: "billing_cycle", Value: 1},
		{Key: "version", Value: -1},
	})
	cursor, err := r.coll.Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("plans: list active: %w", err)
	}
	defer cursor.Close(ctx)
	var plans []Plan
	if err := cursor.All(ctx, &plans); err != nil {
		return nil, fmt.Errorf("plans: decode active: %w", err)
	}
	return plans, nil
}

func buildActivePlanFilter(tenantID, slug, billingCycle, channel string, at time.Time) bson.D {
	filter := bson.D{
		{Key: "tenant_id", Value: tenantID},
		{Key: "active", Value: true},
		{Key: "valid_from", Value: bson.D{{Key: "$lte", Value: at}}},
		{Key: "$or", Value: bson.A{
			bson.D{{Key: "valid_until", Value: bson.D{{Key: "$exists", Value: false}}}},
			bson.D{{Key: "valid_until", Value: nil}},
			bson.D{{Key: "valid_until", Value: bson.D{{Key: "$gte", Value: at}}}},
		}},
	}
	if slug != "" {
		filter = append(filter, bson.E{Key: "slug", Value: slug})
	}
	if billingCycle != "" {
		filter = append(filter, bson.E{Key: "billing_cycle", Value: billingCycle})
	}
	if channel != "" && channel != ChannelAll {
		filter = append(filter, bson.E{Key: "channel", Value: bson.D{{Key: "$in", Value: []string{channel, ChannelAll}}}})
	}
	return filter
}
