package checkout

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/villenneve/vil-core/internal/platform/config"
)

// ─── Orders ───────────────────────────────────────────────────────────────────

type mongoOrders struct{ coll *mongo.Collection }

// NewMongoOrderRepository returns an OrderRepository backed by MongoDB.
func NewMongoOrderRepository(client *mongo.Client, cfg config.MongoConfig) OrderRepository {
	return &mongoOrders{coll: client.Database(cfg.Database).Collection(cfg.CollectionOrders)}
}

func (r *mongoOrders) Create(ctx context.Context, order Order) error {
	_, err := r.coll.InsertOne(ctx, order)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return ErrDuplicateOrder
		}
		return fmt.Errorf("create order: %w", err)
	}
	return nil
}

func (r *mongoOrders) FindByNSU(ctx context.Context, orderNSU string) (*Order, error) {
	var o Order
	err := r.coll.FindOne(ctx, bson.D{{Key: "order_nsu", Value: orderNSU}}).Decode(&o)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, ErrOrderNotFound
		}
		return nil, fmt.Errorf("find order by nsu: %w", err)
	}
	return &o, nil
}

// UpdateStatus updates order status by order_nsu.
// Caller must hold the Redis order lock before invoking this method.
func (r *mongoOrders) UpdateStatus(ctx context.Context, orderNSU, status string, updatedAt time.Time) error {
	update := bson.D{{Key: "$set", Value: bson.D{
		{Key: "status", Value: status},
		{Key: "updated_at", Value: updatedAt},
	}}}
	res, err := r.coll.UpdateOne(ctx, bson.D{{Key: "order_nsu", Value: orderNSU}}, update)
	if err != nil {
		return fmt.Errorf("update order status: %w", err)
	}
	if res.MatchedCount == 0 {
		return ErrOrderNotFound
	}
	return nil
}

func (r *mongoOrders) UpdateProviderURL(ctx context.Context, orderNSU, checkoutURL, invoiceSlug string, updatedAt time.Time) error {
	update := bson.D{{Key: "$set", Value: bson.D{
		{Key: "provider_checkout_url", Value: checkoutURL},
		{Key: "invoice_slug", Value: invoiceSlug},
		{Key: "updated_at", Value: updatedAt},
	}}}
	res, err := r.coll.UpdateOne(ctx, bson.D{{Key: "order_nsu", Value: orderNSU}}, update)
	if err != nil {
		return fmt.Errorf("update order provider url: %w", err)
	}
	if res.MatchedCount == 0 {
		return ErrOrderNotFound
	}
	return nil
}

func (r *mongoOrders) UpdateReceipt(ctx context.Context, orderNSU, receiptURL string, updatedAt time.Time) error {
	update := bson.D{{Key: "$set", Value: bson.D{
		{Key: "receipt_url", Value: receiptURL},
		{Key: "updated_at", Value: updatedAt},
	}}}
	res, err := r.coll.UpdateOne(ctx, bson.D{{Key: "order_nsu", Value: orderNSU}}, update)
	if err != nil {
		return fmt.Errorf("update order receipt: %w", err)
	}
	if res.MatchedCount == 0 {
		return ErrOrderNotFound
	}
	return nil
}

// ─── Payments ─────────────────────────────────────────────────────────────────

type mongoPayments struct{ coll *mongo.Collection }

// NewMongoPaymentRepository returns a PaymentRepository backed by MongoDB.
func NewMongoPaymentRepository(client *mongo.Client, cfg config.MongoConfig) PaymentRepository {
	return &mongoPayments{coll: client.Database(cfg.Database).Collection(cfg.CollectionPayments)}
}

// Upsert inserts or updates a payment matched by transaction_nsu.
// created_at is set only on insert via $setOnInsert; all other fields are always overwritten.
func (r *mongoPayments) Upsert(ctx context.Context, payment Payment) error {
	filter := bson.D{{Key: "transaction_nsu", Value: payment.TransactionNSU}}
	update := bson.D{
		{Key: "$set", Value: bson.D{
			{Key: "id", Value: payment.PaymentID},
			{Key: "order_nsu", Value: payment.OrderNSU},
			{Key: "invoice_slug", Value: payment.InvoiceSlug},
			{Key: "amount_cents", Value: payment.AmountCents},
			{Key: "paid_amount_cents", Value: payment.PaidAmountCents},
			{Key: "installments", Value: payment.Installments},
			{Key: "capture_method", Value: payment.CaptureMethod},
			{Key: "receipt_url", Value: payment.ReceiptURL},
			{Key: "status", Value: payment.Status},
			{Key: "raw_payload", Value: payment.RawPayload},
			{Key: "updated_at", Value: payment.UpdatedAt},
		}},
		{Key: "$setOnInsert", Value: bson.D{
			{Key: "_id", Value: primitive.NewObjectID()},
			{Key: "created_at", Value: payment.CreatedAt},
		}},
	}
	_, err := r.coll.UpdateOne(ctx, filter, update, options.Update().SetUpsert(true))
	if err != nil {
		return fmt.Errorf("upsert payment: %w", err)
	}
	return nil
}

