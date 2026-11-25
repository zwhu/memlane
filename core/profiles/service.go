package profiles

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
)

const (
	ProtectedDatasetName = "ml-user-profiles"
	MaxDocumentBytes     = 256 * 1024
)

var (
	ErrSaveUnconfirmed   = errors.New("profile_save_unconfirmed")
	ErrCancelUnavailable = errors.New("profile_cancel_unavailable")
	ErrImportUnavailable = errors.New("profile_automatic_unavailable")
	ErrInvalidDocument   = errors.New("invalid_profile_document")
	ErrOutputPipeOpen    = errors.New("output_pipe_open")
	errInterrupted       = errors.New("profile_interrupted")
)

var sources = []string{"codex", "claude_code", "traex", "antigravity", "pi", "cursor", "trae_ide", "opencode",
	"manual:chatgpt", "manual:claude", "manual:gemini", "manual:grok", "manual:deepseek"}

func sourceID(source, method string) string {
	source = strings.ToLower(strings.TrimSpace(source))
	switch source {
	case "claude":
		if method != "manual" {
			return "claude_code"
		}
		return "manual:claude"
	case "chatgpt", "gemini", "grok", "deepseek":
		return "manual:" + source
	}
	return source
}

func knownSource(source string) bool {
	for _, id := range sources {
		if id == source {
			return true
		}
	}
	return false
}

func canGenerate(source string) bool {
	switch source {
	case "codex", "claude_code", "traex", "antigravity", "pi":
		return true
	}
	return false
}

type Config struct {
	Store           *domain.Store
	Engine          engine.MemoryEngine
	DatasetID       string
	DatasetResolver func(context.Context) (string, error)
	Prompt          string
	IdleThreshold   time.Duration
	Runner          Runner
}

type Service struct {
	cfg    Config
	mu     sync.Mutex
	active map[string]*operation
	slot   chan struct{}
	closed bool
}

type operation struct {
	row             domain.UserProfile
	progress        Progress
	cancel          context.CancelFunc
	cancelRequested bool
	done            chan struct{}
}

