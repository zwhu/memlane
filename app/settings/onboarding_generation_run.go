package settings

import (
	"context"
	"net/http"
	"time"

	"github.com/zwhu/memlane/internal/bootstrap"
)

type onboardingGenerationPhase uint8

const (
	onboardingGenerationIdle onboardingGenerationPhase = iota
	onboardingSaving
	onboardingValidating
	onboardingSelecting
	onboardingActivating
)

func (p onboardingGenerationPhase) label() string {
	return [...]string{"", "保存配置", "发送测试请求", "设为待启用", "启用记忆提炼"}[p]
}

type onboardingGenerationState struct {
	phase                              onboardingGenerationPhase
	validation                         GenerationValidation
	validationStarted                  time.Time
	validationElapsed                  time.Duration
	failure                            string
	stopValidation                     context.CancelFunc
	activationRef                      *GenerationRef
	activationDirectory                GenerationDirectory
	advanced, inputFocused, focusModel bool
	stopping                           bool
	shownPhase                         onboardingGenerationPhase
	shownFailure                       string
	shownSlow                          bool
}

type onboardingGenerationRun struct {
	client               ProjectControlClient
	directory            GenerationDirectory
	operations           string
	phase                onboardingGenerationPhase
	ref                  *GenerationRef
	validation           GenerationValidation
	validationStarted    time.Time
	loaded               Config
	activationClearErr   error
	activationRefreshErr error
}

func (a *Controller) runOnboardingGeneration() {
	_, blocked, _ := a.onboardingProcessAction()
	client, accessible := a.accessClient()
	if blocked || !accessible || !a.initialSetup || a.onboardingPage() != onboardingProcess {
		return
	}
	s, g := &a.integrations, &a.onboardingGeneration
	config, prepared := a.config, a.environment.RuntimePreparation
	activate := a.activateFirstRuntime
	if activate == nil {
		activate = bootstrap.ActivateFirstRuntime
	}
	checkComplete := a.firstInitComplete
	if checkComplete == nil {
		checkComplete = bootstrap.FirstInitializationComplete
	}
	update := a.dispatch
	run := onboardingGenerationRun{client: client, directory: s.Generation, operations: a.generationOperationsDir()}
	profile, id, key := s.draftProfile(), s.ProfileID, s.APIKey
	if profile.Provider == "ollama" {
		key = ""
	}
	if g.phase == onboardingActivating {
		run.phase, run.ref, run.directory = onboardingActivating, g.activationRef, g.activationDirectory
	} else if g.phase > onboardingSaving && s.cleanSaved() {
		ref := *s.Saved
		run.ref = &ref
	}
	s.Busy = true
	s.Request++
	s.Loading = false
	g.phase, g.failure, g.validation, g.stopping = onboardingSaving, "", GenerationValidation{}, false
	if run.phase == onboardingActivating {
		g.phase = onboardingActivating
	} else {
		g.validationElapsed = 0
	}
	validationCtx, cancel := context.WithCancel(context.Background())
	g.stopValidation = cancel
	go func() {
		defer cancel()
		firstProgress := true
		err := run.execute(validationCtx, id, profile, key, func(validation GenerationValidation, started time.Time) {
			if firstProgress {
				firstProgress = false
				directory := run.directory
				row, ok := directory.row(run.ref)
				update(func() {
					s.Generation = directory
					if ok {
						s.editProfile(id, row)
					}
				})
			}
			update(func() {
				g.phase, g.validation, g.validationStarted = onboardingValidating, validation, started
			})
		}, func() bool {
			// Hand off ownership from validation to enabling on the UI thread.
			// A Stop action already dispatched by that thread must win.
			advance := make(chan bool, 1)
			update(func() {
				allowed := validationCtx.Err() == nil
				if allowed {
					g.phase = onboardingSelecting
					g.stopValidation = nil
					g.validationElapsed = time.Since(g.validationStarted)
				}
				advance <- allowed
			})
			select {
			case allowed := <-advance:
				return allowed
			case <-validationCtx.Done():
				return false
			}
		})
		if err == nil {
			run.phase = onboardingActivating
			update(func() { g.phase = onboardingActivating })
			err = run.activate(prepared, activate, checkComplete)
			if firstActivationRejected(err) {
				run.phase = onboardingSelecting
			}
		} else if run.phase != onboardingActivating {
			// A rejected mutation or a terminal failed validation can have
			// advanced the shared directory revision.
			_ = run.refresh()
		}
		pending, pendingErr := loadGenerationOperationsFromDir(run.operations)
		update(func() {
			s.Busy, g.stopping, g.stopValidation = false, false, nil
			if pendingErr == nil {
				s.PendingOperations = pending
			}
			s.Generation = run.directory
			if run.activationRefreshErr != nil {
				s.GenerationError = "启用未写入，暂时无法读取最新提炼配置，请重新检测。"
			}
			if row, found := run.directory.row(run.ref); found && id == run.ref.ID {
				s.editProfile(id, row)
			}
			g.phase, g.validation = run.phase, run.validation
			if !run.validationStarted.IsZero() {
				g.validationElapsed = generationValidationElapsed(time.Time{}, time.Time{}, run.validation)
				if g.validationElapsed == 0 {
					g.validationElapsed = time.Since(run.validationStarted)
				}
			}
			if err != nil {
				g.failure = onboardingGenerationFailure(run.phase, run.validation, err)
				if run.phase == onboardingActivating {
					g.activationRef, g.activationDirectory = run.ref, run.directory
				} else if firstActivationRejected(err) {
					g.activationRef, g.activationDirectory = nil, GenerationDirectory{}
					g.failure = "启用在写入前被拒绝，配置已保留。可以修改配置、重新验证或稍后配置。"
				}
				return
			}
			g.activationRef, g.activationDirectory = nil, GenerationDirectory{}
			loaded := run.loaded
			if loaded.ConfigFile == "" {
				loaded.ConfigFile = config.ConfigFile
			}
			a.acceptFirstRuntimeActivation(loaded, prepared, run.activationClearErr, false)
			a.showOnboardingDone(generationLabel(s.Generation, run.ref))
		})
	}()
}

func (a *Controller) stopOnboardingValidation() {
	g := &a.onboardingGeneration
	if g.phase == onboardingValidating && !g.stopping && g.stopValidation != nil {
		g.stopping = true
		g.stopValidation()
	}
}

func (r *onboardingGenerationRun) refresh() error {
	directory, err := readOnboardingGenerationDirectory(r.client)
	if err == nil {
		r.directory = directory
	}
	return err
}

func readOnboardingGenerationDirectory(client ProjectControlClient) (GenerationDirectory, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var directory GenerationDirectory
	err := client.request(ctx, http.MethodGet, "/v1/generation", nil, &directory)
	return directory, err
}

func (r *onboardingGenerationRun) execute(validationCtx context.Context, id string, profile GenerationProfile, key string,
	progress func(GenerationValidation, time.Time), beginEnabling func() bool) error {
	// Only activation-uncertain recovery reuses a previously approved Next.
	// Revalidating it would replace the revision in the original activation receipt.
	if r.phase == onboardingActivating {
		return nil
	}
	r.phase = onboardingSaving
	if r.ref == nil {
		if err := r.save(id, profile, key); err != nil {
			return err
		}
	}
	r.phase = onboardingValidating
	if err := r.validate(validationCtx, progress, beginEnabling); err != nil {
		return err
	}
	r.phase = onboardingSelecting
	return r.selectNext()
}
