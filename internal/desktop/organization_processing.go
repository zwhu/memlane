package desktop

import (
	"context"

	"github.com/zwhu/memlane/internal/records"
)

func organizationProcessingAvailable(c records.ProcessingConfiguration) bool {
	v := c.Current.Revision
	return c.Current.Active && v != nil && c.Runtime[v.ID].State == "ready" && v.Spec.Mode == "agent" && v.Spec.Agent == "pi" && v.Validation != nil && v.Validation.State == "succeeded" && !v.Validation.CancelRequested
}

// The picker reads the existing settings ledger. Drawing a page, choosing
// materials and inspecting the current executor never validates or enables it.
func (a *App) refreshOrganizationProcessing() {
	s := &a.organization
	if s.ProcessingLoading || s.Busy || a.libraryContext == nil || a.visualFixture {
		return
	}
	s.ProcessingLoading = true
	s.ProcessingSequence++
	sequence, epoch, life, library := s.ProcessingSequence, s.Epoch, a.libraryContext, a.library
	go func() {
		ctx, stop := context.WithTimeout(life, processingRuntimeMetadataTimeout)
		defer stop()
		configuration, err := library.ProcessingConfiguration(ctx)
		if life.Err() != nil {
			return
		}
		a.dispatch(func() {
			if a.organization.Epoch != epoch || s.ProcessingSequence != sequence {
				return
			}
			s.ProcessingLoading = false
			if err != nil {
				s.ProcessingLoaded = false
				s.ProcessingError = "记忆处理方式读取未完成，保留本次材料；请重试。"
				return
			}
			s.Processing = configuration
			s.ProcessingLoaded = true
			s.ProcessingError = ""
		})
	}()
}
