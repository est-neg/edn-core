package whatsapp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

const (
	providerMetaWhatsApp = "meta_whatsapp"
	dedupeTTL            = 7 * 24 * time.Hour
)

// IntakeProcessor is the service interface required by InternalHandler.
type IntakeProcessor interface {
	ProcessMessages(ctx context.Context, input *IntakeInput) error
}

// IntakeService processes incoming Meta WhatsApp webhook notifications.
type IntakeService struct {
	repo Repository
	log  *zap.Logger
}

// NewIntakeService creates an IntakeService backed by the given repository.
func NewIntakeService(repo Repository, log *zap.Logger) *IntakeService {
	return &IntakeService{repo: repo, log: log}
}

// ProcessMessages extracts all messages from the payload and persists a dedupe+receipt
// record for each new message atomically.
//
// Changes without messages (e.g. status/read-receipt events) are silently ignored.
// For changes that do carry messages, all (phone_number_id, message.id) pairs are
// validated up front. If any pair is invalid, ErrInvalidPayload is returned before
// any write occurs, preventing partial side-effects in a mixed batch.
//
// Duplicate messages (ErrDuplicate) are logged and skipped without error.
// A non-duplicate persistence failure causes the method to return an error immediately;
// the caller should respond 503. Messages already persisted in the same batch are safe:
// they will appear as duplicates on the next Meta retry.
func (s *IntakeService) ProcessMessages(ctx context.Context, input *IntakeInput) error {
	payloadHash := hashBytes(input.RawPayload)

	// Phase 1: validate all relevant messages and build the work list.
	// Changes without messages are skipped entirely (status/read-receipt change events).
	// Any invalid (phone_number_id, message.id) pair in a change that has messages
	// aborts before the first write.
	type workItem struct {
		phoneNumberID string
		msg           MetaMessage
	}
	var items []workItem
	for _, entry := range input.Payload.Entry {
		for _, change := range entry.Changes {
			if len(change.Value.Messages) == 0 {
				continue
			}
			phoneNumberID := change.Value.Metadata.PhoneNumberID
			if phoneNumberID == "" {
				return ErrInvalidPayload
			}
			for _, msg := range change.Value.Messages {
				if msg.ID == "" {
					return ErrInvalidPayload
				}
				items = append(items, workItem{phoneNumberID: phoneNumberID, msg: msg})
			}
		}
	}

	// Phase 2: persist each validated message.
	for _, item := range items {
		dedupeKey := item.phoneNumberID + ":" + item.msg.ID

		now := input.ReceivedAt
		dedupe := &DedupeRecord{
			DedupeKey:     dedupeKey,
			MessageID:     item.msg.ID,
			PhoneNumberID: item.phoneNumberID,
			CreatedAt:     now,
			ExpiresAt:     now.Add(dedupeTTL),
		}
		receipt := &MessageReceipt{
			ReceiptID:        uuid.New().String(),
			Provider:         providerMetaWhatsApp,
			MessageID:        item.msg.ID,
			PhoneNumberID:    item.phoneNumberID,
			From:             item.msg.From,
			MessageType:      item.msg.Type,
			ReceivedAt:       now,
			PayloadHash:      payloadHash,
			DedupeKey:        dedupeKey,
			IngressRequestID: input.IngressRequestID,
		}

		if err := s.repo.InsertMessageWithDedupe(ctx, dedupe, receipt); err != nil {
			if errors.Is(err, ErrDuplicate) {
				s.log.Info("whatsapp message duplicate skipped")
				continue
			}
			return fmt.Errorf("persist whatsapp message receipt: %w", err)
		}

		s.log.Info("whatsapp message receipt stored",
			zap.String("message_type", item.msg.Type),
		)
	}
	return nil
}

func hashBytes(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
