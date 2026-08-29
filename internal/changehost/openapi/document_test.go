package openapi

import (
	"errors"
	"strings"
	"testing"
)

const swagger2Fixture = `{
  "swagger": "2.0",
  "host": "review.internal",
  "basePath": "/api",
  "schemes": ["http", "https"],
  "securityDefinitions": {
    "tokenHeader": {"type": "apiKey", "in": "header", "name": "PRIVATE-TOKEN"},
    "queryToken": {"type": "apiKey", "in": "query", "name": "token"}
  },
  "paths": {
    "/projects/{repository}/reviews/{number}": {
      "get": {
        "operationId": "getReview",
        "tags": ["reviews"],
        "parameters": [
          {"name": "repository", "in": "path", "required": true, "type": "string"},
          {"name": "number", "in": "path", "required": true, "type": "integer"}
        ],
        "responses": {
          "200": {"description": "ok", "schema": {"$ref": "#/definitions/Review"}}
        }
      },
      "delete": {"operationId": "deleteReview", "responses": {"204": {"description": "gone"}}}
    },
    "/projects/{repository}/reviews/{number}/diff": {
      "get": {
        "operationId": "getReviewDiff",
        "produces": ["text/plain"],
        "parameters": [
          {"name": "repository", "in": "path", "required": true, "type": "string"},
          {"name": "number", "in": "path", "required": true, "type": "integer"}
        ],
        "responses": {"200": {"description": "diff"}}
      }
    }
  },
  "definitions": {
    "Review": {
      "type": "object",
      "properties": {
        "id": {"type": "integer"},
        "title": {"type": "string"},
        "state": {"type": "string", "enum": ["open", "merged", "closed"]},
        "source": {"$ref": "#/definitions/Ref"}
      }
    },
    "Ref": {
      "type": "object",
      "properties": {"latestCommit": {"type": "string"}}
    }
  }
}`

const openapi31Fixture = `
openapi: 3.1.0
servers:
  - url: https://api.review.internal/v1
  - url: /relative/base
components:
  securitySchemes:
    bearer:
      type: http
      scheme: bearer
    oauth:
      type: oauth2
      flows: {}
paths:
  /projects/{repository}/reviews/{number}/commits:
    get:
      operationId: listReviewCommits
      parameters:
        - name: repository
          in: path
          required: true
          schema:
            type: string
        - name: number
          in: path
          required: true
          schema:
            type: integer
        - name: page
          in: query
          schema:
            type: integer
      responses:
        '200':
          description: ok
          content:
            application/json:
              schema:
                type: object
                properties:
                  values:
                    type: array
                    items:
                      type: object
                      properties:
                        sha:
                          type: string
                          format: hex
                        subject:
                          type: [string, 'null']
`

func TestParseDocumentSwagger2(t *testing.T) {
	doc, err := ParseDocument([]byte(swagger2Fixture))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Version != "swagger:2.0" {
		t.Fatalf("version: %s", doc.Version)
	}
	// https scheme is preferred, http kept for explicit approval review.
	if len(doc.ServerURLs) != 2 || doc.ServerURLs[0] != "https://review.internal/api" {
		t.Fatalf("server urls: %v", doc.ServerURLs)
	}
	// Only the header apiKey scheme survives; query tokens are dropped.
	if len(doc.SecuritySchemes) != 1 || doc.SecuritySchemes[0].HeaderName != "PRIVATE-TOKEN" {
		t.Fatalf("security schemes: %+v", doc.SecuritySchemes)
	}
	if len(doc.Operations) != 2 {
		t.Fatalf("only GET operations must be extracted: %+v", doc.Operations)
	}
	var getReview *SpecOperation
	for i := range doc.Operations {
		if doc.Operations[i].ID == "getReview" {
			getReview = &doc.Operations[i]
		}
	}
	if getReview == nil {
		t.Fatal("getReview operation missing")
	}
	if len(getReview.PathParams) != 2 || getReview.PathParams[0].Name != "repository" {
		t.Fatalf("path params: %+v", getReview.PathParams)
	}
	// Local $ref chains resolve into the bounded schema tree.
	schema := getReview.ResponseSchema
	if schema == nil || schema.Properties["state"] == nil || len(schema.Properties["state"].Enum) != 3 {
		t.Fatalf("response schema: %+v", schema)
	}
	if schema.Properties["source"] == nil || schema.Properties["source"].Properties["latestCommit"] == nil {
		t.Fatal("nested $ref did not resolve")
	}
	var diff *SpecOperation
	for i := range doc.Operations {
		if doc.Operations[i].ID == "getReviewDiff" {
			diff = &doc.Operations[i]
		}
	}
	if diff == nil || !diff.ProducesText {
		t.Fatal("text diff endpoint not detected")
	}
}

