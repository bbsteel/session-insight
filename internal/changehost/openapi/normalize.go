package openapi

import (
	"fmt"
	"sort"
	"strings"
)

// normalize.go: version-specific extraction onto the unified Document shape.
// Swagger 2.0 (host/basePath/schemes, securityDefinitions, inline response
// schemas) and OpenAPI 3.x (servers, components.securitySchemes, content
// media types) both land on the same SpecOperation/SpecSchema values.

// extractServerURLs derives candidate API base URLs. OpenAPI 3 servers may be
// relative; they are kept as declared and resolved against the user-provided
// API base URL later. Swagger 2.0 prefers https schemes.
func extractServerURLs(root map[string]any, version string) []string {
	seen := map[string]bool{}
	urls := []string{}
	add := func(raw string) {
		raw = strings.TrimSpace(raw)
		if raw == "" || seen[raw] || len(urls) >= maxEndpointOrigins {
			return
		}
		seen[raw] = true
		urls = append(urls, raw)
	}
	if version == "swagger:2.0" {
		host := getString(root, "host")
		basePath := getString(root, "basePath")
		schemes := []string{}
		for _, scheme := range getArray(root, "schemes") {
			if text, ok := scheme.(string); ok && (text == "https" || text == "http") {
				schemes = append(schemes, text)
			}
		}
		sort.SliceStable(schemes, func(i, j int) bool {
			return schemes[i] == "https" && schemes[j] != "https"
		})
		if len(schemes) == 0 {
			schemes = []string{"https"}
		}
		if host != "" {
			for _, scheme := range schemes {
				add(scheme + "://" + host + basePath)
			}
		}
		return urls
	}
	for _, server := range getArray(root, "servers") {
		if object, ok := server.(map[string]any); ok {
			add(getString(object, "url"))
		}
	}
	return urls
}

// extractSecuritySchemes keeps only header-based schemes the V1 credential
// contract supports: HTTP bearer and apiKey-in-header. Everything else
// (query tokens, OAuth, cookies) is deliberately dropped — an operation that
// needs them cannot be adapted in V1.
func extractSecuritySchemes(root map[string]any, version string) []SecurityScheme {
	var container map[string]any
	if version == "swagger:2.0" {
		container = getMap(root, "securityDefinitions")
	} else {
		container = getMap(getMap(root, "components"), "securitySchemes")
	}
	names := make([]string, 0, len(container))
	for name := range container {
		names = append(names, name)
	}
	sort.Strings(names)
	schemes := []SecurityScheme{}
	for _, name := range names {
		scheme, ok := container[name].(map[string]any)
		if !ok {
			continue
		}
		schemeType := getString(scheme, "type")
		isAPIKeyHeader := schemeType == "apiKey" && getString(scheme, "in") == "header"
		isBearer := version != "swagger:2.0" && schemeType == "http" &&
			strings.EqualFold(getString(scheme, "scheme"), "bearer")
		switch {
		case isAPIKeyHeader:
			if header := getString(scheme, "name"); header != "" {
				schemes = append(schemes, SecurityScheme{Name: name, HeaderName: header})
			}
		case isBearer:
			schemes = append(schemes, SecurityScheme{Name: name, HeaderName: "Authorization", ValuePrefix: "Bearer "})
		}
	}
	return schemes
}

// extractOperations normalizes every GET/HEAD operation. Other methods are
// never extracted, so a write endpoint cannot leak into a profile.
func extractOperations(root map[string]any, version string) ([]SpecOperation, error) {
	paths := getMap(root, "paths")
	if len(paths) == 0 {
		return nil, &DocumentError{Code: IssueDocumentInvalid, Detail: "document declares no paths"}
	}
	if len(paths) > maxDocumentPaths {
		return nil, &DocumentError{Code: IssueDocumentInvalid, Detail: "document declares too many paths"}
	}
	pathNames := make([]string, 0, len(paths))
	for path := range paths {
		pathNames = append(pathNames, path)
	}
	sort.Strings(pathNames)

	resolver := &refResolver{root: root}
	operations := []SpecOperation{}
	for _, path := range pathNames {
		pathItem, ok := paths[path].(map[string]any)
		if !ok {
			continue
		}
		sharedParams := extractParameters(getArray(pathItem, "parameters"), resolver)
		for _, method := range []string{"get", "head"} {
			operationRaw, ok := pathItem[method].(map[string]any)
			if !ok {
				continue
			}
			operation := SpecOperation{
				ID:          boundedText(getString(operationRaw, "operationId")),
				Method:      strings.ToUpper(method),
				Path:        path,
				Summary:     boundedText(getString(operationRaw, "summary")),
				Description: boundedText(getString(operationRaw, "description")),
			}
			for _, tag := range getArray(operationRaw, "tags") {
				if text, ok := tag.(string); ok {
					operation.Tags = append(operation.Tags, boundedText(text))
				}
			}
			if operation.ID == "" {
				operation.ID = method + " " + path
			}
			seenParam := map[string]bool{}
			for _, parameter := range append(extractParameters(getArray(operationRaw, "parameters"), resolver), sharedParams...) {
				if seenParam[parameter.Name] {
					continue
				}
				seenParam[parameter.Name] = true
				switch parameter.In {
				case "path":
					operation.PathParams = append(operation.PathParams, parameter)
				case "query":
					operation.QueryParams = append(operation.QueryParams, parameter)
				}
			}
			fillOperationResponse(&operation, operationRaw, version, resolver)
			operations = append(operations, operation)
		}
	}
	return operations, nil
}

