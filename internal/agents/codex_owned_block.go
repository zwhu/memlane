package agents

import (
	"bytes"
	"errors"
	"reflect"

	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

// Codex's official plugin CLI may reformat TOML without changing MCP values.
// Keep the ownership markers and compare both their complete parsed contents
// and the full file's memlane namespace before accepting or removing a block.
func ownedMCPBlock(data []byte, i Installation) ([]byte, error) {
	fail := func() ([]byte, error) {
		return nil, errors.New("Memlane MCP entry changed externally; configuration preserved")
	}
	if !i.Options.ComputerLevel || i.Connection.Agent != "codex" {
		if i.Block == "" || !bytes.Contains(data, []byte(i.Block)) {
			return fail()
		}
		return []byte(i.Block), nil
	}
	begin := []byte("# Memlane " + i.Connection.ID + " begin\n")
	end := []byte("# Memlane " + i.Connection.ID + " end")
	if bytes.Count(data, begin) != 1 || bytes.Count(data, end) != 1 {
		return fail()
	}
	start := bytes.Index(data, begin)
	finish := bytes.Index(data, end)
	if finish < start || start > 0 && data[start-1] != '\n' || finish > 0 && data[finish-1] != '\n' {
		return fail()
	}
	finish += len(end)
	if finish < len(data) {
		if data[finish] != '\n' {
			return fail()
		}
		finish++
	}
	// toml_edit can keep the closing ownership comment at EOF while inserting
	// plugin tables before it. Never treat those tables as removable MCP data.
	limit := finish
	var parser unstable.Parser
	parser.Reset(data)
	for parser.NextExpression() {
		n := parser.Expression()
		if n.Kind != unstable.Table && n.Kind != unstable.ArrayTable {
			continue
		}
		keys := n.Key()
		var parts []string
		offset := -1
		for keys.Next() {
			k := keys.Node()
			if offset < 0 {
				offset = int(k.Raw.Offset)
			}
			parts = append(parts, string(k.Data))
		}
		if offset <= start || offset >= finish {
			continue
		}
		parentTable := len(parts) == 1 && parts[0] == "mcp_servers"
		if !parentTable && (len(parts) < 2 || parts[0] != "mcp_servers" || parts[1] != "memlane") {
			limit = bytes.LastIndexByte(data[:offset], '\n') + 1
			break
		}
	}
	if parser.Error() != nil {
		return fail()
	}
	block := data[start:limit]
	var expected, current, whole map[string]any
	if toml.Unmarshal([]byte(i.Block), &expected) != nil || toml.Unmarshal(block, &current) != nil || toml.Unmarshal(data, &whole) != nil || !reflect.DeepEqual(expected, current) {
		return fail()
	}
	servers, ok := expected["mcp_servers"].(map[string]any)
	if !ok || len(servers) != 1 || servers["memlane"] == nil || len(expected) != 1 {
		return fail()
	}
	actual, ok := whole["mcp_servers"].(map[string]any)
	if !ok || !reflect.DeepEqual(servers["memlane"], actual["memlane"]) {
		return fail()
	}
	if bytes.Contains(data, []byte(i.Block)) {
		return []byte(i.Block), nil
	}
	return block, nil
}

func removeOwnedMCPBlock(data []byte, i Installation) ([]byte, error) {
	block, err := ownedMCPBlock(data, i)
	if err != nil {
		return nil, err
	}
	out := bytes.Replace(data, block, nil, 1)
	if i.Options.ComputerLevel && i.Connection.Agent == "codex" {
		end := []byte("# Memlane " + i.Connection.ID + " end")
		if !bytes.Contains(block, end) {
			if bytes.Contains(out, append(append([]byte(nil), end...), '\n')) {
				end = append(end, '\n')
			}
			out = bytes.Replace(out, end, nil, 1)
		}
	}
	return out, nil
}
