// Command recreate-checkout is an operator recovery tool that reads a legacy order
// from MongoDB and creates a new checkout session via POST /v1/checkout/sessions.
//
// Usage:
//
//	go run ./cmd/recreate-checkout -order-nsu <NSU> [-channel web|mobile] [-base-url <URL>] [-idempotency-key <KEY>] [-dry-run] [-allow-unsafe-url]
//
// Flags:
//
//	-order-nsu        (required) NSU of the legacy order to recreate
//	-channel          Sales channel: web or mobile (defaults to web; logged when omitted)
//	-base-url         API base URL; inferred from MongoDB database name when omitted
//	-idempotency-key  Idempotency-Key header value; derived from order NSU when omitted
//	-dry-run          Print masked summary without calling the API
//	-allow-unsafe-url Allow non-standard base URL overrides (operator risk)
//
// The command loads configuration from the environment (VIL_* prefix) or a config file,
// same as any other service in this repository.
//
// Eligibility: only orders with status "created" and no existing provider_checkout_url are
// eligible. Paid, failed, expired, and pending_review orders are rejected by default.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/villenneve/vil-core/internal/checkout"
	"github.com/villenneve/vil-core/internal/organizations"
	"github.com/villenneve/vil-core/internal/payments"
	"github.com/villenneve/vil-core/internal/platform/config"
	mongoplat "github.com/villenneve/vil-core/internal/platform/mongodb"
	"github.com/villenneve/vil-core/internal/tenants"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	orderNSU := flag.String("order-nsu", "", "NSU of the legacy order to recreate (required)")
	baseURL := flag.String("base-url", "", "API base URL (inferred from database name when empty)")
	idempotencyKey := flag.String("idempotency-key", "", "Idempotency-Key header value (derived from order NSU when empty)")
	channel := flag.String("channel", "", "Sales channel: web|mobile (defaults to web; logged when omitted)")
	dryRun := flag.Bool("dry-run", false, "Print masked summary without calling the API")
	allowUnsafeURL := flag.Bool("allow-unsafe-url", false, "Allow non-standard base URL overrides (operator risk)")
	flag.Parse()

	if strings.TrimSpace(*orderNSU) == "" {
		flag.Usage()
		return errors.New("-order-nsu is required")
	}
	nsu := strings.TrimSpace(*orderNSU)

	// Validate channel early; NormalizeSalesChannel defaults empty input to "web".
	resolvedChannel, err := payments.NormalizeSalesChannel(*channel)
	if err != nil {
		return fmt.Errorf("invalid -channel %q: accepted values are web, mobile", *channel)
	}
	if strings.TrimSpace(*channel) == "" {
		fmt.Printf("[info] -channel omitted; defaulting to %q\n", resolvedChannel)
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if cfg.MongoDB.URI == "" {
		return errors.New("VIL_MONGODB_URI is required")
	}

	apiBase, err := resolveAndValidateBaseURL(*baseURL, cfg.MongoDB.Database, *allowUnsafeURL)
	if err != nil {
		return err
	}

	ikey := strings.TrimSpace(*idempotencyKey)
	if ikey == "" {
		ikey = "recreate-checkout-v1:" + nsu
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	mongoClient, err := mongoplat.New(ctx, cfg.MongoDB)
	if err != nil {
		return fmt.Errorf("connect mongodb: %w", err)
	}
	defer mongoClient.Disconnect(context.Background()) //nolint:errcheck

	orderRepo := checkout.NewMongoOrderRepository(mongoClient, cfg.MongoDB)
	order, err := orderRepo.FindByNSU(ctx, nsu)
	if err != nil {
		if errors.Is(err, checkout.ErrOrderNotFound) {
			return fmt.Errorf("order %q not found in collection %q", nsu, cfg.MongoDB.CollectionOrders)
		}
		return fmt.Errorf("fetch order: %w", err)
	}

	if err := checkOrderEligibility(order); err != nil {
		return err
	}

	orgRepo := organizations.NewMongoRepository(mongoClient, cfg.MongoDB)
	org, err := orgRepo.FindByUUID(ctx, order.OrganizationID)
	if err != nil {
		if errors.Is(err, organizations.ErrNotFound) {
			return fmt.Errorf("organization %q not found (order.organization_id)", order.OrganizationID)
		}
		return fmt.Errorf("fetch organization: %w", err)
	}

	tenantRepo := tenants.NewMongoRepository(mongoClient, cfg.MongoDB)
	tenant, err := tenantRepo.FindByUUID(ctx, order.TenantID)
	if err != nil {
		if errors.Is(err, tenants.ErrNotFound) {
			return fmt.Errorf("tenant %q not found (order.tenant_id)", order.TenantID)
		}
		return fmt.Errorf("fetch tenant: %w", err)
	}

	req := payments.CreateCheckoutRequest{
		OrganizationSlug: org.Slug,
		TenantSlug:       tenant.Slug,
		Channel:          resolvedChannel,
		PlanSlug:         order.PlanSlug,
		BillingCycle:     order.BillingCycle,
		Customer: payments.CustomerPayload{
			Name:     order.CustomerName,
			Email:    order.CustomerEmail,
			Phone:    order.CustomerPhone,
			Document: order.CustomerDocument,
		},
	}

	endpoint := apiBase + "/v1/checkout/sessions"
	fmt.Printf("legacy order summary:\n")
	fmt.Printf("  nsu:           %s\n", order.OrderNSU)
	fmt.Printf("  status:        %s\n", order.Status)
	fmt.Printf("  plan:          %s / %s\n", order.PlanSlug, order.BillingCycle)
	fmt.Printf("  channel:       %s\n", resolvedChannel)
	fmt.Printf("  organization:  %s\n", org.Slug)
	fmt.Printf("  tenant:        %s\n", tenant.Slug)
	fmt.Printf("  customer:      %s / %s\n", maskEmail(order.CustomerEmail), maskDocument(order.CustomerDocument))
	fmt.Printf("  created_at:    %s\n", order.CreatedAt.Format(time.RFC3339))
	fmt.Printf("\nwould POST %s\n", endpoint)
	fmt.Printf("  Idempotency-Key: %s\n", ikey)

	if *dryRun {
		fmt.Println("\n[dry-run] request summary (PII redacted):")
		fmt.Printf("  organization_slug: %s\n", req.OrganizationSlug)
		fmt.Printf("  tenant_slug:       %s\n", req.TenantSlug)
		fmt.Printf("  channel:           %s\n", req.Channel)
		fmt.Printf("  plan_slug:         %s\n", req.PlanSlug)
		fmt.Printf("  billing_cycle:     %s\n", req.BillingCycle)
		fmt.Printf("  customer.name:     %s\n", maskName(req.Customer.Name))
		fmt.Printf("  customer.email:    %s\n", maskEmail(req.Customer.Email))
		fmt.Printf("  customer.phone:    %s\n", maskPhone(req.Customer.Phone))
		fmt.Printf("  customer.document: %s\n", maskDocument(req.Customer.Document))
		fmt.Println("\n[dry-run] no request sent")
		return nil
	}

	resp, err := postCheckout(ctx, endpoint, ikey, req)
	if err != nil {
		return fmt.Errorf("post checkout: %w", err)
	}

	fmt.Printf("\nresult:\n")
	fmt.Printf("  order_nsu:    %s\n", resp.OrderNSU)
	fmt.Printf("  status:       %s\n", resp.Status)
	fmt.Printf("  expires_at:   %s\n", resp.ExpiresAt.Format(time.RFC3339))
	fmt.Printf("  checkout_url: %s\n", resp.CheckoutURL)
	return nil
}

func postCheckout(ctx context.Context, endpoint, idempotencyKey string, req payments.CreateCheckoutRequest) (*payments.CreateCheckoutResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Idempotency-Key", idempotencyKey)

	client := &http.Client{Timeout: 20 * time.Second}
	httpResp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http call: %w", err)
	}
	defer httpResp.Body.Close() //nolint:errcheck

	if httpResp.StatusCode != http.StatusCreated && httpResp.StatusCode != http.StatusOK {
		var errBody struct {
			Error     string `json:"error"`
			ErrorCode string `json:"error_code"`
		}
		_ = json.NewDecoder(httpResp.Body).Decode(&errBody)
		msg := errBody.Error
		if msg == "" {
			msg = "(no error body)"
		}
		if errBody.ErrorCode != "" {
			return nil, fmt.Errorf("API returned %d: %s (error_code: %s)", httpResp.StatusCode, msg, errBody.ErrorCode)
		}
		return nil, fmt.Errorf("API returned %d: %s", httpResp.StatusCode, msg)
	}

	var resp payments.CreateCheckoutResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &resp, nil
}

