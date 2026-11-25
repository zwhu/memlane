package agents

import (
	"errors"
	"path/filepath"
	"slices"
	"strconv"
)

func pathPrefix(prefix, path []string) bool {
	return len(prefix) <= len(path) && slices.Equal(prefix, path[:len(prefix)])
}

func conflictPreview(path string, findings []ConflictFinding) (EditPreview, error) {
	raw, err := SafeReadFile(path, configLimit)
	if err != nil {
		return EditPreview{}, err
	}
	if digest(raw) != findings[0].configDigest {
		return EditPreview{}, errors.New("conflict configuration changed")
	}
	if filepath.Ext(path) != ".toml" {
		return jsonConflictPreview(path, raw, findings)
	}
	_, entries, err := parseTOML(raw)
	if err != nil {
		return EditPreview{}, err
	}
	data := string(raw)
	var spans []spanEdit
	matched := make([]bool, len(findings))
	for _, entry := range entries {
		remove := false
		var nested [][]string
		for i, finding := range findings {
			if pathPrefix(finding.path, entry.path) {
				matched[i] = true
				remove = true
			} else if !entry.table && pathPrefix(entry.path, finding.path) {
				matched[i] = true
				nested = append(nested, finding.path[len(entry.path):])
			}
		}
		if remove {
			spans = append(spans, deleteTOMLEntry(data, entry))
		} else if len(nested) > 0 {
			partial, err := configDeletionSpans(entry.valueTree, nested)
			if err != nil {
				return EditPreview{}, err
			}
			spans = append(spans, partial...)
		}
	}
	if slices.Contains(matched, false) {
		return EditPreview{}, errors.New("unsupported conflict entry layout")
	}
	edited := []byte(applySpans(data, spans))
	if _, err := ParseTOML(edited); err != nil {
		return EditPreview{}, err
	}
	return previewEdit(path, raw, edited, "disable conflicting integrations"), nil
}

func jsonConflictPreview(path string, raw []byte, findings []ConflictFinding) (EditPreview, error) {
	_, node, err := parseJSONC(raw)
	if err != nil {
		return EditPreview{}, err
	}
	paths := make([][]string, len(findings))
	for i, finding := range findings {
		paths[i] = finding.path
	}
	spans, err := configDeletionSpans(node, paths)
	if err != nil {
		return EditPreview{}, err
	}
	edited := []byte(applySpans(string(raw), spans))
	if _, err := ParseJSONC(edited); err != nil {
		return EditPreview{}, err
	}
	return previewEdit(path, raw, edited, "disable conflicting integrations"), nil
}

func configDeletionSpans(node jsonNode, paths [][]string) ([]spanEdit, error) {
	selections := map[string][][]string{}
	for _, path := range paths {
		if len(path) == 0 {
			return nil, errors.New("empty conflict path")
		}
		selections[path[0]] = append(selections[path[0]], path[1:])
	}
	var spans []spanEdit
	keep := make([]bool, len(node.members))
	changes := make([][]spanEdit, len(node.members))
	last := -1
	keys := map[string]bool{}
	for i, member := range node.members {
		key := member.key
		if node.open < 0 {
			key = strconv.Itoa(i)
		}
		if keys[key] {
			return nil, errors.New("duplicate JSON conflict key")
		}
		keys[key] = true
		children := selections[key]
		delete(selections, key)
		if slices.ContainsFunc(children, func(child []string) bool { return len(child) == 0 }) {
			continue
		}
		keep[i] = true
		last = i
		if len(children) > 0 {
			nested, err := configDeletionSpans(member.node, children)
			if err != nil {
				return nil, err
			}
			changes[i] = nested
		}
	}
	if len(selections) != 0 {
		return nil, errors.New("conflict entry missing")
	}
	for i, member := range node.members {
		if keep[i] {
			spans = append(spans, changes[i]...)
		} else {
			if node.open >= 0 {
				spans = append(spans, spanEdit{member.start, member.keyEnd, ""}, spanEdit{member.colon, member.colon + 1, ""})
			}
			spans = append(spans, spanEdit{member.node.start, member.node.end, ""})
		}
		if member.comma >= 0 && (!keep[i] || i == last && i != len(node.members)-1) {
			spans = append(spans, spanEdit{member.comma, member.comma + 1, ""})
		}
	}
	return spans, nil
}
