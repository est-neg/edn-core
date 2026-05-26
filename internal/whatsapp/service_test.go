package whatsapp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"
)

// ─── Fake repository ─────────────────────────────────────────────────────────

type fakeRepo struct {
	insertedDedupes  []*DedupeRecord
	insertedReceipts []*MessageReceipt
	// duplicateKeys is a set of dedupe_key values that will return ErrDuplicate.
	duplicateKeys map[string]bool
	// failOnKey if set, returns an error for that key (simulates transient failure).
	failOnKey string
	failErr   error
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{duplicateKeys: make(map[string]bool)}
}

func (r *fakeRepo) InsertMessageWithDedupe(_ context.Context, dedupe *DedupeRecord, receipt *MessageReceipt) error {
	if r.duplicateKeys[dedupe.DedupeKey] {
		return ErrDuplicate
	}
	if r.failOnKey != "" && dedupe.DedupeKey == r.failOnKey {
		return r.failErr
	}
	r.insertedDedupes = append(r.insertedDedupes, dedupe)
	r.insertedReceipts = append(r.insertedReceipts, receipt)
	// Mark the key so subsequent calls see it as duplicate.
	r.duplicateKeys[dedupe.DedupeKey] = true
	return nil
}

// ─── Test helpers ─────────────────────────────────────────────────────────────

func buildService(repo Repository) *IntakeService {
	return NewIntakeService(repo, zap.NewNop())
}

func multiMessagePayload(phoneNumberID string, msgIDs ...string) *MetaWebhookPayload {
	messages := make([]MetaMessage, 0, len(msgIDs))
	for _, id := range msgIDs {
		messages = append(messages, MetaMessage{
			ID: id, From: "5511999990001", Timestamp: "1715000000", Type: "text",
		})
	}
	return &MetaWebhookPayload{
		Object: "whatsapp_business_account",
		Entry: []MetaEntry{
			{
				ID: "entry-001",
				Changes: []MetaChange{
					{
						Field: "messages",
						Value: MetaChangeValue{
							MessagingProduct: "whatsapp",
							Metadata:         MetaMetadata{PhoneNumberID: phoneNumberID},
							Messages:         messages,
						},
					},
				},
			},
		},
	}
}

