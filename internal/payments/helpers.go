package payments

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/google/uuid"
	commercialplans "github.com/villenneve/vil-core/internal/plans"
)

// GenerateOrderNSU generates a unique, backend-only order identifier.
func GenerateOrderNSU() string {
	return uuid.New().String()
}

// GenerateCheckoutIntentKey generates a high-entropy, opaque resume handle.
// 24 random bytes encoded as base64url (no padding) gives 32 URL-safe characters.
// This must never be derivable from PII or business attributes.
func GenerateCheckoutIntentKey() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failure is catastrophic; fall back to uuid-based key.
		return uuid.New().String()
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// CalculateEventHash computes the SHA-256 hex digest of the raw webhook body.
// This is used for idempotency and dedup keying.
func CalculateEventHash(body []byte) string {
	sum := sha256.Sum256(body)
	return fmt.Sprintf("%x", sum)
}

// CalculateCheckoutRequestHash computes a stable hash for the commercial parts of a
// checkout request after server-side normalization.
func CalculateCheckoutRequestHash(organizationSlug, tenantSlug, channel, planSlug, billingCycle, customerName, email, phone, cpf string) string {
	payload := strings.Join([]string{
		strings.TrimSpace(strings.ToLower(organizationSlug)),
		strings.TrimSpace(strings.ToLower(tenantSlug)),
		strings.TrimSpace(strings.ToLower(channel)),
		strings.TrimSpace(planSlug),
		strings.TrimSpace(billingCycle),
		strings.TrimSpace(customerName),
		strings.TrimSpace(strings.ToLower(email)),
		strings.TrimSpace(phone),
		strings.TrimSpace(cpf),
	}, "|")
	return CalculateEventHash([]byte(payload))
}

var reE164 = regexp.MustCompile(`^\+[1-9]\d{6,14}$`)
var reCPFDigits = regexp.MustCompile(`^\d{11}$`)

