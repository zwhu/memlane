package agents

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var errTOML = errors.New("invalid TOML")

type tomlEntry struct {
	path             []string
	line, value, end int
	table            bool
	valueTree        jsonNode
}

type tomlParser struct {
	s string
	i int
}

func (p *tomlParser) at() byte {
	if p.i == len(p.s) {
		return 0
	}
	return p.s[p.i]
}

func (p *tomlParser) take(c byte) bool {
	if p.i < len(p.s) && p.at() == c {
		p.i++
		return true
	}
	return false
}

func (p *tomlParser) space(lines bool) {
	for p.i < len(p.s) {
		switch p.at() {
		case ' ', '\t', '\r':
			p.i++
		case '\n':
			if !lines {
				return
			}
			p.i++
		case '#':
			if !lines {
				return
			}
			for p.i < len(p.s) && p.at() != '\n' {
				p.i++
			}
		default:
			return
		}
	}
}

func (p *tomlParser) quoted(multiline bool) (string, error) {
	quote := p.at()
	p.i++
	delim := string(quote)
	if multiline && strings.HasPrefix(p.s[p.i:], delim+delim) {
		delim += delim + delim
		p.i += 2
		if strings.HasPrefix(p.s[p.i:], "\r\n") {
			p.i += 2
		} else {
			p.take('\n')
		}
	}
	var out strings.Builder
	for p.i < len(p.s) {
		if strings.HasPrefix(p.s[p.i:], delim) {
			p.i += len(delim)
			if len(delim) == 3 {
				for n := 0; n < 2 && p.take(quote); n++ {
					out.WriteByte(quote)
				}
			}
			return out.String(), nil
		}
		c := p.at()
		p.i++
		if c == '\n' && len(delim) == 1 || c < 32 && c != '\t' && c != '\r' && c != '\n' {
			return "", errTOML
		}
		if c != '\\' || quote == '\'' {
			out.WriteByte(c)
			continue
		}
		escape := p.i - 1
		if len(delim) == 3 {
			p.space(false)
			if p.take('\n') {
				for p.i < len(p.s) && strings.ContainsRune(" \t\r\n", rune(p.at())) {
					p.i++
				}
				continue
			}
			p.i = escape + 1
		}
		width := 2
		switch p.at() {
		case 'u':
			width = 6
		case 'U':
			width = 10
		case 'b', 't', 'n', 'f', 'r', '"', '\\':
		default:
			return "", errTOML
		}
		if escape+width > len(p.s) {
			return "", errTOML
		}
		decoded, err := strconv.Unquote("\"" + p.s[escape:escape+width] + "\"")
		if err != nil {
			return "", errTOML
		}
		out.WriteString(decoded)
		p.i = escape + width
	}
	return "", errTOML
}

func (p *tomlParser) key() ([]string, error) {
	var path []string
	for {
		p.space(false)
		start := p.i
		var key string
		if p.at() == '"' || p.at() == '\'' {
			value, err := p.quoted(false)
			if err != nil {
				return nil, err
			}
			key = value
		} else {
			for isBareKey(p.at()) {
				p.i++
			}
			if p.i == start {
				return nil, errTOML
			}
			key = p.s[start:p.i]
		}
		path = append(path, key)
		p.space(false)
		if !p.take('.') {
			return path, nil
		}
	}
}

func isBareKey(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-'
}

func (p *tomlParser) value(depth int) (any, error) {
	return p.valueWithLayout(depth, nil)
}

