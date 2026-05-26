package webhookrelay

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

// TokenProvider returns a bearer token for the relay→core hop.
// An empty token means no Authorization header is set.
// Implementations must be safe for concurrent use.
type TokenProvider interface {
	Token(ctx context.Context) (string, error)
}

// StaticTokenProvider returns a fixed token value.
// Passing an empty string disables the Authorization header (local dev / tests).
type StaticTokenProvider struct {
	value string
}

// NewStaticTokenProvider creates a StaticTokenProvider with the given token.
// An empty value means no Authorization header will be set.
func NewStaticTokenProvider(token string) *StaticTokenProvider {
	return &StaticTokenProvider{value: token}
}

func (s *StaticTokenProvider) Token(_ context.Context) (string, error) {
	return s.value, nil
}

// MetadataIDTokenProvider fetches a Google-signed OIDC ID token from the GCP
// instance metadata server. This is the primary Cloud Run service-to-service
// authentication control. audience must be the base URL of the core Cloud Run service.
type MetadataIDTokenProvider struct {
	audience string
}

// NewMetadataIDTokenProvider returns a provider that fetches a fresh Google-signed
// OIDC ID token for the given audience from the Cloud Run metadata server.
func NewMetadataIDTokenProvider(audience string) *MetadataIDTokenProvider {
	return &MetadataIDTokenProvider{audience: audience}
}

// Token fetches a fresh OIDC ID token from the GCP metadata server.
// Tokens are short-lived; do not cache the returned value across requests.
func (m *MetadataIDTokenProvider) Token(ctx context.Context) (string, error) {
	path := "instance/service-accounts/default/identity?audience=" +
		url.QueryEscape(m.audience) + "&format=full"
	tok, err := metadata.GetWithContext(ctx, path)
	if err != nil {
		return "", fmt.Errorf("gcp metadata id token fetch: %w", err)
	}
	return strings.TrimSpace(tok), nil
}

// ForwardClient sends the raw webhook body to the core internal reconcile route.
// It enforces the hop timeout, strips all inbound provider headers, and sends
// only the relay-synthesized allow-list. The core response body is always discarded —
// the relay synthesizes its own response from the status code alone.
type ForwardClient struct {
	client            *http.Client
	coreURL           string
	coreInternalToken string        // X-EDN-Internal-Token defense-in-depth header; empty = disabled
	tokenProvider     TokenProvider // primary Cloud Run service-to-service auth; nil = disabled
}

// NewForwardClient creates a ForwardClient with a dedicated HTTP client.
// tp is the primary identity token provider. Pass nil or NewStaticTokenProvider("")
// to disable the Authorization header (local dev only).
func NewForwardClient(coreURL, coreInternalToken string, timeoutSec int, tp TokenProvider) *ForwardClient {
	if timeoutSec <= 0 {
		timeoutSec = 3
	}
	return &ForwardClient{
		client: &http.Client{
			Timeout: time.Duration(timeoutSec) * time.Second,
		},
		coreURL:           coreURL,
		coreInternalToken: coreInternalToken,
		tokenProvider:     tp,
	}
}

// Forward sends rawBody to the core internal route and returns the HTTP status code.
// requestID and receivedAt are passed as relay correlation headers only.
// The core response body is drained and discarded; callers must never read it.
func (c *ForwardClient) Forward(ctx context.Context, requestID, receivedAt string, rawBody []byte) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.coreURL, bytes.NewReader(rawBody))
	if err != nil {
		return 0, err
	}

	// Primary auth: Google-signed OIDC ID token for Cloud Run service-to-service auth.
	if c.tokenProvider != nil {
		tok, err := c.tokenProvider.Token(ctx)
		if err != nil {
			return 0, fmt.Errorf("relay identity token: %w", err)
		}
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}

	// Allow-list headers forwarded to core. No provider headers pass through.
	req.Header.Set("Content-Type", "application/json")
	if requestID != "" {
		req.Header.Set("X-EDN-Relay-Request-ID", requestID)
	}
	if receivedAt != "" {
		req.Header.Set("X-EDN-Relay-Received-At", receivedAt)
	}
	// Defense-in-depth secondary token (IAM service-to-service auth is the primary control).
	if c.coreInternalToken != "" {
		req.Header.Set("X-EDN-Internal-Token", c.coreInternalToken)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return 0, err
	}
	// Drain and discard body to enable connection reuse.
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	return resp.StatusCode, nil
}
