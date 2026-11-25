package bridge

import (
	"encoding/json"
	"errors"
	"math/big"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

var errSchema = errors.New("JSON schema validation failed")

var namedSubschemas = []string{"properties", "$defs", "definitions", "patternProperties", "dependentSchemas"}
var singleSubschemas = []string{"items", "contains", "not", "if", "then", "else", "propertyNames", "unevaluatedProperties", "unevaluatedItems", "additionalProperties"}
var listSubschemas = []string{"anyOf", "allOf", "oneOf", "prefixItems"}

func StrictifySchema(source map[string]any) (map[string]any, error) {
	if source == nil {
		return nil, fault("codex_schema_invalid", 400)
	}
	raw, err := json.Marshal(source)
	var root map[string]any
	if err != nil || decode(raw, &root) != nil {
		return nil, fault("codex_schema_invalid", 400)
	}
	if err := strictNode(root, 0); err != nil {
		return nil, err
	}
	return root, nil
}

func strictNode(node map[string]any, depth int) error {
	invalid := fault("codex_schema_invalid", 400)
	unsupported := fault("codex_schema_unsupported", 400)
	if depth > 64 {
		return invalid
	}
	if !validKeywords(node) {
		return invalid
	}
	for _, key := range []string{"$ref", "$dynamicRef", "$recursiveRef"} {
		if value, ok := node[key]; ok && !strings.HasPrefix(text(value), "#") {
			return unsupported
		}
	}
	delete(node, "default")
	if value, ok := node["oneOf"]; ok {
		if _, found := node["anyOf"]; found || object(node["discriminator"]) == nil {
			return unsupported
		}
		node["anyOf"] = value
		delete(node, "oneOf")
	}
	delete(node, "discriminator")
	props, hasProps := node["properties"]
	if node["type"] == "object" || hasProps {
		if !hasProps {
			if node["additionalProperties"] != false {
				return unsupported
			}
			props = map[string]any{}
			node["properties"] = props
		}
		properties := object(props)
		if properties == nil {
			return invalid
		}
		if value, ok := node["additionalProperties"]; ok && value != false {
			return unsupported
		}
		node["additionalProperties"] = false
		required := append([]any{}, array(node["required"])...)
		seen := map[string]bool{}
		for _, key := range required {
			name, ok := key.(string)
			if _, exists := properties[name]; !ok || !exists || seen[name] {
				return invalid
			}
			seen[name] = true
		}
		rest := []string{}
		for name := range properties {
			if !seen[name] {
				rest = append(rest, name)
			}
		}
		slices.Sort(rest)
		for _, name := range rest {
			required = append(required, name)
		}
		node["required"] = required
	}
	visit := func(value any) error {
		switch v := value.(type) {
		case map[string]any:
			return strictNode(v, depth+1)
		case bool:
			return nil
		default:
			return invalid
		}
	}
	for _, key := range namedSubschemas {
		if value, ok := node[key]; ok {
			children := object(value)
			if children == nil {
				return invalid
			}
			for _, child := range children {
				if err := visit(child); err != nil {
					return err
				}
			}
		}
	}
	for _, key := range singleSubschemas {
		if child, ok := node[key]; ok {
			if err := visit(child); err != nil {
				return err
			}
		}
	}
	for _, key := range listSubschemas {
		if value, ok := node[key]; ok {
			children, ok := value.([]any)
			if !ok || len(children) == 0 {
				return invalid
			}
			for _, child := range children {
				if err := visit(child); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func validKeywords(node map[string]any) bool {
	if raw, ok := node["type"]; ok {
		switch v := raw.(type) {
		case string:
			if !validType(v) {
				return false
			}
		case []any:
			if len(v) == 0 {
				return false
			}
			seen := map[string]bool{}
			for _, item := range v {
				name := text(item)
				if !validType(name) || seen[name] {
					return false
				}
				seen[name] = true
			}
		default:
			return false
		}
	}
	if raw, ok := node["required"]; ok {
		if _, ok := raw.([]any); !ok {
			return false
		}
		seen := map[string]bool{}
		for _, item := range array(raw) {
			key, ok := item.(string)
			if !ok || seen[key] {
				return false
			}
			seen[key] = true
		}
	}
	if raw, ok := node["enum"]; ok && len(array(raw)) == 0 {
		return false
	}
	if raw, ok := node["pattern"]; ok {
		pattern, ok := raw.(string)
		if !ok {
			return false
		}
		if _, err := regexp.Compile(pattern); err != nil {
			return false
		}
	}
	for _, key := range []string{"minLength", "maxLength", "minItems", "maxItems"} {
		if raw, ok := node[key]; ok {
			n := number(raw)
			if n == nil || !n.IsInt() || n.Sign() < 0 {
				return false
			}
		}
	}
	for _, key := range []string{"minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum"} {
		if raw, ok := node[key]; ok && number(raw) == nil {
			return false
		}
	}
	return true
}

func validType(s string) bool {
	return slices.Contains([]string{"object", "array", "string", "number", "integer", "boolean", "null"}, s)
}

func hasType(name string, value any) bool {
	switch name {
	case "null":
		return value == nil
	case "object":
		return object(value) != nil
	case "array":
		_, ok := value.([]any)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "number":
		return number(value) != nil
	case "integer":
		n := number(value)
		return n != nil && n.IsInt()
	default:
		return false
	}
}

type validator struct {
	root      map[string]any
	budget    int
	exhausted bool
}

func (v *validator) reference(ref string) any {
	if ref == "#" {
		return v.root
	}
	if !strings.HasPrefix(ref, "#/") {
		return nil
	}
	pointer, err := url.PathUnescape(ref[2:])
	if err != nil {
		return nil
	}
	var target any = v.root
	for _, key := range strings.Split(pointer, "/") {
		target = object(target)[strings.ReplaceAll(strings.ReplaceAll(key, "~1", "/"), "~0", "~")]
	}
	return target
}

func (v *validator) matches(schema, value any, depth int) bool {
	v.budget--
	if depth > 128 || v.budget < 0 {
		v.exhausted = true
		return false
	}
	if b, ok := schema.(bool); ok {
		return b
	}
	node := object(schema)
	if node == nil {
		return false
	}
	for _, name := range []string{"$ref", "$dynamicRef", "$recursiveRef"} {
		if ref, ok := node[name]; ok && !v.matches(v.reference(text(ref)), value, depth+1) {
			return false
		}
	}
	if t, ok := node["type"]; ok {
		switch types := t.(type) {
		case string:
			if !hasType(types, value) {
				return false
			}
		case []any:
			if !slices.ContainsFunc(types, func(t any) bool { return hasType(text(t), value) }) {
				return false
			}
		default:
			return false
		}
	}
	if raw, ok := node["const"]; ok && !equalJSON(raw, value) {
		return false
	}
	if raw, ok := node["enum"]; ok && !slices.ContainsFunc(array(raw), func(a any) bool { return equalJSON(a, value) }) {
		return false
	}
	for _, key := range []string{"anyOf", "allOf", "oneOf"} {
		if raw, ok := node[key]; ok {
			choices := array(raw)
			if len(choices) == 0 {
				return false
			}
			count := 0
			for _, child := range choices {
				if v.matches(child, value, depth+1) {
					count++
				}
			}
			if key == "anyOf" && count == 0 || key == "allOf" && count != len(choices) || key == "oneOf" && count != 1 {
				return false
			}
		}
	}
	if child, ok := node["not"]; ok && v.matches(child, value, depth+1) {
		return false
	}
	if child, ok := node["if"]; ok {
		key := "else"
		if v.matches(child, value, depth+1) {
			key = "then"
		}
		if branch, ok := node[key]; ok && !v.matches(branch, value, depth+1) {
			return false
		}
	}
	switch x := value.(type) {
	case map[string]any:
		return v.object(node, x, depth)
	case []any:
		return v.list(node, x, depth)
	case string:
		if !bounds(node, "minLength", "maxLength", utf8.RuneCountInString(x)) {
			return false
		}
		if raw, ok := node["pattern"]; ok {
			pattern, err := regexp.Compile(text(raw))
			return err == nil && pattern.MatchString(x)
		}
	}
	if n := number(value); n != nil {
		for _, key := range []string{"minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum"} {
			if raw, ok := node[key]; ok {
				bound := number(raw)
				if bound == nil {
					return false
				}
				cmp := n.Cmp(bound)
				if key == "minimum" && cmp < 0 || key == "maximum" && cmp > 0 || key == "exclusiveMinimum" && cmp <= 0 || key == "exclusiveMaximum" && cmp >= 0 {
					return false
				}
			}
		}
	}
	return true
}

func bounds(node map[string]any, minKey, maxKey string, length int) bool {
	n := new(big.Rat).SetInt64(int64(length))
	for _, key := range []string{minKey, maxKey} {
		if raw, ok := node[key]; ok {
			b := number(raw)
			if b == nil || key == minKey && n.Cmp(b) < 0 || key == maxKey && n.Cmp(b) > 0 {
				return false
			}
		}
	}
	return true
}

func (v *validator) object(node, value map[string]any, depth int) bool {
	for _, key := range array(node["required"]) {
		if _, ok := value[text(key)]; !ok {
			return false
		}
	}
	props := object(node["properties"])
	for key, item := range value {
		found := false
		if schema, ok := props[key]; ok {
			found = true
			if !v.matches(schema, item, depth+1) {
				return false
			}
		}
		for pattern, schema := range object(node["patternProperties"]) {
			p, err := regexp.Compile(pattern)
			if err != nil {
				return false
			}
			if p.MatchString(key) {
				found = true
				if !v.matches(schema, item, depth+1) {
					return false
				}
			}
		}
		if schema, ok := node["additionalProperties"]; ok && !found && !v.matches(schema, item, depth+1) {
			return false
		}
		if schema, ok := node["propertyNames"]; ok && !v.matches(schema, key, depth+1) {
			return false
		}
	}
	for key, schema := range object(node["dependentSchemas"]) {
		if _, ok := value[key]; ok && !v.matches(schema, value, depth+1) {
			return false
		}
	}
	return true
}

func (v *validator) list(node map[string]any, value []any, depth int) bool {
	if !bounds(node, "minItems", "maxItems", len(value)) {
		return false
	}
	prefix := array(node["prefixItems"])
	for i, item := range value {
		if i < len(prefix) {
			if !v.matches(prefix[i], item, depth+1) {
				return false
			}
		} else if schema, ok := node["items"]; ok && !v.matches(schema, item, depth+1) {
			return false
		}
	}
	if schema, ok := node["contains"]; ok {
		return slices.ContainsFunc(value, func(item any) bool { return v.matches(schema, item, depth+1) })
	}
	return true
}

func ValidateJSON(schema map[string]any, value any) error {
	raw, err := json.Marshal(schema)
	var root map[string]any
	if err != nil || decode(raw, &root) != nil || root == nil {
		return errSchema
	}
	return validateDecoded(root, value)
}

func validateDecoded(schema map[string]any, value any) error {
	v := validator{root: schema, budget: 100000}
	if !v.matches(schema, value, 0) || v.exhausted {
		return errSchema
	}
	return nil
}

func ValidateOutput(rawJSON string, originalSchema, transportSchema map[string]any, maxBytes int64) (string, error) {
	if maxBytes == 0 {
		maxBytes = MaxBodyBytes
	}
	if int64(len(rawJSON)) > maxBytes || !utf8.ValidString(rawJSON) {
		return "", errSchema
	}
	var value any
	if decode([]byte(rawJSON), &value) != nil || ValidateJSON(transportSchema, value) != nil {
		return "", errSchema
	}
	if originalSchema != nil {
		if ValidateJSON(originalSchema, value) != nil {
			return "", errSchema
		}
		return rawJSON, nil
	}
	result, ok := object(value)["text"].(string)
	if !ok {
		return "", errSchema
	}
	return result, nil
}