func (r *mongoPayments) FindByTransactionNSU(ctx context.Context, transactionNSU string) (*Payment, error) {
	var p Payment
	err := r.coll.FindOne(ctx, bson.D{{Key: "transaction_nsu", Value: transactionNSU}}).Decode(&p)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, ErrPaymentNotFound
		}
		return nil, fmt.Errorf("find payment by transaction nsu: %w", err)
	}
	return &p, nil
}

func (r *mongoPayments) FindByOrderNSU(ctx context.Context, orderNSU string) ([]Payment, error) {
	cursor, err := r.coll.Find(ctx, bson.D{{Key: "order_nsu", Value: orderNSU}})
	if err != nil {
		return nil, fmt.Errorf("find payments by order nsu: %w", err)
	}
	defer cursor.Close(ctx)
	var payments []Payment
	if err := cursor.All(ctx, &payments); err != nil {
		return nil, fmt.Errorf("decode payments: %w", err)
	}
	return payments, nil
}

func (r *mongoPayments) UpdateStatus(ctx context.Context, transactionNSU, status string, updatedAt time.Time) error {
	update := bson.D{{Key: "$set", Value: bson.D{
		{Key: "status", Value: status},
		{Key: "updated_at", Value: updatedAt},
	}}}
	res, err := r.coll.UpdateOne(ctx, bson.D{{Key: "transaction_nsu", Value: transactionNSU}}, update)
	if err != nil {
		return fmt.Errorf("update payment status: %w", err)
	}
	if res.MatchedCount == 0 {
		return ErrPaymentNotFound
	}
	return nil
}

// ─── Subscriptions ────────────────────────────────────────────────────────────

type mongoSubscriptions struct{ coll *mongo.Collection }

// NewMongoSubscriptionRepository returns a SubscriptionRepository backed by MongoDB.
func NewMongoSubscriptionRepository(client *mongo.Client, cfg config.MongoConfig) SubscriptionRepository {
	return &mongoSubscriptions{coll: client.Database(cfg.Database).Collection(cfg.CollectionSubscriptions)}
}

func (r *mongoSubscriptions) Create(ctx context.Context, sub Subscription) error {
	_, err := r.coll.InsertOne(ctx, sub)
	if err != nil {
		return fmt.Errorf("create subscription: %w", err)
	}
	return nil
}

func (r *mongoSubscriptions) FindByOriginOrderNSU(ctx context.Context, orderNSU string) (*Subscription, error) {
	var s Subscription
	err := r.coll.FindOne(ctx, bson.D{{Key: "origin_order_nsu", Value: orderNSU}}).Decode(&s)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, ErrSubscriptionNotFound
		}
		return nil, fmt.Errorf("find subscription by origin order nsu: %w", err)
	}
	return &s, nil
}

func (r *mongoSubscriptions) FindByEmail(ctx context.Context, email string) ([]Subscription, error) {
	cursor, err := r.coll.Find(ctx, bson.D{{Key: "customer_email", Value: email}})
	if err != nil {
		return nil, fmt.Errorf("find subscriptions by email: %w", err)
	}
	defer cursor.Close(ctx)
	var subs []Subscription
	if err := cursor.All(ctx, &subs); err != nil {
		return nil, fmt.Errorf("decode subscriptions: %w", err)
	}
	return subs, nil
}

// Activate transitions a subscription to active and sets the billing period.
// Matched by origin_order_nsu; this is the canonical activation write path.
func (r *mongoSubscriptions) Activate(ctx context.Context, originOrderNSU string, startsAt, endsAt, updatedAt time.Time) error {
	update := bson.D{{Key: "$set", Value: bson.D{
		{Key: "status", Value: "active"},
		{Key: "starts_at", Value: startsAt},
		{Key: "ends_at", Value: endsAt},
		{Key: "updated_at", Value: updatedAt},
	}}}
	res, err := r.coll.UpdateOne(ctx, bson.D{{Key: "origin_order_nsu", Value: originOrderNSU}}, update)
	if err != nil {
		return fmt.Errorf("activate subscription: %w", err)
	}
	if res.MatchedCount == 0 {
		return ErrSubscriptionNotFound
	}
	return nil
}

func (r *mongoSubscriptions) UpdateStatus(ctx context.Context, id, status string, updatedAt time.Time) error {
	update := bson.D{{Key: "$set", Value: bson.D{
		{Key: "status", Value: status},
		{Key: "updated_at", Value: updatedAt},
	}}}
	res, err := r.coll.UpdateOne(ctx, bson.D{{Key: "id", Value: id}}, update)
	if err != nil {
		return fmt.Errorf("update subscription status: %w", err)
	}
	if res.MatchedCount == 0 {
		return ErrSubscriptionNotFound
	}
	return nil
}

