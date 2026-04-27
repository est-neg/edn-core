package leads

import (
	"strings"
	"time"
	"unicode/utf8"
)

// SubmitRequest is the inbound HTTP request body for POST /api/leads.
type SubmitRequest struct {
	Name   string `json:"name"`
	Email  string `json:"email"`
	Phone  string `json:"phone,omitempty"`
	Source string `json:"source,omitempty"`
}

// Lead is the domain entity persisted to MongoDB.
type Lead struct {
	ID                 string
	DedupKey           string
	TenantID           string
	Source             string
	ReceivedAt         time.Time
	Name               string
	Email              string
	Phone              string
	Status             string
	NotificationStatus string
	NotificationError  string
}

// Validate performs defensive server-side validation of the inbound request.
func (r *SubmitRequest) Validate() error {
	r.Name = strings.TrimSpace(r.Name)
	r.Email = strings.TrimSpace(r.Email)
	r.Phone = strings.TrimSpace(r.Phone)
	r.Source = strings.TrimSpace(r.Source)

	if utf8.RuneCountInString(r.Name) < 2 {
		return &ValidationError{Details: "name must have at least 2 characters"}
	}
	if r.Email == "" {
		return &ValidationError{Details: "email is required"}
	}
	if !isValidEmail(r.Email) {
		return &ValidationError{Details: "email is not a valid email address"}
	}
	return nil
}

// isValidEmail performs a minimal structural check.
func isValidEmail(email string) bool {
	at := strings.LastIndex(email, "@")
	if at < 1 {
		return false
	}
	domain := email[at+1:]
	return strings.Contains(domain, ".") && len(domain) >= 3
}
