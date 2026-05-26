package main

import (
	"testing"
	"time"

	"github.com/villenneve/vil-core/internal/checkout"
	"github.com/villenneve/vil-core/internal/idempotency"
	"github.com/villenneve/vil-core/internal/payments"
)

// -- checkStuckEligibility --

func TestCheckStuckEligibility_PassesCreatedWithAttemptedAt(t *testing.T) {
	ts := time.Now()
	order := &checkout.Order{
		OrderNSU:                  "NSU001",
		Status:                    string(payments.OrderStatusCreated),
		ProviderCreateAttemptedAt: &ts,
	}
	if err := checkStuckEligibility(order); err != nil {
		t.Fatalf("expected eligible, got: %v", err)
	}
}

func TestCheckStuckEligibility_PassesCheckoutCreatedWithAttemptedAt(t *testing.T) {
	ts := time.Now()
	order := &checkout.Order{
		OrderNSU:                  "NSU002",
		Status:                    string(payments.OrderStatusCheckoutCreated),
		ProviderCreateAttemptedAt: &ts,
	}
	if err := checkStuckEligibility(order); err != nil {
		t.Fatalf("expected eligible, got: %v", err)
	}
}

func TestCheckStuckEligibility_PassesPendingWithAttemptedAt(t *testing.T) {
	ts := time.Now()
	order := &checkout.Order{
		OrderNSU:                  "NSU003",
		Status:                    string(payments.OrderStatusPending),
		ProviderCreateAttemptedAt: &ts,
	}
	if err := checkStuckEligibility(order); err != nil {
		t.Fatalf("expected eligible, got: %v", err)
	}
}

func TestCheckStuckEligibility_BlocksProviderURL(t *testing.T) {
	ts := time.Now()
	order := &checkout.Order{
		OrderNSU:                  "NSU004",
		Status:                    string(payments.OrderStatusCreated),
		ProviderCheckoutURL:       "https://pay.example.com/link",
		ProviderCreateAttemptedAt: &ts,
	}
	if err := checkStuckEligibility(order); err == nil {
		t.Fatal("expected blocked for order with provider_checkout_url")
	}
}

func TestCheckStuckEligibility_BlocksMissingAttemptedAt(t *testing.T) {
	order := &checkout.Order{
		OrderNSU: "NSU005",
		Status:   string(payments.OrderStatusCreated),
		// ProviderCreateAttemptedAt is nil
	}
	err := checkStuckEligibility(order)
	if err == nil {
		t.Fatal("expected blocked when provider_create_attempted_at is nil")
	}
}

func TestCheckStuckEligibility_BlocksTerminalStatusFailed(t *testing.T) {
	ts := time.Now()
	order := &checkout.Order{
		OrderNSU:                  "NSU006",
		Status:                    string(payments.OrderStatusFailed),
		ProviderCreateAttemptedAt: &ts,
	}
	if err := checkStuckEligibility(order); err == nil {
		t.Fatal("expected blocked for failed order")
	}
}

func TestCheckStuckEligibility_BlocksTerminalStatusPaid(t *testing.T) {
	ts := time.Now()
	order := &checkout.Order{
		OrderNSU:                  "NSU007",
		Status:                    string(payments.OrderStatusPaid),
		ProviderCreateAttemptedAt: &ts,
	}
	if err := checkStuckEligibility(order); err == nil {
		t.Fatal("expected blocked for paid order")
	}
}

func TestCheckStuckEligibility_BlocksTerminalStatusExpired(t *testing.T) {
	ts := time.Now()
	order := &checkout.Order{
		OrderNSU:                  "NSU008",
		Status:                    string(payments.OrderStatusExpired),
		ProviderCreateAttemptedAt: &ts,
	}
	if err := checkStuckEligibility(order); err == nil {
		t.Fatal("expected blocked for expired order")
	}
}

func TestCheckStuckEligibility_BlocksTerminalStatusPendingReview(t *testing.T) {
	ts := time.Now()
	order := &checkout.Order{
		OrderNSU:                  "NSU009",
		Status:                    string(payments.OrderStatusPendingReview),
		ProviderCreateAttemptedAt: &ts,
	}
	if err := checkStuckEligibility(order); err == nil {
		t.Fatal("expected blocked for pending_review order")
	}
}

func TestCheckStuckEligibility_ErrorMentionsStatus(t *testing.T) {
	ts := time.Now()
	order := &checkout.Order{
		OrderNSU:                  "NSU010",
		Status:                    string(payments.OrderStatusPaid),
		ProviderCreateAttemptedAt: &ts,
	}
	err := checkStuckEligibility(order)
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() == "" {
		t.Error("error message should be non-empty")
	}
}