// extractParameters reads parameter objects, resolving local $ref values.
func extractParameters(raw []any, resolver *refResolver) []SpecParameter {
	parameters := []SpecParameter{}
	for _, item := range raw {
		object, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if ref := getString(object, "$ref"); ref != "" {
			resolved, err := resolver.resolveValue(ref, 0)
			if err != nil {
				continue
			}
			object, ok = resolved.(map[string]any)
			if !ok {
				continue
			}
		}
		location := getString(object, "in")
		if location != "path" && location != "query" {
			continue
		}
		parameter := SpecParameter{
			Name:        boundedText(getString(object, "name")),
			In:          location,
			Required:    object["required"] == true,
			Description: boundedText(getString(object, "description")),
		}
		if schema := getMap(object, "schema"); schema != nil {
			if resolved, err := resolver.resolveSchema(schema, 0); err == nil && resolved != nil {
				parameter.Type = resolved.Type
				parameter.Description = firstNonEmpty(parameter.Description, boundedText(resolved.Description))
			}
		} else {
			// Swagger 2.0 non-body parameters carry the type inline.
			parameter.Type = getString(object, "type")
		}
		if example := object["example"]; example != nil {
			parameter.Example = boundedText(fmt.Sprintf("%v", example))
		}
		if parameter.Name != "" {
			parameters = append(parameters, parameter)
		}
	}
	return parameters
}

// fillOperationResponse extracts the success JSON schema (resolving local
// refs), a bounded inline example, and whether a plain-text diff is produced.
func fillOperationResponse(operation *SpecOperation, operationRaw map[string]any, version string, resolver *refResolver) {
	responses := getMap(operationRaw, "responses")
	if responses == nil {
		return
	}
	var success map[string]any
	for _, code := range []string{"200", "201", "default"} {
		if candidate, ok := responses[code].(map[string]any); ok {
			success = candidate
			break
		}
	}
	if success == nil {
		return
	}
	if ref := getString(success, "$ref"); ref != "" {
		resolved, err := resolver.resolveValue(ref, 0)
		if err == nil {
			success, _ = resolved.(map[string]any)
		}
	}
	if success == nil {
		return
	}

	var schemaRaw map[string]any
	var example any
	if version == "swagger:2.0" {
		schemaRaw = getMap(success, "schema")
		example = success["example"]
		for _, produced := range getArray(operationRaw, "produces") {
			if text, ok := produced.(string); ok && strings.HasPrefix(text, "text/") {
				operation.ProducesText = true
			}
		}
	} else {
		content := getMap(success, "content")
		mediaNames := make([]string, 0, len(content))
		for name := range content {
			mediaNames = append(mediaNames, name)
		}
		sort.Strings(mediaNames)
		for _, mediaName := range mediaNames {
			media, ok := content[mediaName].(map[string]any)
			if !ok {
				continue
			}
			if strings.HasPrefix(mediaName, "text/") {
				operation.ProducesText = true
				continue
			}
			if !strings.Contains(mediaName, "json") {
				continue
			}
			schemaRaw = getMap(media, "schema")
			example = media["example"]
			if example == nil {
				if examples := getMap(media, "examples"); len(examples) > 0 {
					names := make([]string, 0, len(examples))
					for name := range examples {
						names = append(names, name)
					}
					sort.Strings(names)
					if first, ok := examples[names[0]].(map[string]any); ok {
						example = first["value"]
					}
				}
			}
			break
		}
	}
	if schemaRaw != nil {
		if schema, err := resolver.resolveSchema(schemaRaw, 0); err == nil {
			operation.ResponseSchema = schema
		}
	}
	operation.ResponseExample = boundExample(example, 0)
}

