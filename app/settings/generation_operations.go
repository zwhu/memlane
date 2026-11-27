package settings

import "github.com/zwhu/memlane/internal/controlplane"

type generationUIOperation controlplane.UIOperation

func (o generationUIOperation) valid() bool { return controlplane.UIOperation(o).Valid() }

func (o generationUIOperation) connection() bool { return controlplane.UIOperation(o).IsConnection() }

func (s *IntegrationState) pendingConnection(id string) *generationUIOperation {
	for _, o := range s.PendingOperations {
		if o.connection() && o.Profile == id {
			value := o
			return &value
		}
	}
	return nil
}

func (s *IntegrationState) hasPendingGeneration() bool {
	for _, o := range s.PendingOperations {
		if !o.connection() {
			return true
		}
	}
	return false
}

func (a *Controller) generationOperationsDir() string {
	var tokenFile string
	if c := a.config.ProjectControl; c != nil {
		tokenFile = c.ManagementTokenFile
	}
	return controlplane.OperationsDir(a.config.GenerationFile, tokenFile)
}

func (s *IntegrationState) pendingGenerationProfile(id string) bool {
	for _, o := range s.PendingOperations {
		if !o.connection() && o.Profile == id {
			return true
		}
	}
	return false
}

func recordGenerationOperationAt(root string, o generationUIOperation) ([]generationUIOperation, error) {
	return desktopOperations(controlplane.RecordUIOperation(root, controlplane.UIOperation(o)))
}

func desktopOperations(ops []controlplane.UIOperation, err error) ([]generationUIOperation, error) {
	if err != nil || len(ops) == 0 {
		return nil, err
	}
	pending := make([]generationUIOperation, len(ops))
	for i, op := range ops {
		pending[i] = generationUIOperation(op)
	}
	return pending, nil
}

func (a *Controller) recordGenerationOperation(o generationUIOperation) error {
	pending, err := recordGenerationOperationAt(a.generationOperationsDir(), o)
	if err != nil {
		return err
	}
	a.integrations.PendingOperations = pending
	return nil
}

func (a *Controller) loadGenerationOperations() ([]generationUIOperation, error) {
	return loadGenerationOperationsFromDir(a.generationOperationsDir())
}

func loadGenerationOperationsFromDir(root string) ([]generationUIOperation, error) {
	return desktopOperations(controlplane.LoadUIOperations(root))
}

func (a *Controller) finishGenerationOperation(operation string) bool {
	s := &a.integrations
	for i, op := range s.PendingOperations {
		if op.Operation != operation {
			continue
		}
		if err := controlplane.FinishUIOperation(a.generationOperationsDir(), operation); err != nil {
			s.Error = "原回执已确认，但操作记录的清理尚未确认，请重新核对"
			return false
		}
		s.PendingOperations = append(s.PendingOperations[:i], s.PendingOperations[i+1:]...)
		break
	}
	return true
}

func (a *Controller) reviewPendingGeneration(o generationUIOperation) {
	s := &a.integrations
	if s.Busy {
		return
	}
	switch o.Kind {
	case "handoffs":
		s.HandoffOperation = o.Operation
		a.pollGenerationHandoff(o.Operation)
	case "delete":
		a.reviewGenerationDelete(o)
	case "save":
		s.SaveOperation = o.Operation
		a.reviewGenerationSave()
	case "next":
		s.NextOperation = o.Operation
		a.reviewGenerationNext()
	case "validations":
		if row, found := s.Generation.row(&GenerationRef{ID: o.Profile, Revision: o.Revision}); found {
			s.editProfile(o.Profile, row)
			if row.Profile.Mode == "provider" && !a.initialSetup && !a.config.GenerationSetupOnly {
				s.GenerationProviderDialog = true
			}
		}
		s.ValidationOperation = o.Operation
		a.pollGenerationValidation(o.Operation)
	}
}
