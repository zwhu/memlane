package shell

import "testing"

var _ Dispatcher = DispatchFunc(nil)
var _ SessionFence = (*testFence)(nil)

type testFence struct{ FenceSnapshot }

func (f *testFence) Epoch() uint64           { return f.FenceSnapshot.Epoch }
func (f *testFence) AuthEpoch() uint64       { return f.FenceSnapshot.AuthEpoch }
func (f *testFence) ReadingIdentity() string { return f.FenceSnapshot.ReadingIdentity }
func (f *testFence) InvalidateReading()      { f.FenceSnapshot.AuthEpoch++ }

func TestDispatcher(t *testing.T) {
	var f DispatchFunc
	f.Dispatch(func() { t.Fatal("nil dispatcher called callback") })
	called := false
	f = func(fn func()) { fn() }
	f.Dispatch(nil)
	f.Dispatch(func() { called = true })
	if !called {
		t.Fatal("callback was not dispatched")
	}
}

func TestFence(t *testing.T) {
	f := &testFence{FenceSnapshot{1, 2, "reading"}}
	s := CaptureFence(f)
	if !s.Valid(f) || s.Valid(nil) || CaptureFence(nil) != (FenceSnapshot{}) {
		t.Fatal("initial fence mismatch")
	}
	for _, next := range []FenceSnapshot{{2, 2, "reading"}, {1, 3, "reading"}, {1, 2, "other"}} {
		f.FenceSnapshot = next
		if s.Valid(f) {
			t.Fatalf("accepted changed fence %+v", next)
		}
	}
}
