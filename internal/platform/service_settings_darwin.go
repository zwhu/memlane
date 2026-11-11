//go:build darwin

package platform

import (
	"github.com/ebitengine/purego/objc"
	"runtime"
)

// Public ServiceManagement API from SMAppService.h (macOS 13+). Opening System
// Settings does not grant approval, register a helper, or start the service.
func OpenBackgroundServiceSettings() error {
	if err := loadServiceBridge(); err != nil {
		return err
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	objc.ID(objc.GetClass("SMAppService")).Send(objc.RegisterName("openSystemSettingsLoginItems"))
	return nil
}
