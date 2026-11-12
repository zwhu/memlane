package agents

import (
	"bytes"
	"encoding/json"
	"errors"
	"sort"
)

const CodexCompletionProtocol = "memlane-codex-completion-v2-strict-schema"

// codexCompletionSchema adapts finite Pydantic object models to the strict
// output contract. It only strengthens the request: the caller's original
// schema remains the authority for validating results. Open dictionaries are
// not representable by closing an object, so reject them instead of dropping
// their values. Walk schema positions, never property names or const/enum data.
func codexCompletionSchema(raw json.RawMessage) (json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var root map[string]any
	if err := decoder.Decode(&root); err != nil || root == nil {
		return nil, errors.New("object schema required")
	}
	var walk func(map[string]any, int) error
	walk = func(node map[string]any, depth int) error {
		if depth > 64 {
			return errors.New("schema nesting limit")
		}
		delete(node, "default")
		if node["type"] == "object" || node["properties"] != nil {
			properties, ok := node["properties"].(map[string]any)
			if !ok {
				// Empty closed objects are safe; unbounded dicts are not.
				if node["additionalProperties"] != false {
					return errors.New("open object schema unsupported")
				}
				properties = map[string]any{}
				node["properties"] = properties
			}
			if extra, exists := node["additionalProperties"]; exists && extra != false {
				return errors.New("open object schema unsupported")
			}
			node["additionalProperties"] = false
			names := make([]string, 0, len(properties))
			for name := range properties {
				names = append(names, name)
			}
			sort.Strings(names)
			// Keep an already complete required list in its original order.
			required, _ := node["required"].([]any)
			seen := map[string]bool{}
			for _, value := range required {
				name, valid := value.(string)
				if !valid || seen[name] {
					return errors.New("invalid object requirements")
				}
				if _, exists := properties[name]; !exists {
					return errors.New("undeclared object requirement")
				}
				seen[name] = true
			}
			for _, name := range names {
				if !seen[name] {
					required = append(required, name)
				}
			}
			if required == nil {
				required = []any{}
			}
			node["required"] = required
		}
		for _, key := range []string{"properties", "$defs", "definitions", "patternProperties", "dependentSchemas"} {
			if children, ok := node[key].(map[string]any); ok {
				for _, child := range children {
					if schema, ok := child.(map[string]any); ok {
						if err := walk(schema, depth+1); err != nil {
							return err
						}
					}
				}
			}
		}
		for _, key := range []string{"items", "contains", "not", "if", "then", "else", "propertyNames", "unevaluatedProperties", "unevaluatedItems"} {
			if child, ok := node[key].(map[string]any); ok {
				if err := walk(child, depth+1); err != nil {
					return err
				}
			}
		}
		for _, key := range []string{"anyOf", "allOf", "oneOf", "prefixItems"} {
			if children, ok := node[key].([]any); ok {
				for _, child := range children {
					if schema, ok := child.(map[string]any); ok {
						if err := walk(schema, depth+1); err != nil {
							return err
						}
					}
				}
			}
		}
		return nil
	}
	if err := walk(root, 0); err != nil {
		return nil, err
	}
	return json.Marshal(root)
}
