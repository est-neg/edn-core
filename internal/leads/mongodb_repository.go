package leads

import (
	"context"
	"fmt"
	"strings"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/villenneve/vil-core/internal/platform/config"
)

type mongoRepository struct {
	coll *mongo.Collection
}

// NewMongoRepository returns a Repository backed by the configured MongoDB collection.
func NewMongoRepository(client *mongo.Client, cfg config.MongoConfig) Repository {
	coll := client.Database(cfg.Database).Collection(cfg.CollectionLeads)
	return &mongoRepository{coll: coll}
}

func (r *mongoRepository) Save(ctx context.Context, lead Lead) error {
	doc := bson.D{
		{Key: "id", Value: lead.ID},
		{Key: "dedup_key", Value: lead.DedupKey},
		{Key: "tenant_id", Value: lead.TenantID},
		{Key: "source", Value: lead.Source},
		{Key: "received_at", Value: lead.ReceivedAt},
		{Key: "name", Value: lead.Name},
		{Key: "email", Value: lead.Email},
		{Key: "status", Value: lead.Status},
		{Key: "notification_status", Value: lead.NotificationStatus},
	}
	if lead.Phone != "" {
		doc = append(doc, bson.E{Key: "phone", Value: lead.Phone})
	}
	if lead.NotificationError != "" {
		doc = append(doc, bson.E{Key: "notification_error", Value: lead.NotificationError})
	}

	_, err := r.coll.InsertOne(ctx, doc)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return ErrDuplicateLead
		}
		return fmt.Errorf("insert lead: %w", err)
	}
	return nil
}

func (r *mongoRepository) ExistsByEmail(ctx context.Context, email string) (bool, error) {
	count, err := r.coll.CountDocuments(ctx, bson.D{{Key: "email", Value: email}})
	if err != nil {
		return false, fmt.Errorf("count leads by email: %w", err)
	}
	return count > 0, nil
}

func (r *mongoRepository) ExistsByPhone(ctx context.Context, phone string) (bool, error) {
	count, err := r.coll.CountDocuments(ctx, bson.D{{Key: "phone", Value: phone}})
	if err != nil {
		return false, fmt.Errorf("count leads by phone: %w", err)
	}
	return count > 0, nil
}

func (r *mongoRepository) UpdateNotificationStatus(ctx context.Context, leadID, status, detail string) error {
	setFields := bson.D{{Key: "notification_status", Value: status}}
	if detail != "" {
		setFields = append(setFields, bson.E{Key: "notification_error", Value: truncateNotificationError(detail)})
	}

	update := bson.D{{Key: "$set", Value: setFields}}
	if detail == "" {
		update = append(update, bson.E{Key: "$unset", Value: bson.D{{Key: "notification_error", Value: ""}}})
	}

	_, err := r.coll.UpdateOne(ctx, bson.D{{Key: "id", Value: leadID}}, update)
	if err != nil {
		return fmt.Errorf("update notification status: %w", err)
	}
	return nil
}

func truncateNotificationError(detail string) string {
	trimmed := strings.TrimSpace(detail)
	if len(trimmed) <= 255 {
		return trimmed
	}
	return trimmed[:255]
}
