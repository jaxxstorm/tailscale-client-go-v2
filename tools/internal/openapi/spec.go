package openapi

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type Spec struct {
	document map[string]any
}

type Operation struct {
	Method         string
	Path           string
	NormalizedPath string
	OperationID    string
	Summary        string
	Tags           []string
}

func Load(path string) (*Spec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read spec: %w", err)
	}

	return LoadBytes(data)
}

func LoadBytes(data []byte) (*Spec, error) {
	var document map[string]any
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("parse spec: %w", err)
	}

	return &Spec{document: document}, nil
}

func NormalizePath(path string) string {
	path = strings.TrimPrefix(path, "/api/v2")
	if path == "" {
		path = "/"
	}

	segments := strings.Split(path, "/")
	for i, segment := range segments {
		if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
			segments[i] = "{}"
		}
	}

	return strings.Join(segments, "/")
}

func (s *Spec) Operations() ([]Operation, error) {
	paths, ok := mapValue(s.document["paths"])
	if !ok {
		return nil, fmt.Errorf("spec is missing paths")
	}

	pathKeys := sortedKeys(paths)
	operations := make([]Operation, 0)
	methods := []string{"get", "post", "put", "patch", "delete"}

	for _, path := range pathKeys {
		pathItem, ok := mapValue(paths[path])
		if !ok {
			continue
		}

		for _, method := range methods {
			operation, ok := mapValue(pathItem[method])
			if !ok {
				continue
			}

			operations = append(operations, Operation{
				Method:         strings.ToUpper(method),
				Path:           path,
				NormalizedPath: NormalizePath(path),
				OperationID:    stringValue(operation["operationId"]),
				Summary:        stringValue(operation["summary"]),
				Tags:           stringSlice(operation["tags"]),
			})
		}
	}

	return operations, nil
}

func (s *Spec) DeviceProperties() ([]string, error) {
	candidates := []struct {
		method       string
		path         string
		selectSchema func(any) (any, error)
	}{
		{
			method: "get",
			path:   "/device/{deviceId}",
			selectSchema: func(schema any) (any, error) {
				return schema, nil
			},
		},
		{
			method: "get",
			path:   "/tailnet/{tailnet}/devices",
			selectSchema: func(schema any) (any, error) {
				root, ok := mapValue(schema)
				if !ok {
					return nil, fmt.Errorf("device collection response schema is not an object")
				}

				properties, ok := mapValue(root["properties"])
				if !ok {
					return nil, fmt.Errorf("device collection response schema has no properties")
				}

				devices, ok := mapValue(properties["devices"])
				if !ok {
					return nil, fmt.Errorf("device collection response schema has no devices property")
				}

				items, ok := mapValue(devices["items"])
				if !ok {
					return nil, fmt.Errorf("device collection devices property has no items schema")
				}

				return items, nil
			},
		},
	}

	paths := make(map[string]struct{})
	for _, candidate := range candidates {
		operation, err := s.operation(candidate.path, candidate.method)
		if err != nil {
			return nil, err
		}

		schema, err := responseSchema(operation)
		if err != nil {
			return nil, fmt.Errorf("%s %s: %w", strings.ToUpper(candidate.method), candidate.path, err)
		}

		selected, err := candidate.selectSchema(schema)
		if err != nil {
			return nil, fmt.Errorf("%s %s: %w", strings.ToUpper(candidate.method), candidate.path, err)
		}

		if err := s.collectLeafProperties(selected, "", paths, map[string]bool{}); err != nil {
			return nil, fmt.Errorf("%s %s: %w", strings.ToUpper(candidate.method), candidate.path, err)
		}
	}

	return sortedSet(paths), nil
}

func (s *Spec) operation(path, method string) (map[string]any, error) {
	paths, ok := mapValue(s.document["paths"])
	if !ok {
		return nil, fmt.Errorf("spec is missing paths")
	}

	pathItem, ok := mapValue(paths[path])
	if !ok {
		return nil, fmt.Errorf("path not found")
	}

	operation, ok := mapValue(pathItem[strings.ToLower(method)])
	if !ok {
		return nil, fmt.Errorf("operation not found")
	}

	return operation, nil
}

