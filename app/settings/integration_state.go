package settings

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/zwhu/memlane/internal/controlplane"
)

type IntegrationState struct {
	HostVersions                                                                                                map[string]string
	HandoffOpen, HandoffLoading                                                                                 bool
	HandoffRef                                                                                                  *GenerationRef
	HandoffOperation                                                                                            string
	HandoffReady                                                                                                bool
	HandoffRequest                                                                                              uint64
	Handoff                                                                                                     GenerationHandoffDirectory
	PendingOperations                                                                                           []generationUIOperation
	Connections                                                                                                 ConnectionsDirectory
	Generation                                                                                                  GenerationDirectory
	ShowUnavailable                                                                                             bool
	ConnectionBulk                                                                                              bool
	EnvironmentOpen                                                                                             map[string]bool
	CaptureHelpCollapsed                                                                                        map[string]bool
	HookCommandCopiedAt                                                                                         map[string]time.Time
	PendingReconcile                                                                                            map[string]controlplane.ConnectionOperationReceipt
	PendingReconcileHasRevision                                                                                 map[string]bool
	TraeRepairPreview                                                                                           map[string]controlplane.TraeHooksRepairResult
	TraeRepairHasRevision                                                                                       map[string]bool
	ConnectionPreviews                                                                                          map[string]controlplane.ConnectionPreview
	PinnedAgentKinds                                                                                            []string
	CaptureReview                                                                                               map[string]string
	ReceiptCheckLoading                                                                                         bool
	NextReceiptCheck                                                                                            time.Time
	Loaded, Loading, Busy                                                                                       bool
	Request                                                                                                     uint64
	GenerationLoaded, ConnectionsLoaded                                                                         bool
	GenerationError, ConnectionsError                                                                           string
	Error, Message, MessageScope                                                                                string
	Selected                                                                                                    map[string]bool
	ItemMessages                                                                                                map[string]string
	BatchStop                                                                                                   *atomic.Bool
	Editor                                                                                                      bool
	DeleteProfile                                                                                               *GenerationRef
	DeleteOpen                                                                                                  bool
	DeleteError                                                                                                 string
	ProfileID, ProviderLabel, DraftMode, BridgeKind, BridgeProvider, Model, Endpoint, APIKey, CredentialVersion string
	GenerationDrafts                                                                                            map[string]generationEditorDraft
	GenerationModeSelection                                                                                     map[string]string
	GenerationDraftMode                                                                                         string
	GenerationAgentApply                                                                                        bool
	GenerationProviderDialog                                                                                    bool
	Temperature, MaxOutputTokens                                                                                string
	CredentialConfigured                                                                                        bool
	Saved                                                                                                       *GenerationRef
	SavedSignature                                                                                              string
	SaveOperation, ValidationOperation                                                                          string
	NextOperation                                                                                               string
	ValidationStartedAt                                                                                         time.Time
	Validation                                                                                                  GenerationValidation
	ValidationCancel                                                                                            context.CancelFunc
	KeyEpoch                                                                                                    uint64
}
