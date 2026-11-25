package profiles

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
)

type documentView struct {
	DatasetID string `json:"dataset_id"`
	DataID    string `json:"data_id"`
}

func document(ref domain.DocumentRef) *documentView {
	if ref.DataID == "" {
		return nil
	}
	return &documentView{ref.DatasetID, ref.DataID}
}

type profileView struct {
	Source      string          `json:"source"`
	InstanceID  string          `json:"instance_id"`
	Name        string          `json:"name"`
	Kind        string          `json:"kind"`
	Status      string          `json:"status"`
	CanGenerate bool            `json:"can_generate"`
	OperationID string          `json:"operation_id,omitempty"`
	Method      string          `json:"method,omitempty"`
	Document    *documentView   `json:"document,omitempty"`
	Previous    *documentView   `json:"previous,omitempty"`
	ContentHash string          `json:"content_hash,omitempty"`
	StartedAt   float64         `json:"started_at,omitempty"`
	UpdatedAt   float64         `json:"updated_at,omitempty"`
	Progress    json.RawMessage `json:"progress"`
	Extra       json.RawMessage `json:"extra"`
	ErrorCode   string          `json:"error_code,omitempty"`
	Body        string          `json:"body,omitempty"`
}

var sourceNames = map[string]string{
	"codex": "Codex", "claude_code": "Claude Code", "traex": "TraeX", "antigravity": "Gemini App",
	"pi": "pi", "cursor": "Cursor", "trae_ide": "TRAE IDE", "opencode": "OpenCode",
	"manual:chatgpt": "ChatGPT", "manual:claude": "Claude", "manual:gemini": "Gemini", "manual:grok": "Grok",
	"manual:deepseek": "DeepSeek",
}

func validJSON(raw string) json.RawMessage {
	if json.Valid([]byte(raw)) {
		return json.RawMessage(raw)
	}
	return json.RawMessage("{}")
}

func (s *Service) view(row domain.UserProfile) profileView {
	p := profileView{Source: row.InstanceID, InstanceID: row.InstanceID, Name: sourceNames[row.InstanceID],
		Kind: row.Kind, Status: row.Status, CanGenerate: canGenerate(row.InstanceID),
		OperationID: row.OperationID, Method: row.Method, Document: document(row.Document),
		Previous: document(row.Previous), ContentHash: row.ContentHash, StartedAt: row.StartedAt, UpdatedAt: row.UpdatedAt,
		Progress: validJSON(row.ProgressJSON), Extra: validJSON(row.ExtraJSON)}
	var extra struct {
		ErrorCode string `json:"error_code"`
	}
	if json.Unmarshal(p.Extra, &extra) == nil {
		p.ErrorCode = extra.ErrorCode
	}
	return p
}

func (s *Service) list(ctx context.Context) ([]profileView, error) {
	rows, err := s.cfg.Store.ListUserProfiles(ctx)
	if err != nil {
		return nil, err
	}
	bySource := make(map[string]domain.UserProfile, len(rows))
	for _, row := range rows {
		bySource[row.InstanceID] = row
	}
	list := make([]profileView, 0, len(sources))
	for _, source := range sources {
		row, ok := bySource[source]
		if !ok {
			row = domain.UserProfile{InstanceID: source, Kind: source, Status: "manual"}
			if strings.HasPrefix(source, "manual:") {
				row.Kind = "manual"
			}
		}
		list = append(list, s.view(row))
	}
	return list, nil
}

type profileInput struct {
	OperationID string  `json:"operation_id"`
	Body        *string `json:"body"`
	Content     *string `json:"content"`
}

func readInput(w http.ResponseWriter, r *http.Request, optional bool) (profileInput, error) {
	var in profileInput
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 6*MaxDocumentBytes+4096))
	if err != nil {
		return in, domain.ErrInvalidArgument
	}
	input := strings.TrimSpace(string(body))
	if optional && input == "" {
		return in, nil
	}
	if !utf8.Valid(body) || input == "" || input[0] != '{' || json.Unmarshal(body, &in) != nil {
		return in, domain.ErrInvalidArgument
	}
	return in, nil
}

