package overview

import (
	"errors"
	"time"

	"github.com/zwhu/memlane/app/shell"
)

func NewFirstUseKey(clientState, config string, now time.Time) FirstUseKey {
	return FirstUseKey{clientState, config, now.Format("2006-01-02")}
}

func (a *ControllerOf[T]) firstUseKey(now time.Time) FirstUseKey {
	var clientState, config string
	if a.FirstUseFiles != nil {
		clientState, config = a.FirstUseFiles()
	}
	return NewFirstUseKey(clientState, config, now)
}

func (a *ControllerOf[T]) StartFirstUseDaysLoad(now time.Time) {
	if a.setupOnly() {
		return
	}
	v := a.FirstUse
	key := a.firstUseKey(now)
	if v.CachedKey == key || v.LoadingKey == key {
		return
	}
	if key.config == "" && key.clientState == "" {
		v.Request++
		v.LoadingKey = FirstUseKey{}
		v.CachedKey, v.cachedFirstUseDays = key, 0
		v.Err = errors.New("尚未完成初始化")
		return
	}
	if !a.canDispatch() {
		return
	}
	v.Request++
	req := v.Request
	fence, dispatcher := shell.CaptureFence(a.Fence), a.Dispatcher
	v.LoadingKey = key
	go func() {
		days, err := FirstUseDays(key.clientState, key.config, now)
		dispatcher.Dispatch(func() {
			if v.Request != req || v.LoadingKey != key {
				return
			}
			valid := fence.Valid(a.Fence)
			// Release the completed request slot even when its result is stale.
			v.LoadingKey = FirstUseKey{}
			if valid && a.firstUseKey(now) == key {
				v.CachedKey, v.cachedFirstUseDays, v.Err = key, days, err
			}
		})
	}()
}

func (a *ControllerOf[T]) FirstUseDays(now time.Time) (int, error, bool) {
	v := a.FirstUse
	key := a.firstUseKey(now)
	if v.CachedKey != key {
		a.StartFirstUseDaysLoad(now)
	}
	if v.CachedKey == key {
		return v.cachedFirstUseDays, v.Err, true
	}
	return 0, nil, false
}
