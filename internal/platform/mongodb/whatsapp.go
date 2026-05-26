package mongodb

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/villenneve/vil-core/internal/platform/config"
)

// BootstrapWhatsAppStorage materializes the WhatsApp collections and ensures all
// required indexes exist. Safe to call on every startup — all operations are idempotent.
func BootstrapWhatsAppStorage(ctx context.Context, client *mongo.Client, cfg config.MongoConfig) error {
	db := client.Database(cfg.Database)

	for _, name := range []string{
		cfg.CollectionWhatsAppMessageReceipts,
		cfg.CollectionWhatsAppMessageDedupe,
	} {
		if err := ensureCollection(ctx, db, name); err != nil {
			return fmt.Errorf("bootstrap whatsapp collection %q: %w", name, err)
		}
	}

	return EnsureWhatsAppIndexes(ctx, client, cfg)
}

// EnsureWhatsAppIndexes creates all required indexes for WhatsApp collections idempotently.
// Safe to call on every startup.
func EnsureWhatsAppIndexes(ctx context.Context, client *mongo.Client, cfg config.MongoConfig) error {
	db := client.Database(cfg.Database)

	if err := ensureWhatsAppReceiptsIndexes(ctx, db.Collection(cfg.CollectionWhatsAppMessageReceipts)); err != nil {
		return err
	}
	return ensureWhatsAppDedupeIndexes(ctx, db.Collection(cfg.CollectionWhatsAppMessageDedupe))
}

func ensureWhatsAppReceiptsIndexes(ctx context.Context, coll *mongo.Collection) error {
	indexes := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "receipt_id", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("idx_wa_receipt_id_unique"),
		},
		{
			Keys:    bson.D{{Key: "message_id", Value: 1}},
			Options: options.Index().SetName("idx_wa_message_id"),
		},
		{
			Keys: bson.D{
				{Key: "phone_number_id", Value: 1},
				{Key: "received_at", Value: -1},
			},
			Options: options.Index().SetName("idx_wa_phone_received"),
		},
		{
			Keys:    bson.D{{Key: "received_at", Value: -1}},
			Options: options.Index().SetName("idx_wa_received_at"),
		},
	}
	if _, err := coll.Indexes().CreateMany(ctx, indexes); err != nil {
		return fmt.Errorf("ensure whatsapp receipts indexes: %w", err)
	}
	return nil
}

func ensureWhatsAppDedupeIndexes(ctx context.Context, coll *mongo.Collection) error {
	indexes := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "dedupe_key", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("idx_wa_dedupe_key_unique"),
		},
		{
			// TTL index: MongoDB automatically removes documents when expires_at is reached.
			// expireAfterSeconds=0 means the document expires at the time stored in expires_at.
			Keys:    bson.D{{Key: "expires_at", Value: 1}},
			Options: options.Index().SetExpireAfterSeconds(0).SetName("idx_wa_dedupe_expires_at_ttl"),
		},
	}
	if _, err := coll.Indexes().CreateMany(ctx, indexes); err != nil {
		return fmt.Errorf("ensure whatsapp dedupe indexes: %w", err)
	}
	return nil
}