func (s *Service) Handler() http.Handler { return http.HandlerFunc(s.serveHTTP) }

func (s *Service) serveHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if path == "/api/profiles" || strings.HasPrefix(path, "/api/profiles/") {
		path = strings.TrimPrefix(path, "/api/profiles")
	}
	path = strings.TrimPrefix(path, "/")
	if path == "" {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, "GET")
			return
		}
		list, err := s.list(r.Context())
		writeResult(w, http.StatusOK, map[string]any{"profiles": list, "prompt": s.cfg.Prompt}, err)
		return
	}
	source, action, _ := strings.Cut(path, "/")
	if !knownSource(sourceID(source, "agent")) {
		writeResult(w, 0, nil, domain.ErrNotFound)
		return
	}
	switch action {
	case "":
		s.serveProfile(w, r, source)
	case "import", "manual", "cancel":
		if r.Method != http.MethodPost {
			methodNotAllowed(w, "POST")
			return
		}
		s.serveAction(w, r, source, action)
	default:
		writeResult(w, 0, nil, domain.ErrNotFound)
	}
}

func (s *Service) serveProfile(w http.ResponseWriter, r *http.Request, source string) {
	ctx := r.Context()
	switch r.Method {
	case http.MethodGet:
		row, body, err := s.Get(ctx, source)
		view := s.view(row)
		view.Body = body
		writeResult(w, http.StatusOK, view, err)
	case http.MethodDelete:
		writeResult(w, http.StatusOK, map[string]bool{"deleted": true}, s.Delete(ctx, source))
	default:
		methodNotAllowed(w, "GET, DELETE")
	}
}

func (s *Service) serveAction(w http.ResponseWriter, r *http.Request, source, action string) {
	ctx := r.Context()
	in, err := readInput(w, r, action != "manual")
	if err != nil {
		writeResult(w, 0, nil, err)
		return
	}
	switch action {
	case "import":
		id, replay, err := s.Import(ctx, source, in.OperationID)
		status := http.StatusAccepted
		if replay {
			status = http.StatusOK
		}
		writeResult(w, status, map[string]any{"operation_id": id, "replayed": replay}, err)
	case "cancel":
		writeResult(w, http.StatusOK, map[string]bool{"cancel_requested": true}, s.Cancel(ctx, source, in.OperationID))
	case "manual":
		body := in.Body
		if body == nil {
			body = in.Content
		}
		if body == nil {
			writeResult(w, 0, nil, ErrInvalidDocument)
			return
		}
		if err := s.Manual(ctx, source, in.OperationID, *body); err != nil {
			writeResult(w, 0, nil, err)
			return
		}
		row, bodyText, err := s.Get(context.WithoutCancel(ctx), sourceID(source, "manual"))
		view := s.view(row)
		view.Body = bodyText
		writeResult(w, http.StatusOK, view, err)
	}
}

func methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	writeResult(w, http.StatusMethodNotAllowed, map[string]string{"error_code": "method_not_allowed"}, nil)
}

func writeResult(w http.ResponseWriter, status int, data any, err error) {
	if err != nil {
		switch {
		case errors.Is(err, ErrSaveUnconfirmed):
			status = http.StatusServiceUnavailable
		case errors.Is(err, domain.ErrNotFound), errors.Is(err, engine.ErrNotFound):
			status = http.StatusNotFound
		case errors.Is(err, domain.ErrConflict), errors.Is(err, ErrImportUnavailable), errors.Is(err, ErrCancelUnavailable), errors.Is(err, context.Canceled):
			status = http.StatusConflict
		case errors.Is(err, domain.ErrInvalidArgument), errors.Is(err, ErrInvalidDocument):
			status = http.StatusBadRequest
		default:
			status = http.StatusServiceUnavailable
		}
		data = map[string]string{"error_code": errorCode(err)}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
