package openapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Import-time document limits (design §9.3): the parsed document never
// persists, but parsing itself must stay bounded.
const (
	maxDocumentBytes    = 4 << 20
	maxDocumentDepth    = 32
	maxDocumentPaths    = 400
	maxSchemaDepth      = 12
	maxSchemaProperties = 128
	maxEnumValues       = 64
	maxExtractedText    = 512
)

// SpecOperation is the normalized, adapter-relevant view of one GET/HEAD
// operation. Write operations are never extracted.
type SpecOperation struct {
	ID          string
	Method      string
	Path        string
	Tags        []string
	Summary     string
	Description string
	PathParams  []SpecParameter
	QueryParams []SpecParameter
	// ResponseSchema is the resolved 200/default JSON schema, when declared.
	ResponseSchema *SpecSchema
	// ResponseExample is a bounded inline example value, when declared.
	ResponseExample any
	// ProducesText marks endpoints whose success response is plain text
	// (typical for standalone unified-diff resources).
	ProducesText bool
}

// SpecParameter is one declared path/query parameter.
type SpecParameter struct {
	Name        string
	In          string
	Type        string
	Required    bool
	Description string
	Example     string
}

// SpecSchema is a bounded, locally resolved JSON-schema subset.
type SpecSchema struct {
	Type        string
	Format      string
	Description string
	Properties  map[string]*SpecSchema
	Items       *SpecSchema
	Enum        []string
}

// SecurityScheme is a header-based credential scheme declared by the
// document. Only schemes the V1 authentication contract can execute are
// extracted.
type SecurityScheme struct {
	Name        string
	HeaderName  string
	ValuePrefix string
}

// Document is the normalized internal representation of an imported Swagger /
// OpenAPI document (design §7.1). The raw document text is never persisted;
// Digest is the durable identity of what was analyzed.
type Document struct {
	// Version is "swagger:2.0", "openapi:3.0", or "openapi:3.1".
	Version         string
	ServerURLs      []string
	Operations      []SpecOperation
	SecuritySchemes []SecurityScheme
	Digest          string
}

// DocumentError is a parse/normalize failure carrying a stable issue code.
type DocumentError struct {
	Code   ProfileIssueCode
	Detail string
}

func (e *DocumentError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Detail)
}

// ParseDocument parses and normalizes a Swagger 2.0 or OpenAPI 3.x document.
// Parsing is purely local: external $ref values are rejected so an import can
// never trigger a network fetch.
func ParseDocument(raw []byte) (*Document, error) {
	if len(raw) == 0 {
		return nil, &DocumentError{Code: IssueDocumentInvalid, Detail: "empty document"}
	}
	if len(raw) > maxDocumentBytes {
		return nil, &DocumentError{Code: IssueDocumentInvalid, Detail: "document exceeds the size limit"}
	}
	var root any
	if err := json.Unmarshal(raw, &root); err != nil {
		// Swagger/OpenAPI documents are commonly YAML. YAML v3 is a
		// JSON-compatible superset for our purposes.
		if yamlErr := yaml.Unmarshal(raw, &root); yamlErr != nil {
			return nil, &DocumentError{Code: IssueDocumentInvalid, Detail: "document is neither JSON nor YAML"}
		}
		root = normalizeYAMLValue(root)
	}
	object, ok := root.(map[string]any)
	if !ok {
		return nil, &DocumentError{Code: IssueDocumentInvalid, Detail: "document root must be an object"}
	}
	if depthExceeded(root, 0) {
		return nil, &DocumentError{Code: IssueDocumentInvalid, Detail: "document exceeds the nesting limit"}
	}
	if ref := findExternalRef(root, ""); ref != "" {
		return nil, &DocumentError{Code: IssueExternalReference, Detail: "external $ref values are rejected"}
	}

	doc := &Document{}
	switch {
	case getString(object, "swagger") == "2.0":
		doc.Version = "swagger:2.0"
	case strings.HasPrefix(getString(object, "openapi"), "3.0"):
		doc.Version = "openapi:3.0"
	case strings.HasPrefix(getString(object, "openapi"), "3.1"):
		doc.Version = "openapi:3.1"
	default:
		return nil, &DocumentError{Code: IssueDocumentInvalid, Detail: "unsupported spec version (need Swagger 2.0 or OpenAPI 3.x)"}
	}
	doc.ServerURLs = extractServerURLs(object, doc.Version)
	doc.SecuritySchemes = extractSecuritySchemes(object, doc.Version)
	operations, err := extractOperations(object, doc.Version)
	if err != nil {
		return nil, err
	}
	doc.Operations = operations
	doc.Digest = digestDocument(doc)
	return doc, nil
}

// normalizeYAMLValue converts YAML-specific map key types to plain JSON-able
// values (yaml.v3 already yields map[string]any for string keys, but nested
// values may carry non-string keys from non-JSON documents).
func normalizeYAMLValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			typed[key] = normalizeYAMLValue(item)
		}
		return typed
	case []any:
		for i, item := range typed {
			typed[i] = normalizeYAMLValue(item)
		}
		return typed
	default:
		return value
	}
}

func depthExceeded(value any, depth int) bool {
	if depth > maxDocumentDepth {
		return true
	}
	switch typed := value.(type) {
	case map[string]any:
		for _, item := range typed {
			if depthExceeded(item, depth+1) {
				return true
			}
		}
	case []any:
		for _, item := range typed {
			if depthExceeded(item, depth+1) {
				return true
			}
		}
	}
	return false
}

// findExternalRef returns the first non-local $ref value it encounters.
func findExternalRef(value any, _ string) string {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			if key == "$ref" {
				if ref, ok := item.(string); ok && !strings.HasPrefix(ref, "#/") {
					return ref
				}
				continue
			}
			if found := findExternalRef(item, key); found != "" {
				return found
			}
		}
	case []any:
		for _, item := range typed {
			if found := findExternalRef(item, ""); found != "" {
				return found
			}
		}
	}
	return ""
}

func getString(object map[string]any, key string) string {
	value, _ := object[key].(string)
	return value
}

func getMap(object map[string]any, key string) map[string]any {
	value, _ := object[key].(map[string]any)
	return value
}

func getArray(object map[string]any, key string) []any {
	value, _ := object[key].([]any)
	return value
}

func boundedText(raw string) string {
	if len(raw) > maxExtractedText {
		return raw[:maxExtractedText]
	}
	return raw
}

// digestDocument computes the durable SHA-256 identity of the normalized
// document. The struct is JSON-marshaled (Go maps serialize with sorted
// keys), so the digest is stable across import attempts of equal documents.
func digestDocument(doc *Document) string {
	encoded, err := json.Marshal(struct {
		Version         string
		ServerURLs      []string
		Operations      []SpecOperation
		SecuritySchemes []SecurityScheme
	}{doc.Version, doc.ServerURLs, doc.Operations, doc.SecuritySchemes})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:])
}
