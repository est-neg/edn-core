// Command fail-stuck-checkouts is an operator cleanup tool that marks poisoned
// checkout orders as failed and fails their matching checkout idempotency record.
//
// A "stuck" order is one where the provider create call was attempted but never
// completed (no URL delivered), leaving the order in an ambiguous pending state.
//
// Eligibility requires ALL of the following to be true:
//   - status is one of: created, checkout_created, pending
//   - provider_checkout_url is empty
//   - provider_create_attempted_at is set
//
// The tool acquires the distributed Redis order lock before reading or writing,
// so it is safe to run concurrently with the live service.
//
// Rerun safety:
//   - If the order is already failed and the idempotency record is still pending,
//     the tool will finish only the idempotency side (safe rerun after partial failure).
//   - If both are already failed/clean, the tool reports a no-op.
//   - If the idempotency record is committed, the tool hard-stops before any write.
//
// Usage:
//
//	go run ./cmd/fail-stuck-checkouts -order-nsu <NSU>
//	go run ./cmd/fail-stuck-checkouts -order-nsu <NSU1> -order-nsu <NSU2>
//	go run ./cmd/fail-stuck-checkouts -order-nsu <NSU1>,<NSU2>,<NSU3>
//	go run ./cmd/fail-stuck-checkouts -order-nsu <NSU> -dry-run
//
// The command loads configuration from the environment (VIL_* prefix) or a config file,
// same as any other service in this repository.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/villenneve/vil-core/internal/checkout"
	"github.com/villenneve/vil-core/internal/idempotency"
	"github.com/villenneve/vil-core/internal/payments"
	"github.com/villenneve/vil-core/internal/platform/config"
	mongoplat "github.com/villenneve/vil-core/internal/platform/mongodb"
	redisplat "github.com/villenneve/vil-core/internal/platform/redis"
)

const checkoutCreateOperation = "checkout.create"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// multiFlag is a repeatable string flag that also accepts comma-separated values.
type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error {
	for _, s := range strings.FieldsFunc(v, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t'
	}) {
		s = strings.TrimSpace(s)
		if s != "" {
			*m = append(*m, s)
		}
	}
	return nil
}