// allowedBaseURLHosts is the explicit allowlist of approved hostnames for base URL overrides.
var allowedBaseURLHosts = map[string]bool{
	"edn-core.app":     true,
	"dev.edn-core.app": true,
}

// resolveAndValidateBaseURL resolves the effective API base URL and validates it against
// the approved list when an explicit override is supplied.
// When allowUnsafe is false, any non-approved override URL is rejected with an actionable error.
func resolveAndValidateBaseURL(override, database string, allowUnsafe bool) (string, error) {
	if override == "" {
		return inferBaseURL(database), nil
	}
	trimmed := strings.TrimRight(override, "/")
	if err := validateApprovedBaseURL(trimmed); err != nil {
		if !allowUnsafe {
			return "", fmt.Errorf("%w; pass -allow-unsafe-url to override (operator risk)", err)
		}
		fmt.Fprintf(os.Stderr, "[warn] using non-standard base URL %q; -allow-unsafe-url is set\n", trimmed)
	}
	return trimmed, nil
}

// inferBaseURL returns the safe default API base URL derived from the MongoDB database name.
func inferBaseURL(database string) string {
	switch database {
	case "edn-core-db-prd":
		return "https://edn-core.app"
	case "edn-core-db-dev":
		return "https://dev.edn-core.app"
	default:
		return "http://localhost:8080"
	}
}