func (p *tomlParser) valueWithLayout(depth int, layout *jsonNode) (any, error) {
	if depth > 64 {
		return nil, errTOML
	}
	if layout != nil {
		layout.start, layout.open = p.i, -1
		defer func() { layout.end = p.i }()
	}
	switch p.at() {
	case '"', '\'':
		return p.quoted(true)
	case '[':
		return p.arrayValue(depth, layout)
	case '{':
		return p.inlineValue(depth, layout)
	}

	start := p.i
	for p.i < len(p.s) && !strings.ContainsRune(",]}#\r\n\t ", rune(p.at())) {
		p.i++
	}
	raw := p.s[start:p.i]
	switch raw {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	n := strings.ReplaceAll(raw, "_", "")
	base := 10
	if strings.HasPrefix(n, "0x") || strings.HasPrefix(n, "0o") || strings.HasPrefix(n, "0b") {
		base = 0
	}
	if number, err := strconv.ParseInt(n, base, 64); err == nil {
		return number, nil
	}
	if number, err := strconv.ParseFloat(n, 64); err == nil {
		return number, nil
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02", "15:04:05.999999999"} {
		if value, err := time.Parse(layout, raw); err == nil {
			return value, nil
		}
	}
	return nil, errTOML
}

func (p *tomlParser) arrayValue(depth int, layout *jsonNode) (any, error) {
	p.i++
	result := []any{}
	for {
		p.space(true)
		if p.take(']') {
			return result, nil
		}
		entry := jsonMember{start: p.i, comma: -1}
		item, err := p.valueWithLayout(depth+1, &entry.node)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
		p.space(true)
		if p.take(',') {
			entry.comma = p.i - 1
		}
		if layout != nil {
			layout.members = append(layout.members, entry)
		}
		if entry.comma < 0 {
			if p.take(']') {
				return result, nil
			}
			return nil, errTOML
		}
	}
}

func (p *tomlParser) inlineValue(depth int, layout *jsonNode) (any, error) {
	if layout != nil {
		layout.open = p.i
	}
	p.i++
	result := map[string]any{}
	p.space(false)
	if p.take('}') {
		return result, nil
	}
	for {
		p.space(false)
		entry := jsonMember{start: p.i, comma: -1}
		keys, err := p.key()
		entry.keyEnd, entry.colon = p.i, p.i
		if err != nil || !p.take('=') {
			return nil, errTOML
		}
		if len(keys) == 1 {
			entry.key = keys[0]
		} else {
			entry.key = strings.Join(keys, "\x00")
		}
		p.space(false)
		item, err := p.valueWithLayout(depth+1, &entry.node)
		if err != nil || !putTOML(result, keys, item) {
			return nil, errTOML
		}
		p.space(false)
		if p.take(',') {
			entry.comma = p.i - 1
		}
		if layout != nil {
			layout.members = append(layout.members, entry)
		}
		if entry.comma < 0 {
			if p.take('}') {
				return result, nil
			}
			return nil, errTOML
		}
	}
}

func tomlTable(root map[string]any, path []string) map[string]any {
	for _, key := range path {
		switch v := root[key].(type) {
		case nil:
			next := map[string]any{}
			root[key] = next
			root = next
		case map[string]any:
			root = v
		case []map[string]any:
			root = v[len(v)-1]
		default:
			return nil
		}
	}
	return root
}

func putTOML(root map[string]any, path []string, value any) bool {
	table := tomlTable(root, path[:len(path)-1])
	key := path[len(path)-1]
	if table == nil || table[key] != nil {
		return false
	}
	table[key] = value
	return true
}

func parseTOML(data []byte) (map[string]any, []tomlEntry, error) {
	if !utf8.Valid(data) {
		return nil, nil, errTOML
	}
	p := tomlParser{s: string(data)}
	doc := map[string]any{}
	current := doc
	var path []string
	type arrayContext struct {
		keys       []string
		occurrence string
	}
	var arrayStack []arrayContext
	var entries []tomlEntry
	for {
		p.space(true)
		if p.i == len(p.s) {
			return doc, entries, nil
		}
		line := strings.LastIndexByte(p.s[:p.i], '\n') + 1
		if p.take('[') {
			array := p.take('[')
			keys, err := p.key()
			if err != nil || !p.take(']') || array && !p.take(']') {
				return nil, nil, errTOML
			}
			for len(arrayStack) > 0 {
				parent := arrayStack[len(arrayStack)-1].keys
				if len(keys) > len(parent) && slices.Equal(keys[:len(parent)], parent) {
					break
				}
				arrayStack = arrayStack[:len(arrayStack)-1]
			}
			if array {
				parent := tomlTable(doc, keys[:len(keys)-1])
				if parent == nil {
					return nil, nil, errTOML
				}
				key := keys[len(keys)-1]
				rows, ok := parent[key].([]map[string]any)
				if !ok && parent[key] != nil {
					return nil, nil, errTOML
				}
				current = map[string]any{}
				arrayStack = append(arrayStack, arrayContext{keys, strconv.Itoa(len(rows))})
				parent[key] = append(rows, current)
			} else {
				current = tomlTable(doc, keys)
				if current == nil {
					return nil, nil, errTOML
				}
			}
			path = nil
			at := 0
			for _, parent := range arrayStack {
				path = append(path, keys[at:len(parent.keys)]...)
				path = append(path, parent.occurrence)
				at = len(parent.keys)
			}
			path = append(path, keys[at:]...)
			entries = append(entries, tomlEntry{path: path, line: line, end: p.i, table: true})
		} else {
			keys, err := p.key()
			if err != nil || !p.take('=') {
				return nil, nil, errTOML
			}
			p.space(false)
			valueStart := p.i
			var tree jsonNode
			value, err := p.valueWithLayout(0, &tree)
			if err != nil || !putTOML(current, keys, value) {
				return nil, nil, errTOML
			}
			entries = append(entries, tomlEntry{path: append(slices.Clone(path), keys...), line: line, value: valueStart, end: p.i, valueTree: tree})
		}
		p.space(false)
		if p.i < len(p.s) && p.at() != '\n' && p.at() != '#' {
			return nil, nil, errTOML
		}
	}
}

func ParseTOML(data []byte) (map[string]any, error) {
	doc, _, err := parseTOML(data)
	return doc, err
}
