package leads

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Notifier is the notification port for the leads module.
// Implementations should be non-blocking from the HTTP request lifecycle.
type Notifier interface {
	Notify(ctx context.Context, lead Lead) error
}

// NoopNotifier satisfies the Notifier interface without performing any action.
// Use as the default until a concrete notification adapter is wired.
type NoopNotifier struct{}

func (NoopNotifier) Notify(_ context.Context, _ Lead) error { return nil }

// WebhookNotifier sends accepted leads to a configured internal webhook.
type WebhookNotifier struct {
	client *http.Client
	url    string
}

// NewWebhookNotifier constructs a webhook notifier for the configured destination.
func NewWebhookNotifier(url string) Notifier {
	return &WebhookNotifier{
		client: &http.Client{},
		url:    url,
	}
}

func (n *WebhookNotifier) Notify(ctx context.Context, lead Lead) error {
	body, err := json.Marshal(struct {
		ID         string    `json:"id"`
		TenantID   string    `json:"tenant_id,omitempty"`
		Source     string    `json:"source"`
		ReceivedAt time.Time `json:"received_at"`
		Name       string    `json:"name"`
		Email      string    `json:"email"`
		Phone      string    `json:"phone,omitempty"`
		Status     string    `json:"status"`
	}{
		ID:         lead.ID,
		TenantID:   lead.TenantID,
		Source:     lead.Source,
		ReceivedAt: lead.ReceivedAt,
		Name:       lead.Name,
		Email:      lead.Email,
		Phone:      lead.Phone,
		Status:     lead.Status,
	})
	if err != nil {
		return fmt.Errorf("marshal notification payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build notification request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("send notification webhook: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("notification webhook returned status %d", resp.StatusCode)
	}

	return nil
}
