package agents

import (
	"encoding/json"
	"errors"

	"github.com/tailscale/hujson"
)

func jsonMember(object *hujson.Object, name string) (*hujson.Value, error) {
	seen := map[string]bool{}
	var result *hujson.Value
	for index := range object.Members {
		member := &object.Members[index]
		literal, ok := member.Name.Value.(hujson.Literal)
		if !ok || literal.Kind() != '"' {
			return nil, errors.New("MCP object key is invalid")
		}
		key := literal.String()
		if seen[key] {
			return nil, errors.New("duplicate MCP configuration keys; original preserved")
		}
		seen[key] = true
		if key == name {
			result = &member.Value
		}
	}
	return result, nil
}
func ideConfig(data []byte) (hujson.Value, *hujson.Object, error) {
	if len(data) == 0 {
		data = []byte("{}\n")
	}
	value, err := hujson.Parse(data)
	if err != nil {
		return value, nil, errors.New("IDE MCP JSONC is invalid; original preserved")
	}
	root, ok := value.Value.(*hujson.Object)
	if !ok {
		return value, nil, errors.New("IDE MCP configuration must be an object")
	}
	servers, err := jsonMember(root, "mcpServers")
	if err != nil {
		return value, nil, err
	}
	if servers == nil {
		return value, nil, nil
	}
	object, ok := servers.Value.(*hujson.Object)
	if !ok {
		return value, nil, errors.New("IDE mcpServers must be an object")
	}
	return value, object, nil
}
func ownedIDEEntry(value *hujson.Value) string {
	copied := value.Clone()
	copied.BeforeExtra = nil
	copied.AfterExtra = nil
	return string(copied.Pack())
}
func checkIDEConfig(data []byte, block string) error {
	_, servers, err := ideConfig(data)
	if err != nil {
		return err
	}
	if servers == nil || block == "" {
		return errors.New("IDE Memlane entry is missing; reconnect")
	}
	entry, err := jsonMember(servers, "memlane")
	if err != nil {
		return err
	}
	if entry == nil || ownedIDEEntry(entry) != block {
		return errors.New("IDE Memlane entry changed externally; original preserved")
	}
	return nil
}

// Patch only the owned server value; existing member comments, trailing commas
// and whitespace stay in the parsed syntax tree. No global formatter is run.
func editIDEConfig(data []byte, prior string, newEntry []byte) ([]byte, string, error) {
	value, servers, err := ideConfig(data)
	if err != nil {
		return nil, "", err
	}
	var entry *hujson.Value
	if servers != nil {
		entry, err = jsonMember(servers, "memlane")
		if err != nil {
			return nil, "", err
		}
	}
	if prior == "" && entry != nil {
		return nil, "", errors.New("an unrelated IDE Memlane entry exists; original preserved")
	}
	if prior != "" && (entry == nil || ownedIDEEntry(entry) != prior) {
		return nil, "", errors.New("IDE Memlane entry changed externally; original preserved")
	}
	operations := []map[string]any{}
	if servers == nil {
		if newEntry == nil {
			return nil, "", errors.New("owned IDE entry is missing")
		}
		operations = append(operations, map[string]any{"op": "add", "path": "/mcpServers", "value": map[string]any{}})
	}
	if newEntry == nil {
		operations = append(operations, map[string]any{"op": "remove", "path": "/mcpServers/memlane"})
	} else {
		if !json.Valid(newEntry) {
			return nil, "", errors.New("generated IDE entry is invalid")
		}
		op := "add"
		if entry != nil {
			op = "replace"
		}
		operations = append(operations, map[string]any{"op": op, "path": "/mcpServers/memlane", "value": json.RawMessage(newEntry)})
	}
	patch, _ := json.Marshal(operations)
	if err = value.Patch(patch); err != nil {
		return nil, "", err
	}
	result := value.Pack()
	block := ""
	if newEntry != nil {
		_, servers, err = ideConfig(result)
		if err != nil {
			return nil, "", err
		}
		entry, err = jsonMember(servers, "memlane")
		if err != nil || entry == nil {
			return nil, "", errors.New("generated IDE entry could not be read")
		}
		block = ownedIDEEntry(entry)
	}
	return result, block, nil
}
