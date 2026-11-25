package agents

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
)

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

type jsonNode struct {
	start, end, open, close int
	members                 []jsonMember
}
type jsonMember struct {
	key                         string
	start, keyEnd, colon, comma int
	node                        jsonNode
}
type jsoncParser struct {
	data, clean []byte
	i           int
}

func (p *jsoncParser) skip() error {
	for p.i < len(p.data) {
		switch p.data[p.i] {
		case ' ', '\r', '\n', '\t':
			p.i++
		case '/':
			rest := p.data[p.i:]
			end := len(rest)
			switch {
			case bytes.HasPrefix(rest, []byte("//")):
				if next := bytes.IndexByte(rest, '\n'); next >= 0 {
					end = next
				}
			case bytes.HasPrefix(rest, []byte("/*")):
				next := bytes.Index(rest[2:], []byte("*/"))
				if next < 0 {
					return errors.New("unterminated JSON comment")
				}
				end = next + 4
			default:
				return nil
			}
			for j := p.i; j < p.i+end; j++ {
				if p.clean[j] != '\n' {
					p.clean[j] = ' '
				}
			}
			p.i += end
		default:
			return nil
		}
	}
	return nil
}

func (p *jsoncParser) take(c byte) bool {
	if p.i < len(p.data) && p.data[p.i] == c {
		p.i++
		return true
	}
	return false
}

var errJSONC = errors.New("invalid JSON object")

func (p *jsoncParser) stringEnd() bool {
	if !p.take('"') {
		return false
	}
	for p.i < len(p.data) {
		if p.take('"') {
			return true
		}
		p.take('\\')
		if p.i < len(p.data) {
			p.i++
		}
	}
	return false
}

func (p *jsoncParser) value(depth int) (jsonNode, error) {
	if depth > 128 {
		return jsonNode{}, errJSONC
	}
	if err := p.skip(); err != nil {
		return jsonNode{}, err
	}
	node := jsonNode{start: p.i, open: -1}
	if p.i == len(p.data) {
		return node, errJSONC
	}
	switch p.data[p.i] {
	case '{', '[':
		object := p.data[p.i] == '{'
		end := byte(']')
		if object {
			end = '}'
			node.open = p.i
		}
		p.i++
		for {
			if err := p.skip(); err != nil {
				return node, err
			}
			if p.take(end) {
				if n := len(node.members); n > 0 && node.members[n-1].comma >= 0 {
					p.clean[node.members[n-1].comma] = ' '
				}
				node.close = p.i - 1
				break
			}
			entry := jsonMember{start: p.i, comma: -1}
			if object {
				if !p.stringEnd() || json.Unmarshal(p.data[entry.start:p.i], &entry.key) != nil {
					return node, errJSONC
				}
				entry.keyEnd = p.i
				if err := p.skip(); err != nil {
					return node, err
				}
				entry.colon = p.i
				if !p.take(':') {
					return node, errJSONC
				}
			}
			child, err := p.value(depth + 1)
			if err != nil {
				return node, err
			}
			entry.node = child
			if err := p.skip(); err != nil {
				return node, err
			}
			if p.take(',') {
				entry.comma = p.i - 1
			}
			node.members = append(node.members, entry)
			if entry.comma < 0 {
				if !p.take(end) {
					return node, errJSONC
				}
				node.close = p.i - 1
				break
			}
		}
	case '"':
		if !p.stringEnd() {
			return node, errJSONC
		}
	default:
		for p.i < len(p.data) && !strings.ContainsRune(" \r\n\t,]}/", rune(p.data[p.i])) {
			p.i++
		}
		if p.i == node.start {
			return node, errJSONC
		}
	}
	node.end = p.i
	return node, nil
}

func parseJSONC(data []byte) (map[string]any, jsonNode, error) {
	parser := jsoncParser{data: data, clean: slices.Clone(data)}
	node, err := parser.value(0)
	if err != nil {
		return nil, node, err
	}
	if err := parser.skip(); err != nil {
		return nil, node, err
	}
	var doc map[string]any
	if parser.i != len(data) || json.Unmarshal(parser.clean, &doc) != nil || doc == nil {
		return nil, node, errJSONC
	}
	return doc, node, nil
}

func ParseJSONC(data []byte) (map[string]any, error) {
	doc, _, err := parseJSONC(data)
	return doc, err
}

func jsonSpans(node jsonNode, before, after map[string]any) ([]spanEdit, error) {
	var spans []spanEdit
	last := -1
	present := map[string]bool{}
	for i, entry := range node.members {
		if present[entry.key] {
			return nil, errors.New("duplicate JSON key")
		}
		present[entry.key] = true
		if _, ok := after[entry.key]; ok {
			last = i
		}
	}
	additions := []string{}
	for _, key := range sortedKeys(after) {
		if present[key] {
			continue
		}
		k, _ := json.Marshal(key)
		value, err := json.Marshal(after[key])
		if err != nil {
			return nil, err
		}
		additions = append(additions, string(k)+": "+string(value))
	}
	for i, entry := range node.members {
		value, ok := after[entry.key]
		if !ok {
			// Keep comments between the key, colon and value as well as after it.
			spans = append(spans, spanEdit{entry.start, entry.keyEnd, ""}, spanEdit{entry.colon, entry.colon + 1, ""}, spanEdit{entry.node.start, entry.node.end, ""})
		} else if !reflect.DeepEqual(before[entry.key], value) {
			a, aOK := before[entry.key].(map[string]any)
			b, bOK := value.(map[string]any)
			if aOK && bOK && entry.node.open >= 0 {
				children, err := jsonSpans(entry.node, a, b)
				if err != nil {
					return nil, err
				}
				spans = append(spans, children...)
			} else {
				raw, err := json.Marshal(value)
				if err != nil {
					return nil, err
				}
				spans = append(spans, spanEdit{entry.node.start, entry.node.end, string(raw)})
			}
		}
		if entry.comma >= 0 && (!ok || i == last && len(additions) == 0 && i != len(node.members)-1) {
			spans = append(spans, spanEdit{entry.comma, entry.comma + 1, ""})
		}
	}
	if len(additions) > 0 {
		if last >= 0 && node.members[last].comma < 0 {
			pos := node.members[last].node.end
			spans = append(spans, spanEdit{pos, pos, ","})
		}
		spans = append(spans, spanEdit{node.close, node.close, "\n  " + strings.Join(additions, ",\n  ") + "\n"})
	}
	return spans, nil
}

func PreviewJSONEdit(path string, mutator func(map[string]any) error, summary string) (EditPreview, error) {
	raw, err := SafeReadFile(path, configLimit)
	if err != nil {
		return EditPreview{}, err
	}
	after, node, err := parseJSONC(raw)
	if err != nil {
		return EditPreview{}, err
	}
	before, err := ParseJSONC(raw)
	if err != nil {
		return EditPreview{}, err
	}
	if err := mutator(after); err != nil {
		return EditPreview{}, err
	}
	spans, err := jsonSpans(node, before, after)
	if err != nil {
		return EditPreview{}, err
	}
	slices.SortStableFunc(spans, func(a, b spanEdit) int { return a.start - b.start })
	result := []byte(applySpans(string(raw), spans))
	actual, err := ParseJSONC(result)
	if err != nil {
		return EditPreview{}, err
	}
	a, err := json.Marshal(after)
	if err != nil {
		return EditPreview{}, err
	}
	b, err := json.Marshal(actual)
	if err != nil || string(a) != string(b) {
		return EditPreview{}, fmt.Errorf("JSON edit did not preserve the intended document")
	}
	return previewEdit(path, raw, result, summary), nil
}
