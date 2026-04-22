package leads

import (
	"context"
	"fmt"
	"strings"
	"time"

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
		{Key: "source", Value: lead.Source},
		{Key: "submitted_at", Value: lead.SubmittedAt},
		{Key: "received_at", Value: lead.ReceivedAt},
		{Key: "name", Value: lead.Name},
		{Key: "business_name", Value: lead.BusinessName},
		{Key: "whatsapp", Value: lead.WhatsApp},
		{Key: "profile", Value: lead.Profile},
		{Key: "consent", Value: lead.Consent},
		{Key: "status", Value: lead.Status},
		{Key: "notification_status", Value: lead.NotificationStatus},
	}
	if lead.Email != "" {
		doc = append(doc, bson.E{Key: "email", Value: lead.Email})
	}
	if lead.Message != "" {
		doc = append(doc, bson.E{Key: "message", Value: lead.Message})
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

func (r *mongoRepository) ExistsByWhatsAppAndProfile(ctx context.Context, whatsapp, profile string, since time.Time) (bool, error) {
	filter := bson.D{
		{Key: "whatsapp", Value: whatsapp},
		{Key: "profile", Value: profile},
		{Key: "received_at", Value: bson.D{{Key: "$gte", Value: since}}},
	}

	count, err := r.coll.CountDocuments(ctx, filter)
	if err != nil {
		return false, fmt.Errorf("count leads: %w", err)
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
