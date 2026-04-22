package leads

import (
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

var reDigitsOnly = regexp.MustCompile(`^\d+$`)

// SubmitRequest is the inbound HTTP request body for POST /api/leads.
type SubmitRequest struct {
	Source      string      `json:"source"`
	SubmittedAt string      `json:"submittedAt"`
	Lead        LeadPayload `json:"lead"`
}

// LeadPayload holds the lead fields nested inside SubmitRequest.
type LeadPayload struct {
	Name         string `json:"name"`
	BusinessName string `json:"businessName"`
	WhatsApp     string `json:"whatsapp"`
	Email        string `json:"email,omitempty"`
	Profile      string `json:"profile"`
	Message      string `json:"message,omitempty"`
	Consent      bool   `json:"consent"`
}

// Lead is the domain entity persisted to MongoDB.
type Lead struct {
	ID                 string
	DedupKey           string
	Source             string
	SubmittedAt        time.Time
	ReceivedAt         time.Time
	Name               string
	BusinessName       string
	WhatsApp           string
	Email              string
	Profile            string
	Message            string
	Consent            bool
	Status             string
	NotificationStatus string
	NotificationError  string
}

var validProfiles = map[string]bool{
	"administrative": true,
	"medical":        true,
	"dental":         true,
}

// Validate performs defensive server-side validation of the inbound request.
func (r *SubmitRequest) Validate() error {
	if r.Source != "lumina-ia-site" {
		return &ValidationError{Details: `source must be "lumina-ia-site"`}
	}
	if r.SubmittedAt == "" {
		return &ValidationError{Details: "submittedAt is required"}
	}
	if _, err := time.Parse(time.RFC3339, r.SubmittedAt); err != nil {
		return &ValidationError{Details: "submittedAt must be a valid RFC3339 timestamp"}
	}
	return r.Lead.validate()
}

func (p *LeadPayload) validate() error {
	p.Name = strings.TrimSpace(p.Name)
	p.BusinessName = strings.TrimSpace(p.BusinessName)
	p.WhatsApp = strings.TrimSpace(p.WhatsApp)

	if utf8.RuneCountInString(p.Name) < 2 {
		return &ValidationError{Details: "lead.name must have at least 2 characters"}
	}
	if utf8.RuneCountInString(p.BusinessName) < 2 {
		return &ValidationError{Details: "lead.businessName must have at least 2 characters"}
	}
	if utf8.RuneCountInString(p.WhatsApp) < 8 {
		return &ValidationError{Details: "lead.whatsapp must have at least 8 characters"}
	}
	if !reDigitsOnly.MatchString(p.WhatsApp) {
		return &ValidationError{Details: "lead.whatsapp must contain only digits"}
	}
	if p.Email != "" && !isValidEmail(p.Email) {
		return &ValidationError{Details: "lead.email is not a valid email address"}
	}
	if !validProfiles[p.Profile] {
		return &ValidationError{Details: "lead.profile must be one of: administrative, medical, dental"}
	}
	if utf8.RuneCountInString(p.Message) > 500 {
		return &ValidationError{Details: "lead.message must not exceed 500 characters"}
	}
	if !p.Consent {
		return &ValidationError{Details: "lead.consent must be true"}
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
