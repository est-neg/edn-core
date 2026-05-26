// Package docs embeds the OpenAPI specification for serving at runtime.
package docs

import _ "embed"

// OpenAPISpec is the raw OpenAPI YAML specification embedded at build time.
//
//go:embed openapi.yaml
var OpenAPISpec []byte
