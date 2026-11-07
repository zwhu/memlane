//go:build darwin

package serviceipc

import (
	"errors"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

func SameUser(conn net.Conn) error {
	local, ok := conn.(*net.UnixConn)
	if !ok {
		return errors.New("Unix IPC required")
	}
	raw, err := local.SyscallConn()
	if err != nil {
		return err
	}
	var credentialErr error
	err = raw.Control(func(fd uintptr) {
		credentials, e := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if e != nil {
			credentialErr = e
			return
		}
		if credentials.Uid != uint32(os.Getuid()) {
			credentialErr = errors.New("IPC user mismatch")
		}
	})
	return errors.Join(err, credentialErr)
}