func TestParseDocumentOpenAPI31YAML(t *testing.T) {
	doc, err := ParseDocument([]byte(openapi31Fixture))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Version != "openapi:3.1" {
		t.Fatalf("version: %s", doc.Version)
	}
	if len(doc.ServerURLs) != 2 || doc.ServerURLs[0] != "https://api.review.internal/v1" {
		t.Fatalf("server urls: %v", doc.ServerURLs)
	}
	if len(doc.SecuritySchemes) != 1 || doc.SecuritySchemes[0].HeaderName != "Authorization" ||
		doc.SecuritySchemes[0].ValuePrefix != "Bearer " {
		t.Fatalf("security schemes: %+v", doc.SecuritySchemes)
	}
	if len(doc.Operations) != 1 {
		t.Fatalf("operations: %+v", doc.Operations)
	}
	operation := doc.Operations[0]
	if len(operation.PathParams) != 2 || len(operation.QueryParams) != 1 || operation.QueryParams[0].Name != "page" {
		t.Fatalf("params: %+v / %+v", operation.PathParams, operation.QueryParams)
	}
	values := operation.ResponseSchema.Properties["values"]
	if values == nil || values.Type != "array" || values.Items.Properties["sha"] == nil {
		t.Fatalf("list schema: %+v", values)
	}
	// OpenAPI 3.1 union type [string, "null"] normalizes to string.
	if values.Items.Properties["subject"].Type != "string" {
		t.Fatalf("union type: %+v", values.Items.Properties["subject"])
	}
}

func TestParseDocumentRejectsExternalRef(t *testing.T) {
	fixture := `{
	  "openapi": "3.0.3",
	  "paths": {"/x": {"get": {"responses": {"200": {"description": "ok", "content": {"application/json": {"schema": {"$ref": "https://evil.example/schema.json"}}}}}}}}
	}`
	_, err := ParseDocument([]byte(fixture))
	var docErr *DocumentError
	if !errors.As(err, &docErr) || docErr.Code != IssueExternalReference {
		t.Fatalf("external $ref must be rejected: %v", err)
	}
}

func TestParseDocumentLimits(t *testing.T) {
	if _, err := ParseDocument(nil); err == nil {
		t.Fatal("empty document accepted")
	}
	if _, err := ParseDocument([]byte(strings.Repeat("x", maxDocumentBytes+1))); err == nil {
		t.Fatal("oversized document accepted")
	}
	if _, err := ParseDocument([]byte(`{"openapi":"9.9"}`)); err == nil {
		t.Fatal("unknown spec version accepted")
	}
	if _, err := ParseDocument([]byte(`{"openapi":"3.0.3","paths":{}}`)); err == nil {
		t.Fatal("document without paths accepted")
	}
	// Excessive nesting is rejected.
	nested := strings.Repeat(`{"a":`, maxDocumentDepth+2) + `0` + strings.Repeat(`}`, maxDocumentDepth+2)
	if _, err := ParseDocument([]byte(nested)); err == nil {
		t.Fatal("deeply nested document accepted")
	}
}

func TestParseDocumentDigestIsStable(t *testing.T) {
	first, err := ParseDocument([]byte(swagger2Fixture))
	if err != nil {
		t.Fatal(err)
	}
	second, err := ParseDocument([]byte(swagger2Fixture))
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest != second.Digest || !strings.HasPrefix(first.Digest, "sha256:") {
		t.Fatalf("digest unstable: %q vs %q", first.Digest, second.Digest)
	}
}
