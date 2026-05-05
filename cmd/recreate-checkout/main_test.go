package main

import (
	"strings"
	"testing"
	"time"

	"github.com/villenneve/vil-core/internal/checkout"
	"github.com/villenneve/vil-core/internal/payments"
)

// -- checkOrderEligibility --

func TestCheckOrderEligibility_AllowsCreated(t *testing.T) {
	order := &checkout.Order{OrderNSU: "NSU001", Status: string(payments.OrderStatusCreated)}
	if err := checkOrderEligibility(order); err != nil {
		t.Fatalf("expected eligible, got: %v", err)
	}
}

func TestCheckOrderEligibility_BlocksPaid(t *testing.T) {
	order := &checkout.Order{OrderNSU: "NSU002", Status: string(payments.OrderStatusPaid)}
	if err := checkOrderEligibility(order); err == nil {
		t.Fatal("expected ineligible for paid order")
	}
}

func TestCheckOrderEligibility_BlocksFailed(t *testing.T) {
	order := &checkout.Order{OrderNSU: "NSU003", Status: string(payments.OrderStatusFailed)}
	if err := checkOrderEligibility(order); err == nil {
		t.Fatal("expected ineligible for failed order")
	}
}

func TestCheckOrderEligibility_BlocksExpired(t *testing.T) {
	order := &checkout.Order{OrderNSU: "NSU004", Status: string(payments.OrderStatusExpired)}
	if err := checkOrderEligibility(order); err == nil {
		t.Fatal("expected ineligible for expired order")
	}
}

func TestCheckOrderEligibility_BlocksPendingReview(t *testing.T) {
	order := &checkout.Order{OrderNSU: "NSU005", Status: string(payments.OrderStatusPendingReview)}
	if err := checkOrderEligibility(order); err == nil {
		t.Fatal("expected ineligible for pending_review order")
	}
}

func TestCheckOrderEligibility_BlocksCheckoutCreated(t *testing.T) {
	order := &checkout.Order{OrderNSU: "NSU006", Status: string(payments.OrderStatusCheckoutCreated)}
	if err := checkOrderEligibility(order); err == nil {
		t.Fatal("expected ineligible for checkout_created order")
	}
}

func TestCheckOrderEligibility_BlocksExistingProviderURL(t *testing.T) {
	order := &checkout.Order{
		OrderNSU:            "NSU007",
		Status:              string(payments.OrderStatusCreated),
		ProviderCheckoutURL: "https://pay.example.com/link",
	}
	if err := checkOrderEligibility(order); err == nil {
		t.Fatal("expected blocked for order with existing provider_checkout_url")
	}
}

func TestCheckOrderEligibility_BlocksInvoiceSlug(t *testing.T) {
	order := &checkout.Order{
		OrderNSU:    "NSU009",
		Status:      string(payments.OrderStatusCreated),
		InvoiceSlug: "inv-abc-123",
	}
	err := checkOrderEligibility(order)
	if err == nil {
		t.Fatal("expected blocked for order with invoice_slug set")
	}
	if !strings.Contains(err.Error(), "ambiguous provider state") {
		t.Errorf("error should mention ambiguous provider state, got: %v", err)
	}
}

func TestCheckOrderEligibility_BlocksProviderCreateAttemptedAt(t *testing.T) {
	ts := time.Now()
	order := &checkout.Order{
		OrderNSU:                  "NSU010",
		Status:                    string(payments.OrderStatusCreated),
		ProviderCreateAttemptedAt: &ts,
	}
	err := checkOrderEligibility(order)
	if err == nil {
		t.Fatal("expected blocked for order with provider_create_attempted_at set")
	}
	if !strings.Contains(err.Error(), "ambiguous provider state") {
		t.Errorf("error should mention ambiguous provider state, got: %v", err)
	}
}

func TestCheckOrderEligibility_ErrorMentionsStatus(t *testing.T) {
	order := &checkout.Order{OrderNSU: "NSU008", Status: "paid"}
	err := checkOrderEligibility(order)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "paid") {
		t.Errorf("error message should mention the blocking status, got: %v", err)
	}
}

// -- validateApprovedBaseURL --

func TestValidateApprovedBaseURL_AllowsProductionEDN(t *testing.T) {
	if err := validateApprovedBaseURL("https://edn-core.app"); err != nil {
		t.Fatalf("expected approved: %v", err)
	}
}

func TestValidateApprovedBaseURL_AllowsDevEDN(t *testing.T) {
	if err := validateApprovedBaseURL("https://dev.edn-core.app"); err != nil {
		t.Fatalf("expected approved: %v", err)
	}
}

