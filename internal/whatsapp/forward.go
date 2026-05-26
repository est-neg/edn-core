package whatsapp

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"cloud.google.com/go/compute/metadata"
)

// Forwarder sends the raw webhook body to the internal intake service.
// Implementations must be safe for concurrent use.
type Forwarder interface {
	Forward(ctx context.Context, requestID, receivedAt string, rawBody []byte) (int, error)
}

// TokenProvider returns a bearer token for the relay→intake hop.
// An empty token means no Authorization header is set.
// Implementations must be safe for concurrent use.
type TokenProvider interface {
	Token(ctx context.Context) (string, error)
}

// StaticTokenProvider returns a fixed token value.
// Passing an empty string disables the Authorization header (local dev / tests).
type StaticTokenProvider struct{ value string }

// NewStaticTokenProvider creates a StaticTokenProvider with the given token.
func NewStaticTokenProvider(token string) *StaticTokenProvider {
	return &StaticTokenProvider{value: token}
}

func (s *StaticTokenProvider) Token(_ context.Context) (string, error) { return s.value, nil }

// MetadataIDTokenProvider fetches a Google-signed OIDC ID token from the GCP instance
// metadata server. This is the primary Cloud Run service-to-service auth control.
// audience must be the base URL of the internal Cloud Run service.
type MetadataIDTokenProvider struct{ audience string }

// NewMetadataIDTokenProvider returns a provider that fetches a fresh OIDC ID token
// for the given audience from the GCP metadata server.
func NewMetadataIDTokenProvider(audience string) *MetadataIDTokenProvider {
	return &MetadataIDTokenProvider{audience: audience}
}

// Token fetches a fresh OIDC ID token from the GCP metadata server.
// Tokens are short-lived; do not cache across requests.
func (m *MetadataIDTokenProvider) Token(ctx context.Context) (string, error) {
	path := "instance/service-accounts/default/identity?audience=" +
		url.QueryEscape(m.audience) + "&format=full"
	tok, err := metadata.GetWithContext(ctx, path)
	if err != nil {
		return "", fmt.Errorf("gcp metadata id token: %w", err)
	}
	return strings.TrimSpace(tok), nil
}

// ForwardClient sends the raw webhook body to the internal intake URL.
// It sets Cloud Run service-to-service auth as the primary control and
// optionally includes X-EDN-Internal-Token as defense in depth.
type ForwardClient struct {
	client        *http.Client
	intakeURL     string
	internalToken string // X-EDN-Internal-Token (optional, defense in depth); empty = disabled
	tokenProvider TokenProvider
}

// NewForwardClient creates a ForwardClient.
// tp is the primary identity token provider; pass NewStaticTokenProvider("") for local dev.
func NewForwardClient(intakeURL, internalToken string, timeout time.Duration, tp TokenProvider) *ForwardClient {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &ForwardClient{
		client:        &http.Client{Timeout: timeout},
		intakeURL:     intakeURL,
		internalToken: internalToken,
		tokenProvider: tp,
	}
}

// Forward sends rawBody to the internal intake and returns the HTTP status code.
// requestID and receivedAt are forwarded as correlation headers; the core response body
// is always discarded.
func (c *ForwardClient) Forward(ctx context.Context, requestID, receivedAt string, rawBody []byte) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.intakeURL, bytes.NewReader(rawBody))
	if err != nil {
		return 0, err
	}

	// Primary auth: Google-signed OIDC ID token for Cloud Run service-to-service auth.
	if c.tokenProvider != nil {
		tok, err := c.tokenProvider.Token(ctx)
		if err != nil {
			return 0, fmt.Errorf("identity token: %w", err)
		}
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}

	req.Header.Set("Content-Type", "application/json")
	if requestID != "" {
		req.Header.Set("X-EDN-Relay-Request-ID", requestID)
	}
	if receivedAt != "" {
		req.Header.Set("X-EDN-Relay-Received-At", receivedAt)
	}
	// Defense-in-depth secondary token (IAM is the primary control).
	if c.internalToken != "" {
		req.Header.Set("X-EDN-Internal-Token", c.internalToken)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body) //nolint:errcheck
	return resp.StatusCode, nil
}
