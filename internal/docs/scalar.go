// Package docs embeds the OpenAPI specification and the Scalar API reference UI.
package docs

import (
	"crypto/sha256"
	"encoding/base64"
)

// scalarBundleSRI is the Subresource Integrity hash for the pinned CDN bundle.
// It is intentionally left empty until verified against the actual downloaded file.
//
// To compute, run:
//
//	curl -sL "https://cdn.jsdelivr.net/npm/@scalar/api-reference@1.25.125/dist/browser/standalone.js" | openssl dgst -sha256 -binary | base64
//	// prefix the result with "sha256-"
const scalarBundleSRI = ""

// scalarConfigScript is the verbatim inline script that configures Scalar API Reference.
// Its SHA-256 hash is computed at init and injected into the Content-Security-Policy.
// Do NOT change this string without recomputing the CSP hash alignment.
const scalarConfigScript = `document.getElementById("api-reference").dataset.configuration=JSON.stringify({withDefaultFonts:false,persistAuth:false,servers:[{url:"http://localhost:8080",description:"Local development"}]});`

// ScalarCSP is the full Content-Security-Policy header value for the /docs page.
// It is set in init() and includes the computed sha256 hash of the inline config script.
// Use this variable when setting the Content-Security-Policy response header.
var ScalarCSP string

// ScalarHTML is the complete, ready-to-serve Scalar API Reference page.
// The Content-Security-Policy hash is computed from scalarConfigScript at package
// initialization, so it always matches the actual inline script content.
var ScalarHTML []byte

func init() {
	sum := sha256.Sum256([]byte(scalarConfigScript))
	cspHash := "sha256-" + base64.StdEncoding.EncodeToString(sum[:])

	ScalarCSP = "default-src 'none';" +
		" script-src 'self' https://cdn.jsdelivr.net '" + cspHash + "';" +
		" style-src 'self' 'unsafe-inline';" +
		" img-src 'self' data:;" +
		" font-src https://fonts.gstatic.com;" +
		" connect-src 'self';" +
		" object-src 'none';" +
		" frame-ancestors 'none';" +
		" base-uri 'none';" +
		" form-action 'none'"

	var integrityAttr string
	if scalarBundleSRI != "" {
		integrityAttr = ` integrity="` + scalarBundleSRI + `" crossorigin="anonymous"`
	}

	ScalarHTML = []byte(`<!DOCTYPE html>
<html lang="en">
  <head>
    <title>EDN Core — API Docs</title>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <meta http-equiv="Content-Security-Policy"
      content="` + ScalarCSP + `" />
    <meta name="referrer" content="no-referrer" />
    <meta http-equiv="X-Content-Type-Options" content="nosniff" />
  </head>
  <body>
    <script id="api-reference" data-url="/openapi.yaml"></script>
    <script>` + scalarConfigScript + `</script>
    <script src="https://cdn.jsdelivr.net/npm/@scalar/api-reference@1.25.125/dist/browser/standalone.js"` + integrityAttr + `></script>
  </body>
</html>`)
}