// ─── Webhook Events ───────────────────────────────────────────────────────────

type mongoWebhookEvents struct{ coll *mongo.Collection }

// NewMongoWebhookEventRepository returns a WebhookEventRepository backed by MongoDB.
func NewMongoWebhookEventRepository(client *mongo.Client, cfg config.MongoConfig) WebhookEventRepository {
	return &mongoWebhookEvents{coll: client.Database(cfg.Database).Collection(cfg.CollectionWebhookEvents)}
}

func (r *mongoWebhookEvents) Insert(ctx context.Context, event WebhookEvent) error {
	_, err := r.coll.InsertOne(ctx, event)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return ErrDuplicateWebhook
		}
		return fmt.Errorf("insert webhook event: %w", err)
	}
	return nil
}

// ExistsByEventHash is the authoritative Mongo dedup check.
// Always call the Redis fast path (CheckWebhookDedup) before this method.
func (r *mongoWebhookEvents) ExistsByEventHash(ctx context.Context, eventHash string) (bool, error) {
	count, err := r.coll.CountDocuments(ctx, bson.D{{Key: "event_hash", Value: eventHash}})
	if err != nil {
		return false, fmt.Errorf("count webhook by event hash: %w", err)
	}
	return count > 0, nil
}

func (r *mongoWebhookEvents) FindByTransactionNSU(ctx context.Context, transactionNSU string) ([]WebhookEvent, error) {
	cursor, err := r.coll.Find(ctx, bson.D{{Key: "transaction_nsu", Value: transactionNSU}})
	if err != nil {
		return nil, fmt.Errorf("find webhook events by transaction nsu: %w", err)
	}
	defer cursor.Close(ctx)
	var events []WebhookEvent
	if err := cursor.All(ctx, &events); err != nil {
		return nil, fmt.Errorf("decode webhook events: %w", err)
	}
	return events, nil
}

func (r *mongoWebhookEvents) MarkProcessed(ctx context.Context, id string, processedAt time.Time) error {
	update := bson.D{{Key: "$set", Value: bson.D{
		{Key: "processed_at", Value: processedAt},
	}}}
	res, err := r.coll.UpdateOne(ctx, bson.D{{Key: "id", Value: id}}, update)
	if err != nil {
		return fmt.Errorf("mark webhook processed: %w", err)
	}
	if res.MatchedCount == 0 {
		return fmt.Errorf("mark webhook processed: %w", ErrOrderNotFound)
	}
	return nil
}

// ─── Outbox Events ────────────────────────────────────────────────────────────

type mongoOutboxEvents struct{ coll *mongo.Collection }

// NewMongoOutboxEventRepository returns an OutboxEventRepository backed by MongoDB.
func NewMongoOutboxEventRepository(client *mongo.Client, cfg config.MongoConfig) OutboxEventRepository {
	return &mongoOutboxEvents{coll: client.Database(cfg.Database).Collection(cfg.CollectionOutboxEvents)}
}

func (r *mongoOutboxEvents) Insert(ctx context.Context, event OutboxEvent) error {
	_, err := r.coll.InsertOne(ctx, event)
	if err != nil {
		return fmt.Errorf("insert outbox event: %w", err)
	}
	return nil
}

// FetchUnpublished returns up to limit unpublished events sorted by created_at ascending.
// The outbox worker calls this in a polling loop for at-least-once delivery.
func (r *mongoOutboxEvents) FetchUnpublished(ctx context.Context, limit int) ([]OutboxEvent, error) {
	opts := options.Find().
		SetSort(bson.D{{Key: "created_at", Value: 1}}).
		SetLimit(int64(limit))
	cursor, err := r.coll.Find(ctx, bson.D{{Key: "published", Value: false}}, opts)
	if err != nil {
		return nil, fmt.Errorf("fetch unpublished outbox events: %w", err)
	}
	defer cursor.Close(ctx)
	var events []OutboxEvent
	if err := cursor.All(ctx, &events); err != nil {
		return nil, fmt.Errorf("decode outbox events: %w", err)
	}
	return events, nil
}

func (r *mongoOutboxEvents) MarkPublished(ctx context.Context, id string, publishedAt time.Time) error {
	update := bson.D{{Key: "$set", Value: bson.D{
		{Key: "published", Value: true},
		{Key: "published_at", Value: publishedAt},
	}}}
	res, err := r.coll.UpdateOne(ctx, bson.D{{Key: "id", Value: id}}, update)
	if err != nil {
		return fmt.Errorf("mark outbox event published: %w", err)
	}
	if res.MatchedCount == 0 {
		return fmt.Errorf("mark outbox event published: id %s not found", id)
	}
	return nil
}
