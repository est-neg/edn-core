package mongodb

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/villenneve/vil-core/internal/platform/config"
)

// BootstrapCheckoutStorage ensures all checkout collections and their required
// indexes exist. Safe to call on every startup — all operations are idempotent.
func BootstrapCheckoutStorage(ctx context.Context, client *mongo.Client, cfg config.MongoConfig) error {
	db := client.Database(cfg.Database)

	collections := []string{
		cfg.CollectionPlans,
		cfg.CollectionOrders,
		cfg.CollectionPayments,
		cfg.CollectionSubscriptions,
		cfg.CollectionWebhookEvents,
		cfg.CollectionOutboxEvents,
	}
	for _, name := range collections {
		if err := ensureCollection(ctx, db, name); err != nil {
			return fmt.Errorf("bootstrap checkout collection %q: %w", name, err)
		}
	}

	return EnsureCheckoutIndexes(ctx, client, cfg)
}

// EnsureCheckoutIndexes creates all required indexes for checkout collections
// idempotently. Safe to call on every startup.
func EnsureCheckoutIndexes(ctx context.Context, client *mongo.Client, cfg config.MongoConfig) error {
	db := client.Database(cfg.Database)

	if err := ensurePlansIndexes(ctx, db.Collection(cfg.CollectionPlans)); err != nil {
		return err
	}
	if err := ensureOrdersIndexes(ctx, db.Collection(cfg.CollectionOrders)); err != nil {
		return err
	}
	if err := ensurePaymentsIndexes(ctx, db.Collection(cfg.CollectionPayments)); err != nil {
		return err
	}
	if err := ensureSubscriptionsIndexes(ctx, db.Collection(cfg.CollectionSubscriptions)); err != nil {
		return err
	}
	if err := ensureWebhookEventsIndexes(ctx, db.Collection(cfg.CollectionWebhookEvents)); err != nil {
		return err
	}
	if err := ensureOutboxEventsIndexes(ctx, db.Collection(cfg.CollectionOutboxEvents)); err != nil {
		return err
	}
	return nil
}

func ensurePlansIndexes(ctx context.Context, coll *mongo.Collection) error {
	indexes := []mongo.IndexModel{
		{
			// Compound unique: a plan variant is uniquely identified by slug + billing cycle.
			Keys: bson.D{
				{Key: "slug", Value: 1},
				{Key: "billing_cycle", Value: 1},
			},
			Options: options.Index().SetUnique(true).SetName("idx_plans_slug_cycle_unique"),
		},
	}
	if _, err := coll.Indexes().CreateMany(ctx, indexes); err != nil {
		return fmt.Errorf("ensure plans indexes: %w", err)
	}
	return nil
}

func ensureOrdersIndexes(ctx context.Context, coll *mongo.Collection) error {
	indexes := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "order_nsu", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("idx_orders_order_nsu_unique"),
		},
		{
			Keys:    bson.D{{Key: "customer_email", Value: 1}},
			Options: options.Index().SetName("idx_orders_customer_email"),
		},
		{
			Keys:    bson.D{{Key: "status", Value: 1}},
			Options: options.Index().SetName("idx_orders_status"),
		},
		{
			Keys:    bson.D{{Key: "created_at", Value: -1}},
			Options: options.Index().SetName("idx_orders_created_at"),
		},
		{
			Keys: bson.D{
				{Key: "tenant_id", Value: 1},
				{Key: "status", Value: 1},
				{Key: "created_at", Value: -1},
			},
			Options: options.Index().SetName("idx_orders_tenant_status_created_at"),
		},
	}
	if _, err := coll.Indexes().CreateMany(ctx, indexes); err != nil {
		return fmt.Errorf("ensure orders indexes: %w", err)
	}
	return nil
}

func ensurePaymentsIndexes(ctx context.Context, coll *mongo.Collection) error {
	indexes := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "transaction_nsu", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("idx_payments_transaction_nsu_unique"),
		},
		{
			Keys:    bson.D{{Key: "order_nsu", Value: 1}},
			Options: options.Index().SetName("idx_payments_order_nsu"),
		},
		{
			Keys:    bson.D{{Key: "status", Value: 1}},
			Options: options.Index().SetName("idx_payments_status"),
		},
		{
			Keys:    bson.D{{Key: "created_at", Value: -1}},
			Options: options.Index().SetName("idx_payments_created_at"),
		},
	}
	if _, err := coll.Indexes().CreateMany(ctx, indexes); err != nil {
		return fmt.Errorf("ensure payments indexes: %w", err)
	}
	return nil
}

func ensureSubscriptionsIndexes(ctx context.Context, coll *mongo.Collection) error {
	indexes := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "customer_email", Value: 1}},
			Options: options.Index().SetName("idx_subscriptions_customer_email"),
		},
		{
			Keys:    bson.D{{Key: "plan_slug", Value: 1}},
			Options: options.Index().SetName("idx_subscriptions_plan_slug"),
		},
		{
			Keys:    bson.D{{Key: "status", Value: 1}},
			Options: options.Index().SetName("idx_subscriptions_status"),
		},
		{
			// Support activation lookup by origin order NSU.
			Keys:    bson.D{{Key: "origin_order_nsu", Value: 1}},
			Options: options.Index().SetName("idx_subscriptions_origin_order_nsu"),
		},
	}
	if _, err := coll.Indexes().CreateMany(ctx, indexes); err != nil {
		return fmt.Errorf("ensure subscriptions indexes: %w", err)
	}
	return nil
}

func ensureWebhookEventsIndexes(ctx context.Context, coll *mongo.Collection) error {
	indexes := []mongo.IndexModel{
		{
			// Authoritative dedup key — prevents duplicate webhook processing.
			Keys:    bson.D{{Key: "event_hash", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("idx_webhook_events_event_hash_unique"),
		},
		{
			Keys:    bson.D{{Key: "order_nsu", Value: 1}},
			Options: options.Index().SetName("idx_webhook_events_order_nsu"),
		},
		{
			Keys:    bson.D{{Key: "transaction_nsu", Value: 1}},
			Options: options.Index().SetName("idx_webhook_events_transaction_nsu"),
		},
		{
			Keys:    bson.D{{Key: "received_at", Value: -1}},
			Options: options.Index().SetName("idx_webhook_events_received_at"),
		},
	}
	if _, err := coll.Indexes().CreateMany(ctx, indexes); err != nil {
		return fmt.Errorf("ensure webhook_events indexes: %w", err)
	}
	return nil
}

func ensureOutboxEventsIndexes(ctx context.Context, coll *mongo.Collection) error {
	indexes := []mongo.IndexModel{
		{
			// Worker polling index: {published: false} sorted by created_at ascending.
			// Compound index covers the query + sort in a single index scan.
			Keys: bson.D{
				{Key: "published", Value: 1},
				{Key: "created_at", Value: 1},
			},
			Options: options.Index().SetName("idx_outbox_events_published_created_at"),
		},
	}
	if _, err := coll.Indexes().CreateMany(ctx, indexes); err != nil {
		return fmt.Errorf("ensure outbox_events indexes: %w", err)
	}
	return nil
}