// boundExample caps example values so a huge inline example cannot blow up
// the in-memory document representation.
func boundExample(value any, depth int) any {
	if value == nil || depth > maxSchemaDepth {
		return nil
	}
	switch typed := value.(type) {
	case string:
		return boundedText(typed)
	case map[string]any:
		if len(typed) > maxSchemaProperties {
			return nil
		}
		bounded := make(map[string]any, len(typed))
		for key, item := range typed {
			bounded[key] = boundExample(item, depth+1)
		}
		return bounded
	case []any:
		if len(typed) > maxSchemaProperties {
			typed = typed[:maxSchemaProperties]
		}
		bounded := make([]any, 0, len(typed))
		for _, item := range typed {
			bounded = append(bounded, boundExample(item, depth+1))
		}
		return bounded
	default:
		return typed
	}
}

// refResolver resolves local (#/...) references with a depth cap. External
// refs were already rejected at parse time; the resolver defends against
// cycles instead.
type refResolver struct {
	root map[string]any
}

func (r *refResolver) resolveValue(ref string, depth int) (any, error) {
	if depth > maxSchemaDepth*2 {
		return nil, fmt.Errorf("reference depth exceeded")
	}
	if !strings.HasPrefix(ref, "#/") {
		return nil, fmt.Errorf("external reference rejected")
	}
	var current any = r.root
	for _, segment := range strings.Split(ref[2:], "/") {
		segment = strings.ReplaceAll(strings.ReplaceAll(segment, "~1", "/"), "~0", "~")
		object, ok := current.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("reference does not resolve")
		}
		current, ok = object[segment]
		if !ok {
			return nil, fmt.Errorf("reference does not resolve")
		}
	}
	if object, ok := current.(map[string]any); ok {
		if nested := getString(object, "$ref"); nested != "" {
			return r.resolveValue(nested, depth+1)
		}
	}
	return current, nil
}

// resolveSchema builds the bounded SpecSchema tree for one schema object.
func (r *refResolver) resolveSchema(raw map[string]any, depth int) (*SpecSchema, error) {
	if depth > maxSchemaDepth {
		return nil, fmt.Errorf("schema depth exceeded")
	}
	if ref := getString(raw, "$ref"); ref != "" {
		resolved, err := r.resolveValue(ref, depth)
		if err != nil {
			return nil, err
		}
		object, ok := resolved.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("schema reference does not resolve")
		}
		return r.resolveSchema(object, depth+1)
	}
	schema := &SpecSchema{
		Type:        schemaType(raw),
		Format:      boundedText(getString(raw, "format")),
		Description: boundedText(getString(raw, "description")),
	}
	for _, value := range getArray(raw, "enum") {
		if len(schema.Enum) >= maxEnumValues {
			break
		}
		if text, ok := value.(string); ok {
			schema.Enum = append(schema.Enum, boundedText(text))
		}
	}
	properties := getMap(raw, "properties")
	if len(properties) > 0 {
		names := make([]string, 0, len(properties))
		for name := range properties {
			names = append(names, name)
		}
		sort.Strings(names)
		if len(names) > maxSchemaProperties {
			names = names[:maxSchemaProperties]
		}
		schema.Properties = make(map[string]*SpecSchema, len(names))
		for _, name := range names {
			child, ok := properties[name].(map[string]any)
			if !ok {
				continue
			}
			resolved, err := r.resolveSchema(child, depth+1)
			if err != nil {
				continue
			}
			schema.Properties[name] = resolved
		}
	}
	if items := getMap(raw, "items"); len(items) > 0 {
		if resolved, err := r.resolveSchema(items, depth+1); err == nil {
			schema.Items = resolved
		}
	}
	return schema, nil
}

// schemaType normalizes the JSON-schema type field, including the OpenAPI
// 3.1 array form (["string","null"]).
func schemaType(raw map[string]any) string {
	switch typed := raw["type"].(type) {
	case string:
		return typed
	case []any:
		for _, item := range typed {
			if text, ok := item.(string); ok && text != "null" {
				return text
			}
		}
	}
	if getMap(raw, "properties") != nil {
		return "object"
	}
	if getMap(raw, "items") != nil {
		return "array"
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