func responseSchema(operation map[string]any) (any, error) {
	responses, ok := mapValue(operation["responses"])
	if !ok {
		return nil, fmt.Errorf("responses are missing")
	}

	var response map[string]any
	for _, status := range []string{"200", "201", "202"} {
		if candidate, ok := mapValue(responses[status]); ok {
			response = candidate
			break
		}
	}
	if response == nil {
		return nil, fmt.Errorf("no JSON success response found")
	}

	content, ok := mapValue(response["content"])
	if !ok {
		return nil, fmt.Errorf("response content is missing")
	}

	mediaType, ok := mapValue(content["application/json"])
	if !ok {
		return nil, fmt.Errorf("application/json content is missing")
	}

	schema, ok := mediaType["schema"]
	if !ok {
		return nil, fmt.Errorf("response schema is missing")
	}

	return schema, nil
}

func (s *Spec) collectLeafProperties(schema any, prefix string, out map[string]struct{}, stack map[string]bool) error {
	schemaMap, ok := mapValue(schema)
	if !ok {
		if prefix != "" {
			out[prefix] = struct{}{}
		}
		return nil
	}

	if ref := stringValue(schemaMap["$ref"]); ref != "" {
		if stack[ref] {
			return nil
		}

		resolved, err := s.resolveRef(ref)
		if err != nil {
			return err
		}

		nextStack := copyStack(stack)
		nextStack[ref] = true
		return s.collectLeafProperties(resolved, prefix, out, nextStack)
	}

	structured := false
	for _, combiner := range []string{"allOf", "anyOf", "oneOf"} {
		items, ok := sliceValue(schemaMap[combiner])
		if !ok {
			continue
		}

		structured = true
		for _, item := range items {
			if err := s.collectLeafProperties(item, prefix, out, stack); err != nil {
				return err
			}
		}
	}

	if properties, ok := mapValue(schemaMap["properties"]); ok && len(properties) > 0 {
		structured = true
		for _, name := range sortedKeys(properties) {
			if err := s.collectLeafProperties(properties[name], joinPath(prefix, name), out, stack); err != nil {
				return err
			}
		}
	}

	if items, ok := mapValue(schemaMap["items"]); ok {
		structured = true
		if err := s.collectLeafProperties(items, prefix, out, stack); err != nil {
			return err
		}
	}

	if additionalProperties, ok := mapValue(schemaMap["additionalProperties"]); ok {
		structured = true
		if err := s.collectLeafProperties(additionalProperties, prefix, out, stack); err != nil {
			return err
		}
	}

	if !structured && prefix != "" {
		out[prefix] = struct{}{}
	}

	return nil
}

func (s *Spec) resolveRef(ref string) (map[string]any, error) {
	if !strings.HasPrefix(ref, "#/") {
		return nil, fmt.Errorf("unsupported ref %q", ref)
	}

	current := any(s.document)
	for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		node, ok := mapValue(current)
		if !ok {
			return nil, fmt.Errorf("ref %q does not resolve to an object", ref)
		}

		var exists bool
		current, exists = node[decodePointerToken(part)]
		if !exists {
			return nil, fmt.Errorf("ref %q is missing token %q", ref, part)
		}
	}

	resolved, ok := mapValue(current)
	if !ok {
		return nil, fmt.Errorf("ref %q does not resolve to an object", ref)
	}

	return resolved, nil
}

func decodePointerToken(token string) string {
	token = strings.ReplaceAll(token, "~1", "/")
	token = strings.ReplaceAll(token, "~0", "~")
	return token
}

func mapValue(value any) (map[string]any, bool) {
	if value == nil {
		return nil, false
	}

	typed, ok := value.(map[string]any)
	return typed, ok
}

func sliceValue(value any) ([]any, bool) {
	if value == nil {
		return nil, false
	}

	typed, ok := value.([]any)
	return typed, ok
}

func stringValue(value any) string {
	typed, _ := value.(string)
	return typed
}

func stringSlice(value any) []string {
	values, ok := sliceValue(value)
	if !ok {
		return nil
	}

	out := make([]string, 0, len(values))
	for _, value := range values {
		if text := stringValue(value); text != "" {
			out = append(out, text)
		}
	}

	return out
}

func sortedKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}

	sort.Strings(keys)
	return keys
}

func sortedSet(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}

	sort.Strings(keys)
	return keys
}

func joinPath(prefix, name string) string {
	if prefix == "" {
		return name
	}

	return prefix + "." + name
}

func copyStack(values map[string]bool) map[string]bool {
	out := make(map[string]bool, len(values)+1)
	for key, value := range values {
		out[key] = value
	}

	return out
}