// validateApprovedBaseURL returns an error when rawURL does not match an approved destination.
// Approved: edn-core.app, dev.edn-core.app, localhost (any port), 127.0.0.1 (any port).
func validateApprovedBaseURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("base URL %q is not a valid URL: %w", rawURL, err)
	}
	host := u.Hostname()
	if host == "localhost" || host == "127.0.0.1" {
		return nil
	}
	if allowedBaseURLHosts[host] {
		return nil
	}
	return fmt.Errorf("base URL host %q is not in the approved list (edn-core.app, dev.edn-core.app, localhost, 127.0.0.1)", host)
}

// checkOrderEligibility rejects orders that are unsafe source material for checkout recreation.
// Only orders with status "created", no existing provider_checkout_url, no invoice_slug, and
// no provider_create_attempted_at are allowed.
// This is an explicit allowlist: any status not listed is rejected, and any sign of a partial
// provider attempt is also rejected to avoid duplicate or ambiguous checkout creation.
func checkOrderEligibility(order *checkout.Order) error {
	if order.Status != string(payments.OrderStatusCreated) {
		return fmt.Errorf(
			"order %q has status %q and is not eligible for checkout recreation; "+
				"only %q orders are eligible (paid, failed, expired, and pending_review are blocked)",
			order.OrderNSU, order.Status, payments.OrderStatusCreated,
		)
	}
	if strings.TrimSpace(order.ProviderCheckoutURL) != "" {
		return fmt.Errorf(
			"order %q already has provider_checkout_url set; use the existing link instead of creating a second checkout",
			order.OrderNSU,
		)
	}
	if strings.TrimSpace(order.InvoiceSlug) != "" {
		return fmt.Errorf(
			"order %q has invoice_slug %q set, indicating a partial provider attempt; "+
				"this order is in an ambiguous provider state and must not be recreated by this tool",
			order.OrderNSU, order.InvoiceSlug,
		)
	}
	if order.ProviderCreateAttemptedAt != nil {
		return fmt.Errorf(
			"order %q has provider_create_attempted_at set (%s), indicating a partial provider attempt; "+
				"this order is in an ambiguous provider state and must not be recreated by this tool",
			order.OrderNSU, order.ProviderCreateAttemptedAt.Format(time.RFC3339),
		)
	}
	return nil
}

// maskName redacts all but the first rune of each word.
func maskName(name string) string {
	words := strings.Fields(name)
	for i, w := range words {
		runes := []rune(w)
		if len(runes) <= 1 {
			words[i] = string(runes) + "***"
		} else {
			words[i] = string(runes[:1]) + strings.Repeat("*", len(runes)-1)
		}
	}
	return strings.Join(words, " ")
}

// maskPhone redacts all but the last 2 digits.
func maskPhone(phone string) string {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, phone)
	if len(digits) < 2 {
		return "***"
	}
	return strings.Repeat("*", len(digits)-2) + digits[len(digits)-2:]
}

func maskEmail(email string) string {
	at := strings.Index(email, "@")
	if at <= 0 {
		return "***"
	}
	local := email[:at]
	domain := email[at:]
	if len(local) <= 2 {
		return local + "***" + domain
	}
	return local[:2] + strings.Repeat("*", len(local)-2) + domain
}

func maskDocument(doc string) string {
	d := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, doc)
	if len(d) < 3 {
		return "***"
	}
	return strings.Repeat("*", len(d)-3) + d[len(d)-3:]
}