func NewService(cfg Config) (*Service, error) {
	if cfg.Store == nil || cfg.Engine == nil {
		return nil, domain.ErrInvalidArgument
	}
	if cfg.Prompt == "" {
		cfg.Prompt = strings.TrimSpace(DefaultPrompt)
	}
	if cfg.DatasetID == "" {
		cfg.DatasetID = ProtectedDatasetName
	}
	if cfg.IdleThreshold <= 0 {
		cfg.IdleThreshold = 5 * time.Second
	}
	if cfg.Runner == nil {
		cfg.Runner = CLIRunner{}
	}
	s := &Service{cfg: cfg, active: make(map[string]*operation), slot: make(chan struct{}, 1)}
	ctx := context.Background()
	rows, err := cfg.Store.ListUserProfiles(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if !inFlight(row.Status) {
			continue
		}
		row.Status = "unknown"
		if err := s.persist(ctx, row, errInterrupted); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func inFlight(status string) bool {
	switch status {
	case "queued", "waiting", "activity", "exited", "saving":
		return true
	}
	return false
}

func timestamp() float64 { return float64(time.Now().UnixNano()) / float64(time.Second) }

func (s *Service) persist(ctx context.Context, row domain.UserProfile, failure error) error {
	row.UpdatedAt = timestamp()
	a := domain.UserProfileAttempt{
		OperationID: row.OperationID, InstanceID: row.InstanceID, Kind: row.Kind, Method: row.Method,
		Status: row.Status, DatasetID: row.DatasetID, Document: row.Document, Previous: row.Previous,
		ContentHash: row.ContentHash, StartedAt: row.StartedAt, UpdatedAt: row.UpdatedAt,
		ProgressJSON: row.ProgressJSON, ExtraJSON: row.ExtraJSON,
	}
	var extra map[string]any
	if json.Unmarshal([]byte(row.ExtraJSON), &extra) != nil || extra == nil {
		extra = make(map[string]any)
	}
	delete(extra, "error_code")
	delete(extra, "failure_reason")
	delete(extra, "failure_detail")
	delete(extra, "exit_code")
	var progress Progress
	if json.Unmarshal([]byte(row.ProgressJSON), &progress) == nil {
		a.ExitCode = progress.ExitCode
	}
	if failure != nil {
		a.ErrorCode = errorCode(failure)
		extra["error_code"] = a.ErrorCode
		var agentErr *AgentFailure
		if errors.As(failure, &agentErr) {
			if agentErr.ExitCode != nil {
				a.ExitCode = agentErr.ExitCode
			}
			a.FailureReason = agentErr.Reason
			extra["exit_code"], extra["failure_reason"] = a.ExitCode, a.FailureReason
			if detail := SanitizeFailureDetail(agentErr.Detail); detail != "" {
				extra["failure_detail"] = detail
			}
		}
	}
	raw, _ := json.Marshal(extra)
	row.ExtraJSON, a.ExtraJSON = string(raw), string(raw)
	if row.OperationID != "" {
		if err := s.cfg.Store.UpsertUserProfileAttempt(ctx, a); err != nil {
			return err
		}
	}
	return s.cfg.Store.UpsertUserProfile(ctx, row)
}

var operationPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)

func (s *Service) begin(ctx context.Context, source, opID, method string) (*operation, bool, error) {
	if !knownSource(source) {
		return nil, false, domain.ErrNotFound
	}
	if opID == "" {
		opID = "profile:" + randomID()
	}
	if !operationPattern.MatchString(opID) {
		return nil, false, domain.ErrInvalidArgument
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, false, engine.ErrUnavailable
	}
	attempt, err := s.cfg.Store.GetUserProfileAttempt(ctx, opID)
	switch {
	case err == nil:
		if attempt.InstanceID != source || attempt.Method != method {
			return nil, false, domain.ErrConflict
		}
		return nil, true, replayError(method, attempt.Status, attempt.ErrorCode)
	case !errors.Is(err, domain.ErrNotFound):
		return nil, false, err
	}
	if s.active[source] != nil {
		return nil, false, domain.ErrConflict
	}
	if method == "agent" && !canGenerate(source) {
		return nil, false, ErrImportUnavailable
	}
	old, err := s.cfg.Store.GetUserProfile(ctx, source)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return nil, false, err
	}
	if old.OperationID == opID {
		if old.Method != method {
			return nil, false, domain.ErrConflict
		}
		return nil, true, replayError(method, old.Status, "")
	}
	kind := source
	if strings.HasPrefix(source, "manual:") {
		kind = "manual"
	}
	previous := old.Document
	if previous.DataID == "" {
		previous = old.Previous
	}
	op := &operation{done: make(chan struct{}), row: domain.UserProfile{
		InstanceID: source, Kind: kind, OperationID: opID, Method: method, Status: "queued",
		StartedAt: timestamp(), Document: old.Document, Previous: previous, ContentHash: old.ContentHash,
	}}
	if err := s.persist(ctx, op.row, nil); err != nil {
		return nil, false, err
	}
	s.active[source] = op
	return op, false, nil
}

func replayError(method, status, code string) error {
	if method != "manual" || status == "saved" {
		return nil
	}
	if code == ErrSaveUnconfirmed.Error() || status == "failed" || status == "unknown" {
		return ErrSaveUnconfirmed
	}
	return domain.ErrConflict
}

// Import starts an explicitly requested export and returns its operation ID.
func (s *Service) Import(ctx context.Context, source, opID string) (string, bool, error) {
	source = sourceID(source, "agent")
	op, replay, err := s.begin(ctx, source, opID, "agent")
	if err != nil || replay {
		return opID, replay, err
	}
	id := op.row.OperationID
	go func() {
		s.run(context.WithoutCancel(ctx), op, "")
	}()
	return id, false, nil
}

// Manual saves a pasted document, preserving its exact bytes.
func (s *Service) Manual(ctx context.Context, source, opID, body string) error {
	if err := validateBody(body); err != nil {
		return err
	}
	op, replay, err := s.begin(ctx, sourceID(source, "manual"), opID, "manual")
	if err != nil || replay {
		return err
	}
	return s.run(ctx, op, body)
}

func (s *Service) requestCancel(op *operation) {
	op.cancelRequested = true
	if op.cancel != nil {
		op.cancel()
	}
}

func (s *Service) Cancel(_ context.Context, source, opID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	op := s.active[sourceID(source, "agent")]
	if op == nil || !inFlight(op.row.Status) || op.row.Status == "saving" || (opID != "" && opID != op.row.OperationID) {
		return ErrCancelUnavailable
	}
	s.requestCancel(op)
	return nil
}

func (s *Service) Close(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	var pending []chan struct{}
	for _, op := range s.active {
		if inFlight(op.row.Status) && op.row.Status != "saving" {
			s.requestCancel(op)
		}
		pending = append(pending, op.done)
	}
	s.mu.Unlock()
	for _, done := range pending {
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