func rawPayload(t *testing.T, p *MetaWebhookPayload) []byte {
	t.Helper()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// ─── Service tests ────────────────────────────────────────────────────────────

func TestService_ProcessMessages_SingleMessage_Persisted(t *testing.T) {
	repo := newFakeRepo()
	svc := buildService(repo)
	payload := multiMessagePayload("ph-001", "msg-001")

	err := svc.ProcessMessages(context.Background(), &IntakeInput{
		IngressRequestID: "req-001",
		ReceivedAt:       time.Now().UTC(),
		RawPayload:       rawPayload(t, payload),
		Payload:          payload,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repo.insertedReceipts) != 1 {
		t.Errorf("inserted receipts = %d, want 1", len(repo.insertedReceipts))
	}
	receipt := repo.insertedReceipts[0]
	if receipt.MessageID != "msg-001" {
		t.Errorf("MessageID = %q, want %q", receipt.MessageID, "msg-001")
	}
	if receipt.Provider != providerMetaWhatsApp {
		t.Errorf("Provider = %q, want %q", receipt.Provider, providerMetaWhatsApp)
	}
	if receipt.PhoneNumberID != "ph-001" {
		t.Errorf("PhoneNumberID = %q, want %q", receipt.PhoneNumberID, "ph-001")
	}
	if receipt.DedupeKey != "ph-001:msg-001" {
		t.Errorf("DedupeKey = %q, want %q", receipt.DedupeKey, "ph-001:msg-001")
	}
	if receipt.IngressRequestID != "req-001" {
		t.Errorf("IngressRequestID = %q, want %q", receipt.IngressRequestID, "req-001")
	}
}

func TestService_ProcessMessages_MultipleMessages_AllPersisted(t *testing.T) {
	repo := newFakeRepo()
	svc := buildService(repo)
	payload := multiMessagePayload("ph-001", "msg-001", "msg-002", "msg-003")

	err := svc.ProcessMessages(context.Background(), &IntakeInput{
		IngressRequestID: "req-multi",
		ReceivedAt:       time.Now().UTC(),
		RawPayload:       rawPayload(t, payload),
		Payload:          payload,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repo.insertedReceipts) != 3 {
		t.Errorf("inserted receipts = %d, want 3", len(repo.insertedReceipts))
	}
}

func TestService_ProcessMessages_DuplicateMessage_SkippedWithoutError(t *testing.T) {
	repo := newFakeRepo()
	// Pre-seed the dedupe key.
	repo.duplicateKeys["ph-001:msg-dup"] = true
	svc := buildService(repo)
	payload := multiMessagePayload("ph-001", "msg-dup")

	err := svc.ProcessMessages(context.Background(), &IntakeInput{
		IngressRequestID: "req-dup",
		ReceivedAt:       time.Now().UTC(),
		RawPayload:       rawPayload(t, payload),
		Payload:          payload,
	})
	if err != nil {
		t.Fatalf("duplicate should not cause error, got: %v", err)
	}
	if len(repo.insertedReceipts) != 0 {
		t.Errorf("no receipts should be inserted for duplicate, got %d", len(repo.insertedReceipts))
	}
}

func TestService_ProcessMessages_MixedDuplicateAndNew_NewPersisted(t *testing.T) {
	repo := newFakeRepo()
	repo.duplicateKeys["ph-001:msg-dup"] = true
	svc := buildService(repo)
	// Payload has one duplicate and one new message.
	payload := multiMessagePayload("ph-001", "msg-dup", "msg-new")

	err := svc.ProcessMessages(context.Background(), &IntakeInput{
		IngressRequestID: "req-mix",
		ReceivedAt:       time.Now().UTC(),
		RawPayload:       rawPayload(t, payload),
		Payload:          payload,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repo.insertedReceipts) != 1 {
		t.Errorf("inserted receipts = %d, want 1 (only the new message)", len(repo.insertedReceipts))
	}
	if repo.insertedReceipts[0].MessageID != "msg-new" {
		t.Errorf("MessageID = %q, want %q", repo.insertedReceipts[0].MessageID, "msg-new")
	}
}

func TestService_ProcessMessages_TransientError_ReturnsError(t *testing.T) {
	repo := newFakeRepo()
	repo.failOnKey = "ph-001:msg-fail"
	repo.failErr = errors.New("mongodb connection lost")
	svc := buildService(repo)
	payload := multiMessagePayload("ph-001", "msg-fail")

	err := svc.ProcessMessages(context.Background(), &IntakeInput{
		IngressRequestID: "req-fail",
		ReceivedAt:       time.Now().UTC(),
		RawPayload:       rawPayload(t, payload),
		Payload:          payload,
	})
	if err == nil {
		t.Fatal("expected error from transient failure, got nil")
	}
}

func TestService_ProcessMessages_PartialFailure_StopsOnError(t *testing.T) {
	repo := newFakeRepo()
	repo.failOnKey = "ph-001:msg-002"
	repo.failErr = errors.New("transient error")
	svc := buildService(repo)

	// Three messages: first succeeds, second fails, third never reached.
	payload := multiMessagePayload("ph-001", "msg-001", "msg-002", "msg-003")

	err := svc.ProcessMessages(context.Background(), &IntakeInput{
		IngressRequestID: "req-partial",
		ReceivedAt:       time.Now().UTC(),
		RawPayload:       rawPayload(t, payload),
		Payload:          payload,
	})
	if err == nil {
		t.Fatal("expected error from partial failure, got nil")
	}
	// Only msg-001 was inserted before the failure.
	if len(repo.insertedReceipts) != 1 {
		t.Errorf("inserted receipts = %d, want 1 (only before failure)", len(repo.insertedReceipts))
	}
}

func TestService_ProcessMessages_EmptyPayload_NoError(t *testing.T) {
	repo := newFakeRepo()
	svc := buildService(repo)
	payload := &MetaWebhookPayload{Object: "whatsapp_business_account", Entry: nil}

	err := svc.ProcessMessages(context.Background(), &IntakeInput{
		IngressRequestID: "req-empty",
		ReceivedAt:       time.Now().UTC(),
		RawPayload:       []byte(`{}`),
		Payload:          payload,
	})
	if err != nil {
		t.Fatalf("empty payload should not error: %v", err)
	}
	if len(repo.insertedReceipts) != 0 {
		t.Errorf("no receipts for empty payload, got %d", len(repo.insertedReceipts))
	}
}

func TestService_ProcessMessages_DedupeKeyFormat(t *testing.T) {
	repo := newFakeRepo()
	svc := buildService(repo)
	payload := multiMessagePayload("phone-123", "wamid.abc456")

	_ = svc.ProcessMessages(context.Background(), &IntakeInput{
		IngressRequestID: "req-dedup",
		ReceivedAt:       time.Now().UTC(),
		RawPayload:       rawPayload(t, payload),
		Payload:          payload,
	})

	if len(repo.insertedDedupes) != 1 {
		t.Fatalf("expected 1 dedupe record, got %d", len(repo.insertedDedupes))
	}
	want := "phone-123:wamid.abc456"
	if repo.insertedDedupes[0].DedupeKey != want {
		t.Errorf("DedupeKey = %q, want %q", repo.insertedDedupes[0].DedupeKey, want)
	}
}

func TestService_ProcessMessages_DedupeExpiresIn7Days(t *testing.T) {
	repo := newFakeRepo()
	svc := buildService(repo)
	payload := multiMessagePayload("ph-001", "msg-ttl")
	now := time.Date(2026, 5, 13, 10, 0, 0, 0, time.UTC)

	_ = svc.ProcessMessages(context.Background(), &IntakeInput{
		IngressRequestID: "req-ttl",
		ReceivedAt:       now,
		RawPayload:       rawPayload(t, payload),
		Payload:          payload,
	})

	if len(repo.insertedDedupes) == 0 {
		t.Fatal("expected dedupe record")
	}
	want := now.Add(7 * 24 * time.Hour)
	got := repo.insertedDedupes[0].ExpiresAt
	if !got.Equal(want) {
		t.Errorf("ExpiresAt = %v, want %v", got, want)
	}
}

func TestService_ProcessMessages_PayloadHashIsConsistent(t *testing.T) {
	repo := newFakeRepo()
	svc := buildService(repo)
	payload := multiMessagePayload("ph-001", "msg-hash")
	raw := rawPayload(t, payload)

	_ = svc.ProcessMessages(context.Background(), &IntakeInput{
		IngressRequestID: "req-hash",
		ReceivedAt:       time.Now().UTC(),
		RawPayload:       raw,
		Payload:          payload,
	})

	if len(repo.insertedReceipts) == 0 {
		t.Fatal("expected receipt")
	}
	got := repo.insertedReceipts[0].PayloadHash
	want := hashBytes(raw)
	if got != want {
		t.Errorf("PayloadHash = %q, want %q", got, want)
	}
}

func TestService_ProcessMessages_MixedBatch_ValidThenInvalidID_NoWrites(t *testing.T) {
	repo := newFakeRepo()
	svc := buildService(repo)
	// Batch: first message has a valid ID, second has an empty ID.
	// Pre-validation must catch the invalid entry and return ErrInvalidPayload
	// without writing anything.
	payload := &MetaWebhookPayload{
		Object: "whatsapp_business_account",
		Entry: []MetaEntry{
			{
				ID: "entry-001",
				Changes: []MetaChange{
					{
						Field: "messages",
						Value: MetaChangeValue{
							Metadata: MetaMetadata{PhoneNumberID: "ph-001"},
							Messages: []MetaMessage{
								{ID: "msg-valid", From: "5511", Timestamp: "1715000000", Type: "text"},
								{ID: "", From: "5511", Timestamp: "1715000001", Type: "text"},
							},
						},
					},
				},
			},
		},
	}
	err := svc.ProcessMessages(context.Background(), &IntakeInput{
		IngressRequestID: "req-mixed-invalid",
		ReceivedAt:       time.Now().UTC(),
		RawPayload:       rawPayload(t, payload),
		Payload:          payload,
	})
	if !errors.Is(err, ErrInvalidPayload) {
		t.Errorf("expected ErrInvalidPayload for mixed batch, got %v", err)
	}
	if len(repo.insertedReceipts) != 0 {
		t.Errorf("expected zero writes for invalid batch, got %d receipts", len(repo.insertedReceipts))
	}
}

func TestService_ProcessMessages_EmptyPhoneNumberID_ReturnsInvalidPayload(t *testing.T) {
	repo := newFakeRepo()
	svc := buildService(repo)
	// Build a payload where phone_number_id is explicitly empty.
	payload := &MetaWebhookPayload{
		Object: "whatsapp_business_account",
		Entry: []MetaEntry{
			{
				ID: "entry-001",
				Changes: []MetaChange{
					{
						Field: "messages",
						Value: MetaChangeValue{
							Metadata: MetaMetadata{PhoneNumberID: ""},
							Messages: []MetaMessage{
								{ID: "msg-001", From: "5511", Timestamp: "1715000000", Type: "text"},
							},
						},
					},
				},
			},
		},
	}
	err := svc.ProcessMessages(context.Background(), &IntakeInput{
		IngressRequestID: "req-inv",
		ReceivedAt:       time.Now().UTC(),
		RawPayload:       rawPayload(t, payload),
		Payload:          payload,
	})
	if !errors.Is(err, ErrInvalidPayload) {
		t.Errorf("expected ErrInvalidPayload, got %v", err)
	}
	if len(repo.insertedReceipts) != 0 {
		t.Errorf("no receipts should be inserted for invalid payload, got %d", len(repo.insertedReceipts))
	}
}

func TestService_ProcessMessages_MixedBatch_ChangeWithoutMessages_Ignored(t *testing.T) {
	repo := newFakeRepo()
	svc := buildService(repo)
	// Batch: one change with a valid message, one change with no messages and no phone_number_id.
	// The empty change must be silently skipped; ErrInvalidPayload must NOT be returned.
	payload := &MetaWebhookPayload{
		Object: "whatsapp_business_account",
		Entry: []MetaEntry{
			{
				ID: "entry-001",
				Changes: []MetaChange{
					{
						Field: "messages",
						Value: MetaChangeValue{
							Metadata: MetaMetadata{PhoneNumberID: "ph-001"},
							Messages: []MetaMessage{
								{ID: "msg-001", From: "5511", Timestamp: "1715000000", Type: "text"},
							},
						},
					},
					{
						Field: "statuses",
						Value: MetaChangeValue{
							// No messages and no phone_number_id — must be ignored.
							Metadata: MetaMetadata{PhoneNumberID: ""},
							Messages: nil,
						},
					},
				},
			},
		},
	}
	err := svc.ProcessMessages(context.Background(), &IntakeInput{
		IngressRequestID: "req-mixed-no-msgs",
		ReceivedAt:       time.Now().UTC(),
		RawPayload:       rawPayload(t, payload),
		Payload:          payload,
	})
	if err != nil {
		t.Fatalf("expected success for mixed batch with empty change, got: %v", err)
	}
	if len(repo.insertedReceipts) != 1 {
		t.Errorf("inserted receipts = %d, want 1", len(repo.insertedReceipts))
	}
	if len(repo.insertedReceipts) == 1 && repo.insertedReceipts[0].MessageID != "msg-001" {
		t.Errorf("MessageID = %q, want \"msg-001\"", repo.insertedReceipts[0].MessageID)
	}
}

func TestService_ProcessMessages_EmptyMessageID_ReturnsInvalidPayload(t *testing.T) {
	repo := newFakeRepo()
	svc := buildService(repo)
	// Build a payload where message.id is explicitly empty.
	payload := &MetaWebhookPayload{
		Object: "whatsapp_business_account",
		Entry: []MetaEntry{
			{
				ID: "entry-001",
				Changes: []MetaChange{
					{
						Field: "messages",
						Value: MetaChangeValue{
							Metadata: MetaMetadata{PhoneNumberID: "ph-001"},
							Messages: []MetaMessage{
								{ID: "", From: "5511", Timestamp: "1715000000", Type: "text"},
							},
						},
					},
				},
			},
		},
	}
	err := svc.ProcessMessages(context.Background(), &IntakeInput{
		IngressRequestID: "req-inv",
		ReceivedAt:       time.Now().UTC(),
		RawPayload:       rawPayload(t, payload),
		Payload:          payload,
	})
	if !errors.Is(err, ErrInvalidPayload) {
		t.Errorf("expected ErrInvalidPayload, got %v", err)
	}
	if len(repo.insertedReceipts) != 0 {
		t.Errorf("no receipts should be inserted for invalid payload, got %d", len(repo.insertedReceipts))
	}
}
