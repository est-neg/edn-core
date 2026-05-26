package docs_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/villenneve/vil-core/internal/docs"
)

func TestDocsHandler_ScalarHTML(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(docs.ScalarHTML)
	})

	req := httptest.NewRequest(http.MethodGet, "/docs", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("expected Content-Type to contain text/html, got %q", ct)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "@scalar/api-reference") {
		t.Error("body must contain @scalar/api-reference")
	}
	if strings.Contains(strings.ToLower(body), "redoc") {
		t.Error("body must NOT contain redoc")
	}
	if !strings.Contains(body, "/openapi.yaml") {
		t.Error("body must contain /openapi.yaml")
	}
}

func TestOpenAPIYAMLHandler(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write(docs.OpenAPISpec)
	})

	req := httptest.NewRequest(http.MethodGet, "/openapi.yaml", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "application/yaml") {
		t.Errorf("expected Content-Type to contain application/yaml, got %q", ct)
	}
	body := rr.Body.Bytes()
	if len(body) == 0 {
		t.Fatal("expected non-empty response body for /openapi.yaml")
	}
	if !strings.Contains(string(body), "openapi: 3.0.3") {
		t.Errorf("expected body to contain 'openapi: 3.0.3'; got first 100 bytes: %s", body[:min(100, len(body))])
	}
}

// TestOpenAPISpecOwnership asserts that internal/docs/openapi.yaml and api/openapi.yaml
// are identical.  internal/docs/openapi.yaml is the runtime source of truth; api/openapi.yaml
// must be kept in sync manually until the duplication is resolved.
func TestOpenAPISpecOwnership(t *testing.T) {
	internal := docs.OpenAPISpec
	external, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatalf("failed to read api/openapi.yaml: %v", err)
	}
	if !bytes.Equal(internal, external) {
		t.Error(
			"internal/docs/openapi.yaml and api/openapi.yaml are out of sync.\n" +
				"internal/docs/openapi.yaml is the runtime source of truth.\n" +
				"Sync with: cp internal/docs/openapi.yaml api/openapi.yaml",
		)
	}
}
