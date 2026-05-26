package whatsapp

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/mongo"

	"github.com/villenneve/vil-core/internal/platform/config"
)

// MongoRepository implements Repository using MongoDB multi-document transactions.
// Each message persists a dedupe record and a receipt atomically so that a crash
// after dedupe-insert cannot leave a state with "dedupe recorded but receipt missing".
type MongoRepository struct {
	client      *mongo.Client
	receiptsCol *mongo.Collection
	dedupeCol   *mongo.Collection
}

// NewMongoRepository creates a MongoRepository using the configured collection names.
func NewMongoRepository(client *mongo.Client, cfg config.MongoConfig) *MongoRepository {
	db := client.Database(cfg.Database)
	return &MongoRepository{
		client:      client,
		receiptsCol: db.Collection(cfg.CollectionWhatsAppMessageReceipts),
		dedupeCol:   db.Collection(cfg.CollectionWhatsAppMessageDedupe),
	}
}

// InsertMessageWithDedupe atomically inserts a dedupe record and a receipt in a
// MongoDB multi-document transaction.
//
// If the dedupe_key unique index rejects the insert, ErrDuplicate is returned and
// the transaction aborts with no side effects.
// Any other error is returned as-is so the caller can surface a 503.
func (r *MongoRepository) InsertMessageWithDedupe(ctx context.Context, dedupe *DedupeRecord, receipt *MessageReceipt) error {
	session, err := r.client.StartSession()
	if err != nil {
		return fmt.Errorf("start session: %w", err)
	}
	defer session.EndSession(ctx)

	_, err = session.WithTransaction(ctx, func(sessCtx mongo.SessionContext) (interface{}, error) {
		if _, insErr := r.dedupeCol.InsertOne(sessCtx, dedupe); insErr != nil {
			if mongo.IsDuplicateKeyError(insErr) {
				return nil, ErrDuplicate
			}
			return nil, fmt.Errorf("insert dedupe: %w", insErr)
		}
		if _, insErr := r.receiptsCol.InsertOne(sessCtx, receipt); insErr != nil {
			return nil, fmt.Errorf("insert receipt: %w", insErr)
		}
		return nil, nil
	})
	return err
}
