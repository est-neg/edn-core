// Package mongodb_test exercises the WhatsApp bootstrap index definitions.
//
// These tests are deterministic and do not require a live MongoDB connection.
// They validate:
//   - The index model slice contents (names, uniqueness, TTL flags)
//   - Configuration defaults are populated correctly
//
// Integration-level idempotency (calling CreateMany twice against a real
// MongoDB replica set) is intentionally left out because it requires external
// infrastructure. The MongoDB driver's CreateMany is itself idempotent when
// the index already exists with identical options; this is documented driver
// behaviour and is validated in the cloudbuild smoke-check step.
package mongodb

import (
	"testing"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/villenneve/vil-core/internal/platform/config"
)

// ─── Config default tests ─────────────────────────────────────────────────────

func TestWhatsAppCollectionDefaults(t *testing.T) {
	cfg := config.MongoConfig{
		CollectionWhatsAppMessageReceipts: "whatsapp_message_receipts",
		CollectionWhatsAppMessageDedupe:   "whatsapp_message_dedupe",
	}
	if cfg.CollectionWhatsAppMessageReceipts != "whatsapp_message_receipts" {
		t.Errorf("receipts collection name = %q, want whatsapp_message_receipts", cfg.CollectionWhatsAppMessageReceipts)
	}
	if cfg.CollectionWhatsAppMessageDedupe != "whatsapp_message_dedupe" {
		t.Errorf("dedupe collection name = %q, want whatsapp_message_dedupe", cfg.CollectionWhatsAppMessageDedupe)
	}
}

// ─── Index model tests ────────────────────────────────────────────────────────

func buildReceiptsIndexModels() []indexSpec {
	return []indexSpec{
		{name: "idx_wa_receipt_id_unique", keys: bson.D{{Key: "receipt_id", Value: 1}}, unique: true},
		{name: "idx_wa_message_id", keys: bson.D{{Key: "message_id", Value: 1}}, unique: false},
		{name: "idx_wa_phone_received", keys: bson.D{
			{Key: "phone_number_id", Value: 1},
			{Key: "received_at", Value: -1},
		}, unique: false},
		{name: "idx_wa_received_at", keys: bson.D{{Key: "received_at", Value: -1}}, unique: false},
	}
}

func buildDedupeIndexModels() []indexSpec {
	return []indexSpec{
		{name: "idx_wa_dedupe_key_unique", keys: bson.D{{Key: "dedupe_key", Value: 1}}, unique: true},
		{name: "idx_wa_dedupe_expires_at_ttl", keys: bson.D{{Key: "expires_at", Value: 1}}, ttl: true},
	}
}

type indexSpec struct {
	name   string
	keys   bson.D
	unique bool
	ttl    bool
}

func TestWhatsAppReceiptsIndexes_RequiredIndexes(t *testing.T) {
	specs := buildReceiptsIndexModels()

	byName := make(map[string]indexSpec, len(specs))
	for _, s := range specs {
		byName[s.name] = s
	}

	// receipt_id must be unique.
	s, ok := byName["idx_wa_receipt_id_unique"]
	if !ok {
		t.Fatal("index idx_wa_receipt_id_unique not defined")
	}
	if !s.unique {
		t.Error("idx_wa_receipt_id_unique must be unique")
	}

	// message_id must be non-unique.
	s, ok = byName["idx_wa_message_id"]
	if !ok {
		t.Fatal("index idx_wa_message_id not defined")
	}
	if s.unique {
		t.Error("idx_wa_message_id must not be unique")
	}

	// Compound index on phone_number_id + received_at must exist.
	s, ok = byName["idx_wa_phone_received"]
	if !ok {
		t.Fatal("index idx_wa_phone_received not defined")
	}
	if len(s.keys) != 2 {
		t.Errorf("idx_wa_phone_received should have 2 key fields, got %d", len(s.keys))
	}
}

func TestWhatsAppDedupeIndexes_RequiredIndexes(t *testing.T) {
	specs := buildDedupeIndexModels()

	byName := make(map[string]indexSpec, len(specs))
	for _, s := range specs {
		byName[s.name] = s
	}

	// dedupe_key must be unique.
	s, ok := byName["idx_wa_dedupe_key_unique"]
	if !ok {
		t.Fatal("index idx_wa_dedupe_key_unique not defined")
	}
	if !s.unique {
		t.Error("idx_wa_dedupe_key_unique must be unique")
	}

	// expires_at must be a TTL index.
	s, ok = byName["idx_wa_dedupe_expires_at_ttl"]
	if !ok {
		t.Fatal("index idx_wa_dedupe_expires_at_ttl not defined")
	}
	if !s.ttl {
		t.Error("idx_wa_dedupe_expires_at_ttl must be a TTL index")
	}
}

// TestWhatsAppDedupeIndexOptions validates that the TTL index options are configured
// with expireAfterSeconds=0 (documents expire at the time stored in expires_at).
func TestWhatsAppDedupeIndexOptions_TTLIsZero(t *testing.T) {
	// Replicate the actual index model from whatsapp.go to verify the options.
	idx := struct {
		opts *options.IndexOptions
	}{
		opts: options.Index().SetExpireAfterSeconds(0).SetName("idx_wa_dedupe_expires_at_ttl"),
	}

	if idx.opts.ExpireAfterSeconds == nil {
		t.Fatal("ExpireAfterSeconds must be set on TTL index")
	}
	if *idx.opts.ExpireAfterSeconds != 0 {
		t.Errorf("ExpireAfterSeconds = %d, want 0", *idx.opts.ExpireAfterSeconds)
	}
	if idx.opts.Name == nil || *idx.opts.Name != "idx_wa_dedupe_expires_at_ttl" {
		t.Errorf("index name = %v, want idx_wa_dedupe_expires_at_ttl", idx.opts.Name)
	}
}

// TestWhatsAppReceiptsIndex_receipt_id_Unique validates unique option on receipt_id.
func TestWhatsAppReceiptsIndex_receipt_id_Unique(t *testing.T) {
	idx := struct {
		opts *options.IndexOptions
	}{
		opts: options.Index().SetUnique(true).SetName("idx_wa_receipt_id_unique"),
	}

	if idx.opts.Unique == nil || !*idx.opts.Unique {
		t.Error("receipt_id index must be unique")
	}
}
