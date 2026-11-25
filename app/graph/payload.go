package graph

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/zwhu/memlane/app/api"
)

// Payload holds a validated visualizer frame and its node index.
type Payload struct {
	raw   json.RawMessage
	nodes map[string]json.RawMessage
}

func (p Payload) MarshalJSON() ([]byte, error) { return bytes.Clone(p.raw), nil }

const maxNodes, maxEdges = 1000, 50000

func NormalizeGraphResponse(resp api.GraphResponse) (Payload, error) {
	if len(resp.Nodes) > maxNodes || len(resp.Edges) > maxEdges {
		return Payload{}, fmt.Errorf("官方图数据超出数量限制")
	}
	nodes := make([]map[string]any, 0, len(resp.Nodes))
	for _, node := range resp.Nodes {
		value := copyProperties(node.Properties)
		value["id"] = node.ID
		if node.Type != "" {
			value["type"] = node.Type
		}
		if node.Label != "" {
			value["name"] = node.Label
		}
		if _, ok := value["name"]; !ok {
			value["name"] = node.ID
		}
		nodes = append(nodes, value)
	}
	links := make([]map[string]any, 0, len(resp.Edges))
	for _, edge := range resp.Edges {
		value := copyProperties(edge.Properties)
		value["source"], value["target"] = edge.Source, edge.Target
		if edge.ID != "" {
			value["id"] = edge.ID
		}
		if edge.Label != "" {
			value["label"] = edge.Label
		}
		if edge.Relationship != "" {
			value["relation"] = edge.Relationship
		} else if _, ok := value["relation"]; !ok && edge.Label != "" {
			value["relation"] = edge.Label
		}
		links = append(links, value)
	}
	raw, err := json.Marshal(map[string]any{"nodes": nodes, "links": links, "color_maps": map[string]any{"node_set": map[string]string{}}})
	if err != nil {
		return Payload{}, err
	}
	return DecodePayload(raw)
}

func copyProperties(props map[string]any) map[string]any {
	result := make(map[string]any, len(props)+3)
	for key, value := range props {
		result[key] = value
	}
	return result
}

func DecodePayload(raw json.RawMessage) (Payload, error) {
	var wire struct {
		Nodes []json.RawMessage `json:"nodes"`
		Edges []json.RawMessage `json:"edges"`
		Links []struct {
			Source string `json:"source"`
			Target string `json:"target"`
		} `json:"links"`
		Colors map[string]any `json:"color_maps"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil || wire.Nodes == nil || len(wire.Nodes) > maxNodes {
		return Payload{}, fmt.Errorf("官方图数据结构不匹配")
	}
	if wire.Edges != nil && wire.Links == nil && len(wire.Edges) <= maxEdges {
		var standard api.GraphResponse
		if err := json.Unmarshal(raw, &standard); err != nil {
			return Payload{}, fmt.Errorf("官方图数据结构不匹配: %w", err)
		}
		return NormalizeGraphResponse(standard)
	}
	if wire.Edges != nil || wire.Links == nil || wire.Colors == nil || len(wire.Links) > maxEdges {
		return Payload{}, fmt.Errorf("官方图数据结构不匹配")
	}
	nodes := make(map[string]json.RawMessage, len(wire.Nodes))
	for _, node := range wire.Nodes {
		var n struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(node, &n) != nil || strings.TrimSpace(n.ID) == "" || nodes[n.ID] != nil {
			return Payload{}, fmt.Errorf("官方图节点身份不匹配")
		}
		nodes[n.ID] = node
	}
	for _, link := range wire.Links {
		if nodes[link.Source] == nil || nodes[link.Target] == nil {
			return Payload{}, fmt.Errorf("官方图边身份不匹配")
		}
	}
	return Payload{bytes.Clone(raw), nodes}, nil
}
