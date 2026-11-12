package official

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"github.com/zwhu/memlane/internal/records"
)

type VisualizationReader struct{ SessionReader }

// Authorize checks live official read ACL without loading graph/materials.
func (r VisualizationReader) Authorize(ctx context.Context) error {
	if err := r.verifyVisualization(ctx); err != nil {
		return err
	}
	var scope struct {
		Instance string `json:"instance"`
		Dataset  string `json:"dataset"`
	}
	query := url.Values{"expected_instance": {r.Instance}}
	err := Request(ctx, r.Endpoint, "/api/v1/memlane/datasets/"+r.Dataset+"/visualization-scope?"+query.Encode(), "GET", r.Token, "", nil, &scope)
	if err != nil {
		return err
	}
	if scope.Instance != r.Instance || scope.Dataset != r.Dataset {
		return errors.New("official visualization authorization changed")
	}
	return nil
}

func (r VisualizationReader) verifyVisualization(ctx context.Context) error {
	if !canonicalSessionScope(r.Instance) || !canonicalSessionScope(r.Dataset) || r.Token == "" {
		return errors.New("authorized visualization binding required")
	}
	var identity struct {
		Instance     string   `json:"instance"`
		Version      string   `json:"cognee_version"`
		Commit       string   `json:"source_commit"`
		Capabilities []string `json:"capabilities"`
	}
	if err := Request(ctx, r.Endpoint, "/api/v1/memlane/identity", "GET", r.Token, "", nil, &identity); err != nil {
		return err
	}
	hasGraph := false
	for _, capability := range identity.Capabilities {
		hasGraph = hasGraph || capability == "official_visualization_v1"
	}
	if identity.Instance != r.Instance || identity.Version != "1.6.7" || identity.Commit != CodingMemoryServiceCommit || !hasGraph {
		return errors.New("official visualization capability changed")
	}
	return nil
}

// Read preserves all official classification/provenance fields, with no extra
// text store, graph layout, embedding query or scan. Host rechecks its binding.
func (r VisualizationReader) Read(ctx context.Context, maxNodes int, seeds []string) (records.OfficialVisualization, error) {
	var out records.OfficialVisualization
	if maxNodes < 1 || maxNodes > 1000 || len(seeds) > 16 {
		return out, errors.New("bounded visualization request required")
	}
	q := url.Values{"expected_instance": {r.Instance}, "max_nodes": {strconv.Itoa(maxNodes)}}
	seen := map[string]bool{}
	for _, seed := range seeds {
		if len(seed) == 0 || len(seed) > 256 || seen[seed] || strings.ContainsFunc(seed, func(c rune) bool { return unicode.IsSpace(c) || unicode.IsControl(c) }) {
			return out, errors.New("bounded visualization seeds required")
		}
		seen[seed] = true
		q.Add("seed_node_ids", seed)
	}
	if err := r.verifyVisualization(ctx); err != nil {
		return out, err
	}
	path := "/api/v1/memlane/datasets/" + r.Dataset + "/visualization?" + q.Encode()
	if err := Request(ctx, r.Endpoint, path, "GET", r.Token, "", nil, &out); err != nil {
		return records.OfficialVisualization{}, err
	}
	if out.Instance != r.Instance || out.Dataset != r.Dataset || out.Scope != "bounded_subgraph" || out.MaxNodes != maxNodes {
		return records.OfficialVisualization{}, errors.New("official visualization scope changed")
	}
	if err := validateVisualization(out.Payload, maxNodes); err != nil {
		return records.OfficialVisualization{}, err
	}
	if err := r.verifyVisualization(ctx); err != nil {
		return records.OfficialVisualization{}, err
	}
	return out, nil
}

func validateVisualization(raw json.RawMessage, maxNodes int) error {
	var graph struct {
		Nodes []struct {
			ID string `json:"id"`
		} `json:"nodes"`
		Links []struct {
			Source string `json:"source"`
			Target string `json:"target"`
		} `json:"links"`
		ColorMaps map[string]json.RawMessage `json:"color_maps"`
	}
	if json.Unmarshal(raw, &graph) != nil || graph.Nodes == nil || graph.Links == nil || graph.ColorMaps == nil || len(graph.Nodes) > maxNodes || len(graph.Links) > 50000 {
		return errors.New("official visualization payload invalid")
	}
	ids := map[string]bool{}
	for _, node := range graph.Nodes {
		if node.ID == "" || len(node.ID) > 256 || ids[node.ID] {
			return errors.New("official visualization node identity invalid")
		}
		ids[node.ID] = true
	}
	for _, link := range graph.Links {
		if !ids[link.Source] || !ids[link.Target] {
			return errors.New("official visualization link endpoint invalid")
		}
	}
	return nil
}

// ValidateVisualizationPayload is shared by the transport and native host;
// both consume RawMessage while preserving official fields verbatim.
func ValidateVisualizationPayload(raw json.RawMessage, maxNodes int) error {
	if maxNodes < 1 || maxNodes > 1000 {
		return errors.New("bounded visualization request required")
	}
	return validateVisualization(raw, maxNodes)
}
