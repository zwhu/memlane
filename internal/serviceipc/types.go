// Package serviceipc contains lightweight local transport and service settings.
package serviceipc

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/zwhu/memlane/internal/engine"
	"github.com/zwhu/memlane/internal/records"
	"os"
	"path/filepath"
	"time"
)

type Config struct {
	PiCapturePython string        `json:"pi_capture_python,omitempty"`
	PiCapturePolicy string        `json:"pi_capture_policy,omitempty"`
	CodeExtractor   string        `json:"code_extractor,omitempty"`
	ClientRoot      string        `json:"client_root,omitempty"`
	Root            string        `json:"root"`
	Worker          engine.Config `json:"worker"`
	RetryDelayMS    int           `json:"retry_delay_ms,omitempty"`
}

type Request struct {
	OfficialCodeJob                string                                  `json:"official_code_job,omitempty"`
	OfficialVisualization          *records.OfficialVisualizationQuery     `json:"official_visualization,omitempty"`
	CodingMemoryTrigger            *records.CodingMemoryTrigger            `json:"coding_memory_trigger,omitempty"`
	CodingMemoryCapability         string                                  `json:"coding_memory_capability,omitempty"`
	CodingMemoryGrantChange        *records.CodingMemoryGrantChange        `json:"coding_memory_grant_change,omitempty"`
	CodingMemoryGrantBinding       *records.CogneeBinding                  `json:"coding_memory_grant_binding,omitempty"`
	PiCaptureSettingsQuery         *records.PiCaptureSettingsQuery         `json:"pi_capture_settings_query,omitempty"`
	PiCaptureBatch                 *records.PiCaptureBatchRequest          `json:"pi_capture_batch,omitempty"`
	PiCaptureConsentReview         *records.PiCaptureConsentReview         `json:"pi_capture_consent_review,omitempty"`
	PiCaptureConsentDisable        *records.PiCaptureConsentDisable        `json:"pi_capture_consent_disable,omitempty"`
	CogneeReadReauthorization      *records.CogneeReadReauthorization      `json:"cognee_read_reauthorization,omitempty"`
	SessionQuery                   *records.SessionQuery                   `json:"session_query,omitempty"`
	DocumentQuery                  *records.OfficialDocumentQuery          `json:"document_query,omitempty"`
	CaptureRouteConfirmation       *records.CaptureRouteConfirmation       `json:"capture_route_confirmation,omitempty"`
	CaptureDeliveryQuery           *records.CaptureDeliveryQuery           `json:"capture_delivery_query,omitempty"`
	DeliveryAssignmentPreviewQuery *records.DeliveryAssignmentPreviewQuery `json:"delivery_assignment_preview_query,omitempty"`
	DeliveryAssignmentIntent       *records.DeliveryAssignmentIntent       `json:"delivery_assignment_intent,omitempty"`

	ProjectPreferenceDecisionsQuery *records.ProjectPreferenceDecisionsQuery `json:"project_preference_decisions_query,omitempty"`
	ProjectPreferenceQuery          *records.ProjectPreferenceQuery          `json:"project_preference_query,omitempty"`
	ProjectPreferenceConfirmation   *records.ProjectPreferenceConfirmation   `json:"project_preference_confirmation,omitempty"`

	UsageEventsQuery              *records.UsageEventsQuery            `json:"usage_events_query,omitempty"`
	ProcessingCredentialWrite     *records.ProcessingCredentialWrite   `json:"processing_credential_write,omitempty"`
	ProcessingModelQuery          *records.ProcessingModelQuery        `json:"processing_model_query,omitempty"`
	HomeActivity                  records.HomeActivityQuery            `json:"home_activity,omitempty"`
	HomeReview                    records.HomeReviewQuery              `json:"home_review,omitempty"`
	AgentConnectionBatchOperation string                               `json:"agent_connection_batch_operation,omitempty"`
	AgentConnectionBatchID        string                               `json:"agent_connection_batch_id,omitempty"`
	AgentConnectionBatchStart     *records.AgentConnectionBatchStart   `json:"agent_connection_batch_start,omitempty"`
	AgentConnectionBatchAction    *records.AgentConnectionBatchAction  `json:"agent_connection_batch_action,omitempty"`
	ExperiencePreview             *records.ExperiencePreviewWrite      `json:"experience_preview,omitempty"`
	ProjectTaskWatch              *records.ProjectTaskWatch            `json:"project_task_watch,omitempty"`
	ProjectTaskStart              *records.ProjectTaskStart            `json:"project_task_start,omitempty"`
	ProjectHistory                *records.ProjectHistoryQuery         `json:"project_history,omitempty"`
	ProjectBindingChange          *records.ProjectBindingChange        `json:"project_binding_change,omitempty"`
	ProjectTaskID                 string                               `json:"project_task,omitempty"`
	ProjectTaskList               *records.MergeListQuery              `json:"project_task_list,omitempty"`
	CogneeAuthorization           *records.CogneeAuthorization         `json:"cognee_authorization,omitempty"`
	CogneeSync                    *records.CogneeSync                  `json:"cognee_sync,omitempty"`
	CogneeBinding                 *records.CogneeBinding               `json:"cognee_binding,omitempty"`
	CogneeGraphQuery              *records.CogneeGraphQuery            `json:"cognee_graph_query,omitempty"`
	CogneeSubmission              *records.CogneeSubmissionRequest     `json:"cognee_submission,omitempty"`
	CogneeSubmissionID            string                               `json:"cognee_submission_id,omitempty"`
	SessionProcessing             *records.SessionProcessingRequest    `json:"session_processing,omitempty"`
	SessionProcessingID           string                               `json:"session_processing_id,omitempty"`
	CogneeProjectAuthorization    *records.CogneeProjectAuthorization  `json:"cognee_project_authorization,omitempty"`
	SourceRead                    *records.SourceRead                  `json:"source_read,omitempty"`
	CogneeImport                  *records.CogneeImport                `json:"cognee_import,omitempty"`
	BackupID                      string                               `json:"backup_id,omitempty"`
	IndexRebuildStart             *records.IndexRebuildStart           `json:"index_rebuild_start,omitempty"`
	IndexRebuildID                string                               `json:"index_rebuild_id,omitempty"`
	CodeAction                    *records.CodeAction                  `json:"code_action,omitempty"`
	CodeQuery                     *records.CodeQuery                   `json:"code_query,omitempty"`
	CodeID                        string                               `json:"code_id,omitempty"`
	IDEVerification               *records.IDEVerification             `json:"ide_verification,omitempty"`
	InitializationStart           *records.InitializationStart         `json:"initialization_start,omitempty"`
	InitializationBatch           string                               `json:"initialization_batch,omitempty"`
	Important                     *records.ImportantRequest            `json:"important,omitempty"`
	UsageObservation              *records.UsageObservation            `json:"usage_observation,omitempty"`
	ScheduleDraft                 *records.ScheduleDraft               `json:"schedule_draft,omitempty"`
	ScheduleID                    string                               `json:"schedule,omitempty"`
	ExperienceCopy                *records.ExperienceCopy              `json:"experience_copy,omitempty"`
	OrganizationReview            *records.OrganizationReview          `json:"organization_review,omitempty"`
	OrganizationStart             *records.OrganizationStart           `json:"organization_start,omitempty"`
	OrganizationResume            *records.OrganizationResume          `json:"organization_resume,omitempty"`
	OrganizationList              *records.OrganizationListQuery       `json:"organization_list,omitempty"`
	OrganizationPreview           *records.OrganizationPreviewWrite    `json:"organization_preview,omitempty"`
	OrganizationTask              string                               `json:"organization_task,omitempty"`
	MergeList                     *records.MergeListQuery              `json:"merge_list,omitempty"`
	MergeStart                    records.MergeStart                   `json:"merge_start,omitempty"`
	MergeReview                   records.MergeReview                  `json:"merge_review,omitempty"`
	MergeTask                     string                               `json:"merge_task,omitempty"`
	Protocol                      int                                  `json:"protocol"`
	ID                            string                               `json:"id"`
	Op                            string                               `json:"op"`
	Mutation                      records.Mutation                     `json:"mutation,omitempty"`
	Datasets                      []string                             `json:"datasets,omitempty"`
	Query                         string                               `json:"query,omitempty"`
	Limit                         int                                  `json:"limit,omitempty"`
	Memory                        string                               `json:"memory,omitempty"`
	UserConfirmed                 bool                                 `json:"user_confirmed,omitempty"`
	ListQuery                     records.ListQuery                    `json:"list_query,omitempty"`
	ProcessingSave                *records.ProcessingSave              `json:"processing_save,omitempty"`
	ProcessingValidation          *records.ProcessingValidationRequest `json:"processing_validation,omitempty"`
	ProcessingValidationID        string                               `json:"processing_validation_id,omitempty"`
	ProcessingActivation          *records.ProcessingActivation        `json:"processing_activation,omitempty"`
	Project                       records.ScopeDraft                   `json:"project,omitempty"`
	Scope                         string                               `json:"scope,omitempty"`
	AllScopes                     bool                                 `json:"all_scopes,omitempty"`
	BeforeRevision                int64                                `json:"before_revision,omitempty"`
	ExpectedRevision              int64                                `json:"expected_revision,omitempty"`
	Timezone                      string                               `json:"timezone,omitempty"`
	Connection                    records.ConnectionDraft              `json:"connection,omitempty"`
	ConnectionID                  string                               `json:"connection_id,omitempty"`
	ConnectionGeneration          int64                                `json:"connection_generation,omitempty"`
	AgentRequest                  records.AgentRequest                 `json:"agent_request,omitempty"`
}

