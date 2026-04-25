package idempotency_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/villenneve/vil-core/internal/idempotency"
)

// ─── Fake repository ─────────────────────────────────────────────────────────

type fakeRepo struct {
	records map[string]*idempotency.Key // key: "tenantID:operation:idempotencyKey"
}

func newFake() *fakeRepo {
	return &fakeRepo{records: make(map[string]*idempotency.Key)}
}

func lookupKey(tenantID, operation, idemKey string) string {
	return tenantID + ":" + operation + ":" + idemKey
}

func (r *fakeRepo) Reserve(_ context.Context, key idempotency.Key) error {
	k := lookupKey(key.TenantID, key.Operation, key.IdempotencyKey)
	if _, exists := r.records[k]; exists {
		return idempotency.ErrDuplicate
	}
	r.records[k] = &key
	return nil
}

func (r *fakeRepo) FindByTenantOpKey(_ context.Context, tenantID, operation, idemKey string) (*idempotency.Key, error) {
	k := lookupKey(tenantID, operation, idemKey)
	rec, ok := r.records[k]
	if !ok {
		return nil, idempotency.ErrNotFound
	}
	return rec, nil
}

func (r *fakeRepo) Commit(_ context.Context, tenantID, operation, idemKey, resourceID string, updatedAt time.Time) error {
	k := lookupKey(tenantID, operation, idemKey)
	rec, ok := r.records[k]
	if !ok {
		return idempotency.ErrNotFound
	}
	rec.Status = idempotency.StatusCommitted
	rec.ResourceID = resourceID
	rec.UpdatedAt = updatedAt
	return nil
}

func (r *fakeRepo) Fail(_ context.Context, tenantID, operation, idemKey string, updatedAt time.Time) error {
	k := lookupKey(tenantID, operation, idemKey)
	rec, ok := r.records[k]
	if !ok {
		return idempotency.ErrNotFound
	}
	rec.Status = idempotency.StatusFailed
	rec.UpdatedAt = updatedAt
	return nil
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func newKey(tenantID, idemKey, requestHash string) idempotency.Key {
	now := time.Now().UTC()
	return idempotency.Key{
		ID:             primitive.NewObjectID(),
		Operation:      "checkout.create",
		OrganizationID: "org-001",
		TenantID:       tenantID,
		IdempotencyKey: idemKey,
		RequestHash:    requestHash,
		ResourceType:   "order",
		ResourceID:     "",
		Status:         idempotency.StatusPending,
		ExpiresAt:      now.Add(24 * time.Hour),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}

// ─── Tests ───────────────────────────────────────────────────────────────────

// Sequential retries with the same key and same hash must return ErrDuplicate on the
// second Reserve call; the caller should then fetch the existing record and replay it.
func TestIdempotency_SequentialRetries_SameKey_SameHash(t *testing.T) {
	repo := newFake()
	key := newKey("t-001", "key-abc", "hash-xyz")

	if err := repo.Reserve(context.Background(), key); err != nil {
		t.Fatalf("first Reserve failed: %v", err)
	}
	// Commit the record to simulate a completed operation.
	if err := repo.Commit(context.Background(), "t-001", "checkout.create", "key-abc", "order-nsu-001", time.Now().UTC()); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// Second Reserve must fail with ErrDuplicate.
	err := repo.Reserve(context.Background(), newKey("t-001", "key-abc", "hash-xyz"))
	if !errors.Is(err, idempotency.ErrDuplicate) {
		t.Fatalf("expected ErrDuplicate on retry, got %v", err)
	}

	// Caller should look up existing record and return the committed resource.
	existing, err := repo.FindByTenantOpKey(context.Background(), "t-001", "checkout.create", "key-abc")
	if err != nil {
		t.Fatalf("FindByTenantOpKey failed: %v", err)
	}
	if existing.Status != idempotency.StatusCommitted {
		t.Errorf("expected status=committed, got %q", existing.Status)
	}
	if existing.ResourceID != "order-nsu-001" {
		t.Errorf("expected resource_id=order-nsu-001, got %q", existing.ResourceID)
	}
}

// Same key with a different request_hash signals a protocol violation.
func TestIdempotency_SameKey_DifferentHash_ConflictDetected(t *testing.T) {
	repo := newFake()
	_ = repo.Reserve(context.Background(), newKey("t-001", "key-abc", "hash-xyz"))

	// Simulate the conflict-check logic a service layer would apply.
	err := repo.Reserve(context.Background(), newKey("t-001", "key-abc", "hash-DIFFERENT"))
	if !errors.Is(err, idempotency.ErrDuplicate) {
		t.Fatalf("expected ErrDuplicate on duplicate key insert, got %v", err)
	}

	existing, err := repo.FindByTenantOpKey(context.Background(), "t-001", "checkout.create", "key-abc")
	if err != nil {
		t.Fatalf("FindByTenantOpKey failed: %v", err)
	}
	if existing.RequestHash != "hash-xyz" {
		t.Errorf("existing hash should be original, got %q", existing.RequestHash)
	}
	// The service layer must return ErrRequestHashConflict when hashes differ.
	if existing.RequestHash == "hash-DIFFERENT" {
		t.Error("request hash must not have been overwritten")
	}
}

// Keys are tenant-scoped: the same idempotency key for different tenants must not collide.
func TestIdempotency_SameKey_DifferentTenants_Allowed(t *testing.T) {
	repo := newFake()
	_ = repo.Reserve(context.Background(), newKey("t-001", "key-abc", "hash-xyz"))

	err := repo.Reserve(context.Background(), newKey("t-002", "key-abc", "hash-xyz"))
	if err != nil {
		t.Fatalf("same key in different tenants should succeed, got: %v", err)
	}
}

func TestIdempotency_Fail_MarksRecord(t *testing.T) {
	repo := newFake()
	_ = repo.Reserve(context.Background(), newKey("t-001", "key-fail", "hash-fail"))

	if err := repo.Fail(context.Background(), "t-001", "checkout.create", "key-fail", time.Now().UTC()); err != nil {
		t.Fatalf("Fail returned error: %v", err)
	}

	existing, err := repo.FindByTenantOpKey(context.Background(), "t-001", "checkout.create", "key-fail")
	if err != nil {
		t.Fatalf("FindByTenantOpKey failed: %v", err)
	}
	if existing.Status != idempotency.StatusFailed {
		t.Errorf("expected status=failed, got %q", existing.Status)
	}
}

func TestIdempotency_FindByTenantOpKey_NotFound(t *testing.T) {
	repo := newFake()
	_, err := repo.FindByTenantOpKey(context.Background(), "t-001", "checkout.create", "nonexistent")
	if !errors.Is(err, idempotency.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