func TestValidateApprovedBaseURL_AllowsLocalhostVariants(t *testing.T) {
	for _, u := range []string{
		"http://localhost:8080",
		"http://localhost:9090",
		"http://localhost",
	} {
		if err := validateApprovedBaseURL(u); err != nil {
			t.Errorf("expected approved for %q: %v", u, err)
		}
	}
}

func TestValidateApprovedBaseURL_Allows127(t *testing.T) {
	if err := validateApprovedBaseURL("http://127.0.0.1:8080"); err != nil {
		t.Fatalf("expected approved: %v", err)
	}
}

func TestValidateApprovedBaseURL_RejectsArbitraryHosts(t *testing.T) {
	for _, u := range []string{
		"https://attacker.example.com",
		"https://evil.edn-core.app.attacker.io",
		"http://192.168.1.1:8080",
		"https://staging.internal.corp",
	} {
		if err := validateApprovedBaseURL(u); err == nil {
			t.Errorf("expected rejected for %q", u)
		}
	}
}

// -- resolveAndValidateBaseURL --

func TestResolveAndValidateBaseURL_EmptyOverrideUsesInferred(t *testing.T) {
	got, err := resolveAndValidateBaseURL("", "edn-core-db-prd", false)
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://edn-core.app" {
		t.Fatalf("unexpected: %q", got)
	}
}

func TestResolveAndValidateBaseURL_ApprovedOverrideAccepted(t *testing.T) {
	got, err := resolveAndValidateBaseURL("https://dev.edn-core.app", "edn-core-db-prd", false)
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://dev.edn-core.app" {
		t.Fatalf("unexpected: %q", got)
	}
}

func TestResolveAndValidateBaseURL_TrailingSlashStripped(t *testing.T) {
	got, err := resolveAndValidateBaseURL("https://edn-core.app/", "edn-core-db-prd", false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasSuffix(got, "/") {
		t.Fatalf("trailing slash not stripped: %q", got)
	}
}

func TestResolveAndValidateBaseURL_UnapprovedRejectedWithoutFlag(t *testing.T) {
	_, err := resolveAndValidateBaseURL("https://attacker.example.com", "edn-core-db-prd", false)
	if err == nil {
		t.Fatal("expected error for unapproved URL without -allow-unsafe-url")
	}
}

func TestResolveAndValidateBaseURL_UnapprovedAllowedWithFlag(t *testing.T) {
	_, err := resolveAndValidateBaseURL("https://staging.internal.example.com", "other", true)
	if err != nil {
		t.Fatalf("expected allowed with unsafe flag: %v", err)
	}
}

// -- inferBaseURL --

func TestInferBaseURL(t *testing.T) {
	cases := []struct {
		db   string
		want string
	}{
		{"edn-core-db-prd", "https://edn-core.app"},
		{"edn-core-db-dev", "https://dev.edn-core.app"},
		{"edn-core-db-local", "http://localhost:8080"},
		{"", "http://localhost:8080"},
	}
	for _, c := range cases {
		got := inferBaseURL(c.db)
		if got != c.want {
			t.Errorf("inferBaseURL(%q) = %q, want %q", c.db, got, c.want)
		}
	}
}

// -- mask helpers --

func TestMaskEmail(t *testing.T) {
	cases := []struct{ in, want string }{
		{"ab@example.com", "ab***@example.com"},
		{"john.doe@example.com", "jo******@example.com"},
		{"x@example.com", "x***@example.com"},
		{"", "***"},
		{"noatsign", "***"},
	}
	for _, c := range cases {
		if got := maskEmail(c.in); got != c.want {
			t.Errorf("maskEmail(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestMaskDocument(t *testing.T) {
	cases := []struct{ in, want string }{
		{"12345678901", "********901"},
		{"123.456.789-01", "********901"},
		{"12", "***"},
	}
	for _, c := range cases {
		if got := maskDocument(c.in); got != c.want {
			t.Errorf("maskDocument(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestMaskPhone(t *testing.T) {
	cases := []struct{ in, want string }{
		{"+5511999990101", "***********01"},
		{"11999990101", "*********01"},
		{"1", "***"},
	}
	for _, c := range cases {
		if got := maskPhone(c.in); got != c.want {
			t.Errorf("maskPhone(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestMaskName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Joao Silva", "J*** S****"},
		{"A", "A***"},
		{"", ""},
	}
	for _, c := range cases {
		if got := maskName(c.in); got != c.want {
			t.Errorf("maskName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