type Response struct {
	Stage    string `json:"stage,omitempty"`
	Protocol int    `json:"protocol"`
	ID       string `json:"id"`
	OK       bool   `json:"ok"`
	Result   any    `json:"result,omitempty"`
	Error    string `json:"error,omitempty"`
	Code     string `json:"code,omitempty"`
}

type State = records.ServiceState

func SocketPath(root string) string {
	h := sha256.Sum256([]byte(root))
	return filepath.Join("/tmp", fmt.Sprintf("memlane-%d-%s", os.Getuid(), hex.EncodeToString(h[:6])), "service.sock")
}

func WriteJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".state-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// Resume is an explicit user action; it clears only the active stop marker.
func Resume(root string) error {
	if !filepath.IsAbs(root) {
		return errors.New("absolute root required")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	path := filepath.Join(root, "service-state.json")
	state := State{}
	if data, err := os.ReadFile(path); err == nil {
		if err = json.Unmarshal(data, &state); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if state.Stopped {
		if state.StopGeneration == 0 {
			state.StopGeneration = 1
		}
		if state.LastStoppedMS == 0 {
			state.LastStoppedMS = time.Now().UnixMilli()
		}
	}
	state.Stopped = false
	state.Restarts = nil
	return WriteJSON(path, state)
}

func MarkStopped(root string) error {
	if !filepath.IsAbs(root) {
		return errors.New("absolute root required")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	path := filepath.Join(root, "service-state.json")
	state := State{}
	if data, err := os.ReadFile(path); err == nil {
		if err = json.Unmarshal(data, &state); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if !state.Stopped {
		state.StopGeneration++
	}
	if state.StopGeneration == 0 {
		state.StopGeneration = 1
	}
	state.Stopped = true
	state.LastStoppedMS = time.Now().UnixMilli()
	return WriteJSON(path, state)
}
