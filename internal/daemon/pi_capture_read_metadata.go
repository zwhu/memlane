package daemon

import "encoding/json"

// Called only after ReadSavedCapture has verified the original operation,
// official ID and content digest. Absence is unknown, never inferred from prose.
func savedCaptureTruncation(entry json.RawMessage, key string) (out struct{ Question, Answer, Params, Result, Error bool }) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(entry, &fields) != nil {
		return
	}
	var text string
	if json.Unmarshal(fields[key], &text) != nil {
		return
	}
	var marker struct {
		Origin string `json:"origin"`
		Source struct {
			Question bool `json:"question_truncated"`
			Answer   bool `json:"answer_truncated"`
			Params   bool `json:"params_truncated"`
			Result   bool `json:"result_truncated"`
			Error    bool `json:"error_truncated"`
		} `json:"source"`
	}
	if json.Unmarshal([]byte(text), &marker) != nil || marker.Origin != "memlane_pi_capture_v1" {
		return
	}
	out.Question, out.Answer, out.Params, out.Result, out.Error = marker.Source.Question, marker.Source.Answer, marker.Source.Params, marker.Source.Result, marker.Source.Error
	return
}
