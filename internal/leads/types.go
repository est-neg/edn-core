package leads

import (
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// NestedLeadPayload is the nested lead object in the site payload shape.
type NestedLeadPayload struct {
	Name         string `json:"name"`
	BusinessName string `json:"businessName,omitempty"`
	Whatsapp     string `json:"whatsapp,omitempty"`
	Email        string `json:"email"`
	Profile      string `json:"profile,omitempty"`
	Message      string `json:"message,omitempty"`
	Consent      bool   `json:"consent"`
}

// SubmitRequest is the inbound HTTP request body for POST /api/leads.
// It accepts both the legacy flat payload {name,email,phone?,source?} and the
// richer nested site payload {source,submittedAt,lead:{...}}.
// Validate() normalizes both shapes so callers can treat them uniformly.
type SubmitRequest struct {
	// Flat (legacy) JSON fields
	Name   string `json:"name,omitempty"`
	Email  string `json:"email,omitempty"`
	Phone  string `json:"phone,omitempty"`
	Source string `json:"source,omitempty"`

	// Nested site payload JSON fields
	SubmittedAt string             `json:"submittedAt,omitempty"`
	Lead        *NestedLeadPayload `json:"lead,omitempty"`

	// Normalized fields populated by Validate(); never decoded from JSON.
	ParsedSubmittedAt *time.Time `json:"-"`
	BusinessName      string     `json:"-"`
	Profile           string     `json:"-"`
	Message           string     `json:"-"`
	Consent           bool       `json:"-"`
}

// Lead is the domain entity persisted to MongoDB.
type Lead struct {
	ID                 string
	DedupKey           string
	TenantID           string
	Source             string
	ReceivedAt         time.Time
	SubmittedAt        *time.Time
	Name               string
	Email              string
	Phone              string
	BusinessName       string
	Profile            string
	Message            string
	Consent            bool
	Status             string
	NotificationStatus string
	NotificationError  string
}

// validProfiles is the set of allowed profile values in the nested payload.
var validProfiles = map[string]bool{
	"administrative": true,
	"medical":        true,
	"dental":         true,
}

var nonDigitRe = regexp.MustCompile(`[^0-9]`)

func normalizeWhatsapp(s string) string {
	return nonDigitRe.ReplaceAllString(s, "")
}

// Validate performs defensive server-side validation of the inbound request.
// For the nested payload it normalizes into the flat representation so the
// service layer always receives consistent Name/Email/Phone/Source fields.
func (r *SubmitRequest) Validate() error {
	isNested := r.Lead != nil || r.SubmittedAt != ""
	if isNested {
		if r.Name != "" || r.Email != "" || r.Phone != "" {
			return &ValidationError{Details: "ambiguous payload: top-level name/email/phone cannot be combined with nested lead"}
		}
		return r.validateNested()
	}
	return r.validateFlat()
}

func (r *SubmitRequest) validateFlat() error {
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
	if utf8.RuneCountInString(r.Source) > 80 {
		return &ValidationError{Details: "source must not exceed 80 characters"}
	}
	if r.Phone != "" {
		normalized := normalizeWhatsapp(r.Phone)
		if utf8.RuneCountInString(normalized) < 8 {
			return &ValidationError{Details: "phone must have at least 8 digits"}
		}
		r.Phone = normalized
	}
	return nil
}

func (r *SubmitRequest) validateNested() error {
	r.Source = strings.TrimSpace(r.Source)
	if r.Source == "" {
		return &ValidationError{Details: "source is required"}
	}
	if utf8.RuneCountInString(r.Source) > 80 {
		return &ValidationError{Details: "source must not exceed 80 characters"}
	}

	if r.SubmittedAt == "" {
		return &ValidationError{Details: "submittedAt is required"}
	}
	t, err := time.Parse(time.RFC3339Nano, r.SubmittedAt)
	if err != nil {
		return &ValidationError{Details: "submittedAt must be a valid RFC3339 timestamp"}
	}
	parsed := t.UTC()
	r.ParsedSubmittedAt = &parsed

	if r.Lead == nil {
		return &ValidationError{Details: "lead is required"}
	}

	lead := r.Lead

	lead.Name = strings.TrimSpace(lead.Name)
	if utf8.RuneCountInString(lead.Name) < 2 {
		return &ValidationError{Details: "lead.name must have at least 2 characters"}
	}

	lead.Email = strings.TrimSpace(lead.Email)
	if lead.Email == "" {
		return &ValidationError{Details: "lead.email is required"}
	}
	if !isValidEmail(lead.Email) {
		return &ValidationError{Details: "lead.email is not a valid email address"}
	}

	if lead.Whatsapp != "" {
		normalized := normalizeWhatsapp(strings.TrimSpace(lead.Whatsapp))
		if utf8.RuneCountInString(normalized) < 8 {
			return &ValidationError{Details: "lead.whatsapp must have at least 8 digits"}
		}
		lead.Whatsapp = normalized
	}

	if lead.BusinessName != "" {
		lead.BusinessName = strings.TrimSpace(lead.BusinessName)
		if utf8.RuneCountInString(lead.BusinessName) < 2 {
			return &ValidationError{Details: "lead.businessName must have at least 2 characters"}
		}
	}

	if lead.Profile != "" {
		if !validProfiles[lead.Profile] {
			return &ValidationError{Details: "lead.profile must be one of administrative, medical, dental"}
		}
	}

	if lead.Message != "" {
		lead.Message = strings.TrimSpace(lead.Message)
		if utf8.RuneCountInString(lead.Message) > 500 {
			return &ValidationError{Details: "lead.message must not exceed 500 characters"}
		}
	}

	if !lead.Consent {
		return &ValidationError{Details: "lead.consent must be true"}
	}

	// Normalize into flat fields for the service layer.
	r.Name = lead.Name
	r.Email = lead.Email
	r.Phone = lead.Whatsapp // persisted into existing phone field
	r.BusinessName = lead.BusinessName
	r.Profile = lead.Profile
	r.Message = lead.Message
	r.Consent = lead.Consent

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