// -- decidePlan --

// helper builds an open eligible order.
func eligibleOpenOrder(status payments.OrderStatus) *checkout.Order {
	ts := time.Now()
	return &checkout.Order{
		OrderNSU:                  "NSU-OPEN",
		Status:                    string(status),
		ProviderCreateAttemptedAt: &ts,
	}
}

func failedOrder() *checkout.Order {
	return &checkout.Order{
		OrderNSU: "NSU-DONE",
		Status:   string(payments.OrderStatusFailed),
	}
}

// Committed idempotency is a hard stop regardless of order state (open or already failed).
func TestDecidePlan_CommittedIdemIsHardStopOnOpenOrder(t *testing.T) {
	order := eligibleOpenOrder(payments.OrderStatusCreated)
	idem := &idempotency.Key{Status: idempotency.StatusCommitted, IdempotencyKey: "k1"}
	plan := decidePlan(order, idem)
	if plan.action != actionHardStop {
		t.Fatalf("expected actionHardStop, got %v", plan.action)
	}
}

func TestDecidePlan_CommittedIdemIsHardStopOnFailedOrder(t *testing.T) {
	idem := &idempotency.Key{Status: idempotency.StatusCommitted, IdempotencyKey: "k2"}
	plan := decidePlan(failedOrder(), idem)
	if plan.action != actionHardStop {
		t.Fatalf("expected actionHardStop, got %v", plan.action)
	}
}

// Eligible open order + pending idempotency → fail both.
func TestDecidePlan_EligibleOpenPlusPendingIdem(t *testing.T) {
	order := eligibleOpenOrder(payments.OrderStatusCreated)
	idem := &idempotency.Key{Status: idempotency.StatusPending, IdempotencyKey: "k3"}
	plan := decidePlan(order, idem)
	if plan.action != actionFailBoth {
		t.Fatalf("expected actionFailBoth, got %v", plan.action)
	}
}

func TestDecidePlan_EligibleOpenPlusPendingIdem_CheckoutCreated(t *testing.T) {
	order := eligibleOpenOrder(payments.OrderStatusCheckoutCreated)
	idem := &idempotency.Key{Status: idempotency.StatusPending, IdempotencyKey: "k3a"}
	plan := decidePlan(order, idem)
	if plan.action != actionFailBoth {
		t.Fatalf("expected actionFailBoth, got %v", plan.action)
	}
}

func TestDecidePlan_EligibleOpenPlusPendingIdem_Pending(t *testing.T) {
	order := eligibleOpenOrder(payments.OrderStatusPending)
	idem := &idempotency.Key{Status: idempotency.StatusPending, IdempotencyKey: "k3b"}
	plan := decidePlan(order, idem)
	if plan.action != actionFailBoth {
		t.Fatalf("expected actionFailBoth, got %v", plan.action)
	}
}

// Eligible open order + no idempotency record → still fail both (order side only).
func TestDecidePlan_EligibleOpenPlusNoIdem(t *testing.T) {
	order := eligibleOpenOrder(payments.OrderStatusCreated)
	plan := decidePlan(order, nil)
	if plan.action != actionFailBoth {
		t.Fatalf("expected actionFailBoth (no idem record), got %v", plan.action)
	}
}

// Eligible open order + already-failed idempotency → fail order, no-op idem.
func TestDecidePlan_EligibleOpenPlusFailedIdem(t *testing.T) {
	order := eligibleOpenOrder(payments.OrderStatusCreated)
	idem := &idempotency.Key{Status: idempotency.StatusFailed, IdempotencyKey: "k4"}
	plan := decidePlan(order, idem)
	if plan.action != actionFailBoth {
		t.Fatalf("expected actionFailBoth (idem side is no-op inside the action), got %v", plan.action)
	}
}

// Already failed order + pending idempotency → finish idem only.
func TestDecidePlan_AlreadyFailedOrderPlusPendingIdem(t *testing.T) {
	idem := &idempotency.Key{Status: idempotency.StatusPending, IdempotencyKey: "k5"}
	plan := decidePlan(failedOrder(), idem)
	if plan.action != actionFailIdemOnly {
		t.Fatalf("expected actionFailIdemOnly, got %v", plan.action)
	}
	if plan.idemKey == nil || plan.idemKey.IdempotencyKey != "k5" {
		t.Fatal("idemKey should be preserved on the plan")
	}
}

// Already failed order + failed idempotency → no-op.
func TestDecidePlan_AlreadyFailedOrderPlusFailedIdem(t *testing.T) {
	idem := &idempotency.Key{Status: idempotency.StatusFailed, IdempotencyKey: "k6"}
	plan := decidePlan(failedOrder(), idem)
	if plan.action != actionNoOp {
		t.Fatalf("expected actionNoOp, got %v", plan.action)
	}
}

