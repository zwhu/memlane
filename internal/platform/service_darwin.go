//go:build darwin

// Package platform implements the selected macOS background service contract.
package platform

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

type ServiceState string

const (
	NotRegistered    ServiceState = "not_registered"
	Enabled          ServiceState = "enabled"
	RequiresApproval ServiceState = "requires_approval"
	NotFound         ServiceState = "not_found"
)

var bridge struct {
	once        sync.Once
	err         error
	callError   func(objc.ID, objc.SEL, *objc.ID) bool
	stringBytes func(objc.ID, objc.SEL) string
}

func loadServiceBridge() error {
	bridge.once.Do(func() {
		for _, path := range []string{"/System/Library/Frameworks/Foundation.framework/Foundation", "/System/Library/Frameworks/ServiceManagement.framework/ServiceManagement"} {
			if _, err := purego.Dlopen(path, purego.RTLD_GLOBAL|purego.RTLD_NOW); err != nil {
				bridge.err = err
				return
			}
		}
		lib, err := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_GLOBAL|purego.RTLD_NOW)
		if err != nil {
			bridge.err = err
			return
		}
		send, err := purego.Dlsym(lib, "objc_msgSend")
		if err != nil {
			bridge.err = err
			return
		}
		purego.RegisterFunc(&bridge.callError, send)
		purego.RegisterFunc(&bridge.stringBytes, send)
	})
	return bridge.err
}

func service(plist string) (objc.ID, func(), error) {
	if !strings.HasPrefix(plist, "io.github.zwhu.memlane.") || !strings.HasSuffix(plist, ".plist") || strings.ContainsAny(plist, "/\\") {
		return 0, nil, errors.New("invalid Memlane service plist")
	}
	if err := loadServiceBridge(); err != nil {
		return 0, nil, err
	}
	pool := objc.ID(objc.GetClass("NSAutoreleasePool")).Send(objc.RegisterName("new"))
	cleanup := func() { pool.Send(objc.RegisterName("drain")) }
	data := []byte(plist)
	name := objc.ID(objc.GetClass("NSString")).Send(objc.RegisterName("alloc")).Send(objc.RegisterName("initWithBytes:length:encoding:"), uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)), uintptr(4))
	runtime.KeepAlive(data)
	defer name.Send(objc.RegisterName("release"))
	agent := objc.ID(objc.GetClass("SMAppService")).Send(objc.RegisterName("agentServiceWithPlistName:"), name)
	if agent == 0 {
		cleanup()
		return 0, nil, errors.New("SMAppService returned no service")
	}
	return agent, cleanup, nil
}

func ServiceStatus(plist string) (ServiceState, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	agent, cleanup, err := service(plist)
	if err != nil {
		return "", err
	}
	defer cleanup()
	switch int(agent.Send(objc.RegisterName("status"))) {
	case 0:
		return NotRegistered, nil
	case 1:
		return Enabled, nil
	case 2:
		return RequiresApproval, nil
	case 3:
		return NotFound, nil
	default:
		return "", errors.New("unknown SMAppService status")
	}
}

func BundledServiceExists(plist string) (bool, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	_, cleanup, err := service(plist)
	if err != nil {
		return false, err
	}
	defer cleanup()
	bundle := objc.ID(objc.GetClass("NSBundle")).Send(objc.RegisterName("mainBundle"))
	path := bundle.Send(objc.RegisterName("bundlePath"))
	directory := bridge.stringBytes(path, objc.RegisterName("UTF8String"))
	info, err := os.Stat(filepath.Join(directory, "Contents/Library/LaunchAgents", plist))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return info.Mode().IsRegular(), nil
}

func changeService(plist, selector string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	agent, cleanup, err := service(plist)
	if err != nil {
		return err
	}
	defer cleanup()
	var nativeError objc.ID
	if bridge.callError(agent, objc.RegisterName(selector), &nativeError) {
		return nil
	}
	if nativeError == 0 {
		return errors.New("SMAppService action failed")
	}
	description := nativeError.Send(objc.RegisterName("localizedDescription"))
	return fmt.Errorf("SMAppService: %s", bridge.stringBytes(description, objc.RegisterName("UTF8String")))
}

func RegisterService(plist string) error   { return changeService(plist, "registerAndReturnError:") }
func UnregisterService(plist string) error { return changeService(plist, "unregisterAndReturnError:") }