// NormalizePhone normalizes a phone number to E.164 format.
// It strips spaces and dashes, adds +55 for 10/11-digit Brazilian numbers.
// Returns an error if the result is not valid E.164.
func NormalizePhone(raw string) (string, error) {
	stripped := strings.Map(func(r rune) rune {
		if unicode.IsDigit(r) || r == '+' {
			return r
		}
		return -1
	}, strings.TrimSpace(raw))

	if strings.HasPrefix(stripped, "+") {
		if reE164.MatchString(stripped) {
			return stripped, nil
		}
		return "", fmt.Errorf("%w: %q", ErrInvalidRequest, raw)
	}

	// Treat 10/11-digit numbers as Brazilian (+55)
	digits := strings.TrimPrefix(stripped, "0")
	if len(digits) == 10 || len(digits) == 11 {
		candidate := "+55" + digits
		if reE164.MatchString(candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%w: phone not normalizable: %q", ErrInvalidRequest, raw)
}

var reEmail = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)
var reOrderNSU = regexp.MustCompile(`^[a-zA-Z0-9\-_]{1,64}$`)
var rePlanSlug = regexp.MustCompile(`^[a-z0-9\-_]{1,64}$`)

// NormalizeCPF strips formatting from a CPF string and validates its check digits.
// Accepts "000.000.000-00" or "00000000000" formats.
func NormalizeCPF(raw string) (string, error) {
	stripped := strings.Map(func(r rune) rune {
		if unicode.IsDigit(r) {
			return r
		}
		return -1
	}, strings.TrimSpace(raw))

	if !reCPFDigits.MatchString(stripped) {
		return "", fmt.Errorf("%w: document (CPF) must have 11 digits", ErrInvalidRequest)
	}
	// Reject trivially invalid CPFs (all same digit)
	if strings.Count(stripped, string(stripped[0])) == 11 {
		return "", fmt.Errorf("%w: document (CPF) is invalid", ErrInvalidRequest)
	}
	// Validate first check digit
	sum := 0
	for i := 0; i < 9; i++ {
		sum += int(stripped[i]-'0') * (10 - i)
	}
	rem := (sum * 10) % 11
	if rem == 10 || rem == 11 {
		rem = 0
	}
	if rem != int(stripped[9]-'0') {
		return "", fmt.Errorf("%w: document (CPF) is invalid", ErrInvalidRequest)
	}
	// Validate second check digit
	sum = 0
	for i := 0; i < 10; i++ {
		sum += int(stripped[i]-'0') * (11 - i)
	}
	rem = (sum * 10) % 11
	if rem == 10 || rem == 11 {
		rem = 0
	}
	if rem != int(stripped[10]-'0') {
		return "", fmt.Errorf("%w: document (CPF) is invalid", ErrInvalidRequest)
	}
	return stripped, nil
}

// ValidateCreateCheckoutRequest validates the inbound checkout session request.
// Price, currency, and amount are never validated from request — they come from the plan.
func ValidateCreateCheckoutRequest(req CreateCheckoutRequest) error {
	if !rePlanSlug.MatchString(req.PlanSlug) {
		return fmt.Errorf("%w: invalid plan_slug", ErrInvalidRequest)
	}
	if err := ValidateTenantScope(req.OrganizationSlug, req.TenantSlug); err != nil {
		return err
	}
	if _, err := NormalizeSalesChannel(req.Channel); err != nil {
		return err
	}
	if req.BillingCycle != "monthly" && req.BillingCycle != "annual" {
		return fmt.Errorf("%w: billing_cycle must be monthly or annual", ErrInvalidRequest)
	}
	if strings.TrimSpace(req.Customer.Name) == "" {
		return fmt.Errorf("%w: customer name is required", ErrInvalidRequest)
	}
	if !reEmail.MatchString(strings.ToLower(strings.TrimSpace(req.Customer.Email))) {
		return fmt.Errorf("%w: invalid customer email", ErrInvalidRequest)
	}
	if _, err := NormalizePhone(req.Customer.Phone); err != nil {
		return fmt.Errorf("%w: invalid customer phone", ErrInvalidRequest)
	}
	if _, err := NormalizeCPF(req.Customer.Document); err != nil {
		return err
	}
	return nil
}

// ValidateTenantScope enforces that organization_slug and tenant_slug are both provided.
func ValidateTenantScope(organizationSlug, tenantSlug string) error {
	org := strings.TrimSpace(strings.ToLower(organizationSlug))
	tenant := strings.TrimSpace(strings.ToLower(tenantSlug))
	if org == "" || tenant == "" {
		return fmt.Errorf("%w: organization_slug and tenant_slug are required", ErrInvalidRequest)
	}
	if !rePlanSlug.MatchString(org) {
		return fmt.Errorf("%w: invalid organization_slug", ErrInvalidRequest)
	}
	if !rePlanSlug.MatchString(tenant) {
		return fmt.Errorf("%w: invalid tenant_slug", ErrInvalidRequest)
	}
	return nil
}

// NormalizeSalesChannel validates and normalizes public commerce channel values.
// Empty input defaults to web because public checkout traffic originates from the web channel.
// Only "web" and "mobile" are accepted. "all" and "partner" are internal-only channels
// and must not be accepted from unauthenticated public requests (e.g. GET /v1/plans or
// public checkout). Returning an error ensures the public API surface stays minimal.
func NormalizeSalesChannel(raw string) (string, error) {
	channel := strings.ToLower(strings.TrimSpace(raw))
	if channel == "" {
		return commercialplans.ChannelWeb, nil
	}
	switch channel {
	case commercialplans.ChannelWeb, commercialplans.ChannelMobile:
		return channel, nil
	default:
		return "", fmt.Errorf("%w: invalid channel", ErrInvalidRequest)
	}
}

// ValidateOrderNSU checks that an order NSU from a URL param is safe to use in DB queries.
func ValidateOrderNSU(nsu string) error {
	if !reOrderNSU.MatchString(nsu) {
		return fmt.Errorf("%w: invalid order_nsu format", ErrInvalidRequest)
	}
	return nil
}