// Already failed order + no idempotency record → no-op.
func TestDecidePlan_AlreadyFailedOrderPlusNoIdem(t *testing.T) {
	plan := decidePlan(failedOrder(), nil)
	if plan.action != actionNoOp {
		t.Fatalf("expected actionNoOp, got %v", plan.action)
	}
}

// Ineligible terminal statuses block via hard stop (not failed).
func TestDecidePlan_PaidOrderIsHardStop(t *testing.T) {
	ts := time.Now()
	order := &checkout.Order{
		OrderNSU:                  "NSU-PAID",
		Status:                    string(payments.OrderStatusPaid),
		ProviderCreateAttemptedAt: &ts,
	}
	plan := decidePlan(order, nil)
	if plan.action != actionHardStop {
		t.Fatalf("expected actionHardStop for paid order, got %v", plan.action)
	}
}

func TestDecidePlan_ExpiredOrderIsHardStop(t *testing.T) {
	ts := time.Now()
	order := &checkout.Order{
		OrderNSU:                  "NSU-EXP",
		Status:                    string(payments.OrderStatusExpired),
		ProviderCreateAttemptedAt: &ts,
	}
	plan := decidePlan(order, nil)
	if plan.action != actionHardStop {
		t.Fatalf("expected actionHardStop for expired order, got %v", plan.action)
	}
}

func TestDecidePlan_PendingReviewOrderIsHardStop(t *testing.T) {
	ts := time.Now()
	order := &checkout.Order{
		OrderNSU:                  "NSU-PR",
		Status:                    string(payments.OrderStatusPendingReview),
		ProviderCreateAttemptedAt: &ts,
	}
	plan := decidePlan(order, nil)
	if plan.action != actionHardStop {
		t.Fatalf("expected actionHardStop for pending_review order, got %v", plan.action)
	}
}

// -- multiFlag --

func TestMultiFlag_SingleValue(t *testing.T) {
	var m multiFlag
	if err := m.Set("NSU001"); err != nil {
		t.Fatal(err)
	}
	if len(m) != 1 || m[0] != "NSU001" {
		t.Fatalf("unexpected: %v", m)
	}
}

func TestMultiFlag_CommaSeparated(t *testing.T) {
	var m multiFlag
	if err := m.Set("NSU001,NSU002,NSU003"); err != nil {
		t.Fatal(err)
	}
	if len(m) != 3 {
		t.Fatalf("expected 3 values, got %d: %v", len(m), m)
	}
}

func TestMultiFlag_RepeatedSet(t *testing.T) {
	var m multiFlag
	_ = m.Set("NSU001")
	_ = m.Set("NSU002")
	if len(m) != 2 {
		t.Fatalf("expected 2 values, got %d: %v", len(m), m)
	}
}

func TestMultiFlag_TrimsWhitespace(t *testing.T) {
	var m multiFlag
	if err := m.Set("  NSU001 , NSU002  "); err != nil {
		t.Fatal(err)
	}
	if m[0] != "NSU001" || m[1] != "NSU002" {
		t.Fatalf("expected trimmed values, got: %v", m)
	}
}

func TestMultiFlag_SpaceSeparated(t *testing.T) {
	// PowerShell may collapse -order-nsu NSU1,NSU2 into a single arg "NSU1 NSU2".
	var m multiFlag
	if err := m.Set("NSU001 NSU002"); err != nil {
		t.Fatal(err)
	}
	if len(m) != 2 || m[0] != "NSU001" || m[1] != "NSU002" {
		t.Fatalf("expected [NSU001 NSU002], got: %v", m)
	}
}

func TestMultiFlag_MixedCommaAndWhitespace(t *testing.T) {
	var m multiFlag
	if err := m.Set("NSU001, NSU002 NSU003,NSU004"); err != nil {
		t.Fatal(err)
	}
	want := []string{"NSU001", "NSU002", "NSU003", "NSU004"}
	if len(m) != len(want) {
		t.Fatalf("expected %v, got %v", want, m)
	}
	for i, v := range want {
		if m[i] != v {
			t.Fatalf("index %d: expected %q, got %q", i, v, m[i])
		}
	}
}

func TestMultiFlag_TabSeparated(t *testing.T) {
	var m multiFlag
	if err := m.Set("NSU001\tNSU002"); err != nil {
		t.Fatal(err)
	}
	if len(m) != 2 || m[0] != "NSU001" || m[1] != "NSU002" {
		t.Fatalf("expected [NSU001 NSU002], got: %v", m)
	}
}
