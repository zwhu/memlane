//go:build !darwin

package credentials

import "errors"

func (v *Vault) put(Reference, []byte) error { return errors.New("macOS credential vault required") }
func (v *Vault) get(Reference) ([]byte, error) {
	return nil, errors.New("macOS credential vault required")
}
func (v *Vault) delete(Reference) error { return errors.New("macOS credential vault required") }