func run() error {
	var nsus multiFlag
	flag.Var(&nsus, "order-nsu", "NSU of an order to mark failed (repeatable; comma-separated accepted)")
	dryRun := flag.Bool("dry-run", false, "Print planned changes without mutating MongoDB")
	flag.Parse()

	if len(nsus) == 0 {
		flag.Usage()
		return errors.New("-order-nsu is required")
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if cfg.MongoDB.URI == "" {
		return errors.New("VIL_MONGODB_URI is required")
	}
	if cfg.Redis.Addr == "" {
		return errors.New("VIL_REDIS_ADDR is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	mongoClient, err := mongoplat.New(ctx, cfg.MongoDB)
	if err != nil {
		return fmt.Errorf("connect mongodb: %w", err)
	}
	defer mongoClient.Disconnect(context.Background()) //nolint:errcheck

	redisClient, err := redisplat.New(ctx, cfg.Redis)
	if err != nil {
		return fmt.Errorf("connect redis: %w", err)
	}
	defer redisClient.Close() //nolint:errcheck

	orderRepo := checkout.NewMongoOrderRepository(mongoClient, cfg.MongoDB)
	idemRepo := idempotency.NewMongoRepository(mongoClient, cfg.MongoDB)
	store := checkout.NewRedisStore(redisClient)

	var anyFailed bool
	for _, nsu := range nsus {
		if err := processNSU(ctx, nsu, orderRepo, idemRepo, store, *dryRun); err != nil {
			fmt.Fprintf(os.Stderr, "[error] %s: %v\n", nsu, err)
			anyFailed = true
		}
	}

	if anyFailed {
		return errors.New("one or more orders could not be safely processed; review errors above")
	}
	return nil
}

func processNSU(
	ctx context.Context,
	nsu string,
	orderRepo checkout.OrderRepository,
	idemRepo idempotency.Repository,
	store checkout.CheckoutStore,
	dryRun bool,
) error {
	// 1. Acquire the distributed order lock — fail closed if unavailable.
	token := uuid.New().String()
	if err := store.AcquireOrderLock(ctx, nsu, token); err != nil {
		if errors.Is(err, checkout.ErrLockNotAcquired) {
			return fmt.Errorf("order %q lock is held by another process; retry later", nsu)
		}
		return fmt.Errorf("acquire order lock: %w", err)
	}
	defer func() {
		if err := store.ReleaseOrderLock(context.Background(), nsu, token); err != nil {
			fmt.Fprintf(os.Stderr, "[warn] release order lock %s: %v\n", nsu, err)
		}
	}()

	// 2. Re-read the order under lock.
	order, err := orderRepo.FindByNSU(ctx, nsu)
	if err != nil {
		if errors.Is(err, checkout.ErrOrderNotFound) {
			return fmt.Errorf("order %q not found", nsu)
		}
		return fmt.Errorf("fetch order: %w", err)
	}

	// 3. Resolve idempotency record before deciding the plan.
	idemKey, idemErr := idemRepo.FindByTenantOpResourceID(ctx, order.TenantID, checkoutCreateOperation, nsu)
	if idemErr != nil && !errors.Is(idemErr, idempotency.ErrNotFound) {
		return fmt.Errorf("lookup idempotency record: %w", idemErr)
	}
	if errors.Is(idemErr, idempotency.ErrNotFound) {
		idemKey = nil
	}

	// 4. Decide the plan — committed idempotency is a hard stop before any write.
	plan := decidePlan(order, idemKey)

	fmt.Printf("[%s] %s\n", nsu, plan.reason)
	fmt.Printf("  order status: %s\n", order.Status)

	switch plan.action {
	case actionHardStop:
		return fmt.Errorf("hard stop for %q: %s", nsu, plan.reason)

	case actionNoOp:
		fmt.Printf("  [no-op] already clean\n\n")
		return nil
	}

	if dryRun {
		printDryRun(nsu, plan)
		fmt.Printf("  [dry-run] no changes written\n\n")
		return nil
	}

	// 5. Execute the plan.
	now := time.Now().UTC()

	switch plan.action {
	case actionFailBoth:
		if err := orderRepo.UpdateStatus(ctx, nsu, string(payments.OrderStatusFailed), now); err != nil {
			return fmt.Errorf("mark order failed: %w", err)
		}
		fmt.Printf("  order status -> failed (updated_at: %s)\n", now.Format(time.RFC3339))
		// Invalidate order status cache after successful mutation.
		if err := store.InvalidateOrderStatus(ctx, nsu); err != nil {
			fmt.Fprintf(os.Stderr, "[warn] invalidate order status cache %s: %v\n", nsu, err)
		}
		if plan.idemKey != nil && plan.idemKey.Status == idempotency.StatusPending {
			if err := idemRepo.Fail(ctx, order.TenantID, checkoutCreateOperation, plan.idemKey.IdempotencyKey, now); err != nil {
				return fmt.Errorf("fail idempotency record: %w", err)
			}
			fmt.Printf("  idempotency record -> failed (key: %s)\n\n", plan.idemKey.IdempotencyKey)
		} else if plan.idemKey != nil {
			fmt.Printf("  idempotency record already %s — no-op (key: %s)\n\n", plan.idemKey.Status, plan.idemKey.IdempotencyKey)
		} else {
			fmt.Printf("  idempotency record not found — order failed; nothing to update\n\n")
		}

	case actionFailIdemOnly:
		if err := idemRepo.Fail(ctx, order.TenantID, checkoutCreateOperation, plan.idemKey.IdempotencyKey, now); err != nil {
			return fmt.Errorf("fail idempotency record: %w", err)
		}
		fmt.Printf("  idempotency record -> failed (key: %s)\n\n", plan.idemKey.IdempotencyKey)
	}

	return nil
}

// printDryRun prints what the plan would do without executing writes.
func printDryRun(nsu string, plan cleanupPlan) {
	switch plan.action {
	case actionFailBoth:
		fmt.Printf("  [dry-run] would set order status -> failed\n")
		if plan.idemKey != nil && plan.idemKey.Status == idempotency.StatusPending {
			fmt.Printf("  [dry-run] would fail idempotency record (key: %s)\n", plan.idemKey.IdempotencyKey)
		} else if plan.idemKey != nil {
			fmt.Printf("  [dry-run] idempotency record already %s — no-op (key: %s)\n", plan.idemKey.Status, plan.idemKey.IdempotencyKey)
		} else {
			fmt.Printf("  [dry-run] no idempotency record found for %s + resource_id %s\n", checkoutCreateOperation, nsu)
		}
	case actionFailIdemOnly:
		fmt.Printf("  [dry-run] order already failed — would fail idempotency record only (key: %s)\n", plan.idemKey.IdempotencyKey)
	}
}

// checkStuckEligibility returns nil when the order is a safe candidate for stuck-checkout cleanup.
// It is conservative: all three conditions must be met.
func checkStuckEligibility(order *checkout.Order) error {
	switch payments.OrderStatus(order.Status) {
	case payments.OrderStatusCreated, payments.OrderStatusCheckoutCreated, payments.OrderStatusPending:
		// eligible status range — continue
	default:
		return fmt.Errorf("order %q has status %q which is not eligible for stuck-checkout cleanup (must be created, checkout_created, or pending)", order.OrderNSU, order.Status)
	}
	if order.ProviderCheckoutURL != "" {
		return fmt.Errorf("order %q has provider_checkout_url set: the provider delivered a URL, so this is not a stuck-create case", order.OrderNSU)
	}
	if order.ProviderCreateAttemptedAt == nil {
		return fmt.Errorf("order %q has no provider_create_attempted_at: no provider call was recorded; use recreate-checkout instead", order.OrderNSU)
	}
	return nil
}

// cleanupAction describes the set of writes the cleanup should perform.
type cleanupAction int

const (
	actionFailBoth     cleanupAction = iota // open+eligible order + pending idem → fail order then fail idem
	actionFailIdemOnly                      // order already failed + idem still pending → finish idem only
	actionNoOp                              // order already failed + idem failed/not found → nothing to do
	actionHardStop                          // committed idem or ineligible status → do not write anything
)

// cleanupPlan captures the decided intent and the resolved idempotency record.
type cleanupPlan struct {
	action  cleanupAction
	idemKey *idempotency.Key // nil when not found
	reason  string
}

// decidePlan returns the cleanup plan for the given order and idempotency record.
// A committed idempotency record is a hard stop regardless of order state — no writes may proceed.
func decidePlan(order *checkout.Order, idemKey *idempotency.Key) cleanupPlan {
	// Hard stop: committed idempotency must never be overwritten.
	if idemKey != nil && idemKey.Status == idempotency.StatusCommitted {
		return cleanupPlan{
			action:  actionHardStop,
			idemKey: idemKey,
			reason:  fmt.Sprintf("idempotency record is committed (key: %s); manual review required before any write", idemKey.IdempotencyKey),
		}
	}

	orderStatus := payments.OrderStatus(order.Status)

	// Rerun recovery: order is already failed — only finish the idempotency side if needed.
	if orderStatus == payments.OrderStatusFailed {
		if idemKey != nil && idemKey.Status == idempotency.StatusPending {
			return cleanupPlan{
				action:  actionFailIdemOnly,
				idemKey: idemKey,
				reason:  "order already failed; finishing idempotency cleanup",
			}
		}
		return cleanupPlan{
			action:  actionNoOp,
			idemKey: idemKey,
			reason:  "order already failed and idempotency record is clean",
		}
	}

	// First run: check open-order eligibility (status + URL + attempted_at).
	if err := checkStuckEligibility(order); err != nil {
		return cleanupPlan{action: actionHardStop, reason: err.Error()}
	}

	return cleanupPlan{
		action:  actionFailBoth,
		idemKey: idemKey,
		reason:  "stuck open order eligible for cleanup",
	}
}
