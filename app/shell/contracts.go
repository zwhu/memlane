package shell

import kitui "github.com/zwhu/memlane/kit/ui"

type Dispatcher interface {
	Dispatch(fn func())
}

type DispatchFunc func(fn func())

func (f DispatchFunc) Dispatch(fn func()) {
	if f != nil && fn != nil {
		f(fn)
	}
}

type ProjectRef struct {
	ID, Name, DatasetID, CodeDatasetID string
	CodeEnabled                        bool
	Path                               string
	Hidden, Common                     bool
}

type ItemTarget struct {
	ProjectID, DatasetID, ItemID, Kind   string
	SourceDocument, Title, Date, Preview string
}

type Router interface {
	NavigateOverview()
	NavigateProject(projectID string)
	NavigateCommon()
	NavigateProfiles()
	NavigateSettings(section string)
	OpenItem(target ItemTarget) error
}

type SessionFence interface {
	Epoch() uint64
	AuthEpoch() uint64
	ReadingIdentity() string
	InvalidateReading()
}

type FenceSnapshot struct {
	Epoch, AuthEpoch uint64
	ReadingIdentity  string
}

func CaptureFence(f SessionFence) FenceSnapshot {
	if f == nil {
		return FenceSnapshot{}
	}
	return FenceSnapshot{f.Epoch(), f.AuthEpoch(), f.ReadingIdentity()}
}

func (s FenceSnapshot) Valid(f SessionFence) bool {
	return f != nil && s == CaptureFence(f)
}

type ThemeProvider interface {
	Tokens() *kitui.Tokens
	ReduceMotion() bool
}
