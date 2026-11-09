//go:build darwin

package credentials

import (
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

var keychain struct {
	once sync.Once
	err  error
	keys map[string]objc.ID
	add  func(objc.ID, *objc.ID) int32
	copy func(objc.ID, *objc.ID) int32
	del  func(objc.ID) int32
}

func loadKeychain() error {
	keychain.once.Do(func() {
		if _, keychain.err = purego.Dlopen("/System/Library/Frameworks/Foundation.framework/Foundation", purego.RTLD_GLOBAL|purego.RTLD_NOW); keychain.err != nil {
			return
		}
		var library uintptr
		library, keychain.err = purego.Dlopen("/System/Library/Frameworks/Security.framework/Security", purego.RTLD_GLOBAL|purego.RTLD_NOW)
		if keychain.err != nil {
			return
		}
		functions := []struct {
			name string
			fn   any
		}{{"SecItemAdd", &keychain.add}, {"SecItemCopyMatching", &keychain.copy}, {"SecItemDelete", &keychain.del}}
		for _, f := range functions {
			address, err := purego.Dlsym(library, f.name)
			if err != nil {
				keychain.err = err
				return
			}
			purego.RegisterFunc(f.fn, address)
		}
		keychain.keys = map[string]objc.ID{}
		for _, name := range []string{"kSecClass", "kSecClassGenericPassword", "kSecAttrService", "kSecAttrAccount", "kSecValueData", "kSecReturnData", "kSecMatchLimit", "kSecMatchLimitOne", "kSecAttrSynchronizable", "kSecAttrAccessible", "kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly", "kSecUseAuthenticationUI", "kSecUseAuthenticationUIFail"} {
			address, err := purego.Dlsym(library, name)
			if err != nil {
				keychain.err = err
				return
			}
			// dlsym returns the address of a CFStringRef variable. Copy its
			// pointer through NSData rather than converting an arbitrary
			// uintptr into a Go pointer or depending on literal key strings.
			var value objc.ID
			data := objc.ID(objc.GetClass("NSData")).Send(objc.RegisterName("alloc")).Send(objc.RegisterName("initWithBytes:length:"), address, unsafe.Sizeof(value))
			data.Send(objc.RegisterName("getBytes:length:"), uintptr(unsafe.Pointer(&value)), unsafe.Sizeof(value))
			data.Send(objc.RegisterName("release"))
			if value == 0 {
				keychain.err = errors.New("missing native credential attribute")
				return
			}
			keychain.keys[name] = value
		}
	})
	if keychain.err != nil {
		return errors.New("macOS credential vault unavailable")
	}
	return nil
}

func nativeString(value string) objc.ID {
	b := []byte(value)
	out := objc.ID(objc.GetClass("NSString")).Send(objc.RegisterName("alloc")).Send(objc.RegisterName("initWithBytes:length:encoding:"), uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)), uintptr(4))
	runtime.KeepAlive(b)
	return out
}

func (v *Vault) query(ref Reference) objc.ID {
	q := objc.ID(objc.GetClass("NSMutableDictionary")).Send(objc.RegisterName("dictionary"))
	set := objc.RegisterName("setObject:forKey:")
	q.Send(set, keychain.keys["kSecClassGenericPassword"], keychain.keys["kSecClass"])
	service := nativeString(v.namespace)
	defer service.Send(objc.RegisterName("release"))
	account := nativeString(ref.ID + ":" + strconv.FormatInt(ref.Revision, 10))
	defer account.Send(objc.RegisterName("release"))
	q.Send(set, service, keychain.keys["kSecAttrService"])
	q.Send(set, account, keychain.keys["kSecAttrAccount"])
	no := objc.ID(objc.GetClass("NSNumber")).Send(objc.RegisterName("numberWithBool:"), false)
	q.Send(set, no, keychain.keys["kSecAttrSynchronizable"])
	return q
}

func vaultError(status int32) error {
	if status == 0 {
		return nil
	}
	return fmt.Errorf("macOS credential vault status %d", status)
}

func (v *Vault) put(ref Reference, raw []byte) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := loadKeychain(); err != nil {
		return err
	}
	pool := objc.ID(objc.GetClass("NSAutoreleasePool")).Send(objc.RegisterName("new"))
	defer pool.Send(objc.RegisterName("drain"))
	q := v.query(ref)
	data := objc.ID(objc.GetClass("NSData")).Send(objc.RegisterName("dataWithBytes:length:"), uintptr(unsafe.Pointer(&raw[0])), uintptr(len(raw)))
	runtime.KeepAlive(raw)
	q.Send(objc.RegisterName("setObject:forKey:"), data, keychain.keys["kSecValueData"])
	q.Send(objc.RegisterName("setObject:forKey:"), keychain.keys["kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly"], keychain.keys["kSecAttrAccessible"])
	return vaultError(keychain.add(q, nil))
}

func (v *Vault) get(ref Reference) ([]byte, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := loadKeychain(); err != nil {
		return nil, err
	}
	pool := objc.ID(objc.GetClass("NSAutoreleasePool")).Send(objc.RegisterName("new"))
	defer pool.Send(objc.RegisterName("drain"))
	q := v.query(ref)
	set := objc.RegisterName("setObject:forKey:")
	yes := objc.ID(objc.GetClass("NSNumber")).Send(objc.RegisterName("numberWithBool:"), true)
	q.Send(set, yes, keychain.keys["kSecReturnData"])
	q.Send(set, keychain.keys["kSecMatchLimitOne"], keychain.keys["kSecMatchLimit"])
	q.Send(set, keychain.keys["kSecUseAuthenticationUIFail"], keychain.keys["kSecUseAuthenticationUI"])
	var result objc.ID
	if err := vaultError(keychain.copy(q, &result)); err != nil {
		return nil, err
	}
	if result == 0 {
		return nil, errors.New("credential vault returned no data")
	}
	defer result.Send(objc.RegisterName("release"))
	length := result.Send(objc.RegisterName("length"))
	if length < 1 || length > 16384 {
		return nil, errors.New("credential vault returned invalid data length")
	}
	raw := make([]byte, int(length))
	result.Send(objc.RegisterName("getBytes:length:"), uintptr(unsafe.Pointer(&raw[0])), uintptr(len(raw)))
	runtime.KeepAlive(raw)
	return raw, nil
}

func (v *Vault) delete(ref Reference) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := loadKeychain(); err != nil {
		return err
	}
	pool := objc.ID(objc.GetClass("NSAutoreleasePool")).Send(objc.RegisterName("new"))
	defer pool.Send(objc.RegisterName("drain"))
	return vaultError(keychain.del(v.query(ref)))
}
