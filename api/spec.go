// Package api embeds the published API documents: the OpenAPI description
// of the REST API and the JSON Schema of the fixture format.
package api

import _ "embed"

// OpenAPI is the OpenAPI 3.1 description of the REST API.
//
//go:embed openapi.yaml
var OpenAPI []byte

// FixtureSchema is the JSON Schema for fixture files.
//
//go:embed fixture.schema.json
var FixtureSchema []byte
