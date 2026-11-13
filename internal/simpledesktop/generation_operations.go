package simpledesktop

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
)

// Only receipt identifiers belong here. Credentials and request bodies stay out
// of the recovery journal, including when the app exits during a request.
type generationUIOperation struct {
	Operation string `json:"operation_id"`
	Kind      string `json:"kind"`
	Profile   string `json:"profile_id"`
	Revision  int64  `json:"profile_revision,omitempty"`
}

var generationOperationID = regexp.MustCompile(`^ui-[A-Za-z0-9_-]{1,61}$`)

func (o generationUIOperation) valid() bool {
	return generationOperationID.MatchString(o.Operation) && o.Profile != "" &&
		(o.Kind == "save" || o.Kind == "next" || o.Kind == "validations" || o.connection())
}

func (o generationUIOperation) connection() bool {
	return o.Kind == "connection_prepare" || o.Kind == "connection_activate"
}

func (s *integrationState) pendingConnection(id string) *generationUIOperation {
	for _, o := range s.PendingOperations {
		if o.connection() && o.Profile == id {
			value := o
			return &value
		}
	}
	return nil
}

func (s *integrationState) hasPendingGeneration() bool {
	for _, o := range s.PendingOperations {
		if !o.connection() {
			return true
		}
	}
	return false
}

func (a *App) generationOperationsDir() string {
	if a.Config.GenerationFile != "" {
		return a.Config.GenerationFile + ".ui-operations"
	}
	if c := a.Config.ProjectControl; c != nil && c.ManagementTokenFile != "" {
		return filepath.Join(filepath.Dir(c.ManagementTokenFile), "generation-ui-operations")
	}
	return ""
}

func privateOperationPath(path string, directory bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 || info.IsDir() != directory || (!directory && !info.Mode().IsRegular()) {
		return errors.New("private generation operation journal required")
	}
	return nil
}

func syncOperationDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func lockGenerationOperations(root string) (*os.File, error) {
	f, err := os.OpenFile(root+".lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	if err = privateOperationPath(root+".lock", false); err == nil {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func (s *integrationState) pendingGenerationProfile(id string) bool {
	for _, o := range s.PendingOperations {
		if !o.connection() && o.Profile == id {
			return true
		}
	}
	return false
}

func (a *App) recordGenerationOperation(o generationUIOperation) error {
	root := a.generationOperationsDir()
	if root == "" || !filepath.IsAbs(root) || !o.valid() {
		return errors.New("generation operation journal unavailable")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	if err := privateOperationPath(root, true); err != nil {
		return err
	}
	lock, err := lockGenerationOperations(root)
	if err != nil {
		return err
	}
	defer lock.Close()
	pending, err := a.loadGenerationOperations()
	if err != nil {
		return err
	}
	a.integrations.PendingOperations = pending
	for _, pending := range a.integrations.PendingOperations {
		if pending.Profile == o.Profile && pending.connection() == o.connection() {
			return errors.New("review the original operation first")
		}
	}
	f, err := os.OpenFile(filepath.Join(root, o.Operation+".json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	err = json.NewEncoder(f).Encode(o)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = syncOperationDirectory(root)
	}
	if err != nil {
		return err
	}
	a.integrations.PendingOperations = append(a.integrations.PendingOperations, o)
	return nil
}

func (a *App) loadGenerationOperations() ([]generationUIOperation, error) {
	root := a.generationOperationsDir()
	if root == "" {
		return nil, nil
	}
	if !filepath.IsAbs(root) {
		return nil, errors.New("generation operation journal unavailable")
	}
	if err := privateOperationPath(root, true); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var result []generationUIOperation
	for _, e := range entries {
		path := filepath.Join(root, e.Name())
		if err := privateOperationPath(path, false); err != nil {
			return nil, err
		}
		info, err := e.Info()
		if err != nil {
			return nil, err
		}
		if info.Size() > 4096 {
			return nil, errors.New("generation operation metadata too large")
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var o generationUIOperation
		if err = json.Unmarshal(b, &o); err != nil {
			return nil, err
		}
		if !o.valid() || e.Name() != o.Operation+".json" {
			return nil, errors.New("invalid generation operation metadata")
		}
		result = append(result, o)
	}
	return result, nil
}

func (a *App) finishGenerationOperation(operation string) bool {
	found := false
	for _, o := range a.integrations.PendingOperations {
		found = found || o.Operation == operation
	}
	if !found {
		return true
	}
	lock, err := lockGenerationOperations(a.generationOperationsDir())
	if err != nil {
		a.integrations.Error = "本机操作记录暂时不可用，请重新核对"
		return false
	}
	defer lock.Close()
	if err := os.Remove(filepath.Join(a.generationOperationsDir(), operation+".json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		a.integrations.Error = "原回执已确认，但本机操作记录尚未清理，请重新核对"
		return false
	}
	if err := syncOperationDirectory(a.generationOperationsDir()); err != nil {
		a.integrations.Error = "本机操作记录的清理尚未确认，请重新核对"
		return false
	}
	var pending []generationUIOperation
	for _, o := range a.integrations.PendingOperations {
		if o.Operation != operation {
			pending = append(pending, o)
		}
	}
	a.integrations.PendingOperations = pending
	return true
}

func (a *App) reviewPendingGeneration(o generationUIOperation) {
	s := &a.integrations
	if s.Busy {
		return
	}
	switch o.Kind {
	case "save":
		s.SaveOperation = o.Operation
		a.reviewGenerationSave()
	case "next":
		s.NextOperation = o.Operation
		a.reviewGenerationNext()
	case "validations":
		if row, found := s.Generation.row(&GenerationRef{ID: o.Profile, Revision: o.Revision}); found {
			s.editProfile(o.Profile, row)
		}
		s.ValidationOperation = o.Operation
		a.pollGenerationValidation(o.Operation)
	}
}
