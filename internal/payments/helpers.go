package payments

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/google/uuid"
)

// GenerateOrderNSU generates a unique, backend-only order identifier.
func GenerateOrderNSU() string {
	return uuid.New().String()
}

// CalculateEventHash computes the SHA-256 hex digest of the raw webhook body.
// This is used for idempotency and dedup keying.
func CalculateEventHash(body []byte) string {
	sum := sha256.Sum256(body)
	return fmt.Sprintf("%x", sum)
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

// ValidateOrderNSU checks that an order NSU from a URL param is safe to use in DB queries.
func ValidateOrderNSU(nsu string) error {
	if !reOrderNSU.MatchString(nsu) {
		return fmt.Errorf("%w: invalid order_nsu format", ErrInvalidRequest)
	}
	return nil
}
